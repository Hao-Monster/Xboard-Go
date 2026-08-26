package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyGiftCardsPreservesAuditDataAndIsIdempotent(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-migration-admin@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	user, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-migration-user@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := database.CreatePlan(ctx, SavePlanInput{Name: "Gift migration plan", TransferEnableGiB: 100}, now)
	if err != nil {
		t.Fatal(err)
	}
	input := validLegacyGiftCardsImport(admin.ID, user.ID, plan.ID)
	report, err := database.ImportLegacyGiftCards(ctx, input, now)
	if err != nil {
		t.Fatalf("ImportLegacyGiftCards() error = %v", err)
	}
	if report.AlreadyApplied || report.Templates.SourceChecksum != report.Templates.TargetChecksum ||
		report.Codes.SourceChecksum != report.Codes.TargetChecksum || report.Usages.SourceChecksum != report.Usages.TargetChecksum {
		t.Fatalf("report = %#v", report)
	}
	var status int
	var batch, metadata string
	if err := database.db.QueryRow(`SELECT status, batch_no, metadata_json FROM gift_card_codes WHERE id = 20`).Scan(&status, &batch, &metadata); err != nil {
		t.Fatal(err)
	}
	if status != int(GiftCardCodeActive) || batch != "legacy_batch_0001" || metadata != `{"campaign":"summer"}` {
		t.Fatalf("imported code status=%d batch=%q metadata=%q", status, batch, metadata)
	}
	var level, multiplier int64
	if err := database.db.QueryRow(`SELECT user_level_at_use, multiplier_basis_points FROM gift_card_usages WHERE id = 30`).Scan(&level, &multiplier); err != nil {
		t.Fatal(err)
	}
	if level != 9 || multiplier != 12500 {
		t.Fatalf("usage level=%d multiplier=%d", level, multiplier)
	}
	repeated, err := database.ImportLegacyGiftCards(ctx, input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != report.AppliedAt {
		t.Fatalf("repeated import = (%#v, %v)", repeated, err)
	}
}

func TestImportLegacyGiftCardsRejectsBrokenReferencesAndNonEmptyTargetAtomically(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0).UTC()
	admin, err := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "gift-migration-owner@example.test", PasswordHash: "hash"}, now)
	if err != nil {
		t.Fatal(err)
	}
	input := validLegacyGiftCardsImport(admin.ID, admin.ID, 999)
	if _, err := database.ImportLegacyGiftCards(ctx, input, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("missing plan ImportLegacyGiftCards() error = %v, want ErrConflict", err)
	}
	var templates, codes, usages, runs int
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM gift_card_templates`).Scan(&templates)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM gift_card_codes`).Scan(&codes)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM gift_card_usages`).Scan(&usages)
	_ = database.db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyGiftCardsSlice).Scan(&runs)
	if templates != 0 || codes != 0 || usages != 0 || runs != 0 {
		t.Fatalf("partial writes templates=%d codes=%d usages=%d runs=%d", templates, codes, usages, runs)
	}
}

func validLegacyGiftCardsImport(adminID, userID, planID int64) LegacyGiftCardsImport {
	plan := planID
	level := int64(9)
	templates := []LegacyGiftCardTemplate{{
		ID: 10, Name: "Legacy gift", Description: "preserved", Type: GiftCardTypeGeneral, Status: true,
		Rewards: GiftCardReward{Balance: 500, PlanID: nil}, Limits: GiftCardLimits{MaxUsePerUser: 2, InviteRewardBasisPoints: 1000},
		SpecialConfig: GiftCardSpecialConfig{FestivalMultiplierBasisPoints: 12500}, Theme: "#1890ff", SortPosition: 3,
		AdminID: adminID, CreatedAt: 1_700_000_000, UpdatedAt: 1_700_000_100,
	}}
	codes := []LegacyGiftCardCode{{
		ID: 20, TemplateID: 10, Code: "LEGACYGC00000001", BatchNo: "legacy_batch_0001", Status: GiftCardCodeActive,
		UserID: &userID, UsedAt: int64Pointer(1_700_000_200), ActualRewards: &GiftCardReward{Balance: 500}, UsageCount: 1,
		MaxUsage: 2, MetadataJSON: `{"campaign":"summer"}`, CreatedAt: 1_700_000_100, UpdatedAt: 1_700_000_200,
	}}
	usages := []LegacyGiftCardUsage{{
		ID: 30, CodeID: 20, TemplateID: 10, UserID: userID, Rewards: GiftCardReward{Balance: 500},
		UserLevelAtUse: &level, UserPlanID: &plan, MultiplierBasisPoints: 12500, IPAddress: "192.0.2.8",
		UserAgent: "legacy-test", Notes: "audit", UsedAt: 1_700_000_200,
	}}
	return LegacyGiftCardsImport{
		Slice: LegacyGiftCardsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 8192,
		Templates: templates, Codes: codes, Usages: usages,
		TemplatesChecksum: LegacyGiftCardTemplatesChecksum(templates), CodesChecksum: LegacyGiftCardCodesChecksum(codes),
		UsagesChecksum: LegacyGiftCardUsagesChecksum(usages), RollbackBackupPath: "/tmp/gift-card-rollback.xbbackup",
		RollbackBackupSHA256: strings.Repeat("b", 64),
	}
}
