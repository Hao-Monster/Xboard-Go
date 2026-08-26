package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
)

func TestJournalPreservesLastCompleteState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.jsonl")
	journal := NewJournal(path)
	first := State{
		Version: 1, Status: StatusPrepared, UpdatedAt: time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC),
		Previous: &Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)},
	}
	second := first
	second.Status = StatusUpgraded
	second.ActiveDSN = "file:/var/lib/xboard/xboard.db"
	if err := journal.Append(first); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(second); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"status":"partial`); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	loaded, err := journal.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(loaded, second) {
		t.Fatalf("loaded state = %#v, want %#v", loaded, second)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("journal permissions = %o", info.Mode().Perm())
	}
}

func TestJournalRefusesToGrowPastItsBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.jsonl")
	if err := os.WriteFile(path, make([]byte, maxJournalBytes), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := NewJournal(path)
	state := State{
		Version: StateVersion, Status: StatusPrepared,
		UpdatedAt: time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC),
	}

	if err := journal.Append(state); err == nil || !strings.Contains(err.Error(), "exceed") {
		t.Fatalf("Append() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != maxJournalBytes {
		t.Fatalf("journal size = %d, want %d", info.Size(), maxJournalBytes)
	}
}

func TestJournalRejectsPathsOutsideLifecycleVolumes(t *testing.T) {
	now := time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC)
	tests := []State{
		{Version: StateVersion, Status: StatusPrepared, UpdatedAt: now, BackupPath: "/etc/passwd.xbbackup"},
		{Version: StateVersion, Status: StatusRollbackFailed, UpdatedAt: now, RestoredPath: "/tmp/restored.db"},
		{Version: StateVersion, Status: StatusRollbackFailed, UpdatedAt: now, RestoredAttachments: "/tmp/attachments"},
	}
	for _, state := range tests {
		journal := NewJournal(filepath.Join(t.TempDir(), "state.jsonl"))
		if err := journal.Append(state); err == nil || !strings.Contains(err.Error(), "path") {
			t.Fatalf("Append(%#v) error = %v", state, err)
		}
	}
}

func TestStatusReportsAnUnhealthyApplicationForDiagnosis(t *testing.T) {
	image := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
	application := healthyApplication(image, defaultDatabaseDSN)
	application.Healthy = false
	application.HealthStatus = "unhealthy"
	orchestrator := NewOrchestrator(
		&fakePlatform{current: application},
		NewJournal(filepath.Join(t.TempDir(), "state.jsonl")),
		time.Now,
	)

	result, err := orchestrator.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "success" || result.Application == nil || result.Application.Healthy || result.Application.HealthStatus != "unhealthy" {
		t.Fatalf("status result = %#v", result)
	}
}

func TestInstallFailureIsRecordedForDiagnosis(t *testing.T) {
	target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	platform := &fakePlatform{
		images:         map[string]Image{"candidate": target},
		activateErrors: map[string]error{target.ID: errors.New("target failed health validation")},
	}
	journal := NewJournal(filepath.Join(t.TempDir(), "state.jsonl"))
	fixed := time.Date(2026, 8, 25, 7, 30, 0, 123456789, time.UTC)
	orchestrator := NewOrchestrator(platform, journal, func() time.Time { return fixed })

	result, err := orchestrator.Install(context.Background(), "candidate")
	if err == nil || !strings.Contains(err.Error(), "install target") {
		t.Fatalf("Install() result = %#v, error = %v", result, err)
	}
	if result.Status != "failed" || result.Action != "lifecycle.install" || result.State == nil {
		t.Fatalf("Install() result = %#v", result)
	}
	state, loadErr := journal.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if state.Status != StatusInstallFailed || state.Failure != "target failed health validation" {
		t.Fatalf("install state = %#v", state)
	}
	payload, readErr := os.ReadFile(journal.Path())
	if readErr != nil {
		t.Fatal(readErr)
	}
	if lines := strings.Count(string(payload), "\n"); lines != 2 {
		t.Fatalf("journal records = %d, want prepared and failed records", lines)
	}
}

