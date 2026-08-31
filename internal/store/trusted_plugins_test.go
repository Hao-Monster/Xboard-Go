package store

import (
	"context"
	"testing"
	"time"
)

func TestSchemaV55SeedsOnlyTrustedCorePlugins(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()

	var version int
	if err := database.db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 58 {
		t.Fatalf("schema version = %d, want 58", version)
	}

	rows, err := database.db.QueryContext(ctx, `
		SELECT code, type, version, enabled, revision, config_json
		FROM trusted_plugins ORDER BY type, code
	`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type pluginRow struct {
		code, pluginType, version, config string
		enabled, revision                 int64
	}
	plugins := make([]pluginRow, 0, 7)
	for rows.Next() {
		var plugin pluginRow
		if err := rows.Scan(&plugin.code, &plugin.pluginType, &plugin.version, &plugin.enabled, &plugin.revision, &plugin.config); err != nil {
			t.Fatal(err)
		}
		plugins = append(plugins, plugin)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(plugins) != 7 {
		t.Fatalf("trusted plugin count = %d, want 7: %#v", len(plugins), plugins)
	}
	wantCodes := []string{"telegram", "alipay_f2f", "btcpay", "coin_payments", "coinbase", "epay", "mgate"}
	for index, want := range wantCodes {
		if plugins[index].code != want || plugins[index].enabled != 1 || plugins[index].revision != 1 {
			t.Fatalf("plugin[%d] = %#v, want enabled %q revision 1", index, plugins[index], want)
		}
	}
	if plugins[0].pluginType != "feature" || plugins[0].version != "1.0.1" || plugins[0].config == "{}" {
		t.Fatalf("telegram seed = %#v", plugins[0])
	}
	for _, plugin := range plugins[1:] {
		if plugin.pluginType != "payment" || plugin.version != "1.0.0" || plugin.config != "{}" {
			t.Fatalf("payment plugin seed = %#v", plugin)
		}
	}

	for _, statement := range []string{
		`INSERT INTO trusted_plugins(code,name,type,version,enabled,config_json,revision,updated_at) VALUES ('shell','Shell','feature','1.0.0',1,'{}',1,0)`,
		`UPDATE trusted_plugins SET config_json='{"unknown":true}' WHERE code='telegram'`,
		`UPDATE trusted_plugins SET config_json='{"secret":"leak"}' WHERE code='epay'`,
	} {
		if _, err := database.db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("unsafe trusted plugin statement succeeded: %s", statement)
		}
	}
	if _, err := database.db.ExecContext(ctx, `DELETE FROM trusted_plugins WHERE code='telegram'`); err != nil {
		t.Fatal(err)
	}
	if plugins, err := database.ListTrustedPlugins(ctx); err == nil || plugins != nil {
		t.Fatalf("incomplete trusted inventory ListTrustedPlugins()=(%#v,%v), want fail closed", plugins, err)
	}
}

func TestTrustedPluginsUseRevisionCASAndRejectUnknownCodes(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	administrator, err := database.CreateAdminUser(ctx, CreateAdminUserInput{
		Email: "plugin-admin@example.test", PasswordHash: "hash", IsAdmin: true,
	}, time.Unix(1_800_000_000, 0))
	if err != nil {
		t.Fatal(err)
	}

	plugins, err := database.ListTrustedPlugins(ctx)
	if err != nil || len(plugins) != 7 {
		t.Fatalf("ListTrustedPlugins() = (%#v, %v)", plugins, err)
	}
	telegram := plugins[0]
	updated, err := database.UpdateTrustedPlugin(ctx, administrator.ID, telegram.Code, telegram.Revision, SaveTrustedPluginInput{
		Enabled: false, Config: telegram.Config,
	}, time.Unix(1_800_000_100, 0))
	if err != nil || updated.Enabled || updated.Revision != telegram.Revision+1 {
		t.Fatalf("UpdateTrustedPlugin() = (%#v, %v)", updated, err)
	}
	if _, err := database.UpdateTrustedPlugin(ctx, administrator.ID, telegram.Code, telegram.Revision, SaveTrustedPluginInput{Enabled: true, Config: telegram.Config}, time.Unix(1_800_000_101, 0)); err != ErrRevisionConflict {
		t.Fatalf("stale update error = %v, want ErrRevisionConflict", err)
	}
	if _, err := database.UpdateTrustedPlugin(ctx, administrator.ID, "shell", 1, SaveTrustedPluginInput{Enabled: true, Config: map[string]any{}}, time.Unix(1_800_000_102, 0)); err != ErrNotFound {
		t.Fatalf("unknown plugin update error = %v, want ErrNotFound", err)
	}
	if enabled, err := database.TrustedPluginEnabled(ctx, "telegram"); err != nil || enabled {
		t.Fatalf("TrustedPluginEnabled(telegram) = (%t, %v)", enabled, err)
	}
}

func TestSchemaV55ReplacesAnUnversionedPluginSurfaceInsteadOfTrustingIt(t *testing.T) {
	database := newTestStore(t)
	ctx := context.Background()
	if _, err := database.db.ExecContext(ctx, `
		DROP TABLE trusted_plugins;
		CREATE TABLE trusted_plugins (
			code TEXT PRIMARY KEY, name TEXT, type TEXT, version TEXT, enabled INTEGER,
			config_json TEXT, revision INTEGER, updated_by INTEGER, updated_at INTEGER
		);
		INSERT INTO trusted_plugins VALUES ('shell','Shell','feature','9.9.9',1,'{"command":"rm"}',1,NULL,0);
		PRAGMA user_version = 54;
	`); err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("Migrate(unversioned plugin surface) error = %v", err)
	}
	plugins, err := database.ListTrustedPlugins(ctx)
	if err != nil || len(plugins) != 7 {
		t.Fatalf("trusted plugin catalog after migration = (%#v, %v)", plugins, err)
	}
	for _, plugin := range plugins {
		if plugin.Code == "shell" {
			t.Fatal("unversioned executable plugin survived schema v55 migration")
		}
	}
}
