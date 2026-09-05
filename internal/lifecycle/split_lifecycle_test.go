package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
)

func TestDeploymentUpgradeReplacesOnlyChangedFrontendWithoutDatabaseBackup(t *testing.T) {
	now := time.Date(2026, 9, 5, 13, 0, 0, 0, time.UTC)
	current := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := current
	target.SourceRevision = strings.Repeat("b", 40)
	target.Frontend = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: target.SourceRevision}
	target.ID = deploymentFingerprint(target.Gateway, target.Frontend, target.Backend)
	platform := &fakeDeploymentPlatform{
		current:  DeploymentApplication{Deployment: current, DSN: defaultDatabaseDSN, Healthy: true},
		resolved: target,
	}
	store := &memoryDeploymentStateStore{}
	result, err := NewDeploymentOrchestrator(platform, store, func() time.Time { return now }).Upgrade(context.Background(), manifestForDeployment(target))
	if err != nil || result.Status != "success" || len(store.states) != 2 {
		t.Fatalf("Upgrade() = (%#v, %v), states=%#v", result, err, store.states)
	}
	if platform.backups != 0 || platform.restores != 0 {
		t.Fatalf("database operations = backups:%d restores:%d", platform.backups, platform.restores)
	}
	if !reflect.DeepEqual(platform.activations, [][]Component{{ComponentFrontend}}) {
		t.Fatalf("activations = %v", platform.activations)
	}
}

func TestDeploymentInstallActivatesAllComponentsWithoutBackup(t *testing.T) {
	target := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	platform := &fakeDeploymentPlatform{resolved: target}
	store := &memoryDeploymentStateStore{}
	result, err := NewDeploymentOrchestrator(platform, store, time.Now).Install(context.Background(), manifestForDeployment(target))
	if err != nil || result.Status != "success" {
		t.Fatalf("Install() = (%#v, %v)", result, err)
	}
	want := []Component{ComponentGateway, ComponentFrontend, ComponentBackend}
	if len(platform.activations) != 1 || !reflect.DeepEqual(platform.activations[0], want) || platform.backups != 0 {
		t.Fatalf("activations=%v backups=%d", platform.activations, platform.backups)
	}
}

func TestDeploymentFrontendRollbackDoesNotRestoreDatabase(t *testing.T) {
	previous := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := previous
	target.SourceRevision = strings.Repeat("b", 40)
	target.Frontend = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: target.SourceRevision}
	target.ID = deploymentFingerprint(target.Gateway, target.Frontend, target.Backend)
	store := &memoryDeploymentStateStore{states: []DeploymentState{{
		Version: DeploymentStateVersion, Status: StatusUpgraded, UpdatedAt: time.Now(), Changed: []Component{ComponentFrontend},
		Previous: &previous, Target: &target, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN,
	}}}
	platform := &fakeDeploymentPlatform{current: DeploymentApplication{Deployment: target, DSN: defaultDatabaseDSN, Healthy: true}}
	result, err := NewDeploymentOrchestrator(platform, store, time.Now).Rollback(context.Background())
	if err != nil || result.Status != "success" || platform.restores != 0 {
		t.Fatalf("Rollback() = (%#v, %v), restores=%d", result, err, platform.restores)
	}
	if len(platform.targets) != 1 || !sameDeploymentImages(platform.targets[0], previous) {
		t.Fatalf("targets = %#v", platform.targets)
	}
}

func TestDeploymentRollbackAllowsUnhealthyRecordedTarget(t *testing.T) {
	previous := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := previous
	target.SourceRevision = strings.Repeat("b", 40)
	target.Frontend = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: target.SourceRevision}
	target.ID = deploymentFingerprint(target.Gateway, target.Frontend, target.Backend)
	store := &memoryDeploymentStateStore{states: []DeploymentState{{
		Version: DeploymentStateVersion, Status: StatusUpgraded, UpdatedAt: time.Now(), Changed: []Component{ComponentFrontend},
		Previous: &previous, Target: &target, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN,
	}}}
	platform := &fakeDeploymentPlatform{current: DeploymentApplication{Deployment: target, DSN: defaultDatabaseDSN, Healthy: false}}
	result, err := NewDeploymentOrchestrator(platform, store, time.Now).Rollback(context.Background())
	if err != nil || result.Status != "success" {
		t.Fatalf("Rollback() = (%#v, %v)", result, err)
	}
	if len(platform.targets) != 1 || !sameDeploymentImages(platform.targets[0], previous) {
		t.Fatalf("targets = %#v", platform.targets)
	}
}

