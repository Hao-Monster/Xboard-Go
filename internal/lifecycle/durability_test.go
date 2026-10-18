package lifecycle

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

func TestJournalAppendRepairsOnlyIncompleteTail(t *testing.T) {
	for _, topology := range []string{"combined", "split"} {
		t.Run(topology, func(t *testing.T) {
			path, appendState, loadStatus := journalFixture(t, topology)
			if err := appendState(StatusPrepared); err != nil {
				t.Fatal(err)
			}
			if err := appendState(StatusUpgraded); err != nil {
				t.Fatal(err)
			}
			history, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(append([]byte(nil), history...), []byte(`{"status":"partial`)...), 0o600); err != nil {
				t.Fatal(err)
			}
			if got, err := loadStatus(); err != nil || got != StatusUpgraded {
				t.Fatalf("Load partial = %q, %v", got, err)
			}
			if err := appendState(StatusRolledBack); err != nil {
				t.Fatal(err)
			}
			if got, err := loadStatus(); err != nil || got != StatusRolledBack {
				t.Fatalf("Load after append = %q, %v", got, err)
			}
			payload, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(payload, history) || bytes.Contains(payload, []byte("partial")) || bytes.Count(payload, []byte{'\n'}) != 3 {
				t.Fatalf("history changed or invalid tail retained: %q", payload)
			}
		})
	}
}

func TestJournalAppendRejectsCorruptCompleteRecordsWithoutMutation(t *testing.T) {
	for _, topology := range []string{"combined", "split"} {
		for _, corruption := range []string{"{\n", "{}\n", "{", "{\n{\"status\":\"partial", "\n \n"} {
			t.Run(topology+"/"+corruption, func(t *testing.T) {
				path, appendState, _ := journalFixture(t, topology)
				if err := os.WriteFile(path, []byte(corruption), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := appendState(StatusPrepared); err == nil {
					t.Fatal("Append accepted invalid existing journal")
				}
				payload, err := os.ReadFile(path)
				if err != nil || string(payload) != corruption {
					t.Fatalf("rejected append changed history: %q, %v", payload, err)
				}
			})
		}
	}
}

func journalFixture(t *testing.T, topology string) (string, func(string) error, func() (string, error)) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "journal.jsonl")
	now := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	if topology == "combined" {
		journal := NewJournal(path)
		return path, func(status string) error {
			return journal.Append(State{Version: StateVersion, Status: status, UpdatedAt: now})
		}, func() (string, error) { state, err := journal.Load(); return state.Status, err }
	}
	journal := NewDeploymentJournal(path)
	return path, func(status string) error {
		return journal.Append(DeploymentState{Version: DeploymentStateVersion, Status: status, UpdatedAt: now})
	}, func() (string, error) { state, err := journal.Load(); return state.Status, err }
}

func TestRollbackAllowsRecordedUnhealthyTarget(t *testing.T) {
	for _, stateStatus := range []string{StatusUpgraded, StatusTargetFailed} {
		for _, containerStatus := range []string{"running", "exited"} {
			t.Run(stateStatus+"/"+containerStatus, func(t *testing.T) {
				platform, journal := rollbackFixture(t, stateStatus)
				platform.current.Healthy = false
				platform.current.HealthStatus = "unhealthy"
				platform.current.ContainerStatus = containerStatus
				result, err := NewOrchestrator(platform, journal, time.Now).Rollback(context.Background())
				if err != nil || result.Status != "success" || result.State.Status != StatusRolledBack {
					t.Fatalf("Rollback = %#v, %v", result, err)
				}
				if !slicesContainPrefix(platform.actions, "restore:") || !slicesContainPrefix(platform.actions, "activate:"+result.State.Previous.ID) {
					t.Fatalf("no verified recovery: %v", platform.actions)
				}
			})
		}
	}
}

