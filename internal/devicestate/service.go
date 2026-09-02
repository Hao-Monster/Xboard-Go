package devicestate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	OnlineWindow          = 300 * time.Second
	DatabaseThrottle      = 10 * time.Second
	DefaultFlushInterval  = 5 * time.Second
	DefaultFlushLimit     = 500
	MaximumFlushLimit     = 5_000
	MaximumDevicesPerUser = 64

	redisOperationBatch = 500
	maximumIndexMembers = 1_000_000
	stateRetention      = OnlineWindow + time.Minute
	indexRetention      = 2 * stateRetention
)

var safePrefixRE = regexp.MustCompile(`^[0-9A-Za-z:_-]{1,64}$`)

const replaceNodeDevicesScript = `
if redis.call('GET', KEYS[5]) ~= ARGV[6] then
  return -1
end

local removed = 0
for _, field in ipairs(redis.call('SMEMBERS', KEYS[4])) do
  removed = removed + redis.call('HDEL', KEYS[1], field)
end
redis.call('DEL', KEYS[4])

if #ARGV > 6 then
  for index = 7, #ARGV do
    local field = ARGV[1] .. ARGV[index]
    redis.call('HSET', KEYS[1], field, ARGV[2])
    redis.call('SADD', KEYS[4], field)
  end
  redis.call('EXPIRE', KEYS[1], ARGV[3])
  redis.call('EXPIRE', KEYS[4], ARGV[3])
  redis.call('SADD', KEYS[2], ARGV[4])
  redis.call('EXPIRE', KEYS[2], ARGV[5])
  redis.call('SADD', KEYS[3], ARGV[1])
  redis.call('EXPIRE', KEYS[3], ARGV[5])
else
  redis.call('SREM', KEYS[2], ARGV[4])
  redis.call('SREM', KEYS[3], ARGV[1])
  if redis.call('SCARD', KEYS[2]) == 0 then redis.call('DEL', KEYS[2]) end
  if redis.call('SCARD', KEYS[3]) == 0 then redis.call('DEL', KEYS[3]) end
end

return removed
`

const bumpNodeGenerationScript = `
local generation = redis.call('INCR', KEYS[1])
redis.call('EXPIRE', KEYS[1], ARGV[1])
return generation
`

const removeDuePendingScript = `
local score = redis.call('ZSCORE', KEYS[1], ARGV[1])
if score and tonumber(score) <= tonumber(ARGV[2]) then
  return redis.call('ZREM', KEYS[1], ARGV[1])
end
return 0
`

type Summary struct {
	UserID      int64
	OnlineCount int
	ObservedAt  time.Time
}

type SummaryWriter func(context.Context, []Summary) error

type Options struct {
	URL              string
	Prefix           string
	WriteSummaries   SummaryWriter
	Logger           *slog.Logger
	DatabaseThrottle time.Duration
	FlushInterval    time.Duration
}

type Service interface {
	ReplaceNodeDevices(context.Context, int64, map[int64][]string, bool, time.Time) ([]int64, error)
	ListUserDevices(context.Context, []int64, time.Time) (map[int64][]string, error)
	ClearNodeDevices(context.Context, []int64, time.Time) ([]int64, error)
	ClearUserDevices(context.Context, []int64, time.Time) ([]int64, error)
	FlushPending(context.Context, time.Time, int) (int, error)
	Run(context.Context)
	Close() error
}

type RedisService struct {
	client           *redis.Client
	prefix           string
	writeSummaries   SummaryWriter
	logger           *slog.Logger
	databaseThrottle time.Duration
	flushInterval    time.Duration
	closeOnce        sync.Once
	closeErr         error
}

