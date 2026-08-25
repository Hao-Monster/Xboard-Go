package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	LegacyNodesSlice = "nodes-v1"
	maxLegacyNodes   = 250_000
)

type LegacyMachine struct {
	ID         int64           `json:"id"`
	Name       string          `json:"name"`
	Notes      string          `json:"notes"`
	IsActive   bool            `json:"is_active"`
	LastSeenAt *int64          `json:"last_seen_at"`
	LoadStatus json.RawMessage `json:"load_status"`
	CreatedAt  int64           `json:"created_at"`
	UpdatedAt  int64           `json:"updated_at"`
}

type LegacyMachineCredential struct {
	ID          int64  `json:"id"`
	MachineID   int64  `json:"machine_id"`
	TokenHash   string `json:"token_hash"`
	TokenPrefix string `json:"token_prefix"`
	LastUsedAt  *int64 `json:"last_used_at"`
	RevokedAt   *int64 `json:"revoked_at"`
	CreatedAt   int64  `json:"created_at"`
}

type LegacyMachineEnrollment struct {
	ID             int64  `json:"id"`
	MachineID      int64  `json:"machine_id"`
	CodeHash       string `json:"code_hash"`
	RevokeExisting bool   `json:"revoke_existing"`
	ExpiresAt      int64  `json:"expires_at"`
	ConsumedAt     *int64 `json:"consumed_at"`
	CreatedAt      int64  `json:"created_at"`
}

type LegacyMachineLoad struct {
	ID          int64   `json:"id"`
	MachineID   int64   `json:"machine_id"`
	CPU         float64 `json:"cpu"`
	MemTotal    int64   `json:"mem_total"`
	MemUsed     int64   `json:"mem_used"`
	DiskTotal   int64   `json:"disk_total"`
	DiskUsed    int64   `json:"disk_used"`
	NetInSpeed  float64 `json:"net_in_speed"`
	NetOutSpeed float64 `json:"net_out_speed"`
	RecordedAt  int64   `json:"recorded_at"`
}

type LegacyNode struct {
	ID               int64           `json:"id"`
	Type             string          `json:"type"`
	ExternalCode     string          `json:"external_code"`
	ParentID         *int64          `json:"parent_id"`
	GroupIDs         []int64         `json:"group_ids"`
	RouteIDs         []int64         `json:"route_ids"`
	Name             string          `json:"name"`
	RateMicros       int64           `json:"rate_micros"`
	Tags             []string        `json:"tags"`
	Host             string          `json:"host"`
	Port             string          `json:"port"`
	ServerPort       int             `json:"server_port"`
	ProtocolSettings json.RawMessage `json:"protocol_settings"`
	Show             bool            `json:"show"`
	Sort             int             `json:"sort"`
	CreatedAt        int64           `json:"created_at"`
	UpdatedAt        int64           `json:"updated_at"`
	RateTimeEnabled  bool            `json:"rate_time_enabled"`
	RateTimeRanges   json.RawMessage `json:"rate_time_ranges"`
	CustomOutbounds  json.RawMessage `json:"custom_outbounds"`
	CustomRoutes     json.RawMessage `json:"custom_routes"`
	CertConfig       json.RawMessage `json:"cert_config"`
	TransferEnable   int64           `json:"transfer_enable"`
	TrafficUpload    int64           `json:"traffic_upload"`
	TrafficDownload  int64           `json:"traffic_download"`
	MachineID        *int64          `json:"machine_id"`
	Enabled          bool            `json:"enabled"`
	RuntimeConfig    json.RawMessage `json:"runtime_config"`
}

type LegacyActivationSchedule struct {
	NodeID            int64  `json:"node_id"`
	ScheduleType      string `json:"schedule_type"`
	Timezone          string `json:"timezone"`
	EnableSecond      *int64 `json:"enable_second"`
	DisableSecond     *int64 `json:"disable_second"`
	EnableAt          *int64 `json:"enable_at"`
	DisableAt         *int64 `json:"disable_at"`
	Revision          string `json:"revision"`
	NextTransitionAt  *int64 `json:"next_transition_at"`
	NextTargetEnabled *bool  `json:"next_target_enabled"`
	EnabledAppliedAt  *int64 `json:"enabled_applied_at"`
	DisabledAppliedAt *int64 `json:"disabled_applied_at"`
	CreatedAt         int64  `json:"created_at"`
	UpdatedAt         int64  `json:"updated_at"`
}

