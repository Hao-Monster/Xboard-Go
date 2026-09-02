package store

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestUserDeletionPreviewRequestAndRestoreRevokeCredentials(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 2, 8, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "deletion-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "deletion-target@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE app_settings SET app_name='Deletion Board',app_url='https://panel.example.test',smtp_enabled=1,
			smtp_host='smtp.example.test',smtp_port=587,smtp_encryption='starttls',smtp_from_address='no-reply@example.test'
		WHERE id=1
	`); err != nil {
		t.Fatal(err)
	}
	bulkMail, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindMail, AdministratorID: admin.ID,
		Scope:   AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
		Subject: "Account notice", Content: "Notice",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	var beforeToken string
	if err := database.db.QueryRowContext(ctx, `SELECT subscription_token FROM users WHERE id=?`, target.ID).Scan(&beforeToken); err != nil {
		t.Fatal(err)
	}
	impact, err := database.GetAdminUserDeletionImpact(ctx, admin.ID, target.ID)
	if err != nil || !impact.Allowed || impact.UserID != target.ID || impact.Orders != 0 {
		t.Fatalf("GetAdminUserDeletionImpact() = (%#v, %v)", impact, err)
	}
	if _, err := database.GetAdminUserDeletionImpact(ctx, target.ID, admin.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("non-administrator deletion preview error = %v, want ErrNotFound", err)
	}
	requested, err := database.RequestAdminUserDeletion(ctx, admin.ID, target.ID, target.Revision, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("RequestAdminUserDeletion() error = %v", err)
	}
	if requested.LifecycleStatus != UserLifecyclePendingDeletion || !requested.Banned || requested.DeletionDueAt == nil ||
		!requested.DeletionDueAt.Equal(now.Add(time.Minute+30*24*time.Hour)) {
		t.Fatalf("requested user = %#v", requested)
	}
	if _, err := database.GetAdminUserSubscriptionToken(ctx, target.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending-deletion subscription token error = %v, want ErrNotFound", err)
	}
	if _, _, err := database.ResetSubscriptionSecurityAtRevision(ctx, target.ID, requested.Revision, now.Add(90*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending-deletion security reset error = %v, want ErrNotFound", err)
	}
	if _, err := database.CreateAdminUserBulkJob(ctx, CreateAdminUserBulkJobInput{
		Kind: AdminUserBulkKindMail, AdministratorID: admin.ID,
		Scope:   AdminUserBulkScope{Scope: AdminUserBulkScopeSelected, UserIDs: []int64{target.ID}},
		Subject: "Late notice", Content: "Must not be queued",
	}, now.Add(90*time.Second)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("pending-deletion bulk mail error = %v, want ErrInvalidInput", err)
	}
	bulkMail, err = database.GetAdminUserBulkJob(ctx, bulkMail.ID)
	if err != nil || bulkMail.Status != AdminUserBulkStatusCancelled || bulkMail.CancelledCount != 1 {
		t.Fatalf("bulk mail after deletion request = (%#v, %v)", bulkMail, err)
	}
	var requestedToken string
	if err := database.db.QueryRowContext(ctx, `SELECT subscription_token FROM users WHERE id=?`, target.ID).Scan(&requestedToken); err != nil || requestedToken == beforeToken {
		t.Fatalf("requested subscription token was not rotated: before=%q after=%q err=%v", beforeToken, requestedToken, err)
	}
	if _, err := database.RequestAdminUserDeletion(ctx, admin.ID, target.ID, target.Revision, now.Add(2*time.Minute)); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale deletion request error = %v, want ErrRevisionConflict", err)
	}
	restored, err := database.RestoreAdminUser(ctx, admin.ID, target.ID, requested.Revision, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("RestoreAdminUser() error = %v", err)
	}
	if restored.LifecycleStatus != UserLifecycleActive || restored.Banned || restored.DeletionDueAt != nil {
		t.Fatalf("restored user = %#v", restored)
	}
	var restoredToken string
	if err := database.db.QueryRowContext(ctx, `SELECT subscription_token FROM users WHERE id=?`, target.ID).Scan(&restoredToken); err != nil || restoredToken != requestedToken {
		t.Fatalf("restore unexpectedly changed revoked token: requested=%q restored=%q err=%v", requestedToken, restoredToken, err)
	}
	if _, err := database.GetAdminUserDeletionImpact(ctx, admin.ID, admin.ID); !errors.Is(err, ErrUserDeletionSelf) {
		t.Fatalf("self deletion preview error = %v, want ErrUserDeletionSelf", err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance=100 WHERE id=?`, target.ID); err != nil {
		t.Fatal(err)
	}
	financialImpact, err := database.GetAdminUserDeletionImpact(ctx, admin.ID, target.ID)
	if err != nil || financialImpact.Allowed || !strings.Contains(strings.Join(financialImpact.Blockers, ","), "unsettled_financial_balance") {
		t.Fatalf("unsettled financial deletion impact = (%#v, %v)", financialImpact, err)
	}
	if _, err := database.RequestAdminUserDeletion(ctx, admin.ID, target.ID, restored.Revision, now.Add(3*time.Minute)); !errors.Is(err, ErrUserDeletionBlocked) {
		t.Fatalf("unsettled financial deletion request error = %v, want ErrUserDeletionBlocked", err)
	}
}

