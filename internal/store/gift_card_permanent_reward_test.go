package store

import (
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestBL03ActualDayRewards(t *testing.T) {
	for _, tc := range []struct {
		name                     string
		kind                     GiftCardType
		permanent, mixed, noPlan bool
		expiryOffset             time.Duration
	}{
		{"permanent_days", GiftCardTypeGeneral, true, false, false, 0},
		{"permanent_mixed", GiftCardTypeGeneral, true, true, false, 0},
		{"permanent_mystery", GiftCardTypeMystery, true, false, false, 0},
		{"active", GiftCardTypeGeneral, false, false, false, 24 * time.Hour},
		{"expired", GiftCardTypeGeneral, false, false, false, -24 * time.Hour},
		{"no_plan", GiftCardTypeGeneral, false, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestStore(t)
			ctx := t.Context()
			now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
			plan, userID := createOrderFixture(t, db, now, PlanPrices{"monthly": 100}, nil)
			var expiry any
			if !tc.permanent {
				expiry = now.Add(tc.expiryOffset).Unix()
			}
			var planID any = plan.ID
			if tc.noPlan {
				planID = nil
			}
			nextReset := now.Add(15 * 24 * time.Hour).Unix()
			if _, err := db.db.ExecContext(ctx, `UPDATE users SET plan_id=?,expired_at=?,next_reset_at=?,balance=0,transfer_enable=100,device_limit=1,traffic_u=10,traffic_d=20 WHERE id=?`, planID, expiry, nextReset, userID); err != nil {
				t.Fatal(err)
			}
			reward := GiftCardReward{ExpireDays: 30}
			if tc.mixed {
				reward.Balance = 123
				reward.TransferEnable = 456
				reward.DeviceLimit = 2
				reward.ResetTraffic = true
			}
			if tc.kind == GiftCardTypeMystery {
				reward = GiftCardReward{RandomRewards: []GiftCardRandomReward{{Weight: 1, Reward: reward}}}
			}
			template, err := db.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{Name: "QA day rewards", Type: tc.kind, Status: true, Rewards: reward, Limits: GiftCardLimits{MaxUsePerUser: 1}}, userID, now)
			if err != nil {
				t.Fatal(err)
			}
			codes, err := db.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
			if err != nil {
				t.Fatal(err)
			}
			usage, err := db.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: userID, Code: codes[0].Code}, now)
			if tc.noPlan {
				if !errors.Is(err, ErrGiftCardCondition) {
					t.Fatalf("no-plan error=%v", err)
				}
				var uses int
				if err := db.db.QueryRowContext(ctx, `SELECT usage_count FROM gift_card_codes WHERE id=?`, codes[0].ID).Scan(&uses); err != nil || uses != 0 {
					t.Fatalf("no-plan uses=%d err=%v", uses, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var actualExpiry sql.NullInt64
			var next, balance, transfer, upload, download int64
			var devices, uses int
			if err := db.db.QueryRowContext(ctx, `SELECT expired_at,next_reset_at,balance,transfer_enable,traffic_u,traffic_d,device_limit FROM users WHERE id=?`, userID).Scan(&actualExpiry, &next, &balance, &transfer, &upload, &download, &devices); err != nil {
				t.Fatal(err)
			}
			wantDays := 30
			if tc.permanent {
				wantDays = 0
				if actualExpiry.Valid || next != nextReset {
					t.Errorf("permanent expiry=%v next=%d want NULL/%d", actualExpiry, next, nextReset)
				}
			} else {
				base := now
				if tc.expiryOffset > 0 {
					base = now.Add(tc.expiryOffset)
				}
				if !actualExpiry.Valid || actualExpiry.Int64 != base.AddDate(0, 0, 30).Unix() {
					t.Errorf("expiry=%v", actualExpiry)
				}
			}
			persisted, err := db.GetGiftCardUsage(ctx, usage.ID, userID)
			if err != nil {
				t.Fatal(err)
			}
			var recordedDays int
			if err := db.db.QueryRowContext(ctx, `SELECT usage_count,COALESCE(json_extract(actual_rewards_json,'$.expire_days'),0) FROM gift_card_codes WHERE id=?`, codes[0].ID).Scan(&uses, &recordedDays); err != nil {
				t.Fatal(err)
			}
			if usage.Rewards.ExpireDays != wantDays || persisted.Rewards.ExpireDays != wantDays || recordedDays != wantDays || uses != 1 {
				t.Errorf("reward days response/usage/code=%d/%d/%d want %d, uses=%d", usage.Rewards.ExpireDays, persisted.Rewards.ExpireDays, recordedDays, wantDays, uses)
			}
			if tc.mixed {
				if balance != 123 || transfer != 556 || devices != 3 || upload != 0 || download != 0 {
					t.Errorf("mixed reward balance/transfer/devices/traffic=%d/%d/%d/%d/%d", balance, transfer, devices, upload, download)
				}
			} else if balance != 0 || transfer != 100 || devices != 1 || upload != 10 || download != 20 {
				t.Errorf("unexpected non-day reward mutation")
			}
		})
	}
}
