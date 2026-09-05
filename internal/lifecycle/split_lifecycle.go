package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
)

const DeploymentStateVersion = 1

type DeploymentApplication struct {
	Deployment Deployment `json:"deployment"`
	DSN        string     `json:"database_dsn"`
	Healthy    bool       `json:"healthy"`
}

type DeploymentState struct {
	Version             int              `json:"version"`
	Status              string           `json:"status"`
	UpdatedAt           time.Time        `json:"updated_at"`
	Changed             []Component      `json:"changed_components,omitempty"`
	Previous            *Deployment      `json:"previous,omitempty"`
	Target              *Deployment      `json:"target,omitempty"`
	BackupPath          string           `json:"backup_path,omitempty"`
	BackupManifest      *backup.Manifest `json:"backup_manifest,omitempty"`
	OriginalDSN         string           `json:"original_database_dsn,omitempty"`
	ActiveDSN           string           `json:"active_database_dsn,omitempty"`
	RestoredPath        string           `json:"restored_path,omitempty"`
	RestoredAttachments string           `json:"restored_attachment_path,omitempty"`
	Failure             string           `json:"failure,omitempty"`
}

type DeploymentResult struct {
	Status      string                 `json:"status"`
	Action      string                 `json:"action"`
	Application *DeploymentApplication `json:"application,omitempty"`
	State       *DeploymentState       `json:"state,omitempty"`
}

type DeploymentPlatform interface {
	CurrentDeployment(context.Context) (DeploymentApplication, error)
	ResolveDeployment(context.Context, DeploymentManifest) (Deployment, error)
	FreshDeployment(context.Context) (bool, error)
	ActivateDeployment(context.Context, Deployment, string, []Component) (DeploymentApplication, error)
	BackupDeployment(context.Context, DeploymentApplication, string) (backup.Manifest, error)
	RestoreDeployment(context.Context, Image, string, string, string) (backup.Manifest, error)
}

type DeploymentStateStore interface {
	Append(DeploymentState) error
	Load() (DeploymentState, error)
}

type DeploymentOrchestrator struct {
	platform DeploymentPlatform
	store    DeploymentStateStore
	now      func() time.Time
}

func NewDeploymentOrchestrator(platform DeploymentPlatform, store DeploymentStateStore, now func() time.Time) *DeploymentOrchestrator {
	if now == nil {
		now = time.Now
	}
	return &DeploymentOrchestrator{platform: platform, store: store, now: now}
}

func (orchestrator *DeploymentOrchestrator) Status(ctx context.Context) (DeploymentResult, error) {
	application, err := orchestrator.platform.CurrentDeployment(ctx)
	if err != nil {
		return DeploymentResult{}, err
	}
	if err := validateDeploymentApplicationIdentity(application); err != nil {
		return DeploymentResult{}, err
	}
	state, managed, err := orchestrator.reconcileCurrentDeployment(&application)
	if err != nil {
		return DeploymentResult{}, err
	}
	if !managed {
		return DeploymentResult{Status: "success", Action: "lifecycle.deployment.status", Application: &application}, nil
	}
	return DeploymentResult{Status: "success", Action: "lifecycle.deployment.status", Application: &application, State: &state}, nil
}

func (orchestrator *DeploymentOrchestrator) Install(ctx context.Context, manifest DeploymentManifest) (DeploymentResult, error) {
	fresh, err := orchestrator.platform.FreshDeployment(ctx)
	if err != nil {
		return DeploymentResult{}, err
	}
	if !fresh {
		return DeploymentResult{}, errors.New("deployment install requires absent application containers and data volume")
	}
	target, err := orchestrator.platform.ResolveDeployment(ctx, manifest)
	if err != nil {
		return DeploymentResult{}, err
	}
	changed := []Component{ComponentGateway, ComponentFrontend, ComponentBackend}
	state := DeploymentState{
		Version: DeploymentStateVersion, Status: StatusInstallPrepared, UpdatedAt: orchestrator.now().UTC(),
		Changed: changed, Target: &target, OriginalDSN: defaultDatabaseDSN,
	}
	if err := orchestrator.store.Append(state); err != nil {
		return DeploymentResult{}, err
	}
	application, err := orchestrator.platform.ActivateDeployment(ctx, target, defaultDatabaseDSN, changed)
	if err != nil {
		state.Status = StatusInstallFailed
		state.UpdatedAt = orchestrator.now().UTC()
		state.Failure = safeFailure(err)
		if appendErr := orchestrator.store.Append(state); appendErr != nil {
			return DeploymentResult{}, errors.Join(err, appendErr)
		}
		return failedDeploymentResult("lifecycle.deployment.install", application, &state), fmt.Errorf("install deployment: %w", err)
	}
	state.Status = StatusInstalled
	state.UpdatedAt = orchestrator.now().UTC()
	state.ActiveDSN = defaultDatabaseDSN
	if err := orchestrator.store.Append(state); err != nil {
		return failedDeploymentResult("lifecycle.deployment.install", application, &state), err
	}
	return DeploymentResult{Status: "success", Action: "lifecycle.deployment.install", Application: &application, State: &state}, nil
}

