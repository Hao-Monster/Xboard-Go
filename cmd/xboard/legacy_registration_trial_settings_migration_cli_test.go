package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacyRegistrationTrialSettingsOfflineWithRollback(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-registration-trial.db")
	source, err := sql.Open("sqlite", "file:"+sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value TEXT);
		INSERT INTO v2_settings(name,value) VALUES ('try_out_plan_id','1'),('try_out_hour','48')`); err != nil {
		t.Fatal(err)
	}
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	plan, err := target.CreatePlan(t.Context(), store.SavePlanInput{
		Name: "Imported trial plan", TransferEnableGiB: 10, Prices: store.PlanPrices{}, Tags: []string{},
	}, time.Date(2026, 8, 29, 12, 0, 0, 0, time.UTC))
	if err != nil || plan.ID != 1 {
		t.Fatalf("plan=%#v err=%v", plan, err)
	}
	_ = target.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-registration-trial.xbbackup")
	now := time.Date(2026, 8, 29, 12, 30, 0, 0, time.UTC)
	var blockedOut, blockedErr bytes.Buffer
	handled, err := runCommand(context.Background(), []string{
		"migration", "import-legacy-registration-trial-settings", "--source", sourcePath, "--backup-output", backupPath,
	}, &blockedOut, &blockedErr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") || blockedOut.Len() != 0 {
		t.Fatalf("offline gate handled=%v err=%v stdout=%q", handled, err, blockedOut.String())
	}
	var stdout, stderr bytes.Buffer
	handled, err = runCommand(context.Background(), []string{
		"migration", "import-legacy-registration-trial-settings", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil {
		t.Fatalf("runCommand() handled=%v err=%v stderr=%q", handled, err, stderr.String())
	}
	var result legacyRegistrationTrialSettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Action != "migration.import-legacy-registration-trial-settings" ||
		result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum || result.RollbackBackup.Manifest.SchemaVersion != store.CurrentSchemaVersion() {
		t.Fatalf("result=%#v", result)
	}
	target, err = store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	settings, err := target.GetSiteSettings(t.Context())
	if err != nil || settings.TrialPlanID != 1 || settings.TrialHours != 48 {
		t.Fatalf("settings=%#v err=%v", settings, err)
	}
}
