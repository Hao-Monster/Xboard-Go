package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestDecodeLegacyGiftCardSpecialPreservesInactiveMultiplierAndRejectsPartialWindow(t *testing.T) {
	var config store.GiftCardSpecialConfig
	if err := decodeLegacyGiftCardSpecial(sql.NullString{String: `{"festival_bonus":1.5}`, Valid: true}, &config); err != nil {
		t.Fatal(err)
	}
	if config.FestivalMultiplierBasisPoints != 15_000 || config.StartedAt != nil || config.EndedAt != nil {
		t.Fatalf("special config = %#v", config)
	}
	if err := decodeLegacyGiftCardSpecial(sql.NullString{String: `{"start_time":1700000000,"festival_bonus":1.5}`, Valid: true}, &config); err == nil || !strings.Contains(err.Error(), "both be set") {
		t.Fatalf("partial legacy window error = %v", err)
	}
}

func TestReadGiftCardsSnapshotNormalizesLegacyWireSemantics(t *testing.T) {
	path := createLegacyGiftCardsSnapshot(t)
	snapshot, err := ReadGiftCardsSnapshot(context.Background(), path)
	if err != nil {
		t.Fatalf("ReadGiftCardsSnapshot() error = %v", err)
	}
	if len(snapshot.Templates) != 2 || len(snapshot.Codes) != 2 || len(snapshot.Usages) != 1 {
		t.Fatalf("snapshot rows templates=%d codes=%d usages=%d", len(snapshot.Templates), len(snapshot.Codes), len(snapshot.Usages))
	}
	general, mystery := snapshot.Templates[0], snapshot.Templates[1]
	if general.Limits.InviteRewardBasisPoints != 1250 || general.SpecialConfig.FestivalMultiplierBasisPoints != 15000 ||
		general.SpecialConfig.StartedAt == nil || general.SpecialConfig.StartedAt.Unix() != 1_700_000_000 ||
		general.Conditions.NewUserMaxDays == nil || *general.Conditions.NewUserMaxDays != 7 ||
		len(general.Conditions.AllowedPlanIDs) != 1 || general.Conditions.AllowedPlanIDs[0] != 5 {
		t.Fatalf("general template = %#v", general)
	}
	if len(mystery.Rewards.RandomRewards) != 1 || mystery.Rewards.RandomRewards[0].Weight != 3 ||
		mystery.Rewards.RandomRewards[0].Reward.TransferEnable != 1_073_741_824 {
		t.Fatalf("mystery rewards = %#v", mystery.Rewards)
	}
	if snapshot.Codes[0].Status != store.GiftCardCodeActive || snapshot.Codes[1].Status != store.GiftCardCodeExpired ||
		snapshot.Usages[0].MultiplierBasisPoints != 15000 {
		t.Fatalf("codes/usages = (%#v, %#v)", snapshot.Codes, snapshot.Usages)
	}
	if snapshot.TemplatesChecksum != store.LegacyGiftCardTemplatesChecksum(snapshot.Templates) ||
		snapshot.CodesChecksum != store.LegacyGiftCardCodesChecksum(snapshot.Codes) || snapshot.UsagesChecksum != store.LegacyGiftCardUsagesChecksum(snapshot.Usages) {
		t.Fatal("snapshot checksums do not match normalized records")
	}
}

func TestReadGiftCardsSnapshotRejectsMalformedJSONAndBrokenRows(t *testing.T) {
	for _, scenario := range []struct{ name, statement, contains string }{
		{"unknown reward", `UPDATE v2_gift_card_template SET rewards = '{"balance":100,"unknown":1}' WHERE id = 1`, "rewards"},
		{"broken reference", `UPDATE v2_gift_card_code SET template_id = 999 WHERE id = 10`, "missing template"},
		{"bad multiplier", `UPDATE v2_gift_card_usage SET multiplier_applied = 1.23456 WHERE id = 20`, "multiplier"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			path := createLegacyGiftCardsSnapshot(t)
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(scenario.statement); err != nil {
				_ = database.Close()
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadGiftCardsSnapshot(context.Background(), path); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(scenario.contains)) {
				t.Fatalf("ReadGiftCardsSnapshot() error = %v, want %q", err, scenario.contains)
			}
		})
	}
}

func createLegacyGiftCardsSnapshot(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legacy-gift-cards.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = database.Exec(`
		CREATE TABLE v2_gift_card_template (id INTEGER PRIMARY KEY, name TEXT NOT NULL, description TEXT, type INTEGER NOT NULL, status INTEGER NOT NULL, conditions TEXT, rewards TEXT NOT NULL, limits TEXT, special_config TEXT, icon TEXT, background_image TEXT, theme_color TEXT, sort INTEGER, admin_id INTEGER, created_at INTEGER, updated_at INTEGER);
		CREATE TABLE v2_gift_card_code (id INTEGER PRIMARY KEY, template_id INTEGER, code TEXT, batch_id TEXT, status INTEGER, user_id INTEGER, used_at INTEGER, expires_at INTEGER, actual_rewards TEXT, usage_count INTEGER, max_usage INTEGER, metadata TEXT, created_at INTEGER, updated_at INTEGER);
		CREATE TABLE v2_gift_card_usage (id INTEGER PRIMARY KEY, code_id INTEGER, template_id INTEGER, user_id INTEGER, invite_user_id INTEGER, rewards_given TEXT, invite_rewards TEXT, user_level_at_use INTEGER, plan_id_at_use INTEGER, multiplier_applied DECIMAL(3,2), ip_address TEXT, user_agent TEXT, notes TEXT, created_at INTEGER);
		INSERT INTO v2_gift_card_template VALUES (1, '通用卡', '描述', 1, 1, '{"new_user_only":true,"allowed_plans":[5,5]}', '{"balance":100}', '{"max_use_per_user":2,"invite_reward_rate":0.125}', '{"start_time":1700000000,"end_time":1800000000,"festival_bonus":1.5}', '', '', '#1890ff', 1, 7, 1690000000, 1690000100);
		INSERT INTO v2_gift_card_template VALUES (2, '盲盒卡', NULL, 3, 1, NULL, '{"random_rewards":[{"weight":3,"transfer_enable":1073741824,"expire_days":7}]}', NULL, NULL, NULL, NULL, '#000000', 2, 7, 1690000000, 1690000100);
		INSERT INTO v2_gift_card_code VALUES (10, 1, 'LEGACYGC00000001', 'legacy_batch_0001', 1, 8, 1700000200, NULL, '{"balance":100}', 1, 2, '{"campaign":"summer"}', 1690000200, 1700000200);
		INSERT INTO v2_gift_card_code VALUES (11, 2, 'LEGACYGC00000002', NULL, 2, NULL, NULL, 1700000000, NULL, 0, 1, NULL, 1690000200, 1700000200);
		INSERT INTO v2_gift_card_usage VALUES (20, 10, 1, 8, NULL, '{"balance":100}', NULL, 9, NULL, 1.5, '192.0.2.1', 'legacy-test', 'audit', 1700000200);
	`)
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