func (orchestrator *DeploymentOrchestrator) Upgrade(ctx context.Context, manifest DeploymentManifest) (DeploymentResult, error) {
	current, err := orchestrator.platform.CurrentDeployment(ctx)
	if err != nil {
		return DeploymentResult{}, err
	}
	if err := validateDeploymentApplication(current); err != nil {
		return DeploymentResult{}, err
	}
	if _, _, err := orchestrator.reconcileCurrentDeployment(&current); err != nil {
		return DeploymentResult{}, err
	}
	target, err := orchestrator.platform.ResolveDeployment(ctx, manifest)
	if err != nil {
		return DeploymentResult{}, err
	}
	if err := validateDeployment(target); err != nil {
		return DeploymentResult{}, err
	}
	changed := target.ChangedComponents(current.Deployment)
	if len(changed) == 0 {
		return DeploymentResult{}, errors.New("target deployment is already active")
	}
	now := orchestrator.now().UTC()
	state := DeploymentState{
		Version: DeploymentStateVersion, Status: StatusPrepared, UpdatedAt: now, Changed: changed,
		Previous: &current.Deployment, Target: &target, OriginalDSN: current.DSN, ActiveDSN: current.DSN,
	}
	backendChanged := containsComponent(changed, ComponentBackend)
	if backendChanged {
		state.BackupPath = fmt.Sprintf("/var/lib/xboard-backups/lifecycle-%s-%s.xbbackup", lifecycleTimestamp(now), current.Deployment.Backend.Revision[:12])
		manifest, backupErr := orchestrator.platform.BackupDeployment(ctx, current, state.BackupPath)
		if backupErr != nil {
			return DeploymentResult{}, fmt.Errorf("create and verify pre-upgrade backup: %w", backupErr)
		}
		if manifest.AppRevision != current.Deployment.Backend.Revision {
			return DeploymentResult{}, errors.New("backup revision does not match active backend revision")
		}
		if err := validateBackupManifest(manifest); err != nil {
			return DeploymentResult{}, fmt.Errorf("validate verified pre-upgrade backup manifest: %w", err)
		}
		state.BackupManifest = &manifest
	}
	if err := orchestrator.store.Append(state); err != nil {
		return DeploymentResult{}, err
	}
	application, activationErr := orchestrator.platform.ActivateDeployment(ctx, target, current.DSN, changed)
	if activationErr == nil {
		state.Status = StatusUpgraded
		state.UpdatedAt = orchestrator.now().UTC()
		if err := orchestrator.store.Append(state); err != nil {
			return failedDeploymentResult("lifecycle.deployment.upgrade", application, &state), err
		}
		return DeploymentResult{Status: "success", Action: "lifecycle.deployment.upgrade", Application: &application, State: &state}, nil
	}
	state.Status = StatusTargetFailed
	state.UpdatedAt = orchestrator.now().UTC()
	state.Failure = safeFailure(activationErr)
	if err := orchestrator.store.Append(state); err != nil {
		return failedDeploymentResult("lifecycle.deployment.upgrade", application, &state), errors.Join(activationErr, err)
	}

	recoveryDSN := current.DSN
	if backendChanged {
		recoveryDSN, err = orchestrator.restoreDeploymentBackup(ctx, &state)
		if err != nil {
			return orchestrator.rollbackFailure("lifecycle.deployment.upgrade", state, application, activationErr, err)
		}
	}
	recovered, recoveryErr := orchestrator.platform.ActivateDeployment(ctx, current.Deployment, recoveryDSN, changed)
	if recoveryErr != nil {
		return orchestrator.rollbackFailure("lifecycle.deployment.upgrade", state, recovered, activationErr, recoveryErr)
	}
	state.Status = StatusAutoRolledBack
	state.UpdatedAt = orchestrator.now().UTC()
	state.ActiveDSN = recoveryDSN
	result := failedDeploymentResult("lifecycle.deployment.upgrade", recovered, &state)
	operationErr := fmt.Errorf("target deployment failed and was automatically rolled back: %w", activationErr)
	if err := orchestrator.store.Append(state); err != nil {
		operationErr = errors.Join(operationErr, err)
	}
	return result, operationErr
}

