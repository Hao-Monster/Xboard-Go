package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPShutdownWaitsForOrdinaryResponseBeforeSpecialWaiter(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	releaseHandler := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseHandler)
	server := newShutdownTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "response completed before dependency cleanup")
	}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	waiterCalled := make(chan struct{})
	waiter := shutdownWaiterFunc(func(context.Context) error { close(waiterCalled); return nil })
	result := make(chan error, 1)
	go func() { result <- runHTTPServer(ctx, server, waiter, discardShutdownLogger(), time.Second) }()
	response := requestShutdownTestServer(t, server)
	awaitShutdownSignal(t, entered, "handler entry")
	cancel()
	awaitShutdownSignal(t, server.serveReturned, "listener exit")
	select {
	case <-waiterCalled:
		t.Fatal("special waiter ran before the ordinary HTTP response completed")
	case err := <-result:
		t.Fatalf("HTTP coordinator returned while its handler was active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseHandler()
	got := awaitShutdownValue(t, response, "HTTP response")
	if got.err != nil || got.status != http.StatusOK || got.body != "response completed before dependency cleanup" {
		t.Fatalf("response = %#v, want complete successful response", got)
	}
	if err := awaitShutdownValue(t, result, "coordinator completion"); err != nil {
		t.Fatalf("coordinator error = %v, want successful drain", err)
	}
	awaitShutdownSignal(t, waiterCalled, "special waiter")
}

func TestHTTPShutdownHandlesStartupCancellation(t *testing.T) {
	for _, alreadyCancelled := range []bool{false, true} {
		name := "immediately_after_start"
		if alreadyCancelled {
			name = "before_start"
		}
		t.Run(name, func(t *testing.T) {
			server := newShutdownTestServer(t, http.NotFoundHandler())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if alreadyCancelled {
				cancel()
			}
			waiterCalled := make(chan struct{})
			waiter := shutdownWaiterFunc(func(context.Context) error { close(waiterCalled); return nil })
			result := make(chan error, 1)
			go func() { result <- runHTTPServer(ctx, server, waiter, discardShutdownLogger(), time.Second) }()
			awaitShutdownSignal(t, server.serveStarted, "server startup")
			cancel()
			if err := awaitShutdownValue(t, result, "startup cancellation"); err != nil {
				t.Fatalf("coordinator error = %v, want successful drain", err)
			}
			awaitShutdownSignal(t, server.shutdownReturned, "completed shutdown")
			awaitShutdownSignal(t, waiterCalled, "special waiter")
		})
	}
}

func TestHTTPShutdownWaitsForAndReportsDeadline(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	releaseHandler := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseHandler)
	server := newShutdownTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "late response")
	}))
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var output shutdownLogBuffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	result := make(chan error, 1)
	go func() { result <- runHTTPServer(ctx, server, nil, logger, 40*time.Millisecond) }()
	response := requestShutdownTestServer(t, server)
	awaitShutdownSignal(t, entered, "handler entry")
	cancel()
	if err := awaitShutdownValue(t, result, "shutdown deadline"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("coordinator error = %v, want visible shutdown deadline", err)
	}
	if log := output.String(); !strings.Contains(log, "shutdown HTTP server") || !strings.Contains(log, context.DeadlineExceeded.Error()) {
		t.Fatalf("coordinator returned without reporting incomplete HTTP drain: %s", log)
	}
	awaitShutdownSignal(t, server.shutdownReturned, "shutdown completion")
	releaseHandler()
	if got := awaitShutdownValue(t, response, "released response"); got.err != nil {
		t.Fatalf("release test handler: %v", got.err)
	}
}

