package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestTicketHTTPWorkflowEnforcesOwnershipAndLegacyClosedReplyRules(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "ticket-user@example.test", "ticket-user-password-123")
	createHTTPTestUser(t, database, "ticket-other@example.test", "ticket-other-password-123")
	user := loginAs(t, api, "ticket-user@example.test", "ticket-user-password-123")
	other := loginAs(t, api, "ticket-other@example.test", "ticket-other-password-123")
	admin := loginAdmin(t, api)

	createdResponse := user.request(t, api, http.MethodPost, "/api/v1/tickets", `{
		"subject":"Cannot connect","level":2,"message":"Initial message"
	}`)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", createdResponse.Code, createdResponse.Body)
	}
	created := decodeTicketEnvelope(t, createdResponse)
	if created.Status != store.TicketStatusOpen || created.ReplyStatus != store.TicketReplyWaiting {
		t.Fatalf("created ticket = %#v", created)
	}

	duplicate := user.request(t, api, http.MethodPost, "/api/v1/tickets", `{"subject":"duplicate","level":0,"message":"duplicate"}`)
	expectAPIError(t, duplicate, http.StatusConflict, "open_ticket_exists")

	list := user.request(t, api, http.MethodGet, "/api/v1/tickets?page=1&page_size=20", "")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d; body=%s", list.Code, list.Body)
	}
	var pageEnvelope struct {
		Data store.TicketPage `json:"data"`
	}
	decodeResponse(t, list, &pageEnvelope)
	if pageEnvelope.Data.Total != 1 || len(pageEnvelope.Data.Items) != 1 || pageEnvelope.Data.Items[0].ID != created.ID {
		t.Fatalf("ticket page = %#v", pageEnvelope.Data)
	}

	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/api/v1/tickets/" + ticketID(created.ID), ""},
		{http.MethodPost, "/api/v1/tickets/" + ticketID(created.ID) + "/messages", `{"message":"IDOR"}`},
		{http.MethodPost, "/api/v1/tickets/" + ticketID(created.ID) + "/close", `{}`},
	} {
		response := other.request(t, api, request.method, request.path, request.body)
		expectAPIError(t, response, http.StatusNotFound, "not_found")
	}

	replied := user.request(t, api, http.MethodPost, "/api/v1/tickets/"+ticketID(created.ID)+"/messages", `{"message":"User follow-up"}`)
	if replied.Code != http.StatusOK || decodeTicketEnvelope(t, replied).ReplyStatus != store.TicketReplyWaiting {
		t.Fatalf("user reply status = %d; body=%s", replied.Code, replied.Body)
	}
	closed := user.request(t, api, http.MethodPost, "/api/v1/tickets/"+ticketID(created.ID)+"/close", `{}`)
	if closed.Code != http.StatusOK || decodeTicketEnvelope(t, closed).Status != store.TicketStatusClosed {
		t.Fatalf("user close status = %d; body=%s", closed.Code, closed.Body)
	}
	closedReply := user.request(t, api, http.MethodPost, "/api/v1/tickets/"+ticketID(created.ID)+"/messages", `{"message":"must fail"}`)
	expectAPIError(t, closedReply, http.StatusConflict, "ticket_closed")

	adminReply := admin.request(t, api, http.MethodPost, "/api/v1/admin/tickets/"+ticketID(created.ID)+"/messages", `{"message":"Administrator answer"}`)
	if adminReply.Code != http.StatusOK {
		t.Fatalf("admin reply status = %d; body=%s", adminReply.Code, adminReply.Body)
	}
	answered := decodeTicketEnvelope(t, adminReply)
	if answered.Status != store.TicketStatusClosed || answered.ReplyStatus != store.TicketReplyAnswered {
		t.Fatalf("admin reply changed closed state: %#v", answered)
	}
	detailResponse := user.request(t, api, http.MethodGet, "/api/v1/tickets/"+ticketID(created.ID), "")
	detail := decodeTicketEnvelope(t, detailResponse)
	if len(detail.Messages) != 3 || detail.Messages[2].IsMe {
		t.Fatalf("ticket detail messages = %#v", detail.Messages)
	}

	next := user.request(t, api, http.MethodPost, "/api/v1/tickets", `{"subject":"Next issue","level":0,"message":"Allowed after close"}`)
	if next.Code != http.StatusCreated {
		t.Fatalf("new ticket after close status = %d; body=%s", next.Code, next.Body)
	}
}

