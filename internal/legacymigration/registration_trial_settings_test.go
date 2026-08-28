package legacymigration

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestReadRegistrationTrialSettingsSnapshotPreservesEffectiveLegacyValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-trial-settings.db")
	database, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES ('try_out_plan_id','17'),('try_out_hour','48')`); err != nil {
		t.Fatal(err)
	}
	_ = database.Close()
	snapshot, err := ReadRegistrationTrialSettingsSnapshot(context.Background(), path)
	if err != nil || snapshot.Settings.PlanID != 17 || snapshot.Settings.Hours != 48 || len(snapshot.Checksum) != 64 {
		t.Fatalf("snapshot=%#v err=%v", snapshot, err)
	}
}

func TestReadRegistrationTrialSettingsSnapshotRejectsUnsafeEnabledDurationAndNormalizesDisabled(t *testing.T) {
	for _, testCase := range []struct {
		name, planID, hours string
		wantError           bool
	}{
		{"fractional enabled", "17", "1.5", true},
		{"negative enabled", "17", "-2", true},
		{"disabled ignores unsafe duration", "0", "-2.5", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "legacy.db")
			database, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := database.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
				INSERT INTO v2_settings(name,value) VALUES ('try_out_plan_id',?),('try_out_hour',?)`, testCase.planID, testCase.hours); err != nil {
				t.Fatal(err)
			}
			_ = database.Close()
			snapshot, err := ReadRegistrationTrialSettingsSnapshot(context.Background(), path)
			if testCase.wantError && (err == nil || !strings.Contains(err.Error(), "duration")) {
				t.Fatalf("error=%v, want duration rejection", err)
			}
			if !testCase.wantError && (err != nil || snapshot.Settings.PlanID != 0 || snapshot.Settings.Hours != 1) {
				t.Fatalf("snapshot=%#v err=%v", snapshot, err)
			}
		})
	}
}
