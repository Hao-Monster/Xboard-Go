package legacymigration

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadCurrencySettingsSnapshotReadsOnlyFixedKeysAndNormalizes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-currency-settings.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES
		('currency',' usd '),('currency_symbol',' $ '),('payment_secret','must-not-be-read')`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	snapshot, err := ReadCurrencySettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Settings.Currency != "USD" || snapshot.Settings.CurrencySymbol != "$" || snapshot.Checksum == "" || snapshot.SHA256 == "" || snapshot.Size < 1 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
}

func TestReadCurrencySettingsSnapshotDefaultsMissingKeysAndRejectsUnsafeData(t *testing.T) {
	defaultPath := filepath.Join(t.TempDir(), "legacy-currency-defaults.db")
	defaultDB, _ := sql.Open("sqlite", "file:"+defaultPath)
	_, _ = defaultDB.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT)`)
	_ = defaultDB.Close()
	defaults, err := ReadCurrencySettingsSnapshot(t.Context(), defaultPath)
	if err != nil || defaults.Settings.Currency != "CNY" || defaults.Settings.CurrencySymbol != "¥" {
		t.Fatalf("defaults=%#v err=%v", defaults, err)
	}

	for name, rows := range map[string]string{
		"duplicate":    `('currency','USD'),('currency','EUR')`,
		"invalid code": `('currency','US')`,
		"control":      `('currency_symbol','bad` + "\n" + `value')`,
		"oversized":    `('currency_symbol','` + strings.Repeat("a", 17) + `')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-currency-settings.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT); INSERT INTO v2_settings(name,value) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadCurrencySettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("invalid currency settings snapshot was accepted")
			}
		})
	}
}