func (orchestrator *DeploymentOrchestrator) reconcileCurrentDeployment(application *DeploymentApplication) (DeploymentState, bool, error) {
	state, err := orchestrator.store.Load()
	if errors.Is(err, ErrNoState) {
		if application.Deployment.SourceRevision == "" &&
			application.Deployment.Gateway.Revision == application.Deployment.Frontend.Revision &&
			application.Deployment.Frontend.Revision == application.Deployment.Backend.Revision {
			application.Deployment.SourceRevision = application.Deployment.Backend.Revision
		}
		return DeploymentState{}, false, nil
	}
	if err != nil {
		return DeploymentState{}, false, err
	}
	expected := expectedDeploymentForState(state)
	if expected == nil {
		return DeploymentState{}, false, errors.New("lifecycle journal does not identify the expected active deployment")
	}
	if !sameDeploymentImages(application.Deployment, *expected) {
		return DeploymentState{}, false, errors.New("active deployment images drift from the lifecycle journal")
	}
	if state.ActiveDSN != "" && application.DSN != state.ActiveDSN {
		return DeploymentState{}, false, errors.New("active deployment database DSN drifts from the lifecycle journal")
	}
	application.Deployment.SourceRevision = expected.SourceRevision
	return state, true, nil
}

func (orchestrator *DeploymentOrchestrator) Rollback(ctx context.Context) (DeploymentResult, error) {
	state, err := orchestrator.store.Load()
	if err != nil {
		return DeploymentResult{}, err
	}
	if state.Status != StatusUpgraded || state.Previous == nil || state.Target == nil || len(state.Changed) == 0 {
		return DeploymentResult{}, fmt.Errorf("deployment rollback requires complete lifecycle status %q", StatusUpgraded)
	}
	current, err := orchestrator.platform.CurrentDeployment(ctx)
	if err != nil {
		return DeploymentResult{}, err
	}
	if err := validateDeploymentApplication(current); err != nil {
		return DeploymentResult{}, err
	}
	if !sameDeploymentImages(current.Deployment, *state.Target) {
		return DeploymentResult{}, errors.New("active deployment does not match the recorded rollback target")
	}
	if state.ActiveDSN != "" && current.DSN != state.ActiveDSN {
		return DeploymentResult{}, errors.New("active deployment database DSN drifts from the lifecycle journal")
	}
	recoveryDSN := current.DSN
	if containsComponent(state.Changed, ComponentBackend) {
		if state.BackupManifest == nil {
			return DeploymentResult{}, errors.New("backend rollback requires a verified backup manifest")
		}
		recoveryDSN, err = orchestrator.restoreDeploymentBackup(ctx, &state)
		if err != nil {
			return orchestrator.rollbackFailure("lifecycle.deployment.rollback", state, DeploymentApplication{}, errors.New("explicit rollback"), err)
		}
	}
	application, err := orchestrator.platform.ActivateDeployment(ctx, *state.Previous, recoveryDSN, state.Changed)
	if err != nil {
		return orchestrator.rollbackFailure("lifecycle.deployment.rollback", state, application, errors.New("explicit rollback"), err)
	}
	state.Status = StatusRolledBack
	state.UpdatedAt = orchestrator.now().UTC()
	state.ActiveDSN = recoveryDSN
	state.Failure = ""
	if err := orchestrator.store.Append(state); err != nil {
		return failedDeploymentResult("lifecycle.deployment.rollback", application, &state), err
	}
	return DeploymentResult{Status: "success", Action: "lifecycle.deployment.rollback", Application: &application, State: &state}, nil
}

func (orchestrator *DeploymentOrchestrator) restoreDeploymentBackup(ctx context.Context, state *DeploymentState) (string, error) {
	if state.Previous == nil || state.BackupManifest == nil {
		return "", errors.New("deployment rollback state is missing its previous deployment or backup")
	}
	restoredDSN := fmt.Sprintf("file:/var/lib/xboard/xboard-rollback-%s.db", lifecycleTimestamp(orchestrator.now().UTC()))
	restoredPath := strings.TrimPrefix(restoredDSN, "file:")
	restoredAttachments, attachmentOutput, err := restoredAttachmentPaths(restoredDSN, state.BackupManifest.FormatVersion)
	if err != nil {
		return "", err
	}
	restored, err := orchestrator.platform.RestoreDeployment(ctx, state.Previous.Backend, state.BackupPath, restoredPath, attachmentOutput)
	state.RestoredPath = restoredPath
	state.RestoredAttachments = restoredAttachments
	if err != nil {
		return "", err
	}
	if !reflect.DeepEqual(restored, *state.BackupManifest) {
		return "", errors.New("restored deployment backup does not match the verified manifest")
	}
	return restoredDSN, nil
}

