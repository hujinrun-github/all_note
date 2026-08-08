package contentimport

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/airuntime"
	"github.com/hujinrun/flowspace/internal/auth"
	"github.com/hujinrun/flowspace/internal/contentsource"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/podcastaudio"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/transcript"
	"github.com/hujinrun/flowspace/internal/transcription"
)

const (
	maxTranscriptBytes   = 12 << 20
	defaultMaxAudioBytes = 512 << 20
)

type Generator interface {
	Generate(context.Context, string, string, string) (string, error)
}

type Worker struct {
	Store                storage.Store
	Resolver             contentsource.Resolver
	HTTP                 *http.Client
	Generator            Generator
	Transcriber          transcription.Transcriber
	AudioProcessor       podcastaudio.Processor
	WorkerID             string
	Now                  func() time.Time
	NewLeaseToken        func() string
	LeaseDuration        time.Duration
	HeartbeatInterval    time.Duration
	TranscriptionTimeout time.Duration
	MaxAudioBytes        int64
}

func NewWorker(store storage.Store, resolver contentsource.Resolver, httpClient *http.Client, generator Generator, workerID string) Worker {
	return Worker{
		Store: store, Resolver: resolver, HTTP: httpClient, Generator: generator,
		Transcriber: transcription.UnavailableTranscriber{}, AudioProcessor: podcastaudio.NewFFmpegProcessor(), WorkerID: workerID,
		Now: time.Now, NewLeaseToken: uuid.NewString, LeaseDuration: 10 * time.Minute,
		HeartbeatInterval: 2 * time.Minute, TranscriptionTimeout: 10 * time.Minute, MaxAudioBytes: defaultMaxAudioBytes,
	}
}