func TestDeploymentStatusRejectsRuntimeImageDrift(t *testing.T) {
	recorded := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	active := recorded
	active.Gateway = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: strings.Repeat("b", 40)}
	active.ID = deploymentFingerprint(active.Gateway, active.Frontend, active.Backend)
	store := &memoryDeploymentStateStore{states: []DeploymentState{
		{Version: DeploymentStateVersion, Status: StatusInstalled, UpdatedAt: time.Now(), Target: &recorded, Changed: []Component{ComponentGateway, ComponentFrontend, ComponentBackend}, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN},
	}}
	platform := &fakeDeploymentPlatform{current: DeploymentApplication{Deployment: active, DSN: defaultDatabaseDSN, Healthy: true}}
	if _, err := NewDeploymentOrchestrator(platform, store, time.Now).Status(context.Background()); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("Status() error = %v", err)
	}
}

func TestDeploymentUpgradeRejectsRuntimeImageDriftBeforeResolvingTarget(t *testing.T) {
	recorded := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	active := recorded
	active.Gateway = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: strings.Repeat("b", 40)}
	active.ID = deploymentFingerprint(active.Gateway, active.Frontend, active.Backend)
	store := &memoryDeploymentStateStore{states: []DeploymentState{
		{Version: DeploymentStateVersion, Status: StatusInstalled, UpdatedAt: time.Now(), Target: &recorded, Changed: []Component{ComponentGateway, ComponentFrontend, ComponentBackend}, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN},
	}}
	platform := &fakeDeploymentPlatform{current: DeploymentApplication{Deployment: active, DSN: defaultDatabaseDSN, Healthy: true}}
	_, err := NewDeploymentOrchestrator(platform, store, time.Now).Upgrade(context.Background(), manifestForDeployment(recorded))
	if err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("Upgrade() error = %v", err)
	}
	if platform.resolves != 0 || len(platform.activations) != 0 || platform.backups != 0 {
		t.Fatalf("upgrade crossed drift gate: resolves=%d activations=%d backups=%d", platform.resolves, len(platform.activations), platform.backups)
	}
}

func TestDeploymentStatusRejectsRuntimeDatabaseDrift(t *testing.T) {
	recorded := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	store := &memoryDeploymentStateStore{states: []DeploymentState{
		{Version: DeploymentStateVersion, Status: StatusInstalled, UpdatedAt: time.Now(), Target: &recorded, Changed: []Component{ComponentGateway, ComponentFrontend, ComponentBackend}, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN},
	}}
	platform := &fakeDeploymentPlatform{current: DeploymentApplication{Deployment: recorded, DSN: "file:/var/lib/xboard/xboard-other.db", Healthy: true}}
	if _, err := NewDeploymentOrchestrator(platform, store, time.Now).Status(context.Background()); err == nil || !strings.Contains(err.Error(), "database DSN drifts") {
		t.Fatalf("Status() error = %v", err)
	}
}

func TestDeploymentRollbackRejectsRuntimeDatabaseDriftBeforeRestoreOrActivation(t *testing.T) {
	previous := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := previous
	target.SourceRevision = strings.Repeat("b", 40)
	target.Backend = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: target.SourceRevision}
	target.ID = deploymentFingerprint(target.Gateway, target.Frontend, target.Backend)
	verified := testBackupManifest(previous.Backend.Revision)
	store := &memoryDeploymentStateStore{states: []DeploymentState{{
		Version: DeploymentStateVersion, Status: StatusUpgraded, UpdatedAt: time.Now(), Changed: []Component{ComponentBackend},
		Previous: &previous, Target: &target, BackupPath: "/var/lib/xboard-backups/lifecycle-20260905T130000.000000000Z-aaaaaaaaaaaa.xbbackup",
		BackupManifest: &verified, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN,
	}}}
	platform := &fakeDeploymentPlatform{
		current:        DeploymentApplication{Deployment: target, DSN: "file:/var/lib/xboard/xboard-other.db", Healthy: true},
		backupManifest: verified,
	}
	_, err := NewDeploymentOrchestrator(platform, store, time.Now).Rollback(context.Background())
	if err == nil || !strings.Contains(err.Error(), "database DSN drifts") {
		t.Fatalf("Rollback() error = %v", err)
	}
	if platform.restores != 0 || len(platform.activations) != 0 {
		t.Fatalf("rollback crossed drift gate: restores=%d activations=%d", platform.restores, len(platform.activations))
	}
}

