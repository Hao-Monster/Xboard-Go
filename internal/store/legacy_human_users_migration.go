package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/mail"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Hao-Monster/Xboard-Go/internal/security"
	"github.com/google/uuid"
)

const (
	LegacyHumanUsersSlice   = "human-users-v1"
	maxLegacyHumanUsers     = 250_000
	maxLegacyHumanUserBytes = int64(256 << 20)
)

type LegacyHumanUser struct {
	ID                int64   `json:"id"`
	InviteUserID      *int64  `json:"invite_user_id"`
	Email             string  `json:"email"`
	PasswordHash      string  `json:"password_hash"`
	Balance           int64   `json:"balance"`
	Discount          *int    `json:"discount"`
	CommissionType    int     `json:"commission_type"`
	CommissionRate    *int    `json:"commission_rate"`
	CommissionBalance int64   `json:"commission_balance"`
	TransferEnable    int64   `json:"transfer_enable"`
	TrafficUpload     int64   `json:"traffic_upload"`
	TrafficDownload   int64   `json:"traffic_download"`
	Banned            bool    `json:"banned"`
	IsAdmin           bool    `json:"is_admin"`
	IsStaff           bool    `json:"is_staff"`
	IsDistributor     bool    `json:"is_distributor"`
	DistributorName   *string `json:"distributor_name"`
	LastLoginAt       *int64  `json:"last_login_at"`
	UUID              string  `json:"uuid"`
	GroupID           *int64  `json:"group_id"`
	PlanID            *int64  `json:"plan_id"`
	SpeedLimit        int     `json:"speed_limit"`
	ExpiredAt         *int64  `json:"expired_at"`
	DeviceLimit       int     `json:"device_limit"`
	LastOnlineAt      *int64  `json:"last_online_at"`
	NextResetAt       *int64  `json:"next_reset_at"`
	LastResetAt       *int64  `json:"last_reset_at"`
	ResetCount        int64   `json:"reset_count"`
	SubscriptionToken string  `json:"subscription_token"`
	CreatedAt         int64   `json:"created_at"`
	UpdatedAt         int64   `json:"updated_at"`
}

type LegacyHumanUsersImport struct {
	Slice                 string
	SourceSHA256          string
	SourceSize            int64
	Users                 []LegacyHumanUser
	Checksum              string
	RollbackBackupPath    string
	RollbackBackupSHA256  string
	ReplaceBootstrapAdmin bool
}

type LegacyHumanUsersImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Users                LegacyDomainResult `json:"users"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

func LegacyHumanUsersChecksum(users []LegacyHumanUser) string {
	ordered := append([]LegacyHumanUser(nil), users...)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].ID < ordered[right].ID })
	canonical := newLegacyHumanUsersDigest()
	for _, user := range ordered {
		canonical.add(user)
	}
	extended := false
	for _, user := range ordered {
		if user.PlanID != nil || user.NextResetAt != nil || user.LastResetAt != nil || user.ResetCount != 0 {
			extended = true
			break
		}
	}
	if extended {
		canonical.writeString("plan-reset-v1")
		for _, user := range ordered {
			canonical.writeInt64(user.ID)
			canonical.writePointer(user.PlanID)
			canonical.writePointer(user.NextResetAt)
			canonical.writePointer(user.LastResetAt)
			canonical.writeInt64(user.ResetCount)
		}
	}
	financeExtended := false
	for _, user := range ordered {
		if user.Balance != 0 || user.Discount != nil || user.CommissionType != 0 || user.CommissionRate != nil || user.CommissionBalance != 0 {
			financeExtended = true
			break
		}
	}
	if financeExtended {
		canonical.writeString("finance-v1")
		for _, user := range ordered {
			canonical.writeInt64(user.ID)
			canonical.writeInt64(user.Balance)
			canonical.writeIntPointer(user.Discount)
			canonical.writeInt64(int64(user.CommissionType))
			canonical.writeIntPointer(user.CommissionRate)
			canonical.writeInt64(user.CommissionBalance)
		}
	}
	roleExtended := false
	for _, user := range ordered {
		if user.IsStaff || user.IsDistributor || user.DistributorName != nil {
			roleExtended = true
			break
		}
	}
	if roleExtended {
		canonical.writeString("roles-v1")
		for _, user := range ordered {
			canonical.writeInt64(user.ID)
			canonical.writeBool(user.IsStaff)
			canonical.writeBool(user.IsDistributor)
			canonical.writeStringPointer(user.DistributorName)
		}
	}
	return canonical.sum()
}

type legacyHumanUsersDigest struct {
	digest hash.Hash
	number [8]byte
	flag   [1]byte
}

func newLegacyHumanUsersDigest() *legacyHumanUsersDigest {
	return &legacyHumanUsersDigest{digest: sha256.New()}
}

func (canonical *legacyHumanUsersDigest) add(user LegacyHumanUser) {
	canonical.writeInt64(user.ID)
	canonical.writePointer(user.InviteUserID)
	canonical.writeString(user.Email)
	canonical.writeString(user.PasswordHash)
	canonical.writeInt64(user.TransferEnable)
	canonical.writeInt64(user.TrafficUpload)
	canonical.writeInt64(user.TrafficDownload)
	canonical.writeBool(user.Banned)
	canonical.writeBool(user.IsAdmin)
	canonical.writePointer(user.LastLoginAt)
	canonical.writeString(user.UUID)
	canonical.writePointer(user.GroupID)
	canonical.writeInt64(int64(user.SpeedLimit))
	canonical.writePointer(user.ExpiredAt)
	canonical.writeInt64(int64(user.DeviceLimit))
	canonical.writePointer(user.LastOnlineAt)
	canonical.writeString(user.SubscriptionToken)
	canonical.writeInt64(user.CreatedAt)
	canonical.writeInt64(user.UpdatedAt)
}

func (canonical *legacyHumanUsersDigest) writeInt64(value int64) {
	binary.BigEndian.PutUint64(canonical.number[:], uint64(value))
	_, _ = canonical.digest.Write(canonical.number[:])
}

func (canonical *legacyHumanUsersDigest) writeString(value string) {
	canonical.writeInt64(int64(len(value)))
	_, _ = io.WriteString(canonical.digest, value)
}

func (canonical *legacyHumanUsersDigest) writePointer(value *int64) {
	canonical.flag[0] = 0
	if value != nil {
		canonical.flag[0] = 1
	}
	_, _ = canonical.digest.Write(canonical.flag[:])
	if value != nil {
		canonical.writeInt64(*value)
	}
}

func (canonical *legacyHumanUsersDigest) writeIntPointer(value *int) {
	canonical.flag[0] = 0
	if value != nil {
		canonical.flag[0] = 1
	}
	_, _ = canonical.digest.Write(canonical.flag[:])
	if value != nil {
		canonical.writeInt64(int64(*value))
	}
}

func (canonical *legacyHumanUsersDigest) writeStringPointer(value *string) {
	canonical.flag[0] = 0
	if value != nil {
		canonical.flag[0] = 1
	}
	_, _ = canonical.digest.Write(canonical.flag[:])
	if value != nil {
		canonical.writeString(*value)
	}
}

func (canonical *legacyHumanUsersDigest) writeBool(value bool) {
	canonical.flag[0] = 0
	if value {
		canonical.flag[0] = 1
	}
	_, _ = canonical.digest.Write(canonical.flag[:])
}

func (canonical *legacyHumanUsersDigest) sum() string {
	return hex.EncodeToString(canonical.digest.Sum(nil))
}

func ValidateLegacyHumanUsersData(users []LegacyHumanUser) error {
	if len(users) == 0 || len(users) > maxLegacyHumanUsers {
		return fmt.Errorf("%w: legacy human users must contain between 1 and %d rows", ErrInvalidInput, maxLegacyHumanUsers)
	}
	ids := make(map[int64]struct{}, len(users))
	emails := make(map[string]struct{}, len(users))
	uuids := make(map[string]struct{}, len(users))
	tokens := make(map[string]struct{}, len(users))
	var totalBytes int64
	var activeAdmins int
	for _, user := range users {
		distributorName, distributorNameErr := normalizedDistributorName(user.IsDistributor, user.DistributorName)
		address, emailErr := mail.ParseAddress(user.Email)
		if user.ID < 1 || user.Email == "" || len(user.Email) > 320 || normalizeEmail(user.Email) != user.Email ||
			!utf8.ValidString(user.Email) || emailErr != nil || address.Address != user.Email ||
			!security.IsLegacyBcryptHash(user.PasswordHash) || user.TransferEnable < 0 ||
			user.Balance < 0 || user.Balance > maxOrderMoneyCents || user.CommissionBalance < 0 || user.CommissionBalance > maxOrderMoneyCents ||
			user.Discount != nil && (*user.Discount < 0 || *user.Discount > 100) ||
			user.CommissionType < 0 || user.CommissionType > 2 || user.CommissionRate != nil && (*user.CommissionRate < 0 || *user.CommissionRate > 100) ||
			user.TrafficUpload < 0 || user.TrafficDownload < 0 || user.SpeedLimit < 0 || user.DeviceLimit < 0 ||
			user.DeviceLimit > 1_000 || !validLegacyUnixTimestamp(user.CreatedAt) || !validLegacyUnixTimestamp(user.UpdatedAt) ||
			user.UpdatedAt < user.CreatedAt || distributorNameErr != nil || !equalOptionalStrings(distributorName, user.DistributorName) {
			return fmt.Errorf("%w: invalid legacy human user id %d", ErrInvalidInput, user.ID)
		}
		parsedUUID, err := uuid.Parse(user.UUID)
		if err != nil || parsedUUID == uuid.Nil || parsedUUID.String() != user.UUID {
			return fmt.Errorf("%w: legacy human user id %d has a non-canonical UUID", ErrInvalidInput, user.ID)
		}
		if !validLegacySubscriptionToken(user.SubscriptionToken) {
			return fmt.Errorf("%w: legacy human user id %d has an invalid subscription token", ErrInvalidInput, user.ID)
		}
		if user.GroupID != nil && *user.GroupID < 1 || user.InviteUserID != nil && (*user.InviteUserID < 1 || *user.InviteUserID == user.ID) ||
			user.PlanID != nil && *user.PlanID < 1 || user.ResetCount < 0 ||
			user.ExpiredAt != nil && !validLegacyUnixTimestamp(*user.ExpiredAt) ||
			user.LastOnlineAt != nil && !validLegacyUnixTimestamp(*user.LastOnlineAt) ||
			user.LastLoginAt != nil && !validLegacyUnixTimestamp(*user.LastLoginAt) ||
			user.NextResetAt != nil && !validLegacyUnixTimestamp(*user.NextResetAt) ||
			user.LastResetAt != nil && !validLegacyUnixTimestamp(*user.LastResetAt) {
			return fmt.Errorf("%w: legacy human user id %d has an invalid reference or timestamp", ErrInvalidInput, user.ID)
		}
		if _, exists := ids[user.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy human user id %d", ErrInvalidInput, user.ID)
		}
		ids[user.ID] = struct{}{}
		if _, exists := emails[user.Email]; exists {
			return fmt.Errorf("%w: duplicate legacy human user email", ErrInvalidInput)
		}
		emails[user.Email] = struct{}{}
		if _, exists := uuids[user.UUID]; exists {
			return fmt.Errorf("%w: duplicate legacy human user UUID", ErrInvalidInput)
		}
		uuids[user.UUID] = struct{}{}
		if _, exists := tokens[user.SubscriptionToken]; exists {
			return fmt.Errorf("%w: duplicate legacy human user subscription token", ErrInvalidInput)
		}
		tokens[user.SubscriptionToken] = struct{}{}
		if user.IsAdmin && !user.Banned {
			activeAdmins++
		}
		totalBytes += int64(len(user.Email) + len(user.PasswordHash) + len(user.UUID) + len(user.SubscriptionToken))
		if user.DistributorName != nil {
			totalBytes += int64(len(*user.DistributorName))
		}
		if totalBytes > maxLegacyHumanUserBytes {
			return fmt.Errorf("%w: legacy human users exceed the migration data limit", ErrInvalidInput)
		}
	}
	for _, user := range users {
		if user.InviteUserID != nil {
			if _, exists := ids[*user.InviteUserID]; !exists {
				return fmt.Errorf("%w: legacy human user id %d references a missing inviter", ErrInvalidInput, user.ID)
			}
		}
	}
	if activeAdmins == 0 {
		return fmt.Errorf("%w: legacy human users require an unbanned administrator", ErrInvalidInput)
	}
	return nil
}

func equalOptionalStrings(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func validLegacyUnixTimestamp(value int64) bool {
	return value >= 0 && value <= 253_402_300_799
}

func validLegacySubscriptionToken(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func (s *Store) LookupLegacyHumanUsersImport(ctx context.Context, sourceSHA256 string) (LegacyHumanUsersImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyHumanUsersImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyHumanUsersImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyHumanUsersImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyHumanUsersImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`,
		LegacyHumanUsersSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyHumanUsersImportReport{}, false, nil
	}
	if err != nil {
		return LegacyHumanUsersImportReport{}, false, fmt.Errorf("lookup legacy human user migration: %w", err)
	}
	var report LegacyHumanUsersImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyHumanUsersImportReport{}, false, fmt.Errorf("decode legacy human user migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyHumanUsers(ctx context.Context, input LegacyHumanUsersImport, now time.Time) (LegacyHumanUsersImportReport, error) {
	if err := validateLegacyHumanUsersImport(input); err != nil {
		return LegacyHumanUsersImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("begin legacy human user import: %w", err)
	}
	defer tx.Rollback()

	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("read legacy human user target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("legacy human user import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("validate legacy human user target schema: %w", err)
	}
	if existing, found, err := lookupLegacyHumanUsersImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyHumanUsersImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyHumanUsersImportReport{}, fmt.Errorf("commit idempotent legacy human user import: %w", err)
		}
		return existing, nil
	}
	var otherRuns int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyHumanUsersSlice).Scan(&otherRuns); err != nil {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("count legacy human user migrations: %w", err)
	}
	if otherRuns != 0 {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("%w: legacy human user slice was already imported from another snapshot", ErrConflict)
	}
	bootstrapID, err := validateReplaceableBootstrapAdmin(ctx, tx)
	if err != nil {
		return LegacyHumanUsersImportReport{}, err
	}
	if err := validateLegacyHumanUserGroups(ctx, tx, input.Users); err != nil {
		return LegacyHumanUsersImportReport{}, err
	}
	if err := validateLegacyHumanUserPlans(ctx, tx, input.Users); err != nil {
		return LegacyHumanUsersImportReport{}, err
	}
	if err := ensureNoUserDependencies(ctx, tx, bootstrapID); err != nil {
		return LegacyHumanUsersImportReport{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, bootstrapID); err != nil {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("replace bootstrap administrator: %w", err)
	}

	statement, err := tx.PrepareContext(ctx, `
		INSERT INTO users (
			id, email, password_hash, is_admin, is_staff, is_distributor, distributor_name, banned, account_kind, balance, discount, commission_type,
			commission_rate, commission_balance, uuid, group_id, plan_id, transfer_enable,
			traffic_u, traffic_d, expired_at, speed_limit, device_limit, online_count, last_online_at,
			last_login_at, next_reset_at, last_reset_at, reset_count, admin_revision, subscription_token,
			invite_user_id, created_at, updated_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?, 'human',
			?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0,
			?, ?, ?, ?, ?, 1, ?, NULL, ?, ?
		)
	`)
	if err != nil {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("prepare legacy human user import: %w", err)
	}
	defer statement.Close()
	for _, user := range input.Users {
		if _, err := statement.ExecContext(ctx, user.ID, user.Email, user.PasswordHash, user.IsAdmin, user.IsStaff,
			user.IsDistributor, nullableStringValue(user.DistributorName), user.Banned,
			user.Balance, nullableIntValue(user.Discount), user.CommissionType, nullableIntValue(user.CommissionRate), user.CommissionBalance, user.UUID,
			nullableInt64Value(user.GroupID), nullableInt64Value(user.PlanID), user.TransferEnable, user.TrafficUpload, user.TrafficDownload,
			nullableInt64Value(user.ExpiredAt), user.SpeedLimit, user.DeviceLimit, nullableInt64Value(user.LastOnlineAt),
			nullableInt64Value(user.LastLoginAt), nullableInt64Value(user.NextResetAt), nullableInt64Value(user.LastResetAt),
			user.ResetCount, user.SubscriptionToken, user.CreatedAt, user.UpdatedAt); err != nil {
			return LegacyHumanUsersImportReport{}, fmt.Errorf("import legacy human user id %d: %w", user.ID, err)
		}
	}
	for _, user := range input.Users {
		if user.InviteUserID == nil {
			continue
		}
		if _, err := tx.ExecContext(ctx, `UPDATE users SET invite_user_id = ? WHERE id = ?`, *user.InviteUserID, user.ID); err != nil {
			return LegacyHumanUsersImportReport{}, fmt.Errorf("link legacy human user id %d inviter: %w", user.ID, err)
		}
	}
	targetRows, targetChecksum, err := readLegacyTargetHumanUsers(ctx, tx)
	if err != nil {
		return LegacyHumanUsersImportReport{}, err
	}
	report := LegacyHumanUsersImportReport{
		Slice: input.Slice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Users:     LegacyDomainResult{SourceRows: len(input.Users), TargetRows: targetRows, SourceChecksum: input.Checksum, TargetChecksum: targetChecksum},
		AppliedAt: now.UTC(),
	}
	if report.Users.SourceRows != report.Users.TargetRows || report.Users.SourceChecksum != report.Users.TargetChecksum {
		return LegacyHumanUsersImportReport{}, errors.New("legacy human user target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("encode legacy human user migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO legacy_migration_runs
		(slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("record legacy human user migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyHumanUsersImportReport{}, fmt.Errorf("commit legacy human user import: %w", err)
	}
	return report, nil
}

func validateLegacyHumanUsersImport(input LegacyHumanUsersImport) error {
	if input.Slice != LegacyHumanUsersSlice || !input.ReplaceBootstrapAdmin || !validLowerSHA256(input.SourceSHA256) ||
		input.SourceSize < 1 || !validLowerSHA256(input.RollbackBackupSHA256) || input.RollbackBackupPath == "" ||
		len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) ||
		strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 || input.Checksum != LegacyHumanUsersChecksum(input.Users) {
		return fmt.Errorf("%w: invalid legacy human user import", ErrInvalidInput)
	}
	return ValidateLegacyHumanUsersData(input.Users)
}

func validateReplaceableBootstrapAdmin(ctx context.Context, tx *sql.Tx) (int64, error) {
	var total int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&total); err != nil {
		return 0, fmt.Errorf("count target users: %w", err)
	}
	if total != 1 {
		return 0, fmt.Errorf("%w: legacy human user import requires exactly one bootstrap target user", ErrConflict)
	}
	var id int64
	var isAdmin, banned bool
	var kind string
	var runtimeUUID sql.NullString
	var groupID, planID, expiredAt, lastOnlineAt, inviteUserID, nextResetAt, lastResetAt sql.NullInt64
	var resetCount int64
	var transfer, upload, download, balance, commissionBalance int64
	var discount, commissionRate sql.NullInt64
	var commissionType int
	var speed, devices, online int
	if err := tx.QueryRowContext(ctx, `
		SELECT id, is_admin, banned, account_kind, uuid, group_id, plan_id, balance, discount, commission_type,
		       commission_rate, commission_balance, transfer_enable, traffic_u, traffic_d,
		       expired_at, speed_limit, device_limit, online_count, last_online_at, invite_user_id,
		       next_reset_at, last_reset_at, reset_count
		FROM users
	`).Scan(&id, &isAdmin, &banned, &kind, &runtimeUUID, &groupID, &planID, &balance, &discount, &commissionType,
		&commissionRate, &commissionBalance, &transfer, &upload, &download,
		&expiredAt, &speed, &devices, &online, &lastOnlineAt, &inviteUserID, &nextResetAt, &lastResetAt, &resetCount); err != nil {
		return 0, fmt.Errorf("inspect bootstrap administrator: %w", err)
	}
	if !isAdmin || banned || kind != AccountKindHuman || runtimeUUID.Valid || groupID.Valid || planID.Valid || expiredAt.Valid || lastOnlineAt.Valid ||
		inviteUserID.Valid || nextResetAt.Valid || lastResetAt.Valid || resetCount != 0 || transfer != 0 || upload != 0 || download != 0 ||
		balance != 0 || discount.Valid || commissionType != 0 || commissionRate.Valid || commissionBalance != 0 ||
		speed != 0 || devices != 0 || online != 0 {
		return 0, fmt.Errorf("%w: target user is not a replaceable bootstrap administrator", ErrConflict)
	}
	return id, nil
}

func validateLegacyHumanUserGroups(ctx context.Context, tx *sql.Tx, users []LegacyHumanUser) error {
	seen := make(map[int64]struct{})
	for _, user := range users {
		if user.GroupID == nil {
			continue
		}
		if _, checked := seen[*user.GroupID]; checked {
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM server_groups WHERE id = ?)`, *user.GroupID).Scan(&exists); err != nil {
			return fmt.Errorf("validate legacy human user group: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: legacy human users reference missing group %d", ErrConflict, *user.GroupID)
		}
		seen[*user.GroupID] = struct{}{}
	}
	return nil
}

func validateLegacyHumanUserPlans(ctx context.Context, tx *sql.Tx, users []LegacyHumanUser) error {
	seen := make(map[int64]struct{})
	for _, user := range users {
		if user.PlanID == nil {
			continue
		}
		if _, checked := seen[*user.PlanID]; checked {
			continue
		}
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM plans WHERE id = ?)`, *user.PlanID).Scan(&exists); err != nil {
			return fmt.Errorf("validate legacy human user plan: %w", err)
		}
		if !exists {
			return fmt.Errorf("%w: legacy human users reference missing plan %d", ErrConflict, *user.PlanID)
		}
		seen[*user.PlanID] = struct{}{}
	}
	return nil
}

