package maintenance

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const (
	DefaultNodeAuthRetirementObservationDays = 30
	MaxNodeAuthRetirementObservationDays     = 365
)

type nodeAuthTelemetryStore interface {
	GetNodeAuthTelemetry(context.Context) (store.NodeAuthTelemetry, error)
}

type NodeAuthRetirementReadiness struct {
	AsOf                time.Time           `json:"as_of"`
	ObservedSince       time.Time           `json:"observed_since"`
	ObservedDays        int                 `json:"observed_days"`
	MinimumObservedDays int                 `json:"minimum_observed_days"`
	ObservationComplete bool                `json:"observation_complete"`
	RetirementCandidate bool                `json:"retirement_candidate"`
	LegacyGlobalToken   store.NodeAuthUsage `json:"legacy_global_token"`
	MachineCredential   store.NodeAuthUsage `json:"machine_credential"`
	Reasons             []string            `json:"reasons"`
}

func CheckNodeAuthRetirementReadiness(ctx context.Context, database nodeAuthTelemetryStore, now time.Time, minimumObservedDays int) (NodeAuthRetirementReadiness, error) {
	if err := ctx.Err(); err != nil {
		return NodeAuthRetirementReadiness{}, err
	}
	if database == nil || now.IsZero() || now.Unix() < 0 || minimumObservedDays < 1 || minimumObservedDays > MaxNodeAuthRetirementObservationDays {
		return NodeAuthRetirementReadiness{}, fmt.Errorf("node authentication retirement readiness requires a database, current time, and minimum observation window between 1 and %d days", MaxNodeAuthRetirementObservationDays)
	}
	telemetry, err := database.GetNodeAuthTelemetry(ctx)
	if err != nil {
		return NodeAuthRetirementReadiness{}, fmt.Errorf("read node authentication telemetry: %w", err)
	}
	if telemetry.ObservedSince.IsZero() || telemetry.ObservedSince.Unix() < 0 {
		return NodeAuthRetirementReadiness{}, errors.New("node authentication telemetry has an invalid observation start")
	}
	asOf := now.UTC()
	observedSince := telemetry.ObservedSince.UTC()
	observedDays := 0
	if asOf.After(observedSince) {
		observedDays = int(asOf.Sub(observedSince).Hours() / 24)
	}
	legacyUses := usageTotal(telemetry.LegacyGlobalToken)
	machineUses := usageTotal(telemetry.MachineCredential)
	reasons := make([]string, 0, 3)
	observationComplete := observedDays >= minimumObservedDays
	if !observationComplete {
		reasons = append(reasons, "minimum observation window has not elapsed")
	}
	if legacyUses != 0 {
		reasons = append(reasons, "legacy global token was used during the observation window")
	}
	if machineUses == 0 {
		reasons = append(reasons, "no machine credential usage has been observed")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "minimum observation window elapsed with machine credential usage and no legacy global token usage")
	}
	return NodeAuthRetirementReadiness{
		AsOf: asOf, ObservedSince: observedSince, ObservedDays: observedDays,
		MinimumObservedDays: minimumObservedDays, ObservationComplete: observationComplete,
		RetirementCandidate: observationComplete && legacyUses == 0 && machineUses != 0,
		LegacyGlobalToken:   telemetry.LegacyGlobalToken, MachineCredential: telemetry.MachineCredential,
		Reasons: reasons,
	}, nil
}

func usageTotal(usage store.NodeAuthUsage) uint64 {
	return usage.HTTPAuthSuccess + usage.WebSocketAuthSuccess
}
