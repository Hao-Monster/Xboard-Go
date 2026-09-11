package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/legacymigration"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestOperationalLogsCLIImportsOfflineWithFixedReplayAndRestorableBackup(t *testing.T) {
	source, target, archive, now := operationalCLIFixture(t)
	base := []string{"migration", "import-legacy-operational-logs", "--source", source, "--confirm-offline"}
	first := runOperationalCLI(t, append(append([]string{}, base...), "--backup-output", archive), now)
	if first.Action != "migration.import-legacy-operational-logs" || first.Result.AlreadyApplied || first.Result.Logs.TargetRows != 1 || first.Result.Logs.TargetChecksum != first.Result.Logs.SourceChecksum || first.Result.AsOf != now.Unix() || len(first.Result.StatisticsChecksum) != 64 {
		t.Fatalf("first migration=%#v", first)
	}
	if manifest, err := backup.Verify(t.Context(), archive); err != nil || manifest != first.RollbackBackup.Manifest {
		t.Fatalf("backup verification=%#v, %v", manifest, err)
	}
	// Advancing time beyond the original retention window must not select a new
	// slice for a retry of the exact same immutable source.
	again := runOperationalCLI(t, base, now.Add(100*24*time.Hour))
	if !again.Result.AlreadyApplied || again.Result.AsOf != first.Result.AsOf || again.Result.CutoffAt != first.Result.CutoffAt || again.Result.Logs != first.Result.Logs || again.Result.AppliedAt != first.Result.AppliedAt {
		t.Fatalf("time-shifted retry=%#v", again)
	}
	explicit := runOperationalCLI(t, append(append([]string{}, base...), "--as-of", now.Format(time.RFC3339)), now.Add(100*24*time.Hour))
	if explicit.Result.AsOf != now.Unix() || !explicit.Result.AlreadyApplied {
		t.Fatalf("explicit retry=%#v", explicit)
	}
	assertOperationalCLIError(t, append(append([]string{}, base...), "--as-of", now.Add(time.Second).Format(time.RFC3339)), now.Add(time.Hour), "--as-of must match")
	assertOperationalCLIError(t, append(append([]string{}, base...), "--backup-output", archive+".other"), now, "--backup-output does not match")
	restored := filepath.Join(filepath.Dir(target), "restored.db")
	if _, err := backup.Restore(t.Context(), archive, restored); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+restored+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var logs, runs, oldStatistic int
	if err = db.QueryRow(`SELECT COUNT(*) FROM legacy_operational_logs`).Scan(&logs); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT COUNT(*) FROM legacy_migration_runs WHERE slice=?`, store.LegacyOperationalLogsSlice).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(`SELECT value FROM operational_daily_statistics WHERE record_at=57600 AND metric='order_count'`).Scan(&oldStatistic); err != nil {
		t.Fatal(err)
	}
	if logs != 0 || runs != 0 || oldStatistic != 42 {
		t.Fatalf("restored logs=%d runs=%d previous statistic=%d", logs, runs, oldStatistic)
	}
}

func TestOperationalLogsCLIRejectsUnsafeArgumentsWithoutSuccessOutput(t *testing.T) {
	source, target, archive, now := operationalCLIFixture(t)
	prefix := []string{"migration", "import-legacy-operational-logs"}
	assertOperationalCLIError(t, append(append([]string{}, prefix...), "--source", source, "--backup-output", archive), now, "--confirm-offline")
	assertOperationalCLIError(t, append(append([]string{}, prefix...), "--source", source, "--confirm-offline", "--as-of", "yesterday"), now, "RFC3339")
	assertOperationalCLIError(t, append(append([]string{}, prefix...), "--source", source, "--confirm-offline", "--as-of", now.Add(time.Second).Format(time.RFC3339)), now, "future")
	assertOperationalCLIError(t, append(append([]string{}, prefix...), "--source", target, "--backup-output", archive, "--confirm-offline"), now, "different files")
	assertOperationalCLIError(t, append(append([]string{}, prefix...), "--source", source, "--confirm-offline"), now, "--backup-output")
	if _, err := os.Stat(archive); !os.IsNotExist(err) {
		t.Fatalf("rejected arguments created backup: %v", err)
	}
}

func TestOperationalLogsCLIRejectsCorruptedRecordedBackupAndTarget(t *testing.T) {
	for _, corruption := range []string{"backup", "target"} {
		t.Run(corruption, func(t *testing.T) {
			source, target, archive, now := operationalCLIFixture(t)
			base := []string{"migration", "import-legacy-operational-logs", "--source", source, "--confirm-offline"}
			runOperationalCLI(t, append(append([]string{}, base...), "--backup-output", archive), now)
			if corruption == "backup" {
				if err := os.WriteFile(archive, []byte("invalid synthetic backup"), 0o600); err != nil {
					t.Fatal(err)
				}
				assertOperationalCLIError(t, base, now, "rollback backup")
			} else {
				db, err := sql.Open("sqlite", "file:"+target)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = db.Exec(`UPDATE legacy_operational_logs SET method='DELETE'`); err != nil {
					db.Close()
					t.Fatal(err)
				}
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
				assertOperationalCLIError(t, base, now, "verification")
			}
		})
	}
}

func operationalCLIFixture(t *testing.T) (string, string, string, time.Time) {
	t.Helper()
	directory := t.TempDir()
	source := filepath.Join(directory, "source.db")
	targetPath := filepath.Join(directory, "target.db")
	archive := filepath.Join(directory, "rollback.xbbackup")
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	sourceDB, err := sql.Open("sqlite", "file:"+source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sourceDB.Exec(`CREATE TABLE v2_user(created_at INTEGER,invite_user_id INTEGER); CREATE TABLE v2_log(id INTEGER PRIMARY KEY,created_at INTEGER,method TEXT,data TEXT); INSERT INTO v2_log VALUES (1,?,'POST','PRIVATE_FIXTURE_BODY'); CREATE TABLE failed_jobs(payload TEXT); INSERT INTO failed_jobs VALUES ('PRIVATE_FIXTURE_PHP');`, now.Unix()); err != nil {
		sourceDB.Close()
		t.Fatal(err)
	}
	if err = sourceDB.Close(); err != nil {
		t.Fatal(err)
	}
	snapshot, err := legacymigration.ReadOperationalLogsSnapshot(t.Context(), source, now)
	if err != nil {
		t.Fatal(err)
	}
	target, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = target.Migrate(t.Context()); err != nil {
		target.Close()
		t.Fatal(err)
	}
	if err = target.Close(); err != nil {
		t.Fatal(err)
	}
	setup, err := sql.Open("sqlite", "file:"+targetPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, slice := range []string{store.LegacyHumanUsersSlice, store.LegacyNodesSlice, store.LegacyOrdersSlice, store.LegacyCommissionsSlice} {
		if _, err = setup.Exec(`INSERT INTO legacy_migration_runs(slice,source_sha256,source_size,rollback_backup_path,rollback_backup_sha256,report_json,applied_at) VALUES (?,?,4096,'prerequisite',?,'{}',?)`, slice, snapshot.SHA256, strings.Repeat("a", 64), now.Unix()); err != nil {
			setup.Close()
			t.Fatal(err)
		}
	}
	if _, err = setup.Exec(`INSERT INTO operational_daily_statistics VALUES (57600,'order_count',42)`); err != nil {
		setup.Close()
		t.Fatal(err)
	}
	if err = setup.Close(); err != nil {
		t.Fatal(err)
	}
	target, err = store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	_, importErr := target.ImportLegacyDistributors(t.Context(), store.LegacyDistributorsImport{Slice: store.LegacyDistributorsSlice, SourceSHA256: snapshot.SHA256, SourceSize: snapshot.Size,
		Checksum: store.LegacyDistributorsChecksum(store.LegacyDistributorsData{}), RollbackBackupPath: "prerequisite", RollbackBackupSHA256: strings.Repeat("a", 64)}, now)
	closeErr := target.Close()
	if importErr != nil || closeErr != nil {
		t.Fatalf("import verified empty distributor prerequisite: %v close=%v", importErr, closeErr)
	}
	t.Setenv("XBOARD_DATABASE_DSN", "file:"+targetPath)
	t.Setenv("XBOARD_ATTACHMENTS_ROOT", filepath.Join(directory, "attachments"))
	return source, targetPath, archive, now
}

func runOperationalCLI(t *testing.T, args []string, now time.Time) legacyOperationalLogsMigrationCommandResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), args, &stdout, &stderr, func() time.Time { return now })
	if err != nil || !handled || stderr.Len() != 0 {
		t.Fatalf("operational CLI handled=%v err=%v stderr=%q", handled, err, stderr.String())
	}
	if strings.Contains(stdout.String(), "PRIVATE_FIXTURE") {
		t.Fatal("migration output contains raw legacy payload")
	}
	var result legacyOperationalLogsMigrationCommandResult
	if err = json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertOperationalCLIError(t *testing.T, args []string, now time.Time, want string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	handled, err := runCommand(t.Context(), args, &stdout, &stderr, func() time.Time { return now })
	if !handled || err == nil || !strings.Contains(err.Error(), want) || stdout.Len() != 0 {
		t.Fatalf("CLI rejection handled=%v error=%v stdout=%q (want %q)", handled, err, stdout.String(), want)
	}
}
