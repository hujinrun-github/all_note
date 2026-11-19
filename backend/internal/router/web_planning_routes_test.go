package router

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/planstore"
	"github.com/hujinrun/flowspace/internal/transcription"
	"github.com/hujinrun/flowspace/internal/webplanning"
)

type planningConfiguredAI struct{ endpoint string }

func (s planningConfiguredAI) LoadBinding(_ context.Context, _, kind string) (airuntime.Binding, error) {
	return airuntime.Binding{Kind: kind, Mode: "custom", EndpointID: kind}, nil
}
func (s planningConfiguredAI) LoadEndpointProfile(_ context.Context, _, kind, endpoint string) (airuntime.EndpointProfile, error) {
	model := "configured-planner"
	if kind == "llm_transcription" {
		model = "configured-asr"
	}
	config, _ := json.Marshal(map[string]string{"endpoint": s.endpoint, "model": model})
	return airuntime.EndpointProfile{EndpointID: endpoint, Kind: kind, ProfileVersionID: "configured-v1", Provider: "openai_compatible", ConfigJSON: string(config), Secret: []byte("configured-service-key")}, nil
}
func (s planningConfiguredAI) LoadFeatureSetting(_ context.Context, _, feature string) (airuntime.FeatureSetting, error) {
	return airuntime.FeatureSetting{Feature: feature, Enabled: true, FallbackMode: "error"}, nil
}

func TestWebPlanningRoutesRequireAuthenticationAndRuntime(t *testing.T) {
	env := setupRouterAuthEnv(t, false)
	createRouterSession(t, env, "planning-auth")
	routes := Setup(env.config)
	for _, authenticated := range []bool{false, true} {
		req := httptest.NewRequest(http.MethodGet, "/api/planning/capabilities", nil)
		if authenticated {
			req.AddCookie(&http.Cookie{Name: env.auth.Cookie.Name, Value: "planning-auth"})
		}
		response := httptest.NewRecorder()
		routes.ServeHTTP(response, req)
		want := http.StatusUnauthorized
		if authenticated {
			want = http.StatusServiceUnavailable
		}
		if response.Code != want {
			t.Fatalf("auth=%v status=%d body=%s", authenticated, response.Code, response.Body.String())
		}
	}
}