func TestAdminTicketHTTPFiltersValidationAndRoleBoundary(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "filter-ticket@example.test", "filter-ticket-password-123")
	user := loginAs(t, api, "filter-ticket@example.test", "filter-ticket-password-123")
	admin := loginAdmin(t, api)

	created := user.request(t, api, http.MethodPost, "/api/v1/tickets", `{"subject":"Route outage","level":1,"message":"Help"}`)
	ticket := decodeTicketEnvelope(t, created)
	for _, query := range []string{
		"status=0&reply_status=0&level=1&query=Route",
		"status=0&reply_status=0&level=1&query=FILTER-TICKET%40EXAMPLE.TEST",
	} {
		response := admin.request(t, api, http.MethodGet, "/api/v1/admin/tickets?page=1&page_size=20&"+query, "")
		if response.Code != http.StatusOK {
			t.Fatalf("admin list status = %d; body=%s", response.Code, response.Body)
		}
		var envelope struct {
			Data store.TicketPage `json:"data"`
		}
		decodeResponse(t, response, &envelope)
		if envelope.Data.Total != 1 || envelope.Data.Items[0].ID != ticket.ID || envelope.Data.Items[0].UserEmail != "filter-ticket@example.test" {
			t.Fatalf("admin ticket page = %#v", envelope.Data)
		}
	}

	nonAdminList := user.request(t, api, http.MethodGet, "/api/v1/admin/tickets", "")
	expectAPIError(t, nonAdminList, http.StatusForbidden, "forbidden")
	for _, path := range []string{
		"/api/v1/tickets?page_size=101",
		"/api/v1/admin/tickets?status=2",
		"/api/v1/admin/tickets?level=-1",
	} {
		client := user
		if len(path) >= len("/api/v1/admin/") && path[:len("/api/v1/admin/")] == "/api/v1/admin/" {
			client = admin
		}
		response := client.request(t, api, http.MethodGet, path, "")
		expectAPIError(t, response, http.StatusUnprocessableEntity, "validation_failed")
	}
	unknown := user.request(t, api, http.MethodPost, "/api/v1/tickets", `{"subject":"x","level":0,"message":"x","admin":true}`)
	expectAPIError(t, unknown, http.StatusBadRequest, "invalid_json")

	closed := admin.request(t, api, http.MethodPost, "/api/v1/admin/tickets/"+ticketID(ticket.ID)+"/close", `{}`)
	if closed.Code != http.StatusOK || decodeTicketEnvelope(t, closed).Status != store.TicketStatusClosed {
		t.Fatalf("admin close status = %d; body=%s", closed.Code, closed.Body)
	}
}

func TestTicketMutationsAreRateLimitedPerAuthenticatedUser(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "ticket-rate@example.test", "ticket-rate-password-123")
	user := loginAs(t, api, "ticket-rate@example.test", "ticket-rate-password-123")
	created := user.request(t, api, http.MethodPost, "/api/v1/tickets", `{"subject":"rate limit","level":0,"message":"initial"}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d; body=%s", created.Code, created.Body)
	}
	ticket := decodeTicketEnvelope(t, created)
	for attempt := 1; attempt < 60; attempt++ {
		response := user.request(t, api, http.MethodPost, "/api/v1/tickets/"+ticketID(ticket.ID)+"/messages", `{"message":"bounded reply"}`)
		if response.Code != http.StatusOK {
			t.Fatalf("reply %d status = %d; body=%s", attempt, response.Code, response.Body)
		}
	}
	limited := user.request(t, api, http.MethodPost, "/api/v1/tickets/"+ticketID(ticket.ID)+"/messages", `{"message":"must be limited"}`)
	expectAPIError(t, limited, http.StatusTooManyRequests, "ticket_rate_limited")
	if limited.Header().Get("Retry-After") != "60" {
		t.Fatalf("Retry-After = %q, want 60", limited.Header().Get("Retry-After"))
	}
}

func createHTTPTestUser(t *testing.T, database *store.Store, email, password string) {
	t.Helper()
	hasher := security.NewPasswordHasher(security.PasswordParams{
		MemoryKiB: 8 * 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32,
	})
	passwordHash, err := hasher.Hash(password)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAdminUser(context.Background(), store.CreateAdminUserInput{Email: email, PasswordHash: passwordHash}, fixedNow()); err != nil {
		t.Fatalf("CreateAdminUser(%q) error = %v", email, err)
	}
}

func decodeTicketEnvelope(t *testing.T, response *httptest.ResponseRecorder) store.Ticket {
	t.Helper()
	var envelope struct {
		Data store.Ticket `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode ticket response: %v; body=%s", err, response.Body)
	}
	return envelope.Data
}

func expectAPIError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	var envelope struct {
		Status string `json:"status"`
		Error  struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeResponse(t, response, &envelope)
	if response.Code != status || envelope.Status != "fail" || envelope.Error.Code != code {
		t.Fatalf("API error status=%d payload=%#v, want status=%d code=%q; body=%s", response.Code, envelope, status, code, response.Body)
	}
}

func ticketID(value int64) string { return strconv.FormatInt(value, 10) }