func ensureNoUserDependencies(ctx context.Context, tx *sql.Tx, userID int64) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT DISTINCT schema_object.name, foreign_key.[from]
		FROM sqlite_schema AS schema_object
		JOIN pragma_foreign_key_list(schema_object.name) AS foreign_key
		WHERE schema_object.type = 'table' AND foreign_key.[table] = 'users'
		ORDER BY schema_object.name, foreign_key.[from]
	`)
	if err != nil {
		return fmt.Errorf("inspect target user dependencies: %w", err)
	}
	type reference struct{ table, column string }
	references := make([]reference, 0)
	for rows.Next() {
		var item reference
		if err := rows.Scan(&item.table, &item.column); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan target user dependency: %w", err)
		}
		references = append(references, item)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close target user dependencies: %w", err)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate target user dependencies: %w", err)
	}
	for _, item := range references {
		query := `SELECT COUNT(*) FROM ` + quoteSQLiteIdentifier(item.table) + ` WHERE ` + quoteSQLiteIdentifier(item.column) + ` = ?`
		var count int
		if err := tx.QueryRowContext(ctx, query, userID).Scan(&count); err != nil {
			return fmt.Errorf("count target dependency %s.%s: %w", item.table, item.column, err)
		}
		if count != 0 {
			return fmt.Errorf("%w: bootstrap administrator is referenced by %s.%s", ErrConflict, item.table, item.column)
		}
	}
	return nil
}

func quoteSQLiteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func nullableInt64Value(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableIntValue(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func readLegacyTargetHumanUsers(ctx context.Context, database queryer) (int, string, error) {
	rows, err := database.QueryContext(ctx, `
		SELECT id, invite_user_id, email, password_hash, balance, discount, commission_type, commission_rate,
		       commission_balance, transfer_enable, traffic_u, traffic_d, banned, is_admin,is_staff,is_distributor,distributor_name,
		       last_login_at, uuid, group_id, plan_id, speed_limit, expired_at, device_limit, online_count,
		       last_online_at, next_reset_at, last_reset_at, reset_count, subscription_token,
		       admin_revision, account_kind, created_at, updated_at
		FROM users WHERE account_kind = 'human' ORDER BY id
	`)
	if err != nil {
		return 0, "", fmt.Errorf("read imported legacy human users: %w", err)
	}
	defer rows.Close()
	users := make([]LegacyHumanUser, 0)
	count := 0
	for rows.Next() {
		var user LegacyHumanUser
		var inviteUserID, lastLoginAt, groupID, planID, expiredAt, lastOnlineAt, nextResetAt, lastResetAt sql.NullInt64
		var discount, commissionRate sql.NullInt64
		var distributorName sql.NullString
		var onlineCount int
		var revision int64
		var accountKind string
		if err := rows.Scan(&user.ID, &inviteUserID, &user.Email, &user.PasswordHash, &user.Balance, &discount,
			&user.CommissionType, &commissionRate, &user.CommissionBalance, &user.TransferEnable,
			&user.TrafficUpload, &user.TrafficDownload, &user.Banned, &user.IsAdmin, &user.IsStaff, &user.IsDistributor,
			&distributorName, &lastLoginAt, &user.UUID,
			&groupID, &planID, &user.SpeedLimit, &expiredAt, &user.DeviceLimit, &onlineCount, &lastOnlineAt,
			&nextResetAt, &lastResetAt, &user.ResetCount,
			&user.SubscriptionToken, &revision, &accountKind, &user.CreatedAt, &user.UpdatedAt); err != nil {
			return 0, "", fmt.Errorf("scan imported legacy human user: %w", err)
		}
		if onlineCount != 0 || revision != 1 || accountKind != AccountKindHuman {
			return 0, "", fmt.Errorf("imported legacy human user id %d has unexpected target state", user.ID)
		}
		user.InviteUserID = nullableInt64Pointer(inviteUserID)
		user.DistributorName = nullableStringPointer(distributorName)
		user.Discount = nullableIntPointer(discount)
		user.CommissionRate = nullableIntPointer(commissionRate)
		user.LastLoginAt = nullableInt64Pointer(lastLoginAt)
		user.GroupID = nullableInt64Pointer(groupID)
		user.PlanID = nullableInt64Pointer(planID)
		user.ExpiredAt = nullableInt64Pointer(expiredAt)
		user.LastOnlineAt = nullableInt64Pointer(lastOnlineAt)
		user.NextResetAt = nullableInt64Pointer(nextResetAt)
		user.LastResetAt = nullableInt64Pointer(lastResetAt)
		users = append(users, user)
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", fmt.Errorf("iterate imported legacy human users: %w", err)
	}
	return count, LegacyHumanUsersChecksum(users), nil
}

func nullableInt64Pointer(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}