func NewRedis(ctx context.Context, options Options) (*RedisService, error) {
	parsed, err := url.Parse(strings.TrimSpace(options.URL))
	if err != nil || parsed.Host == "" || parsed.Fragment != "" || parsed.Scheme != "redis" && parsed.Scheme != "rediss" {
		return nil, errors.New("device state Redis URL must be an absolute redis or rediss URL without a fragment")
	}
	if !safePrefixRE.MatchString(options.Prefix) {
		return nil, errors.New("device state Redis prefix must contain 1 to 64 ASCII letters, digits, colons, underscores, or hyphens")
	}
	if options.WriteSummaries == nil {
		return nil, errors.New("device state summary writer is required")
	}
	throttle := options.DatabaseThrottle
	if throttle == 0 {
		throttle = DatabaseThrottle
	}
	if throttle < 10*time.Millisecond || throttle > time.Minute {
		return nil, errors.New("device state database throttle must be between 10 milliseconds and 1 minute")
	}
	flushInterval := options.FlushInterval
	if flushInterval == 0 {
		flushInterval = DefaultFlushInterval
	}
	if flushInterval < 10*time.Millisecond || flushInterval > time.Hour {
		return nil, errors.New("device state flush interval must be between 10 milliseconds and 1 hour")
	}
	redisOptions, err := redis.ParseURL(options.URL)
	if err != nil {
		return nil, errors.New("parse device state Redis URL: invalid Redis client option")
	}
	redisOptions.DialTimeout = 3 * time.Second
	redisOptions.ReadTimeout = 2 * time.Second
	redisOptions.WriteTimeout = 2 * time.Second
	redisOptions.PoolTimeout = 3 * time.Second
	redisOptions.ConnMaxIdleTime = 5 * time.Minute
	client := redis.NewClient(redisOptions)
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect device state Redis: %w", err)
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisService{
		client: client, prefix: options.Prefix, writeSummaries: options.WriteSummaries,
		logger: logger, databaseThrottle: throttle, flushInterval: flushInterval,
	}, nil
}

func (service *RedisService) ReplaceNodeDevices(ctx context.Context, nodeID int64, devices map[int64][]string, replaceAll bool, now time.Time) ([]int64, error) {
	if nodeID < 1 || now.Unix() < 0 || len(devices) > maximumIndexMembers {
		return nil, errors.New("invalid device snapshot")
	}
	normalized := make(map[int64][]string, len(devices))
	affected := make(map[int64]struct{}, len(devices))
	for userID, ips := range devices {
		if userID < 1 {
			return nil, errors.New("device snapshot contains an invalid user")
		}
		normalized[userID] = normalizeIPs(ips)
		affected[userID] = struct{}{}
	}
	generation, err := service.bumpNodeGeneration(ctx, nodeID)
	if err != nil {
		return nil, fmt.Errorf("begin node device replacement: %w", err)
	}
	if replaceAll {
		oldUsers, err := service.scanSetIDs(ctx, service.nodeUsersKey(nodeID))
		if err != nil {
			return nil, fmt.Errorf("list replaced node users: %w", err)
		}
		for _, userID := range oldUsers {
			if _, exists := normalized[userID]; !exists {
				normalized[userID] = nil
				affected[userID] = struct{}{}
			}
		}
	}
	userIDs := sortedIDSet(affected)
	for start := 0; start < len(userIDs); start += redisOperationBatch {
		end := min(start+redisOperationBatch, len(userIDs))
		commands := make([]*redis.Cmd, end-start)
		_, err := service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, userID := range userIDs[start:end] {
				commands[index] = service.replaceNodeUser(pipe, ctx, nodeID, userID, normalized[userID], now, generation)
			}
			return nil
		})
		if err != nil {
			return userIDs, fmt.Errorf("replace node device state: %w", err)
		}
		for _, command := range commands {
			changed, commandErr := command.Int64()
			if commandErr != nil {
				return userIDs, fmt.Errorf("read node device replacement: %w", commandErr)
			}
			if changed < 0 {
				return userIDs, errors.New("node device snapshot was superseded")
			}
		}
	}
	if len(userIDs) > 0 {
		if err := service.notifyUsers(ctx, userIDs, now); err != nil {
			return userIDs, fmt.Errorf("persist node device summaries: %w", err)
		}
	}
	return userIDs, nil
}

