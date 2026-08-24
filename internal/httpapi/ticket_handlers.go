package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listUserTickets(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := ticketPageQuery(w, r)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	tickets, err := s.store.ListUserTickets(r.Context(), session.UserID, page, pageSize)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, tickets)
}

func (s *server) createTicket(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Subject string            `json:"subject"`
		Level   store.TicketLevel `json:"level"`
		Message string            `json:"message"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowTicketMutation(w, r, session.UserID) {
		return
	}
	ticket, err := s.store.CreateTicket(r.Context(), session.UserID, store.SaveTicketInput{
		Subject: input.Subject, Level: input.Level, Message: input.Message,
	}, s.now())
	if err != nil {
		handleTicketError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, ticket)
}

func (s *server) getUserTicket(w http.ResponseWriter, r *http.Request) {
	ticketID, ok := pathID(w, r, "ticketID")
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	ticket, err := s.store.GetUserTicket(r.Context(), session.UserID, ticketID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, ticket)
}

func (s *server) replyUserTicket(w http.ResponseWriter, r *http.Request) {
	ticketID, ok := pathID(w, r, "ticketID")
	if !ok {
		return
	}
	message, ok := decodeTicketReply(w, r)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowTicketMutation(w, r, session.UserID) {
		return
	}
	_, err := s.store.ReplyTicketAsUser(r.Context(), session.UserID, ticketID, message, s.now())
	if err != nil {
		handleTicketError(w, err)
		return
	}
	ticket, err := s.store.GetUserTicket(r.Context(), session.UserID, ticketID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, ticket)
}

func (s *server) closeUserTicket(w http.ResponseWriter, r *http.Request) {
	ticketID, ok := pathID(w, r, "ticketID")
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowTicketMutation(w, r, session.UserID) {
		return
	}
	_, err := s.store.CloseTicketAsUser(r.Context(), session.UserID, ticketID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	ticket, err := s.store.GetUserTicket(r.Context(), session.UserID, ticketID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, ticket)
}

func (s *server) listAdminTickets(w http.ResponseWriter, r *http.Request) {
	page, pageSize, ok := ticketPageQuery(w, r)
	if !ok {
		return
	}
	filter := store.TicketFilter{Page: page, PageSize: pageSize, Query: r.URL.Query().Get("query")}
	if filter.Status, ok = optionalTicketStatus(w, r, "status"); !ok {
		return
	}
	if filter.ReplyStatus, ok = optionalTicketReplyStatus(w, r, "reply_status"); !ok {
		return
	}
	if filter.Level, ok = optionalTicketLevel(w, r, "level"); !ok {
		return
	}
	tickets, err := s.store.ListAdminTickets(r.Context(), filter)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, tickets)
}

func (s *server) getAdminTicket(w http.ResponseWriter, r *http.Request) {
	ticketID, ok := pathID(w, r, "ticketID")
	if !ok {
		return
	}
	ticket, err := s.store.GetAdminTicket(r.Context(), ticketID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, ticket)
}

func (s *server) replyAdminTicket(w http.ResponseWriter, r *http.Request) {
	ticketID, ok := pathID(w, r, "ticketID")
	if !ok {
		return
	}
	message, ok := decodeTicketReply(w, r)
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowTicketMutation(w, r, session.UserID) {
		return
	}
	_, err := s.store.ReplyTicketAsAdmin(r.Context(), session.UserID, ticketID, message, s.now())
	if err != nil {
		handleTicketError(w, err)
		return
	}
	ticket, err := s.store.GetAdminTicket(r.Context(), ticketID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, ticket)
}

func (s *server) closeAdminTicket(w http.ResponseWriter, r *http.Request) {
	ticketID, ok := pathID(w, r, "ticketID")
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowTicketMutation(w, r, session.UserID) {
		return
	}
	_, err := s.store.CloseTicketAsAdmin(r.Context(), ticketID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	ticket, err := s.store.GetAdminTicket(r.Context(), ticketID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, ticket)
}

func (s *server) allowTicketMutation(w http.ResponseWriter, r *http.Request, userID int64) bool {
	if s.ticketRequests.allow(r, userID, s.now()) {
		return true
	}
	w.Header().Set("Retry-After", "60")
	writeAPIError(w, http.StatusTooManyRequests, "ticket_rate_limited", "工单操作过于频繁，请稍后重试", nil)
	return false
}

func decodeTicketReply(w http.ResponseWriter, r *http.Request) (string, bool) {
	var input struct {
		Message string `json:"message"`
	}
	if !decodeJSON(w, r, &input) {
		return "", false
	}
	return input.Message, true
}

func ticketPageQuery(w http.ResponseWriter, r *http.Request) (int, int, bool) {
	page, ok := ticketQueryInt(w, r, "page", 1)
	if !ok {
		return 0, 0, false
	}
	pageSize, ok := ticketQueryInt(w, r, "page_size", 20)
	if !ok || pageSize > 100 {
		if ok {
			writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "page_size 必须在 1 到 100 之间", map[string]string{"page_size": "超出范围"})
		}
		return 0, 0, false
	}
	return page, pageSize, true
}

func ticketQueryInt(w http.ResponseWriter, r *http.Request, name string, fallback int) (int, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return fallback, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", name+" 必须为正整数", map[string]string{name: "格式无效"})
		return 0, false
	}
	return value, true
}

func optionalTicketStatus(w http.ResponseWriter, r *http.Request, name string) (*store.TicketStatus, bool) {
	value, set, ok := optionalTicketEnum(w, r, name, int(store.TicketStatusClosed))
	if !set || !ok {
		return nil, ok
	}
	status := store.TicketStatus(value)
	return &status, true
}

func optionalTicketReplyStatus(w http.ResponseWriter, r *http.Request, name string) (*store.TicketReplyStatus, bool) {
	value, set, ok := optionalTicketEnum(w, r, name, int(store.TicketReplyAnswered))
	if !set || !ok {
		return nil, ok
	}
	status := store.TicketReplyStatus(value)
	return &status, true
}

func optionalTicketLevel(w http.ResponseWriter, r *http.Request, name string) (*store.TicketLevel, bool) {
	value, set, ok := optionalTicketEnum(w, r, name, int(store.TicketLevelHigh))
	if !set || !ok {
		return nil, ok
	}
	level := store.TicketLevel(value)
	return &level, true
}

func optionalTicketEnum(w http.ResponseWriter, r *http.Request, name string, maximum int) (int, bool, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return 0, false, true
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 || value > maximum {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", name+" 格式无效", map[string]string{name: "格式无效"})
		return 0, true, false
	}
	return value, true, true
}

func handleTicketError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrOpenTicketExists):
		writeAPIError(w, http.StatusConflict, "open_ticket_exists", "存在未关闭的工单", nil)
	case errors.Is(err, store.ErrTicketClosed):
		writeAPIError(w, http.StatusConflict, "ticket_closed", "工单已关闭，无法回复", nil)
	case errors.Is(err, store.ErrTicketMessageLimit):
		writeAPIError(w, http.StatusConflict, "ticket_message_limit", "工单消息已达到上限，请新建工单继续联系", nil)
	default:
		handleStoreError(w, err)
	}
}