func TestRollbackRejectsDatabaseDriftBeforeRestore(t *testing.T) {
	platform, journal := rollbackFixture(t, StatusUpgraded)
	platform.current.DSN = "file:/var/lib/xboard/other.db"
	if _, err := NewOrchestrator(platform, journal, time.Now).Rollback(context.Background()); err == nil || !strings.Contains(err.Error(), "DSN") {
		t.Fatalf("Rollback drift error = %v", err)
	}
	if !reflect.DeepEqual(platform.actions, []string{"current"}) {
		t.Fatalf("recovery crossed drift gate: %v", platform.actions)
	}
}

func rollbackFixture(t *testing.T, status string) (*fakePlatform, *Journal) {
	t.Helper()
	previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
	target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	journal := NewJournal(filepath.Join(t.TempDir(), "journal.jsonl"))
	if err := journal.Append(State{Version: StateVersion, Status: status, UpdatedAt: time.Now().UTC(), Previous: &previous, Target: &target, BackupPath: "/var/lib/xboard-backups/before.xbbackup", BackupManifest: ptrManifest(testBackupManifest(previous.Revision)), OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN}); err != nil {
		t.Fatal(err)
	}
	return &fakePlatform{current: healthyApplication(target, defaultDatabaseDSN)}, journal
}

func TestFailedUpgradePreservesPostBackupWrites(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	path := filepath.Join(directory, "active.db")
	database, err := store.OpenSQLite("file:" + filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TABLE durability_events (id INTEGER PRIMARY KEY); INSERT INTO durability_events VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
	target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	platform := &sqliteUpgradePlatform{fakePlatform: &fakePlatform{current: healthyApplication(previous, defaultDatabaseDSN), images: map[string]Image{"candidate": target}}, directory: directory, database: raw, target: target, activePath: path}
	journal := NewJournal(filepath.Join(directory, "journal.jsonl"))
	result, upgradeErr := NewOrchestrator(platform, journal, time.Now).Upgrade(ctx, "candidate")
	active, err := sql.Open("sqlite", "file:"+filepath.ToSlash(platform.activePath))
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	var count int
	if err := active.QueryRow(`SELECT COUNT(*) FROM durability_events`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("accepted post-backup write was lost: active count=%d, path=%s", count, platform.activePath)
	}
	if upgradeErr == nil || result.Status != "failed" || result.State.Status != StatusTargetFailed || result.State.ActiveDSN != defaultDatabaseDSN || platform.stops != 1 || platform.restores != 0 {
		t.Fatalf("unsafe failure result=%#v err=%v stops=%d restores=%d", result, upgradeErr, platform.stops, platform.restores)
	}
	if _, err := backup.Verify(ctx, filepath.Join(directory, "before.xbbackup")); err != nil {
		t.Fatalf("backup was not retained: %v", err)
	}
	state, err := journal.Load()
	if err != nil || state.Status != StatusTargetFailed || state.RestoredPath != "" {
		t.Fatalf("journal=%#v, %v", state, err)
	}
}

type sqliteUpgradePlatform struct {
	*fakePlatform
	directory       string
	database        *sql.DB
	target          Image
	activePath      string
	stops, restores int
}

func (platform *sqliteUpgradePlatform) Backup(ctx context.Context, current Application, _ string) (backup.Manifest, error) {
	return backup.Create(ctx, "file:"+filepath.ToSlash(platform.activePath), filepath.Join(platform.directory, "before.xbbackup"), current.Image.Revision, time.Now())
}
func (platform *sqliteUpgradePlatform) Activate(ctx context.Context, image Image, dsn string) (Application, error) {
	if image == platform.target {
		if _, err := platform.database.ExecContext(ctx, `INSERT INTO durability_events VALUES (2)`); err != nil {
			return Application{}, err
		}
		platform.current = healthyApplication(image, dsn)
		platform.current.Healthy = false
		return platform.current, errors.New("candidate unhealthy after accepting a write")
	}
	platform.activePath = filepath.Join(platform.directory, "restored.db")
	return platform.fakePlatform.Activate(ctx, image, dsn)
}
func (platform *sqliteUpgradePlatform) Restore(ctx context.Context, _ Image, _, _, _ string) (backup.Manifest, error) {
	platform.restores++
	return backup.Restore(ctx, filepath.Join(platform.directory, "before.xbbackup"), filepath.Join(platform.directory, "restored.db"))
}
func (platform *sqliteUpgradePlatform) Stop(context.Context) error { platform.stops++; return nil }

func TestFailedUpgradeStopsWithIndependentContextAndReportsCleanupFailure(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "active", true: "cancelled"}[cancelled], func(t *testing.T) {
			previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
			target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			platform := &cleanupPlatform{fakePlatform: &fakePlatform{current: healthyApplication(previous, defaultDatabaseDSN), images: map[string]Image{"candidate": target}}, stopErr: errors.New("stop rejected")}
			if cancelled {
				platform.cancel = cancel
			}
			result, err := NewOrchestrator(platform, NewJournal(filepath.Join(t.TempDir(), "journal.jsonl")), time.Now).Upgrade(ctx, "candidate")
			if err == nil || !strings.Contains(err.Error(), "candidate failed") || !strings.Contains(err.Error(), "stop rejected") || result.Status != "failed" || platform.stops != 1 {
				t.Fatalf("cleanup not reported: %#v %v stops=%d", result, err, platform.stops)
			}
			if platform.stopCancelled || !platform.stopBounded {
				t.Fatalf("cleanup context cancelled=%v bounded=%v", platform.stopCancelled, platform.stopBounded)
			}
			if slicesContainPrefix(platform.actions, "restore:") {
				t.Fatalf("restored snapshot: %v", platform.actions)
			}
		})
	}
}

