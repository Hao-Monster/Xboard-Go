package store

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyClientAppSettingsIsVerifiedAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	settings := LegacyClientAppSettings{
		WindowsVersion: "4.8.1", WindowsDownloadURL: "https://download.example.test/windows.exe",
		MacOSVersion: "4.8.2", MacOSDownloadURL: "https://download.example.test/macos.dmg",
		AndroidVersion: "4.8.3", AndroidDownloadURL: "https://download.example.test/android.apk",
	}
	input := LegacyClientAppSettingsImport{
		Slice: LegacyClientAppSettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Settings: settings, Checksum: LegacyClientAppSettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacyClientAppSettings(context.Background(), input, now)
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum || report.Settings.SourceRows != 1 {
		t.Fatalf("ImportLegacyClientAppSettings()=(%#v,%v)", report, err)
	}
	current, err := database.GetClientAppSettings(context.Background())
	if err != nil || current.Revision != 2 || current.WindowsVersion != "4.8.1" || current.MacOSVersion != "4.8.2" || current.AndroidVersion != "4.8.3" {
		t.Fatalf("client app settings=%#v err=%v", current, err)
	}
	repeated, err := database.ImportLegacyClientAppSettings(context.Background(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	input.SourceSHA256 = strings.Repeat("c", 64)
	if _, err := database.ImportLegacyClientAppSettings(context.Background(), input, now.Add(2*time.Hour)); err == nil || !strings.Contains(err.Error(), "another snapshot") {
		t.Fatalf("different source import error=%v", err)
	}
}

func TestImportLegacyClientAppSettingsRequiresPristineTargetAndSafeNormalizedInput(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	administrator, _ := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "client-app-existing@example.test", PasswordHash: "hash"}, now)
	current, _ := database.GetClientAppSettings(ctx)
	if _, err := database.UpdateClientAppSettings(ctx, administrator.ID, current.Revision, SaveClientAppSettingsInput{WindowsVersion: "existing"}, now); err != nil {
		t.Fatal(err)
	}
	settings := LegacyClientAppSettings{WindowsVersion: "4.8.1", WindowsDownloadURL: "https://download.example.test/windows.exe"}
	input := LegacyClientAppSettingsImport{
		Slice: LegacyClientAppSettingsSlice, SourceSHA256: strings.Repeat("d", 64), SourceSize: 1024,
		Settings: settings, Checksum: LegacyClientAppSettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("e", 64),
	}
	if _, err := database.ImportLegacyClientAppSettings(ctx, input, now.Add(time.Minute)); err == nil || !strings.Contains(err.Error(), "pristine") {
		t.Fatalf("non-pristine import error=%v", err)
	}

	clean := newTestStore(t)
	input.Settings.WindowsVersion = " unnormalized "
	input.Checksum = LegacyClientAppSettingsChecksum(input.Settings)
	if _, err := clean.ImportLegacyClientAppSettings(ctx, input, now); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("unnormalized import error=%v", err)
	}
	input.Settings.WindowsVersion = "4.8.1"
	input.Settings.WindowsDownloadURL = "http://download.example.test/windows.exe"
	input.Checksum = LegacyClientAppSettingsChecksum(input.Settings)
	if _, err := clean.ImportLegacyClientAppSettings(ctx, input, now); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("unsafe import error=%v", err)
	}
}
