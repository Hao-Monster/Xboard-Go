package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAdminUserSchemaV37AddsProfileFieldsAndDirectoryIndexes(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()

	if CurrentSchemaVersion() != 48 {
		t.Fatalf("CurrentSchemaVersion() = %d, want 48", CurrentSchemaVersion())
	}
	for _, column := range []string{"telegram_id", "remind_expire", "remind_traffic", "remarks"} {
		var found int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = ?`, column).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found != 1 {
			t.Fatalf("users.%s count = %d, want 1", column, found)
		}
	}
	for _, index := range []string{
		"idx_users_directory_plan_id", "idx_users_directory_expired_at", "idx_users_directory_online_count",
		"idx_users_directory_total_used", "idx_users_directory_balance", "idx_users_directory_commission_balance",
	} {
		var found int
		if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type = 'index' AND name = ?`, index).Scan(&found); err != nil {
			t.Fatal(err)
		}
		if found != 1 {
			t.Fatalf("index %s count = %d, want 1", index, found)
		}
	}

	created, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "schema-v37@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id = 0 WHERE id = ?`, created.ID); err == nil {
		t.Fatal("zero Telegram id must be rejected")
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET remarks = ? WHERE id = ?`, strings.Repeat("x", 4097), created.ID); err == nil {
		t.Fatal("oversized remarks must be rejected")
	}
}

func TestSchemaV37PreservesV36UsersAndBackfillsReminderDefaults(t *testing.T) {
	database := newTestStore(t)
	removeSchemaV39ForMigrationTest(t, database)
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 4, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "preserved-v36@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance = 12345, commission_balance = 678 WHERE id = ?`, user.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		DROP INDEX idx_users_directory_plan_id;
		DROP INDEX idx_users_directory_expired_at;
		DROP INDEX idx_users_directory_online_count;
		DROP INDEX idx_users_directory_total_used;
		DROP INDEX idx_users_directory_transfer_enable;
		DROP INDEX idx_users_directory_balance;
		DROP INDEX idx_users_directory_commission_balance;
		DROP INDEX idx_users_directory_created_at;
		DROP INDEX idx_users_reminder_expire;
		DROP INDEX idx_users_reminder_traffic;
		DROP INDEX idx_users_unique_telegram_id;
		ALTER TABLE users DROP COLUMN telegram_id;
		ALTER TABLE users DROP COLUMN remind_expire;
		ALTER TABLE users DROP COLUMN remind_traffic;
		ALTER TABLE users DROP COLUMN remarks;
		PRAGMA user_version = 36;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v36 to v37) error = %v", err)
	}
	preserved, err := database.GetAdminUser(ctx, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.Email != user.Email || preserved.Balance != 12345 || preserved.CommissionBalance != 678 ||
		preserved.TelegramID != nil || !preserved.RemindExpire || !preserved.RemindTraffic || preserved.Remarks != nil {
		t.Fatalf("preserved user = %#v", preserved)
	}
}

