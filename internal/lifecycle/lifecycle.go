package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/backup"
)

const (
	StateVersion = 1

	StatusInstalled       = "installed"
	StatusInstallPrepared = "install_prepared"
	StatusInstallFailed   = "install_failed"
	StatusPrepared        = "prepared"
	StatusUpgraded        = "upgraded"
	StatusTargetFailed    = "target_failed"
	StatusAutoRolledBack  = "auto_rolled_back"
	StatusRolledBack      = "rolled_back"
	StatusRollbackFailed  = "rollback_failed"

	defaultDatabaseDSN = "file:/var/lib/xboard/xboard.db"
)

var (
	imageIDPattern      = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
	revisionPattern     = regexp.MustCompile(`^[0-9a-f]{40}$`)
	databasePattern     = regexp.MustCompile(`^file:/var/lib/xboard/[A-Za-z0-9._-]+\.db$`)
	backupPattern       = regexp.MustCompile(`^/var/lib/xboard-backups/[A-Za-z0-9._-]+\.xbbackup$`)
	databasePathPattern = regexp.MustCompile(`^/var/lib/xboard/[A-Za-z0-9._-]+\.db$`)
	attachmentPattern   = regexp.MustCompile(`^/var/lib/xboard/[A-Za-z0-9._-]+-attachments$`)
)

type Image struct {
	ID       string `json:"id"`
	Revision string `json:"revision"`
}

type Application struct {
	Image           Image  `json:"image"`
	DSN             string `json:"database_dsn"`
	ContainerStatus string `json:"container_status"`
	HealthStatus    string `json:"health_status"`
	Healthy         bool   `json:"healthy"`
}

type State struct {
	Version             int              `json:"version"`
	Status              string           `json:"status"`
	UpdatedAt           time.Time        `json:"updated_at"`
	Previous            *Image           `json:"previous_image,omitempty"`
	Target              *Image           `json:"target_image,omitempty"`
	BackupPath          string           `json:"backup_path,omitempty"`
	BackupManifest      *backup.Manifest `json:"backup_manifest,omitempty"`
	OriginalDSN         string           `json:"original_database_dsn,omitempty"`
	ActiveDSN           string           `json:"active_database_dsn,omitempty"`
	RestoredPath        string           `json:"restored_path,omitempty"`
	RestoredAttachments string           `json:"restored_attachment_path,omitempty"`
	Failure             string           `json:"failure,omitempty"`
}

type Result struct {
	Status      string       `json:"status"`
	Action      string       `json:"action"`
	Application *Application `json:"application,omitempty"`
	State       *State       `json:"state,omitempty"`
}

type Platform interface {
	Current(context.Context) (Application, error)
	ResolveImage(context.Context, string) (Image, error)
	Fresh(context.Context) (bool, error)
	Activate(context.Context, Image, string) (Application, error)
	Backup(context.Context, Application, string) (backup.Manifest, error)
	Restore(context.Context, Image, string, string, string) (backup.Manifest, error)
}

type Orchestrator struct {
	platform Platform
	journal  *Journal
	now      func() time.Time
}

func NewOrchestrator(platform Platform, journal *Journal, now func() time.Time) *Orchestrator {
	if now == nil {
		now = time.Now
	}
	return &Orchestrator{platform: platform, journal: journal, now: now}
}

