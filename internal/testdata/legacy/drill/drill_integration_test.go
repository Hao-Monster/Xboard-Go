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
		AlreadyApplied bool `json:"already_applied"`
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
	manifest, err := gen.New(gen.DefaultConfig(sourcePath)).Generate(ctx)
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
	}
	results := make(map[string]commandResult, len(steps))
	for index, step := range steps {
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
		result, raw := runMigration(t, ctx, binaryPath, environment, sourcePath, step, "")
		if !result.Result.AlreadyApplied || result.RollbackBackup.SHA256 != results[step.name].RollbackBackup.SHA256 {
			t.Fatalf("idempotent %s result = %#v", step.name, result)
		}
		assertRedacted(t, raw)
	}

	assertTargetCounts(t, targetPath, map[string]int{
		"users": 2, "plans": 2, "access_tokens": 2, "invitation_codes": 2,
		"coupons": 2, "payments": 1, "orders": 2, "tickets": 2,
		"ticket_messages": 3, "server_machines": 1, "nodes": 1,
		"legacy_migration_runs": len(steps),
	})

	restoredPath := filepath.Join(directory, "restored-before-orders.db")
	ordersBackup := filepath.Join(directory, "10-pre-orders.xbbackup")
	if _, err := backup.Restore(ctx, ordersBackup, restoredPath); err != nil {
		t.Fatalf("restore pre-orders rollback: %v", err)
	}
	assertTargetCounts(t, restoredPath, map[string]int{
		"users": 2, "plans": 2, "coupons": 2, "payments": 1,
		"orders": 0, "nodes": 0, "legacy_migration_runs": 9,
	})
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
