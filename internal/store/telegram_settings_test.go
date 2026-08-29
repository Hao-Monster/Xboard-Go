package store

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTelegramSettingsKeepSecretsWriteOnlyAndRevisionSafe(t *testing.T) {
	database := newTestStore(t)
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	administrator, err := database.CreateAdminUser(t.Context(), CreateAdminUserInput{
		Email: "telegram-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	if err != nil {
		t.Fatalf("CreateAdminUser() error = %v", err)
	}
	initial, err := database.GetTelegramSettings(t.Context())
	if err != nil || initial.BotEnabled || initial.BotTokenSet || initial.WebhookURL != "" || initial.DiscussLink != "" {
		t.Fatalf("initial Telegram settings = %#v, err=%v", initial, err)
	}
	tokenCipher := bytes.Repeat([]byte{0x31}, 64)
	updated, err := database.UpdateTelegramSettings(t.Context(), administrator.ID, initial.Revision, SaveTelegramSettingsInput{
		BotEnabled: true, ReplaceBotToken: true, BotTokenCipher: tokenCipher,
		WebhookURL: " https://panel.example.test/ ", DiscussLink: " https://t.me/xboard_group/ ",
	}, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("UpdateTelegramSettings() error = %v", err)
	}
	if !updated.BotEnabled || !updated.BotTokenSet || updated.WebhookURL != "https://panel.example.test" || updated.DiscussLink != "https://t.me/xboard_group" || updated.Revision != initial.Revision+1 {
		t.Fatalf("updated Telegram settings = %#v", updated)
	}
	secrets, err := database.GetTelegramSecretCiphers(t.Context())
	if err != nil || !bytes.Equal(secrets.BotToken, tokenCipher) || len(secrets.WebhookSecret) != 0 || len(secrets.PendingWebhookSecret) != 0 || secrets.ProvisionID != "" {
		t.Fatalf("Telegram secret ciphers = %#v, err=%v", secrets, err)
	}
	if _, err := database.UpdateTelegramSettings(t.Context(), administrator.ID, initial.Revision, SaveTelegramSettingsInput{
		BotEnabled: true, WebhookURL: updated.WebhookURL, DiscussLink: updated.DiscussLink,
	}, now.Add(2*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale UpdateTelegramSettings() error=%v, want ErrConflict", err)
	}
	cleared, err := database.UpdateTelegramSettings(t.Context(), administrator.ID, updated.Revision, SaveTelegramSettingsInput{
		BotEnabled: false, ReplaceBotToken: true, WebhookURL: updated.WebhookURL, DiscussLink: updated.DiscussLink,
	}, now.Add(3*time.Minute))
	if err != nil || cleared.BotTokenSet || cleared.BotEnabled {
		t.Fatalf("cleared Telegram settings = %#v, err=%v", cleared, err)
	}
}

func TestTelegramWebhookProvisionIsFencedByConfigurationChanges(t *testing.T) {
	database := newTestStore(t)
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	administrator, _ := database.CreateAdminUser(t.Context(), CreateAdminUserInput{
		Email: "telegram-provision@example.test", PasswordHash: "hash", IsAdmin: true,
	}, now)
	initial, _ := database.GetTelegramSettings(t.Context())
	configured, err := database.UpdateTelegramSettings(t.Context(), administrator.ID, initial.Revision, SaveTelegramSettingsInput{
		BotEnabled: true, ReplaceBotToken: true, BotTokenCipher: bytes.Repeat([]byte{0x41}, 64),
		WebhookURL: "https://panel.example.test", DiscussLink: "https://t.me/xboard_group",
	}, now)
	if err != nil {
		t.Fatalf("configure Telegram settings: %v", err)
	}
	provisionID := "0123456789abcdef0123456789abcdef"
	secrets, err := database.BeginTelegramWebhookProvision(t.Context(), administrator.ID, configured.Revision, provisionID, bytes.Repeat([]byte{0x42}, 64), now.Add(time.Minute))
	if err != nil || secrets.ProvisionID != provisionID || len(secrets.BotToken) == 0 || len(secrets.WebhookSecret) != 0 || !bytes.Equal(secrets.PendingWebhookSecret, bytes.Repeat([]byte{0x42}, 64)) {
		t.Fatalf("BeginTelegramWebhookProvision() = %#v, %v", secrets, err)
	}
	current, err := database.GetTelegramSettings(t.Context())
	if err != nil || current.Revision != configured.Revision {
		t.Fatalf("pending provision changed public revision: %#v, %v", current, err)
	}
	completed, err := database.CompleteTelegramWebhookProvision(t.Context(), provisionID, "xboard_test_bot", now.Add(2*time.Minute))
	if err != nil || completed.BotUsername != "xboard_test_bot" || completed.WebhookConfiguredAt == nil {
		t.Fatalf("CompleteTelegramWebhookProvision() = %#v, %v", completed, err)
	}

	secondProvisionID := "1123456789abcdef0123456789abcdef"
	secrets, err = database.BeginTelegramWebhookProvision(t.Context(), administrator.ID, completed.Revision, secondProvisionID, bytes.Repeat([]byte{0x43}, 64), now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("second BeginTelegramWebhookProvision() error = %v", err)
	}
	retried, err := database.BeginTelegramWebhookProvision(t.Context(), administrator.ID, completed.Revision, "2123456789abcdef0123456789abcdef", bytes.Repeat([]byte{0x45}, 64), now.Add(3*time.Minute))
	if err != nil || retried.ProvisionID != secondProvisionID || !bytes.Equal(retried.PendingWebhookSecret, bytes.Repeat([]byte{0x43}, 64)) {
		t.Fatalf("retry did not reuse durable pending provision: %#v, %v", retried, err)
	}
	current, _ = database.GetTelegramSettings(t.Context())
	changed, err := database.UpdateTelegramSettings(t.Context(), administrator.ID, current.Revision, SaveTelegramSettingsInput{
		BotEnabled: true, ReplaceBotToken: true, BotTokenCipher: bytes.Repeat([]byte{0x44}, 64),
		WebhookURL: current.WebhookURL, DiscussLink: current.DiscussLink,
	}, now.Add(4*time.Minute))
	if err != nil || changed.BotUsername != "" || changed.WebhookConfiguredAt != nil {
		t.Fatalf("rotated Telegram settings = %#v, %v", changed, err)
	}
	if _, err := database.CompleteTelegramWebhookProvision(t.Context(), secondProvisionID, "stale_test_bot", now.Add(5*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CompleteTelegramWebhookProvision() error=%v, want ErrConflict", err)
	}
	secrets, _ = database.GetTelegramSecretCiphers(t.Context())
	if len(secrets.WebhookSecret) != 0 || len(secrets.PendingWebhookSecret) != 0 || secrets.ProvisionID != "" {
		t.Fatalf("rotated Telegram secrets retained stale webhook state: %#v", secrets)
	}
}

func TestTelegramUserAvailabilityUsesUniqueIndexedIdentity(t *testing.T) {
	database := newTestStore(t)
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	active, _ := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "telegram-active@example.test", PasswordHash: "hash"}, now)
	if _, err := database.db.ExecContext(t.Context(), `
		UPDATE users SET telegram_id = 778899, transfer_enable = 1024, expired_at = ? WHERE id = ?
	`, now.Add(time.Hour).Unix(), active.ID); err != nil {
		t.Fatalf("prepare Telegram user: %v", err)
	}
	available, err := database.TelegramUserAvailable(t.Context(), 778899, now)
	if err != nil || !available {
		t.Fatalf("TelegramUserAvailable(active)=(%t,%v)", available, err)
	}
	if _, err := database.db.ExecContext(t.Context(), `UPDATE users SET banned = 1 WHERE id = ?`, active.ID); err != nil {
		t.Fatalf("ban Telegram user: %v", err)
	}
	available, err = database.TelegramUserAvailable(t.Context(), 778899, now)
	if err != nil || available {
		t.Fatalf("TelegramUserAvailable(banned)=(%t,%v)", available, err)
	}
	other, _ := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "telegram-duplicate@example.test", PasswordHash: "hash"}, now)
	if _, err := database.db.ExecContext(t.Context(), `UPDATE users SET telegram_id = 778899 WHERE id = ?`, other.ID); err == nil {
		t.Fatal("duplicate Telegram id must be rejected by the database")
	}
}

