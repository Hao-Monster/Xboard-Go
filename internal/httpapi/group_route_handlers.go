package httpapi

import (
	"errors"
	"net/http"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) listServerGroups(w http.ResponseWriter, r *http.Request) {
	groups, err := s.store.ListServerGroups(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, groups)
}

func (s *server) createServerGroup(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	group, err := s.store.CreateServerGroup(r.Context(), input.Name, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, group)
}

func (s *server) updateServerGroup(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(w, r, "groupID")
	if !ok {
		return
	}
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	group, err := s.store.UpdateServerGroup(r.Context(), groupID, input.Name, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, group)
}

func (s *server) deleteServerGroup(w http.ResponseWriter, r *http.Request) {
	groupID, ok := pathID(w, r, "groupID")
	if !ok {
		return
	}
	if err := s.store.DeleteServerGroup(r.Context(), groupID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeAPIError(w, http.StatusConflict, "group_in_use", "权限组仍被用户、套餐或节点使用，无法删除", nil)
			return
		}
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) listRoutingRules(w http.ResponseWriter, r *http.Request) {
	rules, err := s.store.ListRoutingRules(r.Context())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusOK, rules)
}

func (s *server) createRoutingRule(w http.ResponseWriter, r *http.Request) {
	input, ok := decodeRoutingRuleInput(w, r)
	if !ok {
		return
	}
	rule, err := s.store.CreateRoutingRule(r.Context(), input, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	writeSuccess(w, http.StatusCreated, rule)
}

func (s *server) updateRoutingRule(w http.ResponseWriter, r *http.Request) {
	routeID, ok := pathID(w, r, "routeID")
	if !ok {
		return
	}
	input, ok := decodeRoutingRuleInput(w, r)
	if !ok {
		return
	}
	rule, err := s.store.UpdateRoutingRule(r.Context(), routeID, input, s.now())
	if err != nil {
		handleStoreError(w, err)
		return
	}
	if s.hub != nil {
		targets, targetErr := s.store.ListRoutingRuleTargets(r.Context(), routeID)
		if targetErr != nil {
			s.logger.Error("list routing rule sync targets", "route_id", routeID, "error", targetErr)
		} else {
			for _, target := range targets {
				s.hub.NotifyNodeConfig(r.Context(), target.MachineID, target.NodeID)
			}
		}
	}
	writeSuccess(w, http.StatusOK, rule)
}

func (s *server) deleteRoutingRule(w http.ResponseWriter, r *http.Request) {
	routeID, ok := pathID(w, r, "routeID")
	if !ok {
		return
	}
	if err := s.store.DeleteRoutingRule(r.Context(), routeID); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeAPIError(w, http.StatusConflict, "routing_rule_in_use", "路由规则仍被节点使用，无法删除", nil)
			return
		}
		handleStoreError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func decodeRoutingRuleInput(w http.ResponseWriter, r *http.Request) (store.SaveRoutingRuleInput, bool) {
	var input struct {
		Remarks     string   `json:"remarks"`
		Match       []string `json:"match"`
		Action      string   `json:"action"`
		ActionValue string   `json:"action_value"`
	}
	if !decodeJSON(w, r, &input) {
		return store.SaveRoutingRuleInput{}, false
	}
	return store.SaveRoutingRuleInput{
		Remarks: input.Remarks, Match: input.Match, Action: input.Action, ActionValue: input.ActionValue,
	}, true
}
