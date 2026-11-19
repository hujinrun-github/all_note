package webplanning

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/transcription"
)

type memoryRepository struct {
	mu     sync.Mutex
	drafts map[string]Draft
	jobs   map[string]Job
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{drafts: map[string]Draft{}, jobs: map[string]Job{}}
}

type memoryTx struct {
	repo  *memoryRepository
	scope Scope
}

func cloneDraft(d Draft) Draft {
	d.Items = append([]Item(nil), d.Items...)
	d.Clarifications = append([]string(nil), d.Clarifications...)
	d.Assumptions = append([]string(nil), d.Assumptions...)
	d.CleanupObjectKeys = append([]string(nil), d.CleanupObjectKeys...)
	if d.LatestJob != nil {
		j := *d.LatestJob
		d.LatestJob = &j
	}
	return d
}
func (r *memoryRepository) Read(ctx context.Context, scope Scope, fn func(Reader) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fn(&memoryTx{r, scope})
}
func (r *memoryRepository) Write(ctx context.Context, scope Scope, fn func(Tx) error) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := newMemoryRepository()
	for k, v := range r.drafts {
		copy.drafts[k] = cloneDraft(v)
	}
	for k, v := range r.jobs {
		copy.jobs[k] = v
	}
	if err := fn(&memoryTx{copy, scope}); err != nil {
		return err
	}
	r.drafts = copy.drafts
	r.jobs = copy.jobs
	return nil
}
func (r *memoryRepository) Commit(context.Context, Scope, CommitRequest) (Receipt, error) {
	return Receipt{}, ErrUnavailable
}
func (t *memoryTx) owns(workspace, owner string) bool {
	return t.scope.WorkspaceID == workspace && (t.scope.ActorID == "" || t.scope.ActorID == owner)
}
func (t *memoryTx) GetDraft(_ context.Context, id string) (Draft, error) {
	d, ok := t.repo.drafts[id]
	if !ok || !t.owns(d.WorkspaceID, d.OwnerID) {
		return Draft{}, ErrNotFound
	}
	return cloneDraft(d), nil
}
func (t *memoryTx) FindDraftByRequestID(ctx context.Context, id string) (Draft, error) {
	for _, d := range t.repo.drafts {
		if d.RequestID == id && t.owns(d.WorkspaceID, d.OwnerID) {
			return cloneDraft(d), nil
		}
	}
	return Draft{}, ErrNotFound
}
func (t *memoryTx) ListDrafts(context.Context) ([]Draft, error) {
	ds := []Draft{}
	for _, d := range t.repo.drafts {
		if t.owns(d.WorkspaceID, d.OwnerID) {
			ds = append(ds, cloneDraft(d))
		}
	}
	return ds, nil
}
func (t *memoryTx) GetJob(_ context.Context, id string) (Job, error) {
	j, ok := t.repo.jobs[id]
	if !ok || !t.owns(j.WorkspaceID, j.OwnerID) {
		return Job{}, ErrNotFound
	}
	return j, nil
}
func (t *memoryTx) FindJobByRequestID(_ context.Context, id string) (Job, error) {
	for _, j := range t.repo.jobs {
		if j.RequestID == id && t.owns(j.WorkspaceID, j.OwnerID) {
			return j, nil
		}
	}
	return Job{}, ErrNotFound
}
func (t *memoryTx) ListEligibleJobs(_ context.Context, now time.Time, limit int) ([]Job, error) {
	js := []Job{}
	for _, j := range t.repo.jobs {
		if t.owns(j.WorkspaceID, j.OwnerID) && (j.State == "queued" || j.State == "processing" && !j.LeaseUntil.After(now)) {
			js = append(js, j)
		}
	}
	sort.Slice(js, func(i, j int) bool { return js[i].ID < js[j].ID })
	if len(js) > limit {
		js = js[:limit]
	}
	return js, nil
}
func (t *memoryTx) GetCommitByDraft(context.Context, string) (Receipt, error) {
	return Receipt{}, ErrNotFound
}
func (t *memoryTx) GetCommitByKey(context.Context, string) (Receipt, error) {
	return Receipt{}, ErrNotFound
}
func (t *memoryTx) ValidatePlan(_ context.Context, d Draft) (Validation, error) {
	return Validation{DraftRevision: d.Revision, Issues: []string{}, Conflicts: []string{}}, nil
}
func (t *memoryTx) PutDraft(_ context.Context, d Draft, expected int64) error {
	old, ok := t.repo.drafts[d.ID]
	if expected == 0 && ok || expected > 0 && (!ok || old.Revision != expected) || d.Revision != expected+1 || !t.owns(d.WorkspaceID, d.OwnerID) {
		return ErrConflict
	}
	t.repo.drafts[d.ID] = cloneDraft(d)
	return nil
}
func (t *memoryTx) PutJob(_ context.Context, j Job, expected int64) error {
	old, ok := t.repo.jobs[j.ID]
	if expected == 0 && ok || expected > 0 && (!ok || old.Revision != expected) || j.Revision != expected+1 || !t.owns(j.WorkspaceID, j.OwnerID) {
		return ErrConflict
	}
	t.repo.jobs[j.ID] = j
	return nil
}
func (t *memoryTx) CommitPlan(context.Context, CommitRequest) (Receipt, error) {
	return Receipt{}, ErrUnavailable
}