func (service *RedisService) ListUserDevices(ctx context.Context, userIDs []int64, now time.Time) (map[int64][]string, error) {
	ids, err := normalizeIDs(userIDs)
	if err != nil || now.Unix() < 0 {
		return nil, errors.New("invalid device-state query")
	}
	result := make(map[int64][]string)
	cutoff := now.Add(-OnlineWindow).Unix()
	for start := 0; start < len(ids); start += redisOperationBatch {
		end := min(start+redisOperationBatch, len(ids))
		commands := make([]*redis.MapStringStringCmd, end-start)
		_, pipelineErr := service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, userID := range ids[start:end] {
				commands[index] = pipe.HGetAll(ctx, service.userDevicesKey(userID))
			}
			return nil
		})
		if pipelineErr != nil {
			return nil, fmt.Errorf("read user device states: %w", pipelineErr)
		}
		for index, command := range commands {
			userID := ids[start+index]
			fields, commandErr := command.Result()
			if commandErr != nil {
				return nil, fmt.Errorf("read user %d device state: %w", userID, commandErr)
			}
			ips := make(map[string]struct{}, len(fields))
			staleFields := make([]string, 0)
			activeNodes := make(map[int64]struct{})
			seenNodes := make(map[int64]struct{})
			for field, rawTimestamp := range fields {
				rawNodeID, rawIP, valid := strings.Cut(field, ":")
				nodeID, nodeErr := strconv.ParseInt(rawNodeID, 10, 64)
				timestamp, timestampErr := strconv.ParseInt(rawTimestamp, 10, 64)
				ip, ipErr := normalizeIP(rawIP)
				if !valid || nodeErr != nil || nodeID < 1 || timestampErr != nil || ipErr != nil || timestamp <= cutoff {
					staleFields = append(staleFields, field)
					if nodeErr == nil && nodeID > 0 {
						seenNodes[nodeID] = struct{}{}
					}
					continue
				}
				seenNodes[nodeID] = struct{}{}
				activeNodes[nodeID] = struct{}{}
				ips[ip] = struct{}{}
			}
			if len(staleFields) > 0 {
				if err := service.cleanupStaleFields(ctx, userID, staleFields, seenNodes, activeNodes); err != nil {
					return nil, err
				}
			}
			if len(ips) > 0 {
				values := make([]string, 0, len(ips))
				for ip := range ips {
					values = append(values, ip)
				}
				sort.Strings(values)
				result[userID] = values
			}
		}
	}
	return result, nil
}

func (service *RedisService) ClearNodeDevices(ctx context.Context, nodeIDs []int64, now time.Time) ([]int64, error) {
	ids, err := normalizeIDs(nodeIDs)
	if err != nil || now.Unix() < 0 {
		return nil, errors.New("invalid node device-state clear")
	}
	affected := make(map[int64]struct{})
	for _, nodeID := range ids {
		generation, generationErr := service.bumpNodeGeneration(ctx, nodeID)
		if generationErr != nil {
			return sortedIDSet(affected), fmt.Errorf("begin node %d device clear: %w", nodeID, generationErr)
		}
		userIDs, scanErr := service.scanSetIDs(ctx, service.nodeUsersKey(nodeID))
		if scanErr != nil {
			return sortedIDSet(affected), fmt.Errorf("list node %d device users: %w", nodeID, scanErr)
		}
		superseded := false
		for start := 0; start < len(userIDs) && !superseded; start += redisOperationBatch {
			end := min(start+redisOperationBatch, len(userIDs))
			commands := make([]*redis.Cmd, end-start)
			_, pipelineErr := service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
				for index, userID := range userIDs[start:end] {
					commands[index] = service.replaceNodeUser(pipe, ctx, nodeID, userID, nil, now, generation)
				}
				return nil
			})
			if pipelineErr != nil {
				return sortedIDSet(affected), fmt.Errorf("clear node %d device users: %w", nodeID, pipelineErr)
			}
			for index, command := range commands {
				removed, commandErr := command.Int64()
				if commandErr != nil {
					return sortedIDSet(affected), fmt.Errorf("clear node %d device state: %w", nodeID, commandErr)
				}
				if removed < 0 {
					superseded = true
					break
				}
				if removed > 0 {
					affected[userIDs[start+index]] = struct{}{}
				}
			}
		}
		if superseded {
			continue
		}
		if err := service.client.Del(ctx, service.nodeUsersKey(nodeID)).Err(); err != nil {
			return sortedIDSet(affected), fmt.Errorf("remove node %d device index: %w", nodeID, err)
		}
	}
	userIDs := sortedIDSet(affected)
	if len(userIDs) > 0 {
		if err := service.notifyUsers(ctx, userIDs, now); err != nil {
			return userIDs, fmt.Errorf("persist cleared node summaries: %w", err)
		}
	}
	return userIDs, nil
}

