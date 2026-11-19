package japaneseintegration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/credentials"
	"github.com/hujinrun/flowspace/internal/storage"
	storagepostgres "github.com/hujinrun/flowspace/internal/storage/postgres"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/taskruntime"
)

type fixtureRuntime struct {
	snapshot RuntimeSnapshot
}

func (r fixtureRuntime) ResolveMobileRuntime(_ context.Context, workspace string) (RuntimeSnapshot, error) {
	if workspace != "w1" {
		return RuntimeSnapshot{}, ErrUnavailable
	}
	return r.snapshot, nil
}

type integrationFixture struct {
	s               *Service
	control, tenant *sql.DB
	connection      Connection
	pair            PairResult
	now             time.Time
	driver          storage.Driver
	runtime         fixtureRuntime
}

func fixture(t *testing.T, driver storage.Driver) *integrationFixture {
	t.Helper()
	ctx := t.Context()
	f := &integrationFixture{driver: driver, now: time.Date(2026, 11, 19, 11, 0, 0, 0, time.UTC)}
	var controlCfg, tenantCfg storage.Config
	var cp storage.ControlProvider
	var tp interface {
		storage.TenantProvider
		storage.TenantMaintenanceProvider
	}
	if driver == storage.DriverSQLite {
		controlCfg = storage.Config{Driver: driver, Env: "test", SQLitePath: filepath.Join(t.TempDir(), "control.test.db")}
		tenantCfg = storage.Config{Driver: driver, Env: "test", SQLitePath: filepath.Join(t.TempDir(), "tenant.test.db")}
		cp = storagesqlite.Provider{}
		tp = storagesqlite.Provider{}
	} else {
		base := strings.TrimSpace(os.Getenv("FLOWSPACE_TEST_DATABASE_URL"))
		if base == "" {
			if os.Getenv("FLOWSPACE_REQUIRE_POSTGRES_TESTS") == "true" {
				t.Fatal("PostgreSQL test URL required")
			}
			t.Skip("PostgreSQL test URL not configured")
		}
		admin, e := sql.Open("pgx", base)
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { admin.Close() })
		makeURL := func(suffix string) string {
			schema := fmt.Sprintf("fs_test_japanese_%d_%s", time.Now().UnixNano(), suffix)
			if _, e := admin.ExecContext(ctx, `CREATE SCHEMA "`+schema+`"`); e != nil {
				t.Fatal(e)
			}
			t.Cleanup(func() { _, _ = admin.Exec(`DROP SCHEMA IF EXISTS "` + schema + `" CASCADE`) })
			u, e := url.Parse(base)
			if e != nil {
				t.Fatal(e)
			}
			q := u.Query()
			q.Set("options", "-c search_path="+schema+",public")
			u.RawQuery = q.Encode()
			return u.String()
		}
		controlCfg = storage.Config{Driver: driver, Env: "test", URL: makeURL("control")}
		tenantCfg = storage.Config{Driver: driver, Env: "test", URL: makeURL("tenant")}
		cp = storagepostgres.Provider{}
		tp = storagepostgres.Provider{}
	}
	if e := cp.MigrateControl(ctx, controlCfg); e != nil {
		t.Fatal(e)
	}
	if e := tp.MigrateTenant(ctx, tenantCfg); e != nil {
		t.Fatal(e)
	}
	controlStore, e := cp.OpenControl(ctx, controlCfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { controlStore.Close() })
	f.control = controlStore.(storage.SQLStore).SQLDB()
	tenantStore, e := tp.OpenTenant(ctx, tenantCfg, taskruntime.ExpectedTenantSchemaVersion)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { tenantStore.Close() })
	f.tenant = tenantStore.(storage.SQLStore).SQLDB()
	for _, q := range []string{`INSERT INTO users(id,email,password_hash) VALUES('u1','integration@example.test','unused')`, `INSERT INTO workspaces(id,name,owner_user_id) VALUES('w1','Test','u1')`, `INSERT INTO workspace_members(workspace_id,user_id,role) VALUES('w1','u1','owner')`} {
		if _, e = f.control.Exec(q); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = f.tenant.Exec(`INSERT INTO tenant_workspaces(workspace_id) VALUES('w1')`); e != nil {
		t.Fatal(e)
	}
	var writer storage.TenantFencedWriter
	if driver == storage.DriverSQLite {
		writer = storagesqlite.NewTenantWriter(tenantCfg)
	} else {
		writer = (storagepostgres.Provider{}).NewTenantWriter(tenantCfg)
	}
	reader := tenantStore.(storage.TaskDomainRuntimeStore).TaskDomainReader("w1")
	f.runtime = fixtureRuntime{RuntimeSnapshot{WorkspaceID: "w1", Epoch: 1, Driver: driver, DB: f.tenant, Writer: writer, Application: taskapp.RuntimeSnapshot{WorkspaceID: "w1", Epoch: 1, Reader: reader}}}
	keyring, e := credentials.NewKeyring("test", map[string][]byte{"test": bytes.Repeat([]byte{7}, 32)})
	if e != nil {
		t.Fatal(e)
	}
	f.s, e = New(Config{ControlDB: f.control, ControlDriver: driver, Keyring: keyring, Runtimes: f.runtime, Authorize: func(context.Context, string, string) error { return nil }, Now: func() time.Time { return f.now }})
	if e != nil {
		t.Fatal(e)
	}
	code, e := f.s.CreatePairing(ctx, "w1", "u1")
	if e != nil {
		t.Fatal(e)
	}
	f.pair, e = f.s.Pair(ctx, PairRequest{PairingCode: code.PairingCode, SourceInstanceID: "japanese-test", SourceUserID: "123", SourceUserName: "Learner"})
	if e != nil {
		t.Fatal(e)
	}
	f.connection, e = f.s.loadConnection(ctx, f.pair.ConnectionID)
	if e != nil {
		t.Fatal(e)
	}
	if e = f.s.PutCatalog(ctx, f.connection, f.catalog()); e != nil {
		t.Fatal(e)
	}
	if e = writer.BeginFencedWrite(ctx, "w1", 1, func(tx storage.TenantWriteTx) error {
		if e := tx.TaskDomainWriter().EnsureSystemProjects(ctx); e != nil {
			return e
		}
		for _, id := range []string{"target", "other"} {
			snapshot, _, e := (taskdomain.TaskFactory{}).Build(taskdomain.TaskCreationInput{WorkspaceID: "w1", Project: taskdomain.ProjectIdentity{WorkspaceID: "w1", ProjectID: taskdomain.SystemInboxProjectID}, TaskID: id, ActorID: "u1", ActorTime: f.now, Title: id, Schedule: taskdomain.ScheduleInput{RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: "2026-11-19", EndsOn: "2026-12-19", Rule: json.RawMessage(`{"interval":1}`)}})
			if e != nil {
				return e
			}
			snapshot.Task.LifecycleStatus = taskdomain.TaskLifecycleActive
			if e = tx.TaskDomainWriter().CreateTaskAggregate(ctx, snapshot); e != nil {
				return e
			}
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	return f
}
func (f *integrationFixture) catalog() Catalog {
	return Catalog{SchemaVersion: 1, SourceInstanceID: "japanese-test", SourceUserID: "123", CatalogRevision: 1, Plan: &Plan{PlanID: "plan-1", Revision: 1, Title: "Study", Timezone: "Asia/Shanghai", StartDate: "2026-11-19", EndDate: "2026-12-19", Weekdays: []int{0, 1, 2, 3, 4, 5, 6}, TimeSlots: []Slot{{ID: "evening", Title: "Evening", Time: "19:00", DurationMinutes: 20, Tasks: []StudyTask{{ID: "listening", Title: "Listening", Path: "/listening", Minutes: 20}}}}}}
}
func (f *integrationFixture) event(id string, rev int64, completed bool) Event {
	return Event{SchemaVersion: 1, EventID: id, Type: "study_task.state_changed.v1", SourceInstanceID: "japanese-test", SourceUserID: "123", PlanID: "plan-1", PlanRevision: 1, CatalogRevision: 1, SlotID: "evening", TaskID: "listening", StudyDate: "2026-11-19", Timezone: "Asia/Shanghai", StateRevision: rev, Completed: completed, OccurredAt: f.now.UTC().Format(time.RFC3339), Reason: "manual"}
}
func (f *integrationFixture) binding(t *testing.T, target string) Binding {
	t.Helper()
	b, e := f.s.SaveBinding(t.Context(), "w1", "u1", BindingInput{ConnectionID: f.connection.ID, PlanID: "plan-1", SlotID: "evening", TaskID: "listening", TargetTaskID: target})
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func (f *integrationFixture) send(t *testing.T, event Event) Receipt {
	t.Helper()
	if _, e := f.s.AcceptEvent(t.Context(), f.connection, event); e != nil {
		t.Fatal(e)
	}
	if e := f.s.ProcessPending(t.Context()); e != nil {
		t.Fatal(e)
	}
	r, e := f.s.Event(t.Context(), f.connection, event.EventID)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func (f *integrationFixture) state(t *testing.T, date, target string) taskdomain.ExecutionStatus {
	t.Helper()
	o, e := f.runtime.snapshot.Application.Reader.GetOccurrence(t.Context(), taskdomain.DeterministicOccurrenceID("w1", target, date))
	if e != nil {
		t.Fatal(e)
	}
	return o.Status
}
func (f *integrationFixture) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if e := f.tenant.QueryRow(query).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func TestIntegrationCompletionAndRecovery(t *testing.T) {
	for _, driver := range []storage.Driver{storage.DriverSQLite, storage.DriverPostgres} {
		t.Run(string(driver), func(t *testing.T) {
			f := fixture(t, driver)
			f.binding(t, "target")
			status, e := f.s.Status(t.Context(), "w1")
			if e != nil || len(status.Targets) != 2 {
				t.Fatalf("status=%+v err=%v", status, e)
			}
			event := f.event("done-1", 1, true)
			if _, e = f.s.AcceptEvent(t.Context(), f.connection, event); e != nil {
				t.Fatal(e)
			}
			// Rebuild the service after durable receipt to simulate a process restart.
			restarted, e := New(f.s.cfg)
			if e != nil {
				t.Fatal(e)
			}
			f.s = restarted
			if e = f.s.ProcessPending(t.Context()); e != nil {
				t.Fatal(e)
			}
			r, e := f.s.Event(t.Context(), f.connection, event.EventID)
			if e != nil || r.Status != "applied" {
				t.Fatalf("receipt=%+v err=%v", r, e)
			}
			if f.state(t, "2026-11-19", "target") != taskdomain.ExecutionStatusDone || f.state(t, "2026-11-20", "target") != taskdomain.ExecutionStatusOpen {
				t.Fatal("wrong occurrence completed")
			}
			logs := f.count(t, `SELECT COUNT(*) FROM domain_task_execution_logs_v2`)
			seq := f.count(t, `SELECT COUNT(*) FROM mobile_v2_scope_change_batches`)
			repeated := f.send(t, event)
			if repeated.Status != "applied" {
				t.Fatalf("replay=%+v", repeated)
			}
			duplicate := event
			duplicate.EventID = "duplicate-id"
			r = f.send(t, duplicate)
			if r.Status != "stale" {
				t.Fatalf("business duplicate=%+v", r)
			}
			if logs != f.count(t, `SELECT COUNT(*) FROM domain_task_execution_logs_v2`) || seq != f.count(t, `SELECT COUNT(*) FROM mobile_v2_scope_change_batches`) {
				t.Fatal("duplicate produced effects")
			}
			altered := event
			altered.Completed = false
			if _, e = f.s.AcceptEvent(t.Context(), f.connection, altered); !errors.Is(e, ErrConflict) {
				t.Fatalf("event id misuse=%v", e)
			}
			var changed string
			e = f.tenant.QueryRow(`SELECT entities_json FROM mobile_v2_scope_change_batches WHERE scope='iphone-occurrence-window' ORDER BY sequence DESC LIMIT 1`).Scan(&changed)
			if e != nil || !strings.Contains(strings.ReplaceAll(changed, " ", ""), `"execution_status":"done"`) {
				t.Fatalf("mobile=%s err=%v", changed, e)
			}
			r = f.send(t, f.event("undo", 2, false))
			if r.ErrorCode != "source_reopened" || f.state(t, "2026-11-19", "target") != taskdomain.ExecutionStatusDone {
				t.Fatal("undo policy broken")
			}
		})
	}
}
func TestIntegrationOutOfOrderAndFrozenBinding(t *testing.T) {
	for _, name := range []string{"newer undo wins", "frozen binding", "unbound needs explicit routing", "missing occurrence materialized", "completion requirements", "catalog lifecycle", "auth replay"} {
		t.Run(name, func(t *testing.T) {
			f := fixture(t, storage.DriverSQLite)
			switch name {
			case "newer undo wins":
				f.binding(t, "target")
				if _, e := f.s.AcceptEvent(t.Context(), f.connection, f.event("old", 1, true)); e != nil {
					t.Fatal(e)
				}
				r := f.send(t, f.event("new", 2, false))
				if r.Status != "applied" || f.state(t, "2026-11-19", "target") != taskdomain.ExecutionStatusOpen {
					t.Fatal("old completion applied")
				}
			case "frozen binding":
				old := f.binding(t, "target")
				if _, e := f.s.AcceptEvent(t.Context(), f.connection, f.event("queued", 1, true)); e != nil {
					t.Fatal(e)
				}
				next := f.binding(t, "other")
				if next.Version != 2 || next.EffectiveFrom != "2026-11-20" {
					t.Fatalf("next=%+v", next)
				}
				if e := f.s.ProcessPending(t.Context()); e != nil {
					t.Fatal(e)
				}
				r, _ := f.s.Event(t.Context(), f.connection, "queued")
				if r.BindingVersion != old.Version || r.TargetTaskID != "target" || f.state(t, "2026-11-19", "other") != taskdomain.ExecutionStatusOpen {
					t.Fatalf("rerouted=%+v", r)
				}
			case "unbound needs explicit routing":
				r := f.send(t, f.event("unbound", 1, true))
				if r.ErrorCode != "unbound" {
					t.Fatal(r)
				}
				f.binding(t, "target")
				if e := f.s.Retry(t.Context(), "w1", "unbound"); !errors.Is(e, ErrConflict) {
					t.Fatalf("unbound rerouted: %v", e)
				}
			case "missing occurrence materialized":
				f.binding(t, "target")
				if _, e := f.tenant.Exec(`DELETE FROM domain_task_occurrences_v2 WHERE task_id='target' AND occurrence_key='2026-11-19'`); e != nil {
					t.Fatal(e)
				}
				r := f.send(t, f.event("generate", 1, true))
				if r.Status != "applied" {
					t.Fatalf("materialize=%+v", r)
				}
			case "completion requirements":
				f.binding(t, "target")
				if _, e := f.tenant.Exec(`UPDATE domain_tasks_v2 SET completion_requirements='[{"id":"check","kind":"check","title":"check","completed":false}]' WHERE id='target'`); e != nil {
					t.Fatal(e)
				}
				r := f.send(t, f.event("requirement", 1, true))
				if r.ErrorCode != "completion_requirements" {
					t.Fatal(r)
				}
				if _, e := f.tenant.Exec(`UPDATE domain_tasks_v2 SET completion_requirements='[]' WHERE id='target'`); e != nil {
					t.Fatal(e)
				}
				if e := f.s.Retry(t.Context(), "w1", "requirement"); e != nil {
					t.Fatal(e)
				}
				if e := f.s.ProcessPending(t.Context()); e != nil {
					t.Fatal(e)
				}
				r, _ = f.s.Event(t.Context(), f.connection, "requirement")
				if r.Status != "applied" {
					t.Fatal(r)
				}
			case "catalog lifecycle":
				f.binding(t, "target")
				deleted := f.catalog()
				deleted.CatalogRevision = 2
				deleted.Plan = nil
				if e := f.s.PutCatalog(t.Context(), f.connection, deleted); e != nil {
					t.Fatal(e)
				}
				if e := f.s.PutCatalog(t.Context(), f.connection, f.catalog()); e != nil {
					t.Fatal(e)
				}
				r := f.send(t, f.event("old-plan", 1, true))
				if r.Status != "needs_attention" || (r.ErrorCode != "source_plan_changed" && r.ErrorCode != "unbound") {
					t.Fatal(r)
				}
			case "auth replay":
				event := f.event("signed", 1, true)
				raw, _ := json.Marshal(event)
				headers := http.Header{}
				Sign(headers, "POST", "/api/integrations/japanese/events", f.pair.ConnectionID, f.pair.KeyID, f.pair.Secret, raw, f.now, strings.Repeat("a", 32))
				if _, e := f.s.Authenticate(t.Context(), "POST", "/api/integrations/japanese/events", headers, raw); e != nil {
					t.Fatal(e)
				}
				if _, e := f.s.Authenticate(t.Context(), "POST", "/api/integrations/japanese/events", headers, raw); !errors.Is(e, ErrUnauthorized) {
					t.Fatalf("nonce replay=%v", e)
				}
				if e := f.s.Revoke(t.Context(), "w1", "u1", f.connection.ID); e != nil {
					t.Fatal(e)
				}
				Sign(headers, "POST", "/api/integrations/japanese/events", f.pair.ConnectionID, f.pair.KeyID, f.pair.Secret, raw, f.now, strings.Repeat("b", 32))
				if _, e := f.s.Authenticate(t.Context(), "POST", "/api/integrations/japanese/events", headers, raw); !errors.Is(e, ErrUnauthorized) {
					t.Fatal("revoked authenticated")
				}
			}
		})
	}
}

func TestPairingAuthorizerUsesSingleControlConnection(t *testing.T) {
	f := fixture(t, storage.DriverSQLite)
	f.s.cfg.Authorize = func(ctx context.Context, workspace, actor string) error {
		var id string
		return f.control.QueryRowContext(ctx, `SELECT id FROM users WHERE id=?`, actor).Scan(&id)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	code, e := f.s.CreatePairing(ctx, "w1", "u1")
	if e != nil {
		t.Fatal(e)
	}
	paired, e := f.s.Pair(ctx, PairRequest{PairingCode: code.PairingCode, SourceInstanceID: "single-connection", SourceUserID: "456"})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = f.s.Pair(ctx, PairRequest{PairingCode: code.PairingCode, SourceInstanceID: "single-connection", SourceUserID: "456"}); !errors.Is(e, ErrUnauthorized) {
		t.Fatalf("consumed pairing code reused: %v", e)
	}
	if _, e = f.s.Pair(ctx, PairRequest{PairingCode: code.PairingCode, SourceInstanceID: "attacker", SourceUserID: "456"}); !errors.Is(e, ErrUnauthorized) {
		t.Fatal("pair code reassigned")
	}
	var stored string
	if e = f.control.QueryRow(`SELECT encrypted_secret FROM japanese_integration_connections WHERE id=?`, paired.ConnectionID).Scan(&stored); e != nil {
		t.Fatal(e)
	}
	if strings.Contains(stored, paired.Secret) {
		t.Fatal("plaintext secret persisted")
	}
}
func TestIntegrationRejectsConflictingBusinessVersion(t *testing.T) {
	f := fixture(t, storage.DriverSQLite)
	f.binding(t, "target")
	event := f.event("one", 1, true)
	if _, e := f.s.AcceptEvent(t.Context(), f.connection, event); e != nil {
		t.Fatal(e)
	}
	event.EventID = "two"
	event.Completed = false
	if _, e := f.s.AcceptEvent(t.Context(), f.connection, event); !errors.Is(e, ErrConflict) {
		t.Fatalf("conflicting business version=%v", e)
	}
}
func TestIntegrationCompletionReceiptAndMobileBatchRollback(t *testing.T) {
	for _, driver := range []storage.Driver{storage.DriverSQLite, storage.DriverPostgres} {
		t.Run(string(driver), func(t *testing.T) {
			f := fixture(t, driver)
			f.binding(t, "target")
			event := f.event("rollback", 1, true)
			if _, e := f.s.AcceptEvent(t.Context(), f.connection, event); e != nil {
				t.Fatal(e)
			}
			seq := f.count(t, `SELECT COUNT(*) FROM mobile_v2_scope_change_batches`)
			var trigger string
			if driver == storage.DriverSQLite {
				trigger = `CREATE TRIGGER abort_receipt BEFORE UPDATE OF status ON japanese_integration_inbox WHEN NEW.status='applied' BEGIN SELECT RAISE(ABORT,'test receipt failure'); END;`
			} else {
				trigger = `CREATE FUNCTION abort_receipt_fn() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.status='applied' THEN RAISE EXCEPTION 'test receipt failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER abort_receipt BEFORE UPDATE ON japanese_integration_inbox FOR EACH ROW EXECUTE FUNCTION abort_receipt_fn();`
			}
			if _, e := f.tenant.Exec(trigger); e != nil {
				t.Fatal(e)
			}
			if e := f.s.ProcessPending(t.Context()); e == nil {
				t.Fatal("receipt failure not propagated")
			}
			if f.state(t, "2026-11-19", "target") != taskdomain.ExecutionStatusOpen || seq != f.count(t, `SELECT COUNT(*) FROM mobile_v2_scope_change_batches`) || f.count(t, `SELECT COUNT(*) FROM domain_task_execution_logs_v2`) != 0 || f.count(t, `SELECT COUNT(*) FROM japanese_integration_source_states`) != 0 {
				t.Fatal("transaction leaked partial effect")
			}
			drop := `DROP TRIGGER abort_receipt`
			if driver == storage.DriverPostgres {
				drop += ` ON japanese_integration_inbox`
			}
			if _, e := f.tenant.Exec(drop); e != nil {
				t.Fatal(e)
			}
			if e := f.s.ProcessPending(t.Context()); e != nil {
				t.Fatal(e)
			}
			r, _ := f.s.Event(t.Context(), f.connection, event.EventID)
			if r.Status != "applied" {
				t.Fatal(r)
			}
		})
	}
}
func TestIntegrationManualReopenAndLateDelivery(t *testing.T) {
	f := fixture(t, storage.DriverSQLite)
	f.binding(t, "target")
	event := f.event("late", 1, true)
	f.now = f.now.Add(24 * time.Hour)
	r := f.send(t, event)
	if r.Status != "applied" || f.state(t, "2026-11-19", "target") != taskdomain.ExecutionStatusDone || f.state(t, "2026-11-20", "target") != taskdomain.ExecutionStatusOpen {
		t.Fatalf("late=%+v", r)
	}
	if e := f.s.transaction(t.Context(), f.connection, true, func(tx tenantTx) error {
		o, e := tx.app.Reader.GetOccurrence(t.Context(), r.OccurrenceID)
		if e != nil {
			return e
		}
		_, e = tx.app.Occurrences.Execute(t.Context(), taskdomain.OccurrenceCommandRequest{WorkspaceID: "w1", TaskID: "target", OccurrenceID: o.OccurrenceID, Command: taskdomain.OccurrenceCommandReopen, ExpectedRuntimeEpoch: 1, Expected: taskdomain.OccurrenceCommandExpectedRevisions{Task: o.TaskRevision, Schedule: o.ScheduleRevision, Occurrence: o.Revision}, CommandID: "manual-reopen", ActorID: "u1", At: f.now})
		return e
	}); e != nil {
		t.Fatal(e)
	}
	f.send(t, event)
	duplicate := event
	duplicate.EventID = "same-version"
	f.send(t, duplicate)
	if f.state(t, "2026-11-19", "target") != taskdomain.ExecutionStatusOpen {
		t.Fatal("replayed source version undid manual reopen")
	}
	undo := event
	undo.EventID = "reopened-source"
	undo.StateRevision = 2
	undo.Completed = false
	f.send(t, undo)
	redo := event
	redo.EventID = "new-source-completion"
	redo.StateRevision = 3
	f.send(t, redo)
	if f.state(t, "2026-11-19", "target") != taskdomain.ExecutionStatusDone {
		t.Fatal("new source transition not applied")
	}
}
func TestBindingTargetIntervalsCannotOverlap(t *testing.T) {
	for _, driver := range []storage.Driver{storage.DriverSQLite, storage.DriverPostgres} {
		t.Run(string(driver), func(t *testing.T) {
			f := fixture(t, driver)
			f.binding(t, "target")
			f.binding(t, "other")
			catalog := f.catalog()
			catalog.CatalogRevision = 2
			catalog.Plan.Revision = 2
			catalog.Plan.TimeSlots[0].Tasks = append(catalog.Plan.TimeSlots[0].Tasks, StudyTask{ID: "words", Title: "Words", Path: "/words", Minutes: 5})
			if e := f.s.PutCatalog(t.Context(), f.connection, catalog); e != nil {
				t.Fatal(e)
			}
			_, e := f.s.SaveBinding(t.Context(), "w1", "u1", BindingInput{ConnectionID: f.connection.ID, PlanID: "plan-1", SlotID: "evening", TaskID: "words", TargetTaskID: "target", EffectiveFrom: "2026-11-19"})
			if !errors.Is(e, ErrConflict) {
				t.Fatalf("closed future interval overlap accepted: %v", e)
			}
		})
	}
}

func TestRevokedConnectionReleasesTargetAndPendingCannotWrite(t *testing.T) {
	for _, driver := range []storage.Driver{storage.DriverSQLite, storage.DriverPostgres} {
		t.Run(string(driver), func(t *testing.T) {
			f := fixture(t, driver)
			f.binding(t, "target")
			if e := f.s.Revoke(t.Context(), "w1", "u1", f.connection.ID); e != nil {
				t.Fatal(e)
			}
			if e := f.s.Revoke(t.Context(), "w1", "u1", f.connection.ID); e != nil {
				t.Fatal(e)
			}
			code, e := f.s.CreatePairing(t.Context(), "w1", "u1")
			if e != nil {
				t.Fatal(e)
			}
			pair, e := f.s.Pair(t.Context(), PairRequest{PairingCode: code.PairingCode, SourceInstanceID: "japanese-test", SourceUserID: "123"})
			if e != nil {
				t.Fatal(e)
			}
			c, e := f.s.loadConnection(t.Context(), pair.ConnectionID)
			if e != nil {
				t.Fatal(e)
			}
			if e = f.s.PutCatalog(t.Context(), c, f.catalog()); e != nil {
				t.Fatal(e)
			}
			if _, e = f.s.SaveBinding(t.Context(), "w1", "u1", BindingInput{ConnectionID: c.ID, PlanID: "plan-1", SlotID: "evening", TaskID: "listening", TargetTaskID: "target"}); e != nil {
				t.Fatalf("reconnected target still reserved: %v", e)
			}
			if _, e = f.control.Exec(bind(driver, `UPDATE japanese_integration_connections SET state='pending' WHERE id=?`), c.ID); e != nil {
				t.Fatal(e)
			}
			if _, e = f.s.AcceptEvent(t.Context(), c, f.event("pending", 1, true)); !errors.Is(e, ErrUnauthorized) {
				t.Fatalf("pending accepted event: %v", e)
			}
			if e = f.s.PutCatalog(t.Context(), c, f.catalog()); !errors.Is(e, ErrUnauthorized) {
				t.Fatalf("pending accepted catalog: %v", e)
			}
		})
	}
}

func TestPairingCodeOnlyResumesPendingInitialization(t *testing.T) {
	for _, driver := range []storage.Driver{storage.DriverSQLite, storage.DriverPostgres} {
		t.Run(string(driver), func(t *testing.T) {
			f := fixture(t, driver)
			code, e := f.s.CreatePairing(t.Context(), "w1", "u1")
			if e != nil {
				t.Fatal(e)
			}
			input := PairRequest{PairingCode: code.PairingCode, SourceInstanceID: "fresh-source", SourceUserID: "789"}
			pair, e := f.s.Pair(t.Context(), input)
			if e != nil {
				t.Fatal(e)
			}
			if _, e = f.s.Pair(t.Context(), input); !errors.Is(e, ErrUnauthorized) {
				t.Fatalf("active code reused: %v", e)
			}
			// A failed cross-store initialization leaves a pending claim. Its exact
			// original claimant can finish initialization with the still-live code.
			if _, e = f.control.Exec(bind(driver, `UPDATE japanese_integration_connections SET state='pending' WHERE id=?`), pair.ConnectionID); e != nil {
				t.Fatal(e)
			}
			resumed, e := f.s.Pair(t.Context(), input)
			if e != nil || resumed.Secret != pair.Secret || resumed.ConnectionID != pair.ConnectionID {
				t.Fatalf("pending resume=%+v err=%v", resumed, e)
			}
		})
	}
}

func TestBindingRequiresNativeScheduleCoverage(t *testing.T) {
	for _, driver := range []storage.Driver{storage.DriverSQLite, storage.DriverPostgres} {
		t.Run(string(driver), func(t *testing.T) {
			for _, mismatch := range []string{"weekday", "end date", "empty source dates"} {
				t.Run(mismatch, func(t *testing.T) {
					f := fixture(t, driver)
					switch mismatch {
					case "weekday":
						if _, e := f.tenant.Exec(`UPDATE domain_task_schedule_versions_v2 SET recurrence_type='weekly',recurrence_rule='{"interval":1,"weekdays":[1]}' WHERE task_id='target'`); e != nil {
							t.Fatal(e)
						}
					case "end date":
						if _, e := f.tenant.Exec(`UPDATE domain_task_schedule_versions_v2 SET ends_on='2026-11-20' WHERE task_id='target'`); e != nil {
							t.Fatal(e)
						}
					case "empty source dates":
						catalog := f.catalog()
						catalog.CatalogRevision = 2
						catalog.Plan.Revision = 2
						catalog.Plan.EndDate = "2026-11-20"
						catalog.Plan.Weekdays = []int{1}
						if e := f.s.PutCatalog(t.Context(), f.connection, catalog); e != nil {
							t.Fatal(e)
						}
					}
					_, e := f.s.SaveBinding(t.Context(), "w1", "u1", BindingInput{ConnectionID: f.connection.ID, PlanID: "plan-1", SlotID: "evening", TaskID: "listening", TargetTaskID: "target"})
					if !errors.Is(e, ErrInvalid) {
						t.Fatalf("mismatch=%s accepted: %v", mismatch, e)
					}
					if n := f.count(t, `SELECT COUNT(*) FROM japanese_integration_bindings`); n != 0 {
						t.Fatal("invalid preview created binding")
					}
				})
			}
		})
	}
}

func TestCatalogLifecycleReleasesRemovedSourceBindings(t *testing.T) {
	for _, driver := range []storage.Driver{storage.DriverSQLite, storage.DriverPostgres} {
		t.Run(string(driver), func(t *testing.T) {
			for _, change := range []string{"delete and recreate", "new generation", "removed task", "changed semantic task", "title only"} {
				t.Run(change, func(t *testing.T) {
					f := fixture(t, driver)
					old := f.binding(t, "target")
					oldEvent := f.event("old-event", 1, true)
					if _, e := f.s.AcceptEvent(t.Context(), f.connection, oldEvent); e != nil {
						t.Fatal(e)
					}
					catalog := f.catalog()
					catalog.CatalogRevision = 2
					catalog.Plan.Revision = 2
					switch change {
					case "delete and recreate":
						deleted := catalog
						deleted.Plan = nil
						if e := f.s.PutCatalog(t.Context(), f.connection, deleted); e != nil {
							t.Fatal(e)
						}
						catalog.CatalogRevision = 3
						catalog.Plan.PlanID = "plan-new"
					case "new generation":
						catalog.Plan.PlanID = "plan-new"
					case "removed task":
						catalog.Plan.TimeSlots[0].Tasks = []StudyTask{{ID: "words", Title: "Words", Path: "/words", Minutes: 20}}
					case "changed semantic task":
						catalog.Plan.TimeSlots[0].Tasks[0].Path = "/listening/new"
					case "title only":
						catalog.Plan.Title = "Renamed"
						catalog.Plan.TimeSlots[0].Tasks[0].Title = "Renamed task"
					}
					if e := f.s.PutCatalog(t.Context(), f.connection, catalog); e != nil {
						t.Fatal(e)
					}
					var enabled int
					if e := f.tenant.QueryRow(bind(driver, `SELECT enabled FROM japanese_integration_bindings WHERE id=?`), old.ID).Scan(&enabled); e != nil {
						t.Fatal(e)
					}
					if change == "title only" {
						if enabled != 1 {
							t.Fatal("presentation change disabled identity")
						}
						return
					}
					if enabled != 0 {
						t.Fatal("obsolete source still reserves target")
					}
					taskID := catalog.Plan.TimeSlots[0].Tasks[0].ID
					_, e := f.s.SaveBinding(t.Context(), "w1", "u1", BindingInput{ConnectionID: f.connection.ID, PlanID: catalog.Plan.PlanID, SlotID: "evening", TaskID: taskID, TargetTaskID: "target"})
					if e != nil {
						t.Fatalf("new source cannot reuse old target: %v", e)
					}
					if e = f.s.ProcessPending(t.Context()); e != nil {
						t.Fatal(e)
					}
					r, e := f.s.Event(t.Context(), f.connection, oldEvent.EventID)
					if e != nil || r.Status != "needs_attention" || f.state(t, "2026-11-19", "target") != taskdomain.ExecutionStatusOpen {
						t.Fatalf("obsolete event applied: %+v err=%v", r, e)
					}
				})
			}
		})
	}
}