func TestDueUserAnonymizationPreservesBusinessFactsAndIsIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 2, 9, 0, 0, 0, time.UTC)
	plan, targetID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1000}, nil)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "anonymize-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	target, err := database.GetAdminUser(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: targetID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO admin_audit_logs (administrator_id,administrator_email,method,route,status_code,created_at) VALUES (?,?,'POST','/api/v1/admin/example',200,?)`, targetID, target.Email, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO traffic_reset_logs (user_id,reset_at,upload_before,download_before,reset_count,trigger_source,reason,administrator_id,administrator_email,idempotency_key) VALUES (?, ?,0,0,1,'manual','test',?,?,'deletion-snapshot-001')`, targetID, now.Unix(), targetID, target.Email); err != nil {
		t.Fatal(err)
	}
	accountCipher := []byte("encrypted-withdrawal-account")
	accountFingerprint := bytes.Repeat([]byte{0x5a}, 32)
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO commission_withdrawals (
			user_id,idempotency_key,amount,fee_basis_points,fee_amount,net_amount,currency,method,
			account_cipher,account_fingerprint,account_masked,status,revision,external_reference,
			created_at,updated_at,approved_at,paid_at
		) VALUES (?, 'deletion-withdrawal-0001', 2500, 0, 0, 2500, 'CNY', '支付宝', ?, ?, '****9876', 'paid', 3,
			'deletion-payment-0001', ?, ?, ?, ?)
	`, targetID, accountCipher, accountFingerprint, now.Unix(), now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO commission_transfer_events (
			user_id,idempotency_key,amount,currency,commission_balance_before,commission_balance_after,
			balance_before,balance_after,created_at
		) VALUES (?, 'deletion-transfer-0001', 1, 'CNY', 1, 0, 0, 1, ?)
	`, targetID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO admin_balance_adjustment_events (
			actor_user_id,target_user_id,currency,balance_before,balance_after,
			commission_balance_before,commission_balance_after,target_revision_before,target_revision_after,created_at
		) VALUES (?, ?, 'CNY', 0, 1, 0, 0, 1, 2, ?)
	`, admin.ID, targetID, now.Unix()); err != nil {
		t.Fatal(err)
	}
	impact, err := database.GetAdminUserDeletionImpact(ctx, admin.ID, targetID)
	if err != nil || impact.Allowed || impact.Orders != 1 || impact.CommissionTransfers != 1 || impact.AdminBalanceAdjustments != 1 ||
		!strings.Contains(strings.Join(impact.Blockers, ","), "active_order") {
		t.Fatalf("active order deletion impact = (%#v, %v)", impact, err)
	}
	if _, err := database.RequestAdminUserDeletion(ctx, admin.ID, targetID, target.Revision, now.Add(30*time.Second)); !errors.Is(err, ErrUserDeletionBlocked) {
		t.Fatalf("active order deletion request error = %v, want ErrUserDeletionBlocked", err)
	}
	if _, err := database.CancelOrder(ctx, targetID, order.TradeNo, now.Add(45*time.Second)); err != nil {
		t.Fatal(err)
	}
	impact, err = database.GetAdminUserDeletionImpact(ctx, admin.ID, targetID)
	if err != nil || !impact.Allowed || impact.Orders != 1 {
		t.Fatalf("settled order deletion impact = (%#v, %v)", impact, err)
	}
	requested, err := database.RequestAdminUserDeletion(ctx, admin.ID, targetID, target.Revision, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.AssignOrder(ctx, AssignOrderInput{UserID: &targetID, PlanID: plan.ID, Period: "monthly", TotalAmount: 1000}, now.Add(90*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pending-deletion assigned order error = %v, want ErrNotFound", err)
	}
	due := *requested.DeletionDueAt
	preclaimedTombstone := fmt.Sprintf("deleted+%x@invalid.invalid", targetID)
	if _, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: preclaimedTombstone, PasswordHash: "hash"}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("preclaim deterministic tombstone: %v", err)
	}
	if result, err := database.ProcessDueUserAnonymizations(ctx, due.Add(-time.Second), 100); err != nil || result.Processed != 0 {
		t.Fatalf("early anonymization = (%#v, %v)", result, err)
	}
	result, err := database.ProcessDueUserAnonymizations(ctx, due, 100)
	if err != nil || result.Processed != 1 || result.Remaining != 0 {
		t.Fatalf("due anonymization = (%#v, %v)", result, err)
	}
	repeated, err := database.ProcessDueUserAnonymizations(ctx, due.Add(time.Hour), 100)
	if err != nil || repeated.Processed != 0 {
		t.Fatalf("repeated anonymization = (%#v, %v)", repeated, err)
	}
	anonymized, err := database.GetAdminUser(ctx, targetID)
	if err != nil {
		t.Fatal(err)
	}
	if anonymized.LifecycleStatus != UserLifecycleAnonymized || !anonymized.Banned || anonymized.AnonymizedAt == nil ||
		!strings.HasPrefix(anonymized.Email, "deleted+") || !strings.HasSuffix(anonymized.Email, "@invalid.invalid") || anonymized.TelegramID != nil || anonymized.Remarks != nil {
		t.Fatalf("anonymized user = %#v", anonymized)
	}
	if anonymized.Email == preclaimedTombstone {
		t.Fatalf("anonymized user reused preclaimed deterministic tombstone %q", anonymized.Email)
	}
	var auditEmail, trafficAdministratorEmail string
	if err := database.db.QueryRowContext(ctx, `SELECT administrator_email FROM admin_audit_logs WHERE administrator_id=?`, targetID).Scan(&auditEmail); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT administrator_email FROM traffic_reset_logs WHERE administrator_id=?`, targetID).Scan(&trafficAdministratorEmail); err != nil {
		t.Fatal(err)
	}
	if auditEmail != anonymized.Email || trafficAdministratorEmail != anonymized.Email || strings.Contains(auditEmail, "anonymize-user") {
		t.Fatalf("display snapshots were not anonymized: audit=%q traffic=%q tombstone=%q", auditEmail, trafficAdministratorEmail, anonymized.Email)
	}
	var storedCipher, storedFingerprint []byte
	var storedMasked, withdrawalStatus, withdrawalCurrency, externalReference string
	var withdrawalAmount int64
	if err := database.db.QueryRowContext(ctx, `
		SELECT account_cipher,account_fingerprint,account_masked,status,currency,amount,external_reference
		FROM commission_withdrawals WHERE user_id=?
	`, targetID).Scan(&storedCipher, &storedFingerprint, &storedMasked, &withdrawalStatus, &withdrawalCurrency, &withdrawalAmount, &externalReference); err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(storedCipher, accountCipher) || bytes.Equal(storedFingerprint, accountFingerprint) || storedMasked != "[anonymized]" {
		t.Fatalf("withdrawal account data was not purged: cipher=%x fingerprint=%x masked=%q", storedCipher, storedFingerprint, storedMasked)
	}
	if withdrawalStatus != CommissionWithdrawalPaid || withdrawalCurrency != "CNY" || withdrawalAmount != 2500 || externalReference != "deletion-payment-0001" {
		t.Fatalf("withdrawal financial facts changed: status=%q currency=%q amount=%d external_reference=%q", withdrawalStatus, withdrawalCurrency, withdrawalAmount, externalReference)
	}
	storedOrder, err := database.GetUserOrder(ctx, targetID, order.TradeNo)
	if err != nil || storedOrder.UserID != targetID {
		t.Fatalf("preserved order = (%#v, %v)", storedOrder, err)
	}
	if _, err := database.RestoreAdminUser(ctx, admin.ID, targetID, anonymized.Revision, due.Add(time.Hour)); !errors.Is(err, ErrUserDeletionState) {
		t.Fatalf("restore anonymized error = %v, want ErrUserDeletionState", err)
	}
}