type LegacyNodeTrafficStat struct {
	NodeID     int64  `json:"node_id"`
	RecordAt   int64  `json:"record_at"`
	RecordType string `json:"record_type"`
	Upload     int64  `json:"upload"`
	Download   int64  `json:"download"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
}

type LegacyNodesChecksums struct {
	Machines  string `json:"machines"`
	Nodes     string `json:"nodes"`
	Schedules string `json:"schedules"`
	Traffic   string `json:"traffic"`
}

type LegacyNodesImport struct {
	Slice                string
	SourceSHA256         string
	SourceSize           int64
	Machines             []LegacyMachine
	Credentials          []LegacyMachineCredential
	Enrollments          []LegacyMachineEnrollment
	LoadHistory          []LegacyMachineLoad
	Nodes                []LegacyNode
	Schedules            []LegacyActivationSchedule
	Traffic              []LegacyNodeTrafficStat
	Checksums            LegacyNodesChecksums
	RollbackBackupPath   string
	RollbackBackupSHA256 string
}

type LegacyNodesImportReport struct {
	Slice                string             `json:"slice"`
	SourceSHA256         string             `json:"source_sha256"`
	SourceSize           int64              `json:"source_size"`
	RollbackBackupPath   string             `json:"rollback_backup_path"`
	RollbackBackupSHA256 string             `json:"rollback_backup_sha256"`
	Machines             LegacyDomainResult `json:"machines"`
	Nodes                LegacyDomainResult `json:"nodes"`
	Schedules            LegacyDomainResult `json:"schedules"`
	Traffic              LegacyDomainResult `json:"traffic"`
	AppliedAt            time.Time          `json:"applied_at"`
	AlreadyApplied       bool               `json:"already_applied"`
}

type legacyMachineBundle struct {
	Machines    []LegacyMachine           `json:"machines"`
	Credentials []LegacyMachineCredential `json:"credentials"`
	Enrollments []LegacyMachineEnrollment `json:"enrollments"`
	LoadHistory []LegacyMachineLoad       `json:"load_history"`
}

func LegacyMachinesChecksum(machines []LegacyMachine, credentials []LegacyMachineCredential, enrollments []LegacyMachineEnrollment, loads []LegacyMachineLoad) string {
	machines = append([]LegacyMachine(nil), machines...)
	credentials = append([]LegacyMachineCredential(nil), credentials...)
	enrollments = append([]LegacyMachineEnrollment(nil), enrollments...)
	loads = append([]LegacyMachineLoad(nil), loads...)
	sort.Slice(machines, func(i, j int) bool { return machines[i].ID < machines[j].ID })
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].ID < credentials[j].ID })
	sort.Slice(enrollments, func(i, j int) bool { return enrollments[i].ID < enrollments[j].ID })
	sort.Slice(loads, func(i, j int) bool { return loads[i].ID < loads[j].ID })
	return legacyCanonicalChecksum(legacyMachineBundle{nonNilMachines(machines), nonNilCredentials(credentials), nonNilEnrollments(enrollments), nonNilLoads(loads)})
}

func LegacyNodesChecksum(nodes []LegacyNode) string {
	ordered := append([]LegacyNode(nil), nodes...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	if ordered == nil {
		ordered = []LegacyNode{}
	}
	return legacyCanonicalChecksum(ordered)
}

func LegacySchedulesChecksum(schedules []LegacyActivationSchedule) string {
	ordered := append([]LegacyActivationSchedule(nil), schedules...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].NodeID < ordered[j].NodeID })
	if ordered == nil {
		ordered = []LegacyActivationSchedule{}
	}
	return legacyCanonicalChecksum(ordered)
}

func LegacyNodeTrafficChecksum(stats []LegacyNodeTrafficStat) string {
	ordered := append([]LegacyNodeTrafficStat(nil), stats...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].NodeID != ordered[j].NodeID {
			return ordered[i].NodeID < ordered[j].NodeID
		}
		if ordered[i].RecordAt != ordered[j].RecordAt {
			return ordered[i].RecordAt < ordered[j].RecordAt
		}
		return ordered[i].RecordType < ordered[j].RecordType
	})
	if ordered == nil {
		ordered = []LegacyNodeTrafficStat{}
	}
	return legacyCanonicalChecksum(ordered)
}

func nonNilMachines(value []LegacyMachine) []LegacyMachine {
	if value == nil {
		return []LegacyMachine{}
	}
	return value
}
func nonNilCredentials(value []LegacyMachineCredential) []LegacyMachineCredential {
	if value == nil {
		return []LegacyMachineCredential{}
	}
	return value
}
func nonNilEnrollments(value []LegacyMachineEnrollment) []LegacyMachineEnrollment {
	if value == nil {
		return []LegacyMachineEnrollment{}
	}
	return value
}
func nonNilLoads(value []LegacyMachineLoad) []LegacyMachineLoad {
	if value == nil {
		return []LegacyMachineLoad{}
	}
	return value
}

func (s *Store) LookupLegacyNodesImport(ctx context.Context, sourceSHA256 string) (LegacyNodesImportReport, bool, error) {
	if !validLowerSHA256(sourceSHA256) {
		return LegacyNodesImportReport{}, false, ErrInvalidInput
	}
	return lookupLegacyNodesImport(ctx, s.db, sourceSHA256)
}

func lookupLegacyNodesImport(ctx context.Context, database interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}, sourceSHA256 string) (LegacyNodesImportReport, bool, error) {
	var encoded string
	err := database.QueryRowContext(ctx, `SELECT report_json FROM legacy_migration_runs WHERE slice = ? AND source_sha256 = ?`, LegacyNodesSlice, sourceSHA256).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return LegacyNodesImportReport{}, false, nil
	}
	if err != nil {
		return LegacyNodesImportReport{}, false, fmt.Errorf("lookup legacy node migration: %w", err)
	}
	var report LegacyNodesImportReport
	if err := json.Unmarshal([]byte(encoded), &report); err != nil {
		return LegacyNodesImportReport{}, false, fmt.Errorf("decode legacy node migration report: %w", err)
	}
	report.AlreadyApplied = true
	return report, true, nil
}

func (s *Store) ImportLegacyNodes(ctx context.Context, input LegacyNodesImport, now time.Time) (LegacyNodesImportReport, error) {
	if err := validateLegacyNodesImport(input); err != nil {
		return LegacyNodesImportReport{}, err
	}
	defer s.lockWrite()()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return LegacyNodesImportReport{}, fmt.Errorf("begin legacy node import: %w", err)
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return LegacyNodesImportReport{}, fmt.Errorf("read legacy node target schema: %w", err)
	}
	if version != CurrentSchemaVersion() {
		return LegacyNodesImportReport{}, fmt.Errorf("legacy node import requires current schema %d, found %d", CurrentSchemaVersion(), version)
	}
	if err := ValidateSchema(ctx, tx, version); err != nil {
		return LegacyNodesImportReport{}, fmt.Errorf("validate legacy node target schema: %w", err)
	}
	if existing, found, err := lookupLegacyNodesImport(ctx, tx, input.SourceSHA256); err != nil {
		return LegacyNodesImportReport{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return LegacyNodesImportReport{}, fmt.Errorf("commit idempotent legacy node import: %w", err)
		}
		return existing, nil
	}
	var otherRuns int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_migration_runs WHERE slice = ?`, LegacyNodesSlice).Scan(&otherRuns); err != nil {
		return LegacyNodesImportReport{}, err
	}
	if otherRuns != 0 {
		return LegacyNodesImportReport{}, fmt.Errorf("%w: legacy node slice was already imported from another snapshot", ErrConflict)
	}
	for _, table := range []string{
		"server_machines", "server_machine_credentials", "server_machine_enrollments", "server_machine_load_history",
		"nodes", "node_protocol_definitions", "node_group_memberships", "node_route_memberships",
		"server_activation_schedules", "node_traffic_stats", "node_report_receipts", "node_report_traffic_stage",
		"node_device_ips", "node_user_online", "node_runtime_state",
	} {
		var count int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table).Scan(&count); err != nil {
			return LegacyNodesImportReport{}, fmt.Errorf("count target %s: %w", table, err)
		}
		if count != 0 {
			return LegacyNodesImportReport{}, fmt.Errorf("%w: legacy node import requires empty target %s", ErrConflict, table)
		}
	}
	if err := validateLegacyNodeReferencesInTarget(ctx, tx, input.Nodes); err != nil {
		return LegacyNodesImportReport{}, err
	}
	if err := insertLegacyMachines(ctx, tx, input); err != nil {
		return LegacyNodesImportReport{}, err
	}
	if err := insertLegacyNodes(ctx, tx, input.Nodes); err != nil {
		return LegacyNodesImportReport{}, err
	}
	if err := insertLegacySchedulesAndTraffic(ctx, tx, input.Schedules, input.Traffic); err != nil {
		return LegacyNodesImportReport{}, err
	}

	targetMachines, targetCredentials, targetEnrollments, targetLoads, err := readLegacyTargetMachines(ctx, tx)
	if err != nil {
		return LegacyNodesImportReport{}, err
	}
	targetNodes, err := readLegacyTargetNodes(ctx, tx)
	if err != nil {
		return LegacyNodesImportReport{}, err
	}
	targetSchedules, err := readLegacyTargetSchedules(ctx, tx)
	if err != nil {
		return LegacyNodesImportReport{}, err
	}
	targetTraffic, err := readLegacyTargetNodeTraffic(ctx, tx)
	if err != nil {
		return LegacyNodesImportReport{}, err
	}
	report := LegacyNodesImportReport{
		Slice: LegacyNodesSlice, SourceSHA256: input.SourceSHA256, SourceSize: input.SourceSize,
		RollbackBackupPath: input.RollbackBackupPath, RollbackBackupSHA256: input.RollbackBackupSHA256,
		Machines:  LegacyDomainResult{SourceRows: len(input.Machines), TargetRows: len(targetMachines), SourceChecksum: input.Checksums.Machines, TargetChecksum: LegacyMachinesChecksum(targetMachines, targetCredentials, targetEnrollments, targetLoads)},
		Nodes:     LegacyDomainResult{SourceRows: len(input.Nodes), TargetRows: len(targetNodes), SourceChecksum: input.Checksums.Nodes, TargetChecksum: LegacyNodesChecksum(targetNodes)},
		Schedules: LegacyDomainResult{SourceRows: len(input.Schedules), TargetRows: len(targetSchedules), SourceChecksum: input.Checksums.Schedules, TargetChecksum: LegacySchedulesChecksum(targetSchedules)},
		Traffic:   LegacyDomainResult{SourceRows: len(input.Traffic), TargetRows: len(targetTraffic), SourceChecksum: input.Checksums.Traffic, TargetChecksum: LegacyNodeTrafficChecksum(targetTraffic)},
		AppliedAt: now.UTC(),
	}
	if report.Machines.SourceRows != report.Machines.TargetRows || report.Machines.SourceChecksum != report.Machines.TargetChecksum ||
		report.Nodes.SourceRows != report.Nodes.TargetRows || report.Nodes.SourceChecksum != report.Nodes.TargetChecksum ||
		report.Schedules.SourceRows != report.Schedules.TargetRows || report.Schedules.SourceChecksum != report.Schedules.TargetChecksum ||
		report.Traffic.SourceRows != report.Traffic.TargetRows || report.Traffic.SourceChecksum != report.Traffic.TargetChecksum {
		return LegacyNodesImportReport{}, errors.New("legacy node target verification does not match source")
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		return LegacyNodesImportReport{}, fmt.Errorf("encode legacy node migration report: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_migration_runs (slice, source_sha256, source_size, rollback_backup_path, rollback_backup_sha256, report_json, applied_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, report.Slice, report.SourceSHA256, report.SourceSize, report.RollbackBackupPath, report.RollbackBackupSHA256, string(encoded), report.AppliedAt.Unix()); err != nil {
		return LegacyNodesImportReport{}, fmt.Errorf("record legacy node migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return LegacyNodesImportReport{}, fmt.Errorf("commit legacy node import: %w", err)
	}
	return report, nil
}

func validateLegacyNodesImport(input LegacyNodesImport) error {
	if input.Slice != LegacyNodesSlice || !validLowerSHA256(input.SourceSHA256) || input.SourceSize < 1 || !validLowerSHA256(input.RollbackBackupSHA256) || input.RollbackBackupPath == "" || len(input.RollbackBackupPath) > 4096 || !utf8.ValidString(input.RollbackBackupPath) || strings.IndexFunc(input.RollbackBackupPath, unicode.IsControl) >= 0 {
		return ErrInvalidInput
	}
	if err := ValidateLegacyNodesData(input); err != nil {
		return err
	}
	checks := LegacyNodesChecksums{
		Machines: LegacyMachinesChecksum(input.Machines, input.Credentials, input.Enrollments, input.LoadHistory), Nodes: LegacyNodesChecksum(input.Nodes),
		Schedules: LegacySchedulesChecksum(input.Schedules), Traffic: LegacyNodeTrafficChecksum(input.Traffic),
	}
	if checks != input.Checksums {
		return fmt.Errorf("%w: legacy node source checksum mismatch", ErrInvalidInput)
	}
	return nil
}

func ValidateLegacyNodesData(input LegacyNodesImport) error {
	if len(input.Machines) > maxLegacyNodes || len(input.Nodes) > maxLegacyNodes || len(input.Credentials) > maxLegacyNodes*3 || len(input.Enrollments) > maxLegacyNodes*4 || len(input.LoadHistory) > maxLegacyNodes*100 || len(input.Schedules) > maxLegacyNodes || len(input.Traffic) > maxLegacyNodes*100 {
		return ErrInvalidInput
	}
	machines := make(map[int64]struct{}, len(input.Machines))
	for _, item := range input.Machines {
		if item.ID < 1 || strings.TrimSpace(item.Name) != item.Name || item.Name == "" || len(item.Name) > 255 || strings.TrimSpace(item.Notes) != item.Notes || len(item.Notes) > 4000 || item.CreatedAt < 0 || item.UpdatedAt < item.CreatedAt || !validLegacyJSONContainerOrNull(item.LoadStatus) {
			return fmt.Errorf("%w: invalid legacy machine id %d", ErrInvalidInput, item.ID)
		}
		if _, exists := machines[item.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy machine id %d", ErrInvalidInput, item.ID)
		}
		machines[item.ID] = struct{}{}
	}
	credentialIDs := map[int64]struct{}{}
	credentialHashes := map[string]struct{}{}
	for _, item := range input.Credentials {
		if item.ID < 1 || !validLowerSHA256(item.TokenHash) || item.TokenPrefix == "" || len(item.TokenPrefix) > 64 || item.CreatedAt < 0 {
			return fmt.Errorf("%w: invalid legacy machine credential", ErrInvalidInput)
		}
		if _, ok := machines[item.MachineID]; !ok {
			return fmt.Errorf("%w: credential references missing machine", ErrInvalidInput)
		}
		if _, exists := credentialIDs[item.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy credential id", ErrInvalidInput)
		}
		credentialIDs[item.ID] = struct{}{}
		if _, exists := credentialHashes[item.TokenHash]; exists {
			return fmt.Errorf("%w: duplicate legacy credential hash", ErrInvalidInput)
		}
		credentialHashes[item.TokenHash] = struct{}{}
		if item.LastUsedAt != nil && *item.LastUsedAt < item.CreatedAt || item.RevokedAt != nil && *item.RevokedAt < item.CreatedAt {
			return fmt.Errorf("%w: invalid legacy credential timestamp", ErrInvalidInput)
		}
	}
	enrollmentIDs, enrollmentHashes := map[int64]struct{}{}, map[string]struct{}{}
	for _, item := range input.Enrollments {
		if item.ID < 1 || !validLowerSHA256(item.CodeHash) || item.CreatedAt < 0 || item.ExpiresAt <= item.CreatedAt {
			return fmt.Errorf("%w: invalid legacy machine enrollment", ErrInvalidInput)
		}
		if _, ok := machines[item.MachineID]; !ok {
			return fmt.Errorf("%w: enrollment references missing machine", ErrInvalidInput)
		}
		if _, exists := enrollmentIDs[item.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy enrollment id", ErrInvalidInput)
		}
		if _, exists := enrollmentHashes[item.CodeHash]; exists {
			return fmt.Errorf("%w: duplicate legacy enrollment hash", ErrInvalidInput)
		}
		enrollmentIDs[item.ID], enrollmentHashes[item.CodeHash] = struct{}{}, struct{}{}
		if item.ConsumedAt != nil && *item.ConsumedAt < item.CreatedAt {
			return fmt.Errorf("%w: invalid legacy enrollment timestamp", ErrInvalidInput)
		}
	}
	loadIDs := map[int64]struct{}{}
	for _, item := range input.LoadHistory {
		if item.ID < 1 || math.IsNaN(item.CPU) || math.IsInf(item.CPU, 0) || math.IsNaN(item.NetInSpeed) || math.IsInf(item.NetInSpeed, 0) || math.IsNaN(item.NetOutSpeed) || math.IsInf(item.NetOutSpeed, 0) || item.CPU < 0 || item.MemTotal < 0 || item.MemUsed < 0 || item.MemUsed > item.MemTotal || item.DiskTotal < 0 || item.DiskUsed < 0 || item.DiskUsed > item.DiskTotal || item.NetInSpeed < 0 || item.NetOutSpeed < 0 || item.RecordedAt < 0 {
			return fmt.Errorf("%w: invalid legacy machine load", ErrInvalidInput)
		}
		if _, ok := machines[item.MachineID]; !ok {
			return fmt.Errorf("%w: load history references missing machine", ErrInvalidInput)
		}
		if _, exists := loadIDs[item.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy machine load id", ErrInvalidInput)
		}
		loadIDs[item.ID] = struct{}{}
	}
	nodes := make(map[int64]struct{}, len(input.Nodes))
	for _, item := range input.Nodes {
		if item.ID < 1 || strings.TrimSpace(item.Name) != item.Name || item.Name == "" || len(item.Name) > 255 || strings.TrimSpace(item.Host) != item.Host || item.Host == "" || len(item.Host) > 255 || !validNodePort(item.Port) || item.ServerPort < 1 || item.ServerPort > 65535 || item.RateMicros < 0 || item.RateMicros > 1_000_000_000 || item.TransferEnable < 0 || item.TrafficUpload < 0 || item.TrafficDownload < 0 || item.CreatedAt < 0 || item.UpdatedAt < item.CreatedAt {
			return fmt.Errorf("%w: invalid legacy node id %d", ErrInvalidInput, item.ID)
		}
		if _, ok := supportedNodeTypes[item.Type]; !ok {
			return fmt.Errorf("%w: unsupported legacy node type %q", ErrInvalidInput, item.Type)
		}
		if item.ExternalCode != "" && (strings.TrimSpace(item.ExternalCode) != item.ExternalCode || !utf8.ValidString(item.ExternalCode) || strings.IndexFunc(item.ExternalCode, unicode.IsControl) >= 0 || len(item.ExternalCode) > 255) {
			return fmt.Errorf("%w: invalid legacy node code", ErrInvalidInput)
		}
		if _, exists := nodes[item.ID]; exists {
			return fmt.Errorf("%w: duplicate legacy node id %d", ErrInvalidInput, item.ID)
		}
		nodes[item.ID] = struct{}{}
		if item.MachineID != nil {
			if _, ok := machines[*item.MachineID]; !ok {
				return fmt.Errorf("%w: node references missing machine", ErrInvalidInput)
			}
		}
		if item.GroupIDs == nil || item.RouteIDs == nil || item.Tags == nil || len(item.Tags) > 1000 || !validLegacyStringArray(item.Tags) || !validLegacyJSONObject(item.ProtocolSettings) || !validLegacyJSONArray(item.RateTimeRanges) || !validLegacyJSONArray(item.CustomOutbounds) || !validLegacyJSONArray(item.CustomRoutes) || !validLegacyJSONObject(item.CertConfig) || !validLegacyJSONObject(item.RuntimeConfig) || len(item.ProtocolSettings) > maxRuntimeConfigBytes || len(item.RateTimeRanges) > maxRuntimeConfigBytes || len(item.CustomOutbounds) > maxRuntimeConfigBytes || len(item.CustomRoutes) > maxRuntimeConfigBytes || len(item.CertConfig) > maxRuntimeConfigBytes || len(item.RuntimeConfig) > maxRuntimeConfigBytes {
			return fmt.Errorf("%w: invalid legacy node JSON", ErrInvalidInput)
		}
		if err := validateLegacyRateRanges(item.RateTimeEnabled, item.RateTimeRanges); err != nil {
			return fmt.Errorf("%w: node id %d: %v", ErrInvalidInput, item.ID, err)
		}
		if !sort.SliceIsSorted(item.GroupIDs, func(i, j int) bool { return item.GroupIDs[i] < item.GroupIDs[j] }) || !sort.SliceIsSorted(item.RouteIDs, func(i, j int) bool { return item.RouteIDs[i] < item.RouteIDs[j] }) || hasDuplicateInt64(item.GroupIDs) || hasDuplicateInt64(item.RouteIDs) {
			return fmt.Errorf("%w: node memberships must be unique and sorted", ErrInvalidInput)
		}
	}
	for _, item := range input.Nodes {
		if item.ParentID != nil {
			if _, ok := nodes[*item.ParentID]; !ok || *item.ParentID == item.ID {
				return fmt.Errorf("%w: node references invalid parent", ErrInvalidInput)
			}
		}
	}
	seenSchedules := map[int64]struct{}{}
	for _, item := range input.Schedules {
		if _, ok := nodes[item.NodeID]; !ok || item.Revision == "" || len(item.Revision) > 255 || item.CreatedAt < 0 || item.UpdatedAt < item.CreatedAt {
			return fmt.Errorf("%w: invalid legacy activation schedule", ErrInvalidInput)
		}
		if _, exists := seenSchedules[item.NodeID]; exists {
			return fmt.Errorf("%w: duplicate legacy activation schedule", ErrInvalidInput)
		}
		seenSchedules[item.NodeID] = struct{}{}
		if item.ScheduleType == "daily" {
			if item.Timezone != "Asia/Singapore" || item.EnableSecond == nil || item.DisableSecond == nil || *item.EnableSecond < 0 || *item.EnableSecond > 86399 || *item.DisableSecond < 0 || *item.DisableSecond > 86399 || *item.EnableSecond == *item.DisableSecond {
				return fmt.Errorf("%w: invalid daily activation schedule", ErrInvalidInput)
			}
		} else if item.ScheduleType == "once" {
			if item.Timezone != "" || item.EnableAt == nil || item.DisableAt == nil || *item.EnableAt >= *item.DisableAt {
				return fmt.Errorf("%w: invalid once activation schedule", ErrInvalidInput)
			}
		} else {
			return fmt.Errorf("%w: invalid activation schedule type", ErrInvalidInput)
		}
	}
	seenStats := map[string]struct{}{}
	for _, item := range input.Traffic {
		if _, ok := nodes[item.NodeID]; !ok || item.RecordAt < 0 || (item.RecordType != "d" && item.RecordType != "m") || item.Upload < 0 || item.Download < 0 || item.CreatedAt < 0 || item.UpdatedAt < item.CreatedAt {
			return fmt.Errorf("%w: invalid legacy node traffic statistic", ErrInvalidInput)
		}
		key := fmt.Sprintf("%d/%d/%s", item.NodeID, item.RecordAt, item.RecordType)
		if _, exists := seenStats[key]; exists {
			return fmt.Errorf("%w: duplicate legacy node traffic statistic", ErrInvalidInput)
		}
		seenStats[key] = struct{}{}
	}
	return nil
}

func validLegacyJSONObject(raw json.RawMessage) bool {
	var value map[string]any
	return json.Unmarshal(raw, &value) == nil && value != nil
}
func validLegacyJSONContainerOrNull(raw json.RawMessage) bool {
	if string(raw) == "null" {
		return true
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return false
	}
	switch value.(type) {
	case map[string]any, []any:
		return true
	default:
		return false
	}
}
func validLegacyJSONArray(raw json.RawMessage) bool {
	var value []any
	return json.Unmarshal(raw, &value) == nil && value != nil
}
func validLegacyStringArray(values []string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) != value || value == "" || len(value) > 255 {
			return false
		}
	}
	return true
}
func hasDuplicateInt64(values []int64) bool {
	for i := 1; i < len(values); i++ {
		if values[i] == values[i-1] {
			return true
		}
	}
	return false
}

