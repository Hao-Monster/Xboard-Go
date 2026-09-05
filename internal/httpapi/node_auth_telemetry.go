package httpapi

import (
	"context"
	"log/slog"
	"math"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const nodeAuthTelemetryFlushInterval = time.Minute

type nodeAuthTelemetryRecorder struct {
	store   *store.Store
	flushMu sync.Mutex
	counts  [4]atomic.Uint64
	lastUse [4]atomic.Int64
}

func newNodeAuthTelemetryRecorder(database *store.Store) *nodeAuthTelemetryRecorder {
	return &nodeAuthTelemetryRecorder{store: database}
}

func (recorder *nodeAuthTelemetryRecorder) record(authKind string, request *http.Request, now time.Time) {
	index := nodeAuthTelemetryIndex(authKind, request.URL.Path == "/ws")
	for {
		current := recorder.counts[index].Load()
		if current == math.MaxUint64 || recorder.counts[index].CompareAndSwap(current, current+1) {
			break
		}
	}
	storeLatestUnix(&recorder.lastUse[index], now.Unix())
}

func (recorder *nodeAuthTelemetryRecorder) snapshot(ctx context.Context) (store.NodeAuthTelemetry, error) {
	recorder.flushMu.Lock()
	defer recorder.flushMu.Unlock()
	if err := recorder.flushLocked(ctx); err != nil {
		return store.NodeAuthTelemetry{}, err
	}
	return recorder.store.GetNodeAuthTelemetry(ctx)
}

func (recorder *nodeAuthTelemetryRecorder) run(ctx context.Context, logger *slog.Logger) {
	ticker := time.NewTicker(nodeAuthTelemetryFlushInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if _, err := recorder.snapshot(ctx); err != nil && ctx.Err() == nil {
				logger.Warn("flush node authentication telemetry", "error", err)
			}
		case <-ctx.Done():
			flushContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if _, err := recorder.snapshot(flushContext); err != nil {
				logger.Warn("flush node authentication telemetry during shutdown", "error", err)
			}
			cancel()
			return
		}
	}
}

func (recorder *nodeAuthTelemetryRecorder) flushLocked(ctx context.Context) error {
	increments := make([]store.NodeAuthUsageIncrement, 0, len(recorder.counts))
	flushed := [4]uint64{}
	for index := range recorder.counts {
		count := recorder.counts[index].Swap(0)
		if count == 0 {
			continue
		}
		flushed[index] = count
		authKind, transport := nodeAuthTelemetryLabels(index)
		increments = append(increments, store.NodeAuthUsageIncrement{
			AuthKind: authKind, Transport: transport, SuccessCount: count,
			LastUsedAt: time.Unix(recorder.lastUse[index].Load(), 0).UTC(),
		})
	}
	if err := recorder.store.AddNodeAuthUsage(ctx, increments); err != nil {
		for index, count := range flushed {
			if count != 0 {
				recorder.counts[index].Add(count)
			}
		}
		return err
	}
	return nil
}

func nodeAuthTelemetryIndex(authKind string, websocket bool) int {
	index := 0
	if authKind == store.NodeAuthKindMachineCredential {
		index = 2
	}
	if websocket {
		index++
	}
	return index
}

func nodeAuthTelemetryLabels(index int) (string, string) {
	authKind := store.NodeAuthKindLegacyGlobalToken
	if index >= 2 {
		authKind = store.NodeAuthKindMachineCredential
	}
	transport := store.NodeAuthTransportHTTP
	if index%2 == 1 {
		transport = store.NodeAuthTransportWebSocket
	}
	return authKind, transport
}

func storeLatestUnix(destination *atomic.Int64, value int64) {
	for {
		current := destination.Load()
		if value <= current || destination.CompareAndSwap(current, value) {
			return
		}
	}
}