func (w Worker) Run(ctx context.Context, idleDelay time.Duration, onError func(error)) {
	if idleDelay <= 0 {
		idleDelay = time.Second
	}
	for {
		claimed, err := w.RunOne(ctx)
		if err != nil && onError != nil {
			onError(err)
		}
		if ctx.Err() != nil {
			return
		}
		if claimed && err == nil {
			continue
		}
		timer := time.NewTimer(idleDelay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
	}
}

func (w Worker) RunOne(ctx context.Context) (bool, error) {
	if w.Store == nil || w.Resolver == nil || w.HTTP == nil || w.WorkerID == "" || w.Now == nil || w.NewLeaseToken == nil || w.LeaseDuration <= 0 || w.HeartbeatInterval <= 0 || w.MaxAudioBytes <= 0 {
		return false, errors.New("content import worker dependencies are not configured")
	}
	repository, err := storage.ContentImportRepositoryFrom(w.Store)
	if err != nil {
		return false, err
	}
	now := w.Now().UTC()
	lease, err := repository.ClaimNext(ctx, model.ClaimContentImport{WorkerID: w.WorkerID, LeaseToken: w.NewLeaseToken(), Now: now.Unix(), LeaseExpiresAt: now.Add(w.LeaseDuration).Unix()})
	if errors.Is(err, storage.ErrNoContentImport) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	jobCtx := auth.ContextWithWorkspaceScope(ctx, lease.WorkspaceID)
	item := lease.Import

	if item.TranscriptURL == "" && item.Stage != model.ContentImportStageSummarizing && item.Stage != model.ContentImportStagePublishing {
		episode, resolveErr := w.Resolver.Resolve(jobCtx, item.SourceURL)
		if resolveErr != nil {
			return true, w.fail(jobCtx, repository, lease, "SOURCE_RESOLUTION_FAILED", publicSourceError(resolveErr))
		}
		itemPtr, updateErr := repository.UpdateResolved(jobCtx, model.UpdateContentImportResolved{
			ID: item.ID, LeaseToken: lease.LeaseToken, SourceType: episode.SourceType, CanonicalURL: episode.CanonicalURL,
			ExternalID: episode.ExternalID, FeedURL: episode.FeedURL, Title: episode.Title, PodcastTitle: episode.PodcastTitle,
			CoverURL: episode.CoverURL, Description: episode.Description, DurationSeconds: episode.DurationSeconds,
			TranscriptURL: episode.TranscriptURL, AudioURL: episode.AudioURL, Now: w.Now().UTC().Unix(),
		})
		if updateErr != nil {
			return true, fmt.Errorf("persist resolved episode: %w", updateErr)
		}
		item = *itemPtr
	}

	artifact, artifactErr := repository.GetArtifact(jobCtx, item.ID, "transcript_normalized")
	if artifactErr != nil && !errors.Is(artifactErr, sql.ErrNoRows) {
		return true, fmt.Errorf("load normalized transcript: %w", artifactErr)
	}
	if artifact == nil || artifact.Text == "" {
		var text string
		if item.TranscriptURL != "" {
			var fetchErr error
			text, fetchErr = w.fetchTranscript(jobCtx, item.TranscriptURL)
			if fetchErr != nil {
				return true, w.fail(jobCtx, repository, lease, "TRANSCRIPT_FETCH_FAILED", "公开逐字稿暂时无法读取")
			}
		} else {
			var failure *acquisitionFailure
			var acquireErr error
			text, failure, acquireErr = w.transcribePublicAudioWithHeartbeat(jobCtx, repository, lease, item)
			if acquireErr != nil {
				return true, acquireErr
			}
			if failure != nil {
				return true, w.fail(jobCtx, repository, lease, failure.Code, failure.Message)
			}
		}
		normalized, normalizeErr := transcript.Normalize(text)
		if normalizeErr != nil {
			return true, w.fail(jobCtx, repository, lease, "TRANSCRIPT_INVALID", "公开逐字稿内容为空或格式不受支持")
		}
		digest := sha256.Sum256([]byte(normalized))
		artifact = &model.ContentImportArtifact{ImportID: item.ID, Kind: "transcript_normalized", Text: normalized,
			SHA256: hex.EncodeToString(digest[:]), CreatedAt: w.Now().UTC().Unix(), UpdatedAt: w.Now().UTC().Unix()}
		if err := repository.PutArtifact(jobCtx, *artifact); err != nil {
			return true, fmt.Errorf("store normalized transcript: %w", err)
		}
	}

	markdown := ""
	if item.SummarizeWithAI {
		if _, err := repository.UpdateStage(jobCtx, model.UpdateContentImportStage{ID: item.ID, LeaseToken: lease.LeaseToken, Stage: model.ContentImportStageSummarizing, Progress: 68, Now: w.Now().UTC().Unix()}); err != nil {
			return true, fmt.Errorf("advance content import to summarizing: %w", err)
		}
		var generationFailure *acquisitionFailure
		markdown, generationFailure = w.generateStructuredNote(jobCtx, lease.WorkspaceID, item, artifact.Text)
		if generationFailure != nil {
			return true, w.fail(jobCtx, repository, lease, generationFailure.Code, generationFailure.Message)
		}
	} else {
		markdown = renderTranscriptNote(item, artifact.Text)
	}
	if _, err := repository.UpdateStage(jobCtx, model.UpdateContentImportStage{ID: item.ID, LeaseToken: lease.LeaseToken, Stage: model.ContentImportStagePublishing, Progress: 90, Now: w.Now().UTC().Unix()}); err != nil {
		return true, fmt.Errorf("advance content import to publishing: %w", err)
	}

	var resultNoteID string
	if err := w.Store.Transact(jobCtx, func(tx storage.Store) error {
		folderID := item.FolderID
		if folderID == "" {
			folderID = "__uncategorized"
		}
		note, err := tx.Notes().Create(jobCtx, &model.CreateNoteRequest{Title: noteTitle(item), Body: markdown,
			FolderID: folderID, Tags: encodeTags(item.Tags), ProjectIDs: item.ProjectIDs})
		if err != nil {
			return fmt.Errorf("create imported note: %w", err)
		}
		txRepository, err := storage.ContentImportRepositoryFrom(tx)
		if err != nil {
			return err
		}
		if _, err := txRepository.Complete(jobCtx, model.CompleteContentImport{ID: item.ID, LeaseToken: lease.LeaseToken, ResultNoteID: note.ID, Now: w.Now().UTC().Unix()}); err != nil {
			return fmt.Errorf("complete content import: %w", err)
		}
		resultNoteID = note.ID
		return nil
	}); err != nil {
		return true, err
	}
	_ = resultNoteID
	return true, nil
}

type acquisitionFailure struct {
	Code    string
	Message string
}

func (w Worker) transcribePublicAudioWithHeartbeat(
	ctx context.Context,
	repository storage.ContentImportRepository,
	lease *model.ContentImportLease,
	item model.ContentImport,
) (string, *acquisitionFailure, error) {
	operationCtx, cancel := context.WithCancel(ctx)
	heartbeatDone := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(w.HeartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-operationCtx.Done():
				heartbeatDone <- nil
				return
			case <-ticker.C:
				if operationCtx.Err() != nil {
					heartbeatDone <- nil
					return
				}
				now := w.Now().UTC()
				if err := repository.Heartbeat(operationCtx, model.HeartbeatContentImport{
					ID: item.ID, LeaseToken: lease.LeaseToken, Now: now.Unix(), LeaseExpiresAt: now.Add(w.LeaseDuration).Unix(),
				}); err != nil {
					if operationCtx.Err() != nil && errors.Is(err, context.Canceled) {
						heartbeatDone <- nil
						return
					}
					cancel()
					heartbeatDone <- err
					return
				}
			}
		}
	}()
	text, failure, operationErr := w.transcribePublicAudio(operationCtx, repository, lease, item)
	cancel()
	heartbeatErr := <-heartbeatDone
	if heartbeatErr != nil {
		return "", nil, fmt.Errorf("heartbeat content import: %w", heartbeatErr)
	}
	return text, failure, operationErr
}

