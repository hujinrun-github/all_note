package contentimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/contentsource"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/podcastaudio"
	"github.com/hujinrun/flowspace/internal/provisioning"
	"github.com/hujinrun/flowspace/internal/storage"
	storagesqlite "github.com/hujinrun/flowspace/internal/storage/sqlite"
	"github.com/hujinrun/flowspace/internal/testsupport"
)

type fixedResolver struct{ episode *contentsource.Episode }

func (r fixedResolver) Resolve(context.Context, string) (*contentsource.Episode, error) {
	copy := *r.episode
	return &copy, nil
}

type scriptedGenerator struct {
	calls        int
	systemPrompt string
}

func (g *scriptedGenerator) Generate(_ context.Context, _ string, systemPrompt, _ string) (string, error) {
	g.calls++
	g.systemPrompt = systemPrompt
	return `{"title":"AI 产品经理","summary":"产品经理会从需求执行转向问题定义。","key_points":["先定义问题"],"chapters":["角色变化"],"action_items":["重写问题陈述"]}`, nil
}

type failingGenerator struct {
	calls int
	err   error
}

func (g *failingGenerator) Generate(context.Context, string, string, string) (string, error) {
	g.calls++
	return "", g.err
}

func TestWorkerPublishesStructuredNoteFromPublicTranscript(t *testing.T) {
	store := openContentImportTestStore(t)
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace-1")
	resolver := fixedResolver{episode: &contentsource.Episode{SourceType: "xiaoyuzhou", CanonicalURL: "https://www.xiaoyuzhoufm.com/episode/e1", ExternalID: "e1", Title: "AI 产品经理的下一站", PodcastTitle: "产品沉思录", TranscriptURL: "https://cdn.example/transcript.vtt", HasPublicTranscript: true}}
	service, err := NewService(store, resolver)
	if err != nil {
		t.Fatal(err)
	}
	item, err := service.Create(ctx, uuid.NewString(), CreateRequest{SourceURL: "https://www.xiaoyuzhoufm.com/episode/e1", SummarizeWithAI: true, SummaryPrompt: "重点提炼产品策略，并返回约定 JSON。", Tags: []string{"播客"}})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		body := "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\n产品经理需要先定义问题。\n"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: request, ContentLength: int64(len(body))}, nil
	})}
	generator := &scriptedGenerator{}
	worker := NewWorker(store, resolver, client, generator, "test-worker")
	claimed, err := worker.RunOne(context.Background())
	if err != nil || !claimed {
		t.Fatalf("RunOne() claimed=%v error=%v", claimed, err)
	}
	completed, err := service.Get(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != "completed" || completed.ResultNoteID == "" {
		t.Fatalf("completed import = %#v", completed)
	}
	note, err := store.Notes().GetByID(ctx, completed.ResultNoteID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note.Body, "产品经理会从需求执行转向问题定义") || strings.Contains(note.Body, "完整逐字稿") {
		t.Fatalf("note body = %s", note.Body)
	}
	if generator.calls != 1 {
		t.Fatalf("generator calls = %d", generator.calls)
	}
	if generator.systemPrompt != "重点提炼产品策略，并返回约定 JSON。" {
		t.Fatalf("generator system prompt = %q", generator.systemPrompt)
	}
}

func TestWorkerDoesNotUseTextAIWhenSummaryIsDisabled(t *testing.T) {
	store := openContentImportTestStore(t)
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace-1")
	resolver := fixedResolver{episode: &contentsource.Episode{SourceType: "apple", CanonicalURL: "https://podcasts.apple.com/podcast/show/id1?i=2", ExternalID: "2", Title: "完整逐字稿测试", TranscriptURL: "https://cdn.example/transcript.txt", HasPublicTranscript: true}}
	service, _ := NewService(store, resolver)
	item, err := service.Create(ctx, uuid.NewString(), CreateRequest{SourceURL: "https://podcasts.apple.com/podcast/show/id1?i=2", SummarizeWithAI: false})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		body := "这是发布者提供的完整逐字稿。"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: request, ContentLength: int64(len(body))}, nil
	})}
	generator := &scriptedGenerator{}
	worker := NewWorker(store, resolver, client, generator, "test-worker")
	if _, err := worker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	completed, _ := service.Get(ctx, item.ID)
	note, err := store.Notes().GetByID(ctx, completed.ResultNoteID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note.Body, "完整逐字稿") || generator.calls != 0 {
		t.Fatalf("body=%s calls=%d", note.Body, generator.calls)
	}
}

