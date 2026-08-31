package legacymigration

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestReadSubscriptionPolicySettingsSnapshotPreservesExplicitValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-subscription-policy.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	_, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);
		INSERT INTO v2_settings(name,value) VALUES
		('plan_change_enable','0'),('surplus_enable','false'),('new_order_event_id','1'),
		('renew_order_event_id','0'),('change_order_event_id','1'),
		('default_remind_expire','0'),('default_remind_traffic','true'),
		('unrelated_large_value',zeroblob(1048576))`)
	if err != nil {
		t.Fatal(err)
	}
	_ = database.Close()

	snapshot, err := ReadSubscriptionPolicySettingsSnapshot(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	settings := snapshot.Settings
	if settings.PlanChangeEnabled || settings.SurplusEnabled || settings.NewOrderEventID != 1 ||
		settings.RenewOrderEventID != 0 || settings.ChangeOrderEventID != 1 ||
		settings.DefaultRemindExpire || !settings.DefaultRemindTraffic {
		t.Fatalf("snapshot=%#v", snapshot)
	}
	if snapshot.Checksum != store.LegacySubscriptionPolicySettingsChecksum(settings) {
		t.Fatalf("checksum=%q", snapshot.Checksum)
	}
}

func TestReadSubscriptionPolicySettingsSnapshotUsesLegacyDefaultsAndDistinguishesNullFromEmpty(t *testing.T) {
	for name, rows := range map[string]string{
		"missing": "",
		"null and empty": `INSERT INTO v2_settings(name,value) VALUES
			('plan_change_enable',NULL),('surplus_enable',''),('new_order_event_id',NULL),
			('default_remind_expire',NULL),('default_remind_traffic','')`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy-subscription-policy-defaults.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			snapshot, err := ReadSubscriptionPolicySettingsSnapshot(t.Context(), path)
			if err != nil {
				t.Fatal(err)
			}
			want := store.DefaultLegacySubscriptionPolicySettings()
			if name == "null and empty" {
				want.SurplusEnabled = false
				want.DefaultRemindTraffic = false
			}
			if snapshot.Settings != want {
				t.Fatalf("settings=%#v want=%#v", snapshot.Settings, want)
			}
		})
	}
}

func TestReadSubscriptionPolicySettingsSnapshotRejectsInvalidOrUnboundedData(t *testing.T) {
	for name, rows := range map[string]string{
		"duplicate":       `('plan_change_enable','1'),('plan_change_enable','0')`,
		"invalid boolean": `('surplus_enable','yes')`,
		"unknown event":   `('new_order_event_id','2')`,
		"negative event":  `('renew_order_event_id','-1')`,
		"fraction event":  `('change_order_event_id','1.0')`,
		"oversized":       `('default_remind_expire',zeroblob(17000))`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "invalid-subscription-policy.db")
			database, _ := sql.Open("sqlite", "file:"+path)
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);
				INSERT INTO v2_settings(name,value) VALUES ` + rows); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			if _, err := ReadSubscriptionPolicySettingsSnapshot(t.Context(), path); err == nil {
				t.Fatal("invalid legacy subscription policy settings were accepted")
			}
		})
	}
}

func BenchmarkReadSubscriptionPolicySettingsSnapshot(b *testing.B) {
	path := filepath.Join(b.TempDir(), "legacy-subscription-policy-benchmark.db")
	database, _ := sql.Open("sqlite", "file:"+path)
	_, _ = database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);
		INSERT INTO v2_settings(name,value) VALUES
		('plan_change_enable','0'),('surplus_enable','0'),('new_order_event_id','1'),
		('renew_order_event_id','1'),('change_order_event_id','1'),
		('default_remind_expire','0'),('default_remind_traffic','1')`)
	_ = database.Close()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := ReadSubscriptionPolicySettingsSnapshot(b.Context(), path); err != nil {
			b.Fatal(err)
		}
	}
}
