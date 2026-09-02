package devicestate

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type recordingSummaryWriter struct {
	mu         sync.Mutex
	attempts   int
	successes  int
	fail       bool
	summaries  map[int64]Summary
	afterWrite func()
}

func (writer *recordingSummaryWriter) write(_ context.Context, summaries []Summary) error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	writer.attempts++
	if writer.fail {
		return errors.New("injected summary write failure")
	}
	if writer.summaries == nil {
		writer.summaries = make(map[int64]Summary)
	}
	for _, summary := range summaries {
		writer.summaries[summary.UserID] = summary
	}
	writer.successes++
	if writer.afterWrite != nil {
		writer.afterWrite()
	}
	return nil
}

func (writer *recordingSummaryWriter) counts() (int, int) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.attempts, writer.successes
}

func (writer *recordingSummaryWriter) setFailure(fail bool) {
	writer.mu.Lock()
	writer.fail = fail
	writer.mu.Unlock()
}

func newTIMENODE006RedisService(t *testing.T, writer *recordingSummaryWriter, throttle time.Duration) *RedisService {
	t.Helper()
	rawURL := os.Getenv("XBOARD_TEST_REDIS_URL")
	if rawURL == "" {
		t.Skip("XBOARD_TEST_REDIS_URL is required for Redis device-state integration tests")
	}
	prefix := "xboard-go:test-device:" + uuid.NewString() + ":"
	service, err := NewRedis(context.Background(), Options{
		URL: rawURL, Prefix: prefix, WriteSummaries: writer.write, DatabaseThrottle: throttle,
	})
	if err != nil {
		t.Fatalf("NewRedis() error = %v", err)
	}
	t.Cleanup(func() {
		_ = service.Close()
		options, parseErr := redis.ParseURL(rawURL)
		if parseErr != nil {
			return
		}
		client := redis.NewClient(options)
		defer client.Close()
		var cursor uint64
		for {
			keys, next, scanErr := client.Scan(context.Background(), cursor, prefix+"*", 500).Result()
			if scanErr != nil {
				return
			}
			if len(keys) > 0 {
				_ = client.Del(context.Background(), keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				return
			}
		}
	})
	return service
}

func TestTIMENODE006RedisWindowNormalizationDeduplicationAndCapacity(t *testing.T) {
	writer := &recordingSummaryWriter{}
	service := newTIMENODE006RedisService(t, writer, 100*time.Millisecond)
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)

	if users, err := service.ReplaceNodeDevices(ctx, 11, map[int64][]string{
		7: {"192.0.2.1:443", "192.0.2.1", "[2001:db8::1]:8443", "invalid"},
	}, false, base); err != nil || !reflect.DeepEqual(users, []int64{7}) {
		t.Fatalf("ReplaceNodeDevices(node 11) = (%v, %v)", users, err)
	}
	if _, err := service.ReplaceNodeDevices(ctx, 12, map[int64][]string{
		7: {"192.0.2.1", "198.51.100.9:1234"},
	}, false, base.Add(time.Second)); err != nil {
		t.Fatalf("ReplaceNodeDevices(node 12) error = %v", err)
	}

	devices, err := service.ListUserDevices(ctx, []int64{7}, base.Add(299*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"192.0.2.1", "198.51.100.9", "2001:db8::1"}
	if !reflect.DeepEqual(devices[7], want) {
		t.Fatalf("devices at 299 seconds = %#v, want %#v", devices[7], want)
	}
	devices, err = service.ListUserDevices(ctx, []int64{7}, base.Add(300*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	want = []string{"192.0.2.1", "198.51.100.9"}
	if !reflect.DeepEqual(devices[7], want) {
		t.Fatalf("devices at 300 seconds = %#v, want %#v", devices[7], want)
	}
	devices, err = service.ListUserDevices(ctx, []int64{7}, base.Add(301*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(devices[7]) != 0 {
		t.Fatalf("devices at 301 seconds = %#v, want empty", devices[7])
	}

	ips := make([]string, 0, MaximumDevicesPerUser+1)
	for index := 1; index <= MaximumDevicesPerUser+1; index++ {
		ips = append(ips, fmt.Sprintf("2001:db8::%x", index))
	}
	if _, err := service.ReplaceNodeDevices(ctx, 13, map[int64][]string{8: ips}, false, base); err != nil {
		t.Fatalf("ReplaceNodeDevices(capacity) error = %v", err)
	}
	devices, err = service.ListUserDevices(ctx, []int64{8}, base)
	if err != nil {
		t.Fatal(err)
	}
	if len(devices[8]) != MaximumDevicesPerUser {
		t.Fatalf("capacity devices = %d, want %d", len(devices[8]), MaximumDevicesPerUser)
	}
}

func TestTIMENODE006RedisDatabaseThrottleAndTrailingFlush(t *testing.T) {
	writer := &recordingSummaryWriter{}
	throttle := 80 * time.Millisecond
	service := newTIMENODE006RedisService(t, writer, throttle)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := service.ReplaceNodeDevices(ctx, 21, map[int64][]string{9: {"192.0.2.9"}}, false, now); err != nil {
		t.Fatal(err)
	}
	if attempts, successes := writer.counts(); attempts != 1 || successes != 1 {
		t.Fatalf("first update writes = (%d, %d), want (1, 1)", attempts, successes)
	}
	if _, err := service.ReplaceNodeDevices(ctx, 21, map[int64][]string{9: {"192.0.2.9", "192.0.2.10"}}, false, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if attempts, _ := writer.counts(); attempts != 1 {
		t.Fatalf("throttled update attempts = %d, want 1", attempts)
	}
	if flushed, err := service.FlushPending(ctx, now.Add(throttle-time.Millisecond), DefaultFlushLimit); err != nil || flushed != 0 {
		t.Fatalf("early FlushPending() = (%d, %v), want (0, nil)", flushed, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, err := service.FlushPending(ctx, time.Now().UTC(), DefaultFlushLimit)
		if err != nil {
			t.Fatal(err)
		}
		_, successes := writer.counts()
		if successes == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pending device summary did not become due")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if attempts, successes := writer.counts(); attempts != 2 || successes != 2 {
		t.Fatalf("trailing writes = (%d, %d), want (2, 2)", attempts, successes)
	}
	writer.mu.Lock()
	summary := writer.summaries[9]
	writer.mu.Unlock()
	if summary.OnlineCount != 2 {
		t.Fatalf("trailing online count = %d, want 2", summary.OnlineCount)
	}
}

func TestTIMENODE006RedisFailedSummaryRemainsPendingAndRecovers(t *testing.T) {
	writer := &recordingSummaryWriter{fail: true}
	throttle := 60 * time.Millisecond
	service := newTIMENODE006RedisService(t, writer, throttle)
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := service.ReplaceNodeDevices(ctx, 31, map[int64][]string{10: {"203.0.113.10"}}, false, now); err == nil {
		t.Fatal("ReplaceNodeDevices() succeeded through injected database failure")
	}
	devices, err := service.ListUserDevices(ctx, []int64{10}, now)
	if err != nil || !reflect.DeepEqual(devices[10], []string{"203.0.113.10"}) {
		t.Fatalf("authoritative Redis state after DB failure = (%#v, %v)", devices, err)
	}
	writer.setFailure(false)
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, flushErr := service.FlushPending(ctx, time.Now().UTC(), DefaultFlushLimit)
		if flushErr != nil {
			t.Fatal(flushErr)
		}
		_, successes := writer.counts()
		if successes == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed summary was removed instead of retried")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if attempts, successes := writer.counts(); attempts < 2 || successes != 1 {
		t.Fatalf("recovery writes = (%d, %d), want attempts>=2 successes=1", attempts, successes)
	}
}

func TestTIMENODE006RedisReplaceAllAndClearMaintainIndexes(t *testing.T) {
	writer := &recordingSummaryWriter{}
	service := newTIMENODE006RedisService(t, writer, 100*time.Millisecond)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if _, err := service.ReplaceNodeDevices(ctx, 41, map[int64][]string{
		11: {"192.0.2.11"}, 12: {"192.0.2.12"},
	}, true, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplaceNodeDevices(ctx, 42, map[int64][]string{11: {"192.0.2.11"}}, true, now); err != nil {
		t.Fatal(err)
	}
	affected, err := service.ReplaceNodeDevices(ctx, 41, map[int64][]string{11: {"198.51.100.11"}}, true, now.Add(time.Second))
	if err != nil || !reflect.DeepEqual(affected, []int64{11, 12}) {
		t.Fatalf("replace-all affected = (%v, %v), want [11 12]", affected, err)
	}
	devices, err := service.ListUserDevices(ctx, []int64{11, 12}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(devices[11], []string{"192.0.2.11", "198.51.100.11"}) || len(devices[12]) != 0 {
		t.Fatalf("replace-all devices = %#v", devices)
	}
	affected, err = service.ClearNodeDevices(ctx, []int64{41}, now.Add(2*time.Second))
	if err != nil || !reflect.DeepEqual(affected, []int64{11}) {
		t.Fatalf("ClearNodeDevices() = (%v, %v)", affected, err)
	}
	devices, err = service.ListUserDevices(ctx, []int64{11}, now.Add(2*time.Second))
	if err != nil || !reflect.DeepEqual(devices[11], []string{"192.0.2.11"}) {
		t.Fatalf("remaining cross-node device = (%#v, %v)", devices, err)
	}
	cleared, err := service.ClearUserDevices(ctx, []int64{11}, now.Add(3*time.Second))
	if err != nil || !reflect.DeepEqual(cleared, []int64{11}) {
		t.Fatalf("ClearUserDevices() = (%v, %v)", cleared, err)
	}
	devices, err = service.ListUserDevices(ctx, []int64{11}, now.Add(3*time.Second))
	if err != nil || len(devices[11]) != 0 {
		t.Fatalf("cleared user devices = (%#v, %v)", devices, err)
	}
	affected, err = service.ClearNodeDevices(ctx, []int64{42}, now.Add(4*time.Second))
	if err != nil || len(affected) != 0 {
		t.Fatalf("stale node index remained after user clear: (%v, %v)", affected, err)
	}
}

func TestTIMENODE006PendingAcknowledgementPreservesANewerVersion(t *testing.T) {
	writer := &recordingSummaryWriter{}
	throttle := 40 * time.Millisecond
	service := newTIMENODE006RedisService(t, writer, throttle)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := service.ReplaceNodeDevices(ctx, 51, map[int64][]string{13: {"192.0.2.13"}}, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ReplaceNodeDevices(ctx, 51, map[int64][]string{13: {"192.0.2.14"}}, false, now.Add(time.Millisecond)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		exists, err := service.client.Exists(ctx, service.throttleKey(13)).Result()
		if err != nil {
			t.Fatal(err)
		}
		if exists == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("device summary throttle did not expire")
		}
		time.Sleep(5 * time.Millisecond)
	}
	var newerScore int64
	var hookErr error
	writer.mu.Lock()
	writer.afterWrite = func() {
		newerScore = time.Now().Add(throttle).UnixMilli()
		hookErr = service.client.ZAdd(ctx, service.pendingKey(), redis.Z{
			Score: float64(newerScore), Member: "13",
		}).Err()
	}
	writer.mu.Unlock()
	dueAt := time.Now().UTC()
	if _, err := service.FlushPending(ctx, dueAt, DefaultFlushLimit); err != nil {
		t.Fatal(err)
	}
	if hookErr != nil {
		t.Fatal(hookErr)
	}
	score, err := service.client.ZScore(ctx, service.pendingKey(), "13").Result()
	if err != nil || int64(score) != newerScore || int64(score) <= dueAt.UnixMilli() {
		t.Fatalf("newer pending version = (%v, %v), want %d", score, err, newerScore)
	}
	writer.mu.Lock()
	writer.afterWrite = nil
	writer.mu.Unlock()
}

func TestTIMENODE006PendingFlushIsBoundedAndDrainsTheRemainder(t *testing.T) {
	tests := []struct {
		name      string
		pending   int
		limit     int
		wantFirst int
	}{
		{name: "default batch", pending: DefaultFlushLimit + 1, limit: DefaultFlushLimit, wantFirst: DefaultFlushLimit},
		{name: "hard maximum", pending: MaximumFlushLimit + 1, limit: MaximumFlushLimit + 1_000, wantFirst: MaximumFlushLimit},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			writer := &recordingSummaryWriter{}
			service := newTIMENODE006RedisService(t, writer, time.Minute)
			ctx := context.Background()
			now := time.Now().UTC()
			values := make([]redis.Z, test.pending)
			for index := range values {
				values[index] = redis.Z{Score: float64(now.Add(-time.Second).UnixMilli()), Member: strconv.Itoa(index + 1)}
			}
			if err := service.client.ZAdd(ctx, service.pendingKey(), values...).Err(); err != nil {
				t.Fatal(err)
			}

			flushed, err := service.FlushPending(ctx, now, test.limit)
			if err != nil || flushed != test.wantFirst {
				t.Fatalf("first FlushPending() = (%d, %v), want (%d, nil)", flushed, err, test.wantFirst)
			}
			remaining, err := service.client.ZCard(ctx, service.pendingKey()).Result()
			if err != nil || remaining != int64(test.pending-test.wantFirst) {
				t.Fatalf("remaining pending = (%d, %v), want %d", remaining, err, test.pending-test.wantFirst)
			}
			flushed, err = service.FlushPending(ctx, now, MaximumFlushLimit)
			if err != nil || flushed != test.pending-test.wantFirst {
				t.Fatalf("remainder FlushPending() = (%d, %v), want (%d, nil)", flushed, err, test.pending-test.wantFirst)
			}
			remaining, err = service.client.ZCard(ctx, service.pendingKey()).Result()
			if err != nil || remaining != 0 {
				t.Fatalf("final pending = (%d, %v), want (0, nil)", remaining, err)
			}
		})
	}
}

func TestTIMENODE006ConstantsMatchTheFixedContract(t *testing.T) {
	values := []int{DefaultFlushLimit, MaximumFlushLimit, MaximumDevicesPerUser}
	sort.Ints(values)
	if OnlineWindow != 300*time.Second || DatabaseThrottle != 10*time.Second || DefaultFlushInterval != 5*time.Second ||
		DefaultFlushLimit != 500 || MaximumFlushLimit != 5_000 || MaximumDevicesPerUser != 64 {
		t.Fatalf("device-state constants drifted: window=%s throttle=%s interval=%s values=%v", OnlineWindow, DatabaseThrottle, DefaultFlushInterval, values)
	}
}

func TestTIMENODE006RedisScriptsAvoidUnboundedKeyAndHashScans(t *testing.T) {
	upper := strings.ToUpper(replaceNodeDevicesScript)
	for _, forbidden := range []string{"REDIS.CALL('KEYS'", "REDIS.CALL('HKEYS'"} {
		if strings.Contains(upper, forbidden) {
			t.Fatalf("device replacement script contains unbounded command %s", forbidden)
		}
	}
}
