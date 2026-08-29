package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAdminUserDirectoryUsesStableCursorAndHidesInternalAccounts(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)

	for index := 0; index < 5; index++ {
		_, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
			Email: fmt.Sprintf("user-%02d@example.test", index), PasswordHash: "opaque-hash",
			GroupID: pointerTo(int64(7)), TransferEnable: 1_000,
		}, now.Add(time.Duration(index)*time.Second))
		if err != nil {
			t.Fatalf("CreateAdminUser(%d) error = %v", index, err)
		}
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, account_kind, uuid, group_id, transfer_enable, subscription_token, created_at, updated_at)
		VALUES ('internal@example.test', 'opaque-hash', 'internal_subscription', '6c7f599b-b133-4986-8f3a-709122ce0cc1', 7, 1000, ?, ?, ?)
	`, testSubscriptionToken(t), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}

	first, err := database.ListAdminUsers(ctx, AdminUserFilter{Limit: 2})
	if err != nil {
		t.Fatalf("ListAdminUsers(first) error = %v", err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" || first.Items[0].ID <= first.Items[1].ID {
		t.Fatalf("unexpected first page: %#v", first)
	}
	inserted, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "inserted-between-pages@example.test", PasswordHash: "opaque-hash", GroupID: pointerTo(int64(7)), TransferEnable: 1_000,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.ListAdminUsers(ctx, AdminUserFilter{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatalf("ListAdminUsers(second) error = %v", err)
	}
	for _, item := range append(first.Items, second.Items...) {
		if item.ID == inserted.ID || item.Email == "internal@example.test" {
			t.Fatalf("cursor page leaked a later or internal account: %#v", item)
		}
	}
	if second.Items[0].ID >= first.Items[1].ID {
		t.Fatalf("cursor did not advance monotonically: first=%#v second=%#v", first.Items, second.Items)
	}
	if _, err := database.GetAdminUser(ctx, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAdminUser(missing) error = %v", err)
	}
	var internalID int64
	if err := database.db.QueryRowContext(ctx, `SELECT id FROM users WHERE email = 'internal@example.test'`).Scan(&internalID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.GetAdminUser(ctx, internalID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetAdminUser(internal) error = %v, want ErrNotFound", err)
	}
}

func TestAdminUserDirectoryFiltersAndBoundsWork(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)
	for _, input := range []CreateAdminUserInput{
		{Email: "alpha@example.test", PasswordHash: "hash", GroupID: pointerTo(int64(7)), TransferEnable: 100},
		{Email: "alpine@example.test", PasswordHash: "hash", GroupID: pointerTo(int64(8)), TransferEnable: 100, Banned: true},
		{Email: "beta@example.test", PasswordHash: "hash", GroupID: pointerTo(int64(7)), TransferEnable: 100, Banned: true},
	} {
		if _, err := database.CreateAdminUser(ctx, input, now); err != nil {
			t.Fatal(err)
		}
	}
	banned := true
	groupID := int64(7)
	page, err := database.ListAdminUsers(ctx, AdminUserFilter{Limit: 500, EmailPrefix: " B ", Banned: &banned, GroupID: &groupID})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Email != "beta@example.test" {
		t.Fatalf("filtered page = %#v", page)
	}
	if _, err := database.ListAdminUsers(ctx, AdminUserFilter{Limit: -1}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative limit error = %v", err)
	}
	if _, err := database.ListAdminUsers(ctx, AdminUserFilter{Cursor: "not-a-cursor"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid cursor error = %v", err)
	}
}

func TestAdminUserEmailPrefixCursorUsesEmailOrdering(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	for _, email := range []string{"alpine@example.test", "albatross@example.test", "alpha@example.test", "beta@example.test"} {
		if _, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: email, PasswordHash: "hash"}, now); err != nil {
			t.Fatal(err)
		}
	}
	first, err := database.ListAdminUsers(ctx, AdminUserFilter{Limit: 2, EmailPrefix: "al"})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.Items[0].Email != "albatross@example.test" || first.Items[1].Email != "alpha@example.test" || first.NextCursor == "" {
		t.Fatalf("first email page = %#v", first)
	}
	second, err := database.ListAdminUsers(ctx, AdminUserFilter{Limit: 2, EmailPrefix: "al", Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Email != "alpine@example.test" || second.NextCursor != "" {
		t.Fatalf("second email page = %#v", second)
	}
	if _, err := database.ListAdminUsers(ctx, AdminUserFilter{Cursor: first.NextCursor}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("email cursor without email filter error = %v", err)
	}
}

func TestCreateAdminUsersIsAtomicAndAppliesPlanEntitlements(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 10, 0, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)
	resetMethod, speedLimit, deviceLimit := 1, 88, 6
	groupID := int64(7)
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		GroupID: &groupID, TransferEnableGiB: 32, Name: "admin batch plan",
		SpeedLimit: &speedLimit, DeviceLimit: &deviceLimit, ResetTrafficMethod: &resetMethod,
		Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	expiresAt := now.AddDate(0, 1, 0)
	created, err := database.CreateAdminUsers(ctx, []CreateAdminUserInput{
		{Email: "batch-a@example.test", PasswordHash: "argon-a", PlanID: &plan.ID, ExpiredAt: &expiresAt},
		{Email: "batch-b@example.test", PasswordHash: "argon-b", PlanID: &plan.ID, ExpiredAt: &expiresAt},
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUsers() error = %v", err)
	}
	if len(created) != 2 || created[0].User.Email != "batch-a@example.test" || created[1].User.Email != "batch-b@example.test" {
		t.Fatalf("created users = %#v", created)
	}
	for _, item := range created {
		if item.User.PlanID == nil || *item.User.PlanID != plan.ID || item.User.GroupID == nil || *item.User.GroupID != groupID ||
			item.User.TransferEnable != 32*1024*1024*1024 || item.User.SpeedLimit != speedLimit ||
			item.User.DeviceLimit != deviceLimit || item.User.NextResetAt == nil || item.UUID == "" || item.SubscriptionToken == "" {
			t.Fatalf("plan-backed created user = %#v", item)
		}
	}
	if created[0].UUID == created[1].UUID || created[0].SubscriptionToken == created[1].SubscriptionToken {
		t.Fatal("batch users reused subscription credentials")
	}

	if _, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "taken-batch@example.test", PasswordHash: "hash"}, now); err != nil {
		t.Fatal(err)
	}
	_, err = database.CreateAdminUsers(ctx, []CreateAdminUserInput{
		{Email: "must-rollback@example.test", PasswordHash: "hash-a"},
		{Email: "taken-batch@example.test", PasswordHash: "hash-b"},
	}, now)
	if !errors.Is(err, ErrEmailInUse) {
		t.Fatalf("duplicate batch error = %v, want ErrEmailInUse", err)
	}
	var rolledBack int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE email = 'must-rollback@example.test'`).Scan(&rolledBack); err != nil {
		t.Fatal(err)
	}
	if rolledBack != 0 {
		t.Fatalf("failed batch retained %d partial users", rolledBack)
	}
	if _, err := database.CreateAdminUsers(ctx, nil, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty batch error = %v", err)
	}
	tooMany := make([]CreateAdminUserInput, 501)
	if _, err := database.CreateAdminUsers(ctx, tooMany, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized batch error = %v", err)
	}
	maximum := make([]CreateAdminUserInput, 500)
	for index := range maximum {
		maximum[index] = CreateAdminUserInput{
			Email: fmt.Sprintf("boundary-%03d@example.test", index), PasswordHash: fmt.Sprintf("argon-%03d", index),
		}
	}
	maximumCreated, err := database.CreateAdminUsers(ctx, maximum, now.Add(time.Minute))
	if err != nil || len(maximumCreated) != 500 || maximumCreated[0].User.Email != "boundary-000@example.test" ||
		maximumCreated[499].User.Email != "boundary-499@example.test" {
		t.Fatalf("maximum batch: count=%d err=%v", len(maximumCreated), err)
	}
}

