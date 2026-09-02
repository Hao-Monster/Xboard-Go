package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestCommissionWithdrawalAPIEncryptsAccountAndEnforcesAdminStateMachine(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "withdraw-api@example.test", "withdraw-api-password-123")
	userRecord, err := database.FindUserByEmail(context.Background(), "withdraw-api@example.test")
	if err != nil {
		t.Fatal(err)
	}
	adminRecord, err := database.FindUserByEmail(context.Background(), "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.GetAdminUser(context.Background(), userRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	commissionBalance := int64(15000)
	if _, _, err := database.UpdateAdminUser(context.Background(), target.ID, store.UpdateAdminUserInput{
		AdministratorID: adminRecord.ID, Revision: target.Revision, Email: target.Email, GroupID: target.GroupID, TransferEnable: target.TransferEnable,
		ExpiredAt: target.ExpiredAt, SpeedLimit: target.SpeedLimit, DeviceLimit: target.DeviceLimit, Banned: target.Banned,
		CommissionBalance: &commissionBalance,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetCommissionSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	limit := store.CurrencyAmount(10000)
	methods := []string{"USDT"}
	if _, err := database.UpdateCommissionSettings(context.Background(), adminRecord.ID, settings.Revision, store.SaveCommissionSettingsInput{
		InviteCommission: settings.InviteCommission, FirstTimeEnabled: settings.FirstTimeEnabled, AutoCheckEnabled: settings.AutoCheckEnabled,
		WithdrawLimit: &limit, WithdrawMethods: &methods, WithdrawClosed: settings.WithdrawClosed,
		DistributionEnabled: settings.DistributionEnabled, DistributionL1: settings.DistributionL1, DistributionL2: settings.DistributionL2, DistributionL3: settings.DistributionL3,
	}, fixedNow()); err != nil {
		t.Fatal(err)
	}
	user := loginAs(t, api, "withdraw-api@example.test", "withdraw-api-password-123")
	admin := loginAdmin(t, api)

	policy := user.request(t, api, http.MethodGet, "/api/v1/commission-withdrawals/policy", "")
	if policy.Code != http.StatusOK || !containsAll(policy.Body.String(), `"minimum_amount":10000`, `"methods":["USDT"]`, `"available_commission":15000`) {
		t.Fatalf("withdrawal policy status=%d body=%s", policy.Code, policy.Body)
	}
	account := "wallet-user-123456789"
	created := user.request(t, api, http.MethodPost, "/api/v1/commission-withdrawals", `{"idempotency_key":"test-0000000000000001","method":"USDT","account":"`+account+`"}`)
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), account) || !containsAll(created.Body.String(), `"status":"pending"`, `"account_masked":"****6789"`, `"amount":15000`, `"fee_basis_points":0`, `"fee_amount":0`, `"net_amount":15000`) {
		t.Fatalf("create withdrawal status=%d body=%s", created.Code, created.Body)
	}
	var envelope struct {
		Data store.CommissionWithdrawal `json:"data"`
	}
	decodeResponse(t, created, &envelope)
	forbidden := user.request(t, api, http.MethodGet, "/api/v1/admin/commission-withdrawals", "")
	if forbidden.Code != http.StatusForbidden {
		t.Fatalf("non-admin withdrawal list status=%d", forbidden.Code)
	}
	revealed := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/commission-withdrawals/%d/account/reveal", envelope.Data.ID), `{}`)
	if revealed.Code != http.StatusOK || revealed.Header().Get("Cache-Control") != "no-store" || revealed.Header().Get("Pragma") != "no-cache" || !strings.Contains(revealed.Body.String(), account) {
		t.Fatalf("admin account reveal status=%d cache=%q body=%s", revealed.Code, revealed.Header().Get("Cache-Control"), revealed.Body)
	}
	audits, err := database.ListAdminAuditLogs(context.Background(), store.AdminAuditFilter{Page: 1, PageSize: 20, Query: "commission-withdrawals"})
	if err != nil {
		t.Fatal(err)
	}
	if len(audits.Items) != 1 || audits.Items[0].Method != http.MethodPost || audits.Items[0].Route != fmt.Sprintf("/api/v1/admin/commission-withdrawals/%d/account/reveal", envelope.Data.ID) || audits.Items[0].StatusCode != http.StatusOK {
		t.Fatalf("account reveal audit = %+v", audits.Items)
	}
	approved := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/commission-withdrawals/%d/approve", envelope.Data.ID), `{"revision":1,"confirm":true}`)
	if approved.Code != http.StatusOK || !strings.Contains(approved.Body.String(), `"status":"approved"`) {
		t.Fatalf("approve status=%d body=%s", approved.Code, approved.Body)
	}
	paid := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/commission-withdrawals/%d/pay", envelope.Data.ID), `{"revision":2,"external_reference":"BANK-API-001","confirm":true}`)
	if paid.Code != http.StatusOK || !containsAll(paid.Body.String(), `"status":"paid"`, `"external_reference":"BANK-API-001"`) {
		t.Fatalf("pay status=%d body=%s", paid.Code, paid.Body)
	}
	rejected := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/commission-withdrawals/%d/reject", envelope.Data.ID), `{"revision":3,"reason":"late","confirm":true}`)
	expectAPIError(t, rejected, http.StatusConflict, "withdrawal_state_conflict")
}

