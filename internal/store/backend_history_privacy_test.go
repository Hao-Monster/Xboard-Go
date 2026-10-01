package store

import "testing"

func TestAnonymizationClearsImportedHistoryAndPlanMembership(t *testing.T) {
	s, admin, user, now := lifecycleFixture(t)
	p, err := s.CreatePlan(t.Context(), SavePlanInput{Name: "Privacy fixture", TransferEnableGiB: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO legacy_user_history VALUES(?,1700000000,'192.0.2.1',2)`, user.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.SavePlanVisibility(t.Context(), SavePlanVisibilityInput{PlanID: p.ID, CustomerVisibility: "selected", DistributorVisibility: "none", CustomerUserIDs: []int64{user.ID}, DistributorUserIDs: []int64{}}, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ChangeUserLifecycle(t.Context(), UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: user.Revision, Action: "deactivate"}, now); err != nil {
		t.Fatal(err)
	}
	user, err = s.GetAdminUser(t.Context(), user.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ChangeUserLifecycle(t.Context(), UserLifecycleInput{AdministratorID: admin.ID, UserID: user.ID, Revision: user.Revision, Action: "anonymize"}, now.AddDate(0, 0, 31)); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT count(*) FROM legacy_user_history WHERE user_id=?`, `SELECT count(*) FROM plan_visibility_users WHERE user_id=?`} {
		var count int
		if err = s.db.QueryRow(query, user.ID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("privacy state remained: count=%d err=%v", count, err)
		}
	}
}