type cleanupPlatform struct {
	*fakePlatform
	cancel                     context.CancelFunc
	stopErr                    error
	stops                      int
	stopCancelled, stopBounded bool
}

func (platform *cleanupPlatform) Activate(_ context.Context, _ Image, _ string) (Application, error) {
	if platform.cancel != nil {
		platform.cancel()
	}
	return Application{}, errors.New("candidate failed")
}
func (platform *cleanupPlatform) Stop(ctx context.Context) error {
	platform.stops++
	platform.stopCancelled = ctx.Err() != nil
	deadline, ok := ctx.Deadline()
	platform.stopBounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= time.Minute
	return platform.stopErr
}

func TestFailedBackendUpgradeStopsWithoutRestoringSnapshot(t *testing.T) {
	previous := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := resolvedTestDeployment(strings.Repeat("b", 40), "1", "2", "4")
	platform := &cleanupDeploymentPlatform{fakeDeploymentPlatform: &fakeDeploymentPlatform{
		current:  DeploymentApplication{Deployment: previous, DSN: defaultDatabaseDSN, Healthy: true},
		resolved: target, backupManifest: testBackupManifest(previous.Backend.Revision), activateErrors: []error{errors.New("candidate failed")},
	}}
	journal := &memoryDeploymentStateStore{}
	result, err := NewDeploymentOrchestrator(platform, journal, time.Now).Upgrade(context.Background(), manifestForDeployment(target))
	if err == nil || result.Status != "failed" || result.State.Status != StatusTargetFailed || result.State.ActiveDSN != defaultDatabaseDSN || platform.stops != 1 || platform.restores != 0 || len(platform.targets) != 1 {
		t.Fatalf("unsafe backend failure: %#v %v stops=%d restores=%d targets=%d", result, err, platform.stops, platform.restores, len(platform.targets))
	}
}