func TestTelegramWebhookUpdateClaimsFenceDuplicatesFailuresAndStaleWorkers(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	firstClaim := "0123456789abcdef0123456789abcdef"
	secondClaim := "1123456789abcdef0123456789abcdef"
	state, err := database.ClaimTelegramWebhookUpdate(ctx, 9001, firstClaim, now)
	if err != nil || state != TelegramWebhookClaimAcquired {
		t.Fatalf("first claim=(%v,%v)", state, err)
	}
	state, err = database.ClaimTelegramWebhookUpdate(ctx, 9001, secondClaim, now.Add(time.Second))
	if err != nil || state != TelegramWebhookClaimInProgress {
		t.Fatalf("concurrent claim=(%v,%v)", state, err)
	}
	if err := database.CompleteTelegramWebhookUpdate(ctx, 9001, secondClaim, now.Add(2*time.Second)); !errors.Is(err, ErrConflict) {
		t.Fatalf("foreign completion error=%v", err)
	}
	state, err = database.ClaimTelegramWebhookUpdate(ctx, 9001, secondClaim, now.Add(telegramWebhookClaimStaleAfter))
	if err != nil || state != TelegramWebhookClaimAcquired {
		t.Fatalf("stale reclaim=(%v,%v)", state, err)
	}
	if err := database.CompleteTelegramWebhookUpdate(ctx, 9001, firstClaim, now.Add(telegramWebhookClaimStaleAfter)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale worker completion error=%v", err)
	}
	if err := database.CompleteTelegramWebhookUpdate(ctx, 9001, secondClaim, now.Add(telegramWebhookClaimStaleAfter)); err != nil {
		t.Fatalf("active worker completion error=%v", err)
	}
	state, err = database.ClaimTelegramWebhookUpdate(ctx, 9001, firstClaim, now.Add(telegramWebhookClaimStaleAfter+time.Second))
	if err != nil || state != TelegramWebhookClaimCompleted {
		t.Fatalf("completed duplicate=(%v,%v)", state, err)
	}

	state, err = database.ClaimTelegramWebhookUpdate(ctx, 9002, firstClaim, now)
	if err != nil || state != TelegramWebhookClaimAcquired {
		t.Fatalf("retry claim=(%v,%v)", state, err)
	}
	if err := database.ReleaseTelegramWebhookUpdate(ctx, 9002, firstClaim); err != nil {
		t.Fatalf("release failed claim: %v", err)
	}
	state, err = database.ClaimTelegramWebhookUpdate(ctx, 9002, secondClaim, now.Add(time.Second))
	if err != nil || state != TelegramWebhookClaimAcquired {
		t.Fatalf("released retry claim=(%v,%v)", state, err)
	}

	old := now.Add(-telegramWebhookReceiptRetention - time.Second)
	if state, err := database.ClaimTelegramWebhookUpdate(ctx, 9003, firstClaim, old); err != nil || state != TelegramWebhookClaimAcquired {
		t.Fatalf("old claim=(%v,%v)", state, err)
	}
	if err := database.CompleteTelegramWebhookUpdate(ctx, 9003, firstClaim, old); err != nil {
		t.Fatalf("complete old claim: %v", err)
	}
	if state, err := database.ClaimTelegramWebhookUpdate(ctx, 9004, firstClaim, now); err != nil || state != TelegramWebhookClaimAcquired {
		t.Fatalf("pruning claim=(%v,%v)", state, err)
	}
	var retained int
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM telegram_webhook_updates WHERE update_id = 9003`).Scan(&retained); err != nil || retained != 0 {
		t.Fatalf("expired receipt count=%d error=%v", retained, err)
	}
}

func TestTelegramSettingsRejectUnsafeURLsAndMissingToken(t *testing.T) {
	database := newTestStore(t)
	now := time.Unix(1_800_000_000, 0)
	administrator, _ := database.CreateAdminUser(t.Context(), CreateAdminUserInput{Email: "telegram-validation@example.test", PasswordHash: "hash", IsAdmin: true}, now)
	initial, _ := database.GetTelegramSettings(t.Context())
	for name, input := range map[string]SaveTelegramSettingsInput{
		"enabled without token": {BotEnabled: true},
		"insecure webhook":      {WebhookURL: "http://panel.example.test"},
		"webhook credentials":   {WebhookURL: "https://user:pass@panel.example.test"},
		"webhook query":         {WebhookURL: "https://panel.example.test?secret=value"},
		"javascript group":      {DiscussLink: "javascript:alert(1)"},
		"untrusted group":       {DiscussLink: "https://example.test/group"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := database.UpdateTelegramSettings(t.Context(), administrator.ID, initial.Revision, input, now); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("UpdateTelegramSettings() error=%v, want ErrInvalidInput", err)
			}
		})
	}
}

func TestTelegramSchemaValidationRequiresUniquePartialIdentityIndex(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.ExecContext(t.Context(), `DROP INDEX idx_users_unique_telegram_id`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(t.Context(), database.db, CurrentSchemaVersion()); err == nil || !strings.Contains(err.Error(), "Telegram identity index") {
		t.Fatalf("ValidateSchema() error=%v", err)
	}
}

func TestTelegramSchemaValidationRejectsWrongIdentityIndexPredicate(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.ExecContext(t.Context(), `
		DROP INDEX idx_users_unique_telegram_id;
		CREATE UNIQUE INDEX idx_users_unique_telegram_id ON users(telegram_id) WHERE telegram_id IS NULL;
	`); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSchema(t.Context(), database.db, CurrentSchemaVersion()); err == nil || !strings.Contains(err.Error(), "exclude only null identities") {
		t.Fatalf("ValidateSchema() error=%v", err)
	}
}

func TestSchemaV47RejectsDuplicateTelegramIdentitiesWithoutPartialMigration(t *testing.T) {
	database := newTestStore(t)
	ctx := t.Context()
	now := time.Date(2026, 8, 29, 14, 0, 0, 0, time.UTC)
	first, _ := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "telegram-v46-first@example.test", PasswordHash: "hash"}, now)
	second, _ := database.CreateAdminUser(ctx, CreateAdminUserInput{Email: "telegram-v46-second@example.test", PasswordHash: "hash"}, now)
	if _, err := database.db.ExecContext(ctx, `
		DROP INDEX idx_users_unique_telegram_id;
		DROP TABLE telegram_webhook_updates;
		UPDATE users SET telegram_id = 445566 WHERE id IN (?, ?);
		ALTER TABLE app_settings DROP COLUMN telegram_bot_enable;
		ALTER TABLE app_settings DROP COLUMN telegram_bot_token_cipher;
		ALTER TABLE app_settings DROP COLUMN telegram_webhook_url;
		ALTER TABLE app_settings DROP COLUMN telegram_discuss_link;
		ALTER TABLE app_settings DROP COLUMN telegram_webhook_secret_cipher;
		ALTER TABLE app_settings DROP COLUMN telegram_webhook_pending_secret_cipher;
		ALTER TABLE app_settings DROP COLUMN telegram_webhook_provision_id;
		ALTER TABLE app_settings DROP COLUMN telegram_bot_username;
		ALTER TABLE app_settings DROP COLUMN telegram_webhook_configured_at;
		PRAGMA user_version = 46;
	`, first.ID, second.ID); err != nil {
		t.Fatalf("prepare v46 duplicate Telegram identities: %v", err)
	}
	if err := database.Migrate(ctx); err == nil || !strings.Contains(err.Error(), "duplicate Telegram id 445566") {
		t.Fatalf("Migrate(v46 duplicate Telegram identities) error=%v", err)
	}
	var version, telegramColumns, webhookTables int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM pragma_table_info('app_settings') WHERE name LIKE 'telegram_%'`).Scan(&telegramColumns); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name='telegram_webhook_updates'`).Scan(&webhookTables); err != nil {
		t.Fatal(err)
	}
	if version != 46 || telegramColumns != 0 || webhookTables != 0 {
		t.Fatalf("failed migration left version=%d Telegram columns=%d webhook tables=%d", version, telegramColumns, webhookTables)
	}
	if _, err := database.db.ExecContext(ctx, `UPDATE users SET telegram_id = NULL WHERE id = ?`, second.ID); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(v46 repaired Telegram identities) error=%v", err)
	}
	if err := database.ValidateCurrentSchema(ctx); err != nil {
		t.Fatalf("ValidateCurrentSchema() error=%v", err)
	}
}
