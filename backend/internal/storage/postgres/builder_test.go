package postgres

import "testing"

func TestPgPlaceholders(t *testing.T) {
	if got := pgPlaceholder(5); got != "$5" {
		t.Fatalf("expected placeholder $5, got %q", got)
	}

	got := pgPlaceholders(2, 3)
	if got != "$2,$3,$4" {
		t.Fatalf("expected placeholders $2,$3,$4, got %q", got)
	}

	if got := pgPlaceholders(2, 0); got != "" {
		t.Fatalf("expected no placeholders, got %q", got)
	}
}

func TestPgInClauseRejectsEmptyValues(t *testing.T) {
	if _, err := pgInClause("id", 1, 0); err == nil {
		t.Fatal("expected empty IN clause to fail")
	}
}

func TestPgInClause(t *testing.T) {
	got, err := pgInClause("id", 3, 2)
	if err != nil {
		t.Fatalf("in clause: %v", err)
	}
	if got != "id IN ($3,$4)" {
		t.Fatalf("unexpected IN clause: %q", got)
	}
}

func TestPgSetBuilder(t *testing.T) {
	b := newPgSetBuilder(2)
	b.Add("title", "new title")
	b.Add("updated_at", int64(1800000000))

	clause, args := b.ClauseAndArgs()
	if clause != "title = $2, updated_at = $3" {
		t.Fatalf("unexpected set clause: %q", clause)
	}
	if len(args) != 2 || args[0] != "new title" || args[1] != int64(1800000000) {
		t.Fatalf("unexpected args: %#v", args)
	}
}