func TestFailedFrontendUpgradeStillRollsBackWithoutDatabaseChanges(t *testing.T) {
	previous := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := previous
	target.Frontend = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: strings.Repeat("b", 40)}
	target.ID = deploymentFingerprint(target.Gateway, target.Frontend, target.Backend)
	platform := &cleanupDeploymentPlatform{fakeDeploymentPlatform: &fakeDeploymentPlatform{
		current:  DeploymentApplication{Deployment: previous, DSN: defaultDatabaseDSN, Healthy: true},
		resolved: target, activateErrors: []error{errors.New("frontend failed"), nil},
	}}
	result, err := NewDeploymentOrchestrator(platform, &memoryDeploymentStateStore{}, time.Now).Upgrade(context.Background(), manifestForDeployment(target))
	if err == nil || result.State.Status != StatusAutoRolledBack || platform.restores != 0 || platform.backups != 0 || platform.stops != 0 || len(platform.targets) != 2 || platform.targets[1] != previous {
		t.Fatalf("frontend recovery regressed: %#v %v platform=%#v", result, err, platform)
	}
}

func TestDeploymentRollbackAcceptsRecordedFailedTarget(t *testing.T) {
	previous := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := resolvedTestDeployment(strings.Repeat("b", 40), "4", "5", "6")
	platform := &fakeDeploymentPlatform{current: DeploymentApplication{Deployment: target, DSN: defaultDatabaseDSN}, backupManifest: testBackupManifest(previous.Backend.Revision)}
	journal := &memoryDeploymentStateStore{states: []DeploymentState{{Version: DeploymentStateVersion, Status: StatusTargetFailed, UpdatedAt: time.Now(), Previous: &previous, Target: &target, Changed: []Component{ComponentBackend, ComponentFrontend, ComponentGateway}, BackupPath: "/var/lib/xboard-backups/before.xbbackup", BackupManifest: ptrManifest(platform.backupManifest), ActiveDSN: defaultDatabaseDSN}}}
	result, err := NewDeploymentOrchestrator(platform, journal, time.Now).Rollback(context.Background())
	if err != nil || result.Status != "success" || result.State.Status != StatusRolledBack || platform.restores != 1 {
		t.Fatalf("explicit failure recovery=%#v %v", result, err)
	}
}

type cleanupDeploymentPlatform struct {
	*fakeDeploymentPlatform
	stops                      int
	stopErr                    error
	cancel                     context.CancelFunc
	stopCancelled, stopBounded bool
}

func (platform *cleanupDeploymentPlatform) ActivateDeployment(ctx context.Context, target Deployment, dsn string, changed []Component) (DeploymentApplication, error) {
	application, err := platform.fakeDeploymentPlatform.ActivateDeployment(ctx, target, dsn, changed)
	if platform.cancel != nil {
		platform.cancel()
	}
	return application, err
}
func (platform *cleanupDeploymentPlatform) StopDeployment(ctx context.Context) error {
	platform.stops++
	platform.stopCancelled = ctx.Err() != nil
	deadline, ok := ctx.Deadline()
	platform.stopBounded = ok && time.Until(deadline) > 0 && time.Until(deadline) <= time.Minute
	return platform.stopErr
}

