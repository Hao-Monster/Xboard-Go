package diagnostics_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/diagnostics"
)

type ContractFixtures struct {
	IdentityVectors []struct {
		Name                 string `json:"name"`
		PanelScope           string `json:"panel_scope"`
		SubscriberUserID     int64  `json:"subscriber_user_id"`
		ExpectedDiagnosticID string `json:"expected_diagnostic_id"`
	} `json:"identity_vectors"`

	InvalidIdentityVectors []struct {
		Name              string `json:"name"`
		PanelScope        string `json:"panel_scope"`
		SubscriberUserID  int64  `json:"subscriber_user_id"`
		ExpectedErrorCode string `json:"expected_error_code"`
	} `json:"invalid_identity_vectors"`

	EventIDVectors []struct {
		Name            string `json:"name"`
		PanelScope      string `json:"panel_scope"`
		NodeID          int64  `json:"node_id"`
		RuntimeID       string `json:"runtime_id"`
		EventFamily     string `json:"event_family"`
		OwnerID         string `json:"owner_id"`
		Sequence        int64  `json:"sequence"`
		ExpectedEventID string `json:"expected_event_id"`
	} `json:"event_id_vectors"`

	ValidEvents []struct {
		Name    string          `json:"name"`
		Payload json.RawMessage `json:"payload"`
	} `json:"valid_events"`

	InvalidEvents []struct {
		Name           string          `json:"name"`
		ExpectedReason string          `json:"expected_reason"`
		Payload        json.RawMessage `json:"payload"`
	} `json:"invalid_events"`

	DomainNormalizationVectors []struct {
		Input    string  `json:"input"`
		Expected *string `json:"expected"`
		Valid    bool    `json:"valid"`
	} `json:"domain_normalization_vectors"`

	StackSanitizationVectors []struct {
		Name                string                  `json:"name"`
		RawFrames           []diagnostics.StackFrame `json:"raw_frames"`
		ExpectedFramesTotal int                     `json:"expected_frames_total"`
		ExpectedFramesKept  int                     `json:"expected_frames_kept"`
		ExpectedTruncated   bool                    `json:"expected_truncated"`
		ExpectedFirstFrame  string                  `json:"expected_first_frame"`
		ExpectedSecondFrame string                  `json:"expected_second_frame"`
		ExpectedThirdFrame  string                  `json:"expected_third_frame"`
		ExpectedFourthFrame string                  `json:"expected_fourth_frame"`
		ExpectedFifthFrame  string                  `json:"expected_fifth_frame"`
		ForbiddenSubstrings []string                `json:"forbidden_substrings"`
	} `json:"stack_sanitization_vectors"`

	MessageSanitizationVectors []struct {
		Name                string   `json:"name"`
		RawMessage          string   `json:"raw_message"`
		MaxChars            int      `json:"max_chars"`
		ForbiddenSubstrings []string `json:"forbidden_substrings"`
	} `json:"message_sanitization_vectors"`

	LeaseSnapshotVectors []struct {
		Name                        string                    `json:"name"`
		CurrentEpoch                string                    `json:"current_epoch"`
		CurrentVersion              int64                     `json:"current_version"`
		RTTMS                       int64                     `json:"rtt_ms"`
		MonotonicNowMS              int64                     `json:"monotonic_now_ms"`
		ExistingDeadlines           map[string]int64          `json:"existing_deadlines"`
		Snapshot                    diagnostics.LeaseSnapshot `json:"snapshot"`
		ExpectedAccepted            bool                      `json:"expected_accepted"`
		ExpectedReason              string                    `json:"expected_reason"`
		ExpectedActiveDiagnosticIDs []string                  `json:"expected_active_diagnostic_ids"`
		ExpectedDeadlineMonotonicMS int64                     `json:"expected_deadline_monotonic_ms"`
	} `json:"lease_snapshot_vectors"`
}