func TestDeploymentUpgradePreservesRecordedSourceRevisionForRollback(t *testing.T) {
	recorded := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	current := recorded
	current.SourceRevision = ""
	target := recorded
	target.SourceRevision = strings.Repeat("b", 40)
	target.Frontend = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: target.SourceRevision}
	target.ID = deploymentFingerprint(target.Gateway, target.Frontend, target.Backend)
	store := &memoryDeploymentStateStore{states: []DeploymentState{
		{Version: DeploymentStateVersion, Status: StatusInstalled, UpdatedAt: time.Now(), Target: &recorded, Changed: []Component{ComponentGateway, ComponentFrontend, ComponentBackend}, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN},
	}}
	platform := &fakeDeploymentPlatform{
		current: DeploymentApplication{Deployment: current, DSN: defaultDatabaseDSN, Healthy: true}, resolved: target,
	}
	result, err := NewDeploymentOrchestrator(platform, store, time.Now).Upgrade(context.Background(), manifestForDeployment(target))
	if err != nil || result.State == nil || result.State.Previous == nil {
		t.Fatalf("Upgrade() = (%#v, %v)", result, err)
	}
	if result.State.Previous.SourceRevision != recorded.SourceRevision {
		t.Fatalf("previous source revision = %q, want %q", result.State.Previous.SourceRevision, recorded.SourceRevision)
	}
}

func TestDeploymentStatusReportsAnUnhealthyButIdentifiableDeployment(t *testing.T) {
	deployment := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	store := &memoryDeploymentStateStore{states: []DeploymentState{
		{Version: DeploymentStateVersion, Status: StatusInstalled, UpdatedAt: time.Now(), Target: &deployment, Changed: []Component{ComponentGateway, ComponentFrontend, ComponentBackend}, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN},
	}}
	platform := &fakeDeploymentPlatform{current: DeploymentApplication{Deployment: deployment, DSN: defaultDatabaseDSN, Healthy: false}}
	result, err := NewDeploymentOrchestrator(platform, store, time.Now).Status(context.Background())
	if err != nil || result.Application == nil || result.Application.Healthy {
		t.Fatalf("Status() = (%#v, %v)", result, err)
	}
}

func TestDeploymentJournalRoundTripsValidatedState(t *testing.T) {
	deployment := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	want := DeploymentState{
		Version: DeploymentStateVersion, Status: StatusInstalled, UpdatedAt: time.Now().UTC(),
		Target: &deployment, Changed: []Component{ComponentGateway, ComponentFrontend, ComponentBackend}, OriginalDSN: defaultDatabaseDSN, ActiveDSN: defaultDatabaseDSN,
	}
	journal := NewDeploymentJournal(t.TempDir() + "/split.jsonl")
	if err := journal.Append(want); err != nil {
		t.Fatal(err)
	}
	got, err := journal.Load()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = (%#v, %v), want %#v", got, err, want)
	}
}

