package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/theme"
)

func TestImportLegacyThemeSettingsIsVerifiedIdempotentAndPristineOnly(t *testing.T) {
	database := newTestStore(t)
	settings := LegacyThemeSettings{ActiveTheme: "Xboard", Config: theme.Config{ThemeColor: "blue", FontScale: "normal", Radius: "rounded"}}
	input := LegacyThemeSettingsImport{
		Slice: LegacyThemeSettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Settings: settings, Checksum: LegacyThemeSettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	now := time.Date(2026, 8, 30, 13, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacyThemeSettings(context.Background(), input, now)
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("ImportLegacyThemeSettings()=(%#v,%v)", report, err)
	}
	current, err := database.GetTheme(context.Background(), "Xboard")
	if err != nil || current.Config.ThemeColor != "blue" || current.Revision != 2 {
		t.Fatalf("imported theme=%#v err=%v", current, err)
	}
	repeated, err := database.ImportLegacyThemeSettings(context.Background(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("repeated import=(%#v,%v)", repeated, err)
	}
	input.SourceSHA256 = strings.Repeat("c", 64)
	if _, err := database.ImportLegacyThemeSettings(context.Background(), input, now.Add(2*time.Hour)); err == nil || !strings.Contains(err.Error(), "another snapshot") {
		t.Fatalf("different source import error=%v", err)
	}

	nonPristine := newTestStore(t)
	admin, _ := nonPristine.CreateAdminUser(context.Background(), CreateAdminUserInput{Email: "theme-migration@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	_, _ = nonPristine.UpdateThemeConfig(context.Background(), admin.ID, "Xboard", 1, settings.Config, now)
	input.SourceSHA256 = strings.Repeat("d", 64)
	input.Checksum = LegacyThemeSettingsChecksum(input.Settings)
	if _, err := nonPristine.ImportLegacyThemeSettings(context.Background(), input, now); err == nil || !strings.Contains(err.Error(), "pristine") {
		t.Fatalf("non-pristine import error=%v", err)
	}
}
