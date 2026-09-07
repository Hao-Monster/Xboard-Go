package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/redis/go-redis/v9"
)

// TestCAPR014WebSocketConnectionDrain is opt-in because it deliberately opens
// hundreds of real sockets. The test reports characterization data rather than
// enforcing a production SLO while D-015 remains undecided.
func TestCAPR014WebSocketConnectionDrain(t *testing.T) {
	connectionCount := capacityTestInt(t, "XBOARD_WS_CAPACITY_CONNECTIONS", 0, 1, 4_000)
	if connectionCount == 0 {
		t.Skip("XBOARD_WS_CAPACITY_CONNECTIONS is not configured")
	}
	redisURL := os.Getenv("XBOARD_TEST_REDIS_URL")
	if redisURL == "" {
		t.Fatal("XBOARD_TEST_REDIS_URL is required for the capacity test")
	}
	workerCount := capacityTestInt(t, "XBOARD_WS_CAPACITY_WORKERS", min(connectionCount, 64), 1, connectionCount)

	database := cloneHTTPAPITestDatabase(t)
	prefix := "xboard-go-r014-cap:" + uuid.NewString() + ":"
	coordinator := newHTTPAPITestCoordinator(t, redisURL, prefix, "capacity")
	ctx, cancel := context.WithCancel(context.Background())
	handler := New(Dependencies{
		Context: ctx, Store: database, PasswordHasher: newHTTPAPITestPasswordHasher(), Now: fixedNow,
		PanelURL: "https://panel.example.test", AllowedOrigins: []string{"https://panel.example.test"},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), WebSocketEnabled: true,
		NodeCoordinator: coordinator,
	})
	server := httptest.NewUnstartedServer(handler)
	server.Listener = &capacityPeerListener{Listener: server.Listener}
	server.Start()
	defer server.Close()

	type machineCredential struct {
		machineID int64
		token     string
	}
	credentials := make([]machineCredential, connectionCount)
	setupStarted := time.Now()
	for index := range credentials {
		machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{
			Name: fmt.Sprintf("capacity-machine-%05d", index), IsActive: true,
		}, fixedNow())
		if err != nil {
			t.Fatalf("create machine %d: %v", index, err)
		}
		credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, fixedNow())
		if err != nil {
			t.Fatalf("exchange enrollment %d: %v", index, err)
		}
		credentials[index] = machineCredential{machineID: machine.ID, token: credential.Token}
	}
	setupElapsed := time.Since(setupStarted)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	baselineGoroutines := runtime.NumGoroutine()

	connections := make([]*websocket.Conn, connectionCount)
	durations := make([]time.Duration, connectionCount)
	errorsFound := make(chan error, connectionCount)
	workers := make(chan struct{}, workerCount)
	var connectWait sync.WaitGroup
	connectStarted := time.Now()
	for index, credential := range credentials {
		connectWait.Add(1)
		go func(index int, credential machineCredential) {
			defer connectWait.Done()
			workers <- struct{}{}
			defer func() { <-workers }()
			started := time.Now()
			wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + fmt.Sprintf("/ws?machine_id=%d", credential.machineID)
			dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
			connection, response, err := dialer.Dial(wsURL, http.Header{"Authorization": []string{"Bearer " + credential.token}})
			durations[index] = time.Since(started)
			if err != nil {
				status := 0
				if response != nil {
					status = response.StatusCode
					_ = response.Body.Close()
				}
				errorsFound <- fmt.Errorf("connection %d dial status=%d: %w", index, status, err)
				return
			}
			_ = connection.SetReadDeadline(time.Now().Add(10 * time.Second))
			var event wsEnvelope
			if err := connection.ReadJSON(&event); err != nil {
				_ = connection.Close()
				errorsFound <- fmt.Errorf("connection %d read auth event: %w", index, err)
				return
			}
			if event.Event != "auth.success" {
				_ = connection.Close()
				errorsFound <- fmt.Errorf("connection %d auth event=%q, want auth.success", index, event.Event)
				return
			}
			_ = connection.SetReadDeadline(time.Time{})
			connections[index] = connection
		}(index, credential)
	}
	connectWait.Wait()
	connectElapsed := time.Since(connectStarted)
	close(errorsFound)
	var connectErrors []error
	for err := range errorsFound {
		connectErrors = append(connectErrors, err)
	}
	if len(connectErrors) != 0 {
		cancel()
		closeCapacityConnections(connections)
		waitForHTTPAPIShutdown(t, handler, 30*time.Second)
		t.Fatalf("%d/%d WebSocket connections failed; first error: %v", len(connectErrors), connectionCount, connectErrors[0])
	}

	var connected runtime.MemStats
	runtime.ReadMemStats(&connected)
	connectedGoroutines := runtime.NumGoroutine()

	drainStarted := time.Now()
	cancel()
	drainErrors := make(chan error, connectionCount)
	var drainWait sync.WaitGroup
	for index, connection := range connections {
		drainWait.Add(1)
		go func(index int, connection *websocket.Conn) {
			defer drainWait.Done()
			_ = connection.SetReadDeadline(time.Now().Add(30 * time.Second))
			_, _, err := connection.ReadMessage()
			var closeError *websocket.CloseError
			if !errors.As(err, &closeError) || closeError.Code != websocket.CloseServiceRestart {
				drainErrors <- fmt.Errorf("connection %d close=%v, want code %d", index, err, websocket.CloseServiceRestart)
			}
		}(index, connection)
	}
	waitForHTTPAPIShutdown(t, handler, 30*time.Second)
	drainWait.Wait()
	drainElapsed := time.Since(drainStarted)
	close(drainErrors)
	var closeErrors []error
	for err := range drainErrors {
		closeErrors = append(closeErrors, err)
	}
	closeCapacityConnections(connections)
	if len(closeErrors) != 0 {
		t.Fatalf("%d/%d WebSocket drains failed; first error: %v", len(closeErrors), connectionCount, closeErrors[0])
	}
	if keys := redisKeysForPrefix(t, redisURL, prefix); len(keys) != 0 {
		t.Fatalf("capacity drain left %d coordination keys; first key: %s", len(keys), keys[0])
	}

	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	t.Logf("connections=%d workers=%d setup=%s connect_total=%s connect_p50=%s connect_p95=%s connect_p99=%s connect_max=%s drain=%s connect_errors=0 drain_errors=0 heap_delta_bytes=%d total_alloc_delta_bytes=%d goroutine_delta=%d",
		connectionCount, workerCount, setupElapsed, connectElapsed,
		capacityPercentile(durations, 50), capacityPercentile(durations, 95), capacityPercentile(durations, 99), durations[len(durations)-1], drainElapsed,
		int64(connected.HeapAlloc)-int64(before.HeapAlloc), int64(connected.TotalAlloc)-int64(before.TotalAlloc), connectedGoroutines-baselineGoroutines,
	)
}

