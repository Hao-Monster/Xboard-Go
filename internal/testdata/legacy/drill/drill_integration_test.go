package drill_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
	"github.com/Hao-Monster/Xboard-Go/internal/testdata/legacy/gen"
	_ "modernc.org/sqlite"
)

type commandResult struct {
	Status string `json:"status"`
	Action string `json:"action"`
	Source struct {
		SHA256 string `json:"sha256"`
	} `json:"source"`
	RollbackBackup struct {
		Path   string `json:"path"`
		SHA256 string `json:"sha256"`
	} `json:"rollback_backup"`
	Result struct {
		AlreadyApplied     bool                     `json:"already_applied"`
		AsOf               int64                    `json:"as_of"`
		CutoffAt           int64                    `json:"cutoff_at"`
		ExcludedRows       int64                    `json:"excluded_rows"`
		ExcludedFailedJobs int64                    `json:"excluded_failed_jobs"`
		Logs               store.LegacyDomainResult `json:"logs"`
		StatisticsRows     int64                    `json:"statistics_rows"`
		StatisticsChecksum string                   `json:"statistics_checksum"`
		StatisticsTimezone string                   `json:"statistics_timezone"`
		UserFactsChecksum  string                   `json:"user_facts_checksum"`
	} `json:"result"`
}

type migrationStep struct {
	name      string
	extraArgs []string
}