func TestAdminUserPagedDirectoryJoinsProfilesFiltersAndSortsWithoutSecrets(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 27, 3, 0, 0, 0, time.UTC)
	seedAdminUserGroups(t, database, now)
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "旗舰套餐", GroupID: pointerTo(int64(7)), TransferEnableGiB: 100, Prices: PlanPrices{"monthly": 1000},
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "inviter@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	alpha, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "alpha@example.test", PasswordHash: "hash", GroupID: pointerTo(int64(7)), TransferEnable: 1_000,
	}, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	beta, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "beta@example.test", PasswordHash: "hash", GroupID: pointerTo(int64(8)), TransferEnable: 2_000, Banned: true,
	}, now.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET plan_id = ?, invite_user_id = ?, traffic_u = 120, traffic_d = 80,
			balance = 2500, commission_balance = 900, commission_type = 2, commission_rate = 15,
			discount = 80, online_count = 3, next_reset_at = ?, last_reset_at = ?, reset_count = 4,
			telegram_id = 778899, remind_expire = 0, remind_traffic = 1, remarks = '重点客户'
		WHERE id = ?
	`, plan.ID, inviter.ID, now.Add(24*time.Hour).Unix(), now.Add(-24*time.Hour).Unix(), alpha.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET balance = 5000, remarks = '普通客户' WHERE id = ?`, beta.ID); err != nil {
		t.Fatal(err)
	}

	page, err := database.ListAdminUsers(ctx, AdminUserFilter{
		Page: 1, PageSize: 1, SortBy: AdminUserSortBalance, SortDescending: true,
		Rules: []AdminUserFilterRule{
			{Field: AdminUserFieldEmail, Operator: AdminUserOperatorContains, Values: []string{"example.test"}},
			{Field: AdminUserFieldID, Operator: AdminUserOperatorIn, Values: []string{jsonNumber(alpha.ID), jsonNumber(beta.ID)}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || page.Page != 1 || page.PageSize != 1 || len(page.Items) != 1 || page.Items[0].ID != beta.ID {
		t.Fatalf("first page = %#v", page)
	}
	second, err := database.ListAdminUsers(ctx, AdminUserFilter{
		Page: 2, PageSize: 1, SortBy: AdminUserSortBalance, SortDescending: true,
		Rules: []AdminUserFilterRule{{Field: AdminUserFieldID, Operator: AdminUserOperatorIn, Values: []string{jsonNumber(alpha.ID), jsonNumber(beta.ID)}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Total != 2 || len(second.Items) != 1 || second.Items[0].ID != alpha.ID {
		t.Fatalf("second page = %#v", second)
	}
	multiSorted, err := database.ListAdminUsers(ctx, AdminUserFilter{
		Page: 1, PageSize: 2,
		Sorts: []AdminUserSortRule{{Field: AdminUserSortBanned}, {Field: AdminUserSortBalance, Descending: true}},
		Rules: []AdminUserFilterRule{{Field: AdminUserFieldID, Operator: AdminUserOperatorIn, Values: []string{jsonNumber(alpha.ID), jsonNumber(beta.ID)}}},
	})
	if err != nil || len(multiSorted.Items) != 2 || multiSorted.Items[0].ID != alpha.ID || multiSorted.Items[1].ID != beta.ID {
		t.Fatalf("multi-sorted page = (%#v, %v)", multiSorted, err)
	}
	detail := second.Items[0]
	if detail.PlanID == nil || *detail.PlanID != plan.ID || detail.PlanName == nil || *detail.PlanName != plan.Name ||
		detail.GroupName == nil || *detail.GroupName != "group-7" || detail.InviteUserID == nil || *detail.InviteUserID != inviter.ID ||
		detail.InviteUserEmail == nil || *detail.InviteUserEmail != inviter.Email || detail.TrafficUsed != 200 ||
		detail.Balance != 2500 || detail.CommissionBalance != 900 || detail.CommissionType != 2 || detail.CommissionRate == nil ||
		*detail.CommissionRate != 15 || detail.Discount == nil || *detail.Discount != 80 || detail.TelegramID == nil ||
		*detail.TelegramID != 778899 || detail.RemindExpire || !detail.RemindTraffic || detail.Remarks == nil || *detail.Remarks != "重点客户" ||
		detail.NextResetAt == nil || detail.LastResetAt == nil || detail.ResetCount != 4 {
		t.Fatalf("joined detail = %#v", detail)
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"subscription_token", "subscribe_url", `"uuid"`} {
		if strings.Contains(string(encoded), secret) {
			t.Fatalf("admin user directory leaked %q: %s", secret, encoded)
		}
	}
	secretToken := ""
	if err := database.db.QueryRowContext(ctx, `SELECT subscription_token FROM users WHERE id = ?`, alpha.ID).Scan(&secretToken); err != nil {
		t.Fatal(err)
	}
	secretMatch, err := database.ListAdminUsers(ctx, AdminUserFilter{Page: 1, PageSize: 20, Rules: []AdminUserFilterRule{{
		Field: AdminUserFieldSubscriptionToken, Operator: AdminUserOperatorEqual, Values: []string{secretToken},
	}}})
	if err != nil || secretMatch.Total != 1 || secretMatch.Items[0].ID != alpha.ID {
		t.Fatalf("exact secret filter = (%#v, %v)", secretMatch, err)
	}
	andPage, err := database.ListAdminUsers(ctx, AdminUserFilter{Page: 1, PageSize: 20, Rules: []AdminUserFilterRule{
		{Field: AdminUserFieldEmail, Operator: AdminUserOperatorEqual, Values: []string{alpha.Email}},
		{Field: AdminUserFieldEmail, Operator: AdminUserOperatorEqual, Values: []string{beta.Email}},
	}})
	if err != nil || andPage.Total != 0 {
		t.Fatalf("legacy-compatible same-field AND page = (%#v, %v)", andPage, err)
	}
}

func TestAdminUserDirectoryRejectsUnallowlistedFiltersAndOperators(t *testing.T) {
	database := newTestStore(t)
	cases := []AdminUserFilter{
		{Page: 1, PageSize: 20, Rules: []AdminUserFilterRule{{Field: "password_hash", Operator: AdminUserOperatorEqual, Values: []string{"hash"}}}},
		{Page: 1, PageSize: 20, Rules: []AdminUserFilterRule{{Field: AdminUserFieldID, Operator: AdminUserOperatorContains, Values: []string{"1"}}}},
		{Page: 1, PageSize: 20, Rules: []AdminUserFilterRule{{Field: AdminUserFieldUUID, Operator: AdminUserOperatorContains, Values: []string{"0000"}}}},
		{Page: 1, PageSize: 20, Rules: make([]AdminUserFilterRule, 11)},
		{Page: 1, PageSize: 201},
		{Page: 1_000_001, PageSize: 20},
		{Page: 1, PageSize: 20, Sorts: []AdminUserSortRule{{Field: AdminUserSortID}, {Field: AdminUserSortID}}},
		{Page: 1, PageSize: 20, Sorts: make([]AdminUserSortRule, 4)},
	}
	for index, filter := range cases {
		if _, err := database.ListAdminUsers(context.Background(), filter); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("case %d error = %v, want ErrInvalidInput", index, err)
		}
	}
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
