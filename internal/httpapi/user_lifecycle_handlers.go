package httpapi

import (
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"net/http"
)

func (s *server) deactivateAdminUser(w http.ResponseWriter, r *http.Request) {
	s.changeAdminUserLifecycle(w, r, "deactivate")
}
func (s *server) restoreAdminUser(w http.ResponseWriter, r *http.Request) {
	s.changeAdminUserLifecycle(w, r, "restore")
}
func (s *server) anonymizeAdminUser(w http.ResponseWriter, r *http.Request) {
	s.changeAdminUserLifecycle(w, r, "anonymize")
}

func (s *server) getAdminUserLifecycleImpact(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	impact, err := s.store.GetUserLifecycleImpact(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, impact)
}

func (s *server) changeAdminUserLifecycle(w http.ResponseWriter, r *http.Request, action string) {
	id, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return
	}
	s.applyAdminUserLifecycle(w, r, id, input.Revision, action, false)
}

// Legacy destroy now enters the authorized recovery workflow rather than
// cascading through the user's orders and tickets.
func (s *server) legacyDestroyAdminUser(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID int64 `json:"id"`
	}
	if !decodeJSONLimit(w, r, &input, 1024) {
		return
	}
	account, err := s.store.GetAdminUser(r.Context(), input.ID)
	if err != nil {
		writeLegacyAdminUserError(w, err)
		return
	}
	if account.LifecycleStatus != "active" {
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	s.applyAdminUserLifecycle(w, r, input.ID, account.Revision, "deactivate", true)
}

func (s *server) applyAdminUserLifecycle(w http.ResponseWriter, r *http.Request, id, revision int64, action string, legacy bool) {
	session, ok := sessionFromContext(r.Context())
	if !ok || !session.IsAdmin || session.Banned {
		writeAPIError(w, http.StatusForbidden, "admin_required", "需要管理员权限", nil)
		return
	}
	result, mutation, err := s.store.ChangeUserLifecycle(r.Context(), store.UserLifecycleInput{AdministratorID: session.UserID, UserID: id, Revision: revision, Action: action}, s.now())
	if err != nil {
		if legacy {
			writeLegacyAdminUserError(w, err)
		} else {
			handleStoreError(w, err)
		}
		return
	}
	if s.hub != nil && mutation.RuntimeChanged {
		s.hub.NotifyUserMutation(r.Context(), id, mutation.UUID, mutation.OldGroupID, mutation.NewGroupID, true)
	}
	if legacy {
		writeLegacySuccess(w, http.StatusOK, true)
		return
	}
	writeSuccess(w, http.StatusOK, result)
}
