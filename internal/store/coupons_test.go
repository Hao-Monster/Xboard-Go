package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCouponQuoteEnforcesStatusTimePlanPeriodAndUserLimit(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100_000, "yearly": 900_000}, nil)
	otherPlan, err := database.CreatePlan(ctx, SavePlanInput{Name: "Other plan", TransferEnableGiB: 1, Prices: PlanPrices{"monthly": 50_000}}, now)
	if err != nil {
		t.Fatal(err)
	}
	one := 1
	coupon, err := database.CreateCoupon(ctx, SaveCouponInput{
		Code: " FIXED123 ", Name: "固定 12.34", Type: CouponTypeFixed, Value: 1_234, Show: true,
		LimitUse: &one, LimitUseWithUser: &one, LimitPlanIDs: []int64{plan.ID}, LimitPeriods: []string{"month_price"},
		StartedAt: now.Add(-time.Minute), EndedAt: now.Add(time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if coupon.Code != "FIXED123" || len(coupon.LimitPeriods) != 1 || coupon.LimitPeriods[0] != "monthly" {
		t.Fatalf("normalized coupon = %#v", coupon)
	}
	quote, err := database.CheckCoupon(ctx, CouponCheckInput{UserID: userID, PlanID: plan.ID, Period: "month_price", Code: "FIXED123"}, now)
	if err != nil || quote.CouponDiscountAmount != 1_234 || quote.TotalAfterCoupon != 98_766 {
		t.Fatalf("CheckCoupon() = (%#v, %v)", quote, err)
	}
	for name, test := range map[string]struct {
		input CouponCheckInput
		want  error
	}{
		"wrong plan":   {CouponCheckInput{UserID: userID, PlanID: otherPlan.ID, Period: "monthly", Code: coupon.Code}, ErrCouponPlanRestricted},
		"wrong period": {CouponCheckInput{UserID: userID, PlanID: plan.ID, Period: "year_price", Code: coupon.Code}, ErrCouponPeriodRestricted},
		"not started":  {CouponCheckInput{UserID: userID, PlanID: plan.ID, Period: "monthly", Code: coupon.Code}, ErrCouponNotStarted},
		"expired":      {CouponCheckInput{UserID: userID, PlanID: plan.ID, Period: "monthly", Code: coupon.Code}, ErrCouponExpired},
	} {
		t.Run(name, func(t *testing.T) {
			at := now
			switch name {
			case "not started":
				at = coupon.StartedAt.Add(-time.Second)
			case "expired":
				at = coupon.EndedAt.Add(time.Second)
			}
			if _, err := database.CheckCoupon(ctx, test.input, at); !errors.Is(err, test.want) {
				t.Fatalf("CheckCoupon() error = %v, want %v", err, test.want)
			}
		})
	}
	if _, err := database.SetCouponVisibility(ctx, coupon.ID, false, now); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CheckCoupon(ctx, CouponCheckInput{UserID: userID, PlanID: plan.ID, Period: "monthly", Code: coupon.Code}, now); !errors.Is(err, ErrCouponInvalid) {
		t.Fatalf("hidden CheckCoupon() error = %v, want ErrCouponInvalid", err)
	}
}

func TestCouponGlobalSettingDisablesValidationAndSurvivesPartialSiteUpdates(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 10_000}, nil)
	coupon, err := database.CreateCoupon(ctx, SaveCouponInput{
		Code: "SWITCHED", Name: "全局开关", Type: CouponTypeFixed, Value: 100, Show: true,
		StartedAt: now.Add(-time.Hour), EndedAt: now.Add(time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := database.GetSiteSettings(ctx)
	if err != nil || !settings.CouponEnabled {
		t.Fatalf("initial coupon setting = (%#v, %v)", settings, err)
	}
	disabled := false
	input := siteSettingsSaveInput(settings)
	input.CouponEnabled = &disabled
	settings, err = database.UpdateSiteSettings(ctx, userID, settings.Revision, input, now)
	if err != nil || settings.CouponEnabled {
		t.Fatalf("disable coupon setting = (%#v, %v)", settings, err)
	}
	if _, err := database.CheckCoupon(ctx, CouponCheckInput{UserID: userID, PlanID: plan.ID, Period: "monthly", Code: coupon.Code}, now); !errors.Is(err, ErrCouponInvalid) {
		t.Fatalf("disabled CheckCoupon() error = %v, want ErrCouponInvalid", err)
	}
	input = siteSettingsSaveInput(settings)
	settings, err = database.UpdateSiteSettings(ctx, userID, settings.Revision, input, now.Add(time.Second))
	if err != nil || settings.CouponEnabled {
		t.Fatalf("partial settings update changed coupon switch = (%#v, %v)", settings, err)
	}
}

func TestCreateOrderConsumesFixedCouponAndCancellationDoesNotRestoreLimit(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100_000}, nil)
	two, one := 2, 1
	coupon, err := database.CreateCoupon(ctx, SaveCouponInput{
		Code: "FIXED123", Name: "固定 12.34", Type: CouponTypeFixed, Value: 1_234, Show: true,
		LimitUse: &two, LimitUseWithUser: &one, StartedAt: now.Add(-time.Hour), EndedAt: now.Add(time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "month_price", CouponCode: coupon.Code}, now)
	if err != nil {
		t.Fatal(err)
	}
	if order.CouponID == nil || *order.CouponID != coupon.ID || order.DiscountAmount != 1_234 || order.TotalAmount != 98_766 {
		t.Fatalf("coupon order = %#v", order)
	}
	if _, err := database.CancelOrder(ctx, userID, order.TradeNo, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	remaining, err := database.GetCoupon(ctx, coupon.ID)
	if err != nil || remaining.LimitUse == nil || *remaining.LimitUse != 1 {
		t.Fatalf("coupon after cancel = (%#v, %v), want remaining 1", remaining, err)
	}
	second, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly", CouponCode: coupon.Code}, now.Add(2*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CompleteOrder(ctx, second.TradeNo, second.TradeNo, now.Add(3*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CheckCoupon(ctx, CouponCheckInput{UserID: userID, PlanID: plan.ID, Period: "monthly", Code: coupon.Code}, now.Add(4*time.Minute)); !errors.Is(err, ErrCouponExhausted) {
		t.Fatalf("exhausted CheckCoupon() error = %v", err)
	}
}

func TestPercentageCouponUsesIntegerHalfUpAndCapsCombinedDiscount(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 99_999}, nil)
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET discount = 10 WHERE id = ?`, userID); err != nil {
		t.Fatal(err)
	}
	coupon, err := database.CreateCoupon(ctx, SaveCouponInput{
		Code: "PERCENT15", Name: "比例 15%", Type: CouponTypePercentage, Value: 15, Show: true,
		StartedAt: now.Add(-time.Hour), EndedAt: now.Add(time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly", CouponCode: coupon.Code}, now)
	if err != nil {
		t.Fatal(err)
	}
	// 15% and 10% are each rounded half-up from 99,999 cents, then added.
	if order.DiscountAmount != 25_000 || order.TotalAmount != 74_999 {
		t.Fatalf("percentage order discount=%d total=%d, want 25000/74999", order.DiscountAmount, order.TotalAmount)
	}
	var discountType, totalType string
	if err := database.db.QueryRowContext(ctx, `SELECT typeof(discount_amount), typeof(total_amount) FROM orders WHERE id = ?`, order.ID).Scan(&discountType, &totalType); err != nil {
		t.Fatal(err)
	}
	if discountType != "integer" || totalType != "integer" {
		t.Fatalf("money storage types = %s/%s, want integer/integer", discountType, totalType)
	}
}

func TestCouponGlobalLimitIsAtomicAcrossIndependentConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "coupons.db")
	first, err := OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.Close() })
	if err := first.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, firstUserID := createOrderFixture(t, first, now, PlanPrices{"monthly": 100_000}, nil)
	groupID := *plan.GroupID
	secondUser, err := first.CreateRuntimeUser(ctx, CreateRuntimeUserInput{
		Email: "coupon-concurrent@example.test", PasswordHash: "hash", GroupID: groupID,
		UUID: "f0850739-5f8d-4764-bd21-b50dc9afe075", TransferEnable: 1,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	one := 1
	coupon, err := first.CreateCoupon(ctx, SaveCouponInput{
		Code: "ONLYONCE", Name: "only once", Type: CouponTypeFixed, Value: 100, Show: true, LimitUse: &one,
		StartedAt: now.Add(-time.Hour), EndedAt: now.Add(time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for index, database := range []*Store{first, second} {
		userID := []int64{firstUserID, secondUser.ID}[index]
		go func() {
			<-start
			_, createErr := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly", CouponCode: coupon.Code}, now)
			results <- createErr
		}()
	}
	close(start)
	var succeeded, exhausted int
	for range 2 {
		switch err := <-results; {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrCouponExhausted):
			exhausted++
		default:
			t.Fatalf("concurrent CreateOrder() error = %v", err)
		}
	}
	var remaining, used int
	if err := first.db.QueryRowContext(ctx, `SELECT limit_use FROM coupons WHERE id = ?`, coupon.ID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE coupon_id = ?`, coupon.ID).Scan(&used); err != nil {
		t.Fatal(err)
	}
	if succeeded != 1 || exhausted != 1 || remaining != 0 || used != 1 {
		t.Fatalf("coupon concurrency succeeded=%d exhausted=%d remaining=%d used=%d", succeeded, exhausted, remaining, used)
	}
}