func TestUpgradePreActivationFailuresLeaveCurrentApplicationAlone(t *testing.T) {
	for _, failure := range []string{"resolve", "backup", "journal"} {
		t.Run(failure, func(t *testing.T) {
			previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
			target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
			platform := &fakePlatform{current: healthyApplication(previous, defaultDatabaseDSN), images: map[string]Image{"candidate": target}}
			journal := NewJournal(filepath.Join(t.TempDir(), "journal.jsonl"))
			switch failure {
			case "resolve":
				platform.images = nil
			case "backup":
				platform.backupManifest = backup.Manifest{AppRevision: target.Revision}
			case "journal":
				if err := os.WriteFile(journal.Path(), []byte("{\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := NewOrchestrator(platform, journal, time.Now).Upgrade(context.Background(), "candidate"); err == nil {
				t.Fatal("expected pre-activation failure")
			}
			if slicesContainPrefix(platform.actions, "activate:") || slicesContainPrefix(platform.actions, "restore:") || slicesContain(platform.actions, "stop") || platform.current.Image != previous {
				t.Fatalf("preflight changed active application: %v", platform.actions)
			}
		})
	}
}

func TestUpgradeJournalFailureDoesNotPreventStoppingCandidate(t *testing.T) {
	for _, activationFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "record_success_failure", true: "record_failure_failure"}[activationFails], func(t *testing.T) {
			previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
			target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
			journal := NewJournal(filepath.Join(t.TempDir(), "journal.jsonl"))
			platform := &journalFailurePlatform{fakePlatform: &fakePlatform{current: healthyApplication(previous, defaultDatabaseDSN), images: map[string]Image{"candidate": target}}, journalPath: journal.Path(), activationFails: activationFails}
			result, err := NewOrchestrator(platform, journal, time.Now).Upgrade(context.Background(), "candidate")
			if err == nil || result.Status != "failed" || !strings.Contains(err.Error(), "journal") || !slicesContain(platform.actions, "stop") || slicesContainPrefix(platform.actions, "restore:") {
				t.Fatalf("record failure escaped stop: %#v %v actions=%v", result, err, platform.actions)
			}
		})
	}
}

type journalFailurePlatform struct {
	*fakePlatform
	journalPath     string
	activationFails bool
}

func (platform *journalFailurePlatform) Activate(ctx context.Context, image Image, dsn string) (Application, error) {
	application, err := platform.fakePlatform.Activate(ctx, image, dsn)
	if err != nil {
		return application, err
	}
	if err := os.WriteFile(platform.journalPath, []byte("{\n"), 0o600); err != nil {
		return application, err
	}
	if platform.activationFails {
		return application, errors.New("candidate failed")
	}
	return application, nil
}

func TestFailedBackendUpgradeReportsStopAndJournalErrors(t *testing.T) {
	for _, activationFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "record_success_failure", true: "record_failure_failure"}[activationFails], func(t *testing.T) {
			previous := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
			target := resolvedTestDeployment(strings.Repeat("b", 40), "4", "5", "6")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			platform := &cleanupDeploymentPlatform{fakeDeploymentPlatform: &fakeDeploymentPlatform{current: DeploymentApplication{Deployment: previous, DSN: defaultDatabaseDSN, Healthy: true}, resolved: target, backupManifest: testBackupManifest(previous.Backend.Revision)}, stopErr: errors.New("stop rejected"), cancel: cancel}
			if activationFails {
				platform.activateErrors = []error{errors.New("candidate failed")}
			}
			journal := &failingDeploymentJournal{}
			result, err := NewDeploymentOrchestrator(platform, journal, time.Now).Upgrade(ctx, manifestForDeployment(target))
			if err == nil || result.Status != "failed" || platform.stops != 1 || platform.restores != 0 || !strings.Contains(err.Error(), "stop rejected") || !strings.Contains(err.Error(), "journal unavailable") || platform.stopCancelled || !platform.stopBounded {
				t.Fatalf("backend failure=%#v err=%v platform=%#v", result, err, platform)
			}
		})
	}
}

type failingDeploymentJournal struct{ memoryDeploymentStateStore }

func (journal *failingDeploymentJournal) Append(state DeploymentState) error {
	if len(journal.states) > 0 {
		return errors.New("journal unavailable")
	}
	return journal.memoryDeploymentStateStore.Append(state)
}