func (service *RedisService) ClearUserDevices(ctx context.Context, userIDs []int64, now time.Time) ([]int64, error) {
	ids, err := normalizeIDs(userIDs)
	if err != nil || now.Unix() < 0 {
		return nil, errors.New("invalid user device-state clear")
	}
	for start := 0; start < len(ids); start += redisOperationBatch {
		end := min(start+redisOperationBatch, len(ids))
		nodeCommands := make([]*redis.StringSliceCmd, end-start)
		_, pipelineErr := service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, userID := range ids[start:end] {
				nodeCommands[index] = pipe.SMembers(ctx, service.userNodesKey(userID))
			}
			return nil
		})
		if pipelineErr != nil {
			return nil, fmt.Errorf("read user device indexes: %w", pipelineErr)
		}
		_, pipelineErr = service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, userID := range ids[start:end] {
				nodes, commandErr := nodeCommands[index].Result()
				if commandErr != nil {
					return commandErr
				}
				for _, rawNodeID := range nodes {
					rawNodeID = strings.TrimSuffix(rawNodeID, ":")
					nodeID, parseErr := strconv.ParseInt(rawNodeID, 10, 64)
					if parseErr == nil && nodeID > 0 {
						pipe.SRem(ctx, service.nodeUsersKey(nodeID), userID)
						pipe.Del(ctx, service.userNodeFieldsKey(userID, nodeID))
					}
				}
				pipe.Del(ctx, service.userDevicesKey(userID), service.userNodesKey(userID))
			}
			return nil
		})
		if pipelineErr != nil {
			return nil, fmt.Errorf("clear user device states: %w", pipelineErr)
		}
	}
	if len(ids) > 0 {
		if err := service.notifyUsers(ctx, ids, now); err != nil {
			return ids, fmt.Errorf("persist cleared user summaries: %w", err)
		}
	}
	return ids, nil
}

func (service *RedisService) FlushPending(ctx context.Context, now time.Time, limit int) (int, error) {
	if now.Unix() < 0 {
		return 0, errors.New("invalid pending flush time")
	}
	limit = max(1, min(limit, MaximumFlushLimit))
	dueAt := now.UnixMilli()
	rawIDs, err := service.client.ZRangeByScore(ctx, service.pendingKey(), &redis.ZRangeBy{
		Min: "-inf", Max: strconv.FormatInt(dueAt, 10), Offset: 0, Count: int64(limit),
	}).Result()
	if err != nil {
		return 0, fmt.Errorf("list pending device summaries: %w", err)
	}
	userIDs := make([]int64, 0, len(rawIDs))
	invalid := make([]any, 0)
	for _, rawID := range rawIDs {
		userID, parseErr := strconv.ParseInt(rawID, 10, 64)
		if parseErr != nil || userID < 1 {
			invalid = append(invalid, rawID)
			continue
		}
		userIDs = append(userIDs, userID)
	}
	if len(invalid) > 0 {
		if err := service.client.ZRem(ctx, service.pendingKey(), invalid...).Err(); err != nil {
			return 0, fmt.Errorf("remove invalid pending device summaries: %w", err)
		}
	}
	if len(userIDs) == 0 {
		return 0, nil
	}
	if err := service.notifyUsers(ctx, userIDs, now); err != nil {
		return 0, fmt.Errorf("flush pending device summaries: %w", err)
	}
	_, err = service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, userID := range userIDs {
			pipe.Eval(ctx, removeDuePendingScript, []string{service.pendingKey()}, strconv.FormatInt(userID, 10), dueAt)
		}
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("acknowledge pending device summaries: %w", err)
	}
	return len(userIDs), nil
}

func (service *RedisService) Run(ctx context.Context) {
	ticker := time.NewTicker(service.flushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if _, err := service.FlushPending(ctx, now.UTC(), DefaultFlushLimit); err != nil && !errors.Is(err, context.Canceled) {
				service.logger.Warn("flush pending device summaries", "error", err)
			}
		}
	}
}

func (service *RedisService) Close() error {
	service.closeOnce.Do(func() {
		service.closeErr = service.client.Close()
	})
	return service.closeErr
}