func TestAdminUserDeletionAPIRequiresImpactConfirmationAndRevokesSession(t *testing.T) {
	api, database := newTestAPI(t)
	createHTTPTestUser(t, database, "delete-api@example.test", "delete-api-password-123")
	targetRecord, err := database.FindUserByEmail(context.Background(), "delete-api@example.test")
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.GetAdminUser(context.Background(), targetRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	user := loginAs(t, api, "delete-api@example.test", "delete-api-password-123")
	admin := loginAdmin(t, api)

	unauthorized := user.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%d/deletion-impact", target.ID), "")
	if unauthorized.Code != http.StatusForbidden {
		t.Fatalf("non-admin deletion preview status=%d", unauthorized.Code)
	}
	preview := admin.request(t, api, http.MethodGet, fmt.Sprintf("/api/v1/admin/users/%d/deletion-impact", target.ID), "")
	if preview.Code != http.StatusOK || !containsAll(preview.Body.String(), `"allowed":true`, `"lifecycle_status":"active"`) {
		t.Fatalf("deletion preview status=%d body=%s", preview.Code, preview.Body)
	}
	missingConfirmation := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%d/deletion", target.ID), fmt.Sprintf(`{"revision":%d}`, target.Revision))
	expectAPIError(t, missingConfirmation, http.StatusUnprocessableEntity, "confirmation_required")
	requested := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%d/deletion", target.ID), fmt.Sprintf(`{"revision":%d,"confirm":true}`, target.Revision))
	if requested.Code != http.StatusOK || !containsAll(requested.Body.String(), `"lifecycle_status":"pending_deletion"`, `"banned":true`) {
		t.Fatalf("deletion request status=%d body=%s", requested.Code, requested.Body)
	}
	var requestedEnvelope struct {
		Data store.AdminUser `json:"data"`
	}
	decodeResponse(t, requested, &requestedEnvelope)
	revoked := user.request(t, api, http.MethodGet, "/api/v1/invitations", "")
	if revoked.Code != http.StatusUnauthorized {
		t.Fatalf("revoked user session status=%d body=%s", revoked.Code, revoked.Body)
	}
	restored := admin.request(t, api, http.MethodPost, fmt.Sprintf("/api/v1/admin/users/%d/deletion/restore", target.ID), fmt.Sprintf(`{"revision":%d,"confirm":true}`, requestedEnvelope.Data.Revision))
	if restored.Code != http.StatusOK || !containsAll(restored.Body.String(), `"lifecycle_status":"active"`, `"banned":false`) {
		t.Fatalf("restore status=%d body=%s", restored.Code, restored.Body)
	}
	oldPassword := agentRequest(api, http.MethodPost, "/api/v1/auth/login", "", `{"email":"delete-api@example.test","password":"delete-api-password-123"}`)
	if oldPassword.Code != http.StatusUnauthorized {
		t.Fatalf("old password after restore status=%d body=%s", oldPassword.Code, oldPassword.Body)
	}
}
