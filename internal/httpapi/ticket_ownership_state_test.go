package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestTicketHTTPUserOwnershipPreservesStateAfterUnauthorizedRequests(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "ownership-owner@example.test", "ownership-owner-password-123")
	createHTTPTestUser(t, database, "ownership-other@example.test", "ownership-other-password-123")
	owner := loginAs(t, api, "ownership-owner@example.test", "ownership-owner-password-123")
	other := loginAs(t, api, "ownership-other@example.test", "ownership-other-password-123")

	createdResponse := owner.request(t, api, http.MethodPost, "/api/v1/tickets", `{"subject":"Ownership state","level":1,"message":"Owner initial message"}`)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d", createdResponse.Code)
	}
	created := decodeTicketEnvelope(t, createdResponse)
	initial := ownerTicketSnapshot(t, owner, api, created.ID)

	unauthorizedRequests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "read", method: http.MethodGet, path: "/api/v1/tickets/" + ticketID(created.ID)},
		{name: "reply", method: http.MethodPost, path: "/api/v1/tickets/" + ticketID(created.ID) + "/messages", body: `{"message":"unauthorized message"}`},
		{name: "close", method: http.MethodPost, path: "/api/v1/tickets/" + ticketID(created.ID) + "/close", body: `{}`},
	}
	for _, request := range unauthorizedRequests {
		t.Run(request.name, func(t *testing.T) {
			response := other.request(t, api, request.method, request.path, request.body)
			expectAPIError(t, response, http.StatusNotFound, "not_found")
			assertTicketSnapshotUnchanged(t, initial, ownerTicketSnapshot(t, owner, api, created.ID))
		})
	}

	replied := owner.request(t, api, http.MethodPost, "/api/v1/tickets/"+ticketID(created.ID)+"/messages", `{"message":"Owner legitimate reply"}`)
	if replied.Code != http.StatusOK {
		t.Fatalf("owner reply status = %d", replied.Code)
	}
	afterReply := ownerTicketSnapshot(t, owner, api, created.ID)
	if afterReply.ID != initial.ID || afterReply.Status != store.TicketStatusOpen ||
		afterReply.ReplyStatus != store.TicketReplyWaiting || afterReply.MessageCount != initial.MessageCount+1 {
		t.Fatalf("owner reply snapshot = %#v", afterReply)
	}
}

type ticketOwnershipSnapshot struct {
	ID           int64
	Status       store.TicketStatus
	ReplyStatus  store.TicketReplyStatus
	MessageCount int
	MessageHash  string
}

func ownerTicketSnapshot(t *testing.T, owner testClient, api http.Handler, ticketID int64) ticketOwnershipSnapshot {
	t.Helper()
	response := owner.request(t, api, http.MethodGet, "/api/v1/tickets/"+ticketIDValue(ticketID), "")
	if response.Code != http.StatusOK {
		t.Fatalf("owner detail status = %d", response.Code)
	}
	ticket := decodeTicketEnvelope(t, response)
	return ticketOwnershipSnapshot{
		ID: ticket.ID, Status: ticket.Status, ReplyStatus: ticket.ReplyStatus,
		MessageCount: len(ticket.Messages), MessageHash: ticketMessageHash(ticket.Messages),
	}
}

func assertTicketSnapshotUnchanged(t *testing.T, want, got ticketOwnershipSnapshot) {
	t.Helper()
	if want != got {
		t.Fatalf("owner ticket snapshot changed: want=%#v got=%#v", want, got)
	}
}

func ticketMessageHash(messages []store.TicketMessage) string {
	digest := sha256.New()
	for _, message := range messages {
		fmt.Fprintf(digest, "%d:%d:%d:%s\x00", message.ID, message.TicketID, message.UserID, message.Message)
	}
	return hex.EncodeToString(digest.Sum(nil))
}

func ticketIDValue(value int64) string {
	return fmt.Sprintf("%d", value)
}