func TestCouponCRUDRejectsDuplicateInvalidAndReferencedDeletion(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 1_000}, nil)
	coupon, err := database.CreateCoupon(ctx, SaveCouponInput{
		Code: "UNIQUE", Name: "safe", Type: CouponTypeFixed, Value: 100, Show: true,
		StartedAt: now.Add(-time.Hour), EndedAt: now.Add(time.Hour),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateCoupon(ctx, SaveCouponInput{Code: "UNIQUE", Name: "duplicate", Type: CouponTypeFixed, Value: 1, StartedAt: now, EndedAt: now.Add(time.Hour)}, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate CreateCoupon() error = %v, want ErrConflict", err)
	}
	if _, err := database.CreateCoupon(ctx, SaveCouponInput{Code: "BAD", Name: "negative", Type: CouponTypeFixed, Value: -1, StartedAt: now, EndedAt: now.Add(time.Hour)}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid CreateCoupon() error = %v, want ErrInvalidInput", err)
	}
	page, err := database.ListCoupons(ctx, CouponFilter{Page: 1, PageSize: 20, Query: "uniq"})
	if err != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != coupon.ID {
		t.Fatalf("ListCoupons() = (%#v, %v)", page, err)
	}
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly", CouponCode: coupon.Code}, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteCoupon(ctx, coupon.ID); !errors.Is(err, ErrCouponReferenced) {
		t.Fatalf("DeleteCoupon(referenced) error = %v, want ErrCouponReferenced", err)
	}
	if _, err := database.CancelOrder(ctx, userID, order.TradeNo, now); err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteCoupon(ctx, coupon.ID); !errors.Is(err, ErrCouponReferenced) {
		t.Fatalf("DeleteCoupon(cancelled reference) error = %v, want ErrCouponReferenced", err)
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM coupons WHERE id = ?`, coupon.ID); err == nil {
		t.Fatal("database trigger allowed deleting a coupon referenced by an order")
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE orders SET coupon_id = 999999 WHERE id = ?`, order.ID); err == nil {
		t.Fatal("database trigger allowed a dangling order coupon reference")
	}
}

