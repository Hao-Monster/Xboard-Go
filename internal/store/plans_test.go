package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func BenchmarkListPlansTenThousandRows(b *testing.B) {
	database, err := OpenSQLite(fmt.Sprintf("file:plan-benchmark-%d?mode=memory&cache=shared", time.Now().UnixNano()))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if err := database.Migrate(ctx); err != nil {
		b.Fatal(err)
	}
	tx, err := database.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	statement, err := tx.Prepare(`
		INSERT INTO plans (
			id, transfer_enable_gib, name, show, sort_position, renew, content, prices_json,
			sell, tags_json, revision, created_at, updated_at
		) VALUES (?, 100, ?, 1, ?, 1, '', '{"monthly":999}', 1, '[]', 1, 1, 1)
	`)
	if err != nil {
		_ = tx.Rollback()
		b.Fatal(err)
	}
	for index := 1; index <= 10_000; index++ {
		if _, err := statement.Exec(index, fmt.Sprintf("Plan %05d", index), index-1); err != nil {
			_ = statement.Close()
			_ = tx.Rollback()
			b.Fatal(err)
		}
	}
	_ = statement.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		plans, err := database.ListPlans(ctx, time.Unix(100, 0))
		if err != nil || len(plans) != 10_000 {
			b.Fatalf("ListPlans() rows=%d error=%v", len(plans), err)
		}
	}
	b.ReportMetric(10_000, "plans/op")
}