func (service *RedisService) bumpNodeGeneration(ctx context.Context, nodeID int64) (int64, error) {
	return service.client.Eval(ctx, bumpNodeGenerationScript, []string{service.nodeGenerationKey(nodeID)}, int64(indexRetention/time.Second)).Int64()
}

func (service *RedisService) replaceNodeUser(pipe redis.Pipeliner, ctx context.Context, nodeID, userID int64, ips []string, now time.Time, generation int64) *redis.Cmd {
	nodePrefix := strconv.FormatInt(nodeID, 10) + ":"
	args := make([]any, 0, 6+len(ips))
	args = append(args, nodePrefix, now.Unix(), int64(stateRetention/time.Second), userID, int64(indexRetention/time.Second), generation)
	for _, ip := range ips {
		args = append(args, ip)
	}
	return pipe.Eval(ctx, replaceNodeDevicesScript, []string{
		service.userDevicesKey(userID), service.nodeUsersKey(nodeID), service.userNodesKey(userID), service.userNodeFieldsKey(userID, nodeID),
		service.nodeGenerationKey(nodeID),
	}, args...)
}

func (service *RedisService) notifyUsers(ctx context.Context, userIDs []int64, now time.Time) error {
	ids, err := normalizeIDs(userIDs)
	if err != nil {
		return err
	}
	for start := 0; start < len(ids); start += redisOperationBatch {
		end := min(start+redisOperationBatch, len(ids))
		batch := ids[start:end]
		commands := make([]*redis.BoolCmd, len(batch))
		_, pipelineErr := service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for index, userID := range batch {
				commands[index] = pipe.SetNX(ctx, service.throttleKey(userID), "1", service.databaseThrottle)
			}
			return nil
		})
		if pipelineErr != nil {
			_ = service.schedulePending(ctx, batch, now.Add(service.databaseThrottle))
			return fmt.Errorf("reserve device summary writes: %w", pipelineErr)
		}
		immediate := make([]int64, 0, len(batch))
		deferred := make([]int64, 0, len(batch))
		for index, command := range commands {
			acquired, commandErr := command.Result()
			if commandErr != nil {
				_ = service.schedulePending(ctx, batch, now.Add(service.databaseThrottle))
				return fmt.Errorf("read device summary throttle: %w", commandErr)
			}
			if acquired {
				immediate = append(immediate, batch[index])
			} else {
				deferred = append(deferred, batch[index])
			}
		}
		if len(deferred) > 0 {
			if err := service.schedulePending(ctx, deferred, now.Add(service.databaseThrottle)); err != nil {
				return err
			}
		}
		if len(immediate) == 0 {
			continue
		}
		devices, err := service.ListUserDevices(ctx, immediate, now)
		if err != nil {
			_ = service.schedulePending(ctx, immediate, now.Add(service.databaseThrottle))
			return err
		}
		summaries := make([]Summary, len(immediate))
		for index, userID := range immediate {
			summaries[index] = Summary{UserID: userID, OnlineCount: len(devices[userID]), ObservedAt: now}
		}
		if err := service.writeSummaries(ctx, summaries); err != nil {
			pendingErr := service.schedulePending(ctx, immediate, now.Add(service.databaseThrottle))
			return errors.Join(err, pendingErr)
		}
	}
	return nil
}

func (service *RedisService) schedulePending(ctx context.Context, userIDs []int64, due time.Time) error {
	for start := 0; start < len(userIDs); start += redisOperationBatch {
		end := min(start+redisOperationBatch, len(userIDs))
		values := make([]redis.Z, 0, end-start)
		for _, userID := range userIDs[start:end] {
			values = append(values, redis.Z{Score: float64(due.UnixMilli()), Member: strconv.FormatInt(userID, 10)})
		}
		if err := service.client.ZAdd(ctx, service.pendingKey(), values...).Err(); err != nil {
			return fmt.Errorf("schedule pending device summaries: %w", err)
		}
	}
	return nil
}