func TestHTTPShutdownListenerFailureStopsCoordinator(t *testing.T) {
	wantErr := errors.New("synthetic listener failure")
	var shutdownCalls atomic.Int32
	server := shutdownTestFunctions{
		listen:   func() error { return wantErr },
		shutdown: func(context.Context) error { shutdownCalls.Add(1); return nil },
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	waiter := shutdownWaiterFunc(func(context.Context) error {
		t.Error("special waiter called on listener startup failure")
		return nil
	})
	go func() { result <- runHTTPServer(ctx, server, waiter, discardShutdownLogger(), time.Second) }()
	if err := awaitShutdownValue(t, result, "listener failure"); !errors.Is(err, wantErr) {
		t.Fatalf("coordinator error = %v, want listener error", err)
	}
	if calls := shutdownCalls.Load(); calls != 1 {
		t.Fatalf("listener failure shutdown calls = %d, want one completed cleanup", calls)
	}
	cancel()
	if calls := shutdownCalls.Load(); calls != 1 {
		t.Fatalf("shutdown repeated after coordinator returned: %d", calls)
	}
}

func TestHTTPShutdownDrainsActiveHandlerAfterListenerFailure(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	releaseHandler := sync.OnceFunc(func() { close(release) })
	t.Cleanup(releaseHandler)
	server := newShutdownTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "completed after listener failure")
	}))
	wantErr := errors.New("synthetic accept failure")
	listener := &failingShutdownListener{
		Listener: server.listener, fail: make(chan struct{}), closed: make(chan struct{}), err: wantErr,
	}
	server.listener = listener
	result := make(chan error, 1)
	go func() { result <- runHTTPServer(t.Context(), server, nil, discardShutdownLogger(), time.Second) }()
	response := requestShutdownTestServer(t, server)
	awaitShutdownSignal(t, entered, "handler entry")
	close(listener.fail)
	awaitShutdownSignal(t, server.serveReturned, "listener failure")
	select {
	case err := <-result:
		t.Fatalf("listener failure released dependencies while handler was active: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	releaseHandler()
	got := awaitShutdownValue(t, response, "active response after listener failure")
	if got.err != nil || got.status != http.StatusOK || got.body != "completed after listener failure" {
		t.Fatalf("response = %#v, want complete successful response", got)
	}
	if err := awaitShutdownValue(t, result, "drained listener failure"); !errors.Is(err, wantErr) {
		t.Fatalf("coordinator error = %v, want original listener error", err)
	}
	awaitShutdownSignal(t, server.shutdownReturned, "completed shutdown")
}

func TestHTTPShutdownPreservesAllLifecycleErrors(t *testing.T) {
	serveErr := errors.New("synthetic serve failure")
	shutdownErr := errors.New("synthetic shutdown failure")
	drainErr := errors.New("synthetic special drain failure")
	server := shutdownTestFunctions{
		listen:   func() error { return serveErr },
		shutdown: func(context.Context) error { return shutdownErr },
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	waiter := shutdownWaiterFunc(func(context.Context) error { return drainErr })
	err := runHTTPServer(ctx, server, waiter, discardShutdownLogger(), time.Second)
	for _, want := range []error{serveErr, shutdownErr, drainErr} {
		if !errors.Is(err, want) {
			t.Errorf("coordinator error = %v, missing %v", err, want)
		}
	}
}

func TestHTTPShutdownPreservesSpecialWaiterDeadline(t *testing.T) {
	server := newShutdownTestServer(t, http.NotFoundHandler())
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var output shutdownLogBuffer
	waiter := shutdownWaiterFunc(func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	result := make(chan error, 1)
	go func() {
		result <- runHTTPServer(ctx, server, waiter, slog.New(slog.NewTextHandler(&output, nil)), 40*time.Millisecond)
	}()
	awaitShutdownSignal(t, server.serveStarted, "server startup")
	cancel()
	if err := awaitShutdownValue(t, result, "special drain deadline"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("coordinator error = %v, want visible special waiter deadline", err)
	}
	if log := output.String(); !strings.Contains(log, "wait for HTTP API shutdown") || !strings.Contains(log, context.DeadlineExceeded.Error()) {
		t.Fatalf("special waiter failure not reported: %s", log)
	}
}

type shutdownTestServer struct {
	server           *http.Server
	listener         net.Listener
	serveStarted     chan struct{}
	serveReturned    chan struct{}
	shutdownReturned chan struct{}
}

func newShutdownTestServer(t *testing.T, handler http.Handler) *shutdownTestServer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &shutdownTestServer{
		server: &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}, listener: listener,
		serveStarted: make(chan struct{}), serveReturned: make(chan struct{}), shutdownReturned: make(chan struct{}),
	}
	t.Cleanup(func() { _ = server.server.Close(); _ = listener.Close() })
	return server
}

func (s *shutdownTestServer) ListenAndServe() error {
	close(s.serveStarted)
	defer close(s.serveReturned)
	return s.server.Serve(s.listener)
}

func (s *shutdownTestServer) Shutdown(ctx context.Context) error {
	defer close(s.shutdownReturned)
	return s.server.Shutdown(ctx)
}

type failingShutdownListener struct {
	net.Listener
	fail      chan struct{}
	closed    chan struct{}
	err       error
	accepted  bool
	closeOnce sync.Once
	closeErr  error
}

func (l *failingShutdownListener) Accept() (net.Conn, error) {
	if !l.accepted {
		l.accepted = true
		return l.Listener.Accept()
	}
	select {
	case <-l.fail:
		return nil, l.err
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *failingShutdownListener) Close() error {
	l.closeOnce.Do(func() {
		close(l.closed)
		l.closeErr = l.Listener.Close()
	})
	return l.closeErr
}

type shutdownTestFunctions struct {
	listen   func() error
	shutdown func(context.Context) error
}

func (s shutdownTestFunctions) ListenAndServe() error              { return s.listen() }
func (s shutdownTestFunctions) Shutdown(ctx context.Context) error { return s.shutdown(ctx) }

type shutdownWaiterFunc func(context.Context) error

func (f shutdownWaiterFunc) WaitForShutdown(ctx context.Context) error { return f(ctx) }

type shutdownHTTPResponse struct {
	status int
	body   string
	err    error
}

func requestShutdownTestServer(t *testing.T, server *shutdownTestServer) <-chan shutdownHTTPResponse {
	t.Helper()
	transport := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	result := make(chan shutdownHTTPResponse, 1)
	go func() {
		response, err := client.Get("http://" + server.listener.Addr().String())
		if err != nil {
			result <- shutdownHTTPResponse{err: err}
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		result <- shutdownHTTPResponse{status: response.StatusCode, body: string(body), err: err}
	}()
	return result
}

func awaitShutdownSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	awaitShutdownValue(t, ch, name)
}

func awaitShutdownValue[T any](t *testing.T, ch <-chan T, name string) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
		var zero T
		return zero
	}
}

func discardShutdownLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

type shutdownLogBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *shutdownLogBuffer) Write(value []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(value)
}

func (b *shutdownLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}
