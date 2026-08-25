package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestImportLegacySubscriptionConfigIsAtomicVerifiedAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	input := validLegacySubscriptionConfigImport()
	now := time.Unix(1_800_000_000, 0).UTC()

	report, err := database.ImportLegacySubscriptionConfig(context.Background(), input, now)
	if err != nil {
		t.Fatalf("ImportLegacySubscriptionConfig() error = %v", err)
	}
	if report.AlreadyApplied || report.Config.SourceRows != 1 || report.Config.TargetRows != 1 ||
		report.Config.SourceChecksum != report.Config.TargetChecksum {
		t.Fatalf("report = %#v", report)
	}
	settings, err := database.GetSubscriptionSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if settings.Path != "legacy_feed" || !settings.ShowInfo || !settings.ShowProtocol ||
		settings.Templates["clash"] != "proxies: []\nproxy-groups: []\nrules: []\n" || settings.Revision != 2 {
		t.Fatalf("settings = %#v", settings)
	}

	repeated, err := database.ImportLegacySubscriptionConfig(context.Background(), input, now.Add(time.Minute))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != now {
		t.Fatalf("repeated import = (%#v, %v)", repeated, err)
	}
	different := input
	different.SourceSHA256 = strings.Repeat("b", 64)
	if _, err := database.ImportLegacySubscriptionConfig(context.Background(), different, now); !errors.Is(err, ErrConflict) {
		t.Fatalf("different source error = %v, want ErrConflict", err)
	}
}

func TestImportLegacySubscriptionConfigRejectsUnsafeOrNonPristineData(t *testing.T) {
	t.Run("unsafe template", func(t *testing.T) {
		database := newTestStore(t)
		input := validLegacySubscriptionConfigImport()
		input.Config.Templates["singbox"] = `{not-json}`
		input.Checksum = LegacySubscriptionConfigChecksum(input.Config)
		if _, err := database.ImportLegacySubscriptionConfig(context.Background(), input, time.Now()); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("unsafe import error = %v, want ErrInvalidInput", err)
		}
		settings, readErr := database.GetSubscriptionSettings(context.Background())
		if readErr != nil || settings.Revision != 1 || settings.Path != "s" {
			t.Fatalf("partial settings = %#v err=%v", settings, readErr)
		}
	})

	t.Run("administrator changed target", func(t *testing.T) {
		database := newTestStore(t)
		if _, err := database.db.Exec(`UPDATE subscription_settings SET path = 'custom', revision = 2 WHERE id = 1`); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ImportLegacySubscriptionConfig(context.Background(), validLegacySubscriptionConfigImport(), time.Now()); !errors.Is(err, ErrConflict) {
			t.Fatalf("non-pristine error = %v, want ErrConflict", err)
		}
	})
}

func validLegacySubscriptionConfigImport() LegacySubscriptionConfigImport {
	templates := emptySubscriptionTemplateMap()
	templates["clash"] = "proxies: []\nproxy-groups: []\nrules: []\n"
	config := LegacySubscriptionConfig{Path: "legacy_feed", ShowInfo: true, ShowProtocol: true, Templates: templates}
	return LegacySubscriptionConfigImport{
		Slice: LegacySubscriptionConfigSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Config: config, Checksum: LegacySubscriptionConfigChecksum(config),
		RollbackBackupPath: "/var/lib/xboard-backups/pre-subscription.xbbackup", RollbackBackupSHA256: strings.Repeat("c", 64),
	}
}

func emptySubscriptionTemplateMap() map[string]string {
	result := make(map[string]string, len(SubscriptionTemplateNames))
	for _, name := range SubscriptionTemplateNames {
		result[name] = ""
	}
	return result
}
