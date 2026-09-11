package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestNodeAuthTelemetryRecorderCoalescesAndPersistsAllFourBuckets(t *testing.T) {
	database := cloneHTTPAPITestDatabase(t)
	observedSince := fixedNow().UTC()
	if err := database.EnsureNodeAuthTelemetry(context.Background(), observedSince); err != nil {
		t.Fatal(err)
	}
	recorder := newNodeAuthTelemetryRecorder(database)
	requests := []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v2/server/handshake", nil),
		httptest.NewRequest(http.MethodGet, "/ws", nil),
	}
	var workers sync.WaitGroup
	for index := 0; index < 200; index++ {
		workers.Add(1)
		go func(index int) {
			defer workers.Done()
			authKind := store.NodeAuthKindLegacyGlobalToken
			if index%4 >= 2 {
				authKind = store.NodeAuthKindMachineCredential
			}
			recorder.record(authKind, requests[index%2], observedSince.Add(time.Duration(index)*time.Second))
		}(index)
	}
	workers.Wait()
	snapshot, err := recorder.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LegacyGlobalToken.HTTPAuthSuccess != 50 || snapshot.LegacyGlobalToken.WebSocketAuthSuccess != 50 ||
		snapshot.MachineCredential.HTTPAuthSuccess != 50 || snapshot.MachineCredential.WebSocketAuthSuccess != 50 {
		t.Fatalf("coalesced telemetry=%#v", snapshot)
	}

	restarted := newNodeAuthTelemetryRecorder(database)
	persisted, err := restarted.snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if persisted.LegacyGlobalToken.HTTPAuthSuccess != 50 || persisted.LegacyGlobalToken.WebSocketAuthSuccess != 50 ||
		persisted.MachineCredential.HTTPAuthSuccess != 50 || persisted.MachineCredential.WebSocketAuthSuccess != 50 ||
		!persisted.ObservedSince.Equal(observedSince) {
		t.Fatalf("restarted telemetry=%#v", persisted)
	}
}

func BenchmarkNodeAuthTelemetryRecord(b *testing.B) {
	recorder := &nodeAuthTelemetryRecorder{}
	request := httptest.NewRequest(http.MethodPost, "/api/v2/server/push", nil)
	now := time.Unix(1_800_000_000, 0).UTC()
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		recorder.record(store.NodeAuthKindMachineCredential, request, now)
	}
}
