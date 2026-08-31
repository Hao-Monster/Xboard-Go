package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	TrustedPluginTelegram     = "telegram"
	TrustedPluginAlipayF2F    = "alipay_f2f"
	TrustedPluginBTCPay       = "btcpay"
	TrustedPluginCoinPayments = "coin_payments"
	TrustedPluginCoinbase     = "coinbase"
	TrustedPluginEPay         = "epay"
	TrustedPluginMGate        = "mgate"
)

var telegramPluginTextKeys = []string{
	"start_welcome_title", "start_bot_description", "start_bind_guide", "start_unbind_guide",
	"start_bind_commands", "start_footer", "help_text",
}

func (s *Store) ListTrustedPlugins(ctx context.Context) ([]TrustedPlugin, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT code,name,type,version,enabled,config_json,revision,updated_at
		FROM trusted_plugins ORDER BY type,code
	`)
	if err != nil {
		return nil, fmt.Errorf("list trusted plugins: %w", err)
	}
	defer rows.Close()
	plugins := make([]TrustedPlugin, 0, 7)
	for rows.Next() {
		plugin, err := scanTrustedPlugin(rows)
		if err != nil {
			return nil, err
		}
		plugins = append(plugins, plugin)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate trusted plugins: %w", err)
	}
	return plugins, nil
}

func (s *Store) GetTrustedPlugin(ctx context.Context, code string) (TrustedPlugin, error) {
	if !validTrustedPluginCode(code) {
		return TrustedPlugin{}, ErrNotFound
	}
	return scanTrustedPlugin(s.db.QueryRowContext(ctx, `
		SELECT code,name,type,version,enabled,config_json,revision,updated_at
		FROM trusted_plugins WHERE code=?
	`, code))
}

func (s *Store) TrustedPluginEnabled(ctx context.Context, code string) (bool, error) {
	if !validTrustedPluginCode(code) {
		return false, ErrNotFound
	}
	var enabled bool
	if err := s.db.QueryRowContext(ctx, `SELECT enabled FROM trusted_plugins WHERE code=?`, code).Scan(&enabled); err != nil {
		if err == sql.ErrNoRows {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("read trusted plugin status: %w", err)
	}
	return enabled, nil
}

func (s *Store) UpdateTrustedPlugin(ctx context.Context, administratorID int64, code string, revision int64, input SaveTrustedPluginInput, now time.Time) (TrustedPlugin, error) {
	if administratorID < 1 || revision < 1 || now.Unix() < 0 || !validTrustedPluginCode(code) {
		if !validTrustedPluginCode(code) {
			return TrustedPlugin{}, ErrNotFound
		}
		return TrustedPlugin{}, ErrInvalidInput
	}
	configJSON, err := normalizeTrustedPluginConfig(code, input.Config)
	if err != nil {
		return TrustedPlugin{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TrustedPlugin{}, fmt.Errorf("begin trusted plugin update: %w", err)
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
		UPDATE trusted_plugins
		SET enabled=?,config_json=?,revision=revision+1,updated_by=?,updated_at=?
		WHERE code=? AND revision=?
	`, input.Enabled, string(configJSON), administratorID, now.Unix(), code, revision)
	if err != nil {
		return TrustedPlugin{}, fmt.Errorf("update trusted plugin: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return TrustedPlugin{}, fmt.Errorf("count trusted plugin update: %w", err)
	}
	if changed != 1 {
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM trusted_plugins WHERE code=?)`, code).Scan(&exists); err != nil {
			return TrustedPlugin{}, fmt.Errorf("inspect trusted plugin update conflict: %w", err)
		}
		if !exists {
			return TrustedPlugin{}, ErrNotFound
		}
		return TrustedPlugin{}, ErrRevisionConflict
	}
	updated, err := scanTrustedPlugin(tx.QueryRowContext(ctx, `
		SELECT code,name,type,version,enabled,config_json,revision,updated_at
		FROM trusted_plugins WHERE code=?
	`, code))
	if err != nil {
		return TrustedPlugin{}, err
	}
	if err := tx.Commit(); err != nil {
		return TrustedPlugin{}, fmt.Errorf("commit trusted plugin update: %w", err)
	}
	return updated, nil
}

func TrustedPluginCodeForPaymentProvider(provider PaymentProvider) (string, bool) {
	switch provider {
	case PaymentProviderAlipayF2F:
		return TrustedPluginAlipayF2F, true
	case PaymentProviderBTCPay:
		return TrustedPluginBTCPay, true
	case PaymentProviderCoinPayments:
		return TrustedPluginCoinPayments, true
	case PaymentProviderCoinbase:
		return TrustedPluginCoinbase, true
	case PaymentProviderEPay:
		return TrustedPluginEPay, true
	case PaymentProviderMGate:
		return TrustedPluginMGate, true
	default:
		return "", false
	}
}

type trustedPluginScanner interface {
	Scan(...any) error
}

func scanTrustedPlugin(scanner trustedPluginScanner) (TrustedPlugin, error) {
	var plugin TrustedPlugin
	var configJSON string
	var updatedAt int64
	if err := scanner.Scan(&plugin.Code, &plugin.Name, &plugin.Type, &plugin.Version, &plugin.Enabled, &configJSON, &plugin.Revision, &updatedAt); err != nil {
		if err == sql.ErrNoRows {
			return TrustedPlugin{}, ErrNotFound
		}
		return TrustedPlugin{}, fmt.Errorf("scan trusted plugin: %w", err)
	}
	if err := json.Unmarshal([]byte(configJSON), &plugin.Config); err != nil {
		return TrustedPlugin{}, fmt.Errorf("decode trusted plugin config: %w", err)
	}
	plugin.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return plugin, nil
}

func normalizeTrustedPluginConfig(code string, input map[string]any) ([]byte, error) {
	if code != TrustedPluginTelegram {
		if len(input) != 0 {
			return nil, ErrInvalidInput
		}
		return []byte("{}"), nil
	}
	if len(input) != 9 {
		return nil, ErrInvalidInput
	}
	for _, key := range []string{"enable_ticket_notify", "enable_payment_notify"} {
		if _, ok := input[key].(bool); !ok {
			return nil, ErrInvalidInput
		}
	}
	for _, key := range telegramPluginTextKeys {
		value, ok := input[key].(string)
		if !ok || value == "" || len(value) > 4_096 || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return nil, ErrInvalidInput
		}
	}
	encoded, err := json.Marshal(input)
	if err != nil || len(encoded) > 32_768 {
		return nil, ErrInvalidInput
	}
	return encoded, nil
}

func validTrustedPluginCode(code string) bool {
	switch code {
	case TrustedPluginTelegram, TrustedPluginAlipayF2F, TrustedPluginBTCPay, TrustedPluginCoinPayments, TrustedPluginCoinbase, TrustedPluginEPay, TrustedPluginMGate:
		return true
	default:
		return false
	}
}