func (orchestrator *DeploymentOrchestrator) rollbackFailure(action string, state DeploymentState, application DeploymentApplication, targetErr, rollbackErr error) (DeploymentResult, error) {
	state.Status = StatusRollbackFailed
	state.UpdatedAt = orchestrator.now().UTC()
	state.Failure = safeFailure(rollbackErr)
	operationErr := fmt.Errorf("target deployment failed (%v) and rollback failed: %w", targetErr, rollbackErr)
	if err := orchestrator.store.Append(state); err != nil {
		operationErr = errors.Join(operationErr, err)
	}
	return failedDeploymentResult(action, application, &state), operationErr
}

func validateDeploymentApplication(application DeploymentApplication) error {
	if err := validateDeploymentApplicationIdentity(application); err != nil {
		return err
	}
	if !application.Healthy {
		return errors.New("active deployment is not healthy")
	}
	return nil
}

func validateDeploymentApplicationIdentity(application DeploymentApplication) error {
	if err := validateDeployment(application.Deployment); err != nil {
		return err
	}
	if !databasePattern.MatchString(application.DSN) {
		return fmt.Errorf("unsupported active database DSN %q", application.DSN)
	}
	return nil
}

func validateDeploymentState(state DeploymentState) error {
	if state.Version != DeploymentStateVersion {
		return fmt.Errorf("unsupported deployment lifecycle state version %d", state.Version)
	}
	if !validLifecycleStatus(state.Status) || state.UpdatedAt.IsZero() {
		return errors.New("invalid deployment lifecycle status or update time")
	}
	if state.Previous != nil {
		if err := validateDeployment(*state.Previous); err != nil {
			return fmt.Errorf("validate previous deployment: %w", err)
		}
	}
	if state.Target != nil {
		if err := validateDeployment(*state.Target); err != nil {
			return fmt.Errorf("validate target deployment: %w", err)
		}
	}
	seen := map[Component]bool{}
	for _, component := range state.Changed {
		if component != ComponentGateway && component != ComponentFrontend && component != ComponentBackend {
			return fmt.Errorf("invalid changed component %q", component)
		}
		if seen[component] {
			return fmt.Errorf("duplicate changed component %q", component)
		}
		seen[component] = true
	}
	if state.BackupManifest != nil {
		if err := validateBackupManifest(*state.BackupManifest); err != nil {
			return err
		}
		if state.Previous == nil || state.BackupManifest.AppRevision != state.Previous.Backend.Revision {
			return errors.New("deployment backup revision does not match previous backend")
		}
	}
	if state.BackupPath != "" && !backupPattern.MatchString(state.BackupPath) {
		return fmt.Errorf("invalid deployment backup path %q", state.BackupPath)
	}
	if state.RestoredPath != "" && !databasePathPattern.MatchString(state.RestoredPath) {
		return fmt.Errorf("invalid deployment restored database path %q", state.RestoredPath)
	}
	if state.RestoredAttachments != "" && !attachmentPattern.MatchString(state.RestoredAttachments) {
		return fmt.Errorf("invalid deployment restored attachment path %q", state.RestoredAttachments)
	}
	for _, dsn := range []string{state.OriginalDSN, state.ActiveDSN} {
		if dsn != "" && !databasePattern.MatchString(dsn) {
			return fmt.Errorf("invalid deployment database DSN %q", dsn)
		}
	}
	return nil
}

func containsComponent(values []Component, target Component) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func expectedDeploymentForState(state DeploymentState) *Deployment {
	switch state.Status {
	case StatusInstallPrepared, StatusInstalled, StatusInstallFailed, StatusPrepared, StatusUpgraded, StatusTargetFailed:
		return state.Target
	case StatusAutoRolledBack, StatusRolledBack, StatusRollbackFailed:
		return state.Previous
	default:
		return nil
	}
}

func failedDeploymentResult(action string, application DeploymentApplication, state *DeploymentState) DeploymentResult {
	result := DeploymentResult{Status: "failed", Action: action, State: state}
	if validateDeploymentApplicationIdentity(application) == nil {
		result.Application = &application
	}
	return result
}