func (o *Orchestrator) Status(ctx context.Context) (Result, error) {
	application, err := o.platform.Current(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := validateApplicationIdentity(application); err != nil {
		return Result{}, err
	}
	state, err := o.journal.Load()
	var statePointer *State
	if errors.Is(err, ErrNoState) {
		statePointer = nil
	} else if err != nil {
		return Result{}, err
	} else {
		statePointer = &state
	}
	return Result{Status: "success", Action: "lifecycle.status", Application: &application, State: statePointer}, nil
}

func (o *Orchestrator) Install(ctx context.Context, imageReference string) (Result, error) {
	fresh, err := o.platform.Fresh(ctx)
	if err != nil {
		return Result{}, err
	}
	if !fresh {
		return Result{}, errors.New("install requires an absent application container and data volume")
	}
	image, err := o.platform.ResolveImage(ctx, imageReference)
	if err != nil {
		return Result{}, err
	}
	if err := validateImage(image); err != nil {
		return Result{}, err
	}
	now := o.now().UTC()
	state := State{
		Version: StateVersion, Status: StatusInstallPrepared, UpdatedAt: now,
		Target: &image, OriginalDSN: defaultDatabaseDSN,
	}
	if err := o.journal.Append(state); err != nil {
		return Result{}, err
	}
	application, err := o.platform.Activate(ctx, image, defaultDatabaseDSN)
	if err != nil {
		state.Status = StatusInstallFailed
		state.UpdatedAt = o.now().UTC()
		state.Failure = safeFailure(err)
		if appendErr := o.journal.Append(state); appendErr != nil {
			return Result{}, errors.Join(fmt.Errorf("install target: %w", err), appendErr)
		}
		result := failedResult("lifecycle.install", application, &state)
		return result, fmt.Errorf("install target: %w", err)
	}
	state.Status = StatusInstalled
	state.UpdatedAt = o.now().UTC()
	state.ActiveDSN = defaultDatabaseDSN
	if err := o.journal.Append(state); err != nil {
		return failedResult("lifecycle.install", application, &state), err
	}
	return Result{Status: "success", Action: "lifecycle.install", Application: &application, State: &state}, nil
}

func (o *Orchestrator) Upgrade(ctx context.Context, imageReference string) (Result, error) {
	current, err := o.platform.Current(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := validateApplication(current); err != nil {
		return Result{}, err
	}
	target, err := o.platform.ResolveImage(ctx, imageReference)
	if err != nil {
		return Result{}, err
	}
	if err := validateImage(target); err != nil {
		return Result{}, err
	}
	if target.ID == current.Image.ID {
		return Result{}, errors.New("target image is already active")
	}

	now := o.now().UTC()
	backupPath := fmt.Sprintf(
		"/var/lib/xboard-backups/lifecycle-%s-%s.xbbackup",
		lifecycleTimestamp(now), current.Image.Revision[:12],
	)
	manifest, err := o.platform.Backup(ctx, current, backupPath)
	if err != nil {
		return Result{}, fmt.Errorf("create and verify pre-upgrade backup: %w", err)
	}
	if manifest.AppRevision != current.Image.Revision {
		return Result{}, fmt.Errorf("backup revision %q does not match active revision %q", manifest.AppRevision, current.Image.Revision)
	}
	if err := validateBackupManifest(manifest); err != nil {
		return Result{}, fmt.Errorf("validate verified pre-upgrade backup manifest: %w", err)
	}
	state := State{
		Version: StateVersion, Status: StatusPrepared, UpdatedAt: now,
		Previous: &current.Image, Target: &target, BackupPath: backupPath,
		BackupManifest: &manifest,
		OriginalDSN:    current.DSN, ActiveDSN: current.DSN,
	}
	if err := o.journal.Append(state); err != nil {
		return Result{}, err
	}

	application, activationErr := o.platform.Activate(ctx, target, current.DSN)
	if activationErr == nil {
		state.Status = StatusUpgraded
		state.UpdatedAt = o.now().UTC()
		if err := o.journal.Append(state); err != nil {
			return failedResult("lifecycle.upgrade", application, &state), err
		}
		return Result{Status: "success", Action: "lifecycle.upgrade", Application: &application, State: &state}, nil
	}
	state.Status = StatusTargetFailed
	state.UpdatedAt = o.now().UTC()
	state.Failure = safeFailure(activationErr)
	if err := o.journal.Append(state); err != nil {
		return failedResult("lifecycle.upgrade", application, &state), errors.Join(activationErr, err)
	}

	restoredDSN := fmt.Sprintf("file:/var/lib/xboard/xboard-rollback-%s.db", lifecycleTimestamp(now))
	restoredPath := strings.TrimPrefix(restoredDSN, "file:")
	restoredAttachments, attachmentOutput, restoreErr := restoredAttachmentPaths(restoredDSN, manifest.FormatVersion)
	var restoredManifest backup.Manifest
	if restoreErr == nil {
		restoredManifest, restoreErr = o.platform.Restore(ctx, current.Image, backupPath, restoredPath, attachmentOutput)
	}
	if restoreErr == nil && !reflect.DeepEqual(restoredManifest, manifest) {
		restoreErr = fmt.Errorf(
			"restored backup revision %q or manifest does not match the verified pre-upgrade backup revision %q",
			restoredManifest.AppRevision, manifest.AppRevision,
		)
	}
	if restoreErr != nil {
		state.Status = StatusRollbackFailed
		state.UpdatedAt = o.now().UTC()
		state.RestoredPath = restoredPath
		state.RestoredAttachments = restoredAttachments
		state.Failure = safeFailure(restoreErr)
		operationErr := fmt.Errorf("target failed (%v) and automatic database restore failed: %w", activationErr, restoreErr)
		if appendErr := o.journal.Append(state); appendErr != nil {
			operationErr = errors.Join(operationErr, appendErr)
		}
		return failedResult("lifecycle.upgrade", application, &state), operationErr
	}
	recovered, err := o.platform.Activate(ctx, current.Image, restoredDSN)
	if err != nil {
		state.Status = StatusRollbackFailed
		state.UpdatedAt = o.now().UTC()
		state.RestoredPath = restoredPath
		state.RestoredAttachments = restoredAttachments
		state.Failure = safeFailure(err)
		operationErr := fmt.Errorf("target failed (%v) and restored previous image failed health validation: %w", activationErr, err)
		if appendErr := o.journal.Append(state); appendErr != nil {
			operationErr = errors.Join(operationErr, appendErr)
		}
		return failedResult("lifecycle.upgrade", recovered, &state), operationErr
	}
	state.Status = StatusAutoRolledBack
	state.UpdatedAt = o.now().UTC()
	state.ActiveDSN = restoredDSN
	state.RestoredPath = restoredPath
	state.RestoredAttachments = restoredAttachments
	state.Failure = safeFailure(activationErr)
	result := failedResult("lifecycle.upgrade", recovered, &state)
	if err := o.journal.Append(state); err != nil {
		return result, errors.Join(fmt.Errorf("upgrade target failed and was automatically rolled back: %w", activationErr), err)
	}
	return result, fmt.Errorf("upgrade target failed and was automatically rolled back: %w", activationErr)
}

func (o *Orchestrator) Rollback(ctx context.Context) (Result, error) {
	state, err := o.journal.Load()
	if err != nil {
		return Result{}, err
	}
	if state.Status != StatusUpgraded {
		return Result{}, fmt.Errorf("rollback requires lifecycle status %q, found %q", StatusUpgraded, state.Status)
	}
	current, err := o.platform.Current(ctx)
	if err != nil {
		return Result{}, err
	}
	if err := validateApplication(current); err != nil {
		return Result{}, err
	}
	if state.Previous == nil || state.Target == nil {
		return Result{}, errors.New("rollback state is missing its previous or target image")
	}
	if current.Image.ID != state.Target.ID {
		return Result{}, fmt.Errorf("rollback target image mismatch: active %s, recorded target image %s", current.Image.ID, state.Target.ID)
	}
	if state.BackupManifest == nil {
		return Result{}, errors.New("rollback state is missing its verified backup manifest")
	}

	now := o.now().UTC()
	restoredDSN := fmt.Sprintf("file:/var/lib/xboard/xboard-rollback-%s.db", lifecycleTimestamp(now))
	restoredPath := strings.TrimPrefix(restoredDSN, "file:")
	restoredAttachments, attachmentOutput, restoreErr := restoredAttachmentPaths(restoredDSN, state.BackupManifest.FormatVersion)
	var restoredManifest backup.Manifest
	if restoreErr == nil {
		restoredManifest, restoreErr = o.platform.Restore(ctx, *state.Previous, state.BackupPath, restoredPath, attachmentOutput)
	}
	if restoreErr == nil && !reflect.DeepEqual(restoredManifest, *state.BackupManifest) {
		restoreErr = fmt.Errorf(
			"restored backup revision %q or manifest does not match recorded backup revision %q",
			restoredManifest.AppRevision, state.BackupManifest.AppRevision,
		)
	}
	if restoreErr != nil {
		state.Status = StatusRollbackFailed
		state.UpdatedAt = o.now().UTC()
		state.RestoredPath = restoredPath
		state.RestoredAttachments = restoredAttachments
		state.Failure = safeFailure(restoreErr)
		operationErr := fmt.Errorf("restore rollback database: %w", restoreErr)
		if appendErr := o.journal.Append(state); appendErr != nil {
			operationErr = errors.Join(operationErr, appendErr)
		}
		return failedResult("lifecycle.rollback", Application{}, &state), operationErr
	}
	application, err := o.platform.Activate(ctx, *state.Previous, restoredDSN)
	if err != nil {
		state.Status = StatusRollbackFailed
		state.UpdatedAt = o.now().UTC()
		state.RestoredPath = restoredPath
		state.RestoredAttachments = restoredAttachments
		state.Failure = safeFailure(err)
		operationErr := fmt.Errorf("activate previous image: %w", err)
		if appendErr := o.journal.Append(state); appendErr != nil {
			operationErr = errors.Join(operationErr, appendErr)
		}
		return failedResult("lifecycle.rollback", application, &state), operationErr
	}
	state.Status = StatusRolledBack
	state.UpdatedAt = o.now().UTC()
	state.ActiveDSN = restoredDSN
	state.RestoredPath = restoredPath
	state.RestoredAttachments = restoredAttachments
	state.Failure = ""
	if err := o.journal.Append(state); err != nil {
		return failedResult("lifecycle.rollback", application, &state), err
	}
	return Result{Status: "success", Action: "lifecycle.rollback", Application: &application, State: &state}, nil
}

func validateApplication(application Application) error {
	if err := validateApplicationIdentity(application); err != nil {
		return err
	}
	if !application.Healthy {
		return fmt.Errorf("active application is not healthy (container=%s health=%s)", application.ContainerStatus, application.HealthStatus)
	}
	return nil
}

func validateApplicationIdentity(application Application) error {
	if err := validateImage(application.Image); err != nil {
		return fmt.Errorf("validate active image: %w", err)
	}
	if !databasePattern.MatchString(application.DSN) {
		return fmt.Errorf("unsupported active database DSN %q", application.DSN)
	}
	return nil
}

func validateImage(image Image) error {
	if !imageIDPattern.MatchString(image.ID) {
		return fmt.Errorf("invalid immutable image ID %q", image.ID)
	}
	if !revisionPattern.MatchString(image.Revision) {
		return fmt.Errorf("invalid image revision %q", image.Revision)
	}
	return nil
}

func validateState(state State) error {
	if state.Version != StateVersion {
		return fmt.Errorf("unsupported lifecycle state version %d", state.Version)
	}
	if strings.TrimSpace(state.Status) == "" {
		return errors.New("lifecycle state status is required")
	}
	if !validLifecycleStatus(state.Status) {
		return fmt.Errorf("unsupported lifecycle state status %q", state.Status)
	}
	if state.UpdatedAt.IsZero() {
		return errors.New("lifecycle state update time is required")
	}
	if state.Previous != nil {
		if err := validateImage(*state.Previous); err != nil {
			return fmt.Errorf("validate previous image: %w", err)
		}
	}
	if state.Target != nil {
		if err := validateImage(*state.Target); err != nil {
			return fmt.Errorf("validate target image: %w", err)
		}
	}
	if state.BackupManifest != nil {
		if err := validateBackupManifest(*state.BackupManifest); err != nil {
			return fmt.Errorf("validate lifecycle backup manifest: %w", err)
		}
		if state.Previous != nil && state.BackupManifest.AppRevision != state.Previous.Revision {
			return errors.New("lifecycle backup revision does not match previous image revision")
		}
	}
	if state.BackupPath != "" && !backupPattern.MatchString(state.BackupPath) {
		return fmt.Errorf("invalid lifecycle backup path %q", state.BackupPath)
	}
	if state.RestoredPath != "" && !databasePathPattern.MatchString(state.RestoredPath) {
		return fmt.Errorf("invalid lifecycle restored database path %q", state.RestoredPath)
	}
	if state.RestoredAttachments != "" && !attachmentPattern.MatchString(state.RestoredAttachments) {
		return fmt.Errorf("invalid lifecycle restored attachment path %q", state.RestoredAttachments)
	}
	for _, dsn := range []string{state.OriginalDSN, state.ActiveDSN} {
		if dsn != "" && !databasePattern.MatchString(dsn) {
			return fmt.Errorf("invalid lifecycle database DSN %q", dsn)
		}
	}
	return nil
}

func attachmentRootForDSN(dsn string) (string, error) {
	if !databasePattern.MatchString(dsn) {
		return "", fmt.Errorf("unsupported runtime database DSN %q", dsn)
	}
	if dsn == defaultDatabaseDSN {
		return "/var/lib/xboard/knowledge-attachments", nil
	}
	return strings.TrimSuffix(strings.TrimPrefix(dsn, "file:"), ".db") + "-attachments", nil
}

func restoredAttachmentPaths(dsn string, formatVersion int) (string, string, error) {
	root, err := attachmentRootForDSN(dsn)
	if err != nil {
		return "", "", err
	}
	if formatVersion == 2 {
		return root, root, nil
	}
	return root, "", nil
}

func failedResult(action string, application Application, state *State) Result {
	result := Result{Status: "failed", Action: action, State: state}
	if validateApplicationIdentity(application) == nil {
		result.Application = &application
	}
	return result
}

func lifecycleTimestamp(now time.Time) string {
	return now.UTC().Format("20060102T150405.000000000Z")
}

func validLifecycleStatus(status string) bool {
	switch status {
	case StatusInstallPrepared, StatusInstalled, StatusInstallFailed, StatusPrepared,
		StatusUpgraded, StatusTargetFailed, StatusAutoRolledBack, StatusRolledBack,
		StatusRollbackFailed:
		return true
	default:
		return false
	}
}

func validateBackupManifest(manifest backup.Manifest) error {
	if err := backup.ValidateManifest(manifest); err != nil {
		return err
	}
	if !revisionPattern.MatchString(manifest.AppRevision) {
		return fmt.Errorf("invalid backup revision %q", manifest.AppRevision)
	}
	return nil
}

func safeFailure(err error) string {
	if err == nil {
		return ""
	}
	message := strings.Join(strings.Fields(err.Error()), " ")
	if len(message) > 512 {
		message = message[:512]
	}
	return message
}