func (w Worker) transcribePublicAudio(
	ctx context.Context,
	repository storage.ContentImportRepository,
	lease *model.ContentImportLease,
	item model.ContentImport,
) (string, *acquisitionFailure, error) {
	if strings.TrimSpace(item.AudioURL) == "" {
		return "", &acquisitionFailure{Code: "SOURCE_MEDIA_UNAVAILABLE", Message: "该单集没有公开逐字稿，也没有可公开读取的音频地址"}, nil
	}
	if w.Transcriber == nil {
		return "", &acquisitionFailure{Code: "TRANSCRIPTION_UNAVAILABLE", Message: "当前 workspace 尚未配置语音转写服务"}, nil
	}
	if w.AudioProcessor == nil {
		return "", &acquisitionFailure{Code: "AUDIO_PROCESSING_UNAVAILABLE", Message: "服务端尚未配置长音频处理能力"}, nil
	}
	temporary, err := os.MkdirTemp("", "flowspace-podcast-import-")
	if err != nil {
		return "", nil, fmt.Errorf("create podcast import directory: %w", err)
	}
	defer os.RemoveAll(temporary)

	inputPath, failure, err := w.downloadAudio(ctx, item.AudioURL, temporary)
	if err != nil || failure != nil {
		return "", failure, err
	}
	chunks, err := w.AudioProcessor.Split(ctx, inputPath, filepath.Join(temporary, "chunks"))
	if err != nil {
		if errors.Is(err, podcastaudio.ErrProcessorUnavailable) {
			return "", &acquisitionFailure{Code: "AUDIO_PROCESSING_UNAVAILABLE", Message: "服务端需要安装 ffmpeg 才能处理长音频"}, nil
		}
		return "", &acquisitionFailure{Code: "AUDIO_PROCESSING_FAILED", Message: "公开音频无法解码或切片"}, nil
	}
	if len(chunks) == 0 {
		return "", &acquisitionFailure{Code: "AUDIO_PROCESSING_FAILED", Message: "公开音频没有可转写的音轨"}, nil
	}

	segments := make([]string, 0, len(chunks))
	for index, chunk := range chunks {
		kind := fmt.Sprintf("transcript_chunk_%04d", index)
		chunkText := ""
		cached, cacheErr := repository.GetArtifact(ctx, item.ID, kind)
		if cacheErr != nil && !errors.Is(cacheErr, sql.ErrNoRows) {
			return "", nil, fmt.Errorf("load transcript chunk %d: %w", index, cacheErr)
		}
		if cached != nil && cached.Text != "" && cached.SHA256 == chunk.SHA256 {
			chunkText = cached.Text
		} else {
			file, openErr := os.Open(chunk.Path)
			if openErr != nil {
				return "", nil, fmt.Errorf("open podcast audio chunk %d: %w", index, openErr)
			}
			chunkText, err = w.Transcriber.Transcribe(ctx, transcription.Input{
				Audio: file, Filename: chunk.Filename, ContentType: chunk.ContentType,
				Language: item.Language, Timeout: w.TranscriptionTimeout,
			})
			closeErr := file.Close()
			if err != nil {
				if errors.Is(err, transcription.ErrUnavailable) {
					return "", &acquisitionFailure{Code: "TRANSCRIPTION_UNAVAILABLE", Message: "当前 workspace 尚未配置语音转写服务"}, nil
				}
				return "", &acquisitionFailure{Code: "TRANSCRIPTION_FAILED", Message: fmt.Sprintf("音频第 %d/%d 段转写失败，可稍后重试", index+1, len(chunks))}, nil
			}
			if closeErr != nil {
				return "", nil, fmt.Errorf("close podcast audio chunk %d: %w", index, closeErr)
			}
			chunkText, err = transcript.Normalize(chunkText)
			if err != nil {
				return "", &acquisitionFailure{Code: "TRANSCRIPTION_EMPTY", Message: fmt.Sprintf("音频第 %d/%d 段没有识别出文字", index+1, len(chunks))}, nil
			}
			now := w.Now().UTC().Unix()
			if err := repository.PutArtifact(ctx, model.ContentImportArtifact{
				ImportID: item.ID, Kind: kind, Text: chunkText, SHA256: chunk.SHA256, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return "", nil, fmt.Errorf("store transcript chunk %d: %w", index, err)
			}
		}
		segments = append(segments, fmt.Sprintf("[%s]\n%s", formatOffset(chunk.Offset), chunkText))
		progress := 42 + ((index + 1) * 23 / len(chunks))
		if _, err := repository.UpdateStage(ctx, model.UpdateContentImportStage{
			ID: item.ID, LeaseToken: lease.LeaseToken, Stage: model.ContentImportStageAcquiring, Progress: progress, Now: w.Now().UTC().Unix(),
		}); err != nil {
			return "", nil, fmt.Errorf("record podcast transcription progress: %w", err)
		}
	}
	return strings.Join(segments, "\n\n"), nil, nil
}

func (w Worker) downloadAudio(ctx context.Context, rawURL, temporary string) (string, *acquisitionFailure, error) {
	parsedURL, err := parsePublicMediaURL(rawURL)
	if err != nil {
		return "", &acquisitionFailure{Code: "AUDIO_FETCH_FAILED", Message: "公开音频地址无效"}, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return "", &acquisitionFailure{Code: "AUDIO_FETCH_FAILED", Message: "公开音频地址无效"}, nil
	}
	request.Header.Set("Accept", "audio/*, application/octet-stream;q=0.8")
	request.Header.Set("User-Agent", "FlowSpace/0.2 (+podcast import)")
	response, err := w.HTTP.Do(request)
	if err != nil {
		return "", &acquisitionFailure{Code: "AUDIO_FETCH_FAILED", Message: "公开音频暂时无法下载"}, nil
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &acquisitionFailure{Code: "AUDIO_FETCH_FAILED", Message: "公开音频暂时无法下载"}, nil
	}
	if response.ContentLength > w.MaxAudioBytes {
		return "", &acquisitionFailure{Code: "AUDIO_TOO_LARGE", Message: "音频超过当前 512 MB 导入上限"}, nil
	}
	contentType := strings.TrimSpace(response.Header.Get("Content-Type"))
	if parsedType, _, parseErr := mime.ParseMediaType(contentType); parseErr == nil {
		contentType = parsedType
	}
	if !supportedRemoteAudioType(contentType) {
		return "", &acquisitionFailure{Code: "AUDIO_TYPE_UNSUPPORTED", Message: "音频地址返回了不受支持的媒体类型"}, nil
	}
	path := filepath.Join(temporary, "source"+audioExtension(response.Request.URL))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("create podcast audio file: %w", err)
	}
	written, copyErr := io.Copy(file, io.LimitReader(response.Body, w.MaxAudioBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return "", &acquisitionFailure{Code: "AUDIO_FETCH_FAILED", Message: "公开音频下载中断，可稍后重试"}, nil
	}
	if closeErr != nil {
		return "", nil, fmt.Errorf("close podcast audio file: %w", closeErr)
	}
	if written == 0 {
		return "", &acquisitionFailure{Code: "AUDIO_FETCH_FAILED", Message: "公开音频内容为空"}, nil
	}
	if written > w.MaxAudioBytes {
		return "", &acquisitionFailure{Code: "AUDIO_TOO_LARGE", Message: "音频超过当前 512 MB 导入上限"}, nil
	}
	return path, nil, nil
}

func supportedRemoteAudioType(contentType string) bool {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	return contentType == "" || strings.HasPrefix(contentType, "audio/") || contentType == "video/mp4" ||
		contentType == "application/octet-stream" || contentType == "binary/octet-stream"
}

func audioExtension(source *url.URL) string {
	if source != nil {
		extension := strings.ToLower(filepath.Ext(source.Path))
		switch extension {
		case ".mp3", ".m4a", ".mp4", ".aac", ".wav", ".ogg", ".opus", ".flac":
			return extension
		}
	}
	return ".audio"
}

func formatOffset(offset time.Duration) string {
	total := int64(offset / time.Second)
	return fmt.Sprintf("%02d:%02d:%02d", total/3600, (total%3600)/60, total%60)
}

func (w Worker) fetchTranscript(ctx context.Context, rawURL string) (string, error) {
	parsedURL, err := parsePublicMediaURL(rawURL)
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
	if err != nil {
		return "", err
	}
	request.Header.Set("Accept", "text/vtt, application/x-subrip, text/plain, text/*;q=0.8")
	request.Header.Set("User-Agent", "FlowSpace/0.2 (+podcast import)")
	response, err := w.HTTP.Do(request)
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 || response.ContentLength > maxTranscriptBytes {
		return "", errors.New("transcript response unavailable")
	}
	contents, err := io.ReadAll(io.LimitReader(response.Body, maxTranscriptBytes+1))
	if err != nil || len(contents) > maxTranscriptBytes {
		return "", errors.New("transcript exceeds limit")
	}
	return string(contents), nil
}

func parsePublicMediaURL(rawURL string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, contentsource.ErrInvalidURL
	}
	return parsed, nil
}