func TestWorkerExposesTextAICallFailureAndRetriesFromStoredTranscript(t *testing.T) {
	store := openContentImportTestStore(t)
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace-1")
	resolver := fixedResolver{episode: &contentsource.Episode{
		SourceType: "xiaoyuzhou", CanonicalURL: "https://www.xiaoyuzhoufm.com/episode/ai-unavailable",
		ExternalID: "ai-unavailable", Title: "需要 AI 整理的单集", TranscriptURL: "https://cdn.example/transcript.txt",
		HasPublicTranscript: true,
	}}
	service, _ := NewService(store, resolver)
	item, err := service.Create(ctx, uuid.NewString(), CreateRequest{
		SourceURL: "https://www.xiaoyuzhoufm.com/episode/ai-unavailable", SummarizeWithAI: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	transcriptFetches := 0
	client := &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		transcriptFetches++
		body := "这是已经付出转写成本、需要保留以便重试的逐字稿。"
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: request, ContentLength: int64(len(body))}, nil
	})}
	generator := &failingGenerator{err: errors.New("provider timeout")}
	worker := NewWorker(store, resolver, client, generator, "text-ai-failure-worker")
	if claimed, err := worker.RunOne(context.Background()); err != nil || !claimed {
		t.Fatalf("RunOne() claimed=%v error=%v", claimed, err)
	}

	failed, err := service.Get(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != model.ContentImportStatusFailed || failed.ErrorCode != "TEXT_AI_CALL_FAILED" || failed.ResultNoteID != "" || !failed.Retryable {
		t.Fatalf("failed import = %#v", failed)
	}
	if generator.calls != 1 {
		t.Fatalf("generator calls = %d", generator.calls)
	}
	transcriptText, err := service.GetTranscript(ctx, item.ID)
	if err != nil || !strings.Contains(transcriptText, "需要保留") {
		t.Fatalf("transcript=%q error=%v", transcriptText, err)
	}

	retrying, err := service.Retry(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retrying.Status != model.ContentImportStatusActive || retrying.Stage != model.ContentImportStageQueued {
		t.Fatalf("retrying import = %#v", retrying)
	}
	successfulGenerator := &scriptedGenerator{}
	retryWorker := NewWorker(store, resolver, client, successfulGenerator, "text-ai-retry-worker")
	if claimed, err := retryWorker.RunOne(context.Background()); err != nil || !claimed {
		t.Fatalf("retry RunOne() claimed=%v error=%v", claimed, err)
	}
	completed, err := service.Get(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.Notes().GetByID(ctx, completed.ResultNoteID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.ContentImportStatusCompleted || !strings.Contains(note.Body, "产品经理会从需求执行转向问题定义") {
		t.Fatalf("completed=%#v body=%s", completed, note.Body)
	}
	if transcriptFetches != 1 || successfulGenerator.calls != 1 {
		t.Fatalf("transcript fetches=%d generator calls=%d", transcriptFetches, successfulGenerator.calls)
	}
}

func TestWorkerDownloadsChunksAndTranscribesPublicAudio(t *testing.T) {
	store := openContentImportTestStore(t)
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace-1")
	resolver := fixedResolver{episode: &contentsource.Episode{
		SourceType: "apple", CanonicalURL: "https://podcasts.apple.com/podcast/show/id1?i=audio-1",
		ExternalID: "audio-1", Title: "长音频测试", AudioURL: "https://cdn.example/episode.mp3",
	}}
	service, _ := NewService(store, resolver)
	item, err := service.Create(ctx, uuid.NewString(), CreateRequest{SourceURL: "https://podcasts.apple.com/podcast/show/id1?i=audio-1", SummarizeWithAI: false})
	if err != nil {
		t.Fatal(err)
	}
	client := audioHTTPClient(t)
	transcriber := testsupport.NewScriptedTranscriber(
		testsupport.TranscriptionStep{Text: "第一段逐字稿。"},
		testsupport.TranscriptionStep{Text: "第二段逐字稿。"},
	)
	worker := NewWorker(store, resolver, client, &scriptedGenerator{}, "audio-worker")
	worker.AudioProcessor = fakeAudioProcessor{contents: [][]byte{[]byte("chunk-one"), []byte("chunk-two")}}
	worker.Transcriber = transcriber
	if claimed, err := worker.RunOne(context.Background()); err != nil || !claimed {
		t.Fatalf("RunOne() claimed=%v error=%v", claimed, err)
	}
	completed, err := service.Get(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.Notes().GetByID(ctx, completed.ResultNoteID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != model.ContentImportStatusCompleted || !strings.Contains(note.Body, "[00:00:00]") ||
		!strings.Contains(note.Body, "[00:15:00]") || !strings.Contains(note.Body, "第二段逐字稿") {
		t.Fatalf("completed=%#v body=%s", completed, note.Body)
	}
	if transcriber.CallCount() != 2 {
		t.Fatalf("transcription calls = %d", transcriber.CallCount())
	}
	provenance, err := service.GetByResultNoteID(ctx, completed.ResultNoteID)
	if err != nil || provenance.ID != item.ID || provenance.CanonicalURL != resolver.episode.CanonicalURL {
		t.Fatalf("provenance=%#v error=%v", provenance, err)
	}
	storedTranscript, err := service.GetTranscript(ctx, item.ID)
	if err != nil || !strings.Contains(storedTranscript, "第一段逐字稿") || !strings.Contains(storedTranscript, "第二段逐字稿") {
		t.Fatalf("transcript=%q error=%v", storedTranscript, err)
	}
}

func TestWorkerRetryReusesCompletedAudioChunks(t *testing.T) {
	store := openContentImportTestStore(t)
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace-1")
	resolver := fixedResolver{episode: &contentsource.Episode{
		SourceType: "xiaoyuzhou", CanonicalURL: "https://www.xiaoyuzhoufm.com/episode/audio-2",
		ExternalID: "audio-2", Title: "断点续转测试", AudioURL: "https://cdn.example/retry.mp3",
	}}
	service, _ := NewService(store, resolver)
	item, err := service.Create(ctx, uuid.NewString(), CreateRequest{SourceURL: "https://www.xiaoyuzhoufm.com/episode/audio-2", SummarizeWithAI: false})
	if err != nil {
		t.Fatal(err)
	}
	processor := fakeAudioProcessor{contents: [][]byte{[]byte("stable-one"), []byte("stable-two")}}
	firstTranscriber := testsupport.NewScriptedTranscriber(
		testsupport.TranscriptionStep{Text: "已经完成的第一段。"},
		testsupport.TranscriptionStep{Err: errors.New("provider unavailable")},
	)
	firstWorker := NewWorker(store, resolver, audioHTTPClient(t), nil, "retry-worker-1")
	firstWorker.AudioProcessor = processor
	firstWorker.Transcriber = firstTranscriber
	if _, err := firstWorker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	failed, _ := service.Get(ctx, item.ID)
	if failed.ErrorCode != "TRANSCRIPTION_FAILED" {
		t.Fatalf("failed import = %#v", failed)
	}
	if _, err := service.Retry(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	secondTranscriber := testsupport.NewScriptedTranscriber(testsupport.TranscriptionStep{Text: "重试完成的第二段。"})
	secondWorker := NewWorker(store, resolver, audioHTTPClient(t), nil, "retry-worker-2")
	secondWorker.AudioProcessor = processor
	secondWorker.Transcriber = secondTranscriber
	if _, err := secondWorker.RunOne(context.Background()); err != nil {
		t.Fatal(err)
	}
	completed, _ := service.Get(ctx, item.ID)
	note, err := store.Notes().GetByID(ctx, completed.ResultNoteID)
	if err != nil {
		t.Fatal(err)
	}
	if secondTranscriber.CallCount() != 1 || !strings.Contains(note.Body, "已经完成的第一段") || !strings.Contains(note.Body, "重试完成的第二段") {
		t.Fatalf("calls=%d body=%s", secondTranscriber.CallCount(), note.Body)
	}
}

func TestDeleteCompletedImportRemovesArtifactsAndPreservesResultNote(t *testing.T) {
	store := openContentImportTestStore(t)
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace-1")
	service, item, noteID := createCompletedContentImport(t, store, ctx)

	completed, err := service.Get(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !completed.ResultNoteAvailable {
		t.Fatal("completed import should report its result note as available")
	}
	if err := service.Delete(ctx, item.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, item.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted import error = %v, want sql.ErrNoRows", err)
	}
	repository, err := storage.ContentImportRepositoryFrom(store)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.GetArtifact(ctx, item.ID, "transcript_normalized"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted artifact error = %v, want sql.ErrNoRows", err)
	}
	if _, err := store.Notes().GetByID(ctx, noteID); err != nil {
		t.Fatalf("result note should be preserved: %v", err)
	}
}

func TestCompletedImportCanBeDeletedAfterResultNoteWasDeleted(t *testing.T) {
	store := openContentImportTestStore(t)
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace-1")
	service, item, noteID := createCompletedContentImport(t, store, ctx)

	if err := store.Notes().Delete(ctx, noteID); err != nil {
		t.Fatal(err)
	}
	completed, err := service.Get(ctx, item.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.ResultNoteAvailable {
		t.Fatal("completed import should report a deleted result note as unavailable")
	}
	if err := service.Delete(ctx, item.ID); err != nil {
		t.Fatalf("delete import after result note deletion: %v", err)
	}
}

func TestDeleteRejectsActiveImport(t *testing.T) {
	store := openContentImportTestStore(t)
	ctx := auth.ContextWithWorkspaceScope(context.Background(), "workspace-1")
	service, err := NewService(store, fixedResolver{episode: &contentsource.Episode{}})
	if err != nil {
		t.Fatal(err)
	}
	item, err := service.Create(ctx, uuid.NewString(), CreateRequest{
		SourceURL: "https://www.xiaoyuzhoufm.com/episode/active-delete-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, item.ID); !errors.Is(err, storage.ErrContentImportNotDeletable) {
		t.Fatalf("delete active import error = %v", err)
	}
	if _, err := service.Get(ctx, item.ID); err != nil {
		t.Fatalf("active import should remain after rejected delete: %v", err)
	}
}

func createCompletedContentImport(t *testing.T, store storage.Store, ctx context.Context) (*Service, *model.ContentImport, string) {
	t.Helper()
	service, err := NewService(store, fixedResolver{episode: &contentsource.Episode{}})
	if err != nil {
		t.Fatal(err)
	}
	note, err := store.Notes().Create(ctx, &model.CreateNoteRequest{
		Title: "播客整理笔记", FolderID: "__uncategorized", Tags: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	item, err := service.Create(ctx, uuid.NewString(), CreateRequest{
		SourceURL: "https://www.xiaoyuzhoufm.com/episode/" + uuid.NewString(),
	})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := storage.ContentImportRepositoryFrom(store)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := repository.ClaimNext(ctx, model.ClaimContentImport{
		WorkerID: "delete-test-worker", LeaseToken: uuid.NewString(), Now: 100, LeaseExpiresAt: 200,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := repository.PutArtifact(ctx, model.ContentImportArtifact{
		ImportID: item.ID, Kind: "transcript_normalized", Text: "待清理逐字稿", SHA256: "artifact-hash", CreatedAt: 100, UpdatedAt: 100,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.Complete(ctx, model.CompleteContentImport{
		ID: item.ID, LeaseToken: lease.LeaseToken, ResultNoteID: note.ID, Now: 101,
	}); err != nil {
		t.Fatal(err)
	}
	return service, item, note.ID
}

type fakeAudioProcessor struct{ contents [][]byte }

func (p fakeAudioProcessor) Split(_ context.Context, _ string, outputDir string) ([]podcastaudio.Chunk, error) {
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return nil, err
	}
	chunks := make([]podcastaudio.Chunk, 0, len(p.contents))
	for index, contents := range p.contents {
		path := filepath.Join(outputDir, "chunk-"+string(rune('a'+index))+".mp3")
		if err := os.WriteFile(path, contents, 0o600); err != nil {
			return nil, err
		}
		digest := sha256.Sum256(contents)
		chunks = append(chunks, podcastaudio.Chunk{
			Path: path, Filename: filepath.Base(path), ContentType: "audio/mpeg",
			Offset: time.Duration(index) * 15 * time.Minute, SHA256: hex.EncodeToString(digest[:]),
		})
	}
	return chunks, nil
}

func audioHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	return &http.Client{Transport: roundTrip(func(request *http.Request) (*http.Response, error) {
		body := "public podcast audio"
		return &http.Response{
			StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Request: request,
			ContentLength: int64(len(body)), Header: http.Header{"Content-Type": []string{"audio/mpeg"}},
		}, nil
	})}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func openContentImportTestStore(t *testing.T) storage.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flowspace.content-import.test.db")
	store, err := (storagesqlite.Provider{}).Open(t.Context(), storage.Config{Env: "test", Driver: storage.DriverSQLite, SQLitePath: path})
	if err != nil {
		t.Fatal(err)
	}
	authRepository := store.Auth()
	user := &model.User{ID: "user-1", Email: "content-import@example.test", DisplayName: "Import Test", PasswordHash: "test", PasswordSet: true, Role: "user", Status: "active"}
	if err := authRepository.CreateUser(t.Context(), user); err != nil {
		t.Fatal(err)
	}
	if err := authRepository.CreateWorkspace(t.Context(), &model.Workspace{ID: "workspace-1", Name: "Import Workspace", OwnerUserID: user.ID}); err != nil {
		t.Fatal(err)
	}
	if err := authRepository.AddWorkspaceMember(t.Context(), "workspace-1", user.ID, "owner"); err != nil {
		t.Fatal(err)
	}
	workspaceContext := auth.ContextWithWorkspaceScope(t.Context(), "workspace-1")
	if err := provisioning.EnsureDefaultWorkspaceData(workspaceContext, store); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}