func TestDockerStopUsesOnlyConfiguredContainerAndPropagatesFailure(t *testing.T) {
	for _, topology := range []string{"combined", "split"} {
		for _, fails := range []bool{false, true} {
			t.Run(topology+"/"+map[bool]string{false: "success", true: "failure"}[fails], func(t *testing.T) {
				platform, runner := newDockerTestPlatform(t)
				var commandErr error
				if fails {
					commandErr = errors.New("Docker unavailable")
				}
				runner.outputs = []runnerResult{{err: commandErr}}
				var err error
				container := "lifecycle-test-xboard-go-1"
				if topology == "split" {
					container = "lifecycle-test-backend-1"
					err = platform.StopDeployment(context.Background())
				} else {
					err = platform.Stop(context.Background())
				}
				if !errors.Is(err, commandErr) {
					t.Fatalf("stop error=%v, want %v", err, commandErr)
				}
				want := []commandCall{{name: "docker", arguments: []string{"container", "stop", "--time", "15", container}}}
				if !reflect.DeepEqual(runner.calls, want) {
					t.Fatalf("unexpected stop scope: %#v", runner.calls)
				}
			})
		}
	}
}

type preStartFailurePlatform struct {
	*fakePlatform
	docker *DockerPlatform
	target Image
	stops  int
}

func (p *preStartFailurePlatform) Activate(ctx context.Context, image Image, dsn string) (Application, error) {
	if image.ID == p.target.ID {
		return p.docker.Activate(ctx, image, dsn)
	}
	return p.fakePlatform.Activate(ctx, image, dsn)
}

func (p *preStartFailurePlatform) Stop(context.Context) error {
	p.stops++
	p.current.Healthy = false
	p.current.ContainerStatus = "exited"
	return nil
}

func TestUpgradePreStartPreparationFailurePreservesHealthyPrevious(t *testing.T) {
	previous := Image{ID: "sha256:" + strings.Repeat("1", 64), Revision: strings.Repeat("a", 40)}
	target := Image{ID: "sha256:" + strings.Repeat("2", 64), Revision: strings.Repeat("b", 40)}
	docker, runner := newDockerTestPlatform(t)
	runner.outputs = []runnerResult{{err: errors.New("synthetic image tag failure")}}
	platform := &preStartFailurePlatform{
		fakePlatform: &fakePlatform{
			current: healthyApplication(previous, defaultDatabaseDSN),
			images:  map[string]Image{"candidate": target},
		},
		docker: docker,
		target: target,
	}
	orchestrator := NewOrchestrator(platform, NewJournal(filepath.Join(t.TempDir(), "journal.jsonl")), time.Now)
	result, err := orchestrator.Upgrade(context.Background(), "candidate")
	if err == nil {
		t.Fatal("expected pre-start preparation failure")
	}
	if len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0].arguments, " "), "image tag") {
		t.Fatalf("unexpected candidate commands: %#v", runner.calls)
	}
	if !platform.current.Healthy || platform.stops != 0 {
		t.Fatalf("pre-start failure stopped the healthy previous version: healthy=%v stops=%d", platform.current.Healthy, platform.stops)
	}
	if result.State == nil || result.State.Status != StatusAutoRolledBack {
		t.Fatalf("state = %#v, want %q", result.State, StatusAutoRolledBack)
	}
}

type splitPreStartFailurePlatform struct {
	*fakeDeploymentPlatform
	docker *DockerPlatform
	target Deployment
	stops  int
}

func (p *splitPreStartFailurePlatform) ActivateDeployment(ctx context.Context, target Deployment, dsn string, changed []Component) (DeploymentApplication, error) {
	if sameDeploymentImages(target, p.target) {
		return p.docker.ActivateDeployment(ctx, target, dsn, changed)
	}
	return p.fakeDeploymentPlatform.ActivateDeployment(ctx, target, dsn, changed)
}

func (p *splitPreStartFailurePlatform) StopDeployment(context.Context) error {
	p.stops++
	p.current.Healthy = false
	return nil
}

