package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/httpapi"
)

type httpServerLifecycle interface {
	ListenAndServe() error
	Shutdown(context.Context) error
}

func runHTTPServer(ctx context.Context, server httpServerLifecycle, waiter httpapi.ShutdownWaiter, logger *slog.Logger, timeout time.Duration) error {
	serveDone := make(chan error, 1)
	go func() { serveDone <- server.ListenAndServe() }()
	var serveErr error
	serveReturned := false
	select {
	case <-ctx.Done():
	case serveErr = <-serveDone:
		serveReturned = true
	}
	// Serve also returns before active handlers finish when its listener fails.
	// Always drain HTTP before releasing dependencies, not only after a signal.
	shutdownContext, cancel := context.WithTimeout(context.Background(), timeout)
	shutdownErr := server.Shutdown(shutdownContext)
	cancel()
	if shutdownErr != nil {
		logger.Error("shutdown HTTP server", "error", shutdownErr)
	}
	if !serveReturned {
		serveErr = <-serveDone
	}
	if errors.Is(serveErr, http.ErrServerClosed) {
		serveErr = nil
	}
	var drainErr error
	if ctx.Err() != nil && waiter != nil {
		drainContext, cancel := context.WithTimeout(context.Background(), timeout)
		drainErr = waiter.WaitForShutdown(drainContext)
		if drainErr != nil {
			logger.Error("wait for HTTP API shutdown", "error", drainErr)
		}
		cancel()
	}
	return errors.Join(serveErr, shutdownErr, drainErr)
}