func TestPlanLifecycleVisibilityCapacityAndForceUpdate(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	group, err := database.CreateServerGroup(ctx, "Premium", now)
	if err != nil {
		t.Fatal(err)
	}
	capacity := 1
	speed := 200
	devices := 3
	method := 1
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: " Pro ", GroupID: &group.ID, TransferEnableGiB: 100,
		SpeedLimit: &speed, DeviceLimit: &devices, CapacityLimit: &capacity,
		ResetTrafficMethod: &method, Prices: PlanPrices{"monthly": 123, "quarterly": 345},
		Tags: []string{" 推荐 ", "推荐", "稳定"}, Content: "{{transfer}} / {{speed}} / {{devices}} / {{reset_method}}",
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	if plan.Name != "Pro" || plan.Show || plan.Sell || !plan.Renew || plan.Revision != 1 ||
		!reflect.DeepEqual(plan.Tags, []string{"推荐", "稳定"}) || plan.Prices["monthly"] != 123 {
		t.Fatalf("created plan = %#v", plan)
	}

	state, err := database.SetPlanState(ctx, plan.ID, plan.Revision, PlanState{Show: true, Sell: true, Renew: false}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("SetPlanState() error = %v", err)
	}
	guest, err := database.ListGuestPlanOffers(ctx, now.Add(time.Minute))
	if err != nil || len(guest) != 1 || guest[0].CapacityRemaining == nil || *guest[0].CapacityRemaining != 1 || !guest[0].CanPurchase ||
		guest[0].Content != "100 / 200 / 3 / 按月" {
		t.Fatalf("guest offers before capacity = (%#v, %v)", guest, err)
	}

	user, err := database.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "plan-user@example.test", PasswordHash: "hash", UUID: "22d948e2-4259-45e1-8939-88461528c0b9",
		GroupID: group.ID, TransferEnable: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET plan_id = ?, expired_at = ? WHERE id = ?`, plan.ID, now.Add(24*time.Hour).Unix(), user.ID); err != nil {
		t.Fatal(err)
	}
	guest, err = database.ListGuestPlanOffers(ctx, now.Add(time.Minute))
	if err != nil || len(guest) != 0 {
		t.Fatalf("sold-out guest offers = (%#v, %v)", guest, err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET expired_at = ? WHERE id = ?`, now.Add(time.Minute).Unix(), user.ID); err != nil {
		t.Fatal(err)
	}
	adminPlans, err := database.ListPlans(ctx, now.Add(time.Minute))
	if err != nil || len(adminPlans) != 1 || adminPlans[0].UsersCount != 1 || adminPlans[0].ActiveUsersCount != 0 || adminPlans[0].CapacityUsersCount != 1 {
		t.Fatalf("plan statistics at expiry boundary = (%#v, %v)", adminPlans, err)
	}
	guest, err = database.ListGuestPlanOffers(ctx, now.Add(time.Minute))
	if err != nil || len(guest) != 0 {
		t.Fatalf("capacity at expiry boundary = (%#v, %v)", guest, err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET expired_at = ? WHERE id = ?`, now.Add(24*time.Hour).Unix(), user.ID); err != nil {
		t.Fatal(err)
	}
	userOffers, err := database.ListUserPlanOffers(ctx, user.ID, now.Add(time.Minute))
	if err != nil || len(userOffers) != 0 {
		t.Fatalf("non-renewable current user offers = (%#v, %v)", userOffers, err)
	}

	capacity = 0
	updated, err := database.UpdatePlan(ctx, plan.ID, state.Revision, SavePlanInput{
		Name: "Pro 2", GroupID: nil, TransferEnableGiB: 200,
		SpeedLimit: intPointer(500), DeviceLimit: intPointer(5), CapacityLimit: &capacity,
		ResetTrafficMethod: nil, Prices: PlanPrices{"yearly": 9999}, Tags: []string{"长期"}, Content: "content",
	}, true, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("UpdatePlan(force) error = %v", err)
	}
	guest, err = database.ListGuestPlanOffers(ctx, now.Add(2*time.Minute))
	if err != nil || len(guest) != 1 || guest[0].CapacityRemaining != nil || !guest[0].CanPurchase {
		t.Fatalf("unlimited guest offers = (%#v, %v)", guest, err)
	}
	var userGroup *int64
	var transfer int64
	var userSpeed, userDevices int
	var nextReset sql.NullInt64
	if err := database.db.QueryRowContext(ctx, `SELECT group_id, transfer_enable, speed_limit, device_limit, next_reset_at FROM users WHERE id = ?`, user.ID).
		Scan(&userGroup, &transfer, &userSpeed, &userDevices, &nextReset); err != nil {
		t.Fatal(err)
	}
	if userGroup != nil || transfer != 200*(1<<30) || userSpeed != 500 || userDevices != 5 || !nextReset.Valid || nextReset.Int64 <= now.Unix() {
		t.Fatalf("forced user benefits = group=%v transfer=%d speed=%d devices=%d next_reset=%#v", userGroup, transfer, userSpeed, userDevices, nextReset)
	}
	if _, err := database.UpdatePlan(ctx, plan.ID, state.Revision, SavePlanInput{Name: "stale", TransferEnableGiB: 1}, false, now); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("UpdatePlan(stale) error = %v, want ErrRevisionConflict", err)
	}
	if err := database.DeletePlan(ctx, plan.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeletePlan(referenced) error = %v, want ErrConflict", err)
	}
	_ = updated
}

func TestPlanReorderAndValidationAreAtomic(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	first, err := database.CreatePlan(ctx, SavePlanInput{Name: "First", TransferEnableGiB: 1, Prices: PlanPrices{"monthly": 1}}, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := database.CreatePlan(ctx, SavePlanInput{Name: "Second", TransferEnableGiB: 2, Prices: PlanPrices{"yearly": 2}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ReorderPlans(ctx, []int64{first.ID, first.ID}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ReorderPlans(duplicate) error = %v", err)
	}
	if _, err := database.ReorderPlans(ctx, []int64{second.ID}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("ReorderPlans(partial) error = %v", err)
	}
	ordered, err := database.ReorderPlans(ctx, []int64{first.ID, second.ID}, now)
	if err != nil || len(ordered) != 2 || ordered[0].ID != first.ID || ordered[0].SortPosition != 0 || ordered[1].SortPosition != 1 {
		t.Fatalf("ReorderPlans() = (%#v, %v)", ordered, err)
	}
	invalid := []SavePlanInput{
		{Name: "", TransferEnableGiB: 1},
		{Name: "bad period", TransferEnableGiB: 1, Prices: PlanPrices{"weekly": 1}},
		{Name: "negative price", TransferEnableGiB: 1, Prices: PlanPrices{"monthly": -1}},
		{Name: "no traffic", TransferEnableGiB: 0},
	}
	for _, input := range invalid {
		if _, err := database.CreatePlan(ctx, input, now); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("CreatePlan(%#v) error = %v, want ErrInvalidInput", input, err)
		}
	}
}

func TestPlanSupportsAllLegacyBillingPeriods(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	want := PlanPrices{
		"monthly":       101,
		"quarterly":     202,
		"half_yearly":   303,
		"yearly":        404,
		"two_yearly":    505,
		"three_yearly":  606,
		"onetime":       707,
		"reset_traffic": 808,
	}
	created, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "Every legacy period", TransferEnableGiB: 100, Prices: want,
	}, now)
	if err != nil {
		t.Fatalf("CreatePlan() error = %v", err)
	}
	if !reflect.DeepEqual(created.Prices, want) {
		t.Fatalf("CreatePlan() prices = %#v, want %#v", created.Prices, want)
	}

	plans, err := database.ListPlans(ctx, now)
	if err != nil {
		t.Fatalf("ListPlans() error = %v", err)
	}
	if len(plans) != 1 || !reflect.DeepEqual(plans[0].Prices, want) {
		t.Fatalf("ListPlans() = %#v, want all legacy prices %#v", plans, want)
	}
}

func TestCalculateNextTrafficResetMatchesLegacyCalendarRules(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 1, 31, 12, 0, 0, 0, shanghai)
	expires := time.Date(2028, 2, 29, 8, 30, 15, 0, shanghai)
	cases := []struct {
		name   string
		plan   *int
		system int
		want   *time.Time
	}{
		{name: "follow monthly expiry clamps day", system: 1, want: timePointer(time.Date(2026, 2, 28, 8, 30, 15, 0, shanghai))},
		{name: "first day month", plan: intPointer(0), system: 2, want: timePointer(time.Date(2026, 2, 1, 0, 0, 0, 0, shanghai))},
		{name: "never", plan: intPointer(2), system: 1},
		{name: "first day year", plan: intPointer(3), system: 1, want: timePointer(time.Date(2027, 1, 1, 0, 0, 0, 0, shanghai))},
		{name: "yearly leap clamps", plan: intPointer(4), system: 1, want: timePointer(time.Date(2026, 2, 28, 8, 30, 15, 0, shanghai))},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			got := CalculateNextTrafficReset(test.plan, test.system, &expires, now)
			if !equalTimePointers(got, test.want) {
				t.Fatalf("CalculateNextTrafficReset() = %v, want %v", got, test.want)
			}
		})
	}
	if got := CalculateNextTrafficReset(intPointer(1), 1, nil, now); got != nil {
		t.Fatalf("permanent user reset = %v, want nil", got)
	}
}

func TestProcessDueTrafficResetsIsBoundedAndExactlyOnce(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	method := 0
	plan, err := database.CreatePlan(ctx, SavePlanInput{
		Name: "Reset plan", TransferEnableGiB: 1, ResetTrafficMethod: &method,
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	group, err := database.CreateServerGroup(ctx, "Reset users", now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "reset-user@example.test", PasswordHash: "hash", UUID: "2f135f5d-40d7-43c4-a756-fd13b5b17ee3",
		GroupID: group.ID, TransferEnable: 1 << 30,
	}, now.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	scheduled := now.Add(-time.Minute)
	expires := now.Add(365 * 24 * time.Hour)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET plan_id = ?, traffic_u = 11, traffic_d = 22, expired_at = ?, next_reset_at = ? WHERE id = ?
	`, plan.ID, expires.Unix(), scheduled.Unix(), user.ID); err != nil {
		t.Fatal(err)
	}
	result, err := database.ProcessDueTrafficResets(ctx, now, 1)
	if err != nil || result.Processed != 1 || result.Remaining != 0 {
		t.Fatalf("ProcessDueTrafficResets() = (%#v, %v)", result, err)
	}
	second, err := database.ProcessDueTrafficResets(ctx, now, 1)
	if err != nil || second.Processed != 0 {
		t.Fatalf("ProcessDueTrafficResets(second) = (%#v, %v)", second, err)
	}
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET traffic_u = 44, traffic_d = 55, expired_at = ?, next_reset_at = ? WHERE id = ?
	`, now.Unix(), scheduled.Unix(), user.ID); err != nil {
		t.Fatal(err)
	}
	expired, err := database.ProcessDueTrafficResets(ctx, now, 1)
	if err != nil || expired.Processed != 0 || expired.Remaining != 0 {
		t.Fatalf("ProcessDueTrafficResets(expired at boundary) = (%#v, %v)", expired, err)
	}
	var upload, download, resetCount int64
	var nextReset int64
	if err := database.db.QueryRowContext(ctx, `SELECT traffic_u, traffic_d, reset_count, next_reset_at FROM users WHERE id = ?`, user.ID).
		Scan(&upload, &download, &resetCount, &nextReset); err != nil {
		t.Fatal(err)
	}
	if upload != 44 || download != 55 || resetCount != 1 || nextReset != scheduled.Unix() {
		t.Fatalf("reset user state = u=%d d=%d count=%d next=%d", upload, download, resetCount, nextReset)
	}
	var logs int
	var loggedUpload, loggedDownload int64
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*), COALESCE(MAX(upload_before), 0), COALESCE(MAX(download_before), 0)
		FROM traffic_reset_logs WHERE user_id = ?
	`, user.ID).Scan(&logs, &loggedUpload, &loggedDownload); err != nil {
		t.Fatal(err)
	}
	if logs != 1 || loggedUpload != 11 || loggedDownload != 22 {
		t.Fatalf("reset logs = count=%d u=%d d=%d", logs, loggedUpload, loggedDownload)
	}
}

func intPointer(value int) *int { return &value }

func timePointer(value time.Time) *time.Time { return &value }

func equalTimePointers(left, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