func TestRepresentativeLegacyMigrationDrill(t *testing.T) {
	if testing.Short() {
		t.Skip("representative drill builds and executes the maintenance binary")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "legacy.db")
	config := gen.DefaultConfig(sourcePath)
	manifest, err := gen.New(config).Generate(ctx)
	if err != nil {
		t.Fatalf("generate representative source: %v", err)
	}
	targetPath := filepath.Join(directory, "target.db")
	initializeTarget(t, ctx, targetPath)
	binaryPath := buildMaintenanceBinary(t, ctx, directory)
	encryptionKey := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0x53}, 32))
	environment := overrideEnvironment(os.Environ(), map[string]string{
		"XBOARD_DATABASE_DSN":                  "file:" + targetPath,
		"XBOARD_SETTINGS_ENCRYPTION_KEY":       encryptionKey,
		"XBOARD_SETTINGS_ENCRYPTION_KEY_FILE":  "",
		"XBOARD_BOOTSTRAP_ADMIN_EMAIL":         "",
		"XBOARD_BOOTSTRAP_ADMIN_PASSWORD":      "",
		"XBOARD_BOOTSTRAP_ADMIN_PASSWORD_FILE": "",
	})
	steps := []migrationStep{
		{name: "content"},
		{name: "groups-routes"},
		{name: "plans"},
		{name: "human-users", extraArgs: []string{"--replace-bootstrap-admin"}},
		{name: "access-tokens"},
		{name: "invitation-codes"},
		{name: "tickets"},
		{name: "coupons"},
		{name: "payments"},
		{name: "orders"},
		{name: "nodes"},
		{name: "currency-settings"},
		{name: "public-origin-settings"},
		{name: "safe-access-settings"},
		{name: "commissions"},
		{name: "distributors"},
		{name: "operational-logs", extraArgs: []string{"--as-of", config.Now.Format(time.RFC3339)}},
	}
	results := make(map[string]commandResult, len(steps))
	initialRollbackBackup := filepath.Join(directory, "01-pre-content.xbbackup")
	for index, step := range steps {
		if step.name == "distributors" {
			assertOperationalImportRequiresDistributors(t, ctx, binaryPath, environment, sourcePath, filepath.Join(directory, "blocked-before-distributors.xbbackup"), config.Now)
			assertTargetCounts(t, targetPath, map[string]int{"users": 2, "orders": 3, "commission_logs": 1, "legacy_migration_runs": index, "legacy_operational_logs": 0, "operational_daily_statistics": 0})
		}
		backupPath := filepath.Join(directory, fmt.Sprintf("%02d-pre-%s.xbbackup", index+1, step.name))
		result, raw := runMigration(t, ctx, binaryPath, environment, sourcePath, step, backupPath)
		if result.Status != "success" || result.Action != "migration.import-legacy-"+step.name || result.Result.AlreadyApplied {
			t.Fatalf("first %s result = %#v", step.name, result)
		}
		if result.Source.SHA256 != manifest.DatabaseSHA || result.RollbackBackup.Path == "" || len(result.RollbackBackup.SHA256) != 64 {
			t.Fatalf("%s identities = %#v", step.name, result)
		}
		assertRedacted(t, raw)
		assertReconciled(t, step.name, raw)
		if _, err := backup.Verify(ctx, backupPath); err != nil {
			t.Fatalf("verify %s rollback: %v", step.name, err)
		}
		results[step.name] = result
	}

	for _, step := range steps {
		if step.name == "operational-logs" {
			step.extraArgs = nil // Replay must reuse the recorded boundary, not now.
		}
		result, raw := runMigration(t, ctx, binaryPath, environment, sourcePath, step, "")
		if !result.Result.AlreadyApplied || result.RollbackBackup.SHA256 != results[step.name].RollbackBackup.SHA256 {
			t.Fatalf("idempotent %s result = %#v", step.name, result)
		}
		assertRedacted(t, raw)
		if step.name == "operational-logs" {
			first := results[step.name].Result
			if result.Result.AsOf != first.AsOf || result.Result.CutoffAt != first.CutoffAt || result.Result.StatisticsChecksum != first.StatisticsChecksum || result.Result.UserFactsChecksum != first.UserFactsChecksum || result.Result.Logs != first.Logs {
				t.Fatalf("operational replay drifted: %#v first=%#v", result.Result, first)
			}
		}
	}

	assertTargetCounts(t, targetPath, map[string]int{
		"users": 3, "plans": 2, "access_tokens": 2, "invitation_codes": 2,
		"coupons": 2, "payments": 1, "orders": 3, "tickets": 2,
		"ticket_messages": 3, "server_machines": 1, "nodes": 1,
		"commission_logs": 1, "distributor_subscriptions": 1, "node_traffic_stats": 1,
		"legacy_operational_logs": 4, "operational_daily_statistics": 9,
		"legacy_migration_runs": len(steps),
	})
	assertOperationalProjection(t, sourcePath, targetPath, config.Now, results["operational-logs"])
	restoredOperationalPath := filepath.Join(directory, "restored-before-operational.db")
	if _, err := backup.Restore(ctx, results["operational-logs"].RollbackBackup.Path, restoredOperationalPath); err != nil {
		t.Fatalf("restore pre-operational rollback: %v", err)
	}
	assertTargetCounts(t, restoredOperationalPath, map[string]int{"users": 3, "orders": 3, "commission_logs": 1, "distributor_subscriptions": 1, "node_traffic_stats": 1,
		"legacy_operational_logs": 0, "operational_daily_statistics": 0, "legacy_migration_runs": len(steps) - 1})
	restoredEnvironment := overrideEnvironment(environment, map[string]string{"XBOARD_DATABASE_DSN": "file:" + restoredOperationalPath})
	reapplied, raw := runMigration(t, ctx, binaryPath, restoredEnvironment, sourcePath, steps[len(steps)-1], filepath.Join(directory, "restored-pre-operational.xbbackup"))
	if reapplied.Result.AlreadyApplied || reapplied.Result.StatisticsChecksum != results["operational-logs"].Result.StatisticsChecksum {
		t.Fatalf("reapply restored operational projection: %#v", reapplied.Result)
	}
	assertRedacted(t, raw)
	assertOperationalProjection(t, sourcePath, restoredOperationalPath, config.Now, reapplied)

	restoredPath := filepath.Join(directory, "restored-before-orders.db")
	ordersBackup := filepath.Join(directory, "10-pre-orders.xbbackup")
	if _, err := backup.Restore(ctx, ordersBackup, restoredPath); err != nil {
		t.Fatalf("restore pre-orders rollback: %v", err)
	}
	assertTargetCounts(t, restoredPath, map[string]int{
		"users": 2, "plans": 2, "coupons": 2, "payments": 1,
		"orders": 0, "nodes": 0, "legacy_migration_runs": 9,
	})

	restoredInitialPath := filepath.Join(directory, "restored-initial-target.db")
	if _, err := backup.Restore(ctx, initialRollbackBackup, restoredInitialPath); err != nil {
		t.Fatalf("restore initial rollback: %v", err)
	}
	assertMigratableTarget(t, ctx, restoredInitialPath)
	assertTargetCounts(t, restoredInitialPath, map[string]int{
		"users": 1, "plans": 0, "access_tokens": 0, "invitation_codes": 0,
		"coupons": 0, "payments": 0, "orders": 0, "tickets": 0,
		"ticket_messages": 0, "server_machines": 0, "nodes": 0,
		"commission_logs": 0, "distributor_subscriptions": 0, "node_traffic_stats": 0,
		"legacy_operational_logs": 0, "operational_daily_statistics": 0,
		"legacy_migration_runs": 0,
	})
	assertLegacyIdentitiesAbsent(t, restoredInitialPath)
}