func (w Worker) generateStructuredNote(ctx context.Context, workspaceID string, item model.ContentImport, transcriptText string) (string, *acquisitionFailure) {
	if w.Generator == nil {
		return "", &acquisitionFailure{
			Code:    "TEXT_AI_UNAVAILABLE",
			Message: "当前工作区尚未配置文本 AI，请先在设置的 AI 服务中完成配置后重试",
		}
	}
	input := transcriptText
	if len(input) > 120000 {
		input = input[:120000]
	}
	systemPrompt := strings.TrimSpace(item.SummaryPrompt)
	if systemPrompt == "" {
		systemPrompt = `你是严谨的播客笔记编辑。只根据逐字稿提炼，不补充外部事实。返回 JSON 对象：title、summary、key_points、chapters、action_items。key_points、chapters、action_items 必须是字符串数组。`
	}
	userPrompt := fmt.Sprintf("节目：%s\n单集：%s\n\n逐字稿：\n%s", item.PodcastTitle, item.Title, input)
	generated, err := w.Generator.Generate(ctx, workspaceID, systemPrompt, userPrompt)
	if err != nil {
		if errors.Is(err, airuntime.ErrCapabilityDisabled) || errors.Is(err, airuntime.ErrConfigurationUnavailable) {
			return "", &acquisitionFailure{
				Code:    "TEXT_AI_UNAVAILABLE",
				Message: "当前工作区的文本 AI 不可用，请在设置中完成配置后重试",
			}
		}
		return "", &acquisitionFailure{
			Code:    "TEXT_AI_CALL_FAILED",
			Message: "文本 AI 调用失败，逐字稿已保留，可直接重试 AI 整理",
		}
	}
	var result struct {
		Title       string   `json:"title"`
		Summary     string   `json:"summary"`
		KeyPoints   []string `json:"key_points"`
		Chapters    []string `json:"chapters"`
		ActionItems []string `json:"action_items"`
	}
	cleaned := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(generated), "```json"), "```"))
	if json.Unmarshal([]byte(cleaned), &result) != nil || strings.TrimSpace(result.Summary) == "" {
		return "", &acquisitionFailure{
			Code:    "IMPORT_OUTPUT_INVALID",
			Message: "文本 AI 返回的笔记格式不完整，请重试",
		}
	}
	var body strings.Builder
	fmt.Fprintf(&body, "> 来源：[%s](%s)\n\n", sourceLabel(item), item.CanonicalURL)
	fmt.Fprintf(&body, "## 摘要\n\n%s\n\n", result.Summary)
	writeList(&body, "核心观点", result.KeyPoints)
	writeList(&body, "章节", result.Chapters)
	writeList(&body, "行动项", result.ActionItems)
	if item.IncludeTranscript {
		fmt.Fprintf(&body, "## 完整逐字稿\n\n%s\n", transcriptText)
	}
	return body.String(), nil
}

