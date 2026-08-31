package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacySubscriptionPolicySettingsWithVerifiedBackup(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-subscription-policy.db")
	source, _ := sql.Open("sqlite", "file:"+sourcePath)
	_, err := source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);
		INSERT INTO v2_settings(name,value) VALUES
		('plan_change_enable','0'),('surplus_enable','0'),('new_order_event_id','1'),
		('renew_order_event_id','0'),('change_order_event_id','1'),
		('default_remind_expire','0'),('default_remind_traffic','1')`)
	if err != nil {
		t.Fatal(err)
	}
	_ = source.Close()

	targetPath := filepath.Join(directory, "target.db")
	target, _ := store.OpenSQLite("file:" + targetPath)
	if err := target.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = target.Close()
	rawTarget, _ := sql.Open("sqlite", "file:"+targetPath)
	if _, err := rawTarget.Exec(`UPDATE app_settings SET traffic_reset_method=4 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_ = rawTarget.Close()

	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-subscription-policy.xbbackup")
	now := time.Date(2026, 8, 31, 15, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-subscription-policy-settings", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("runCommand() handled=%t err=%v stderr=%q", handled, err, stderr.String())
	}
	var result legacySubscriptionPolicySettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Result.AlreadyApplied ||
		result.Action != "migration.import-legacy-subscription-policy-settings" ||
		result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if verified, err := backup.Verify(t.Context(), backupPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify()=(%#v,%v)", verified, err)
	}

	inspection, _ := store.OpenSQLite("file:" + targetPath)
	policy, policyErr := inspection.GetSubscriptionPolicySettings(t.Context())
	_ = inspection.Close()
	if policyErr != nil || policy.Revision != 2 || policy.PlanChangeEnabled || policy.ResetTrafficMethod != 4 ||
		policy.SurplusEnabled || policy.NewOrderEventID != 1 || policy.RenewOrderEventID != 0 || policy.ChangeOrderEventID != 1 ||
		policy.DefaultRemindExpire || !policy.DefaultRemindTraffic {
		t.Fatalf("policy=%#v err=%v", policy, policyErr)
	}

	restoredPath := filepath.Join(directory, "restored.db")
	if _, err := backup.Restore(t.Context(), backupPath, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, _ := store.OpenSQLite("file:" + restoredPath)
	restoredPolicy, restoredErr := restored.GetSubscriptionPolicySettings(t.Context())
	_, imported, lookupErr := restored.LookupLegacySubscriptionPolicySettingsImport(t.Context(), result.Source.SHA256)
	_ = restored.Close()
	defaults := store.DefaultLegacySubscriptionPolicySettings()
	if restoredErr != nil || lookupErr != nil || imported || restoredPolicy.ResetTrafficMethod != 4 ||
		restoredPolicy.PlanChangeEnabled != defaults.PlanChangeEnabled || restoredPolicy.SurplusEnabled != defaults.SurplusEnabled ||
		restoredPolicy.NewOrderEventID != defaults.NewOrderEventID || restoredPolicy.RenewOrderEventID != defaults.RenewOrderEventID ||
		restoredPolicy.ChangeOrderEventID != defaults.ChangeOrderEventID || restoredPolicy.DefaultRemindExpire != defaults.DefaultRemindExpire ||
		restoredPolicy.DefaultRemindTraffic != defaults.DefaultRemindTraffic {
		t.Fatalf("restored policy=%#v imported=%t restoredErr=%v lookupErr=%v", restoredPolicy, imported, restoredErr, lookupErr)
	}

	stdout.Reset()
	handled, err = runCommand(t.Context(), []string{
		"migration", "import-legacy-subscription-policy-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Minute) })
	if !handled || err != nil || json.Unmarshal(stdout.Bytes(), &result) != nil || !result.Result.AlreadyApplied || result.Result.AppliedAt != now {
		t.Fatalf("idempotent run handled=%t err=%v result=%#v", handled, err, result)
	}
}

func TestRunCommandRejectsSubscriptionPolicyMigrationWithoutOfflineOrBackupGate(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-subscription-policy.db")
	source, _ := sql.Open("sqlite", "file:"+sourcePath)
	_, _ = source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB)`)
	_ = source.Close()
	targetPath := filepath.Join(directory, "target.db")
	target, _ := store.OpenSQLite("file:" + targetPath)
	_ = target.Migrate(t.Context())
	_ = target.Close()
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	var stdout, stderr bytes.Buffer
	if handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-subscription-policy-settings", "--source", sourcePath,
	}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") {
		t.Fatalf("missing confirmation handled=%t err=%v", handled, err)
	}
	if handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-subscription-policy-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "backup-output") {
		t.Fatalf("missing backup handled=%t err=%v", handled, err)
	}
}
