package maintenance

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestCheckNodeAuthRetirementReadinessRequiresCompleteWindowNoLegacyAndMachineUsage(t *testing.T) {
	observedSince := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	asOf := observedSince.Add(31 * 24 * time.Hour)
	machineLastUsed := observedSince.Add(30 * 24 * time.Hour)
	result, err := CheckNodeAuthRetirementReadiness(context.Background(), &fakeNodeAuthTelemetryStore{
		telemetry: store.NodeAuthTelemetry{
			ObservedSince: observedSince,
			MachineCredential: store.NodeAuthUsage{
				HTTPAuthSuccess: 10, WebSocketAuthSuccess: 4, LastUsedAt: &machineLastUsed,
			},
		},
	}, asOf, 30)
	if err != nil {
		t.Fatalf("CheckNodeAuthRetirementReadiness() error = %v", err)
	}
	if !result.RetirementCandidate || !result.ObservationComplete || result.ObservedDays != 31 || result.MinimumObservedDays != 30 ||
		result.LegacyGlobalToken.HTTPAuthSuccess != 0 || result.MachineCredential.HTTPAuthSuccess != 10 {
		t.Fatalf("readiness result = %#v", result)
	}
	if got := strings.Join(result.Reasons, "\n"); !strings.Contains(got, "minimum observation window elapsed") {
		t.Fatalf("readiness reasons = %#v", result.Reasons)
	}
}

func TestCheckNodeAuthRetirementReadinessReportsBlockingReasons(t *testing.T) {
	observedSince := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	legacyLastUsed := observedSince.Add(6 * 24 * time.Hour)
	result, err := CheckNodeAuthRetirementReadiness(context.Background(), &fakeNodeAuthTelemetryStore{
		telemetry: store.NodeAuthTelemetry{
			ObservedSince: observedSince,
			LegacyGlobalToken: store.NodeAuthUsage{
				HTTPAuthSuccess: 1, LastUsedAt: &legacyLastUsed,
			},
		},
	}, observedSince.Add(7*24*time.Hour), 30)
	if err != nil {
		t.Fatalf("CheckNodeAuthRetirementReadiness() error = %v", err)
	}
	if result.RetirementCandidate || result.ObservationComplete {
		t.Fatalf("readiness incorrectly passed: %#v", result)
	}
	reasons := strings.Join(result.Reasons, "\n")
	for _, want := range []string{
		"minimum observation window has not elapsed",
		"legacy global token was used during the observation window",
		"no machine credential usage has been observed",
	} {
		if !strings.Contains(reasons, want) {
			t.Fatalf("readiness reasons %q missing %q", reasons, want)
		}
	}
}

func TestCheckNodeAuthRetirementReadinessRejectsInvalidInputsWithoutReads(t *testing.T) {
	for _, testCase := range []struct {
		name                string
		now                 time.Time
		minimumObservedDays int
	}{
		{name: "zero time", minimumObservedDays: 30},
		{name: "zero days", now: time.Now().UTC()},
		{name: "excessive days", now: time.Now().UTC(), minimumObservedDays: MaxNodeAuthRetirementObservationDays + 1},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			database := &fakeNodeAuthTelemetryStore{}
			if _, err := CheckNodeAuthRetirementReadiness(context.Background(), database, testCase.now, testCase.minimumObservedDays); err == nil {
				t.Fatal("CheckNodeAuthRetirementReadiness() accepted invalid input")
			}
			if database.reads != 0 {
				t.Fatalf("invalid input performed %d telemetry reads", database.reads)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	database := &fakeNodeAuthTelemetryStore{}
	if _, err := CheckNodeAuthRetirementReadiness(cancelled, database, time.Now().UTC(), 30); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled readiness error = %v", err)
	}
	if database.reads != 0 {
		t.Fatalf("cancelled context performed %d telemetry reads", database.reads)
	}
}

type fakeNodeAuthTelemetryStore struct {
	telemetry store.NodeAuthTelemetry
	err       error
	reads     int
}

func (s *fakeNodeAuthTelemetryStore) GetNodeAuthTelemetry(context.Context) (store.NodeAuthTelemetry, error) {
	s.reads++
	return s.telemetry, s.err
}