func (w Worker) fail(ctx context.Context, repository storage.ContentImportRepository, lease *model.ContentImportLease, code, message string) error {
	_, err := repository.Fail(ctx, model.FailContentImport{ID: lease.Import.ID, LeaseToken: lease.LeaseToken,
		Status: model.ContentImportStatusFailed, ErrorCode: code, ErrorMessage: message, Now: w.Now().UTC().Unix()})
	if err != nil {
		return fmt.Errorf("fail content import %s: %w", code, err)
	}
	return nil
}

func renderTranscriptNote(item model.ContentImport, transcriptText string) string {
	return fmt.Sprintf("> 来源：[%s](%s)\n\n## 单集简介\n\n%s\n\n## 完整逐字稿\n\n%s\n", sourceLabel(item), item.CanonicalURL, item.Description, transcriptText)
}

func noteTitle(item model.ContentImport) string {
	title := strings.TrimSpace(item.Title)
	if title == "" {
		title = "播客逐字稿"
	}
	if item.SummarizeWithAI {
		return title + "｜播客笔记"
	}
	return title + "｜完整逐字稿"
}

func sourceLabel(item model.ContentImport) string {
	if item.PodcastTitle == "" {
		return item.Title
	}
	return item.PodcastTitle + " · " + item.Title
}

func encodeTags(tags []string) string { encoded, _ := json.Marshal(tags); return string(encoded) }
func writeList(body *strings.Builder, title string, values []string) {
	if len(values) == 0 {
		return
	}
	fmt.Fprintf(body, "## %s\n\n", title)
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			fmt.Fprintf(body, "- %s\n", strings.TrimSpace(value))
		}
	}
	body.WriteString("\n")
}

func publicSourceError(err error) string {
	switch {
	case errors.Is(err, contentsource.ErrEpisodeRequired):
		return "请粘贴具体单集链接，而不是节目主页"
	case errors.Is(err, contentsource.ErrUnsupportedSource):
		return "目前仅支持小宇宙和 Apple Podcasts 单集链接"
	case errors.Is(err, contentsource.ErrInvalidURL):
		return "链接格式不正确"
	default:
		return "来源页面暂时无法解析"
	}
}