func TestUpgradeFailureAutomaticallyRestoresPreviousImageAndDatabase(t *testing.T) {
	previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
	target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	platform := &fakePlatform{
		current:        healthyApplication(previous, "file:/var/lib/xboard/xboard.db"),
		images:         map[string]Image{"candidate": target},
		activateErrors: map[string]error{target.ID: errors.New("target health check failed")},
	}
	journal := NewJournal(filepath.Join(t.TempDir(), "state.jsonl"))
	fixed := time.Date(2026, 8, 25, 8, 0, 0, 0, time.UTC)
	orchestrator := NewOrchestrator(platform, journal, func() time.Time { return fixed })

	result, err := orchestrator.Upgrade(context.Background(), "candidate")
	if err == nil || !strings.Contains(err.Error(), "automatically rolled back") {
		t.Fatalf("Upgrade() result = %#v error = %v", result, err)
	}
	wantActions := []string{
		"current", "resolve:candidate",
		"backup:/var/lib/xboard-backups/lifecycle-20260825T080000.000000000Z-aaaaaaaaaaaa.xbbackup",
		"activate:" + target.ID + ":file:/var/lib/xboard/xboard.db",
		"restore:" + previous.ID + ":/var/lib/xboard/xboard-rollback-20260825T080000.000000000Z.db:",
		"activate:" + previous.ID + ":file:/var/lib/xboard/xboard-rollback-20260825T080000.000000000Z.db",
	}
	if !reflect.DeepEqual(platform.actions, wantActions) {
		t.Fatalf("actions = %#v, want %#v", platform.actions, wantActions)
	}
	state, loadErr := journal.Load()
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if state.Status != StatusAutoRolledBack || state.ActiveDSN != "file:/var/lib/xboard/xboard-rollback-20260825T080000.000000000Z.db" {
		t.Fatalf("state = %#v", state)
	}
	if state.Failure != "target health check failed" {
		t.Fatalf("failure = %q", state.Failure)
	}
}

func TestRollbackRequiresTheRecordedHealthyTarget(t *testing.T) {
	previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
	target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	journal := NewJournal(filepath.Join(t.TempDir(), "state.jsonl"))
	if err := journal.Append(State{
		Version: 1, Status: StatusUpgraded, UpdatedAt: time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC), Previous: &previous, Target: &target,
		BackupPath:     "/var/lib/xboard-backups/pre-upgrade.xbbackup",
		BackupManifest: ptrManifest(testBackupManifest(previous.Revision)),
		ActiveDSN:      "file:/var/lib/xboard/xboard.db",
	}); err != nil {
		t.Fatal(err)
	}
	platform := &fakePlatform{current: healthyApplication(previous, "file:/var/lib/xboard/xboard.db")}
	orchestrator := NewOrchestrator(platform, journal, time.Now)

	if _, err := orchestrator.Rollback(context.Background()); err == nil || !strings.Contains(err.Error(), "target image") {
		t.Fatalf("Rollback() error = %v", err)
	}
	if !reflect.DeepEqual(platform.actions, []string{"current"}) {
		t.Fatalf("actions = %#v", platform.actions)
	}
}

