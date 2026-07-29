package contracttest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/mobilev2command"
	"github.com/hujinrun/flowspace/internal/mobilev2contract"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
)

type MobileV2CommandLedgerFixture struct {
	DB      *sql.DB
	Dialect mobilev2command.SQLDialect
}

func RunMobileV2CommandLedgerSuite(t *testing.T, fixture MobileV2CommandLedgerFixture) {
	t.Helper()
	ctx := context.Background()
	ledger := mobilev2command.NewSQLLedger(fixture.DB, fixture.Dialect)
	const workspaceID = "mobile-v2-command-w1"

	mustExec(t, fixture.DB, `INSERT INTO tenant_workspaces(workspace_id) VALUES ('mobile-v2-command-w1')`)
	mustExec(t, fixture.DB, `CREATE TABLE mobile_v2_command_test_effects (
		workspace_id TEXT NOT NULL,
		command_id TEXT NOT NULL,
		value TEXT NOT NULL,
		PRIMARY KEY (workspace_id, command_id)
	)`)

	t.Run("terminal_receipt_effect_and_change_commit_atomically", func(t *testing.T) {
		command := mobileV2LedgerCommand(t, workspaceID, "command-applied")
		completedAt := time.Date(2026, 7, 23, 10, 30, 0, 0, time.UTC)
		response, err := ledger.Commit(ctx, mobilev2command.TerminalCommit{
			Command: command,
			Result: mobilev2command.DomainResult{
				Status: mobilev2command.StatusApplied,
				IdentityMappings: []mobilev2command.IdentityMapping{{
					EntityType: "task_occurrence", ClientID: mobileV2StringRef("occ-client"), EntityID: mobileV2StringRef("occ-server"),
				}},
				AffectedRevisions: []mobilev2command.AffectedRevision{{EntityType: "task_occurrence", EntityID: "occ-server", Revision: "7"}},
				AfterImages:       [][]byte{[]byte(`{"entity_type":"task_occurrence","entity_id":"occ-server"}`)},
			},
			ScopeChanges: []mobilev2command.ScopeChange{{
				Scope: mobilev2sync.ScopeIPhoneOccurrenceWindow,
				AfterImages: [][]byte{
					[]byte(`{"entity_type":"task_occurrence","entity_id":"occ-server"}`),
				},
			}},
			CompletedAt: completedAt,
		}, func(ctx context.Context, tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, mobileV2Bind(fixture.Dialect, `INSERT INTO mobile_v2_command_test_effects(workspace_id,command_id,value) VALUES (?,?,?)`),
				workspaceID, command.CommandID, "applied")
			return err
		})
		if err != nil || response.Receipt == nil || response.Replayed || response.Receipt.CommitSequence != 1 ||
			!response.Receipt.CompletedAt.Equal(completedAt) {
			t.Fatalf("commit response=%#v err=%v", response, err)
		}

		stored, found, err := ledger.Lookup(ctx, workspaceID, command.OriginDeviceClientID, command.CommandID)
		if err != nil || !found || stored.RequestDigest != command.RequestDigest || stored.Status != mobilev2command.StatusApplied {
			t.Fatalf("lookup receipt=%#v found=%v err=%v", stored, found, err)
		}
		changes, err := ledger.ChangesAfter(ctx, workspaceID, 0)
		if err != nil || len(changes) != 1 || changes[0].CausedByCommandID != command.CommandID ||
			changes[0].OriginDeviceClientID != command.OriginDeviceClientID || len(changes[0].AfterImages) != 1 {
			t.Fatalf("changes=%#v err=%v", changes, err)
		}
		assertMobileV2EffectCount(t, fixture.DB, workspaceID, command.CommandID, 1)
		var scopeEntityType string
		scopeEntityQuery := `SELECT json_extract(entities_json,'$[0].entity_type')
			FROM mobile_v2_scope_change_batches WHERE workspace_id=? AND scope=? AND sequence=?`
		if fixture.Dialect == mobilev2command.SQLDialectPostgres {
			scopeEntityQuery = `SELECT entities_json->0->>'entity_type'
				FROM mobile_v2_scope_change_batches WHERE workspace_id=? AND scope=? AND sequence=?`
		}
		if err := fixture.DB.QueryRow(mobileV2Bind(fixture.Dialect, scopeEntityQuery),
			workspaceID, string(mobilev2sync.ScopeIPhoneOccurrenceWindow), 1,
		).Scan(&scopeEntityType); err != nil {
			t.Fatal(err)
		}
		if scopeEntityType != "task_occurrence" {
			t.Fatalf("scope change entity type = %q", scopeEntityType)
		}
	})

	t.Run("same_idempotency_key_replays_without_running_effect", func(t *testing.T) {
		command := mobileV2LedgerCommand(t, workspaceID, "command-applied")
		called := false
		response, err := ledger.Commit(ctx, mobilev2command.TerminalCommit{
			Command: command,
			Result:  mobilev2command.DomainResult{Status: mobilev2command.StatusRejected},
		}, func(context.Context, *sql.Tx) error {
			called = true
			return nil
		})
		if err != nil || response.Receipt == nil || !response.Replayed || response.Receipt.Status != mobilev2command.StatusApplied || called {
			t.Fatalf("replay response=%#v called=%v err=%v", response, called, err)
		}
		assertMobileV2EffectCount(t, fixture.DB, workspaceID, command.CommandID, 1)
	})

	t.Run("concurrent_same_key_runs_business_effect_once", func(t *testing.T) {
		command := mobileV2LedgerCommand(t, workspaceID, "command-concurrent")
		start := make(chan struct{})
		results := make(chan struct {
			response mobilev2command.Response
			err      error
		}, 2)
		var applyCount atomic.Int32
		for range 2 {
			go func() {
				<-start
				response, err := ledger.Commit(ctx, mobilev2command.TerminalCommit{
					Command: command, Result: mobilev2command.DomainResult{Status: mobilev2command.StatusApplied},
				}, func(ctx context.Context, tx *sql.Tx) error {
					applyCount.Add(1)
					_, err := tx.ExecContext(ctx, mobileV2Bind(fixture.Dialect, `INSERT INTO mobile_v2_command_test_effects(workspace_id,command_id,value) VALUES (?,?,?)`),
						workspaceID, command.CommandID, "concurrent")
					return err
				})
				results <- struct {
					response mobilev2command.Response
					err      error
				}{response: response, err: err}
			}()
		}
		close(start)
		replayed := 0
		for range 2 {
			result := <-results
			if result.err != nil || result.response.Receipt == nil {
				t.Fatalf("concurrent response=%#v err=%v", result.response, result.err)
			}
			if result.response.Replayed {
				replayed++
			}
		}
		if replayed != 1 || applyCount.Load() != 1 {
			t.Fatalf("replayed/apply count=%d/%d", replayed, applyCount.Load())
		}
		assertMobileV2EffectCount(t, fixture.DB, workspaceID, command.CommandID, 1)
	})

	t.Run("same_key_with_different_digest_is_rejected", func(t *testing.T) {
		command := mobileV2LedgerCommand(t, workspaceID, "command-applied")
		command.RawEnvelope = mobileV2LedgerEnvelope(workspaceID, command.CommandID, "9")
		command.RequestDigest = mobileV2MustDigest(t, command.RawEnvelope)
		if _, err := ledger.Commit(ctx, mobilev2command.TerminalCommit{
			Command: command, Result: mobilev2command.DomainResult{Status: mobilev2command.StatusApplied},
		}, nil); !errors.Is(err, mobilev2command.ErrRequestDigestMismatch) {
			t.Fatalf("digest mismatch error=%v", err)
		}
	})

	t.Run("all_terminal_statuses_survive_change_compaction", func(t *testing.T) {
		for index, status := range []mobilev2command.ResultStatus{
			mobilev2command.StatusNoOp,
			mobilev2command.StatusConflict,
			mobilev2command.StatusRejected,
		} {
			command := mobileV2LedgerCommand(t, workspaceID, fmt.Sprintf("command-terminal-%d", index))
			response, err := ledger.Commit(ctx, mobilev2command.TerminalCommit{
				Command: command, Result: mobilev2command.DomainResult{Status: status},
			}, nil)
			if err != nil || response.Receipt == nil || response.Receipt.Status != status {
				t.Fatalf("terminal %s response=%#v err=%v", status, response, err)
			}
		}
		if err := ledger.CompactChanges(ctx, workspaceID, 99); err != nil {
			t.Fatal(err)
		}
		changes, err := ledger.ChangesAfter(ctx, workspaceID, 0)
		if err != nil || len(changes) != 0 {
			t.Fatalf("compacted changes=%#v err=%v", changes, err)
		}
		for index, status := range []mobilev2command.ResultStatus{
			mobilev2command.StatusNoOp,
			mobilev2command.StatusConflict,
			mobilev2command.StatusRejected,
		} {
			commandID := fmt.Sprintf("command-terminal-%d", index)
			receipt, found, err := ledger.Lookup(ctx, workspaceID, "watch-device", commandID)
			if err != nil || !found || receipt.Status != status {
				t.Fatalf("terminal %s receipt=%#v found=%v err=%v", status, receipt, found, err)
			}
		}
		expectStatementRejected(t, fixture.DB, mobileV2Bind(fixture.Dialect,
			`UPDATE mobile_v2_command_receipts SET status=? WHERE workspace_id=? AND origin_device_client_id=? AND command_id=?`),
			string(mobilev2command.StatusRejected), workspaceID, "watch-device", "command-terminal-0")
		expectStatementRejected(t, fixture.DB, mobileV2Bind(fixture.Dialect,
			`DELETE FROM mobile_v2_command_receipts WHERE workspace_id=? AND origin_device_client_id=? AND command_id=?`),
			workspaceID, "watch-device", "command-terminal-0")
	})

	t.Run("retry_later_never_creates_a_terminal_receipt", func(t *testing.T) {
		command := mobileV2LedgerCommand(t, workspaceID, "command-retry")
		response, err := ledger.Commit(ctx, mobilev2command.TerminalCommit{
			Command: command, Result: mobilev2command.DomainResult{Status: mobilev2command.StatusRetryLater},
		}, nil)
		if err != nil || !response.RetryLater || response.Receipt != nil {
			t.Fatalf("retry response=%#v err=%v", response, err)
		}
		if _, found, err := ledger.Lookup(ctx, workspaceID, command.OriginDeviceClientID, command.CommandID); err != nil || found {
			t.Fatalf("retry lookup found=%v err=%v", found, err)
		}
	})

	t.Run("business_failure_rolls_back_effect_receipt_and_change", func(t *testing.T) {
		command := mobileV2LedgerCommand(t, workspaceID, "command-rollback")
		rollbackErr := errors.New("force rollback")
		_, err := ledger.Commit(ctx, mobilev2command.TerminalCommit{
			Command: command, Result: mobilev2command.DomainResult{Status: mobilev2command.StatusApplied},
		}, func(ctx context.Context, tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, mobileV2Bind(fixture.Dialect, `INSERT INTO mobile_v2_command_test_effects(workspace_id,command_id,value) VALUES (?,?,?)`),
				workspaceID, command.CommandID, "must rollback"); err != nil {
				return err
			}
			return rollbackErr
		})
		if !errors.Is(err, rollbackErr) {
			t.Fatalf("rollback error=%v", err)
		}
		assertMobileV2EffectCount(t, fixture.DB, workspaceID, command.CommandID, 0)
		if _, found, lookupErr := ledger.Lookup(ctx, workspaceID, command.OriginDeviceClientID, command.CommandID); lookupErr != nil || found {
			t.Fatalf("rolled back receipt found=%v err=%v", found, lookupErr)
		}
	})

	t.Run("ambiguous_history_is_durable_and_distinguishable", func(t *testing.T) {
		if err := ledger.MarkReceiptHistoryAmbiguous(ctx, workspaceID); err != nil {
			t.Fatal(err)
		}
		complete, err := ledger.ReceiptHistoryComplete(ctx, workspaceID)
		if err != nil || complete {
			t.Fatalf("history complete=%v err=%v", complete, err)
		}
		command := mobileV2LedgerCommand(t, workspaceID, "command-after-history-loss")
		called := false
		if _, err := ledger.Commit(ctx, mobilev2command.TerminalCommit{
			Command: command, Result: mobilev2command.DomainResult{Status: mobilev2command.StatusApplied},
		}, func(context.Context, *sql.Tx) error {
			called = true
			return nil
		}); !errors.Is(err, mobilev2command.ErrReceiptHistoryAmbiguous) || called {
			t.Fatalf("ambiguous commit error=%v callback=%v", err, called)
		}
	})
}