// This exercises real HTTP handlers, the configured provider adapters, separate
// identity/tenant stores, durable job recovery, and the atomic v2 commit. Only
// the external providers and object store are local test doubles.
func TestWebVoicePlanHTTPReusesConfiguredASRAndCommitsOnce(t *testing.T) {
	fixture := newWorkspaceQueryHTTPFixture(t)
	db, err := sql.Open("sqlite", fixture.tenantPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var initialNotes int
	if err := db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&initialNotes); err != nil {
		t.Fatal(err)
	}
	var asrCalls, chatCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer configured-service-key" {
			http.Error(w, "wrong configured credential", 401)
			return
		}
		switch r.URL.Path {
		case "/audio/transcriptions":
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				http.Error(w, "bad multipart", 400)
				return
			}
			defer r.MultipartForm.RemoveAll()
			file, header, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "missing audio", 400)
				return
			}
			defer file.Close()
			data, _ := io.ReadAll(file)
			if r.FormValue("model") != "configured-asr" || r.FormValue("language") != "zh" || header.Header.Get("Content-Type") != "audio/wav" || len(data) < 44 || string(data[:4]) != "RIFF" {
				http.Error(w, "wrong configured ASR request", 400)
				return
			}
			asrCalls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]string{"text": "明天八点整理资料，然后复习单词"})
		case "/chat/completions":
			var body struct {
				Model    string `json:"model"`
				Messages []struct {
					Content string `json:"content"`
				} `json:"messages"`
			}
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "configured-planner" || len(body.Messages) < 2 || !strings.Contains(body.Messages[1].Content, "九点") {
				http.Error(w, "planner did not receive corrected text", 400)
				return
			}
			chatCalls.Add(1)
			plan := `{"schema_version":1,"title":"明日安排","items":[{"id":"a","selected":true,"title":"整理资料","description":"确认具体日期时间后安排","priority":1,"schedule":{"recurrence_type":"none","timing_type":"unscheduled","timezone":"Asia/Shanghai"}},{"id":"b","selected":true,"title":"复习单词","description":"复习本周词汇","priority":0,"schedule":{"recurrence_type":"none","timing_type":"unscheduled","timezone":"Asia/Shanghai"}}],"clarifications":[],"assumptions":[]}`
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": plan}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer provider.Close()
	aiResolver, err := airuntime.NewResolver(planningConfiguredAI{provider.URL})
	if err != nil {
		t.Fatal(err)
	}
	asr, err := transcription.NewRuntimeTranscriber(aiResolver, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	chat, err := airuntime.NewGenerator(aiResolver, provider.Client())
	if err != nil {
		t.Fatal(err)
	}
	repository, err := planstore.New(fixture.env.config.TaskDomainV2Runtime.(planstore.RuntimeResolver))
	if err != nil {
		t.Fatal(err)
	}
	config := webplanning.Config{Repository: repository, Objects: objectstore.NewMemoryStore(), Transcriber: transcription.NewWebAudioTranscriber(asr), Chat: chat, Features: chat, ResolveTranscription: asr.CheckAvailable, ResolveGeneration: chat.CheckAvailable}
	service, err := webplanning.New(config)
	if err != nil {
		t.Fatal(err)
	}
	fixture.env.config.WebPlanning = service
	server := httptest.NewServer(Setup(fixture.env.config))
	defer server.Close()
	client, base, cookie := server.Client(), server.URL, fixture.primary
	caps := taskDomainV2SmokeRequest[webplanning.Capabilities](t, client, base, cookie, http.MethodGet, "/api/planning/capabilities", nil, http.StatusOK)
	if !caps.TranscriptionAvailable || !caps.GenerationAvailable {
		t.Fatalf("capabilities=%+v", caps)
	}
	created := taskDomainV2SmokeRequest[struct {
		Draft webplanning.Draft `json:"draft"`
	}](t, client, base, cookie, http.MethodPost, "/api/plan-drafts", map[string]any{"request_id": "http-create", "input_text": "", "timezone": "Asia/Shanghai", "project_id": "shared-project"}, http.StatusCreated).Draft
	path := "/api/plan-drafts/" + created.ID
	pcm := make([]byte, 44+32000)
	copy(pcm, "RIFF")
	binary.LittleEndian.PutUint32(pcm[4:], uint32(len(pcm)-8))
	copy(pcm[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(pcm[16:], 16)
	binary.LittleEndian.PutUint16(pcm[20:], 1)
	binary.LittleEndian.PutUint16(pcm[22:], 1)
	binary.LittleEndian.PutUint32(pcm[24:], 16000)
	binary.LittleEndian.PutUint32(pcm[28:], 32000)
	binary.LittleEndian.PutUint16(pcm[32:], 2)
	binary.LittleEndian.PutUint16(pcm[34:], 16)
	copy(pcm[36:], "data")
	binary.LittleEndian.PutUint32(pcm[40:], 32000)
	req, _ := http.NewRequest(http.MethodPut, base+path+"/audio?expected_revision=1", bytes.NewReader(pcm))
	req.AddCookie(cookie)
	req.Header.Set("Content-Type", "audio/wav")
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("upload=%d %s", response.StatusCode, raw)
	}
	var upload struct {
		Data struct {
			Draft webplanning.Draft `json:"draft"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &upload); err != nil {
		t.Fatal(err)
	}
	started := taskDomainV2SmokeRequest[webplanning.JobResult](t, client, base, cookie, http.MethodPost, path+"/transcribe", map[string]any{"request_id": "http-asr", "expected_revision": upload.Data.Draft.Revision}, http.StatusAccepted)
	// Reconstruct the service, as on restart, before running the persisted job.
	service, err = webplanning.New(config)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := service.RunWorkspace(context.Background(), routerTestWorkspaceID); err != nil || !claimed {
		t.Fatalf("ASR worker=%v %v", claimed, err)
	}
	transcribed := taskDomainV2SmokeRequest[struct {
		Draft webplanning.Draft `json:"draft"`
	}](t, client, base, cookie, http.MethodGet, path, nil, http.StatusOK).Draft
	if transcribed.InputText != "明天八点整理资料，然后复习单词" || transcribed.Transcript != transcribed.InputText {
		t.Fatalf("transcript=%+v", transcribed)
	}
	corrected := taskDomainV2SmokeRequest[struct {
		Draft webplanning.Draft `json:"draft"`
	}](t, client, base, cookie, http.MethodPatch, path, map[string]any{"expected_revision": transcribed.Revision, "input_text": "明天九点整理资料，然后复习单词"}, http.StatusOK).Draft
	taskDomainV2SmokeRequest[webplanning.JobResult](t, client, base, cookie, http.MethodPost, path+"/generate", map[string]any{"request_id": "http-generate", "expected_revision": corrected.Revision}, http.StatusAccepted)
	if claimed, err := service.RunWorkspace(context.Background(), routerTestWorkspaceID); err != nil || !claimed {
		t.Fatalf("generation worker=%v %v", claimed, err)
	}
	ready := taskDomainV2SmokeRequest[struct {
		Draft webplanning.Draft `json:"draft"`
	}](t, client, base, cookie, http.MethodGet, path, nil, http.StatusOK).Draft
	if ready.Status != "ready" || len(ready.Items) != 2 || ready.Transcript == ready.InputText {
		t.Fatalf("generated=%+v", ready)
	}
	validation := taskDomainV2SmokeRequest[webplanning.Validation](t, client, base, cookie, http.MethodPost, path+"/validate", map[string]any{"expected_revision": ready.Revision}, http.StatusOK)
	if len(validation.Issues) != 0 {
		t.Fatalf("validation=%+v", validation)
	}
	commit := map[string]any{"request_id": "http-commit", "expected_revision": ready.Revision, "publish": true, "allow_conflicts": false}
	for i := 0; i < 2; i++ {
		receipt := taskDomainV2SmokeRequest[struct {
			Receipt webplanning.Receipt `json:"receipt"`
		}](t, client, base, cookie, http.MethodPost, path+"/commit", commit, http.StatusOK).Receipt
		if len(receipt.TaskIDs) != 2 {
			t.Fatalf("receipt=%+v", receipt)
		}
	}
	for _, target := range []string{path, path + "/audio", "/api/plan-jobs/" + started.Job.ID} {
		taskDomainV2SmokeRequest[map[string]any](t, client, base, fixture.secondary, http.MethodGet, target, nil, http.StatusNotFound)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM domain_tasks_v2 WHERE workspace_id=? AND title IN ('整理资料','复习单词') AND lifecycle_status='active'`, routerTestWorkspaceID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("published count=%d err=%v", count, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM notes`).Scan(&count); err != nil || count != initialNotes {
		t.Fatalf("voice plan polluted notes: %d %v", count, err)
	}
	if asrCalls.Load() != 1 || chatCalls.Load() != 1 {
		t.Fatalf("provider calls asr=%d chat=%d", asrCalls.Load(), chatCalls.Load())
	}
}
