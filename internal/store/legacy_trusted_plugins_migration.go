package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const LegacyTrustedPluginsSlice = "trusted-plugins-v1"

type LegacyTrustedPlugin struct {
	Code    string         `json:"code"`
	Type    string         `json:"type"`
	Version string         `json:"version"`
	Enabled bool           `json:"enabled"`
	Config  map[string]any `json:"config"`
}

type LegacyTrustedPluginsImport struct {
	Slice, SourceSHA256                      string
	SourceSize                               int64
	Plugins                                  []LegacyTrustedPlugin
	Checksum                                 string
	RollbackBackupPath, RollbackBackupSHA256 string
}

type LegacyTrustedPluginsImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Plugins              LegacyDomainResult `json:"plugins"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

type trustedPluginMigrationDefinition struct {
	name, pluginType, version string
}

var trustedPluginMigrationDefinitions = map[string]trustedPluginMigrationDefinition{
	TrustedPluginTelegram:     {name: "Telegram Bot", pluginType: "feature", version: "1.0.1"},
	TrustedPluginAlipayF2F:    {name: "AlipayF2F", pluginType: "payment", version: "1.0.0"},
	TrustedPluginBTCPay:       {name: "BTCPay", pluginType: "payment", version: "1.0.0"},
	TrustedPluginCoinPayments: {name: "CoinPayments", pluginType: "payment", version: "1.0.0"},
	TrustedPluginCoinbase:     {name: "Coinbase", pluginType: "payment", version: "1.0.0"},
	TrustedPluginEPay:         {name: "EPay", pluginType: "payment", version: "1.0.0"},
	TrustedPluginMGate:        {name: "MGate", pluginType: "payment", version: "1.0.0"},
}

func LegacyTrustedPluginsChecksum(plugins []LegacyTrustedPlugin) string {
	ordered := append([]LegacyTrustedPlugin(nil), plugins...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Code < ordered[right].Code })
	for index := range ordered {
		if ordered[index].Config == nil {
			ordered[index].Config = map[string]any{}
		}
	}
	if ordered == nil {
		ordered = []LegacyTrustedPlugin{}
	}
	return legacyCanonicalChecksum(ordered)
}

func ValidateLegacyTrustedPluginsData(plugins []LegacyTrustedPlugin) error {
	if len(plugins) != len(trustedPluginMigrationDefinitions) {
		return fmt.Errorf("%w: legacy trusted plugin inventory must contain exactly %d plugins", ErrInvalidInput, len(trustedPluginMigrationDefinitions))
	}
	seen := make(map[string]struct{}, len(plugins))
	for _, plugin := range plugins {
		definition, trusted := trustedPluginMigrationDefinitions[plugin.Code]
		if !trusted || plugin.Type != definition.pluginType || plugin.Version != definition.version {
			return fmt.Errorf("%w: untrusted legacy plugin identity %q", ErrInvalidInput, plugin.Code)
		}
		if _, duplicate := seen[plugin.Code]; duplicate {
			return fmt.Errorf("%w: duplicate legacy plugin %q", ErrInvalidInput, plugin.Code)
		}
		seen[plugin.Code] = struct{}{}
		if _, err := normalizeTrustedPluginConfig(plugin.Code, plugin.Config); err != nil {
			return fmt.Errorf("%w: invalid legacy plugin config for %q", ErrInvalidInput, plugin.Code)
		}
	}
	return nil
}

func (s *Store) LookupLegacyTrustedPluginsImport(ctx context.Context, sourceSHA256 string) (LegacyTrustedPluginsImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyTrustedPluginsImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyTrustedPluginsImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyTrustedPluginsImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyTrustedPluginsImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice=? AND source_sha256=?`, LegacyTrustedPluginsSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyTrustedPluginsImportReport{}, false, nil
	}
	if err != nil {
		return LegacyTrustedPluginsImportReport{}, false, fmt.Errorf("lookup legacy trusted plugins migration: %w", err)
	}
	var report LegacyTrustedPluginsImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyTrustedPluginsImportReport{}, false, fmt.Errorf("decode legacy trusted plugins migration report: %w", err)
	}
	if err := validateLegacyTrustedPluginsImportReport(report, sourceSHA256); err != nil {
		return LegacyTrustedPluginsImportReport{}, false, fmt.Errorf("validate legacy trusted plugins migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyTrustedPlugins(ctx context.Context, input LegacyTrustedPluginsImport, now time.Time) (LegacyTrustedPluginsImportReport, error) {
	if err := validateLegacyTrustedPluginsImport(input); err != nil || now.UTC().Unix() < 0 {
		if err != nil {
			return LegacyTrustedPluginsImportReport{}, err
		}
		return LegacyTrustedPluginsImportReport{}, ErrInvalidInput
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyTrustedPluginsImportReport{}, fmt.Errorf("begin legacy trusted plugins import: %w", err)
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil || version != CurrentSchemaVersion() {
		return LegacyTrustedPluginsImportReport{}, fmt.Errorf("legacy trusted plugins import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyTrustedPluginsImportReport{}, fmt.Errorf("validate legacy trusted plugins target schema: %w", err)
	}
	if existing, found, err := lookupLegacyTrustedPluginsImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyTrustedPluginsImportReport{}, err
	} else if found {
		if existing.Slice != input.Slice || existing.SourceSHA256 != input.SourceSHA256 || existing.SourceSize != input.SourceSize ||
			existing.RollbackBackupPath != input.RollbackBackupPath || existing.RollbackBackupSHA256 != input.RollbackBackupSHA256 ||
			existing.Plugins.SourceRows != len(input.Plugins) || existing.Plugins.TargetRows != len(input.Plugins) ||
			existing.Plugins.SourceChecksum != input.Checksum || !validLowerSHA256(existing.Plugins.TargetChecksum) {
			return LegacyTrustedPluginsImportReport{}, fmt.Errorf("%w: legacy trusted plugins migration ledger does not match the source", ErrConflict)
		}
		plugins, _, err := readTrustedPluginMigrationTarget(ctx, tx)
		if err != nil {
			return LegacyTrustedPluginsImportReport{}, err
		}
		if LegacyTrustedPluginsChecksum(plugins) != existing.Plugins.TargetChecksum {
			return LegacyTrustedPluginsImportReport{}, fmt.Errorf("%w: imported legacy trusted plugins no longer match their migration ledger", ErrConflict)
		}
		if err := tx.Commit(); err != nil {
			return LegacyTrustedPluginsImportReport{}, err
		}
		return existing, nil
	}
	var runs int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyTrustedPluginsSlice).Scan(&runs); err != nil {
		return LegacyTrustedPluginsImportReport{}, err
	}
	if runs != 0 {
		return LegacyTrustedPluginsImportReport{}, fmt.Errorf("%w: legacy trusted plugins were already imported from another snapshot", ErrConflict)
	}
	current, metadata, err := readTrustedPluginMigrationTarget(ctx, tx)
	if err != nil {
		return LegacyTrustedPluginsImportReport{}, err
	}
	if LegacyTrustedPluginsChecksum(current) != LegacyTrustedPluginsChecksum(defaultTrustedPluginMigrationData()) {
		return LegacyTrustedPluginsImportReport{}, fmt.Errorf("%w: legacy trusted plugins import requires pristine plugin configuration", ErrConflict)
	}
	for _, meta := range metadata {
		if meta.revision != 1 || meta.updatedBy.Valid || meta.updatedAt != 0 {
			return LegacyTrustedPluginsImportReport{}, fmt.Errorf("%w: legacy trusted plugins import requires pristine plugin metadata", ErrConflict)
		}
	}

	ordered := append([]LegacyTrustedPlugin(nil), input.Plugins...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Code < ordered[right].Code })
	for _, plugin := range ordered {
		configJSON, err := normalizeTrustedPluginConfig(plugin.Code, plugin.Config)
		if err != nil {
			return LegacyTrustedPluginsImportReport{}, err
		}
		result, err := tx.ExecContext(ctx, `
			UPDATE trusted_plugins
			SET enabled=?,config_json=?,revision=revision+1,updated_by=NULL,updated_at=?
			WHERE code=? AND revision=1 AND updated_by IS NULL AND updated_at=0
		`, plugin.Enabled, string(configJSON), now.UTC().Unix(), plugin.Code)
		if err != nil {
			return LegacyTrustedPluginsImportReport{}, fmt.Errorf("write legacy trusted plugin %q: %w", plugin.Code, err)
		}
		if changed, err := result.RowsAffected(); err != nil || changed != 1 {
			return LegacyTrustedPluginsImportReport{}, fmt.Errorf("%w: trusted plugin %q changed during import", ErrConflict, plugin.Code)
		}
	}
	for _, plugin := range ordered {
		if plugin.Code == TrustedPluginTelegram && !plugin.Enabled {
			if err := cancelPendingTelegramMessagesTx(ctx, tx, "cancelled because Telegram plugin was disabled by legacy migration", now.UTC()); err != nil {
				return LegacyTrustedPluginsImportReport{}, err
			}
			break
		}
	}

	target, targetMetadata, err := readTrustedPluginMigrationTarget(ctx, tx)
	if err != nil {
		return LegacyTrustedPluginsImportReport{}, err
	}
	for _, meta := range targetMetadata {
		if meta.revision != 2 || meta.updatedBy.Valid || meta.updatedAt != now.UTC().Unix() {
			return LegacyTrustedPluginsImportReport{}, errors.New("legacy trusted plugin target metadata verification failed")
		}
	}
	report := LegacyTrustedPluginsImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Plugins: LegacyDomainResult{
			SourceRows: len(input.Plugins), TargetRows: len(target), SourceChecksum: input.Checksum,
			TargetChecksum: LegacyTrustedPluginsChecksum(target),
		},
		AppliedAt: now.UTC(),
	}
	if report.Plugins.SourceChecksum != report.Plugins.TargetChecksum {
		return LegacyTrustedPluginsImportReport{}, errors.New("legacy trusted plugins target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyTrustedPluginsImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at)
		VALUES(?,?,?,?,?,?,?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyTrustedPluginsImportReport{}, fmt.Errorf("record legacy trusted plugins migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyTrustedPluginsImportReport{}, err
	}
	return report, nil
}

type trustedPluginMigrationMetadata struct {
	revision  int64
	updatedBy sql.NullInt64
	updatedAt int64
}

func readTrustedPluginMigrationTarget(ctx context.Context, database interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) ([]LegacyTrustedPlugin, []trustedPluginMigrationMetadata, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT code,name,type,version,enabled,config_json,revision,updated_by,updated_at
		FROM trusted_plugins ORDER BY code
	`)
	if err != nil {
		return nil, nil, fmt.Errorf("read trusted plugin migration target: %w", err)
	}
	defer rows.Close()
	plugins := make([]LegacyTrustedPlugin, 0, len(trustedPluginMigrationDefinitions))
	metadata := make([]trustedPluginMigrationMetadata, 0, len(trustedPluginMigrationDefinitions))
	for rows.Next() {
		var plugin LegacyTrustedPlugin
		var name, configJSON string
		var enabled int
		var meta trustedPluginMigrationMetadata
		if err := rows.Scan(&plugin.Code, &name, &plugin.Type, &plugin.Version, &enabled, &configJSON, &meta.revision, &meta.updatedBy, &meta.updatedAt); err != nil {
			return nil, nil, fmt.Errorf("scan trusted plugin migration target: %w", err)
		}
		definition, trusted := trustedPluginMigrationDefinitions[plugin.Code]
		if !trusted || name != definition.name || enabled < 0 || enabled > 1 {
			return nil, nil, errors.New("trusted plugin migration target contains an untrusted identity")
		}
		plugin.Enabled = enabled == 1
		if err := json.Unmarshal([]byte(configJSON), &plugin.Config); err != nil {
			return nil, nil, fmt.Errorf("decode trusted plugin migration target config: %w", err)
		}
		plugins = append(plugins, plugin)
		metadata = append(metadata, meta)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("iterate trusted plugin migration target: %w", err)
	}
	if err := ValidateLegacyTrustedPluginsData(plugins); err != nil {
		return nil, nil, fmt.Errorf("validate trusted plugin migration target: %w", err)
	}
	return plugins, metadata, nil
}

func defaultTrustedPluginMigrationData() []LegacyTrustedPlugin {
	plugins := make([]LegacyTrustedPlugin, 0, len(trustedPluginMigrationDefinitions))
	for code, definition := range trustedPluginMigrationDefinitions {
		config := map[string]any{}
		if code == TrustedPluginTelegram {
			config = map[string]any{
				"enable_ticket_notify":  true,
				"enable_payment_notify": true,
				"start_welcome_title":   "🎉 欢迎使用 XBoard Telegram Bot！",
				"start_bot_description": "🤖 我是您的专属助手，可以帮助您：\\n• 绑定您的 XBoard 账号\\n• 查看流量使用情况\\n• 获取最新订阅链接\\n• 管理账号绑定状态",
				"start_bind_guide":      "🔗 请先绑定您的 XBoard 账号：\\n1. 登录您的 XBoard 账户\\n2. 复制您的订阅链接\\n3. 发送 /bind + 订阅链接",
				"start_unbind_guide":    "📋 可用命令：\\n/traffic - 查看流量使用情况\\n/getlatesturl - 获取订阅链接\\n/unbind - 解绑账号",
				"start_bind_commands":   "📋 可用命令：\\n/bind [订阅链接] - 绑定账号",
				"start_footer":          "💡 提示：所有命令都需要在私聊中使用",
				"help_text":             "请使用以下命令：\\n/bind - 绑定账号\\n/traffic - 查看流量\\n/getlatesturl - 获取最新链接",
			}
		}
		plugins = append(plugins, LegacyTrustedPlugin{Code: code, Type: definition.pluginType, Version: definition.version, Enabled: true, Config: config})
	}
	return plugins
}

func validateLegacyTrustedPluginsImport(input LegacyTrustedPluginsImport) error {
	if err := ValidateLegacyTrustedPluginsData(input.Plugins); err != nil {
		return err
	}
	if input.Slice != LegacyTrustedPluginsSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 ||
		input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(input.RollbackBackupSHA256) || input.Checksum != LegacyTrustedPluginsChecksum(input.Plugins) {
		return fmt.Errorf("%w: invalid legacy trusted plugins import", ErrInvalidInput)
	}
	return nil
}

func validateLegacyTrustedPluginsImportReport(report LegacyTrustedPluginsImportReport, sourceSHA256 string) error {
	if report.AlreadyApplied || report.Slice != LegacyTrustedPluginsSlice || report.SourceSHA256 != sourceSHA256 ||
		!validLowerSHA256(report.SourceSHA256) || report.SourceSize < 1 ||
		report.RollbackBackupPath == "" || len(report.RollbackBackupPath) > 4096 || !utf8.ValidString(report.RollbackBackupPath) || strings.IndexFunc(report.RollbackBackupPath, unicode.IsControl) >= 0 ||
		!validLowerSHA256(report.RollbackBackupSHA256) || report.Plugins.SourceRows != len(trustedPluginMigrationDefinitions) || report.Plugins.TargetRows != len(trustedPluginMigrationDefinitions) ||
		!validLowerSHA256(report.Plugins.SourceChecksum) || !validLowerSHA256(report.Plugins.TargetChecksum) || report.AppliedAt.IsZero() || report.AppliedAt.UTC().Unix() < 0 {
		return fmt.Errorf("%w: invalid legacy trusted plugins migration report", ErrConflict)
	}
	return nil
}
