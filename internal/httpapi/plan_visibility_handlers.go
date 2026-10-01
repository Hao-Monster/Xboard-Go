package httpapi

import (
	"net/http"
	"strconv"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) getPlanVisibility(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("id")
	if raw == "" {
		plans, err := s.store.ListPlanVisibility(r.Context())
		if err != nil {
			handleStoreError(w, err)
			return
		}
		writeSuccess(w, http.StatusOK, map[string]any{"plans": plans})
		return
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 1 {
		handleStoreError(w, store.ErrInvalidInput)
		return
	}
	plan, err := s.store.GetPlanVisibility(r.Context(), id)
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, map[string]any{"plan": plan})
}
func (s *server) searchPlanVisibilityUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.SearchPlanAudienceUsers(r.Context(), r.URL.Query().Get("audience"), r.URL.Query().Get("q"))
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, users)
}
func (s *server) savePlanVisibility(w http.ResponseWriter, r *http.Request) {
	var input store.SavePlanVisibilityInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.store.SavePlanVisibility(r.Context(), input, s.now()); err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, true)
}
