package legacymigration

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestReadCommissionPolicySettingsSnapshotPreservesExplicitValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-commission-policy.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	_, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);
		INSERT INTO v2_settings(name,value) VALUES
		('invite_commission','25'),('commission_first_time_enable','0'),
		('commission_auto_check_enable','false'),('withdraw_close_enable','1'),
		('commission_distribution_enable','true'),('commission_distribution_l1','50'),
		('commission_distribution_l2','30'),('commission_distribution_l3','20'),
		('unrelated_large_value',zeroblob(1048576))`)
	if err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	snapshot, err := ReadCommissionPolicySettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	settings := snapshot.Settings
	if settings.InviteCommission != 25 || settings.FirstTimeEnabled || settings.AutoCheckEnabled ||
		!settings.WithdrawClosed || !settings.DistributionEnabled || settings.DistributionL1 != 50 ||
		settings.DistributionL2 != 30 || settings.DistributionL3 != 20 {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Checksum != store.LegacyCommissionPolicySettingsChecksum(settings) {
		t.Fatalf("checksum=%q", snapshot.Checksum)
	}
}

func TestReadCommissionPolicySettingsSnapshotUsesLegacySourceDefaultsAndDistinguishesNullFromEmpty(t *testing.T) {
	for name, rows := range map[string]string{
		"missing": "",
		"null and empty boolean": `INSERT INTO v2_settings(name,value) VALUES
			('invite_commission',NULL),('commission_first_time_enable',NULL),
			('commission_auto_check_enable',''),('withdraw_close_enable',''),
			('commission_distribution_enable',NULL),('commission_distribution_l1',NULL)`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy-commission-policy-defaults.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			snapshot, err := ReadCommissionPolicySettingsSnapshot(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			want := store.DefaultLegacyCommissionPolicySettings()
			if name == "null and empty boolean" {
				want.AutoCheckEnabled = false
			}
			if snapshot.Settings != want {
				t.Fatalf("settings=%#v want=%#v", snapshot.Settings, want)
			}
		})
	}
}

func TestReadCommissionPolicySettingsSnapshotRejectsInvalidOrUnboundedData(t *testing.T) {
	for name, rows := range map[string]string{
		"duplicate":             `('invite_commission','10'),('invite_commission','20')`,
		"invalid boolean":       `('commission_auto_check_enable','yes')`,
		"empty integer":         `('invite_commission','')`,
		"fraction":              `('commission_distribution_l1','10.5')`,
		"negative":              `('commission_distribution_l2','-1')`,
		"above one hundred":     `('invite_commission','101')`,
		"sum above one hundred": `('commission_distribution_l1','50'),('commission_distribution_l2','30'),('commission_distribution_l3','21')`,
		"oversized":             `('commission_distribution_l3',zeroblob(17000))`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-commission-policy.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);
				INSERT INTO v2_settings(name,value) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadCommissionPolicySettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("invalid legacy commission policy settings were accepted")
			}
		})
	}
}

func BenchmarkReadCommissionPolicySettingsSnapshot(b *testing.B) {
	path := filepath.Join(b.TempDir(), "legacy-commission-policy-benchmark.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	_, _ = database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);
		INSERT INTO v2_settings(name,value) VALUES
		('invite_commission','25'),('commission_first_time_enable','0'),
		('commission_auto_check_enable','0'),('withdraw_close_enable','1'),
		('commission_distribution_enable','1'),('commission_distribution_l1','50'),
		('commission_distribution_l2','30'),('commission_distribution_l3','20')`)
	_ = database.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := ReadCommissionPolicySettingsSnapshot(b.Context(), path); err != nil {
			b.Fatal(err)
		}
	}
}
