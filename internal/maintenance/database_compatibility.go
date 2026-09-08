package maintenance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Hao-Monster/Xboard-Go/internal/store"
)

const DefaultDatabaseCompatibilityTargetEngine = "sqlite"

type DatabaseCompatibilityReadiness struct {
	AsOf                       time.Time `json:"as_of"`
	TargetEngine               string    `json:"target_engine"`
	RepresentativeDataSupplied bool      `json:"representative_data_supplied"`
	DatabaseSize               int64     `json:"database_size"`
	SchemaVersion              int       `json:"schema_version"`
	CurrentSchemaVersion       int       `json:"current_schema_version"`
	CurrentSchema              bool      `json:"current_schema"`
	SchemaValid                bool      `json:"schema_valid"`
	IntegrityOK                bool      `json:"integrity_ok"`
	ForeignKeyViolations       int       `json:"foreign_key_violations"`
	QueryOnly                  bool      `json:"query_only"`
	SQLiteCandidate            bool      `json:"sqlite_candidate"`
	ProductionDecisionReady    bool      `json:"production_decision_ready"`
	Reasons                    []string  `json:"reasons"`
}

func CheckDatabaseCompatibilityReadiness(ctx context.Context, path string, now time.Time, targetEngine string, representativeDataSupplied bool) (DatabaseCompatibilityReadiness, error) {
	if err := ctx.Err(); err != nil {
		return DatabaseCompatibilityReadiness{}, err
	}
	if now.IsZero() || now.Unix() < 0 {
		return DatabaseCompatibilityReadiness{}, errors.New("database compatibility readiness requires a current time")
	}
	path = strings.TrimSpace(path)
	if path == "" {
		return DatabaseCompatibilityReadiness{}, errors.New("database compatibility readiness requires a SQLite database path")
	}
	targetEngine = normalizeDatabaseTargetEngine(targetEngine)

	info, err := os.Lstat(path)
	if err != nil {
		return DatabaseCompatibilityReadiness{}, fmt.Errorf("inspect database compatibility source: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() <= 0 {
		return DatabaseCompatibilityReadiness{}, errors.New("database compatibility source must be a non-empty regular file")
	}

	database, err := sql.Open("sqlite", readOnlySQLiteDSN(path))
	if err != nil {
		return DatabaseCompatibilityReadiness{}, fmt.Errorf("open database compatibility source: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	if err := database.PingContext(ctx); err != nil {
		return DatabaseCompatibilityReadiness{}, fmt.Errorf("ping database compatibility source: %w", err)
	}

	var queryOnly int
	if err := database.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&queryOnly); err != nil {
		return DatabaseCompatibilityReadiness{}, fmt.Errorf("inspect database query-only mode: %w", err)
	}
	var schemaVersion int
	if err := database.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		return DatabaseCompatibilityReadiness{}, fmt.Errorf("read database schema version: %w", err)
	}
	integrityOK, err := sqliteIntegrityOK(ctx, database)
	if err != nil {
		return DatabaseCompatibilityReadiness{}, err
	}
	foreignKeyViolations, err := sqliteForeignKeyViolationCount(ctx, database)
	if err != nil {
		return DatabaseCompatibilityReadiness{}, err
	}

	schemaErr := store.ValidateSchema(ctx, database, schemaVersion)
	schemaValid := schemaErr == nil
	currentSchema := schemaVersion == store.CurrentSchemaVersion()
	queryOnlyEnabled := queryOnly != 0
	sqliteCandidate := targetEngine == DefaultDatabaseCompatibilityTargetEngine &&
		currentSchema && schemaValid && integrityOK && foreignKeyViolations == 0 && queryOnlyEnabled
	productionReady := sqliteCandidate && representativeDataSupplied
	reasons := databaseCompatibilityReasons(targetEngine, representativeDataSupplied, currentSchema, schemaValid, integrityOK, foreignKeyViolations, queryOnlyEnabled, schemaErr)

	return DatabaseCompatibilityReadiness{
		AsOf:                       now.UTC(),
		TargetEngine:               targetEngine,
		RepresentativeDataSupplied: representativeDataSupplied,
		DatabaseSize:               info.Size(),
		SchemaVersion:              schemaVersion,
		CurrentSchemaVersion:       store.CurrentSchemaVersion(),
		CurrentSchema:              currentSchema,
		SchemaValid:                schemaValid,
		IntegrityOK:                integrityOK,
		ForeignKeyViolations:       foreignKeyViolations,
		QueryOnly:                  queryOnlyEnabled,
		SQLiteCandidate:            sqliteCandidate,
		ProductionDecisionReady:    productionReady,
		Reasons:                    reasons,
	}, nil
}

func normalizeDatabaseTargetEngine(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || value == "sqlite3" {
		return DefaultDatabaseCompatibilityTargetEngine
	}
	return value
}

func readOnlySQLiteDSN(path string) string {
	return "file:" + filepath.ToSlash(path) + "?mode=ro&_pragma=foreign_keys(1)&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
}

func sqliteIntegrityOK(ctx context.Context, database *sql.DB) (bool, error) {
	rows, err := database.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return false, fmt.Errorf("run database integrity check: %w", err)
	}
	defer rows.Close()
	checked := false
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return false, fmt.Errorf("read database integrity check: %w", err)
		}
		checked = true
		if result != "ok" {
			return false, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read database integrity check: %w", err)
	}
	return checked, nil
}

func sqliteForeignKeyViolationCount(ctx context.Context, database *sql.DB) (int, error) {
	rows, err := database.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return 0, fmt.Errorf("run database foreign-key check: %w", err)
	}
	defer rows.Close()
	violations := 0
	for rows.Next() {
		violations++
	}
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read database foreign-key check: %w", err)
	}
	return violations, nil
}

func databaseCompatibilityReasons(targetEngine string, representativeDataSupplied, currentSchema, schemaValid, integrityOK bool, foreignKeyViolations int, queryOnly bool, schemaErr error) []string {
	reasons := make([]string, 0, 6)
	if targetEngine != DefaultDatabaseCompatibilityTargetEngine {
		reasons = append(reasons, "only SQLite is implemented by the current runtime; non-SQLite production targets require a separate D-006 compatibility decision")
	}
	if !representativeDataSupplied {
		reasons = append(reasons, "representative production-shaped database evidence has not been supplied")
	}
	if !currentSchema {
		reasons = append(reasons, "database schema is not at the current Xboard-Go schema version")
	}
	if !schemaValid {
		if schemaErr != nil {
			reasons = append(reasons, "database schema validation failed: "+schemaErr.Error())
		} else {
			reasons = append(reasons, "database schema validation failed")
		}
	}
	if !integrityOK {
		reasons = append(reasons, "SQLite integrity_check did not return ok")
	}
	if foreignKeyViolations != 0 {
		reasons = append(reasons, "SQLite foreign_key_check reported violations")
	}
	if !queryOnly {
		reasons = append(reasons, "database inspection did not run in query-only mode")
	}
	if len(reasons) == 0 {
		reasons = append(reasons, "SQLite target has current schema, integrity, foreign-key validity, query-only inspection, and representative data evidence")
	}
	return reasons
}