func (service *RedisService) cleanupStaleFields(ctx context.Context, userID int64, stale []string, seenNodes, activeNodes map[int64]struct{}) error {
	_, err := service.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HDel(ctx, service.userDevicesKey(userID), stale...)
		for _, field := range stale {
			rawNodeID, _, valid := strings.Cut(field, ":")
			nodeID, parseErr := strconv.ParseInt(rawNodeID, 10, 64)
			if valid && parseErr == nil && nodeID > 0 {
				pipe.SRem(ctx, service.userNodeFieldsKey(userID, nodeID), field)
			}
		}
		for nodeID := range seenNodes {
			if _, active := activeNodes[nodeID]; active {
				continue
			}
			pipe.SRem(ctx, service.nodeUsersKey(nodeID), userID)
			pipe.SRem(ctx, service.userNodesKey(userID), strconv.FormatInt(nodeID, 10)+":")
			pipe.Del(ctx, service.userNodeFieldsKey(userID, nodeID))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("clean expired user device states: %w", err)
	}
	return nil
}

func (service *RedisService) scanSetIDs(ctx context.Context, key string) ([]int64, error) {
	var cursor uint64
	unique := make(map[int64]struct{})
	invalid := make([]any, 0)
	for {
		members, next, err := service.client.SScan(ctx, key, cursor, "*", redisOperationBatch).Result()
		if err != nil {
			return nil, err
		}
		for _, rawMember := range members {
			member := strings.TrimSuffix(rawMember, ":")
			value, parseErr := strconv.ParseInt(member, 10, 64)
			if parseErr != nil || value < 1 {
				invalid = append(invalid, rawMember)
				continue
			}
			unique[value] = struct{}{}
			if len(unique) > maximumIndexMembers {
				return nil, errors.New("device-state index exceeds its bounded capacity")
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	if len(invalid) > 0 {
		if err := service.client.SRem(ctx, key, invalid...).Err(); err != nil {
			return nil, err
		}
	}
	return sortedIDSet(unique), nil
}

func normalizeIPs(values []string) []string {
	result := make([]string, 0, min(len(values), MaximumDevicesPerUser))
	seen := make(map[string]struct{}, min(len(values), MaximumDevicesPerUser))
	for _, value := range values {
		ip, err := normalizeIP(value)
		if err != nil {
			continue
		}
		if _, exists := seen[ip]; exists {
			continue
		}
		seen[ip] = struct{}{}
		result = append(result, ip)
		if len(result) == MaximumDevicesPerUser {
			break
		}
	}
	return result
}

func normalizeIP(value string) (string, error) {
	value = strings.TrimSpace(value)
	if address, err := netip.ParseAddr(value); err == nil && address.Zone() == "" {
		return address.Unmap().String(), nil
	}
	if host, _, err := net.SplitHostPort(value); err == nil {
		if address, err := netip.ParseAddr(host); err == nil && address.Zone() == "" {
			return address.Unmap().String(), nil
		}
	}
	return "", errors.New("invalid IP address")
}

func normalizeIDs(values []int64) ([]int64, error) {
	if len(values) > maximumIndexMembers {
		return nil, errors.New("device-state identifier list exceeds its bounded capacity")
	}
	unique := make(map[int64]struct{}, len(values))
	for _, value := range values {
		if value < 1 {
			return nil, errors.New("device-state identifier must be positive")
		}
		unique[value] = struct{}{}
	}
	return sortedIDSet(unique), nil
}

func sortedIDSet(values map[int64]struct{}) []int64 {
	result := make([]int64, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func (service *RedisService) userDevicesKey(userID int64) string {
	return service.prefix + "device:user:" + strconv.FormatInt(userID, 10)
}

func (service *RedisService) userNodesKey(userID int64) string {
	return service.prefix + "device:user-nodes:" + strconv.FormatInt(userID, 10)
}

func (service *RedisService) userNodeFieldsKey(userID, nodeID int64) string {
	return service.prefix + "device:user-node-fields:" + strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(nodeID, 10)
}

func (service *RedisService) nodeUsersKey(nodeID int64) string {
	return service.prefix + "device:node-users:" + strconv.FormatInt(nodeID, 10)
}

func (service *RedisService) nodeGenerationKey(nodeID int64) string {
	return service.prefix + "device:node-generation:" + strconv.FormatInt(nodeID, 10)
}

func (service *RedisService) throttleKey(userID int64) string {
	return service.prefix + "device:db-throttle:" + strconv.FormatInt(userID, 10)
}

func (service *RedisService) pendingKey() string {
	return service.prefix + "device:db-pending"
}