func validateLegacyRateRanges(enabled bool, raw json.RawMessage) error {
	var ranges []nodeRateRange
	if err := json.Unmarshal(raw, &ranges); err != nil {
		return errors.New("invalid rate ranges")
	}
	if !enabled {
		return nil
	}
	if len(ranges) == 0 || len(ranges) > 96 {
		return errors.New("enabled rate ranges must contain 1-96 entries")
	}
	for _, item := range ranges {
		if !validClockMinute(item.Start) || !validClockMinute(item.End) || item.Rate < 0 || item.Rate > 1000 {
			return errors.New("invalid rate range")
		}
	}
	return nil
}

func validClockMinute(value string) bool {
	if len(value) != 5 || value[2] != ':' ||
		value[0] < '0' || value[0] > '9' || value[1] < '0' || value[1] > '9' ||
		value[3] < '0' || value[3] > '9' || value[4] < '0' || value[4] > '9' {
		return false
	}
	hour := int(value[0]-'0')*10 + int(value[1]-'0')
	minute := int(value[3]-'0')*10 + int(value[4]-'0')
	return hour >= 0 && hour < 24 && minute >= 0 && minute < 60
}

func validateLegacyNodeReferencesInTarget(ctx context.Context, tx *sql.Tx, nodes []LegacyNode) error {
	groupSet, routeSet := map[int64]struct{}{}, map[int64]struct{}{}
	for _, node := range nodes {
		for _, id := range node.GroupIDs {
			groupSet[id] = struct{}{}
		}
		for _, id := range node.RouteIDs {
			routeSet[id] = struct{}{}
		}
	}
	for table, ids := range map[string]map[int64]struct{}{"server_groups": groupSet, "routing_rules": routeSet} {
		for id := range ids {
			var exists bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM `+table+` WHERE id = ?)`, id).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				return fmt.Errorf("%w: legacy node references missing %s id %d", ErrConflict, table, id)
			}
		}
	}
	return nil
}

func insertLegacyMachines(ctx context.Context, tx *sql.Tx, input LegacyNodesImport) error {
	for _, item := range input.Machines {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_machines (id,name,notes,is_active,last_seen_at,load_status,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, item.ID, item.Name, item.Notes, item.IsActive, item.LastSeenAt, item.LoadStatus, item.CreatedAt, item.UpdatedAt); err != nil {
			return fmt.Errorf("import legacy machine id %d: %w", item.ID, err)
		}
	}
	for _, item := range input.Credentials {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_machine_credentials (id,machine_id,token_hash,token_prefix,last_used_at,revoked_at,created_at) VALUES (?,?,?,?,?,?,?)`, item.ID, item.MachineID, item.TokenHash, item.TokenPrefix, item.LastUsedAt, item.RevokedAt, item.CreatedAt); err != nil {
			return fmt.Errorf("import legacy credential id %d: %w", item.ID, err)
		}
	}
	for _, item := range input.Enrollments {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_machine_enrollments (id,machine_id,code_hash,revoke_existing,expires_at,consumed_at,created_at) VALUES (?,?,?,?,?,?,?)`, item.ID, item.MachineID, item.CodeHash, item.RevokeExisting, item.ExpiresAt, item.ConsumedAt, item.CreatedAt); err != nil {
			return fmt.Errorf("import legacy enrollment id %d: %w", item.ID, err)
		}
	}
	for _, item := range input.LoadHistory {
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_machine_load_history (id,machine_id,cpu,mem_total,mem_used,disk_total,disk_used,net_in_speed,net_out_speed,recorded_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, item.ID, item.MachineID, item.CPU, item.MemTotal, item.MemUsed, item.DiskTotal, item.DiskUsed, item.NetInSpeed, item.NetOutSpeed, item.RecordedAt); err != nil {
			return fmt.Errorf("import legacy machine load id %d: %w", item.ID, err)
		}
	}
	return nil
}

func insertLegacyNodes(ctx context.Context, tx *sql.Tx, nodes []LegacyNode) error {
	for _, item := range nodes {
		hotRate := max(int64(1), item.RateMicros)
		if _, err := tx.ExecContext(ctx, `INSERT INTO nodes (id,name,type,host,port,show,enabled,sort,machine_id,created_at,updated_at,rate_micros,runtime_config,traffic_u,traffic_d) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.Name, item.Type, item.Host, item.Port, item.Show, item.Enabled, item.Sort, item.MachineID, item.CreatedAt, item.UpdatedAt, hotRate, item.RuntimeConfig, item.TrafficUpload, item.TrafficDownload); err != nil {
			return fmt.Errorf("import legacy node id %d: %w", item.ID, err)
		}
	}
	for _, item := range nodes {
		tags, _ := json.Marshal(item.Tags)
		code := any(nil)
		if item.ExternalCode != "" {
			code = item.ExternalCode
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_protocol_definitions (node_id,external_code,parent_id,server_port,tags_json,protocol_settings_json,rate_time_enabled,rate_time_ranges_json,custom_outbounds_json,custom_routes_json,cert_config_json,transfer_enable,configured_rate_micros) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.ID, code, item.ParentID, item.ServerPort, string(tags), item.ProtocolSettings, item.RateTimeEnabled, item.RateTimeRanges, item.CustomOutbounds, item.CustomRoutes, item.CertConfig, item.TransferEnable, item.RateMicros); err != nil {
			return fmt.Errorf("import legacy node definition id %d: %w", item.ID, err)
		}
		for _, id := range item.GroupIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO node_group_memberships (node_id,group_id) VALUES (?,?)`, item.ID, id); err != nil {
				return err
			}
		}
		for _, id := range item.RouteIDs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO node_route_memberships (node_id,route_id) VALUES (?,?)`, item.ID, id); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertLegacySchedulesAndTraffic(ctx context.Context, tx *sql.Tx, schedules []LegacyActivationSchedule, stats []LegacyNodeTrafficStat) error {
	for _, item := range schedules {
		var timezone any
		if item.Timezone != "" {
			timezone = item.Timezone
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO server_activation_schedules (node_id,schedule_type,timezone,enable_second,disable_second,enable_at,disable_at,revision,next_transition_at,next_target_enabled,enabled_applied_at,disabled_applied_at,created_at,updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.NodeID, item.ScheduleType, timezone, item.EnableSecond, item.DisableSecond, item.EnableAt, item.DisableAt, item.Revision, item.NextTransitionAt, item.NextTargetEnabled, item.EnabledAppliedAt, item.DisabledAppliedAt, item.CreatedAt, item.UpdatedAt); err != nil {
			return fmt.Errorf("import legacy schedule for node %d: %w", item.NodeID, err)
		}
	}
	for _, item := range stats {
		if _, err := tx.ExecContext(ctx, `INSERT INTO node_traffic_stats (node_id,record_at,record_type,upload,download,created_at,updated_at) VALUES (?,?,?,?,?,?,?)`, item.NodeID, item.RecordAt, item.RecordType, item.Upload, item.Download, item.CreatedAt, item.UpdatedAt); err != nil {
			return fmt.Errorf("import legacy traffic for node %d: %w", item.NodeID, err)
		}
	}
	return nil
}

func readLegacyTargetMachines(ctx context.Context, database queryer) ([]LegacyMachine, []LegacyMachineCredential, []LegacyMachineEnrollment, []LegacyMachineLoad, error) {
	machines := []LegacyMachine{}
	rows, err := database.QueryContext(ctx, `SELECT id,name,notes,is_active,last_seen_at,COALESCE(load_status,'null'),created_at,updated_at FROM server_machines ORDER BY id`)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var item LegacyMachine
		if err := rows.Scan(&item.ID, &item.Name, &item.Notes, &item.IsActive, &item.LastSeenAt, &item.LoadStatus, &item.CreatedAt, &item.UpdatedAt); err != nil {
			rows.Close()
			return nil, nil, nil, nil, err
		}
		machines = append(machines, item)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}
	credentials := []LegacyMachineCredential{}
	rows, err = database.QueryContext(ctx, `SELECT id,machine_id,token_hash,token_prefix,last_used_at,revoked_at,created_at FROM server_machine_credentials ORDER BY id`)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var item LegacyMachineCredential
		if err := rows.Scan(&item.ID, &item.MachineID, &item.TokenHash, &item.TokenPrefix, &item.LastUsedAt, &item.RevokedAt, &item.CreatedAt); err != nil {
			rows.Close()
			return nil, nil, nil, nil, err
		}
		credentials = append(credentials, item)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}
	enrollments := []LegacyMachineEnrollment{}
	rows, err = database.QueryContext(ctx, `SELECT id,machine_id,code_hash,revoke_existing,expires_at,consumed_at,created_at FROM server_machine_enrollments ORDER BY id`)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var item LegacyMachineEnrollment
		if err := rows.Scan(&item.ID, &item.MachineID, &item.CodeHash, &item.RevokeExisting, &item.ExpiresAt, &item.ConsumedAt, &item.CreatedAt); err != nil {
			rows.Close()
			return nil, nil, nil, nil, err
		}
		enrollments = append(enrollments, item)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}
	loads := []LegacyMachineLoad{}
	rows, err = database.QueryContext(ctx, `SELECT id,machine_id,cpu,mem_total,mem_used,disk_total,disk_used,net_in_speed,net_out_speed,recorded_at FROM server_machine_load_history ORDER BY id`)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	for rows.Next() {
		var item LegacyMachineLoad
		if err := rows.Scan(&item.ID, &item.MachineID, &item.CPU, &item.MemTotal, &item.MemUsed, &item.DiskTotal, &item.DiskUsed, &item.NetInSpeed, &item.NetOutSpeed, &item.RecordedAt); err != nil {
			rows.Close()
			return nil, nil, nil, nil, err
		}
		loads = append(loads, item)
	}
	if err := rows.Close(); err != nil {
		return nil, nil, nil, nil, err
	}
	return machines, credentials, enrollments, loads, nil
}

