package nodecoord

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
	DefaultLeaseDuration = 180 * time.Second
	RenewInterval        = 60 * time.Second
	maxCoordinatedNodes  = 10_000
	maxEventIDs          = 10_000
	maxEventBytes        = 256 << 10
	eventVersion         = 1

	EventReplacement       = "replacement"
	EventMachineNodes      = "machine_nodes"
	EventDisconnectMachine = "disconnect_machine"
	EventNodeFull          = "node_full"
	EventNodeConfig        = "node_config"
	EventDeviceUsers       = "device_users"
	EventRefreshGroups     = "refresh_groups"
	EventDisconnectNodes   = "disconnect_nodes"
	EventDisconnectLegacy  = "disconnect_legacy"
	EventDisconnectAll     = "disconnect_all"
)

var (
	safePrefixRE   = regexp.MustCompile(`^[0-9A-Za-z:_-]{1,64}$`)
	unsafeRevision = regexp.MustCompile(`[^0-9A-Za-z_.-]+`)
)

const claimScript = `
for _, key in ipairs(KEYS) do
  redis.call('SET', key, ARGV[1], 'EX', ARGV[2])
end
redis.call('PUBLISH', ARGV[3], ARGV[4])
return #KEYS
`

const claimMachineNodesScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
redis.call('EXPIRE', KEYS[1], ARGV[2])
for index = 2, #KEYS do
  redis.call('SET', KEYS[index], ARGV[1], 'EX', ARGV[2])
end
if #KEYS > 1 then
  redis.call('PUBLISH', ARGV[3], ARGV[4])
end
return 1
`

const verifyScript = `
for _, key in ipairs(KEYS) do
  if redis.call('GET', key) ~= ARGV[1] then
    return 0
  end
end
return 1
`

const renewScript = `
for _, key in ipairs(KEYS) do
  if redis.call('GET', key) ~= ARGV[1] then
    return 0
  end
end
for _, key in ipairs(KEYS) do
  redis.call('EXPIRE', key, ARGV[2])
end
return 1
`

const releaseScript = `
if redis.call('GET', KEYS[1]) ~= ARGV[1] then
  return 0
