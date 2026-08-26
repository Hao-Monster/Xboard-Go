package store

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestGiftCardGeneralRedeemIsAtomicAndAppliesInviterReward(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, userID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100}, nil)
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "gift-inviter@example.test", PasswordHash: "hash",
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	expires := now.Add(24 * time.Hour)
	if _, err := database.db.ExecContext(ctx, `
		UPDATE users SET invite_user_id = ?, plan_id = ?, expired_at = ?, transfer_enable = 0, traffic_u = 123, traffic_d = 456
		WHERE id = ?
	`, inviter.ID, plan.ID, expires.Unix(), userID); err != nil {
		t.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "General reward", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 1_000, TransferEnable: 2 * bytesPerGiB, ExpireDays: 30, DeviceLimit: 4, ResetTraffic: true},
		Limits:  GiftCardLimits{MaxUsePerUser: 1, InviteRewardBasisPoints: 2_500},
	}, inviter.ID, now)
	if err != nil {
		t.Fatalf("CreateGiftCardTemplate() error = %v", err)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, Prefix: "VIP", MaxUsage: 1}, now)
	if err != nil || len(codes) != 1 {
		t.Fatalf("GenerateGiftCardCodes() = (%#v, %v)", codes, err)
	}
	preview, err := database.CheckGiftCard(ctx, userID, codes[0].Code, now)
	if err != nil || preview.Template.ID != template.ID || preview.Rewards.Balance != 1_000 {
		t.Fatalf("CheckGiftCard() = (%#v, %v)", preview, err)
	}
	usage, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: userID, Code: codes[0].Code, IPAddress: "192.0.2.10", UserAgent: "gift-test"}, now)
	if err != nil {
		t.Fatalf("RedeemGiftCard() error = %v", err)
	}
	if usage.Rewards.Balance != 1_000 || usage.InviterRewards.Balance != 250 {
		t.Fatalf("usage rewards = %#v, inviter = %#v", usage.Rewards, usage.InviterRewards)
	}
	if usage.TrafficResetUploadBefore == nil || *usage.TrafficResetUploadBefore != 123 ||
		usage.TrafficResetDownloadBefore == nil || *usage.TrafficResetDownloadBefore != 456 {
		t.Fatalf("traffic reset audit upload=%v download=%v", usage.TrafficResetUploadBefore, usage.TrafficResetDownloadBefore)
	}
	if usage.UserLevelAtUse == nil || *usage.UserLevelAtUse != int64(plan.SortPosition) || usage.UserPlanID == nil || *usage.UserPlanID != plan.ID {
		t.Fatalf("usage audit level=%v plan=%v, want level=%d plan=%d", usage.UserLevelAtUse, usage.UserPlanID, plan.SortPosition, plan.ID)
	}
	persistedUsage, err := database.GetGiftCardUsage(ctx, usage.ID, userID)
	if err != nil || persistedUsage.TrafficResetUploadBefore == nil || *persistedUsage.TrafficResetUploadBefore != 123 ||
		persistedUsage.TrafficResetDownloadBefore == nil || *persistedUsage.TrafficResetDownloadBefore != 456 {
		t.Fatalf("persisted traffic reset audit = (%#v, %v)", persistedUsage, err)
	}
	var balance, transfer, upload, download, expiry, inviterBalance, lastReset int64
	var device int
	var resetCount int64
	if err := database.db.QueryRowContext(ctx, `
		SELECT u.balance, u.transfer_enable, u.traffic_u, u.traffic_d, u.expired_at, u.device_limit,
			u.last_reset_at, u.reset_count, i.balance
		FROM users u JOIN users i ON i.id = ? WHERE u.id = ?
	`, inviter.ID, userID).Scan(&balance, &transfer, &upload, &download, &expiry, &device, &lastReset, &resetCount, &inviterBalance); err != nil {
		t.Fatal(err)
	}
	if balance != 1_000 || transfer != 2*bytesPerGiB || upload != 0 || download != 0 ||
		expiry != expires.AddDate(0, 0, 30).Unix() || device != 4 || lastReset != now.Unix() || resetCount != 1 || inviterBalance != 250 {
		t.Fatalf("reward state balance=%d transfer=%d traffic=%d/%d expiry=%d device=%d reset=%d/%d inviter=%d",
			balance, transfer, upload, download, expiry, device, lastReset, resetCount, inviterBalance)
	}
	if _, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: userID, Code: codes[0].Code}, now); !errors.Is(err, ErrGiftCardExhausted) {
		t.Fatalf("second RedeemGiftCard() error = %v, want ErrGiftCardExhausted", err)
	}
}