// capacityPeerListener keeps the application admission limiter in the test
// path while representing nodes arriving through multiple edge peers. A single
// peer is intentionally capped at 600 handshakes/minute by the security model.
type capacityPeerListener struct {
	net.Listener
	next atomic.Uint32
}

func (listener *capacityPeerListener) Accept() (net.Conn, error) {
	connection, err := listener.Listener.Accept()
	if err != nil {
		return nil, err
	}
	sequence := listener.next.Add(1)
	third := byte((sequence / 254) % 254)
	fourth := byte(sequence%254 + 1)
	return &capacityPeerConnection{
		Conn: connection,
		remote: &net.TCPAddr{
			IP:   net.IPv4(198, 18, third, fourth),
			Port: 10_000 + int(sequence%50_000),
		},
	}, nil
}

type capacityPeerConnection struct {
	net.Conn
	remote net.Addr
}

func (connection *capacityPeerConnection) RemoteAddr() net.Addr { return connection.remote }

func capacityTestInt(t *testing.T, name string, defaultValue, minimum, maximum int) int {
	t.Helper()
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return defaultValue
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		t.Fatalf("%s must be an integer from %d through %d", name, minimum, maximum)
	}
	return value
}

func capacityPercentile(sorted []time.Duration, percentile int) time.Duration {
	index := (len(sorted)*percentile + 99) / 100
	return sorted[max(0, index-1)]
}

func closeCapacityConnections(connections []*websocket.Conn) {
	for _, connection := range connections {
		if connection != nil {
			_ = connection.Close()
		}
	}
}

func redisKeysForPrefix(t *testing.T, redisURL, prefix string) []string {
	t.Helper()
	options, err := redis.ParseURL(redisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(options)
	defer client.Close()
	var keys []string
	var cursor uint64
	for {
		batch, next, err := client.Scan(context.Background(), cursor, prefix+"*", 100).Result()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, batch...)
		cursor = next
		if cursor == 0 {
			return keys
		}
	}
}