func TestSchemaV4BackfillsHumanKindAndRevision(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "users-v3.db")
	database, err := OpenSQLite("file:" + filepath.ToSlash(databasePath))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, schemaV1); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, schemaV2); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, schemaV3); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Unix()
	result, err := database.db.ExecContext(ctx, `INSERT INTO users (email, password_hash, created_at, updated_at) VALUES ('legacy@example.test', 'hash', ?, ?)`, now, now)
	if err != nil {
		t.Fatal(err)
	}
	userID, _ := result.LastInsertId()
	if _, err := database.db.ExecContext(ctx, `PRAGMA user_version = 3`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	user, err := database.GetAdminUser(ctx, userID)
	if err != nil || user.Revision != 1 || user.Email != "legacy@example.test" {
		t.Fatalf("migrated user = %#v err=%v", user, err)
	}
	found, err := database.FindUserByID(ctx, userID)
	if err != nil || found.AccountKind != AccountKindHuman {
		t.Fatalf("migrated account kind = %#v err=%v", found, err)
	}
}

func TestAdminUserMutationIsOptimisticAndRevokesSensitiveState(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "before@example.test", PasswordHash: "old-hash", GroupID: pointerTo(int64(7)), TransferEnable: 1_000,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSession(ctx, user.ID, "session-digest", "csrf-digest", now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: user.ID, TokenHash: strings.Repeat("1", 64), Name: "admin-update-client",
	}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO node_device_ips (node_id, user_id, ip, expires_at) VALUES (1, ?, '192.0.2.8', ?)
	`, user.ID, now.Add(time.Minute).Unix()); err != nil {
		// The test store does not guarantee node 1, so create runtime fixtures when needed.
		machine, node := createReportingNode(t, database, now)
		_ = machine
		if _, retryErr := database.db.ExecContext(ctx, `
			INSERT INTO node_device_ips (node_id, user_id, ip, expires_at) VALUES (?, ?, '192.0.2.8', ?)
		`, node.ID, user.ID, now.Add(time.Minute).Unix()); retryErr != nil {
			t.Fatal(retryErr)
		}
	}

	updated, change, err := database.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{
		Revision: user.Revision, Email: " AFTER@example.test ", GroupID: pointerTo(int64(8)),
		TransferEnable: 2_000, SpeedLimit: 50, DeviceLimit: 3, Banned: true,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("UpdateAdminUser() error = %v", err)
	}
	if updated.Email != "after@example.test" || updated.Revision != user.Revision+1 || !updated.Banned || updated.GroupID == nil || *updated.GroupID != 8 {
		t.Fatalf("updated user = %#v", updated)
	}
	if change.OldGroupID == nil || *change.OldGroupID != 7 || change.NewGroupID == nil || *change.NewGroupID != 8 || change.UUID == "" || !change.AccessStateCleared {
		t.Fatalf("mutation metadata = %#v", change)
	}
	if _, err := database.AuthenticateSession(ctx, "session-digest", now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sensitive update left session active: %v", err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("1", 64), now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sensitive update left access token active: %v", err)
	}
	var deviceCount, onlineCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_device_ips WHERE user_id = ?`, user.ID).Scan(&deviceCount); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT online_count FROM users WHERE id = ?`, user.ID).Scan(&onlineCount); err != nil {
		t.Fatal(err)
	}
	if deviceCount != 0 || onlineCount != 0 {
		t.Fatalf("revoked access retained state: devices=%d online=%d", deviceCount, onlineCount)
	}
	if _, _, err := database.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{
		Revision: user.Revision, Email: "stale@example.test", GroupID: pointerTo(int64(8)), TransferEnable: 2_000,
	}, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update error = %v, want ErrConflict", err)
	}
	fresh, err := database.GetAdminUser(ctx, user.ID)
	if err != nil || fresh.Email != "after@example.test" {
		t.Fatalf("stale update overwrote winner: %#v err=%v", fresh, err)
	}
}

func TestAdminUserUpdateReportsTelegramIDConflictWithoutMutatingUser(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 29, 9, 0, 0, 0, time.UTC)
	telegramID := int64(7788990011)

	owner, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-owner@example.test", PasswordHash: "owner-hash",
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUser(owner) error = %v", err)
	}
	subject, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "telegram-subject@example.test", PasswordHash: "subject-hash",
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUser(subject) error = %v", err)
	}
	if _, _, err := database.UpdateAdminUser(ctx, owner.ID, UpdateAdminUserInput{
		Revision: owner.Revision, Email: owner.Email, TransferEnable: owner.TransferEnable,
		SpeedLimit: owner.SpeedLimit, DeviceLimit: owner.DeviceLimit, Banned: owner.Banned,
		TelegramIDSet: true, TelegramID: &telegramID,
	}, now.Add(time.Minute)); err != nil {
		t.Fatalf("UpdateAdminUser(owner Telegram ID) error = %v", err)
	}

	_, _, err = database.UpdateAdminUser(ctx, subject.ID, UpdateAdminUserInput{
		Revision: subject.Revision, Email: subject.Email, TransferEnable: subject.TransferEnable,
		SpeedLimit: subject.SpeedLimit, DeviceLimit: subject.DeviceLimit, Banned: subject.Banned,
		TelegramIDSet: true, TelegramID: &telegramID,
	}, now.Add(2*time.Minute))
	if !errors.Is(err, ErrTelegramIDInUse) {
		t.Fatalf("duplicate Telegram ID error = %v, want ErrTelegramIDInUse", err)
	}
	if errors.Is(err, ErrEmailInUse) {
		t.Fatalf("duplicate Telegram ID was misclassified as ErrEmailInUse: %v", err)
	}
	fresh, getErr := database.GetAdminUser(ctx, subject.ID)
	if getErr != nil {
		t.Fatalf("GetAdminUser(subject) error = %v", getErr)
	}
	if fresh.Revision != subject.Revision || fresh.TelegramID != nil {
		t.Fatalf("failed Telegram ID update mutated subject: %#v", fresh)
	}
}

func TestAdminUserFullProfileUpdateAppliesPlanAndSideEffectsAtomically(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)

	resetMethod := 1
	speedLimit := 125
	deviceLimit := 5
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		GroupID: pointerTo(int64(8)), TransferEnableGiB: 64, Name: "U2 plan",
		SpeedLimit: &speedLimit, DeviceLimit: &deviceLimit, ResetTrafficMethod: &resetMethod,
		Prices: PlanPrices{}, Tags: []string{},
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "inviter-u2@example.test", PasswordHash: "inviter-hash",
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUser(inviter) error = %v", err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "profile-before@example.test", PasswordHash: "old-hash", GroupID: pointerTo(int64(7)),
		TransferEnable: 1_000, SpeedLimit: 10, DeviceLimit: 2,
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUser(subject) error = %v", err)
	}
	if err := database.CreateSession(ctx, user.ID, "u2-session", "u2-csrf", now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: user.ID, TokenHash: strings.Repeat("7", 64), Name: "u2-client",
	}, now); err != nil {
		t.Fatal(err)
	}
	_, node := createReportingNode(t, database, now)
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO node_device_ips (node_id, user_id, ip, expires_at) VALUES (?, ?, '192.0.2.27', ?);
		INSERT INTO node_user_online (node_id, user_id, connections, expires_at) VALUES (?, ?, 2, ?);
		UPDATE users SET online_count = 2 WHERE id = ?
	`, node.ID, user.ID, now.Add(time.Hour).Unix(), node.ID, user.ID, now.Add(time.Hour).Unix(), user.ID); err != nil {
		t.Fatal(err)
	}

	expiresAt := now.AddDate(0, 2, 0)
	upload, download := int64(11_000), int64(22_000)
	balance, commissionBalance := int64(12_345), int64(6_789)
	commissionType, commissionRate, discount := 2, 18, 80
	telegramID := int64(778899)
	remindExpire, remindTraffic := false, true
	remarks := "U2 priority account"
	isStaff := true
	updated, mutation, err := database.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{
		Revision: user.Revision, Email: "profile-after@example.test", PasswordHash: pointerTo("new-hash"),
		IsStaff: &isStaff, GroupID: pointerTo(int64(7)), TransferEnable: 999, ExpiredAt: &expiresAt,
		SpeedLimit: 1, DeviceLimit: 1, PlanIDSet: true, PlanID: &plan.ID,
		InviteUserEmailSet: true, InviteUserEmail: &inviter.Email,
		TrafficUpload: &upload, TrafficDownload: &download, Balance: &balance,
		CommissionType: &commissionType, CommissionRateSet: true, CommissionRate: &commissionRate,
		CommissionBalance: &commissionBalance, DiscountSet: true, Discount: &discount,
		TelegramIDSet: true, TelegramID: &telegramID, RemindExpire: &remindExpire, RemindTraffic: &remindTraffic,
		RemarksSet: true, Remarks: &remarks,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("UpdateAdminUser(full profile) error = %v", err)
	}
	if updated.Email != "profile-after@example.test" || !updated.IsStaff || updated.PlanID == nil || *updated.PlanID != plan.ID ||
		updated.GroupID == nil || *updated.GroupID != 8 || updated.TransferEnable != 64*bytesPerGiB ||
		updated.SpeedLimit != speedLimit || updated.DeviceLimit != deviceLimit || updated.TrafficUpload != upload ||
		updated.TrafficDownload != download || updated.TrafficUsed != upload+download || updated.Balance != balance ||
		updated.CommissionType != commissionType || updated.CommissionRate == nil || *updated.CommissionRate != commissionRate ||
		updated.CommissionBalance != commissionBalance || updated.Discount == nil || *updated.Discount != discount ||
		updated.InviteUserID == nil || *updated.InviteUserID != inviter.ID || updated.TelegramID == nil || *updated.TelegramID != telegramID ||
		updated.RemindExpire != remindExpire || updated.RemindTraffic != remindTraffic || updated.Remarks == nil || *updated.Remarks != remarks ||
		updated.NextResetAt == nil {
		t.Fatalf("updated full profile = %#v", updated)
	}
	if !mutation.RuntimeChanged || !mutation.AccessStateCleared || mutation.OldGroupID == nil || *mutation.OldGroupID != 7 ||
		mutation.NewGroupID == nil || *mutation.NewGroupID != 8 {
		t.Fatalf("mutation metadata = %#v", mutation)
	}
	found, err := database.FindUserByID(ctx, user.ID)
	if err != nil || found.PasswordHash != "new-hash" {
		t.Fatalf("password hash = %q err=%v", found.PasswordHash, err)
	}
	if _, err := database.AuthenticateSession(ctx, "u2-session", now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("full update left session active: %v", err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("7", 64), now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("full update left access token active: %v", err)
	}
	var devices, onlineRows, onlineCount int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_device_ips WHERE user_id = ?`, user.ID).Scan(&devices); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM node_user_online WHERE user_id = ?`, user.ID).Scan(&onlineRows); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT online_count FROM users WHERE id = ?`, user.ID).Scan(&onlineCount); err != nil {
		t.Fatal(err)
	}
	if devices != 0 || onlineRows != 0 || onlineCount != 0 {
		t.Fatalf("full update retained runtime state: devices=%d online_rows=%d online_count=%d", devices, onlineRows, onlineCount)
	}
}

func TestAdminUserFullProfileUpdateRollsBackOnMissingReferences(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "rollback-u2@example.test", PasswordHash: "original-hash", GroupID: pointerTo(int64(7)),
		TransferEnable: 1024, SpeedLimit: 10, DeviceLimit: 2,
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUser() error = %v", err)
	}

	changedBalance := int64(50_000)
	missingPlanID := int64(999_999)
	if _, _, err := database.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{
		Revision: user.Revision, Email: "must-rollback@example.test", GroupID: pointerTo(int64(8)),
		TransferEnable: 2048, SpeedLimit: 20, DeviceLimit: 3, PlanIDSet: true, PlanID: &missingPlanID,
		Balance: &changedBalance,
	}, now.Add(time.Minute)); !errors.Is(err, ErrAdminUserPlanNotFound) {
		t.Fatalf("missing plan error = %v, want ErrAdminUserPlanNotFound", err)
	}
	fresh, err := database.GetAdminUser(ctx, user.ID)
	if err != nil {
		t.Fatalf("GetAdminUser(after missing plan) error = %v", err)
	}
	if fresh.Email != user.Email || fresh.Revision != user.Revision || fresh.Balance != user.Balance ||
		fresh.GroupID == nil || *fresh.GroupID != 7 || fresh.TransferEnable != user.TransferEnable {
		t.Fatalf("missing plan partially updated user: %#v", fresh)
	}

	missingInviter := "missing-inviter@example.test"
	if _, _, err := database.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{
		Revision: user.Revision, Email: "must-also-rollback@example.test", GroupID: user.GroupID,
		TransferEnable: user.TransferEnable, SpeedLimit: user.SpeedLimit, DeviceLimit: user.DeviceLimit,
		InviteUserEmailSet: true, InviteUserEmail: &missingInviter,
	}, now.Add(2*time.Minute)); !errors.Is(err, ErrAdminInviteUserNotFound) {
		t.Fatalf("missing inviter error = %v, want ErrAdminInviteUserNotFound", err)
	}
	fresh, err = database.GetAdminUser(ctx, user.ID)
	if err != nil || fresh.Email != user.Email || fresh.Revision != user.Revision || fresh.InviteUserID != nil {
		t.Fatalf("missing inviter partially updated user: %#v err=%v", fresh, err)
	}
}

func TestAdminUserRolesRequireDistributorNameAndRevokeCredentials(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 9, 0, 0, 0, time.UTC)

	if _, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "invalid-distributor@example.test", PasswordHash: "hash", IsDistributor: true,
	}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("CreateAdminUser(distributor without name) error = %v, want ErrInvalidInput", err)
	}

	created, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "roles@example.test", PasswordHash: "hash", IsAdmin: true, IsStaff: true,
		IsDistributor: true, DistributorName: "  华东渠道  ",
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUser(roles) error = %v", err)
	}
	if !created.IsAdmin || !created.IsStaff || !created.IsDistributor || created.DistributorName == nil || *created.DistributorName != "华东渠道" {
		t.Fatalf("created roles = %#v", created)
	}
	if err := database.CreateSession(ctx, created.ID, "roles-session", "csrf", now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: created.ID, TokenHash: strings.Repeat("9", 64), Name: "roles-client",
	}, now); err != nil {
		t.Fatal(err)
	}
	session, err := database.AuthenticateSession(ctx, "roles-session", now.Add(time.Minute))
	if err != nil || !session.IsAdmin || !session.IsStaff || !session.IsDistributor || session.DistributorName == nil || *session.DistributorName != "华东渠道" {
		t.Fatalf("session roles = %#v err=%v", session, err)
	}

	disabled := false
	updated, _, err := database.UpdateAdminUser(ctx, created.ID, UpdateAdminUserInput{
		Revision: created.Revision, Email: created.Email, IsDistributor: &disabled, DistributorName: pointerTo("不得保留"),
	}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("UpdateAdminUser(disable distributor) error = %v", err)
	}
	if updated.IsDistributor || updated.DistributorName != nil || !updated.IsAdmin || !updated.IsStaff {
		t.Fatalf("updated roles = %#v", updated)
	}
	if _, err := database.AuthenticateSession(ctx, "roles-session", now.Add(3*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("role change left session active: %v", err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("9", 64), now.Add(3*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("role change left access token active: %v", err)
	}
}

func TestAdminPasswordResetRevokesSessionsAndChecksRevision(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "password@example.test", PasswordHash: "old-hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.CreateSession(ctx, user.ID, "password-session", "csrf", now.Add(time.Hour), now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAccessToken(ctx, CreateAccessTokenInput{
		UserID: user.ID, TokenHash: strings.Repeat("2", 64), Name: "admin-password-client",
	}, now); err != nil {
		t.Fatal(err)
	}
	updated, err := database.ResetAdminUserPassword(ctx, user.ID, user.Revision, "new-hash", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Revision != user.Revision+1 {
		t.Fatalf("revision = %d", updated.Revision)
	}
	if _, err := database.AuthenticateSession(ctx, "password-session", now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("password reset left session active: %v", err)
	}
	if _, err := database.AuthenticateAccessToken(ctx, strings.Repeat("2", 64), now.Add(2*time.Minute)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("password reset left access token active: %v", err)
	}
	found, err := database.FindUserByID(ctx, user.ID)
	if err != nil || found.PasswordHash != "new-hash" {
		t.Fatalf("password hash = %q err=%v", found.PasswordHash, err)
	}
	if _, err := database.ResetAdminUserPassword(ctx, user.ID, user.Revision, "overwrite", now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale password reset error = %v", err)
	}
}

func TestAdminUserUpdateSupportsLegacyNullRuntimeIdentityAndReportsEntitlementChanges(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC)
	if _, err := database.BootstrapAdmin(ctx, "admin@example.test", "hash", now); err != nil {
		t.Fatal(err)
	}
	admin, err := database.FindUserByEmail(ctx, "admin@example.test")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := database.GetAdminUser(ctx, admin.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, mutation, err := database.UpdateAdminUser(ctx, admin.ID, UpdateAdminUserInput{
		Revision: detail.Revision, Email: detail.Email, TransferEnable: 1_000,
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("UpdateAdminUser(null uuid) error = %v", err)
	}
	if updated.TransferEnable != 1_000 || mutation.UUID != "" || !mutation.RuntimeChanged || !mutation.AccessStateCleared {
		t.Fatalf("updated=%#v mutation=%#v", updated, mutation)
	}
}

func TestAdminUserDirectoryQueryPlansUseIndexes(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	queries := []struct {
		name string
		sql  string
		args []any
	}{
		{"default", `EXPLAIN QUERY PLAN SELECT id FROM users WHERE account_kind = 'human' AND id < ? ORDER BY id DESC LIMIT ?`, []any{1_000_000, 51}},
		{"banned", `EXPLAIN QUERY PLAN SELECT id FROM users WHERE account_kind = 'human' AND banned = ? AND id < ? ORDER BY id DESC LIMIT ?`, []any{1, 1_000_000, 51}},
		{"group", `EXPLAIN QUERY PLAN SELECT id FROM users WHERE account_kind = 'human' AND group_id = ? AND id < ? ORDER BY id DESC LIMIT ?`, []any{7, 1_000_000, 51}},
		{"email-prefix", `EXPLAIN QUERY PLAN SELECT id FROM users WHERE account_kind = 'human' AND email LIKE ? ESCAPE '\\' ORDER BY email COLLATE NOCASE, id LIMIT ?`, []any{"alpha%", 51}},
		{"plan", `EXPLAIN QUERY PLAN SELECT id FROM users WHERE account_kind = 'human' AND plan_id = ? ORDER BY id DESC LIMIT ?`, []any{7, 51}},
		{"balance-sort", `EXPLAIN QUERY PLAN SELECT id FROM users WHERE account_kind = 'human' ORDER BY balance DESC, id DESC LIMIT ?`, []any{51}},
		{"traffic-used-sort", `EXPLAIN QUERY PLAN SELECT id FROM users WHERE account_kind = 'human' ORDER BY (traffic_u + traffic_d) DESC, id DESC LIMIT ?`, []any{51}},
	}
	for _, test := range queries {
		t.Run(test.name, func(t *testing.T) {
			rows, err := database.db.QueryContext(ctx, test.sql, test.args...)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			var details []string
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				details = append(details, detail)
			}
			plan := strings.Join(details, " ")
			if strings.Contains(plan, "SCAN users") || !strings.Contains(plan, "SEARCH users") {
				t.Fatalf("query plan is not indexed: %s", plan)
			}
		})
	}
}

func BenchmarkListAdminUsers100K(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-admin-users?mode=memory&cache=shared")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	now := time.Date(2026, 8, 24, 12, 0, 0, 0, time.UTC).Unix()
	defer database.lockWrite()()
	if _, err := database.db.ExecContext(ctx, `
		INSERT INTO server_groups (id, name, created_at, updated_at)
		VALUES (7, 'bench-7', ?, ?), (8, 'bench-8', ?, ?), (9, 'bench-9', ?, ?)
	`, now, now, now, now, now, now); err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO users (email, password_hash, account_kind, banned, group_id, transfer_enable, traffic_u, traffic_d, balance, commission_balance, online_count, uuid, subscription_token, created_at, updated_at)
		VALUES (?, 'hash', 'human', ?, ?, 1000000, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`)
	if err != nil {
		b.Fatal(err)
	}
	for index := 0; index < 100_000; index++ {
		_, err := statement.ExecContext(ctx, fmt.Sprintf("bench-%06d@example.test", index), index%2, 7+index%3,
			index*3, index*5, index*11, index*7, index%25,
			fmt.Sprintf("00000000-0000-4000-8000-%012d", index), testSubscriptionToken(b), now, now)
		if err != nil {
			b.Fatal(err)
		}
	}
	_ = statement.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	b.Run("group-and-banned", func(b *testing.B) {
		banned := true
		groupID := int64(8)
		b.ReportAllocs()
		for range b.N {
			if _, err := database.ListAdminUsers(ctx, AdminUserFilter{Limit: 50, Banned: &banned, GroupID: &groupID}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("sparse-email-prefix", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := database.ListAdminUsers(ctx, AdminUserFilter{Limit: 50, EmailPrefix: "bench-000"}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("paged-balance-sort", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := database.ListAdminUsers(ctx, AdminUserFilter{
				Page: 1, PageSize: 50, SortBy: AdminUserSortBalance, SortDescending: true,
				Rules: []AdminUserFilterRule{{Field: AdminUserFieldBalance, Operator: AdminUserOperatorGreaterOrEqual, Values: []string{"0"}}},
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("paged-multi-filter", func(b *testing.B) {
		b.ReportAllocs()
		for range b.N {
			if _, err := database.ListAdminUsers(ctx, AdminUserFilter{
				Page: 1, PageSize: 50, SortBy: AdminUserSortTrafficUsed, SortDescending: true,
				Rules: []AdminUserFilterRule{
					{Field: AdminUserFieldBanned, Operator: AdminUserOperatorEqual, Values: []string{"true"}},
					{Field: AdminUserFieldTrafficUsed, Operator: AdminUserOperatorGreater, Values: []string{"400000"}},
				},
			}); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func BenchmarkUpdateAdminUserFullProfile(b *testing.B) {
	database, err := OpenSQLite("file:benchmark-admin-user-update?mode=memory&cache=shared")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	seedAdminUserGroups(b, database, now)
	resetMethod := 1
	speedLimit := 100
	deviceLimit := 5
	plans := make([]Plan, 0, 2)
	for index, groupID := range []int64{7, 8} {
		plan, createErr := database.CreatePlan(ctx, SavePlanInput{
			GroupID: pointerTo(groupID), TransferEnableGiB: int64(64 + index), Name: fmt.Sprintf("benchmark-plan-%d", index),
			SpeedLimit: &speedLimit, DeviceLimit: &deviceLimit, ResetTrafficMethod: &resetMethod,
			Prices: PlanPrices{}, Tags: []string{},
		}, now)
		if createErr != nil {
			b.Fatal(createErr)
		}
		plans = append(plans, plan)
	}
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "benchmark-inviter@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		b.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "benchmark-profile@example.test", PasswordHash: "hash", GroupID: pointerTo(int64(7)), TransferEnable: 1,
	}, now)
	if err != nil {
		b.Fatal(err)
	}
	upload, download := int64(1), int64(2)
	balance, commissionBalance := int64(12_345), int64(6_789)
	commissionType, commissionRate, discount := 2, 18, 80
	telegramID := int64(778899)
	remindExpire, remindTraffic := false, true
	remarks := "benchmark full profile"

	b.ReportAllocs()
	b.ResetTimer()
	for index := range b.N {
		plan := plans[index%len(plans)]
		updated, _, updateErr := database.UpdateAdminUser(ctx, user.ID, UpdateAdminUserInput{
			Revision: user.Revision, Email: user.Email, GroupID: user.GroupID, PlanIDSet: true, PlanID: &plan.ID,
			InviteUserEmailSet: true, InviteUserEmail: &inviter.Email, TransferEnable: user.TransferEnable,
			TrafficUpload: &upload, TrafficDownload: &download, ExpiredAt: user.ExpiredAt,
			SpeedLimit: user.SpeedLimit, DeviceLimit: user.DeviceLimit, Banned: user.Banned,
			Balance: &balance, CommissionType: &commissionType, CommissionRateSet: true, CommissionRate: &commissionRate,
			CommissionBalance: &commissionBalance, DiscountSet: true, Discount: &discount,
			TelegramIDSet: true, TelegramID: &telegramID, RemindExpire: &remindExpire, RemindTraffic: &remindTraffic,
			RemarksSet: true, Remarks: &remarks,
		}, now)
		if updateErr != nil {
			b.Fatal(updateErr)
		}
		user = updated
		upload++
		download++
	}
}

func pointerTo[T any](value T) *T { return &value }

func seedAdminUserGroups(t testing.TB, database *Store, now time.Time) {
	t.Helper()
	for _, name := range []string{"group-7", "group-8"} {
		if _, err := database.CreateServerGroup(context.Background(), name, now); err != nil {
			t.Fatal(err)
		}
	}
	// Tests use stable IDs 7 and 8 to mirror the runtime fixtures used elsewhere.
	if _, err := database.db.Exec(`UPDATE server_groups SET id = id + 6`); err != nil {
		t.Fatal(err)
	}
}
