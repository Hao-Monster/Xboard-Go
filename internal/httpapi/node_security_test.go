package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestSECNODE005HandshakeBodyBoundary(t *testing.T) {
	api, database := newTestAPI(t)
	machine, enrollment, err := database.CreateMachine(context.Background(), store.CreateMachineInput{
		Name: "node-security-handshake", IsActive: true,
	}, fixedNow())
	if err != nil {
		t.Fatal(err)
	}
	credential, err := database.ExchangeEnrollment(context.Background(), machine.ID, enrollment.Code, fixedNow())
	if err != nil {
		t.Fatal(err)
	}

	const legacyHandshakeBodyLimit = 64 << 10
	base := fmt.Sprintf(`{"machine_id":%d}`, machine.ID)
	for _, size := range []int{legacyHandshakeBodyLimit - 1, legacyHandshakeBodyLimit} {
		response := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", credential.Token, paddedNodeSecurityJSON(t, base, size))
		if response.Code != http.StatusOK {
			t.Fatalf("handshake body size %d status=%d, want %d; body=%s", size, response.Code, http.StatusOK, response.Body)
		}
	}

	oversized := agentRequest(api, http.MethodPost, "/api/v2/server/handshake", credential.Token,
		paddedNodeSecurityJSON(t, base, legacyHandshakeBodyLimit+1))
	expectAPIError(t, oversized, http.StatusRequestEntityTooLarge, "request_too_large")
}

func paddedNodeSecurityJSON(t *testing.T, base string, size int) string {
	t.Helper()
	if len(base) > size {
		t.Fatalf("base JSON is %d bytes, larger than requested %d-byte body", len(base), size)
	}
	return base + strings.Repeat(" ", size-len(base))
}