func TestGiftCardPlanConditionsAndAssignmentMatchLegacyRules(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, activeUserID := createOrderFixture(t, database, now, PlanPrices{"monthly": 100}, nil)
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET plan_id = ?, expired_at = ? WHERE id = ?`, plan.ID, now.Add(time.Hour).Unix(), activeUserID); err != nil {
		t.Fatal(err)
	}
	newUser, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-plan-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Plan reward", Type: GiftCardTypePlan, Status: true,
		Rewards: GiftCardReward{PlanID: &plan.ID, PlanValidityDays: 7},
		Limits:  GiftCardLimits{MaxUsePerUser: 1},
	}, newUser.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 2, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: activeUserID, Code: codes[0].Code}, now); !errors.Is(err, ErrGiftCardActivePlan) {
		t.Fatalf("active plan RedeemGiftCard() error = %v, want ErrGiftCardActivePlan", err)
	}
	usage, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: newUser.ID, Code: codes[1].Code}, now)
	if err != nil {
		t.Fatalf("new plan RedeemGiftCard() error = %v", err)
	}
	if usage.UserPlanID == nil || *usage.UserPlanID != plan.ID || usage.UserLevelAtUse == nil || *usage.UserLevelAtUse != int64(plan.SortPosition) {
		t.Fatalf("plan usage audit level=%v plan=%v, want level=%d plan=%d", usage.UserLevelAtUse, usage.UserPlanID, plan.SortPosition, plan.ID)
	}
	var planID, transfer, expiry int64
	if err := database.db.QueryRowContext(ctx, `SELECT plan_id, transfer_enable, expired_at FROM users WHERE id = ?`, newUser.ID).Scan(&planID, &transfer, &expiry); err != nil {
		t.Fatal(err)
	}
	if planID != plan.ID || transfer != plan.TransferEnableGiB*bytesPerGiB || expiry != now.AddDate(0, 0, 7).Unix() {
		t.Fatalf("assigned plan=%d transfer=%d expiry=%d", planID, transfer, expiry)
	}
}

func TestGiftCardConcurrentRedeemHasExactlyOneWinnerAcrossConnections(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gift-cards.db")
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
	admin, err := first.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-owner@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	users := make([]int64, 2)
	for index := range users {
		user, createErr := first.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-concurrent-" + string(rune('a'+index)) + "@example.test", PasswordHash: "hash"}, now)
		if createErr != nil {
			t.Fatal(createErr)
		}
		users[index] = user.ID
	}
	template, err := first.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Single use", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 500}, Limits: GiftCardLimits{MaxUsePerUser: 1},
	}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := first.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index, database := range []*Store{first, second} {
		wait.Add(1)
		go func(database *Store, userID int64) {
			defer wait.Done()
			<-start
			_, redeemErr := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: userID, Code: codes[0].Code}, now)
			results <- redeemErr
		}(database, users[index])
	}
	close(start)
	wait.Wait()
	close(results)
	var success, exhausted int
	for redeemErr := range results {
		switch {
		case redeemErr == nil:
			success++
		case errors.Is(redeemErr, ErrGiftCardExhausted):
			exhausted++
		default:
			t.Fatalf("concurrent RedeemGiftCard() error = %v", redeemErr)
		}
	}
	var usageCount, credited int64
	if err := first.db.QueryRowContext(ctx, `SELECT usage_count FROM gift_card_codes WHERE id = ?`, codes[0].ID).Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(balance), 0) FROM users WHERE id IN (?, ?)`, users[0], users[1]).Scan(&credited); err != nil {
		t.Fatal(err)
	}
	if success != 1 || exhausted != 1 || usageCount != 1 || credited != 500 {
		t.Fatalf("concurrent result success=%d exhausted=%d usages=%d credited=%d", success, exhausted, usageCount, credited)
	}
}

