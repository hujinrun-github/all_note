package postgres

import (
	"strings"
	"testing"
)

func TestPostgresControlMigrationExecutionSQLKeepsChecksumSourceButSupportsPostgres10Triggers(t *testing.T) {
	t.Parallel()

	raw := []byte("CREATE TRIGGER example BEFORE UPDATE ON profiles FOR EACH ROW EXECUTE FUNCTION reject_mutation();")
	got := postgresControlMigrationExecutionSQL(raw)
	if strings.Contains(got, "EXECUTE FUNCTION") {
		t.Fatalf("execution SQL still requires PostgreSQL 11 syntax: %s", got)
	}
	if !strings.Contains(got, "EXECUTE PROCEDURE reject_mutation()") {
		t.Fatalf("execution SQL = %q, want PostgreSQL 10-compatible trigger invocation", got)
	}
	if string(raw) == got {
		t.Fatal("test fixture did not exercise compatibility rewrite")
	}
	if string(raw) != "CREATE TRIGGER example BEFORE UPDATE ON profiles FOR EACH ROW EXECUTE FUNCTION reject_mutation();" {
		t.Fatalf("compatibility rewrite mutated checksum source: %q", raw)
	}
}
