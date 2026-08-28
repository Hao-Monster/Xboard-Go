package nodecoord

import (
	"strings"
	"testing"
)

func TestDecodeEventRejectsMalformedOrUnboundedInput(t *testing.T) {
	valid := `{"version":1,"kind":"node_config","source":"peer","machine_id":7,"node_id":9}`
	decoded, err := decodeEvent(valid)
	if err != nil || decoded.Kind != EventNodeConfig || decoded.MachineID != 7 || decoded.NodeID != 9 {
		t.Fatalf("decodeEvent(valid) = (%#v, %v)", decoded, err)
	}
	for name, payload := range map[string]string{
		"empty":          "",
		"unknown field":  `{"version":1,"kind":"node_config","source":"peer","machine_id":7,"node_id":9,"token":"secret"}`,
		"trailing JSON":  valid + `{}`,
		"wrong version":  `{"version":2,"kind":"node_config","source":"peer","machine_id":7,"node_id":9}`,
		"invalid target": `{"version":1,"kind":"node_config","source":"peer","machine_id":7,"node_id":0}`,
		"unknown kind":   `{"version":1,"kind":"unknown","source":"peer"}`,
		"oversized":      strings.Repeat("x", maxEventBytes+1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeEvent(payload); err == nil {
				t.Fatal("decodeEvent() accepted invalid coordination input")
			}
		})
	}
}

func TestValidateLeaseNormalizesIDsAndRejectsInvalidIdentity(t *testing.T) {
	nodes, err := validateLease(7, []int64{9, 3, 9, 5}, "revision:0123456789abcdef", false)
	if err != nil {
		t.Fatal(err)
	}
	if got := []int64{3, 5, 9}; len(nodes) != len(got) || nodes[0] != got[0] || nodes[1] != got[1] || nodes[2] != got[2] {
		t.Fatalf("validateLease() nodes = %v, want %v", nodes, got)
	}
	for name, testCase := range map[string]struct {
		machineID    int64
		nodeIDs      []int64
		connectionID string
	}{
		"machine":     {0, []int64{1}, "connection"},
		"empty nodes": {1, nil, "connection"},
		"node":        {1, []int64{0}, "connection"},
		"connection":  {1, []int64{1}, " connection"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := validateLease(testCase.machineID, testCase.nodeIDs, testCase.connectionID, false); err == nil {
				t.Fatal("validateLease() accepted invalid identity")
			}
		})
	}
}

func TestSanitizeRevisionBoundsAndRemovesUnsafeCharacters(t *testing.T) {
	value := sanitizeRevision(" release/2026 secret@" + strings.Repeat("x", 200))
	if value == "" || len(value) > 128 || strings.ContainsAny(value, " /@") {
		t.Fatalf("sanitizeRevision() = %q", value)
	}
}
