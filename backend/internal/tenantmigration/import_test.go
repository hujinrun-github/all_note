package tenantmigration

import (
	"context"
	"errors"
	"testing"
)

type memoryImportTarget struct {
	state, migrationID string
	activeBinding      bool
	failTable          string
	deleted, committed bool
	rolledBack         bool
	inserted           map[string]int
}

func (t *memoryImportTarget) BeginImport(context.Context) (ImportTransaction, error) {
	copy := *t
	copy.inserted = map[string]int{}
	return &memoryImportTx{owner: t, value: &copy}, nil
}

type memoryImportTx struct{ owner, value *memoryImportTarget }

func (t *memoryImportTx) WorkspaceState(context.Context, string) (string, string, error) {
	return t.value.state, t.value.migrationID, nil
}
func (t *memoryImportTx) HasActiveBinding(context.Context, string) (bool, error) {
	return t.value.activeBinding, nil
}
func (t *memoryImportTx) DeleteWorkspace(context.Context, string) error {
	t.value.deleted = true
	return nil
}
func (t *memoryImportTx) PrepareFenced(context.Context, string, string) error { return nil }
func (t *memoryImportTx) InsertRows(_ context.Context, table LogicalTable, rows []LogicalRow) error {
	if table.Name == t.value.failTable {
		return errors.New("injected insert failure")
	}
	t.value.inserted[table.Name] += len(rows)
	return nil
}
func (t *memoryImportTx) Commit() error   { *t.owner = *t.value; t.owner.committed = true; return nil }
func (t *memoryImportTx) Rollback() error { t.owner.rolledBack = true; return nil }

func TestImportRollsBackWholeWorkspaceOnFailure(t *testing.T) {
	pack, _ := Export(context.Background(), baselineSnapshot(false))
	target := &memoryImportTarget{state: "fenced", migrationID: "m1", failTable: "notes"}
	err := Import(context.Background(), target, pack, ImportOptions{MigrationID: "m1"})
	if err == nil || !target.rolledBack || target.committed {
		t.Fatalf("error=%v rollback=%v committed=%v", err, target.rolledBack, target.committed)
	}
}

func TestReplaceRetiredRequiresExplicitPolicyAndNoActiveBinding(t *testing.T) {
	pack, _ := Export(context.Background(), baselineSnapshot(false))
	for _, test := range []struct {
		name    string
		options ImportOptions
		active  bool
		wantErr bool
	}{
		{"default rejects", ImportOptions{MigrationID: "m2"}, false, true},
		{"active binding rejects", ImportOptions{MigrationID: "m2", ReplaceRetired: true}, true, true},
		{"explicit replacement", ImportOptions{MigrationID: "m2", ReplaceRetired: true}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			target := &memoryImportTarget{state: "retired", activeBinding: test.active}
			err := Import(context.Background(), target, pack, test.options)
			if (err != nil) != test.wantErr {
				t.Fatalf("Import() error=%v", err)
			}
			if err == nil && (!target.deleted || !target.committed) {
				t.Fatalf("target=%+v", target)
			}
		})
	}
}

func TestImportRejectsActiveAndAllowsSameMigrationResume(t *testing.T) {
	pack, _ := Export(context.Background(), baselineSnapshot(false))
	if err := Import(context.Background(), &memoryImportTarget{state: "active"}, pack, ImportOptions{MigrationID: "m1"}); !errors.Is(err, ErrTargetWorkspaceInUse) {
		t.Fatalf("active target error=%v", err)
	}
	if err := Import(context.Background(), &memoryImportTarget{state: "fenced", migrationID: "other"}, pack, ImportOptions{MigrationID: "m1"}); !errors.Is(err, ErrTargetWorkspaceInUse) {
		t.Fatalf("other migration error=%v", err)
	}
	if err := Import(context.Background(), &memoryImportTarget{state: "fenced", migrationID: "m1"}, pack, ImportOptions{MigrationID: "m1"}); err != nil {
		t.Fatalf("resume: %v", err)
	}
}
