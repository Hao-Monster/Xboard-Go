package httpapi

import (
	"context"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func (s *server) notifyEntitlementChange(ctx context.Context, change *store.EntitlementChange) {
	if s.hub == nil || change == nil {
		return
	}
	s.hub.NotifyUserMutation(ctx, change.UserID, change.PreviousUUID, change.OldGroupID, change.NewGroupID, true)
}

func (h *lifecycleHandler) NotifyEntitlementChange(ctx context.Context, change *store.EntitlementChange) {
	if h.notifyEntitlementChange != nil {
		h.notifyEntitlementChange(ctx, change)
	}
}
