package httpapi

import (
	"net/http"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

// Registered on the administrator mux so session, role, CSRF, path isolation,
// and audit middleware also apply to the financial transition endpoint.
func (s *server) registerCommissionWithdrawalRoutes(admin *http.ServeMux) {
	admin.HandleFunc("POST /api/v1/admin/tickets/{ticketID}/withdrawal", s.transitionCommissionWithdrawal)
}

func (s *server) transitionCommissionWithdrawal(w http.ResponseWriter, r *http.Request) {
	ticketID, ok := pathID(w, r, "ticketID")
	if !ok {
		return
	}
	var input store.CommissionWithdrawalTransitionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	session, _ := sessionFromContext(r.Context())
	if !s.allowTicketMutation(w, r, session.UserID) {
		return
	}
	if _, err := s.store.TransitionCommissionWithdrawal(r.Context(), session.UserID, ticketID, input, s.now()); err != nil {
		handleCommissionWithdrawalError(w, err, false)
		return
	}
	ticket, err := s.store.GetAdminTicket(r.Context(), ticketID)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, ticket)
}