func TestSchemaV30ThroughV31PreservesV29OrdersAndAddsCouponConstraints(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(100, 0)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100}, nil)
	order, err := database.CreateOrder(ctx, CreateOrderInput{UserID: userID, PlanID: plan.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `
		DROP TRIGGER trg_orders_payment_insert;
		DROP TRIGGER trg_orders_payment_update;
		DROP TRIGGER trg_payments_delete_restrict;
		DROP TABLE payment_webhook_receipts;
		DROP TABLE payment_checkout_attempts;
		DROP TABLE payments;
		DROP INDEX idx_orders_payment_status;
		DROP TRIGGER trg_orders_coupon_insert;
		DROP TRIGGER trg_orders_coupon_update;
		DROP TRIGGER trg_coupons_delete_restrict;
		DROP TABLE coupons;
		ALTER TABLE app_settings DROP COLUMN coupon_enabled;
		DROP INDEX idx_orders_coupon_user_status;
		PRAGMA user_version = 29;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var version, couponEnabled int
	var couponTriggerCount int
	var tradeNo string
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT coupon_enabled FROM app_settings WHERE id = 1`).Scan(&couponEnabled); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT trade_no FROM orders WHERE id = ?`, order.ID).Scan(&tradeNo); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sqlite_schema
		WHERE type = 'trigger' AND name IN ('trg_orders_coupon_insert', 'trg_orders_coupon_update', 'trg_coupons_delete_restrict')
	`).Scan(&couponTriggerCount); err != nil {
		t.Fatal(err)
	}
	if version != currentSchemaVersion || couponEnabled != 1 || tradeNo != order.TradeNo || couponTriggerCount != 3 {
		t.Fatalf("migration version=%d enabled=%d trade=%q/%q triggers=%d", version, couponEnabled, tradeNo, order.TradeNo, couponTriggerCount)
	}
	if _, err := database.db.ExecContext(ctx, `INSERT INTO coupons (code,name,type,value,show,limit_plan_ids_json,limit_periods_json,started_at,ended_at,created_at,updated_at) VALUES ('P','p',2,101,1,'[]','[]',0,1,0,0)`); err == nil {
		t.Fatal("percentage above 100 bypassed schema constraint")
	}
}

func TestCouponHotPathsUseDedicatedIndexes(t *testing.T) {
	database := newTestStore(t)
	for _, test := range []struct {
		query string
		args  []any
		index string
	}{
		{query: `SELECT id FROM coupons WHERE code = ?`, args: []any{"CODE"}, index: "sqlite_autoindex_coupons_1"},
		{query: `SELECT COUNT(*) FROM orders WHERE coupon_id = ? AND user_id = ? AND status NOT IN (0,2)`, args: []any{1, 1}, index: "idx_orders_coupon_user_status"},
	} {
		rows, err := database.db.Query(`EXPLAIN QUERY PLAN `+test.query, test.args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			plan.WriteString(detail)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(plan.String(), test.index) {
			t.Fatalf("query plan does not use %s: %s", test.index, plan.String())
		}
	}
}
