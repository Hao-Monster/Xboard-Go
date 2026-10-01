package store

import (
	"testing"
	"time"
)

func TestGlobalTrafficResetLogsFilterAndAggregate(t *testing.T) {
	s := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 10, 1, 1, 0, 0, 0, time.UTC)
	method := 1
	p, err := s.CreatePlan(ctx, SavePlanInput{Name: "Reset log fixture", TransferEnableGiB: 1, ResetTrafficMethod: &method}, now)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.CreateAdminUser(ctx, CreateAdminUserInput{Email: "reset-admin@example.test", PasswordHash: "fixture", IsAdmin: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := s.CreateAdminUser(ctx, CreateAdminUserInput{Email: "filter_user@example.test", PasswordHash: "fixture", PlanID: &p.ID, TransferEnable: 1 << 30}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ResetAdminUserTraffic(ctx, AdminUserTrafficResetInput{UserID: user.ID, AdministratorID: admin.ID, Reason: "fixture reason", IdempotencyKey: "reset-filter-001"}, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO traffic_reset_logs(user_id,plan_id,scheduled_for,reset_at,upload_before,download_before,reset_count) VALUES(?,?,?,?,11,22,2)`, user.ID, p.ID, now.Add(-time.Hour).Unix(), now.Add(-time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		email, kind, source string
		want                int64
	}{
		{"", "", "", 2}, {"filter_user", "manual", "manual", 1}, {"filter_user", "monthly", "cron", 1}, {"filter%user", "", "", 0}, {"' OR 1=1--", "", "", 0}, {"", "purchase", "", 0},
	} {
		page, err := s.ListTrafficResetLogs(ctx, TrafficResetFilter{Page: 1, PageSize: 10, Email: tc.email, ResetType: tc.kind, Source: tc.source})
		if err != nil || page.Total != tc.want || int64(len(page.Items)) != tc.want {
			t.Fatalf("filter %+v: total=%d items=%d err=%v", tc, page.Total, len(page.Items), err)
		}
		if tc.source == "manual" && (page.Items[0].AdministratorID == nil || *page.Items[0].AdministratorID != admin.ID || page.Items[0].Reason != "fixture reason") {
			t.Fatal("manual reset metadata lost")
		}
	}
	end := now
	page, err := s.ListTrafficResetLogs(ctx, TrafficResetFilter{Page: 1, PageSize: 10, End: &end})
	if err != nil || page.Total != 1 || page.Items[0].UploadBefore != 11 {
		t.Fatalf("exclusive boundary: %+v %v", page, err)
	}
	stats, err := s.GetTrafficResetStats(ctx, 1, now)
	if err != nil || stats.Total != 2 || stats.Manual != 1 || stats.Cron != 1 || stats.Auto != 0 {
		t.Fatalf("stats: %+v %v", stats, err)
	}
}
