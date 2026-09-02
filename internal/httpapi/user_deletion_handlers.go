package httpapi

import (
	"errors"
	"net/http"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) getAdminUserDeletionImpact(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	session, _ := sessionFromContext(r.Context())
	impact, err := s.store.GetAdminUserDeletionImpact(r.Context(), session.UserID, userID)
	if err != nil {
		writeUserDeletionError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, impact)
}

func (s *server) requestAdminUserDeletion(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
		Confirm  bool  `json:"confirm"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision < 1 || !input.Confirm {
		writeAPIError(w, http.StatusUnprocessableEntity, "confirmation_required", "需要当前 revision 和二次确认", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.RequestAdminUserDeletion(r.Context(), session.UserID, userID, input.Revision, s.now())
	if err != nil {
		writeUserDeletionError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func (s *server) restoreAdminUser(w http.ResponseWriter, r *http.Request) {
	userID, ok := pathID(w, r, "userID")
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
		Confirm  bool  `json:"confirm"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision < 1 || !input.Confirm {
		writeAPIError(w, http.StatusUnprocessableEntity, "confirmation_required", "需要当前 revision 和二次确认", nil)
		return
	}
	session, _ := sessionFromContext(r.Context())
	updated, err := s.store.RestoreAdminUser(r.Context(), session.UserID, userID, input.Revision, s.now())
	if err != nil {
		writeUserDeletionError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, updated)
}

func writeUserDeletionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrUserDeletionSelf):
		writeAPIError(w, http.StatusConflict, "cannot_delete_self", "管理员不能删除当前登录账号", nil)
	case errors.Is(err, store.ErrUserDeletionBlocked):
		writeAPIError(w, http.StatusConflict, "user_deletion_blocked", "用户仍承担受保护责任，不能进入删除流程", nil)
	case errors.Is(err, store.ErrUserDeletionState):
		writeAPIError(w, http.StatusConflict, "user_deletion_state", "用户删除生命周期不允许此操作", nil)
	default:
		handleStoreError(w, err)
	}
}