func loadFixtures(t *testing.T) ContractFixtures {
	t.Helper()
	path := filepath.Join("..", "..", "docs", "handoffs", "customer-connection-diagnostics", "references", "contract-fixtures.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read contract fixtures: %v", err)
	}
	var f ContractFixtures
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatalf("failed to unmarshal contract fixtures: %v", err)
	}
	return f
}

func TestIdentityVectors(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.IdentityVectors {
		t.Run(tc.Name, func(t *testing.T) {
			got, err := diagnostics.ComputeDiagnosticID(tc.PanelScope, tc.SubscriberUserID)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.ExpectedDiagnosticID {
				t.Errorf("got %q, want %q", got, tc.ExpectedDiagnosticID)
			}
		})
	}
}

func TestInvalidIdentityVectors(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.InvalidIdentityVectors {
		t.Run(tc.Name, func(t *testing.T) {
			_, err := diagnostics.ComputeDiagnosticID(tc.PanelScope, tc.SubscriberUserID)
			if err == nil {
				t.Fatalf("expected error %q, got nil", tc.ExpectedErrorCode)
			}
			if err.Error() != tc.ExpectedErrorCode {
				t.Errorf("got error %q, want %q", err.Error(), tc.ExpectedErrorCode)
			}
		})
	}
}

func TestEventIDVectors(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.EventIDVectors {
		t.Run(tc.Name, func(t *testing.T) {
			got := diagnostics.ComputeEventID(tc.PanelScope, tc.NodeID, tc.RuntimeID, tc.EventFamily, tc.OwnerID, tc.Sequence)
			if got != tc.ExpectedEventID {
				t.Errorf("got %q, want %q", got, tc.ExpectedEventID)
			}
		})
	}
}

func TestValidEvents(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.ValidEvents {
		t.Run(tc.Name, func(t *testing.T) {
			if err := diagnostics.ValidateEventJSON(tc.Payload); err != nil {
				t.Errorf("expected valid event, got err: %v", err)
			}
		})
	}
}

func TestInvalidEvents(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.InvalidEvents {
		t.Run(tc.Name, func(t *testing.T) {
			err := diagnostics.ValidateEventJSON(tc.Payload)
			if err == nil {
				t.Fatalf("expected invalid event (%s), got nil error", tc.ExpectedReason)
			}
			if !strings.Contains(err.Error(), tc.ExpectedReason) {
				t.Errorf("got error %q, expected it to contain %q", err.Error(), tc.ExpectedReason)
			}
		})
	}
}

func TestDomainNormalizationVectors(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.DomainNormalizationVectors {
		t.Run(tc.Input, func(t *testing.T) {
			got, ok := diagnostics.NormalizeDomain(tc.Input)
			if ok != tc.Valid {
				t.Fatalf("valid flag mismatch for %q: got %v, want %v (result=%q)", tc.Input, ok, tc.Valid, got)
			}
			if tc.Valid && tc.Expected != nil {
				if got != *tc.Expected {
					t.Errorf("normalized domain mismatch for %q: got %q, want %q", tc.Input, got, *tc.Expected)
				}
			}
		})
	}
}

