package legacymigration

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadConfigurationCompatibilitySettingsSnapshotReadsOnlyFixedKeysAndDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-configuration-compatibility.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('commission_withdraw_limit','250.50'),('commission_withdraw_method','["USDT"]'),
		('frontend_theme_sidebar','dark'),('frontend_theme_header','light'),
		('payment_secret','must-not-be-read')`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	snapshot, err := ReadConfigurationCompatibilitySettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Settings.CommissionWithdrawLimit != 25_050 || strings.Join(snapshot.Settings.CommissionWithdrawMethod, ",") != "USDT" ||
		snapshot.Settings.SidebarStyle != "dark" || snapshot.Settings.HeaderStyle != "light" || snapshot.Checksum == "" || snapshot.SHA256 == "" {
		t.Fatalf("snapshot=%#v", snapshot)
	}

	defaultPath := filepath.Join(t.TempDir(), "legacy-configuration-compatibility-defaults.db")
	defaultDatabase, _ := sql.Open("sqlite", "file:"+defaultPath)
	_, _ = defaultDatabase.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT)`)
	_ = defaultDatabase.Close()
	defaults, err := ReadConfigurationCompatibilitySettingsSnapshot(t.Context(), defaultPath)
	if err != nil || defaults.Settings.CommissionWithdrawLimit != 10_000 ||
		strings.Join(defaults.Settings.CommissionWithdrawMethod, ",") != "支付宝,USDT,Paypal" ||
		defaults.Settings.SidebarStyle != "light" || defaults.Settings.HeaderStyle != "dark" {
		t.Fatalf("defaults=%#v err=%v", defaults, err)
	}
}

func TestReadConfigurationCompatibilitySettingsSnapshotRejectsInvalidOrAmbiguousRows(t *testing.T) {
	for name, rows := range map[string]string{
		"duplicate":        `('commission_withdraw_limit','100'),('commission_withdraw_limit','200')`,
		"sub-cent limit":   `('commission_withdraw_limit','1.001')`,
		"invalid methods":  `('commission_withdraw_method','[1]')`,
		"trailing methods": `('commission_withdraw_method','["USDT"] trailing')`,
		"invalid sidebar":  `('frontend_theme_sidebar','system')`,
		"oversized":        `('commission_withdraw_method','["` + strings.Repeat("a", 9000) + `"]')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-configuration-compatibility.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			_, _ = database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
				INSERT INTO v2_settings(name,value) VALUES ` + rows)
			_ = database.Close()
			if _, err := ReadConfigurationCompatibilitySettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("invalid configuration compatibility settings were accepted")
			}
		})
	}
}
