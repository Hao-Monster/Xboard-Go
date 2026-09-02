package devicestate

import (
	"context"
	"io"
	"net"
	"net/url"
	"os"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func TestTIMENODE006RedisOutageFailsClosedAndRecoversWithoutInventingEmptyState(t *testing.T) {
	rawURL := os.Getenv("XBOARD_TEST_REDIS_URL")
	if rawURL == "" || os.Getenv("XBOARD_TEST_REDIS_FAILURES") != "true" {
		t.Skip("destructive Redis device-state failure injection is not enabled")
	}
	proxy, proxiedURL := newDeviceRedisFaultProxy(t, rawURL)
	writer := &recordingSummaryWriter{}
	prefix := "xg:device-failure:" + uuid.NewString() + ":"
	service, err := NewRedis(context.Background(), Options{
		URL: proxiedURL, Prefix: prefix, WriteSummaries: writer.write, DatabaseThrottle: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = service.Close()
		removeDeviceRedisKeys(t, rawURL, prefix)
	})
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := service.ReplaceNodeDevices(ctx, 61, map[int64][]string{14: {"192.0.2.14"}}, false, now); err != nil {
		t.Fatal(err)
	}

	proxy.Block()
	if devices, err := service.ListUserDevices(ctx, []int64{14}, now); err == nil || len(devices) != 0 {
		t.Fatalf("blocked Redis query = (%#v, %v), want an error and no fabricated result", devices, err)
	}
	if _, err := service.ReplaceNodeDevices(ctx, 61, map[int64][]string{14: {"198.51.100.14"}}, false, now.Add(time.Second)); err == nil {
		t.Fatal("device replacement succeeded while Redis was unavailable")
	}

	proxy.Unblock()
	deadline := time.Now().Add(8 * time.Second)
	for {
		devices, queryErr := service.ListUserDevices(ctx, []int64{14}, now)
		if queryErr == nil {
			if !reflect.DeepEqual(devices[14], []string{"192.0.2.14"}) && !reflect.DeepEqual(devices[14], []string{"198.51.100.14"}) {
				t.Fatalf("ambiguous failed write produced an invalid state: %#v", devices)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("Redis device state did not recover: %v", queryErr)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := service.ReplaceNodeDevices(ctx, 61, map[int64][]string{14: {"198.51.100.14"}}, false, time.Now().UTC()); err != nil {
		t.Fatalf("post-recovery replacement error = %v", err)
	}
	devices, err := service.ListUserDevices(ctx, []int64{14}, time.Now().UTC())
	if err != nil || !reflect.DeepEqual(devices[14], []string{"198.51.100.14"}) {
		t.Fatalf("post-recovery state = (%#v, %v)", devices, err)
	}
}

func removeDeviceRedisKeys(t *testing.T, rawURL, prefix string) {
	t.Helper()
	options, err := redis.ParseURL(rawURL)
	if err != nil {
		t.Errorf("parse Redis cleanup URL: %v", err)
		return
	}
	client := redis.NewClient(options)
	defer client.Close()
	var cursor uint64
	for {
		keys, next, scanErr := client.Scan(context.Background(), cursor, prefix+"*", 500).Result()
		if scanErr != nil {
			t.Errorf("scan Redis cleanup keys: %v", scanErr)
			return
		}
		if len(keys) > 0 {
			if err := client.Del(context.Background(), keys...).Err(); err != nil {
				t.Errorf("delete Redis cleanup keys: %v", err)
				return
			}
		}
		cursor = next
		if cursor == 0 {
			return
		}
	}
}

type deviceRedisFaultProxy struct {
	listener net.Listener
	target   string
	blocked  atomic.Bool
	mu       sync.Mutex
	active   map[net.Conn]struct{}
}

func newDeviceRedisFaultProxy(t *testing.T, rawURL string) (*deviceRedisFaultProxy, string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "redis" || parsed.Host == "" {
		t.Skip("Redis failure injection requires a redis:// integration URL")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for Redis device-state fault proxy: %v", err)
	}
	proxy := &deviceRedisFaultProxy{listener: listener, target: parsed.Host, active: make(map[net.Conn]struct{})}
	go proxy.accept()
	t.Cleanup(func() {
		_ = listener.Close()
		proxy.Block()
	})
	parsed.Host = listener.Addr().String()
	return proxy, parsed.String()
}

func (proxy *deviceRedisFaultProxy) accept() {
	for {
		client, err := proxy.listener.Accept()
		if err != nil {
			return
		}
		if proxy.blocked.Load() {
			_ = client.Close()
			continue
		}
		upstream, err := net.DialTimeout("tcp", proxy.target, time.Second)
		if err != nil {
			_ = client.Close()
			continue
		}
		proxy.track(client, upstream)
		if proxy.blocked.Load() {
			_ = client.Close()
			_ = upstream.Close()
			continue
		}
		go proxy.pipe(client, upstream)
		go proxy.pipe(upstream, client)
	}
}

func (proxy *deviceRedisFaultProxy) pipe(destination, source net.Conn) {
	_, _ = io.Copy(destination, source)
	_ = destination.Close()
	_ = source.Close()
	proxy.mu.Lock()
	delete(proxy.active, destination)
	delete(proxy.active, source)
	proxy.mu.Unlock()
}

func (proxy *deviceRedisFaultProxy) track(connections ...net.Conn) {
	proxy.mu.Lock()
	for _, connection := range connections {
		proxy.active[connection] = struct{}{}
	}
	proxy.mu.Unlock()
}

func (proxy *deviceRedisFaultProxy) Block() {
	proxy.blocked.Store(true)
	proxy.mu.Lock()
	connections := make([]net.Conn, 0, len(proxy.active))
	for connection := range proxy.active {
		connections = append(connections, connection)
	}
	proxy.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
}

func (proxy *deviceRedisFaultProxy) Unblock() { proxy.blocked.Store(false) }