end
return redis.call('DEL', KEYS[1])
`

const revokeMachineScript = `
redis.call('DEL', KEYS[1])
redis.call('PUBLISH', ARGV[1], ARGV[2])
return 1
`

type Options struct {
	URL           string
	Prefix        string
	Revision      string
	LeaseDuration time.Duration
	Logger        *slog.Logger
}

type Lease struct {
	MachineID    int64
	NodeIDs      []int64
	ConnectionID string
}

type NodeLease struct {
	NodeID       int64
	ConnectionID string
}

type Event struct {
	Version        int     `json:"version"`
	Kind           string  `json:"kind"`
	Source         string  `json:"source"`
	MachineID      int64   `json:"machine_id,omitempty"`
	NodeID         int64   `json:"node_id,omitempty"`
	NodeIDs        []int64 `json:"node_ids,omitempty"`
	UserIDs        []int64 `json:"user_ids,omitempty"`
	GroupIDs       []int64 `json:"group_ids,omitempty"`
	ConnectionID   string  `json:"connection_id,omitempty"`
	Reason         string  `json:"reason,omitempty"`
	DevicesCleared bool    `json:"devices_cleared,omitempty"`
}

type Coordinator interface {
	InstanceID() string
	NewConnectionID() (string, error)
	ClaimMachine(context.Context, int64, []int64, string) error
	ClaimNode(context.Context, int64, string) error
	ClaimMachineNodesIfOwned(context.Context, int64, []int64, string) (bool, error)
	OwnsMachine(context.Context, int64, string) (bool, error)
	OwnsMachineAndNodes(context.Context, int64, []int64, string) (bool, error)
	OwnsNode(context.Context, int64, string) (bool, error)
	Renew(context.Context, []Lease) ([]bool, error)
	RenewNodes(context.Context, []NodeLease) ([]bool, error)
	ReleaseMachineIfOwned(context.Context, int64, string) (bool, error)
	ReleaseNodeIfOwned(context.Context, int64, string) (bool, error)
	RevokeMachine(context.Context, int64, string) error
	Publish(context.Context, Event) error
	Start(context.Context, func(Event)) error
	Close() error
}

type RedisCoordinator struct {
	client       *redis.Client
	prefix       string
	revision     string
	instanceID   string
	leaseSeconds int64
	channel      string
	logger       *slog.Logger

	mu      sync.Mutex
	started bool
	pubsub  *redis.PubSub
	cancel  context.CancelFunc
}

func NewRedis(ctx context.Context, options Options) (*RedisCoordinator, error) {
	parsed, err := url.Parse(strings.TrimSpace(options.URL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "redis" && parsed.Scheme != "rediss") || parsed.Fragment != "" {
		return nil, errors.New("node coordination Redis URL must be an absolute redis or rediss URL without a fragment")
	}
	if !safePrefixRE.MatchString(options.Prefix) {
		return nil, errors.New("node coordination Redis prefix must contain 1 to 64 ASCII letters, digits, colons, underscores, or hyphens")
	}
	revision := sanitizeRevision(options.Revision)
	if revision == "" {
		return nil, errors.New("node coordination revision is required")
	}
	leaseDuration := options.LeaseDuration
	if leaseDuration == 0 {
		leaseDuration = DefaultLeaseDuration
	}
	if leaseDuration < 3*time.Second || leaseDuration > 24*time.Hour || leaseDuration%time.Second != 0 {
		return nil, errors.New("node coordination lease must be a whole number of seconds between 3 seconds and 24 hours")
	}
	redisOptions, err := redis.ParseURL(options.URL)
	if err != nil {
		return nil, errors.New("parse node coordination Redis URL: invalid Redis client option")
	}
	// Do not allow URL query options to remove the control plane's bounded I/O.
	redisOptions.DialTimeout = 3 * time.Second
	redisOptions.ReadTimeout = 2 * time.Second
	redisOptions.WriteTimeout = 2 * time.Second
	redisOptions.PoolTimeout = 3 * time.Second
	redisOptions.ConnMaxIdleTime = 5 * time.Minute
	redisOptions.MaxRetries = 1
	client := redis.NewClient(redisOptions)
	pingContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := client.Ping(pingContext).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("connect node coordination Redis: %w", err)
	}
	instanceID, err := randomID()
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("create node coordination instance ID: %w", err)
	}
	logger := options.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &RedisCoordinator{
		client: client, prefix: options.Prefix, revision: revision,
		instanceID: instanceID, leaseSeconds: int64(leaseDuration / time.Second),
		channel: options.Prefix + "{node-coordination}:events", logger: logger,
	}, nil
}

func (c *RedisCoordinator) InstanceID() string { return c.instanceID }

func (c *RedisCoordinator) NewConnectionID() (string, error) {
	random, err := randomID()
	if err != nil {
		return "", fmt.Errorf("generate random connection ID: %w", err)
	}
	return c.revision + ":" + random, nil
}

func (c *RedisCoordinator) ClaimMachine(ctx context.Context, machineID int64, nodeIDs []int64, connectionID string) error {
	normalized, err := validateLease(machineID, nodeIDs, connectionID, true)
	if err != nil {
		return err
	}
	event, err := c.marshalEvent(Event{
		Kind: EventReplacement, MachineID: machineID, NodeIDs: normalized, ConnectionID: connectionID,
	})
	if err != nil {
		return err
	}
	keys := c.machineAndNodeKeys(machineID, normalized)
	return c.client.Eval(ctx, claimScript, keys, connectionID, c.leaseSeconds, c.channel, event).Err()
}

func (c *RedisCoordinator) ClaimNode(ctx context.Context, nodeID int64, connectionID string) error {
	if nodeID < 1 || !validConnectionID(connectionID) {
		return errors.New("node ID and connection ID are required")
	}
	event, err := c.marshalEvent(Event{Kind: EventReplacement, NodeIDs: []int64{nodeID}, ConnectionID: connectionID})
	if err != nil {
		return err
	}
	return c.client.Eval(ctx, claimScript, []string{c.nodeKey(nodeID)}, connectionID, c.leaseSeconds, c.channel, event).Err()
}

func (c *RedisCoordinator) ClaimMachineNodesIfOwned(ctx context.Context, machineID int64, nodeIDs []int64, connectionID string) (bool, error) {
	normalized, err := validateLease(machineID, nodeIDs, connectionID, true)
	if err != nil {
		return false, err
	}
	event, err := c.marshalEvent(Event{
		Kind: EventReplacement, MachineID: machineID, NodeIDs: normalized, ConnectionID: connectionID,
	})
	if err != nil {
		return false, err
	}
	result, err := c.client.Eval(ctx, claimMachineNodesScript, c.machineAndNodeKeys(machineID, normalized), connectionID, c.leaseSeconds, c.channel, event).Int64()
	return result == 1, err
}

func (c *RedisCoordinator) OwnsMachine(ctx context.Context, machineID int64, connectionID string) (bool, error) {
	if err := validateIdentity(machineID, connectionID); err != nil {
		return false, err
	}
	result, err := c.client.Eval(ctx, verifyScript, []string{c.machineKey(machineID)}, connectionID).Int64()
	return result == 1, err
}

func (c *RedisCoordinator) OwnsMachineAndNodes(ctx context.Context, machineID int64, nodeIDs []int64, connectionID string) (bool, error) {
	if len(nodeIDs) == 0 {
		return false, nil
	}
	normalized, err := validateLease(machineID, nodeIDs, connectionID, false)
	if err != nil {
		return false, err
	}
	result, err := c.client.Eval(ctx, verifyScript, c.machineAndNodeKeys(machineID, normalized), connectionID).Int64()
	return result == 1, err
}

func (c *RedisCoordinator) OwnsNode(ctx context.Context, nodeID int64, connectionID string) (bool, error) {
	if nodeID < 1 || !validConnectionID(connectionID) {
		return false, errors.New("node ID and connection ID are required")
	}
	result, err := c.client.Eval(ctx, verifyScript, []string{c.nodeKey(nodeID)}, connectionID).Int64()
	return result == 1, err
}

func (c *RedisCoordinator) Renew(ctx context.Context, leases []Lease) ([]bool, error) {
	results := make([]bool, len(leases))
	if len(leases) == 0 {
		return results, nil
	}
	commands := make([]*redis.Cmd, len(leases))
	pipeline := c.client.Pipeline()
	for index, lease := range leases {
		nodeIDs, err := validateLease(lease.MachineID, lease.NodeIDs, lease.ConnectionID, true)
		if err != nil {
			return nil, fmt.Errorf("lease %d: %w", index, err)
		}
		commands[index] = pipeline.Eval(ctx, renewScript, c.machineAndNodeKeys(lease.MachineID, nodeIDs), lease.ConnectionID, c.leaseSeconds)
	}
	_, execErr := pipeline.Exec(ctx)
	for index, command := range commands {
		value, err := command.Int64()
		if err == nil {
			results[index] = value == 1
		}
	}
	return results, execErr
}

func (c *RedisCoordinator) RenewNodes(ctx context.Context, leases []NodeLease) ([]bool, error) {
	results := make([]bool, len(leases))
	if len(leases) == 0 {
		return results, nil
	}
	commands := make([]*redis.Cmd, len(leases))
	pipeline := c.client.Pipeline()
	for index, lease := range leases {
		if lease.NodeID < 1 || !validConnectionID(lease.ConnectionID) {
			return nil, fmt.Errorf("node lease %d is invalid", index)
		}
		commands[index] = pipeline.Eval(ctx, renewScript, []string{c.nodeKey(lease.NodeID)}, lease.ConnectionID, c.leaseSeconds)
	}
	_, execErr := pipeline.Exec(ctx)
	for index, command := range commands {
		value, err := command.Int64()
		if err == nil {
			results[index] = value == 1
		}
	}
	return results, execErr
}

func (c *RedisCoordinator) ReleaseMachineIfOwned(ctx context.Context, machineID int64, connectionID string) (bool, error) {
	if err := validateIdentity(machineID, connectionID); err != nil {
		return false, err
	}
	return c.release(ctx, c.machineKey(machineID), connectionID)
}

func (c *RedisCoordinator) ReleaseNodeIfOwned(ctx context.Context, nodeID int64, connectionID string) (bool, error) {
	if nodeID < 1 || !validConnectionID(connectionID) {
		return false, errors.New("node ID and connection ID are required")
	}
	return c.release(ctx, c.nodeKey(nodeID), connectionID)
}

func (c *RedisCoordinator) RevokeMachine(ctx context.Context, machineID int64, reason string) error {
	if machineID < 1 || len(reason) > 128 {
		return errors.New("machine ID or disconnect reason is invalid")
	}
	event, err := c.marshalEvent(Event{Kind: EventDisconnectMachine, MachineID: machineID, Reason: reason})
	if err != nil {
		return err
	}
	return c.client.Eval(ctx, revokeMachineScript, []string{c.machineKey(machineID)}, c.channel, event).Err()
}

func (c *RedisCoordinator) release(ctx context.Context, key, connectionID string) (bool, error) {
	result, err := c.client.Eval(ctx, releaseScript, []string{key}, connectionID).Int64()
	return result == 1, err
}

func (c *RedisCoordinator) Publish(ctx context.Context, event Event) error {
	payload, err := c.marshalEvent(event)
	if err != nil {
		return err
	}
	return c.client.Publish(ctx, c.channel, payload).Err()
}

func (c *RedisCoordinator) Start(ctx context.Context, handler func(Event)) error {
	if handler == nil {
		return errors.New("node coordination event handler is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started {
		return errors.New("node coordination subscriber is already started")
	}
	subscriptionContext, stop := context.WithCancel(ctx)
	pubsub := c.client.Subscribe(subscriptionContext, c.channel)
	readyContext, readyCancel := context.WithTimeout(subscriptionContext, 3*time.Second)
	defer readyCancel()
	if _, err := pubsub.Receive(readyContext); err != nil {
		stop()
		_ = pubsub.Close()
		return fmt.Errorf("subscribe node coordination events: %w", err)
	}
	c.pubsub = pubsub
	c.cancel = stop
	c.started = true
	go c.receive(subscriptionContext, pubsub, handler)
	return nil
}

func (c *RedisCoordinator) receive(ctx context.Context, pubsub *redis.PubSub, handler func(Event)) {
	delay := 100 * time.Millisecond
	for {
		message, err := pubsub.ReceiveMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			c.logger.Warn("receive node coordination event", "error", err)
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
			delay = min(delay*2, 5*time.Second)
			continue
		}
		delay = 100 * time.Millisecond
		event, err := decodeEvent(message.Payload)
		if err != nil {
			c.logger.Warn("reject node coordination event", "error", err)
			continue
		}
		handler(event)
	}
}

func (c *RedisCoordinator) Close() error {
	c.mu.Lock()
	pubsub := c.pubsub
	cancel := c.cancel
	c.pubsub = nil
	c.cancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	var pubsubErr error
	if pubsub != nil {
		pubsubErr = pubsub.Close()
	}
	return errors.Join(pubsubErr, c.client.Close())
}

func (c *RedisCoordinator) marshalEvent(event Event) (string, error) {
	event.Version = eventVersion
	event.Source = c.instanceID
	if err := validateEvent(event); err != nil {
		return "", err
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return "", fmt.Errorf("encode node coordination event: %w", err)
	}
	if len(payload) > maxEventBytes {
		return "", errors.New("node coordination event exceeds 256 KiB")
	}
	return string(payload), nil
}

func decodeEvent(payload string) (Event, error) {
	if len(payload) == 0 || len(payload) > maxEventBytes {
		return Event{}, errors.New("node coordination event size is invalid")
	}
	decoder := json.NewDecoder(io.LimitReader(bytes.NewBufferString(payload), maxEventBytes+1))
	decoder.DisallowUnknownFields()
	var event Event
	if err := decoder.Decode(&event); err != nil {
		return Event{}, errors.New("node coordination event JSON is invalid")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Event{}, errors.New("node coordination event contains trailing JSON")
	}
	if event.Version != eventVersion {
		return Event{}, errors.New("node coordination event version is unsupported")
	}
	if err := validateEvent(event); err != nil {
		return Event{}, err
	}
	return event, nil
}

func validateEvent(event Event) error {
	if event.Source == "" || len(event.Source) > 64 {
		return errors.New("node coordination event source is invalid")
	}
	if len(event.NodeIDs) > maxEventIDs || len(event.UserIDs) > maxEventIDs || len(event.GroupIDs) > maxEventIDs {
		return errors.New("node coordination event contains too many IDs")
	}
	for _, values := range [][]int64{event.NodeIDs, event.UserIDs, event.GroupIDs} {
		for _, value := range values {
			if value < 1 {
				return errors.New("node coordination event contains an invalid ID")
			}
		}
	}
	if event.MachineID < 0 || event.NodeID < 0 || len(event.Reason) > 128 || len(event.ConnectionID) > 256 {
		return errors.New("node coordination event field is invalid")
	}
	switch event.Kind {
	case EventReplacement:
		if event.ConnectionID == "" || event.MachineID == 0 && len(event.NodeIDs) == 0 {
			return errors.New("replacement event target is required")
		}
	case EventMachineNodes, EventDisconnectMachine:
		if event.MachineID < 1 {
			return errors.New("machine event target is required")
		}
	case EventNodeFull, EventNodeConfig:
		if event.NodeID < 1 {
			return errors.New("node event target is required")
		}
	case EventDeviceUsers:
		if len(event.UserIDs) == 0 {
			return errors.New("device event users are required")
		}
	case EventRefreshGroups:
		if len(event.GroupIDs) == 0 {
			return errors.New("group event targets are required")
		}
	case EventDisconnectNodes:
		if len(event.NodeIDs) == 0 {
			return errors.New("node disconnect targets are required")
		}
	case EventDisconnectLegacy, EventDisconnectAll:
		if event.MachineID != 0 || event.NodeID != 0 || len(event.NodeIDs) != 0 || event.ConnectionID != "" {
			return errors.New("global disconnect event must not contain a connection target")
		}
	default:
		return errors.New("node coordination event kind is unsupported")
	}
	return nil
}

func validateLease(machineID int64, nodeIDs []int64, connectionID string, allowEmpty bool) ([]int64, error) {
	if err := validateIdentity(machineID, connectionID); err != nil {
		return nil, err
	}
	if !allowEmpty && len(nodeIDs) == 0 {
		return nil, errors.New("at least one node ID is required")
	}
	if len(nodeIDs) > maxCoordinatedNodes {
		return nil, fmt.Errorf("a machine may coordinate at most %d nodes", maxCoordinatedNodes)
	}
	normalized := append([]int64(nil), nodeIDs...)
	sort.Slice(normalized, func(i, j int) bool { return normalized[i] < normalized[j] })
	write := 0
	for _, nodeID := range normalized {
		if nodeID < 1 {
			return nil, errors.New("node IDs must be positive")
		}
		if write == 0 || normalized[write-1] != nodeID {
			normalized[write] = nodeID
			write++
		}
	}
	return normalized[:write], nil
}

func validateIdentity(machineID int64, connectionID string) error {
	if machineID < 1 || !validConnectionID(connectionID) {
		return errors.New("machine ID and connection ID are required")
	}
	return nil
}

func validConnectionID(value string) bool {
	return value != "" && len(value) <= 256 && strings.TrimSpace(value) == value
}

func sanitizeRevision(value string) string {
	value = unsafeRevision.ReplaceAllString(strings.TrimSpace(value), "-")
	value = strings.Trim(value, "-")
	if len(value) > 128 {
		value = value[:128]
	}
	return value
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func (c *RedisCoordinator) machineAndNodeKeys(machineID int64, nodeIDs []int64) []string {
	keys := make([]string, 1, len(nodeIDs)+1)
	keys[0] = c.machineKey(machineID)
	for _, nodeID := range nodeIDs {
		keys = append(keys, c.nodeKey(nodeID))
	}
	return keys
}

func (c *RedisCoordinator) machineKey(machineID int64) string {
	return c.prefix + "{node-coordination}:machine:" + strconv.FormatInt(machineID, 10)
}

func (c *RedisCoordinator) nodeKey(nodeID int64) string {
	return c.prefix + "{node-coordination}:node:" + strconv.FormatInt(nodeID, 10)
}