func TestSplitUpgradePreStartPreparationFailurePreservesHealthyPrevious(t *testing.T) {
	previous := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := previous
	target.SourceRevision = strings.Repeat("b", 40)
	target.Backend = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: target.SourceRevision}
	target.ID = deploymentFingerprint(target.Gateway, target.Frontend, target.Backend)
	docker, runner := newDockerTestPlatform(t)
	runner.outputs = []runnerResult{{err: errors.New("synthetic image tag failure")}}
	platform := &splitPreStartFailurePlatform{
		fakeDeploymentPlatform: &fakeDeploymentPlatform{
			current:        DeploymentApplication{Deployment: previous, DSN: defaultDatabaseDSN, Healthy: true},
			resolved:       target,
			backupManifest: testBackupManifest(previous.Backend.Revision),
		},
		docker: docker,
		target: target,
	}
	orchestrator := NewDeploymentOrchestrator(platform, &memoryDeploymentStateStore{}, time.Now)
	result, err := orchestrator.Upgrade(context.Background(), manifestForDeployment(target))
	if err == nil {
		t.Fatal("expected pre-start preparation failure")
	}
	if len(runner.calls) != 1 || !strings.Contains(strings.Join(runner.calls[0].arguments, " "), "image tag") {
		t.Fatalf("unexpected candidate commands: %#v", runner.calls)
	}
	if !platform.current.Healthy || platform.stops != 0 {
		t.Fatalf("pre-start failure stopped the healthy previous backend: healthy=%v stops=%d", platform.current.Healthy, platform.stops)
	}
	if result.State == nil || result.State.Status != StatusAutoRolledBack {
		t.Fatalf("state = %#v, want %q", result.State, StatusAutoRolledBack)
	}
	if statusRes, statusErr := orchestrator.Status(context.Background()); statusErr != nil || statusRes.Application == nil || !statusRes.Application.Healthy {
		t.Fatalf("Status() after pre-start failure = (%#v, %v), want healthy previous deployment", statusRes, statusErr)
	}
}

func TestSplitRollbackRestoreFailureOnPreviousAllowsStatusAndRetry(t *testing.T) {
	now := time.Date(2026, 9, 5, 15, 0, 0, 0, time.UTC)
	previous := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := previous
	target.SourceRevision = strings.Repeat("b", 40)
	target.Backend = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: target.SourceRevision}
	target.ID = deploymentFingerprint(target.Gateway, target.Frontend, target.Backend)
	verified := testBackupManifest(previous.Backend.Revision)
	store := &memoryDeploymentStateStore{states: []DeploymentState{{
		Version: DeploymentStateVersion, Status: StatusTargetFailed, UpdatedAt: now, Changed: []Component{ComponentBackend},
		Previous: &previous, Target: &target, BackupPath: "/var/lib/xboard-backups/lifecycle-20260905T150000.000000000Z-aaaaaaaaaaaa.xbbackup",
		BackupManifest: &verified, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN,
	}}}
	runtimePrevious := previous
	runtimePrevious.SourceRevision = ""
	platform := &fakeDeploymentPlatform{
		current:        DeploymentApplication{Deployment: runtimePrevious, DSN: defaultDatabaseDSN, Healthy: false},
		backupManifest: verified,
		restoreErr:     errors.New("temporary restore failure"),
	}
	orchestrator := NewDeploymentOrchestrator(platform, store, func() time.Time { return now })
	if _, err := orchestrator.Rollback(context.Background()); err == nil {
		t.Fatal("expected first Rollback() to fail during restore")
	}
	statusRes, err := orchestrator.Status(context.Background())
	if err != nil || statusRes.Application == nil || statusRes.Application.Deployment.SourceRevision != previous.SourceRevision {
		t.Fatalf("Status() after restore failure on Previous = (%#v, %v), want SourceRevision %q", statusRes, err, previous.SourceRevision)
	}
	platform.restoreErr = nil
	res, err := orchestrator.Rollback(context.Background())
	if err != nil || res.Status != "success" || res.State == nil || res.State.Status != StatusRolledBack {
		t.Fatalf("retry Rollback() = (%#v, %v), want success StatusRolledBack", res, err)
	}
}