func TestSuccessfulUpgradeCanBeExplicitlyRolledBack(t *testing.T) {
	previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
	target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	manifest := testBackupManifest(previous.Revision)
	manifest.FormatVersion = 2
	manifest.AttachmentSHA256 = strings.Repeat("c", 64)
	platform := &fakePlatform{
		current: healthyApplication(previous, defaultDatabaseDSN),
		images:  map[string]Image{"candidate": target}, backupManifest: manifest, restoreManifest: manifest,
	}
	journal := NewJournal(filepath.Join(t.TempDir(), "state.jsonl"))
	fixed := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	orchestrator := NewOrchestrator(platform, journal, func() time.Time { return fixed })

	upgraded, err := orchestrator.Upgrade(context.Background(), "candidate")
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.State == nil || upgraded.State.Status != StatusUpgraded || upgraded.Application == nil || upgraded.Application.Image != target {
		t.Fatalf("upgrade result = %#v", upgraded)
	}
	rolledBack, err := orchestrator.Rollback(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.State == nil || rolledBack.State.Status != StatusRolledBack || rolledBack.Application == nil || rolledBack.Application.Image != previous {
		t.Fatalf("rollback result = %#v", rolledBack)
	}
	if rolledBack.State.ActiveDSN != "file:/var/lib/xboard/xboard-rollback-20260825T090000.000000000Z.db" {
		t.Fatalf("active DSN = %q", rolledBack.State.ActiveDSN)
	}
	if rolledBack.State.RestoredAttachments != "/var/lib/xboard/xboard-rollback-20260825T090000.000000000Z-attachments" {
		t.Fatalf("restored attachments = %q", rolledBack.State.RestoredAttachments)
	}
	if !slicesContainPrefix(platform.actions, "restore:"+previous.ID+":"+rolledBack.State.RestoredPath+":"+rolledBack.State.RestoredAttachments) {
		t.Fatalf("bundle attachment restore was not recorded: %#v", platform.actions)
	}
}

func TestUpgradeRejectsARestoreFromTheWrongRevision(t *testing.T) {
	previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
	target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	platform := &fakePlatform{
		current:         healthyApplication(previous, defaultDatabaseDSN),
		images:          map[string]Image{"candidate": target},
		activateErrors:  map[string]error{target.ID: errors.New("target failed")},
		restoreManifest: backup.Manifest{AppRevision: strings.Repeat("c", 40)},
	}
	orchestrator := NewOrchestrator(platform, NewJournal(filepath.Join(t.TempDir(), "state.jsonl")), time.Now)

	if _, err := orchestrator.Upgrade(context.Background(), "candidate"); err == nil || !strings.Contains(err.Error(), "restored backup revision") {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if slicesContainPrefix(platform.actions, "activate:"+previous.ID) {
		t.Fatalf("previous image activated against an unverified restore: %#v", platform.actions)
	}
}

func TestExplicitRollbackRejectsARestoreFromTheWrongRevision(t *testing.T) {
	previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
	target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	journal := NewJournal(filepath.Join(t.TempDir(), "state.jsonl"))
	if err := journal.Append(State{
		Version: StateVersion, Status: StatusUpgraded, UpdatedAt: time.Now().UTC(),
		Previous: &previous, Target: &target, BackupPath: "/var/lib/xboard-backups/pre-upgrade.xbbackup",
		BackupManifest: ptrManifest(testBackupManifest(previous.Revision)),
		OriginalDSN:    defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN,
	}); err != nil {
		t.Fatal(err)
	}
	platform := &fakePlatform{
		current:         healthyApplication(target, defaultDatabaseDSN),
		restoreManifest: backup.Manifest{AppRevision: strings.Repeat("c", 40)},
	}
	orchestrator := NewOrchestrator(platform, journal, time.Now)

	if _, err := orchestrator.Rollback(context.Background()); err == nil || !strings.Contains(err.Error(), "restored backup revision") {
		t.Fatalf("Rollback() error = %v", err)
	}
	if slicesContainPrefix(platform.actions, "activate:"+previous.ID) {
		t.Fatalf("previous image activated against an unverified restore: %#v", platform.actions)
	}
}

func TestLifecycleLockIsExclusiveAndExplicitlyReleased(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operation.lock")
	now := time.Date(2026, 8, 25, 9, 0, 0, 0, time.UTC)
	lock, err := AcquireLock(path, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireLock(path, now); err == nil || !strings.Contains(err.Error(), "locked") {
		t.Fatalf("second AcquireLock() error = %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireLock(path, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Release(); err != nil {
		t.Fatal(err)
	}
}

type fakePlatform struct {
	current         Application
	images          map[string]Image
	activateErrors  map[string]error
	restoreManifest backup.Manifest
	backupManifest  backup.Manifest
	actions         []string
}

func (f *fakePlatform) Current(context.Context) (Application, error) {
	f.actions = append(f.actions, "current")
	return f.current, nil
}

func (f *fakePlatform) ResolveImage(_ context.Context, reference string) (Image, error) {
	f.actions = append(f.actions, "resolve:"+reference)
	image, ok := f.images[reference]
	if !ok {
		return Image{}, errors.New("image not found")
	}
	return image, nil
}

func (f *fakePlatform) Fresh(context.Context) (bool, error) { return true, nil }

func (f *fakePlatform) Activate(_ context.Context, image Image, dsn string) (Application, error) {
	f.actions = append(f.actions, "activate:"+image.ID+":"+dsn)
	if err := f.activateErrors[image.ID]; err != nil {
		return Application{}, err
	}
	f.current = healthyApplication(image, dsn)
	return f.current, nil
}

func (f *fakePlatform) Backup(_ context.Context, _ Application, output string) (backup.Manifest, error) {
	f.actions = append(f.actions, "backup:"+output)
	if f.backupManifest.AppRevision != "" {
		return f.backupManifest, nil
	}
	return testBackupManifest(f.current.Image.Revision), nil
}

func (f *fakePlatform) Restore(_ context.Context, image Image, _ string, output, attachmentOutput string) (backup.Manifest, error) {
	f.actions = append(f.actions, "restore:"+image.ID+":"+output+":"+attachmentOutput)
	if f.restoreManifest.AppRevision != "" {
		return f.restoreManifest, nil
	}
	return testBackupManifest(image.Revision), nil
}

func testBackupManifest(revision string) backup.Manifest {
	return backup.Manifest{
		FormatVersion:  1,
		CreatedAt:      time.Date(2026, 8, 25, 7, 0, 0, 0, time.UTC),
		AppRevision:    revision,
		SchemaVersion:  23,
		DatabaseSize:   1024,
		DatabaseSHA256: strings.Repeat("d", 64),
	}
}

func TestValidateBackupManifestAcceptsCurrentBundleFormat(t *testing.T) {
	manifest := testBackupManifest(strings.Repeat("a", 40))
	manifest.FormatVersion = 2
	manifest.AttachmentSHA256 = strings.Repeat("b", 64)
	if err := validateBackupManifest(manifest); err != nil {
		t.Fatalf("validateBackupManifest() error = %v", err)
	}

	manifest.AttachmentSHA256 = ""
	if err := validateBackupManifest(manifest); err == nil {
		t.Fatal("validateBackupManifest() accepted incomplete bundle metadata")
	}
}

func ptrManifest(manifest backup.Manifest) *backup.Manifest {
	return &manifest
}

func slicesContainPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func healthyApplication(image Image, dsn string) Application {
	return Application{
		Image: image, DSN: dsn, ContainerStatus: "running", HealthStatus: "healthy", Healthy: true,
	}
}
