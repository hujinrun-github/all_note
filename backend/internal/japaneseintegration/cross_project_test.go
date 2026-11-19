package japaneseintegration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/japaneseintegration"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

// TestJapaneseCrossProjectHTTP launches the sibling repository's source test
// to exercise its real PostgreSQL transaction/outbox and signed HTTP worker
// against this repository's real handlers, inbox, domain writer and changes.
// Set FLOWSPACE_JAPANESE_REPO and JAPANESE_TEST_DATABASE_URL to opt in.
func TestJapaneseCrossProjectHTTP(t *testing.T) {
	repo := os.Getenv("FLOWSPACE_JAPANESE_REPO")
	if repo == "" {
		t.Skip("set FLOWSPACE_JAPANESE_REPO to run both repositories' real HTTP integration")
	}
	sourceDB := os.Getenv("JAPANESE_TEST_DATABASE_URL")
	if sourceDB == "" {
		t.Fatal("JAPANESE_TEST_DATABASE_URL must name an isolated Japanese PostgreSQL test database")
	}
	if strings.TrimSpace(os.Getenv("FLOWSPACE_TEST_DATABASE_URL")) == "" {
		t.Fatal("FLOWSPACE_TEST_DATABASE_URL is required when the cross-project test is enabled")
	}
	for _, driver := range []storage.Driver{storage.DriverSQLite, storage.DriverPostgres} {
		t.Run(string(driver), func(t *testing.T) {
			fixture := japaneseintegration.NewCrossProjectFixture(t, driver)
			ctx := t.Context()
			location, err := time.LoadLocation("Asia/Shanghai")
			if err != nil {
				t.Fatal(err)
			}
			localNow := time.Now().In(location)
			today := localNow.Format("2006-01-02")
			tomorrow := localNow.AddDate(0, 0, 1).Format("2006-01-02")
			const targetID = "cross-project-target"
			if err := fixture.Runtime.Writer.BeginFencedWrite(ctx, fixture.WorkspaceID, fixture.Runtime.Epoch, func(tx storage.TenantWriteTx) error {
				snapshot, _, err := (taskdomain.TaskFactory{}).Build(taskdomain.TaskCreationInput{WorkspaceID: fixture.WorkspaceID, Project: taskdomain.ProjectIdentity{WorkspaceID: fixture.WorkspaceID, ProjectID: taskdomain.SystemInboxProjectID}, TaskID: targetID, ActorID: fixture.ActorID, ActorTime: fixture.Now, Title: "Cross-project listening", Schedule: taskdomain.ScheduleInput{RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "Asia/Shanghai", StartsOn: today, EndsOn: localNow.AddDate(0, 0, 2).Format("2006-01-02"), Rule: json.RawMessage(`{"interval":1}`)}})
				if err != nil {
					return err
				}
				snapshot.Task.LifecycleStatus = taskdomain.TaskLifecycleActive
				return tx.TaskDomainWriter().CreateTaskAggregate(ctx, snapshot)
			}); err != nil {
				t.Fatal(err)
			}
			gin.SetMode(gin.TestMode)
			engine := gin.New()
			management := engine.Group("/api")
			management.Use(func(c *gin.Context) {
				requestContext := auth.ContextWithIdentity(c.Request.Context(), auth.RequestIdentity{UserID: fixture.ActorID, WorkspaceID: fixture.WorkspaceID})
				c.Request = c.Request.WithContext(auth.ContextWithWorkspaceScope(requestContext, fixture.WorkspaceID))
				c.Next()
			})
			handler.RegisterJapaneseIntegrationRoutes(engine.Group("/api"), management, fixture.Service)
			var lostAcknowledgement atomic.Bool
			var nonceLock sync.Mutex
			nonces := []string{}
			receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodPost && r.URL.Path == "/api/integrations/japanese/events" {
					nonceLock.Lock()
					nonces = append(nonces, r.Header.Get(japaneseintegration.HeaderNonce))
					nonceLock.Unlock()
					if lostAcknowledgement.CompareAndSwap(false, true) {
						response := httptest.NewRecorder()
						engine.ServeHTTP(response, r)
						if response.Code == http.StatusAccepted {
							panic(http.ErrAbortHandler)
						}
						for key, values := range response.Header() {
							w.Header()[key] = values
						}
						w.WriteHeader(response.Code)
						_, _ = w.Write(response.Body.Bytes())
						return
					}
				}
				engine.ServeHTTP(w, r)
			}))
			defer receiver.Close()
			request := func(method, path string, input, output any) {
				t.Helper()
				var body bytes.Buffer
				if input != nil {
					if err := json.NewEncoder(&body).Encode(input); err != nil {
						t.Fatal(err)
					}
				}
				req, err := http.NewRequestWithContext(ctx, method, receiver.URL+path, &body)
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Content-Type", "application/json")
				response, err := receiver.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer response.Body.Close()
				if response.StatusCode < 200 || response.StatusCode >= 300 {
					raw, _ := io.ReadAll(response.Body)
					t.Fatalf("receiver %s %s: HTTP %d %s", method, path, response.StatusCode, raw)
				}
				if output != nil {
					if err := json.NewDecoder(response.Body).Decode(output); err != nil {
						t.Fatal(err)
					}
				}
			}
			var pairing struct {
				Data japaneseintegration.PairingCode `json:"data"`
			}
			request(http.MethodPost, "/api/integrations/japanese/pairing-codes", nil, &pairing)
			handshake := t.TempDir()
			childContext, stopChild := context.WithTimeout(ctx, 90*time.Second)
			defer stopChild()
			command := exec.CommandContext(childContext, "go", "test", "./internal/module/flowspace", "-run", "^TestFlowSpaceCrossProjectSource$", "-count=1", "-v")
			command.Dir = repo
			command.Env = append(os.Environ(), "DATABASE_URL_TEST="+sourceDB, "DATABASE_URL=postgres://unused@127.0.0.1/japanese_application_unused?sslmode=disable", "REQUIRE_TEST_DATABASE=1", "FLOWSPACE_CROSS_PROJECT_URL="+receiver.URL, "FLOWSPACE_CROSS_PROJECT_CODE="+pairing.Data.PairingCode, "FLOWSPACE_CROSS_PROJECT_HANDSHAKE="+handshake)
			var childOutput bytes.Buffer
			command.Stdout, command.Stderr = &childOutput, &childOutput
			childDone := make(chan error, 1)
			go func() { childDone <- command.Run() }()
			workerContext, stopWorker := context.WithCancel(ctx)
			workerDone := make(chan struct{})
			go func() { defer close(workerDone); fixture.Service.Run(workerContext) }()
			defer func() { stopWorker(); <-workerDone }()
			var source struct {
				ConnectionID string `json:"connection_id"`
				PlanID       string `json:"plan_id"`
				StudyDate    string `json:"study_date"`
			}
			ticker := time.NewTicker(25 * time.Millisecond)
			defer ticker.Stop()
			for {
				raw, err := os.ReadFile(filepath.Join(handshake, "source.json"))
				if err == nil {
					if err := json.Unmarshal(raw, &source); err != nil {
						t.Fatal(err)
					}
					break
				}
				select {
				case err := <-childDone:
					t.Fatalf("source exited before catalog: %v\n%s", err, childOutput.String())
				case <-childContext.Done():
					t.Fatal("cross-project source setup timeout")
				case <-ticker.C:
				}
			}
			if source.StudyDate != today {
				t.Fatalf("source date=%s receiver date=%s", source.StudyDate, today)
			}
			var binding struct {
				Data japaneseintegration.Binding `json:"data"`
			}
			request(http.MethodPost, "/api/integrations/japanese/bindings", japaneseintegration.BindingInput{ConnectionID: source.ConnectionID, PlanID: source.PlanID, SlotID: "evening", TaskID: "listening", TargetTaskID: targetID}, &binding)
			var initialBatches int
			if err := fixture.TenantDB.QueryRow(`SELECT count(*) FROM mobile_v2_scope_change_batches`).Scan(&initialBatches); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(handshake, "binding-ready"), []byte("ready"), 0600); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-childDone:
				if err != nil {
					t.Fatalf("source proof failed: %v\n%s", err, childOutput.String())
				}
			case <-childContext.Done():
				t.Fatal("cross-project source completion timeout")
			}
			var result struct {
				EventID          string `json:"event_id"`
				RemoteStatus     string `json:"remote_status"`
				Attempts         int    `json:"attempts"`
				SourceEventCount int    `json:"source_event_count"`
			}
			raw, err := os.ReadFile(filepath.Join(handshake, "result.json"))
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result.RemoteStatus != "applied" || result.Attempts != 2 || result.SourceEventCount != 1 {
				t.Fatalf("invalid source result: %+v", result)
			}
			for date, want := range map[string]taskdomain.ExecutionStatus{today: taskdomain.ExecutionStatusDone, tomorrow: taskdomain.ExecutionStatusOpen} {
				occurrence, err := fixture.Runtime.Application.Reader.GetOccurrence(ctx, taskdomain.DeterministicOccurrenceID(fixture.WorkspaceID, targetID, date))
				if err != nil || occurrence.Status != want {
					t.Fatalf("%s status=%s want=%s error=%v", date, occurrence.Status, want, err)
				}
			}
			var receiptCount, logCount, batches int
			if err := fixture.TenantDB.QueryRow(`SELECT count(*) FROM japanese_integration_inbox`).Scan(&receiptCount); err != nil {
				t.Fatal(err)
			}
			if err := fixture.TenantDB.QueryRow(`SELECT count(*) FROM domain_task_execution_logs_v2`).Scan(&logCount); err != nil {
				t.Fatal(err)
			}
			if err := fixture.TenantDB.QueryRow(`SELECT count(*) FROM mobile_v2_scope_change_batches`).Scan(&batches); err != nil {
				t.Fatal(err)
			}
			if receiptCount != 1 || logCount != 1 || batches <= initialBatches {
				t.Fatalf("receipt=%d logs=%d batches=%d baseline=%d", receiptCount, logCount, batches, initialBatches)
			}
			nonceLock.Lock()
			copied := append([]string(nil), nonces...)
			nonceLock.Unlock()
			if len(copied) != 2 || copied[0] == "" || copied[0] == copied[1] {
				t.Fatal("HTTP retry did not use fresh transport nonces")
			}
			if !strings.Contains(childOutput.String(), "PASS") {
				t.Fatal("source test did not report success")
			}
			t.Log("real PostgreSQL completion → durable outbox → signed HTTP (lost acknowledgement + restarted worker) → one receipt/log/mobile change; today done, tomorrow open")
		})
	}
}
