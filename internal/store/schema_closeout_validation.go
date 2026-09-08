package store

import (
	"context"
	"fmt"
	"strings"
	"unicode"
)

func validateSchemaV61Objects(ctx context.Context, database schemaQueryer) error {
	return validateDeclaredSchemaObjects(ctx, database, schemaV61CommissionWithdrawals)
}

// Only fixed, package-owned CREATE declarations are supported. Compare their
// definitions as well as their names so IF NOT EXISTS cannot preserve a dummy
// trigger, a non-unique index, or a weakened financial CHECK constraint.
func validateDeclaredSchemaObjects(ctx context.Context, database schemaQueryer, declarations string) error {
	expected := make(map[string]string)
	var args []any
	for _, declaration := range strings.Split(strings.TrimSpace(declarations), "\nCREATE ") {
		definition := "CREATE " + strings.TrimPrefix(declaration, "CREATE ")
		definition = strings.Replace(definition, " IF NOT EXISTS ", " ", 1)
		fields := strings.Fields(definition)
		nameIndex := 2
		if len(fields) > 2 && fields[1] == "UNIQUE" {
			nameIndex = 3
		}
		if len(fields) <= nameIndex || (fields[1] != "TABLE" && fields[1] != "INDEX" && !(fields[1] == "UNIQUE" && fields[2] == "INDEX") && fields[1] != "TRIGGER") {
			return fmt.Errorf("unsupported package schema declaration")
		}
		name := strings.SplitN(fields[nameIndex], "(", 2)[0]
		if _, duplicate := expected[name]; duplicate {
			return fmt.Errorf("duplicate package schema object %q", name)
		}
		expected[name] = definition
		args = append(args, name)
	}
	if len(args) == 0 {
		return fmt.Errorf("empty package schema declarations")
	}
	rows, err := database.QueryContext(ctx, `SELECT name, sql FROM sqlite_schema WHERE name IN (`+strings.TrimSuffix(strings.Repeat("?,", len(args)), ",")+`)`, args...)
	if err != nil {
		return fmt.Errorf("inspect required schema definitions: %w", err)
	}
	defer rows.Close()
	found := make(map[string]string, len(expected))
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			return fmt.Errorf("read required schema definition: %w", err)
		}
		found[name] = definition
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read required schema definitions: %w", err)
	}
	for name, definition := range expected {
		if normalizeProtectedSchemaDefinition(definition) != normalizeProtectedSchemaDefinition(found[name]) {
			return fmt.Errorf("required schema object %q is missing or invalid", name)
		}
	}
	return nil
}

func normalizeProtectedSchemaDefinition(value string) string {
	var normalized strings.Builder
	normalized.Grow(len(value))
	inLiteral := false
	for _, character := range value {
		if character == '\'' {
			inLiteral = !inLiteral
			normalized.WriteRune(character)
		} else if inLiteral {
			// A predicate on 'PENDING' is not equivalent to one on 'pending'.
			normalized.WriteRune(character)
		} else if !unicode.IsSpace(character) {
			normalized.WriteRune(unicode.ToLower(character))
		}
	}
	return strings.TrimSuffix(normalized.String(), ";")
}