func TestGiftCardConcurrentDifferentCodesStillEnforcePerUserLimit(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "gift-user-limit.db")
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
	admin, err := first.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-limit-owner@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := first.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-limit-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	template, err := first.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Per-user limit", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 400}, Limits: GiftCardLimits{MaxUsePerUser: 1},
	}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := first.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 2, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index, database := range []*Store{first, second} {
		wait.Add(1)
		go func(database *Store, code string) {
			defer wait.Done()
			<-start
			_, redeemErr := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user.ID, Code: code}, now)
			results <- redeemErr
		}(database, codes[index].Code)
	}
	close(start)
	wait.Wait()
	close(results)
	var success, limited int
	for redeemErr := range results {
		switch {
		case redeemErr == nil:
			success++
		case errors.Is(redeemErr, ErrGiftCardUserLimit):
			limited++
		default:
			t.Fatalf("concurrent RedeemGiftCard() error = %v", redeemErr)
		}
	}
	var usageCount, balance int64
	if err := first.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM gift_card_usages WHERE user_id = ?`, user.ID).Scan(&usageCount); err != nil {
		t.Fatal(err)
	}
	if err := first.db.QueryRowContext(ctx, `SELECT balance FROM users WHERE id = ?`, user.ID).Scan(&balance); err != nil {
		t.Fatal(err)
	}
	if success != 1 || limited != 1 || usageCount != 1 || balance != 400 {
		t.Fatalf("concurrent result success=%d limited=%d usages=%d balance=%d", success, limited, usageCount, balance)
	}
}

func TestGiftCardInputBoundsAndDisallowedPlansAreEnforced(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-validation@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Invalid", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: -1},
	}, admin.ID, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("negative reward error = %v, want ErrInvalidInput", err)
	}
	for name, input := range map[string]SaveGiftCardTemplateInput{
		"unsafe background URL": {Name: "Invalid", Type: GiftCardTypeGeneral, Status: true, Rewards: GiftCardReward{Balance: 1}, BackgroundImage: "javascript:alert(1)"},
		"invalid theme":         {Name: "Invalid", Type: GiftCardTypeGeneral, Status: true, Rewards: GiftCardReward{Balance: 1}, Theme: "red"},
	} {
		if _, err := database.CreateGiftCardTemplate(ctx, input, admin.ID, now); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("%s error = %v, want ErrInvalidInput", name, err)
		}
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Bounded", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 1}, Limits: GiftCardLimits{MaxUsePerUser: 1},
	}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if template.Theme != "#1890ff" {
		t.Fatalf("default theme = %q, want #1890ff", template.Theme)
	}
	if _, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 10_001, MaxUsage: 1}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized batch error = %v, want ErrInvalidInput", err)
	}
	if _, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1_001}, now); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("oversized usage limit error = %v, want ErrInvalidInput", err)
	}
}

func TestGiftCardLegacyEligibilityRequiresPlanForSubscriptionRewards(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-no-plan@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Subscription reward", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{TransferEnable: bytesPerGiB}, Limits: GiftCardLimits{MaxUsePerUser: 1},
		Conditions: GiftCardConditions{NewUserOnly: true},
	}, user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if template.Conditions.NewUserMaxDays == nil || *template.Conditions.NewUserMaxDays != 7 {
		t.Fatalf("new-user default = %#v", template.Conditions.NewUserMaxDays)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CheckGiftCard(ctx, user.ID, codes[0].Code, now); !errors.Is(err, ErrGiftCardCondition) {
		t.Fatalf("CheckGiftCard(no plan) error = %v, want ErrGiftCardCondition", err)
	}
}

func TestGiftCardLegacyFestivalWindowAndConditionalFlags(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-window-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-window-user@example.test", PasswordHash: "hash"}, now.AddDate(-1, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	maxDays := 1
	starts := now.Add(24 * time.Hour)
	ends := starts.Add(24 * time.Hour)
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Future bonus", Type: GiftCardTypeGeneral, Status: true,
		Conditions: GiftCardConditions{NewUserOnly: false, NewUserMaxDays: &maxDays},
		Rewards:    GiftCardReward{Balance: 500}, Limits: GiftCardLimits{MaxUsePerUser: 1},
		SpecialConfig: GiftCardSpecialConfig{StartedAt: &starts, EndedAt: &ends, FestivalMultiplierBasisPoints: 20_000},
	}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := database.CheckGiftCard(ctx, user.ID, codes[0].Code, now)
	if err != nil || preview.Rewards.Balance != 500 {
		t.Fatalf("outside-window CheckGiftCard() = (%#v, %v), want base reward", preview, err)
	}
	if _, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user.ID, Code: codes[0].Code}, now); err != nil {
		t.Fatalf("outside-window RedeemGiftCard() error = %v", err)
	}
}

func TestGiftCardFestivalMultiplierWithoutWindowUsesBaseReward(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-no-window-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-no-window-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "No window", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 500}, Limits: GiftCardLimits{MaxUsePerUser: 1},
		SpecialConfig: GiftCardSpecialConfig{FestivalMultiplierBasisPoints: 20_000},
	}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if template.SpecialConfig.FestivalMultiplierBasisPoints != 20_000 {
		t.Fatalf("stored multiplier without window = %d, want 20000", template.SpecialConfig.FestivalMultiplierBasisPoints)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user.ID, Code: codes[0].Code}, now)
	if err != nil || usage.Rewards.Balance != 500 || usage.Multiplier != 10_000 {
		t.Fatalf("RedeemGiftCard(no window) = (%#v, %v)", usage, err)
	}
}

func TestGiftCardFestivalMultiplierChangesQuantitiesButNotPlanIdentifier(t *testing.T) {
	planID := int64(17)
	reward := GiftCardReward{
		Balance: 125, TransferEnable: 1_001, ExpireDays: 3, DeviceLimit: 2,
		PlanID: &planID, PlanValidityDays: 5,
	}
	actual, err := multiplyGiftCardReward(reward, 15_000)
	if err != nil {
		t.Fatal(err)
	}
	if actual.Balance != 187 || actual.TransferEnable != 1_501 || actual.ExpireDays != 4 || actual.DeviceLimit != 3 ||
		actual.PlanValidityDays != 7 || actual.PlanID == nil || *actual.PlanID != 17 {
		t.Fatalf("multiplied reward = %#v", actual)
	}
}

func TestGiftCardDisabledTemplateCannotGenerateCodes(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-disabled-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Disabled", Type: GiftCardTypeGeneral, Status: false,
		Rewards: GiftCardReward{Balance: 100}, Limits: GiftCardLimits{MaxUsePerUser: 1},
	}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now); !errors.Is(err, ErrGiftCardUnavailable) {
		t.Fatalf("GenerateGiftCardCodes(disabled) error = %v, want ErrGiftCardUnavailable", err)
	}
}

func TestGiftCardAllowedPlansRejectsUserWithoutPlan(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	plan, _ := createOrderFixture(t, database, now, PlanPrices{"monthly": 100}, nil)
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-allowed-no-plan@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Plan restricted", Type: GiftCardTypeGeneral, Status: true,
		Conditions: GiftCardConditions{AllowedPlanIDs: []int64{plan.ID}}, Rewards: GiftCardReward{Balance: 100},
		Limits: GiftCardLimits{MaxUsePerUser: 1},
	}, user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.CheckGiftCard(ctx, user.ID, codes[0].Code, now); !errors.Is(err, ErrGiftCardCondition) {
		t.Fatalf("CheckGiftCard(no plan, restricted) error = %v, want ErrGiftCardCondition", err)
	}
}

func TestGiftCardAdminLifecycleListsAuditsAndProtectsReferences(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-lifecycle-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-lifecycle-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Lifecycle", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 100}, Limits: GiftCardLimits{MaxUsePerUser: 1}, SortPosition: 8,
	}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := database.GetGiftCardTemplate(ctx, template.ID)
	if err != nil || loaded.Name != template.Name || loaded.Revision != 1 {
		t.Fatalf("GetGiftCardTemplate() = (%#v, %v)", loaded, err)
	}
	updated, err := database.UpdateGiftCardTemplate(ctx, template.ID, template.Revision, SaveGiftCardTemplateInput{
		Name: "Lifecycle updated", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 200}, Limits: GiftCardLimits{MaxUsePerUser: 1}, SortPosition: 2,
	}, admin.ID, now.Add(time.Second))
	if err != nil || updated.Revision != 2 || updated.Rewards.Balance != 200 {
		t.Fatalf("UpdateGiftCardTemplate() = (%#v, %v)", updated, err)
	}
	kind, enabled := GiftCardTypeGeneral, true
	templates, err := database.ListGiftCardTemplates(ctx, GiftCardTemplateFilter{Page: 1, PageSize: 20, Type: &kind, Status: &enabled})
	if err != nil || templates.Total != 1 || len(templates.Items) != 1 || templates.Items[0].ID != template.ID {
		t.Fatalf("ListGiftCardTemplates() = (%#v, %v)", templates, err)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 2, Prefix: "LC", MaxUsage: 2}, now)
	if err != nil || len(codes) != 2 {
		t.Fatalf("GenerateGiftCardCodes() = (%#v, %v)", codes, err)
	}
	expires := now.Add(48 * time.Hour)
	changed, err := database.UpdateGiftCardCode(ctx, codes[0].ID, SaveGiftCardCodeInput{Code: "CUSTOMGC01", Status: GiftCardCodeActive, ExpiresAt: &expires, MaxUsage: 2}, now)
	if err != nil || changed.Code != "CUSTOMGC01" || changed.ExpiresAt == nil {
		t.Fatalf("UpdateGiftCardCode() = (%#v, %v)", changed, err)
	}
	loadedCode, err := database.GetGiftCardCode(ctx, changed.ID)
	if err != nil || loadedCode.Code != changed.Code {
		t.Fatalf("GetGiftCardCode() = (%#v, %v)", loadedCode, err)
	}
	disabled, err := database.ToggleGiftCardCode(ctx, changed.ID, now)
	if err != nil || disabled.Status != GiftCardCodeDisabled {
		t.Fatalf("ToggleGiftCardCode(disable) = (%#v, %v)", disabled, err)
	}
	enabledCode, err := database.ToggleGiftCardCode(ctx, changed.ID, now)
	if err != nil || enabledCode.Status != GiftCardCodeActive {
		t.Fatalf("ToggleGiftCardCode(enable) = (%#v, %v)", enabledCode, err)
	}
	listedCodes, err := database.ListGiftCardCodes(ctx, GiftCardCodeFilter{Page: 1, PageSize: 20, Query: "Lifecycle updated", TemplateID: &template.ID, BatchNo: codes[0].BatchNo})
	if err != nil || listedCodes.Total != 2 || len(listedCodes.Items) != 2 {
		t.Fatalf("ListGiftCardCodes() = (%#v, %v)", listedCodes, err)
	}
	if err := database.DeleteGiftCardCode(ctx, changed.ID); err != nil {
		t.Fatalf("DeleteGiftCardCode(unused) error = %v", err)
	}
	usage, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user.ID, Code: codes[1].Code}, now)
	if err != nil {
		t.Fatalf("RedeemGiftCard() error = %v", err)
	}
	if _, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user.ID, Code: codes[1].Code}, now.Add(time.Minute)); !errors.Is(err, ErrGiftCardUserLimit) {
		t.Fatalf("second RedeemGiftCard() error = %v, want ErrGiftCardUserLimit", err)
	}
	usagePage, err := database.ListGiftCardUsages(ctx, GiftCardUsageFilter{Page: 1, PageSize: 20, UserID: &user.ID, TemplateID: &template.ID, CodeID: &codes[1].ID})
	if err != nil || usagePage.Total != 1 || len(usagePage.Items) != 1 || usagePage.Items[0].ID != usage.ID {
		t.Fatalf("ListGiftCardUsages() = (%#v, %v)", usagePage, err)
	}
	usageDetail, err := database.GetGiftCardUsage(ctx, usage.ID, user.ID)
	if err != nil || usageDetail.Code != codes[1].Code {
		t.Fatalf("GetGiftCardUsage(owner) = (%#v, %v)", usageDetail, err)
	}
	if _, err := database.GetGiftCardUsage(ctx, usage.ID, admin.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGiftCardUsage(other user) error = %v, want ErrNotFound", err)
	}
	statistics, err := database.GiftCardStatistics(ctx, now)
	if err != nil || statistics.TemplateTotal != 1 || statistics.ActiveTemplates != 1 || statistics.CodeTotal != 1 ||
		statistics.UsedCodes != 0 || statistics.UsageTotal != 1 || len(statistics.TemplateStats) != 1 {
		t.Fatalf("GiftCardStatistics() = (%#v, %v)", statistics, err)
	}
	if err := database.DeleteGiftCardCode(ctx, codes[1].ID); !errors.Is(err, ErrGiftCardReferenced) {
		t.Fatalf("DeleteGiftCardCode(used) error = %v, want ErrGiftCardReferenced", err)
	}
	if err := database.DeleteGiftCardTemplate(ctx, template.ID); !errors.Is(err, ErrGiftCardReferenced) {
		t.Fatalf("DeleteGiftCardTemplate(referenced) error = %v, want ErrGiftCardReferenced", err)
	}
	unused, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Unused", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 1}, Limits: GiftCardLimits{MaxUsePerUser: 1},
	}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.DeleteGiftCardTemplate(ctx, unused.ID); err != nil {
		t.Fatalf("DeleteGiftCardTemplate(unused) error = %v", err)
	}
	if _, err := database.GetGiftCardTemplate(ctx, unused.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetGiftCardTemplate(deleted) error = %v, want ErrNotFound", err)
	}
}

func TestGiftCardMysteryRewardIsPersistedAsTheActualReward(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-mystery-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-mystery-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Mystery", Type: GiftCardTypeMystery, Status: true,
		Rewards: GiftCardReward{RandomRewards: []GiftCardRandomReward{{Weight: 1, Reward: GiftCardReward{Balance: 777}}}},
		Limits:  GiftCardLimits{MaxUsePerUser: 1},
	}, admin.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user.ID, Code: codes[0].Code}, now)
	if err != nil || usage.Rewards.Balance != 777 || len(usage.Rewards.RandomRewards) != 0 {
		t.Fatalf("RedeemGiftCard(mystery) = (%#v, %v)", usage, err)
	}
	stored, err := database.GetGiftCardCode(ctx, codes[0].ID)
	if err != nil || stored.ActualRewards == nil || stored.ActualRewards.Balance != 777 {
		t.Fatalf("GetGiftCardCode(mystery) = (%#v, %v)", stored, err)
	}
}

func TestGiftCardMaximumInviterRewardDoesNotOverflow(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	inviter, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-max-inviter@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-max-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, inviter.ID, user.ID); err != nil {
		t.Fatal(err)
	}
	template, err := database.CreateGiftCardTemplate(ctx, SaveGiftCardTemplateInput{
		Name: "Maximum inviter reward", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: maxGiftCardMoney},
		Limits:  GiftCardLimits{MaxUsePerUser: 1, InviteRewardBasisPoints: 10_000},
	}, inviter.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	codes, err := database.GenerateGiftCardCodes(ctx, template.ID, GenerateGiftCardCodesInput{Count: 1, MaxUsage: 1}, now)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := database.RedeemGiftCard(ctx, RedeemGiftCardInput{UserID: user.ID, Code: codes[0].Code}, now)
	if err != nil || usage.Rewards.Balance != maxGiftCardMoney || usage.InviterRewards.Balance != maxGiftCardMoney {
		t.Fatalf("RedeemGiftCard(maximum inviter reward) = (%#v, %v)", usage, err)
	}
	var userBalance, inviterBalance int64
	if err := database.db.QueryRowContext(ctx, `SELECT u.balance, i.balance FROM users u JOIN users i ON i.id = ? WHERE u.id = ?`, inviter.ID, user.ID).Scan(&userBalance, &inviterBalance); err != nil {
		t.Fatal(err)
	}
	if userBalance != maxGiftCardMoney || inviterBalance != maxGiftCardMoney {
		t.Fatalf("balances user=%d inviter=%d", userBalance, inviterBalance)
	}
}