func mobileV2LedgerCommand(t *testing.T, workspaceID, commandID string) mobilev2command.Command {
	t.Helper()
	raw := mobileV2LedgerEnvelope(workspaceID, commandID, "8")
	return mobilev2command.Command{
		WorkspaceID: workspaceID, OriginDeviceClientID: "watch-device", CommandID: commandID,
		RequestDigest: mobileV2MustDigest(t, raw), CommandType: "occurrence.complete", CreatedRuntimeEpoch: "8",
		ExpectedRevisionNames: []string{"task", "schedule", "occurrence"}, RawEnvelope: raw,
	}
}

func mobileV2LedgerEnvelope(workspaceID, commandID, epoch string) []byte {
	return []byte(`{"command_id":"` + commandID + `","request_digest":"sha256:ignored","origin_device_client_id":"watch-device","workspace_id":"` + workspaceID + `","command_type":"occurrence.complete","target":{"entity_id":"occ-1","client_id":null},"created_runtime_epoch":"` + epoch + `","expected":{"task_revision":{"source":"exact","value":"6"},"schedule_revision":{"source":"exact","value":"2"},"occurrence_revision":{"source":"exact","value":"7"}},"depends_on_command_id":null,"supersedes_command_id":null,"payload":{}}`)
}

func mobileV2MustDigest(t *testing.T, raw []byte) string {
	t.Helper()
	digest, err := mobilev2contract.RequestDigest(raw)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}

func mobileV2Bind(dialect mobilev2command.SQLDialect, query string) string {
	if dialect != mobilev2command.SQLDialectPostgres {
		return query
	}
	result := ""
	parameter := 1
	for _, char := range query {
		if char == '?' {
			result += fmt.Sprintf("$%d", parameter)
			parameter++
		} else {
			result += string(char)
		}
	}
	return result
}

func assertMobileV2EffectCount(t *testing.T, db *sql.DB, workspaceID, commandID string, want int) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM mobile_v2_command_test_effects WHERE workspace_id='` + workspaceID + `' AND command_id='` + commandID + `'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("effect count=%d, want %d", count, want)
	}
}

func mobileV2StringRef(value string) *string { return &value }