func TestStackSanitizationVectors(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.StackSanitizationVectors {
		t.Run(tc.Name, func(t *testing.T) {
			res := diagnostics.SanitizeStack(tc.RawFrames)
			if res.FramesTotal != tc.ExpectedFramesTotal {
				t.Errorf("frames total: got %d, want %d", res.FramesTotal, tc.ExpectedFramesTotal)
			}
			if res.FramesKept != tc.ExpectedFramesKept {
				t.Errorf("frames kept: got %d, want %d", res.FramesKept, tc.ExpectedFramesKept)
			}
			if res.Truncated != tc.ExpectedTruncated {
				t.Errorf("truncated: got %v, want %v", res.Truncated, tc.ExpectedTruncated)
			}
			if len(res.FormattedFrames) > 0 && res.FormattedFrames[0] != tc.ExpectedFirstFrame {
				t.Errorf("frame 1: got %q, want %q", res.FormattedFrames[0], tc.ExpectedFirstFrame)
			}
			if len(res.FormattedFrames) > 1 && res.FormattedFrames[1] != tc.ExpectedSecondFrame {
				t.Errorf("frame 2: got %q, want %q", res.FormattedFrames[1], tc.ExpectedSecondFrame)
			}
			if len(res.FormattedFrames) > 2 && res.FormattedFrames[2] != tc.ExpectedThirdFrame {
				t.Errorf("frame 3: got %q, want %q", res.FormattedFrames[2], tc.ExpectedThirdFrame)
			}
			if len(res.FormattedFrames) > 3 && res.FormattedFrames[3] != tc.ExpectedFourthFrame {
				t.Errorf("frame 4: got %q, want %q", res.FormattedFrames[3], tc.ExpectedFourthFrame)
			}
			if len(res.FormattedFrames) > 4 && res.FormattedFrames[4] != tc.ExpectedFifthFrame {
				t.Errorf("frame 5: got %q, want %q", res.FormattedFrames[4], tc.ExpectedFifthFrame)
			}

			payload := res.SerializedPayload
			for _, forbidden := range tc.ForbiddenSubstrings {
				if strings.Contains(payload, forbidden) {
					t.Errorf("forbidden substring %q found in payload: %s", forbidden, payload)
				}
			}
		})
	}
}

func TestMessageSanitizationVectors(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.MessageSanitizationVectors {
		t.Run(tc.Name, func(t *testing.T) {
			clean := diagnostics.SanitizeMessage(tc.RawMessage, tc.MaxChars)
			for _, forbidden := range tc.ForbiddenSubstrings {
				if strings.Contains(clean, forbidden) {
					t.Errorf("forbidden substring %q found in sanitized message: %s", forbidden, clean)
				}
			}
		})
	}

	t.Run("no_false_positive_on_hex_words", func(t *testing.T) {
		msg := "bad gateway: dead connection with feedback"
		clean := diagnostics.SanitizeMessage(msg, 256)
		if clean != msg {
			t.Errorf("unexpected redaction of normal text: got %q, want %q", clean, msg)
		}
	})
}

func TestLeaseSnapshotVectors(t *testing.T) {
	f := loadFixtures(t)
	for _, tc := range f.LeaseSnapshotVectors {
		t.Run(tc.Name, func(t *testing.T) {
			res := diagnostics.EvaluateLeaseSnapshot(
				tc.CurrentEpoch,
				tc.CurrentVersion,
				tc.RTTMS,
				tc.MonotonicNowMS,
				tc.Snapshot,
				tc.ExistingDeadlines,
			)

			if res.Accepted != tc.ExpectedAccepted {
				t.Fatalf("accepted mismatch: got %v, want %v (reason=%q)", res.Accepted, tc.ExpectedAccepted, res.Reason)
			}
			if !tc.ExpectedAccepted && tc.ExpectedReason != "" {
				if res.Reason != tc.ExpectedReason {
					t.Errorf("reason mismatch: got %q, want %q", res.Reason, tc.ExpectedReason)
				}
			}
			if tc.ExpectedAccepted {
				if len(res.ActiveDiagnosticIDs) != len(tc.ExpectedActiveDiagnosticIDs) {
					t.Errorf("active IDs length: got %d, want %d", len(res.ActiveDiagnosticIDs), len(tc.ExpectedActiveDiagnosticIDs))
				}
				if tc.ExpectedDeadlineMonotonicMS > 0 && res.ExpectedDeadlineMonotonic != tc.ExpectedDeadlineMonotonicMS {
					t.Errorf("deadline mismatch: got %d, want %d", res.ExpectedDeadlineMonotonic, tc.ExpectedDeadlineMonotonicMS)
				}
			}
		})
	}
}