type chatFunc func(context.Context, string, string, string) (string, error)

func (f chatFunc) Generate(ctx context.Context, w, s, u string) (string, error) {
	return f(ctx, w, s, u)
}

type transcribeFunc func(context.Context, transcription.Input) (string, error)

func (f transcribeFunc) Transcribe(ctx context.Context, in transcription.Input) (string, error) {
	return f(ctx, in)
}
func testService(t *testing.T, chat Chat) (*Service, *memoryRepository, Scope) {
	t.Helper()
	repository := newMemoryRepository()
	var counter atomic.Int64
	service, err := New(Config{Repository: repository, Objects: objectstore.NewMemoryStore(), Chat: chat, Transcriber: transcribeFunc(func(context.Context, transcription.Input) (string, error) { return "周日复习半小时", nil }), Now: func() time.Time { return time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC) }, NewID: func() string { return fmt.Sprintf("id-%d", counter.Add(1)) }})
	if err != nil {
		t.Fatal(err)
	}
	return service, repository, Scope{WorkspaceID: "workspace", ActorID: "owner"}
}
func createTextDraft(t *testing.T, s *Service, scope Scope) Draft {
	t.Helper()
	d, err := s.Create(t.Context(), scope, CreateRequest{RequestID: "create", InputText: "周日复习半小时", Timezone: "Asia/Shanghai", ProjectID: "project"})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const sundayPlan = `{"schema_version":1,"title":"周日复习","items":[{"title":"复习","description":"周日八点复习半小时","priority":0,"schedule":{"recurrence_type":"weekly","timing_type":"time_block","timezone":"Asia/Shanghai","starts_on":"2026-11-22","local_start_time":"20:00:00","duration_minutes":30,"rule":{"interval":1,"weekdays":[0]}}}],"clarifications":[],"assumptions":[]}`

func TestPlanningCreateIsIdempotentAndOwnerScoped(t *testing.T) {
	s, _, scope := testService(t, nil)
	d := createTextDraft(t, s, scope)
	again, err := s.Create(t.Context(), scope, CreateRequest{RequestID: "create", InputText: "周日复习半小时", Timezone: "Asia/Shanghai", ProjectID: "project"})
	if err != nil || again.ID != d.ID {
		t.Fatalf("replay=%+v err=%v", again, err)
	}
	_, err = s.Create(t.Context(), scope, CreateRequest{RequestID: "create", InputText: "不同内容", Timezone: "Asia/Shanghai"})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("changed request: %v", err)
	}
	_, err = s.Get(t.Context(), Scope{WorkspaceID: scope.WorkspaceID, ActorID: "someone-else"}, d.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("owner isolation: %v", err)
	}
	_, err = s.Get(t.Context(), Scope{WorkspaceID: "other-workspace", ActorID: scope.ActorID}, d.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("workspace isolation: %v", err)
	}
}
func TestPlanningGenerationUsesFixedReferenceAndSundayZero(t *testing.T) {
	var system, user string
	s, _, scope := testService(t, chatFunc(func(_ context.Context, _ string, sys, in string) (string, error) {
		system, user = sys, in
		return sundayPlan, nil
	}))
	d := createTextDraft(t, s, scope)
	job, err := s.Start(t.Context(), scope, d.ID, "generate", StartRequest{RequestID: "generate", ExpectedRevision: d.Revision})
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.RunWorkspace(t.Context(), scope.WorkspaceID)
	if err != nil || !claimed {
		t.Fatalf("run=%v %v", claimed, err)
	}
	got, err := s.Get(t.Context(), scope, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || len(got.Items) != 1 || got.Items[0].Schedule.Rule.Weekdays[0] != 0 || got.LatestJob.State != "completed" {
		t.Fatalf("draft=%+v", got)
	}
	if !strings.Contains(system, "0=Sunday") || !strings.Contains(user, "2026-11-19T12:00:00Z") {
		t.Fatalf("generation context missing: %s / %s", system, user)
	}
	if got.Items[0].ID == "" || got.Revision <= job.Draft.Revision {
		t.Fatal("missing stable IDs or version")
	}
}
func TestPlanningRejectsMalformedGeneratedSchedule(t *testing.T) {
	s, _, scope := testService(t, chatFunc(func(context.Context, string, string, string) (string, error) {
		return strings.Replace(sundayPlan, `"weekdays":[0]`, `"weekdays":[7]`, 1), nil
	}))
	d := createTextDraft(t, s, scope)
	_, err := s.Start(t.Context(), scope, d.ID, "generate", StartRequest{RequestID: "generate", ExpectedRevision: d.Revision})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RunWorkspace(t.Context(), scope.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(t.Context(), scope, d.ID)
	if got.LatestJob.State != "failed" || got.LatestJob.ErrorCode != "invalid_plan" || len(got.Items) != 0 {
		t.Fatalf("invalid schedule applied: %+v", got)
	}
}
func TestPlanningLateGenerationCannotOverwriteEditedText(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s, _, scope := testService(t, chatFunc(func(context.Context, string, string, string) (string, error) {
		close(started)
		<-release
		return sundayPlan, nil
	}))
	d := createTextDraft(t, s, scope)
	job, err := s.Start(t.Context(), scope, d.ID, "generate", StartRequest{RequestID: "generate", ExpectedRevision: d.Revision})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := s.RunWorkspace(context.Background(), scope.WorkspaceID); done <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not start")
	}
	newText := "周日不安排，改成周一"
	updated, err := s.Patch(t.Context(), scope, d.ID, PatchRequest{ExpectedRevision: job.Draft.Revision, InputText: &newText})
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not finish")
	}
	got, _ := s.Get(t.Context(), scope, d.ID)
	if got.InputText != newText || got.Revision != updated.Revision || len(got.Items) != 0 || got.LatestJob.State != "stale" {
		t.Fatalf("late generation overwrite: %+v", got)
	}
}
func TestPlanningStartReplaySurvivesConfigurationChange(t *testing.T) {
	s, _, scope := testService(t, chatFunc(func(context.Context, string, string, string) (string, error) { return sundayPlan, nil }))
	d := createTextDraft(t, s, scope)
	req := StartRequest{RequestID: "generate", ExpectedRevision: d.Revision}
	first, err := s.Start(t.Context(), scope, d.ID, "generate", req)
	if err != nil {
		t.Fatal(err)
	}
	s.config.Chat = nil
	replay, err := s.Start(t.Context(), scope, d.ID, "generate", req)
	if err != nil || replay.Job.ID != first.Job.ID {
		t.Fatalf("replay failed: %+v %v", replay, err)
	}
}
func TestPlanningRetryIsIdempotentAndCannotUseEditedInput(t *testing.T) {
	s, _, scope := testService(t, chatFunc(func(context.Context, string, string, string) (string, error) {
		return "", errors.New("temporary outage")
	}))
	d := createTextDraft(t, s, scope)
	first, err := s.Start(t.Context(), scope, d.ID, "generate", StartRequest{RequestID: "generate", ExpectedRevision: d.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunWorkspace(t.Context(), scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	retry, err := s.Retry(t.Context(), scope, first.Job.ID, "retry")
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Retry(t.Context(), scope, first.Job.ID, "retry")
	if err != nil || retry.Job.ID != replay.Job.ID {
		t.Fatalf("retry replay: %+v %v", replay, err)
	}
	if _, err = s.RunWorkspace(t.Context(), scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(t.Context(), scope, d.ID)
	text := "已经纠正的文字"
	if _, err = s.Patch(t.Context(), scope, d.ID, PatchRequest{ExpectedRevision: got.Revision, InputText: &text}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Retry(t.Context(), scope, retry.Job.ID, "retry-edited"); !errors.Is(err, ErrConflict) {
		t.Fatalf("old input retried: %v", err)
	}
}
func TestPlanningAudioUploadReplayAndTranscription(t *testing.T) {
	s, _, scope := testService(t, nil)
	d := createTextDraft(t, s, scope)
	audio := []byte("RIFF synthetic test audio")
	uploaded, err := s.Upload(t.Context(), scope, d.ID, d.Revision, "audio/wav", strings.NewReader(string(audio)))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.Upload(t.Context(), scope, d.ID, d.Revision, "audio/wav", strings.NewReader(string(audio)))
	if err != nil || replay.Revision != uploaded.Revision {
		t.Fatalf("audio replay: %+v %v", replay, err)
	}
	object, err := s.Audio(t.Context(), scope, d.ID)
	if err != nil {
		t.Fatal(err)
	}
	read, _ := io.ReadAll(object.Body)
	_ = object.Body.Close()
	if string(read) != string(audio) {
		t.Fatal("wrong audio")
	}
	_, err = s.Start(t.Context(), scope, d.ID, "transcribe", StartRequest{RequestID: "transcribe", ExpectedRevision: uploaded.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunWorkspace(t.Context(), scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(t.Context(), scope, d.ID)
	if got.Transcript != "周日复习半小时" || got.InputText != got.Transcript || got.LatestJob.State != "completed" {
		t.Fatalf("transcription=%+v", got)
	}
}

func TestPlanningExpiredAudioCleanupIsIdempotentWhenObjectAlreadyMissing(t *testing.T) {
	s, _, scope := testService(t, nil)
	d := createTextDraft(t, s, scope)
	uploaded, err := s.Upload(t.Context(), scope, d.ID, d.Revision, "audio/wav", strings.NewReader("RIFF test audio"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.config.Objects.Remove(t.Context(), uploaded.AudioObjectKey); err != nil {
		t.Fatal(err)
	}
	later := s.now().Add(8 * 24 * time.Hour)
	s.config.Now = func() time.Time { return later }
	if _, err = s.RunWorkspace(t.Context(), scope.WorkspaceID); err != nil {
		t.Fatalf("missing object blocked cleanup: %v", err)
	}
	got, err := s.Get(t.Context(), scope, d.ID)
	if err != nil || got.AudioAvailable || got.AudioObjectKey != "" {
		t.Fatalf("expired audio remained: %+v %v", got, err)
	}
	if _, err = s.RunWorkspace(t.Context(), scope.WorkspaceID); err != nil {
		t.Fatalf("cleanup replay: %v", err)
	}
}
func TestPlanningProcessRestartReclaimsExpiredLease(t *testing.T) {
	s, repository, scope := testService(t, chatFunc(func(context.Context, string, string, string) (string, error) { return sundayPlan, nil }))
	d := createTextDraft(t, s, scope)
	queued, err := s.Start(t.Context(), scope, d.ID, "generate", StartRequest{ExpectedRevision: d.Revision, RequestID: "generate"})
	if err != nil {
		t.Fatal(err)
	}
	err = repository.Write(t.Context(), scope, func(tx Tx) error {
		j, e := tx.GetJob(t.Context(), queued.Job.ID)
		if e != nil {
			return e
		}
		old := j.Revision
		j.Revision++
		j.State = "processing"
		j.Attempt = 1
		j.LeaseToken = "dead-process"
		j.LeaseUntil = s.now().Add(-time.Second)
		return tx.PutJob(t.Context(), j, old)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RunWorkspace(t.Context(), scope.WorkspaceID); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(t.Context(), scope, d.ID)
	if got.LatestJob.State != "completed" || got.LatestJob.Attempt != 2 || got.Status != "ready" {
		t.Fatalf("lease not recovered: %+v", got)
	}
}