func TestUserDeletionBlocksUnsettledCommissionAndAnonymizedInviterDoesNotAccrue(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 2, 10, 0, 0, 0, time.UTC)
	plan, buyerID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "commission-deletion-admin@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "commission-deletion-inviter@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id=? WHERE id=?`, inviter.ID, buyerID); err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: buyerID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteOrder(ctx, order.TradeNo, "commission-deletion-callback", now); err != nil {
		t.Fatal(err)
	}
	impact, err := database.GetAdminUserDeletionImpact(ctx, admin.ID, inviter.ID)
	if err != nil || impact.Allowed || !strings.Contains(strings.Join(impact.Blockers, ","), "unsettled_commission") {
		t.Fatalf("unsettled commission deletion impact = (%#v, %v)", impact, err)
	}
	if _, err := database.RequestAdminUserDeletion(ctx, admin.ID, inviter.ID, inviter.Revision, now.Add(time.Minute)); !errors.Is(err, ErrUserDeletionBlocked) {
		t.Fatalf("unsettled commission deletion request error = %v, want ErrUserDeletionBlocked", err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE orders SET commission_status=2 WHERE id=?`, order.ID); err != nil {
		t.Fatal(err)
	}
	pending, err := database.RequestAdminUserDeletion(ctx, admin.ID, inviter.ID, inviter.Revision, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	due := *pending.DeletionDueAt
	if result, err := database.ProcessDueUserAnonymizations(ctx, due, 100); err != nil || result.Processed != 1 {
		t.Fatalf("anonymize inviter = (%#v, %v)", result, err)
	}
	nextOrder, err := database.CreateOrder(ctx, CreateOrderInput{UserID: buyerID, PlanID: plan.ID, Period: "monthly"}, due.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if nextOrder.InviteUserID == nil || *nextOrder.InviteUserID != inviter.ID || nextOrder.CommissionBalance != 0 {
		t.Fatalf("order after inviter anonymization = %#v", nextOrder)
	}
}
