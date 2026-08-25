package httpapi

import (
	"errors"
	"net/http"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listAdminPlans(w http.ResponseWriter, r *http.Request) {
	plans, err := s.store.ListPlans(r.Context(), s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, adminPlanResponses(plans))
}

func (s *server) listGuestPlans(w http.ResponseWriter, r *http.Request) {
	offers, err := s.store.ListGuestPlanOffers(r.Context(), s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, offers)
}

func (s *server) listUserPlans(w http.ResponseWriter, r *http.Request) {
	session, _ := sessionFromContext(r.Context())
	offers, err := s.store.ListUserPlanOffers(r.Context(), session.UserID, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, offers)
}

func (s *server) createPlan(w http.ResponseWriter, r *http.Request) {
	input, _, _, ok := decodePlanInput(w, r, false)
	if !ok {
		return
	}
	plan, err := s.store.CreatePlan(r.Context(), input, s.now())
	if err != nil {
		handlePlanMutationError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, adminPlanResponseOf(plan))
}

func (s *server) updatePlan(w http.ResponseWriter, r *http.Request) {
	planID, ok := pathID(w, r, "planID")
	if !ok {
		return
	}
	input, revision, forceUpdate, ok := decodePlanInput(w, r, true)
	if !ok {
		return
	}
	plan, err := s.store.UpdatePlan(r.Context(), planID, revision, input, forceUpdate, s.now())
	if err != nil {
		handlePlanMutationError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, adminPlanResponseOf(plan))
}

func (s *server) setPlanState(w http.ResponseWriter, r *http.Request) {
	planID, ok := pathID(w, r, "planID")
	if !ok {
		return
	}
	var input struct {
		Revision int64 `json:"revision"`
		Show     bool  `json:"show"`
		Sell     bool  `json:"sell"`
		Renew    bool  `json:"renew"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Revision < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "revision 必须为正整数", nil)
		return
	}
	plan, err := s.store.SetPlanState(r.Context(), planID, input.Revision, store.PlanState{
		Show: input.Show, Sell: input.Sell, Renew: input.Renew,
	}, s.now())
	if err != nil {
		handlePlanMutationError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, adminPlanResponseOf(plan))
}

func (s *server) reorderPlans(w http.ResponseWriter, r *http.Request) {
	var input struct {
		IDs []int64 `json:"ids"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	plans, err := s.store.ReorderPlans(r.Context(), input.IDs, s.now())
	if err != nil {
		handlePlanMutationError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, adminPlanResponses(plans))
}

type adminPlanResponse struct {
	store.Plan
	UsersCount         int64 `json:"users_count"`
	ActiveUsersCount   int64 `json:"active_users_count"`
	CapacityUsersCount int64 `json:"capacity_users_count"`
}

func adminPlanResponseOf(plan store.Plan) adminPlanResponse {
	return adminPlanResponse{
		Plan: plan, UsersCount: plan.UsersCount, ActiveUsersCount: plan.ActiveUsersCount,
		CapacityUsersCount: plan.CapacityUsersCount,
	}
}

func adminPlanResponses(plans []store.Plan) []adminPlanResponse {
	result := make([]adminPlanResponse, 0, len(plans))
	for _, plan := range plans {
		result = append(result, adminPlanResponseOf(plan))
	}
	return result
}

func (s *server) deletePlan(w http.ResponseWriter, r *http.Request) {
	planID, ok := pathID(w, r, "planID")
	if !ok {
		return
	}
	if err := s.store.DeletePlan(r.Context(), planID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeAPIError(w, http.StatusConflict, "plan_in_use", "套餐仍被用户或业务记录使用，无法删除", nil)
			return
		}
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodePlanInput(w http.ResponseWriter, r *http.Request, requireRevision bool) (store.SavePlanInput, int64, bool, bool) {
	var input struct {
		Revision           int64            `json:"revision,omitempty"`
		GroupID            *int64           `json:"group_id"`
		TransferEnableGiB  int64            `json:"transfer_enable"`
		Name               string           `json:"name"`
		SpeedLimit         *int             `json:"speed_limit"`
		Content            string           `json:"content"`
		ResetTrafficMethod *int             `json:"reset_traffic_method"`
		CapacityLimit      *int             `json:"capacity_limit"`
		Prices             store.PlanPrices `json:"prices"`
		DeviceLimit        *int             `json:"device_limit"`
		Tags               []string         `json:"tags"`
		ForceUpdate        bool             `json:"force_update,omitempty"`
	}
	if !decodeJSON(w, r, &input) {
		return store.SavePlanInput{}, 0, false, false
	}
	if requireRevision && input.Revision < 1 {
		writeAPIError(w, http.StatusUnprocessableEntity, "validation_failed", "revision 必须为正整数", nil)
		return store.SavePlanInput{}, 0, false, false
	}
	return store.SavePlanInput{
		GroupID: input.GroupID, TransferEnableGiB: input.TransferEnableGiB, Name: input.Name,
		SpeedLimit: input.SpeedLimit, Content: input.Content, ResetTrafficMethod: input.ResetTrafficMethod,
		CapacityLimit: input.CapacityLimit, Prices: input.Prices, DeviceLimit: input.DeviceLimit, Tags: input.Tags,
	}, input.Revision, input.ForceUpdate, true
}

func handlePlanMutationError(w http.ResponseWriter, err error) {
	if errors.Is(err, store.ErrRevisionConflict) {
		writeAPIError(w, http.StatusConflict, "plan_conflict", "套餐已被其他操作修改，请刷新后重试", nil)
		return
	}
	if errors.Is(err, store.ErrConflict) {
		writeAPIError(w, http.StatusConflict, "plan_conflict", "套餐目录已发生变化，请刷新后重试", nil)
		return
	}
	handleStoreError(w, err)
}
