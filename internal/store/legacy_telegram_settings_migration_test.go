package store

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyTelegramSettingsIsVerifiedIdempotentAndCredentialFree(t *testing.T) {
	database := newTestStore(t)
	settings := LegacyTelegramSettings{
		BotEnabled: true, BotTokenConfigured: true, BotTokenCipher: bytes.Repeat([]byte{0x51}, 64),
		WebhookURL: "https://panel.example.test", DiscussLink: "https://t.me/xboard_group",
	}
	input := LegacyTelegramSettingsImport{
		Slice: LegacyTelegramSettingsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Settings: settings, Checksum: LegacyTelegramSettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	now := time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)
	report, err := database.ImportLegacyTelegramSettings(context.Background(), input, now)
	if err != nil || report.Settings.SourceChecksum != report.Settings.TargetChecksum {
		t.Fatalf("ImportLegacyTelegramSettings()=(%#v,%v)", report, err)
	}
	current, err := database.GetTelegramSettings(context.Background())
	if err != nil || !current.BotEnabled || !current.BotTokenSet || current.WebhookURL != settings.WebhookURL || current.DiscussLink != settings.DiscussLink {
		t.Fatalf("Telegram settings=%#v err=%v", current, err)
	}
	repeated, err := database.ImportLegacyTelegramSettings(context.Background(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || repeated.AppliedAt != now {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	encoded := LegacyTelegramSettingsChecksum(settings)
	settings.BotTokenCipher = bytes.Repeat([]byte{0x52}, 64)
	if LegacyTelegramSettingsChecksum(settings) != encoded {
		t.Fatal("public Telegram migration checksum varies with encrypted credential material")
	}
}

func TestImportLegacyTelegramSettingsRequiresPristineDomainAndValidEncryption(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	current, _ := database.GetTelegramSettings(ctx)
	administrator, _ := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "telegram-existing@example.test", PasswordHash: "hash"}, time.Unix(1, 0))
	if _, err := database.UpdateTelegramSettings(ctx, administrator.ID, current.Revision, SaveTelegramSettingsInput{WebhookURL: "https://existing.example.test"}, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	settings := LegacyTelegramSettings{BotEnabled: true, BotTokenConfigured: true, BotTokenCipher: bytes.Repeat([]byte{0x61}, 64), WebhookURL: "https://panel.example.test"}
	input := LegacyTelegramSettingsImport{
		Slice: LegacyTelegramSettingsSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 1024,
		Settings: settings, Checksum: LegacyTelegramSettingsChecksum(settings),
		RollbackBackupPath: "backup.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64),
	}
	if _, err := database.ImportLegacyTelegramSettings(ctx, input, time.Unix(3, 0)); err == nil || !strings.Contains(err.Error(), "pristine") {
		t.Fatalf("non-pristine import error=%v", err)
	}
	input.Settings.BotTokenCipher = []byte("short")
	input.Checksum = LegacyTelegramSettingsChecksum(input.Settings)
	if _, err := database.ImportLegacyTelegramSettings(ctx, input, time.Unix(3, 0)); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid cipher import error=%v", err)
	}
}
