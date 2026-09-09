package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type giftCardCodeRedemptionSnapshot struct {
	status            int
	userID            sql.NullInt64
	usedAt            sql.NullInt64
	actualRewardsJSON sql.NullString
	usageCount        int64
	updatedAt         int64
}

type giftCardRewardUserSnapshot struct {
	planID          sql.NullInt64
	groupID         sql.NullInt64
	expiredAt       sql.NullInt64
	nextResetAt     sql.NullInt64
	lastResetAt     sql.NullInt64
	balance         int64
	transferEnable  int64
	trafficUpload   int64
	trafficDownload int64
	speedLimit      int64
	deviceLimit     int64
	resetCount      int64
	adminRevision   int64
	updatedAt       int64
}

type giftCardRedemptionSnapshot struct {
	code       giftCardCodeRedemptionSnapshot
	recipient  giftCardRewardUserSnapshot
	inviter    giftCardRewardUserSnapshot
	usageCount int64
}

func readGiftCardRedemptionSnapshot(t *testing.T, database *Store, codeID, recipientID, inviterID int64) giftCardRedemptionSnapshot {
	t.Helper()
	ctx := t.Context()
	var snapshot giftCardRedemptionSnapshot
	if err := database.db.QueryRowContext(ctx, `
		SELECT status, user_id, used_at, actual_rewards_json, usage_count, updated_at
		FROM gift_card_codes WHERE id = ?
	`, codeID).Scan(&snapshot.code.status, &snapshot.code.userID, &snapshot.code.usedAt,
		&snapshot.code.actualRewardsJSON, &snapshot.code.usageCount, &snapshot.code.updatedAt); err != nil {
		t.Fatal(err)
	}
	readUser := func(userID int64, target *giftCardRewardUserSnapshot) {
		if err := database.db.QueryRowContext(ctx, `
			SELECT plan_id, group_id, expired_at, next_reset_at, last_reset_at,
				balance, transfer_enable, traffic_u, traffic_d, speed_limit, device_limit,
				reset_count, admin_revision, updated_at
			FROM users WHERE id = ?
		`, userID).Scan(&target.planID, &target.groupID, &target.expiredAt, &target.nextResetAt,
			&target.lastResetAt, &target.balance, &target.transferEnable, &target.trafficUpload,
			&target.trafficDownload, &target.speedLimit, &target.deviceLimit, &target.resetCount,
			&target.adminRevision, &target.updatedAt); err != nil {
			t.Fatal(err)
		}
	}
	readUser(recipientID, &snapshot.recipient)
	readUser(inviterID, &snapshot.inviter)
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gift_card_usages WHERE code_id = ?`, codeID).
		Scan(&snapshot.usageCount); err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func TestGiftCardRedemptionAuditFailureRollsBackRewardsAndRetrySucceeds(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	plan, recipientID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100}, nil)
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "gift-retry-inviter@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	initialExpiry := now.Add(24 * time.Hour)
	initialNextReset := now.Add(48 * time.Hour)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET invite_user_id = ?, plan_id = ?, balance = 100, transfer_enable = ?,
			traffic_u = 123, traffic_d = 456, expired_at = ?, speed_limit = 7, device_limit = 2,
			next_reset_at = ?, last_reset_at = ?, reset_count = 7, updated_at = ?
		WHERE id = ?
	`, inviter.ID, plan.ID, bytesPerGiB, initialExpiry.Unix(), initialNextReset.Unix(), now.Unix(), now.Unix(), recipientID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET balance = 50, transfer_enable = 200, updated_at = ? WHERE id = ?
	`, now.Unix(), inviter.ID); err != nil {
		t.Fatal(err)
	}

	reward := GiftCardReward{
		Balance: 1_000, TransferEnable: 2 * bytesPerGiB, ExpireDays: 30, DeviceLimit: 4, ResetTraffic: true,
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Rollback and retry", Type: GiftCardTypeGeneral, Status: true, Rewards: reward,
		Limits: GiftCardLimits{MaxUsePerUser: 1, InviteRewardBasisPoints: 2_500},
	}, inviter.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
	if err != nil || len(codes) != 1 {
		t.Fatalf("GenerateGiftCardCodes() = (%#v, %v), want one code", codes, err)
	}
	code := codes[0]
	before := readGiftCardRedemptionSnapshot(t, database, code.ID, recipientID, inviter.ID)

	if _, err := database.db.ExecContext(ctx, `
		CREATE TRIGGER fail_gift_card_usage_insert
		BEFORE INSERT ON gift_card_usages
		BEGIN SELECT RAISE(ABORT, 'injected gift card usage failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	failureTime := now.Add(2 * time.Hour)
	if _, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{
		UserID: recipientID, Code: code.Code, IPAddress: "192.0.2.20", UserAgent: "gift-retry-test",
	}, failureTime); err == nil || !strings.Contains(err.Error(), "record gift card usage") || !strings.Contains(err.Error(), "injected gift card usage failure") {
		t.Fatalf("RedeemGiftCard() error = %v, want injected usage audit failure", err)
	}
	afterFailure := readGiftCardRedemptionSnapshot(t, database, code.ID, recipientID, inviter.ID)
	if afterFailure != before {
		t.Fatalf("failed redemption changed code, recipient, inviter, revision, or usage state: got %#v want %#v", afterFailure, before)
	}

	if _, err := database.db.ExecContext(ctx, `DROP TRIGGER fail_gift_card_usage_insert`); err != nil {
		t.Fatal(err)
	}
	retryTime := now.Add(3 * time.Hour)
	usage, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{
		UserID: recipientID, Code: code.Code, IPAddress: "192.0.2.20", UserAgent: "gift-retry-test",
	}, retryTime)
	if err != nil {
		t.Fatalf("retry RedeemGiftCard() error = %v", err)
	}
	if usage.CodeID != code.ID || usage.UserID != recipientID || usage.InviterID == nil || *usage.InviterID != inviter.ID ||
		usage.Rewards.Balance != 1_000 || usage.Rewards.TransferEnable != 2*bytesPerGiB || usage.Rewards.ExpireDays != 30 ||
		usage.Rewards.DeviceLimit != 4 || !usage.Rewards.ResetTraffic || usage.InviterRewards.Balance != 250 ||
		usage.InviterRewards.TransferEnable != bytesPerGiB/2 || !usage.UsedAt.Equal(retryTime) {
		t.Fatalf("retry usage = %#v, want fixed recipient and inviter rewards", usage)
	}
	persistedUsage, err := database.GetGiftCardUsage(ctx, usage.ID, recipientID)
	if err != nil {
		t.Fatalf("GetGiftCardUsage() error = %v", err)
	}
	wantInviterReward := GiftCardReward{Balance: 250, TransferEnable: bytesPerGiB / 2}
	if persistedUsage.CodeID != code.ID || persistedUsage.UserID != recipientID ||
		persistedUsage.InviterID == nil || *persistedUsage.InviterID != inviter.ID ||
		!reflect.DeepEqual(persistedUsage.Rewards, reward) || !reflect.DeepEqual(persistedUsage.InviterRewards, wantInviterReward) ||
		persistedUsage.UserLevelAtUse == nil || *persistedUsage.UserLevelAtUse != int64(plan.SortPosition) ||
		persistedUsage.UserPlanID == nil || *persistedUsage.UserPlanID != plan.ID || persistedUsage.Multiplier != 10_000 ||
		persistedUsage.IPAddress != "192.0.2.20" || persistedUsage.UserAgent != "gift-retry-test" ||
		persistedUsage.TrafficResetUploadBefore == nil || *persistedUsage.TrafficResetUploadBefore != 123 ||
		persistedUsage.TrafficResetDownloadBefore == nil || *persistedUsage.TrafficResetDownloadBefore != 456 ||
		!persistedUsage.UsedAt.Equal(retryTime) {
		t.Fatalf("persisted retry usage = %#v, want fixed audit facts", persistedUsage)
	}

	rewardJSON, err := json.Marshal(reward)
	if err != nil {
		t.Fatal(err)
	}
	afterRetry := readGiftCardRedemptionSnapshot(t, database, code.ID, recipientID, inviter.ID)
	wantAfterRetry := before
	wantAfterRetry.code.status = int(GiftCardCodeUsed)
	wantAfterRetry.code.userID = sql.NullInt64{Int64: recipientID, Valid: true}
	wantAfterRetry.code.usedAt = sql.NullInt64{Int64: retryTime.Unix(), Valid: true}
	wantAfterRetry.code.actualRewardsJSON = sql.NullString{String: string(rewardJSON), Valid: true}
	wantAfterRetry.code.usageCount++
	wantAfterRetry.code.updatedAt = retryTime.Unix()
	wantAfterRetry.recipient.balance += 1_000
	wantAfterRetry.recipient.transferEnable += 2 * bytesPerGiB
	wantAfterRetry.recipient.trafficUpload = 0
	wantAfterRetry.recipient.trafficDownload = 0
	wantAfterRetry.recipient.expiredAt = sql.NullInt64{Int64: initialExpiry.AddDate(0, 0, 30).Unix(), Valid: true}
	wantAfterRetry.recipient.nextResetAt = sql.NullInt64{Int64: time.Date(2026, 9, 30, 16, 0, 0, 0, time.UTC).Unix(), Valid: true}
	wantAfterRetry.recipient.lastResetAt = sql.NullInt64{Int64: retryTime.Unix(), Valid: true}
	wantAfterRetry.recipient.deviceLimit += 4
	wantAfterRetry.recipient.resetCount++
	wantAfterRetry.recipient.adminRevision++
	wantAfterRetry.recipient.updatedAt = retryTime.Unix()
	wantAfterRetry.inviter.balance += 250
	wantAfterRetry.inviter.transferEnable += bytesPerGiB / 2
	wantAfterRetry.inviter.adminRevision++
	wantAfterRetry.inviter.updatedAt = retryTime.Unix()
	wantAfterRetry.usageCount++
	if afterRetry != wantAfterRetry {
		t.Fatalf("retry code, recipient, inviter, revision, or usage state = %#v, want %#v", afterRetry, wantAfterRetry)
	}

	if _, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: recipientID, Code: code.Code}, retryTime.Add(time.Minute)); !errors.Is(err, ErrGiftCardExhausted) {
		t.Fatalf("second successful-attempt RedeemGiftCard() error = %v, want ErrGiftCardExhausted", err)
	}
	afterExhausted := readGiftCardRedemptionSnapshot(t, database, code.ID, recipientID, inviter.ID)
	if afterExhausted != afterRetry {
		t.Fatalf("exhausted redemption duplicated rewards or usage: got %#v want %#v", afterExhausted, afterRetry)
	}
}
