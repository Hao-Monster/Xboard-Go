package legacymigration

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const legacyMailSettingsKeys = `'email_host','email_port','email_username','email_password','email_encryption','email_from_address','remind_mail_enable'`

type MailSettingsSnapshot struct {
	Path         string
	Size         int64
	SHA256       string
	Settings     store.LegacyMailSettings
	SMTPPassword []byte `json:"-"`
	Checksum     string
}

func (snapshot *MailSettingsSnapshot) ClearSecrets() {
	if snapshot == nil {
		return
	}
	zeroLegacyBytes(snapshot.SMTPPassword)
	snapshot.SMTPPassword = nil
}

func ReadMailSettingsSnapshot(ctx context.Context, sourcePath string) (MailSettingsSnapshot, error) {
	settings := store.DefaultLegacyMailSettings()
	var password []byte
	identity, err := readLegacySnapshot(ctx, sourcePath, func(database *sql.DB) error {
		if err := requireRealTable(ctx, database, "v2_settings", []string{"name", "value"}); err != nil {
			return err
		}
		budgetQuery := `SELECT COUNT(*),COALESCE(SUM(length(CAST(name AS BLOB))+COALESCE(length(CAST(value AS BLOB)),0)),0)
			FROM v2_settings WHERE name IN (` + legacyMailSettingsKeys + `)`
		if err := validateLegacyQueryBudget(ctx, database, budgetQuery, 7, 16<<10, "legacy mail settings"); err != nil {
			return err
		}
		rows, err := database.QueryContext(ctx, `SELECT name,CAST(value AS BLOB),value IS NULL FROM v2_settings WHERE name IN (`+legacyMailSettingsKeys+`) ORDER BY name`)
		if err != nil {
			return fmt.Errorf("read legacy mail settings: %w", err)
		}
		defer rows.Close()
		seen := make(map[string]struct{}, 7)
		for rows.Next() {
			var name string
			var raw []byte
			var isNull bool
			if err := rows.Scan(&name, &raw, &isNull); err != nil {
				return fmt.Errorf("scan legacy mail setting: %w", err)
			}
			// database/sql represents both SQL NULL and a zero-length BLOB as a nil
			// byte slice. Preserve the old setting's empty-string semantics.
			if !isNull && raw == nil {
				raw = []byte{}
			}
			if _, exists := seen[name]; exists {
				zeroLegacyBytes(raw)
				return fmt.Errorf("legacy mail settings contain duplicate %q rows", name)
			}
			seen[name] = struct{}{}
			if err := applyLegacyMailSetting(name, raw, &settings, &password); err != nil {
				zeroLegacyBytes(raw)
				return err
			}
			zeroLegacyBytes(raw)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("iterate legacy mail settings: %w", err)
		}
		settings.SMTPEnabled = settings.SMTPHost != ""
		settings, err = store.NormalizeLegacyMailSettings(settings)
		if err != nil {
			return fmt.Errorf("validate legacy mail settings: %w", err)
		}
		return nil
	})
	if err != nil {
		zeroLegacyBytes(password)
		return MailSettingsSnapshot{}, err
	}
	return MailSettingsSnapshot{
		Path: identity.Path, Size: identity.Size, SHA256: identity.SHA256,
		Settings: settings, SMTPPassword: password, Checksum: store.LegacyMailSettingsChecksum(settings),
	}, nil
}

func applyLegacyMailSetting(name string, raw []byte, settings *store.LegacyMailSettings, password *[]byte) error {
	if settings == nil || password == nil {
		return errors.New("legacy mail settings destination is unavailable")
	}
	var err error
	switch name {
	case "email_host":
		settings.SMTPHost, err = parseLegacyMailText(raw, settings.SMTPHost, 253)
	case "email_port":
		settings.SMTPPort, err = parseLegacyMailPort(raw, settings.SMTPPort)
	case "email_username":
		settings.SMTPUsername, err = parseLegacyMailText(raw, settings.SMTPUsername, 320)
	case "email_password":
		*password, settings.SMTPPasswordConfigured, err = parseLegacyMailSecret(raw)
	case "email_encryption":
		settings.SMTPEncryption, err = parseLegacyMailEncryption(raw, settings.SMTPEncryption)
	case "email_from_address":
		settings.SMTPFromAddress, err = parseLegacyMailText(raw, settings.SMTPFromAddress, 320)
	case "remind_mail_enable":
		settings.RemindMailEnabled, err = parseLegacyPolicyBoolean(raw, settings.RemindMailEnabled)
	default:
		return fmt.Errorf("unsupported legacy mail setting %q", name)
	}
	if err != nil {
		return fmt.Errorf("validate legacy %s: %w", name, err)
	}
	return nil
}

func parseLegacyMailText(raw []byte, fallback string, limit int) (string, error) {
	if raw == nil || bytes.EqualFold(bytes.TrimSpace(raw), []byte("null")) {
		return fallback, nil
	}
	value := strings.TrimSpace(string(raw))
	if !utf8.ValidString(value) || len(value) > limit || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return "", errors.New("contains invalid text")
	}
	return value, nil
}

func parseLegacyMailPort(raw []byte, fallback int) (int, error) {
	if raw == nil || bytes.EqualFold(bytes.TrimSpace(raw), []byte("null")) {
		return fallback, nil
	}
	value := strings.TrimSpace(string(raw))
	parsed, err := strconv.ParseInt(value, 10, 32)
	if err != nil || parsed < 1 || parsed > 65_535 {
		return 0, errors.New("must be an integer between 1 and 65535")
	}
	return int(parsed), nil
}

func parseLegacyMailEncryption(raw []byte, fallback string) (string, error) {
	if raw == nil || bytes.EqualFold(bytes.TrimSpace(raw), []byte("null")) {
		return fallback, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(raw))) {
	case "tls":
		return "starttls", nil
	case "ssl":
		return "tls", nil
	case "":
		return "none", nil
	default:
		return "", errors.New("must be tls or ssl")
	}
}

func parseLegacyMailSecret(raw []byte) ([]byte, bool, error) {
	if len(raw) == 0 || bytes.EqualFold(bytes.TrimSpace(raw), []byte("null")) {
		return nil, false, nil
	}
	if len(raw) > 4<<10 || bytes.IndexByte(raw, 0) >= 0 {
		return nil, false, errors.New("secret exceeds 4096 bytes or contains a null byte")
	}
	return append([]byte(nil), raw...), true, nil
}