func TestDeploymentBackendFailureRestoresBackupAndExactPreviousCombination(t *testing.T) {
	now := time.Date(2026, 9, 5, 13, 1, 0, 0, time.UTC)
	current := resolvedTestDeployment(strings.Repeat("a", 40), "1", "2", "3")
	target := current
	target.SourceRevision = strings.Repeat("b", 40)
	target.Backend = Image{ID: "sha256:" + strings.Repeat("4", 64), Revision: target.SourceRevision}
	target.ID = deploymentFingerprint(target.Gateway, target.Frontend, target.Backend)
	verified := testBackupManifest(current.Backend.Revision)
	platform := &fakeDeploymentPlatform{
		current:  DeploymentApplication{Deployment: current, DSN: defaultDatabaseDSN, Healthy: true},
		resolved: target, backupManifest: verified, activateErrors: []error{errors.New("candidate unhealthy"), nil},
	}
	store := &memoryDeploymentStateStore{}
	result, err := NewDeploymentOrchestrator(platform, store, func() time.Time { return now }).Upgrade(context.Background(), manifestForDeployment(target))
	if err == nil || !strings.Contains(err.Error(), "automatically rolled back") || result.Status != "failed" {
		t.Fatalf("Upgrade() = (%#v, %v)", result, err)
	}
	if platform.backups != 1 || platform.restores != 1 {
		t.Fatalf("database operations = backups:%d restores:%d", platform.backups, platform.restores)
	}
	if len(platform.targets) != 2 || platform.targets[0] != target || platform.targets[1] != current {
		t.Fatalf("activation targets = %#v", platform.targets)
	}
	if last := store.states[len(store.states)-1]; last.Status != StatusAutoRolledBack || last.ActiveDSN == defaultDatabaseDSN {
		t.Fatalf("last state = %#v", last)
	}
}

func resolvedTestDeployment(revision, gateway, frontend, backend string) Deployment {
	image := func(marker string) Image {
		return Image{ID: "sha256:" + strings.Repeat(marker, 64), Revision: revision}
	}
	deployment := Deployment{SourceRevision: revision, Gateway: image(gateway), Frontend: image(frontend), Backend: image(backend)}
	deployment.ID = deploymentFingerprint(deployment.Gateway, deployment.Frontend, deployment.Backend)
	return deployment
}

func manifestForDeployment(deployment Deployment) DeploymentManifest {
	component := func(name string, image Image) ComponentImage {
		return ComponentImage{Reference: "registry.example/xboard-" + name + "@" + image.ID, ID: image.ID, Revision: image.Revision}
	}
	return DeploymentManifest{
		FormatVersion: DeploymentManifestVersion, Revision: deployment.SourceRevision,
		Gateway: component("gateway", deployment.Gateway), Frontend: component("frontend", deployment.Frontend), Backend: component("backend", deployment.Backend),
	}
}

type memoryDeploymentStateStore struct{ states []DeploymentState }

func (store *memoryDeploymentStateStore) Append(state DeploymentState) error {
	store.states = append(store.states, state)
	return nil
}
func (store *memoryDeploymentStateStore) Load() (DeploymentState, error) {
	if len(store.states) == 0 {
		return DeploymentState{}, ErrNoState
	}
	return store.states[len(store.states)-1], nil
}

type fakeDeploymentPlatform struct {
	current        DeploymentApplication
	resolved       Deployment
	backupManifest backup.Manifest
	activateErrors []error
	activations    [][]Component
	targets        []Deployment
	backups        int
	restores       int
	resolves       int
}

func (platform *fakeDeploymentPlatform) CurrentDeployment(context.Context) (DeploymentApplication, error) {
	return platform.current, nil
}
func (platform *fakeDeploymentPlatform) ResolveDeployment(context.Context, DeploymentManifest) (Deployment, error) {
	platform.resolves++
	return platform.resolved, nil
}
func (platform *fakeDeploymentPlatform) FreshDeployment(context.Context) (bool, error) {
	return true, nil
}
func (platform *fakeDeploymentPlatform) ActivateDeployment(_ context.Context, target Deployment, dsn string, changed []Component) (DeploymentApplication, error) {
	platform.targets = append(platform.targets, target)
	platform.activations = append(platform.activations, append([]Component(nil), changed...))
	var err error
	if len(platform.activateErrors) > 0 {
		err = platform.activateErrors[0]
		platform.activateErrors = platform.activateErrors[1:]
	}
	if err != nil {
		return DeploymentApplication{}, err
	}
	platform.current = DeploymentApplication{Deployment: target, DSN: dsn, Healthy: true}
	return platform.current, nil
}
func (platform *fakeDeploymentPlatform) BackupDeployment(context.Context, DeploymentApplication, string) (backup.Manifest, error) {
	platform.backups++
	return platform.backupManifest, nil
}
func (platform *fakeDeploymentPlatform) RestoreDeployment(context.Context, Image, string, string, string) (backup.Manifest, error) {
	platform.restores++
	return platform.backupManifest, nil
}
