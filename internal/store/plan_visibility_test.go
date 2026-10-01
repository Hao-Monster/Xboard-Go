package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestPlanAudienceVisibilityEnforcedAtListingAndPurchase(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1800000000, 0)
	// Use existing domain fixtures so the test exercises real purchase transactions.
	for _, q := range []string{
		`INSERT INTO users(id,email,password_hash,uuid,subscription_token,created_at,updated_at,is_distributor,distributor_name) VALUES(901,'customer-a@example.test','unused','visibility-a',lower(hex(randomblob(16))),1,1,0,NULL),(902,'customer-b@example.test','unused','visibility-b',lower(hex(randomblob(16))),1,1,0,NULL),(903,'reseller@example.test','unused','visibility-c',lower(hex(randomblob(16))),1,1,1,'fixture reseller')`,
	} {
		if _, err := s.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	p, err := s.CreatePlan(ctx, SavePlanInput{Name: "Audience", TransferEnableGiB: 10, Prices: PlanPrices{"monthly": 100}, Tags: []string{}}, now)
	if err != nil {
		t.Fatal(err)
	}
	if p.CustomerVisibility != "all" || p.DistributorVisibility != "none" {
		t.Fatal("new plan defaults differ from PHP")
	}
	p, err = s.SetPlanState(ctx, p.ID, p.Revision, PlanState{Show: true, Sell: true, Renew: true}, now)
	if err != nil {
		t.Fatal(err)
	}
	input := SavePlanVisibilityInput{PlanID: p.ID, CustomerVisibility: "selected", DistributorVisibility: "none", CustomerUserIDs: []int64{901}, DistributorUserIDs: []int64{}}
	if err = s.SavePlanVisibility(ctx, input, now); err != nil {
		t.Fatal(err)
	}
	guest, err := s.ListGuestPlanOffers(ctx, now)
	if err != nil || len(guest) != 0 {
		t.Fatalf("guest: %v %v", guest, err)
	}
	for _, id := range []int64{901, 902, 903} {
		offers, e := s.ListUserPlanOffers(ctx, id, now)
		want := 0
		if id == 901 {
			want = 1
		}
		if e != nil || len(offers) != want {
			t.Fatalf("user %d offers=%d err=%v", id, len(offers), e)
		}
	}
	if _, err = s.CreateOrder(ctx, CreateOrderInput{UserID: 902, PlanID: p.ID, Period: "monthly"}, now); !errors.Is(err, ErrPlanUnavailable) {
		t.Fatalf("unlisted buyer: %v", err)
	}
	if _, err = s.CreateDistributorOrder(ctx, CreateDistributorOrderInput{DistributorUserID: 903, PlanID: p.ID, Period: "monthly"}, now); !errors.Is(err, ErrPlanUnavailable) {
		t.Fatalf("unlisted distributor: %v", err)
	}
	if _, err = s.CreateOrder(ctx, CreateOrderInput{UserID: 901, PlanID: p.ID, Period: "monthly"}, now); err != nil {
		t.Fatal(err)
	}
	input.CustomerUserIDs = []int64{903}
	if err = s.SavePlanVisibility(ctx, input, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("wrong audience: %v", err)
	}
	offers, err := s.ListUserPlanOffers(ctx, 901, now)
	if err != nil || len(offers) != 1 {
		t.Fatal("invalid update changed existing permissions", err)
	}
	// Existing customers may renew after removal from the new-purchase audience.
	if _, err = s.db.Exec(`UPDATE users SET plan_id=?,expired_at=? WHERE id=902`, p.ID, now.Add(24*time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateOrder(ctx, CreateOrderInput{UserID: 902, PlanID: p.ID, Period: "monthly"}, now); err != nil {
		t.Fatalf("existing customer renewal: %v", err)
	}
	input.CustomerUserIDs = []int64{901}
	input.DistributorVisibility = "selected"
	input.DistributorUserIDs = []int64{903}
	if err = s.SavePlanVisibility(ctx, input, now); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateDistributorOrder(ctx, CreateDistributorOrderInput{DistributorUserID: 903, PlanID: p.ID, Period: "monthly"}, now)
	if err != nil {
		t.Fatal(err)
	}
	input.DistributorVisibility = "none"
	input.DistributorUserIDs = []int64{}
	if err = s.SavePlanVisibility(ctx, input, now); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RenewDistributorOrder(ctx, RenewDistributorOrderInput{DistributorUserID: 903, TradeNo: created.Order.TradeNo, Period: "monthly", IdempotencyKey: "e394f1e7-3aa5-4ad3-9de8-c171418962a7"}, now.Add(time.Minute)); err != nil {
		t.Fatalf("existing distributor renewal: %v", err)
	}
}

func TestSchemaV66PreservesExistingPlanAccessAndIsRepeatable(t *testing.T) {
	s := newTestStore(t)
	now := time.Unix(1800000000, 0)
	p, err := s.CreatePlan(t.Context(), SavePlanInput{Name: "Existing plan", TransferEnableGiB: 23}, now)
	if err != nil {
		t.Fatal(err)
	}
	// Reconstruct the previous schema in this disposable test database.
	if _, err = s.db.Exec(`DROP TABLE plan_visibility_users; DROP TABLE legacy_user_history;
	 ALTER TABLE plans DROP COLUMN customer_visibility; ALTER TABLE plans DROP COLUMN distributor_visibility;
	 ALTER TABLE traffic_reset_logs DROP COLUMN reset_method; DROP INDEX idx_traffic_reset_logs_global_time;
	 PRAGMA user_version=65`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	p, err = s.GetPlan(t.Context(), p.ID, now)
	if err != nil || p.Name != "Existing plan" || p.TransferEnableGiB != 23 || p.CustomerVisibility != "all" || p.DistributorVisibility != "all" {
		t.Fatalf("upgrade changed existing plan: %+v %v", p, err)
	}
	if _, err = s.db.Exec(`UPDATE plans SET customer_visibility='none' WHERE id=?`, p.ID); err == nil {
		t.Fatal("invalid customer audience accepted")
	}
}
