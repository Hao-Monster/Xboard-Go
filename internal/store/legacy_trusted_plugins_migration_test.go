package store

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"
)

func TestImportLegacyTrustedPluginsIsVerifiedAtomicAndIdempotent(t *testing.T) {
	database := newTestStore(t)
	plugins := legacyTrustedPluginsForTest()
	for index := range plugins {
		switch plugins[index].Code {
		case TrustedPluginTelegram:
			plugins[index].Enabled = false
			plugins[index].Config["enable_ticket_notify"] = false
			plugins[index].Config["help_text"] = "迁移后的帮助"
		case TrustedPluginEPay:
			plugins[index].Enabled = false
		}
	}
	now := time.Date(2026, 8, 31, 22, 0, 0, 0, time.UTC)
	if _, err := database.db.ExecContext(t.Context(), `
		INSERT INTO telegram_message_outbox(source_kind,source_id,chat_id,text,available_at,created_at,updated_at)
		VALUES('command',9876,12345,'pending before migration',?,?,?)
	`, now.Unix(), now.Unix(), now.Unix()); err != nil {
		t.Fatal(err)
	}
	input := LegacyTrustedPluginsImport{
		Slice: LegacyTrustedPluginsSlice, SourceSHA256: strings.Repeat("a", 64), SourceSize: 4096,
		Plugins: plugins, Checksum: LegacyTrustedPluginsChecksum(plugins),
		RollbackBackupPath: "pre-trusted-plugins.xbbackup", RollbackBackupSHA256: strings.Repeat("b", 64),
	}
	report, err := database.ImportLegacyTrustedPlugins(context.Background(), input, now)
	if err != nil || report.Plugins.SourceRows != 7 || report.Plugins.TargetRows != 7 || report.Plugins.SourceChecksum != report.Plugins.TargetChecksum {
		t.Fatalf("ImportLegacyTrustedPlugins()=(%#v,%v)", report, err)
	}
	imported, err := database.ListTrustedPlugins(context.Background())
	if err != nil || len(imported) != 7 {
		t.Fatalf("ListTrustedPlugins()=(%#v,%v)", imported, err)
	}
	for _, plugin := range imported {
		if plugin.Revision != 2 || !plugin.UpdatedAt.Equal(now) {
			t.Fatalf("imported plugin metadata=%#v", plugin)
		}
		switch plugin.Code {
		case TrustedPluginTelegram:
			if plugin.Enabled || plugin.Config["help_text"] != "迁移后的帮助" || plugin.Config["enable_ticket_notify"] != false {
				t.Fatalf("imported Telegram=%#v", plugin)
			}
		case TrustedPluginEPay:
			if plugin.Enabled {
				t.Fatalf("imported EPay=%#v", plugin)
			}
		default:
			if !plugin.Enabled || len(plugin.Config) != 0 {
				t.Fatalf("imported payment=%#v", plugin)
			}
		}
	}
	var cancelled bool
	if err := database.db.QueryRowContext(t.Context(), `SELECT cancelled_at IS NOT NULL FROM telegram_message_outbox WHERE source_id=9876`).Scan(&cancelled); err != nil || !cancelled {
		t.Fatalf("pending Telegram cancellation=(%t,%v)", cancelled, err)
	}
	repeated, err := database.ImportLegacyTrustedPlugins(context.Background(), input, now.Add(time.Hour))
	if err != nil || !repeated.AlreadyApplied || !repeated.AppliedAt.Equal(now) {
		t.Fatalf("idempotent import=(%#v,%v)", repeated, err)
	}
	if _, err := database.db.ExecContext(t.Context(), `UPDATE trusted_plugins SET enabled=1 WHERE code='telegram'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ImportLegacyTrustedPlugins(context.Background(), input, now.Add(2*time.Hour)); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted idempotent import error=%v, want ErrConflict", err)
	}
}

func TestImportLegacyTrustedPluginsRejectsTargetDriftDifferentSourceAndInvalidConfig(t *testing.T) {
	plugins := legacyTrustedPluginsForTest()
	input := LegacyTrustedPluginsImport{
		Slice: LegacyTrustedPluginsSlice, SourceSHA256: strings.Repeat("c", 64), SourceSize: 4096,
		Plugins: plugins, Checksum: LegacyTrustedPluginsChecksum(plugins),
		RollbackBackupPath: "pre-trusted-plugins.xbbackup", RollbackBackupSHA256: strings.Repeat("d", 64),
	}

	drifted := newTestStore(t)
	if _, err := drifted.db.ExecContext(t.Context(), `UPDATE trusted_plugins SET enabled=0 WHERE code='epay'`); err != nil {
		t.Fatal(err)
	}
	if _, err := drifted.ImportLegacyTrustedPlugins(t.Context(), input, time.Unix(1, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("drifted target import error=%v, want ErrConflict", err)
	}
	var runs, revisions int
	if err := drifted.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyTrustedPluginsSlice).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err := drifted.db.QueryRowContext(t.Context(), `SELECT SUM(revision) FROM trusted_plugins`).Scan(&revisions); err != nil || runs != 0 || revisions != 7 {
		t.Fatalf("rejected import mutated target runs=%d revisions=%d err=%v", runs, revisions, err)
	}

	clean := newTestStore(t)
	if _, err := clean.ImportLegacyTrustedPlugins(t.Context(), input, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	different := input
	different.SourceSHA256 = strings.Repeat("e", 64)
	if _, err := clean.ImportLegacyTrustedPlugins(t.Context(), different, time.Unix(3, 0)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different source import error=%v, want ErrConflict", err)
	}

	for name, mutate := range map[string]func([]LegacyTrustedPlugin){
		"unknown Telegram field": func(values []LegacyTrustedPlugin) {
			for index := range values {
				if values[index].Code == TrustedPluginTelegram {
					values[index].Config["unsafe"] = true
				}
			}
		},
		"payment secret": func(values []LegacyTrustedPlugin) {
			for index := range values {
				if values[index].Code == TrustedPluginEPay {
					values[index].Config["secret"] = "must-not-migrate"
				}
			}
		},
		"unknown code": func(values []LegacyTrustedPlugin) { values[0].Code = "shell" },
	} {
		t.Run(name, func(t *testing.T) {
			values := legacyTrustedPluginsForTest()
			mutate(values)
			invalid := input
			invalid.Plugins = values
			invalid.Checksum = LegacyTrustedPluginsChecksum(values)
			if _, err := newTestStore(t).ImportLegacyTrustedPlugins(t.Context(), invalid, time.Unix(4, 0)); !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("invalid import error=%v, want ErrInvalidInput", err)
			}
		})
	}
	nonJSON := input
	nonJSON.Plugins = legacyTrustedPluginsForTest()
	for index := range nonJSON.Plugins {
		if nonJSON.Plugins[index].Code == TrustedPluginTelegram {
			nonJSON.Plugins[index].Config["help_text"] = math.NaN()
		}
	}
	if _, err := newTestStore(t).ImportLegacyTrustedPlugins(t.Context(), nonJSON, time.Unix(5, 0)); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("non-JSON config import error=%v, want ErrInvalidInput", err)
	}
}

func TestImportLegacyTrustedPluginsRollsBackPartialWrites(t *testing.T) {
	database := newTestStore(t)
	if _, err := database.db.ExecContext(t.Context(), `
		CREATE TRIGGER trusted_plugins_test_abort
		BEFORE UPDATE ON trusted_plugins WHEN NEW.code='coinbase'
		BEGIN SELECT RAISE(ABORT,'forced trusted plugin failure'); END
	`); err != nil {
		t.Fatal(err)
	}
	plugins := legacyTrustedPluginsForTest()
	input := LegacyTrustedPluginsImport{
		Slice: LegacyTrustedPluginsSlice, SourceSHA256: strings.Repeat("f", 64), SourceSize: 4096,
		Plugins: plugins, Checksum: LegacyTrustedPluginsChecksum(plugins),
		RollbackBackupPath: "pre-trusted-plugins.xbbackup", RollbackBackupSHA256: strings.Repeat("1", 64),
	}
	if _, err := database.ImportLegacyTrustedPlugins(t.Context(), input, time.Unix(5, 0)); err == nil {
		t.Fatal("forced write failure was accepted")
	}
	var revisions, runs int
	if err := database.db.QueryRowContext(t.Context(), `SELECT SUM(revision) FROM trusted_plugins`).Scan(&revisions); err != nil {
		t.Fatal(err)
	}
	if err := database.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, LegacyTrustedPluginsSlice).Scan(&runs); err != nil || revisions != 7 || runs != 0 {
		t.Fatalf("partial import was not rolled back revisions=%d runs=%d err=%v", revisions, runs, err)
	}
}

func TestLookupLegacyTrustedPluginsImportRejectsCorruptLedger(t *testing.T) {
	database := newTestStore(t)
	plugins := legacyTrustedPluginsForTest()
	sourceSHA := strings.Repeat("2", 64)
	input := LegacyTrustedPluginsImport{
		Slice: LegacyTrustedPluginsSlice, SourceSHA256: sourceSHA, SourceSize: 4096,
		Plugins: plugins, Checksum: LegacyTrustedPluginsChecksum(plugins),
		RollbackBackupPath: "pre-trusted-plugins.xbbackup", RollbackBackupSHA256: strings.Repeat("3", 64),
	}
	if _, err := database.ImportLegacyTrustedPlugins(t.Context(), input, time.Unix(6, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.db.ExecContext(t.Context(), `
		UPDATE legacy_migration_runs
		SET report_json=json_set(report_json,'$.rollback_backup_path',char(10) || 'unsafe')
		WHERE slice=? AND source_sha256=?
	`, LegacyTrustedPluginsSlice, sourceSHA); err != nil {
		t.Fatal(err)
	}
	if _, found, err := database.LookupLegacyTrustedPluginsImport(t.Context(), sourceSHA); err == nil || found {
		t.Fatalf("corrupt ledger lookup=(found=%t,error=%v)", found, err)
	}
}

func legacyTrustedPluginsForTest() []LegacyTrustedPlugin {
	return defaultTrustedPluginMigrationData()
}