func initializeTarget(t *testing.T, ctx context.Context, targetPath string) {
	t.Helper()
	database, err := store.OpenSQLite("file:" + targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BootstrapAdmin(ctx, "bootstrap@example.test", "bootstrap-hash", time.Unix(50, 0)); err != nil {
		t.Fatal(err)
	}
}

func assertOperationalImportRequiresDistributors(t *testing.T, ctx context.Context, binaryPath string, environment []string, sourcePath, backupPath string, asOf time.Time) {
	t.Helper()
	command := exec.CommandContext(ctx, binaryPath, "migration", "import-legacy-operational-logs", "--source", sourcePath, "--confirm-offline", "--backup-output", backupPath, "--as-of", asOf.Format(time.RFC3339))
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err == nil || stdout.Len() != 0 || !strings.Contains(stderr.String(), "import human users, distributors, nodes, orders and commissions") {
		t.Fatalf("operational migration before distributors error=%v stdout=%s stderr=%s", err, stdout.String(), stderr.String())
	}
	assertRedacted(t, stderr.String())
}

func assertOperationalProjection(t *testing.T, sourcePath, targetPath string, asOf time.Time, result commandResult) {
	t.Helper()
	cutoff := asOf.Add(-90 * 24 * time.Hour).Unix()
	if result.Result.AsOf != asOf.Unix() || result.Result.CutoffAt != cutoff || result.Result.ExcludedRows != 1 || result.Result.ExcludedFailedJobs != 1 ||
		result.Result.Logs.SourceRows != 4 || result.Result.Logs.TargetRows != 4 || result.Result.StatisticsRows != 9 || result.Result.StatisticsTimezone != "Asia/Shanghai" ||
		len(result.Result.StatisticsChecksum) != 64 || len(result.Result.UserFactsChecksum) != 64 {
		t.Fatalf("operational report=%#v", result.Result)
	}
	source, err := sql.Open("sqlite", "file:"+sourcePath+"?mode=ro&immutable=1")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	target, err := sql.Open("sqlite", "file:"+targetPath+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	// The fixture's business facts occur on 2023-11-15 in the old app's
	// Asia/Shanghai calendar, independently of the 2026 log retention window.
	day := time.Date(2023, 11, 15, 0, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600)).Unix()
	want := map[string]int64{"register_count": 3, "invite_count": 1, "order_count": 3, "order_total": 3522, "paid_count": 2, "paid_total": 1523, "commission_count": 1, "commission_total": 50, "transfer_used_total": 12}
	queries := map[string]string{
		"register_count":      `SELECT COUNT(*) FROM v2_user WHERE created_at >= ? AND created_at < ?`,
		"invite_count":        `SELECT COUNT(*) FROM v2_user WHERE created_at >= ? AND created_at < ? AND invite_user_id IS NOT NULL`,
		"order_count":         `SELECT COUNT(*) FROM v2_order WHERE created_at >= ? AND created_at < ?`,
		"order_total":         `SELECT SUM(total_amount) FROM v2_order WHERE created_at >= ? AND created_at < ?`,
		"paid_count":          `SELECT COUNT(*) FROM v2_order WHERE paid_at >= ? AND paid_at < ? AND status NOT IN (0,2)`,
		"paid_total":          `SELECT SUM(total_amount) FROM v2_order WHERE paid_at >= ? AND paid_at < ? AND status NOT IN (0,2)`,
		"commission_count":    `SELECT COUNT(*) FROM v2_commission_log WHERE created_at >= ? AND created_at < ?`,
		"commission_total":    `SELECT SUM(get_amount) FROM v2_commission_log WHERE created_at >= ? AND created_at < ?`,
		"transfer_used_total": `SELECT SUM(u)+SUM(d) FROM v2_stat_server WHERE created_at >= ? AND created_at < ?`,
	}
	for metric, query := range queries {
		var original, rebuilt int64
		if err := source.QueryRow(query, day, day+86400).Scan(&original); err != nil {
			t.Fatalf("original %s: %v", metric, err)
		}
		if err := target.QueryRow(`SELECT value FROM operational_daily_statistics WHERE record_at=? AND metric=?`, day, metric).Scan(&rebuilt); err != nil {
			t.Fatalf("rebuilt %s: %v", metric, err)
		}
		if original != want[metric] || rebuilt != original {
			t.Errorf("%s original=%d rebuilt=%d fixture=%d", metric, original, rebuilt, want[metric])
		}
	}
	rows, err := target.Query(`SELECT category,source_id,method,created_at FROM legacy_operational_logs ORDER BY category,source_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	logs := []store.LegacyOperationalLog{}
	for rows.Next() {
		var log store.LegacyOperationalLog
		if err := rows.Scan(&log.Category, &log.ID, &log.Method, &log.CreatedAt); err != nil {
			t.Fatal(err)
		}
		logs = append(logs, log)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	wantLogs := []store.LegacyOperationalLog{{Category: "admin_audit", ID: 151, Method: "POST", CreatedAt: cutoff}, {Category: "mail", ID: 154, CreatedAt: asOf.Unix()},
		{Category: "request", ID: 153, Method: "GET", CreatedAt: asOf.Unix()}, {Category: "server", ID: 155, CreatedAt: cutoff}}
	if !reflect.DeepEqual(logs, wantLogs) || store.LegacyOperationalLogsChecksum(logs) != result.Result.Logs.TargetChecksum {
		t.Fatalf("actual retained metadata=%#v want=%#v", logs, wantLogs)
	}
	var kind string
	var created int64
	if err := target.QueryRow(`SELECT account_kind,created_at FROM users WHERE id=100`).Scan(&kind, &created); err != nil || kind != store.AccountKindInternalSubscription || created != 1700000400 {
		t.Fatalf("internal subscriber mapping kind=%s created=%d: %v", kind, created, err)
	}
	var rawTables int
	if err := target.QueryRow(`SELECT COUNT(*) FROM sqlite_schema WHERE name IN ('v2_stat','stats_daily','failed_jobs','v2_log','v2_mail_log')`).Scan(&rawTables); err != nil || rawTables != 0 {
		t.Fatalf("raw legacy tables leaked into target count=%d: %v", rawTables, err)
	}
}

func buildMaintenanceBinary(t *testing.T, ctx context.Context, directory string) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	name := "xboard"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	path := filepath.Join(directory, name)
	command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", path, "./cmd/xboard")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build maintenance binary: %v\n%s", err, output)
	}
	return path
}

func assertMigratableTarget(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	database, err := store.OpenSQLite("file:" + path)
	if err != nil {
		t.Fatalf("open restored target: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("migrate restored target: %v", err)
	}
}

func runMigration(t *testing.T, ctx context.Context, binaryPath string, environment []string, sourcePath string, step migrationStep, backupPath string) (commandResult, string) {
	t.Helper()
	arguments := []string{"migration", "import-legacy-" + step.name, "--source", sourcePath, "--confirm-offline"}
	if backupPath != "" {
		arguments = append(arguments, "--backup-output", backupPath)
	}
	arguments = append(arguments, step.extraArgs...)
	command := exec.CommandContext(ctx, binaryPath, arguments...)
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("run %s: %v\nstdout=%s\nstderr=%s", step.name, err, stdout.String(), stderr.String())
	}
	if strings.TrimSpace(stderr.String()) != "" {
		t.Fatalf("run %s wrote stderr: %s", step.name, stderr.String())
	}
	var result commandResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode %s result: %v\n%s", step.name, err, stdout.String())
	}
	return result, stdout.String()
}

func assertRedacted(t *testing.T, output string) {
	t.Helper()
	for _, forbidden := range []string{
		"admin-one@example.test", "member-two@example.test", "SynA1234", "SynB5678",
		"synthetic-ipn-secret", "synthetic-machine-token", "$2y$10$",
		"internal-subscriber@example.test", "SYNTHETIC_RAW_", "!internal:synthetic",
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("migration output exposed synthetic credential or identity %q", forbidden)
		}
	}
}

func assertReconciled(t *testing.T, stepName, output string) {
	t.Helper()
	var envelope struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode %s reconciliation: %v", stepName, err)
	}
	reconciled := 0
	for field, raw := range envelope.Result {
		var domain struct {
			SourceRows     *int   `json:"source_rows"`
			TargetRows     int    `json:"target_rows"`
			SourceChecksum string `json:"source_checksum"`
			TargetChecksum string `json:"target_checksum"`
		}
		if err := json.Unmarshal(raw, &domain); err != nil || domain.SourceRows == nil {
			continue
		}
		reconciled++
		if *domain.SourceRows != domain.TargetRows || len(domain.SourceChecksum) != 64 || domain.SourceChecksum != domain.TargetChecksum {
			t.Errorf("%s domain %s is not reconciled: source_rows=%d target_rows=%d source_checksum=%q target_checksum=%q",
				stepName, field, *domain.SourceRows, domain.TargetRows, domain.SourceChecksum, domain.TargetChecksum)
		}
	}
	if reconciled == 0 {
		t.Errorf("%s result contains no independently reconciled domain", stepName)
	}
}

func assertTargetCounts(t *testing.T, path string, expected map[string]int) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for table, want := range expected {
		var got int
		query := `SELECT COUNT(*) FROM "` + table + `"`
		if err := database.QueryRow(query).Scan(&got); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		if got != want {
			t.Errorf("%s rows = %d, want %d", table, got, want)
		}
	}
}

func assertLegacyIdentitiesAbsent(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var importedUsers int
	if err := database.QueryRow(`SELECT COUNT(*) FROM users WHERE email IN (?, ?)`, "admin-one@example.test", "member-two@example.test").Scan(&importedUsers); err != nil {
		t.Fatalf("count restored legacy user identities: %v", err)
	}
	if importedUsers != 0 {
		t.Errorf("restored initial target contains %d imported legacy user identities", importedUsers)
	}
	var bootstrapUsers int
	if err := database.QueryRow(`SELECT COUNT(*) FROM users WHERE email = ?`, "bootstrap@example.test").Scan(&bootstrapUsers); err != nil {
		t.Fatalf("count restored bootstrap user: %v", err)
	}
	if bootstrapUsers != 1 {
		t.Errorf("restored initial target bootstrap users = %d, want 1", bootstrapUsers)
	}
}

func overrideEnvironment(base []string, overrides map[string]string) []string {
	filtered := make([]string, 0, len(base)+len(overrides))
	for _, entry := range base {
		name, _, found := strings.Cut(entry, "=")
		if found {
			if _, overridden := overrides[strings.ToUpper(name)]; overridden {
				continue
			}
		}
		filtered = append(filtered, entry)
	}
	for name, value := range overrides {
		filtered = append(filtered, name+"="+value)
	}
	return filtered
}