func readLegacyTargetNodes(ctx context.Context, database queryer) ([]LegacyNode, error) {
	rows, err := database.QueryContext(ctx, `SELECT n.id,n.type,COALESCE(d.external_code,''),d.parent_id,n.name,d.configured_rate_micros,d.tags_json,n.host,n.port,d.server_port,d.protocol_settings_json,n.show,n.sort,n.created_at,n.updated_at,d.rate_time_enabled,d.rate_time_ranges_json,d.custom_outbounds_json,d.custom_routes_json,d.cert_config_json,d.transfer_enable,n.traffic_u,n.traffic_d,n.machine_id,n.enabled,n.runtime_config FROM nodes n JOIN node_protocol_definitions d ON d.node_id=n.id ORDER BY n.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []LegacyNode{}
	for rows.Next() {
		var item LegacyNode
		var tags string
		if err := rows.Scan(&item.ID, &item.Type, &item.ExternalCode, &item.ParentID, &item.Name, &item.RateMicros, &tags, &item.Host, &item.Port, &item.ServerPort, &item.ProtocolSettings, &item.Show, &item.Sort, &item.CreatedAt, &item.UpdatedAt, &item.RateTimeEnabled, &item.RateTimeRanges, &item.CustomOutbounds, &item.CustomRoutes, &item.CertConfig, &item.TransferEnable, &item.TrafficUpload, &item.TrafficDownload, &item.MachineID, &item.Enabled, &item.RuntimeConfig); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(tags), &item.Tags); err != nil {
			return nil, err
		}
		item.GroupIDs = []int64{}
		item.RouteIDs = []int64{}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byID := make(map[int64]*LegacyNode, len(result))
	for i := range result {
		byID[result[i].ID] = &result[i]
	}
	for _, spec := range []struct {
		query string
		route bool
	}{{`SELECT node_id,group_id FROM node_group_memberships ORDER BY node_id,group_id`, false}, {`SELECT node_id,route_id FROM node_route_memberships ORDER BY node_id,route_id`, true}} {
		memberRows, err := database.QueryContext(ctx, spec.query)
		if err != nil {
			return nil, err
		}
		for memberRows.Next() {
			var nodeID, id int64
			if err := memberRows.Scan(&nodeID, &id); err != nil {
				memberRows.Close()
				return nil, err
			}
			node := byID[nodeID]
			if node == nil {
				memberRows.Close()
				return nil, errors.New("orphan node membership")
			}
			if spec.route {
				node.RouteIDs = append(node.RouteIDs, id)
			} else {
				node.GroupIDs = append(node.GroupIDs, id)
			}
		}
		if err := memberRows.Close(); err != nil {
			return nil, err
		}
	}
	return result, nil
}

func readLegacyTargetSchedules(ctx context.Context, database queryer) ([]LegacyActivationSchedule, error) {
	rows, err := database.QueryContext(ctx, `SELECT node_id,schedule_type,COALESCE(timezone,''),enable_second,disable_second,enable_at,disable_at,revision,next_transition_at,next_target_enabled,enabled_applied_at,disabled_applied_at,created_at,updated_at FROM server_activation_schedules ORDER BY node_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []LegacyActivationSchedule{}
	for rows.Next() {
		var item LegacyActivationSchedule
		if err := rows.Scan(&item.NodeID, &item.ScheduleType, &item.Timezone, &item.EnableSecond, &item.DisableSecond, &item.EnableAt, &item.DisableAt, &item.Revision, &item.NextTransitionAt, &item.NextTargetEnabled, &item.EnabledAppliedAt, &item.DisabledAppliedAt, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func readLegacyTargetNodeTraffic(ctx context.Context, database queryer) ([]LegacyNodeTrafficStat, error) {
	rows, err := database.QueryContext(ctx, `SELECT node_id,record_at,record_type,upload,download,created_at,updated_at FROM node_traffic_stats ORDER BY node_id,record_at,record_type`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []LegacyNodeTrafficStat{}
	for rows.Next() {
		var item LegacyNodeTrafficStat
		if err := rows.Scan(&item.NodeID, &item.RecordAt, &item.RecordType, &item.Upload, &item.Download, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
