package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	_ "modernc.org/sqlite"
)

func TestRunCommandImportsLegacyCommissionPolicySettingsWithVerifiedBackup(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-commission-policy.db")
	source, _ := sql.Open("sqlite", "file:"+sourcePath)
	_, err := source.Exec(`CREATE TABLE v2_settings(id INTEGER PRIMARY KEY,name TEXT,value BLOB);
		INSERT INTO v2_settings(name,value) VALUES
		('invite_commission','25'),('commission_first_time_enable','0'),
		('commission_auto_check_enable','0'),('withdraw_close_enable','1'),
		('commission_distribution_enable','1'),('commission_distribution_l1','50'),
		('commission_distribution_l2','30'),('commission_distribution_l3','20')`)
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
	if _, err := rawTarget.Exec(`UPDATE app_settings SET invite_force=1,commission_withdraw_limit=25050,
		commission_withdraw_method='["USDT"]',plan_change_enable=0 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	_ = rawTarget.Close()

	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	backupPath := filepath.Join(directory, "pre-commission-policy.xbbackup")
	now := time.Date(2026, 8, 31, 17, 0, 0, 0, time.UTC)
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-commission-policy-settings", "--source", sourcePath,
		"--backup-output", backupPath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now })
	if !handled || err != nil || stderr.Len() != 0 {
		t.Fatalf("runCommand() handled=%t err=%v stderr=%q", handled, err, stderr.String())
	}
	var result legacyCommissionPolicySettingsMigrationCommandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Result.AlreadyApplied ||
		result.Action != "migration.import-legacy-commission-policy-settings" ||
		result.Result.Settings.SourceChecksum != result.Result.Settings.TargetChecksum {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if verified, err := backup.Verify(t.Context(), backupPath); err != nil || verified != result.RollbackBackup.Manifest {
		t.Fatalf("Verify()=(%#v,%v)", verified, err)
	}

	inspection, _ := store.OpenSQLite("file:" + targetPath)
	commission, commissionErr := inspection.GetCommissionSettings(t.Context())
	_ = inspection.Close()
	if commissionErr != nil || commission.Revision != 2 || commission.InviteCommission != 25 ||
		commission.FirstTimeEnabled || commission.AutoCheckEnabled || !commission.WithdrawClosed ||
		!commission.DistributionEnabled || commission.DistributionL1 != 50 || commission.DistributionL2 != 30 ||
		commission.DistributionL3 != 20 || commission.WithdrawLimit != 25050 ||
		!reflect.DeepEqual(commission.WithdrawMethods, []string{"USDT"}) {
		t.Fatalf("commission=%#v err=%v", commission, commissionErr)
	}

	restoredPath := filepath.Join(directory, "restored.db")
	if _, err := backup.Restore(t.Context(), backupPath, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, _ := store.OpenSQLite("file:" + restoredPath)
	restoredCommission, restoredErr := restored.GetCommissionSettings(t.Context())
	_, imported, lookupErr := restored.LookupLegacyCommissionPolicySettingsImport(t.Context(), result.Source.SHA256)
	_ = restored.Close()
	if restoredErr != nil || lookupErr != nil || imported || restoredCommission.Revision != 1 ||
		restoredCommission.InviteCommission != 10 || !restoredCommission.FirstTimeEnabled ||
		!restoredCommission.AutoCheckEnabled || restoredCommission.WithdrawClosed || restoredCommission.DistributionEnabled ||
		restoredCommission.DistributionL1 != 100 || restoredCommission.DistributionL2 != 0 || restoredCommission.DistributionL3 != 0 ||
		restoredCommission.WithdrawLimit != 25050 || !reflect.DeepEqual(restoredCommission.WithdrawMethods, []string{"USDT"}) {
		t.Fatalf("restored commission=%#v imported=%t restoredErr=%v lookupErr=%v", restoredCommission, imported, restoredErr, lookupErr)
	}

	stdout.Reset()
	handled, err = runCommand(t.Context(), []string{
		"migration", "import-legacy-commission-policy-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, func() time.Time { return now.Add(time.Minute) })
	if !handled || err != nil || json.Unmarshal(stdout.Bytes(), &result) != nil || !result.Result.AlreadyApplied || result.Result.AppliedAt != now {
		t.Fatalf("idempotent run handled=%t err=%v result=%#v", handled, err, result)
	}
}

func TestRunCommandRejectsCommissionPolicyMigrationWithoutOfflineOrBackupGate(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy-commission-policy.db")
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
		"migration", "import-legacy-commission-policy-settings", "--source", sourcePath,
	}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "confirm-offline") {
		t.Fatalf("missing confirmation handled=%t err=%v", handled, err)
	}
	if handled, err := runCommand(t.Context(), []string{
		"migration", "import-legacy-commission-policy-settings", "--source", sourcePath, "--confirm-offline",
	}, &stdout, &stderr, time.Now); !handled || err == nil || !strings.Contains(err.Error(), "backup-output") {
		t.Fatalf("missing backup handled=%t err=%v", handled, err)
	}
}
