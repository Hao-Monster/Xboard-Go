package httpapi

import (
	"context"
	"net/http"
	"strconv"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

// admitSubscriptionDelivery keeps all node-delivery entry points on the same
// account budget, render capacity and distributor device/claim checks.
func (s *server) admitSubscriptionDelivery(w http.ResponseWriter, r *http.Request, account store.SubscriptionAccount) (hwid store.DistributorHWIDAuthorization, release func(), ok bool) {
	if !s.subscriptionRequests.take(strconv.FormatInt(account.ID, 10), s.now()) {
		s.runtimeTracker.MarkSubscriptionRateLimited()
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("Retry-After", "60")
		writeAPIError(w, http.StatusTooManyRequests, "subscription_rate_limited", "订阅请求过于频繁，请稍后重试", nil)
		return hwid, nil, false
	}
	select {
	case s.subscriptionRenderSlots <- struct{}{}:
	default:
		s.runtimeTracker.MarkSubscriptionBusy()
		w.Header().Set("Cache-Control", "no-store, private")
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, http.StatusServiceUnavailable, "subscription_busy", "订阅服务繁忙，请稍后重试", nil)
		return hwid, nil, false
	}
	s.runtimeTracker.BeginSubscriptionRender()
	release = func() {
		s.runtimeTracker.EndSubscriptionRender()
		<-s.subscriptionRenderSlots
	}
	// Only successful admission transfers ownership of the slot to the caller.
	defer func() {
		if !ok {
			release()
		}
	}()
	var err error
	hwid, err = s.store.AuthorizeDistributorHWID(r.Context(), store.AuthorizeDistributorHWIDInput{
		SubscriberUserID: account.ID, HWID: r.Header.Get("x-hwid"), DeviceOS: r.Header.Get("x-device-os"),
		OSVersion: r.Header.Get("x-ver-os"), DeviceModel: r.Header.Get("x-device-model"),
		UserAgent: r.UserAgent(), IPAddress: requestIP(r),
	}, s.now())
	if err != nil {
		handleStoreError(w, err)
		return hwid, release, false
	}
	setDistributorHWIDHeaders(w, hwid)
	if !hwid.Allowed {
		w.WriteHeader(http.StatusNotFound)
		return hwid, release, false
	}
	if hwid.SubscriptionID > 0 {
		if err := s.store.MarkDistributorSubscriptionClaimed(r.Context(), hwid.SubscriptionID, requestIP(r), r.UserAgent(), s.now()); err != nil {
			handleStoreError(w, err)
			return hwid, release, false
		}
	}
	return hwid, release, true
}

func (s *server) markSubscriptionConfigIssued(ctx context.Context, hwid store.DistributorHWIDAuthorization) error {
	if hwid.SubscriptionID == 0 {
		return nil
	}
	return s.store.MarkDistributorConfigIssued(ctx, hwid.SubscriptionID, s.now())
}
