package mobilev2service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/mobilev2contract"
	"github.com/hujinrun/flowspace/internal/mobilev2projection"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskruntime"
)

const (
	defaultPageSize    = 200
	defaultSnapshotTTL = 15 * time.Minute
)

type RuntimeResolver interface {
	ResolveMobileRuntime(context.Context, string) (taskruntime.MobileRuntimeSnapshot, error)
}

type CommandService interface {
	ApplyCommand(context.Context, handler.MobileV2CommandRequest) (any, error)
	Receipt(context.Context, handler.MobileV2ReceiptRequest) (any, error)
}

type Config struct {
	Runtime      RuntimeResolver
	Commands     CommandService
	TokenSecret  string
	MinimumBuild int
	PageSize     int
	SnapshotTTL  time.Duration
	Now          func() time.Time
}

type Service struct {
	runtime      RuntimeResolver
	commands     CommandService
	tokens       mobilev2sync.TokenCodec
	minimumBuild int
	pageSize     int
	snapshotTTL  time.Duration
	now          func() time.Time
}

func New(config Config) (*Service, error) {
	if config.Runtime == nil || config.Commands == nil || strings.TrimSpace(config.TokenSecret) == "" {
		return nil, errors.New("mobile-v2 runtime, command service, and token secret are required")
	}
	if config.MinimumBuild < 1 {
		config.MinimumBuild = 1
	}
	if config.PageSize == 0 {
		config.PageSize = defaultPageSize
	}
	if config.PageSize < 1 || config.PageSize > 1000 {
		return nil, errors.New("mobile-v2 page size must be between 1 and 1000")
	}
	if config.SnapshotTTL == 0 {
		config.SnapshotTTL = defaultSnapshotTTL
	}
	if config.SnapshotTTL < time.Minute {
		return nil, errors.New("mobile-v2 snapshot TTL must be at least one minute")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &Service{
		runtime: config.Runtime, commands: config.Commands,
		tokens:       mobilev2sync.NewTokenCodec(config.TokenSecret),
		minimumBuild: config.MinimumBuild, pageSize: config.PageSize,
		snapshotTTL: config.SnapshotTTL, now: config.Now,
	}, nil
}

type scopeCapability struct {
	Name                       mobilev2sync.ScopeName `json:"name"`
	Membership                 string                 `json:"membership"`
	ProjectionTimeZoneRequired bool                   `json:"projection_time_zone_required"`
}

type capabilitiesResponse struct {
	SchemaVersion       string            `json:"schema_version"`
	ContractSHA256      string            `json:"contract_sha256"`
	WorkspaceID         string            `json:"workspace_id"`
	WorkspaceMode       string            `json:"workspace_mode"`
	ServerCutoverEpoch  string            `json:"server_cutover_epoch"`
	MobileContractEpoch string            `json:"mobile_contract_epoch"`
	RuntimeEpoch        string            `json:"runtime_epoch"`
	TaskModelVersion    int               `json:"task_model_version"`
	MinimumClientBuild  int               `json:"minimum_client_build"`
	SyncScopes          []scopeCapability `json:"sync_scopes"`
	Features            map[string]bool   `json:"features"`
}

func (service *Service) Capabilities(ctx context.Context, identity handler.MobileV2Identity) (any, error) {
	runtime, err := service.resolve(ctx, identity)
	if err != nil {
		return nil, err
	}
	epoch := strconv.FormatInt(runtime.Epoch, 10)
	return capabilitiesResponse{
		SchemaVersion: "mobile-v2", ContractSHA256: mobilev2contract.ContractSHA256,
		WorkspaceID: identity.WorkspaceID, WorkspaceMode: "v2-active",
		ServerCutoverEpoch: epoch, MobileContractEpoch: mobilev2contract.ContractEpoch,
		RuntimeEpoch: epoch, TaskModelVersion: 2, MinimumClientBuild: service.minimumBuild,
		SyncScopes: []scopeCapability{
			{Name: mobilev2sync.ScopeIPhoneContent, Membership: "stable"},
			{Name: mobilev2sync.ScopeIPhoneTaskCore, Membership: "stable"},
			{Name: mobilev2sync.ScopeIPhoneOccurrenceWindow, Membership: "sliding", ProjectionTimeZoneRequired: true},
			{Name: mobilev2sync.ScopeWatchOccurrenceWindow, Membership: "sliding", ProjectionTimeZoneRequired: true},
		},
		Features: map[string]bool{
			"content_commands": true, "task_commands": true,
			"roadmap_read": true, "watch_occurrence_commands": true,
			"voice_upload": true, "transcription_jobs": true,
		},
	}, nil
}

type scopeView struct {
	Binding         mobilev2sync.TokenBinding
	ProjectionAsOf  time.Time
	ScopeValidUntil *time.Time
	WindowStart     time.Time
	WindowEnd       time.Time
	WindowStartDate string
	WindowEndDate   string
}

type snapshotResponse struct {
	SnapshotID              string          `json:"snapshot_id"`
	AsOfSequence            string          `json:"as_of_sequence"`
	SnapshotCursor          string          `json:"snapshot_cursor"`
	ProjectionAsOf          string          `json:"projection_as_of"`
	ProjectionTimeZone      *string         `json:"projection_time_zone"`
	ScopeGeneration         string          `json:"scope_generation"`
	ScopeValidUntil         *string         `json:"scope_valid_until"`
	ProjectionRefreshNeeded bool            `json:"projection_refresh_required"`
	Entities                json.RawMessage `json:"entities"`
	PageIndex               int             `json:"page_index"`
	PageChecksum            string          `json:"page_checksum"`
	ManifestChecksum        string          `json:"snapshot_manifest_checksum"`
	NextPageToken           *string         `json:"next_page_token"`
	HasMore                 bool            `json:"has_more"`
}

func (service *Service) Snapshot(ctx context.Context, request handler.MobileV2SnapshotRequest) (any, error) {
	runtime, err := service.resolve(ctx, request.Identity)
	if err != nil {
		return nil, err
	}
	view, err := service.scopeView(runtime, request.Scope, request.ProjectionTimeZone)
	if err != nil {
		return nil, err
	}
	repository, dialect, err := service.repository(runtime)
	if err != nil {
		return nil, err
	}
	now := service.now().UTC()
	if request.PageToken == "" {
		expiresAt := now.Add(service.snapshotTTL)
		if view.ScopeValidUntil != nil && expiresAt.After(*view.ScopeValidUntil) {
			expiresAt = *view.ScopeValidUntil
		}
		page, err := repository.CreateSnapshot(ctx, mobilev2sync.CreateSnapshotInput{
			Binding: view.Binding, ProjectionAsOf: view.ProjectionAsOf,
			ScopeValidUntil: view.ScopeValidUntil, ExpiresAt: expiresAt, PageSize: service.pageSize,
		}, func(ctx context.Context, tx *sql.Tx, sequence uint64) ([]json.RawMessage, error) {
			return mobilev2projection.Project(ctx, tx, dialect, mobilev2projection.Projection{
				WorkspaceID: request.Identity.WorkspaceID, Scope: view.Binding.Scope,
				AsOf: view.ProjectionAsOf, WindowStart: view.WindowStart, WindowEnd: view.WindowEnd,
				WindowStartDate: view.WindowStartDate, WindowEndDate: view.WindowEndDate, Sequence: sequence,
			})
		})
		if err != nil {
			return nil, err
		}
		return service.snapshotPage(page, view.Binding), nil
	}
	token, err := service.tokens.DecodeSnapshotPage(request.PageToken, view.Binding)
	if err != nil || now.Unix() >= token.ExpiresAt {
		return nil, resyncRequired("mobile-v2 snapshot page token is invalid or expired")
	}
	page, err := repository.ReadSnapshotPage(ctx, view.Binding, token.SnapshotID, token.PageIndex, now)
	if err != nil {
		if errors.Is(err, mobilev2sync.ErrSnapshotExpired) || errors.Is(err, mobilev2sync.ErrSnapshotMismatch) {
			return nil, resyncRequired("mobile-v2 snapshot session is unavailable")
		}
		return nil, err
	}
	if page.AsOfSequence != token.AsOfSequence || page.SnapshotCursor != token.SnapshotCursor {
		return nil, resyncRequired("mobile-v2 snapshot metadata no longer matches the page token")
	}
	return service.snapshotPage(page, view.Binding), nil
}

func (service *Service) snapshotPage(page mobilev2sync.StoredSnapshotPage, binding mobilev2sync.TokenBinding) snapshotResponse {
	hasMore := page.PageIndex+1 < page.PageCount
	var nextPageToken *string
	if hasMore {
		token, err := service.tokens.EncodeSnapshotPage(mobilev2sync.SnapshotPageToken{
			Binding: binding, SnapshotID: page.SnapshotID, AsOfSequence: page.AsOfSequence,
			SnapshotCursor: page.SnapshotCursor, PageIndex: page.PageIndex + 1, ExpiresAt: page.ExpiresAt.Unix(),
		})
		if err == nil {
			nextPageToken = &token
		}
	}
	return snapshotResponse{
		SnapshotID: page.SnapshotID, AsOfSequence: page.AsOfSequence, SnapshotCursor: page.SnapshotCursor,
		ProjectionAsOf: formatInstant(page.ProjectionAsOf), ProjectionTimeZone: page.ProjectionTimeZone,
		ScopeGeneration: page.ScopeGeneration, ScopeValidUntil: formatOptionalInstant(page.ScopeValidUntil),
		Entities: page.EntitiesJSON, PageIndex: page.PageIndex, PageChecksum: page.PageChecksum,
		ManifestChecksum: page.SnapshotManifestChecksum, NextPageToken: nextPageToken, HasMore: hasMore,
	}
}

type changeBatchResponse struct {
	ChangeSequence       string          `json:"change_sequence"`
	CausedByCommandID    *string         `json:"caused_by_command_id"`
	OriginDeviceClientID *string         `json:"origin_device_client_id"`
	Receipt              json.RawMessage `json:"receipt"`
	Entities             json.RawMessage `json:"entities"`
}

type changesResponse struct {
	Changes                 []changeBatchResponse `json:"changes"`
	NextCursor              string                `json:"next_cursor"`
	HasMore                 bool                  `json:"has_more"`
	ProjectionTimeZone      *string               `json:"projection_time_zone"`
	ScopeGeneration         string                `json:"scope_generation"`
	ScopeValidUntil         *string               `json:"scope_valid_until"`
	ProjectionRefreshNeeded bool                  `json:"projection_refresh_required"`
}

func (service *Service) Changes(ctx context.Context, request handler.MobileV2ChangesRequest) (any, error) {
	runtime, err := service.resolve(ctx, request.Identity)
	if err != nil {
		return nil, err
	}
	view, err := service.scopeView(runtime, request.Scope, request.ProjectionTimeZone)
	if err != nil {
		return nil, err
	}
	if request.Cursor != "" {
		cursor, decodeErr := service.tokens.DecodeChangeCursorToken(request.Cursor)
		if decodeErr == nil && cursor.Binding.WorkspaceID == view.Binding.WorkspaceID &&
			cursor.Binding.Scope == view.Binding.Scope &&
			(cursor.Binding.ScopeGeneration != view.Binding.ScopeGeneration ||
				!sameOptionalString(cursor.Binding.ProjectionTimeZone, view.Binding.ProjectionTimeZone)) {
			return nil, projectionRefresh(view)
		}
	}
	repository, _, err := service.repository(runtime)
	if err != nil {
		return nil, err
	}
	page, err := repository.ReadChanges(ctx, view.Binding, request.Cursor, service.pageSize)
	if err != nil {
		if errors.Is(err, mobilev2sync.ErrCursorMismatch) {
			return nil, resyncRequired("mobile-v2 change cursor is invalid for the current runtime")
		}
		return nil, err
	}
	changes := make([]changeBatchResponse, len(page.Changes))
	for index, change := range page.Changes {
		receipt := change.ReceiptJSON
		if len(receipt) == 0 {
			receipt = json.RawMessage("null")
		}
		changes[index] = changeBatchResponse{
			ChangeSequence: change.Sequence, CausedByCommandID: change.CausedByCommandID,
			OriginDeviceClientID: change.OriginDeviceClientID, Receipt: receipt, Entities: change.EntitiesJSON,
		}
	}
	return changesResponse{
		Changes: changes, NextCursor: page.NextCursor, HasMore: page.HasMore,
		ProjectionTimeZone: view.Binding.ProjectionTimeZone, ScopeGeneration: view.Binding.ScopeGeneration,
		ScopeValidUntil: formatOptionalInstant(view.ScopeValidUntil),
	}, nil
}

func (service *Service) ApplyCommand(ctx context.Context, request handler.MobileV2CommandRequest) (any, error) {
	return service.commands.ApplyCommand(ctx, request)
}

func (service *Service) Receipt(ctx context.Context, request handler.MobileV2ReceiptRequest) (any, error) {
	return service.commands.Receipt(ctx, request)
}

func (service *Service) resolve(ctx context.Context, identity handler.MobileV2Identity) (taskruntime.MobileRuntimeSnapshot, error) {
	if strings.TrimSpace(identity.WorkspaceID) == "" {
		return taskruntime.MobileRuntimeSnapshot{}, &handler.MobileV2ProtocolError{
			Status: http.StatusUnauthorized, Code: "upgrade_required", Message: "workspace identity is required",
		}
	}
	runtime, err := service.runtime.ResolveMobileRuntime(ctx, identity.WorkspaceID)
	if err != nil {
		return taskruntime.MobileRuntimeSnapshot{}, err
	}
	if runtime.WorkspaceID != identity.WorkspaceID || runtime.Epoch < 1 || runtime.DB == nil {
		return taskruntime.MobileRuntimeSnapshot{}, errors.New("incomplete mobile-v2 workspace runtime")
	}
	return runtime, nil
}

func (service *Service) repository(runtime taskruntime.MobileRuntimeSnapshot) (*mobilev2sync.SQLRepository, mobilev2projection.Dialect, error) {
	switch runtime.Driver {
	case storage.DriverSQLite:
		return mobilev2sync.NewSQLRepository(runtime.DB, mobilev2sync.SQLDialectSQLite, service.tokens),
			mobilev2projection.DialectSQLite, nil
	case storage.DriverPostgres:
		return mobilev2sync.NewSQLRepository(runtime.DB, mobilev2sync.SQLDialectPostgres, service.tokens),
			mobilev2projection.DialectPostgres, nil
	default:
		return nil, "", errors.New("unsupported mobile-v2 workspace storage driver")
	}
}

func (service *Service) scopeView(runtime taskruntime.MobileRuntimeSnapshot, rawScope, rawTimeZone string) (scopeView, error) {
	scope := mobilev2sync.ScopeName(strings.TrimSpace(rawScope))
	now := service.now().UTC().Truncate(time.Millisecond)
	epoch := strconv.FormatInt(runtime.Epoch, 10)
	binding := mobilev2sync.TokenBinding{
		WorkspaceID: runtime.WorkspaceID, Scope: scope, ContractEpoch: mobilev2contract.ContractEpoch,
		RuntimeEpoch: epoch, TaskModelVersion: 2,
	}
	switch scope {
	case mobilev2sync.ScopeIPhoneContent, mobilev2sync.ScopeIPhoneTaskCore:
		if strings.TrimSpace(rawTimeZone) != "" {
			return scopeView{}, resyncRequired("stable mobile-v2 scopes do not accept a projection time zone")
		}
		binding.ScopeGeneration = fmt.Sprintf("%s:contract:%s:runtime:%s", scope, mobilev2contract.ContractEpoch, epoch)
		return scopeView{Binding: binding, ProjectionAsOf: now}, nil
	case mobilev2sync.ScopeIPhoneOccurrenceWindow, mobilev2sync.ScopeWatchOccurrenceWindow:
		timeZone := strings.TrimSpace(rawTimeZone)
		location, err := time.LoadLocation(timeZone)
		if err != nil || timeZone == "" {
			return scopeView{}, &handler.MobileV2ProtocolError{
				Status: http.StatusUnprocessableEntity, Code: "projection_refresh_required",
				Message: "sliding mobile-v2 scopes require an IANA projection time zone",
			}
		}
		localNow := now.In(location)
		localDay := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), 0, 0, 0, 0, location)
		validUntil := localDay.AddDate(0, 0, 1).UTC()
		startDays, endDays := -30, 91
		if scope == mobilev2sync.ScopeWatchOccurrenceWindow {
			startDays, endDays = -1, 15
		}
		windowStartLocal := localDay.AddDate(0, 0, startDays)
		windowEndLocal := localDay.AddDate(0, 0, endDays)
		binding.ProjectionTimeZone = &timeZone
		binding.ScopeGeneration = fmt.Sprintf("%s@%s:runtime:%s", localDay.Format("2006-01-02"), timeZone, epoch)
		return scopeView{
			Binding: binding, ProjectionAsOf: now, ScopeValidUntil: &validUntil,
			WindowStart: windowStartLocal.UTC(), WindowEnd: windowEndLocal.UTC(),
			WindowStartDate: windowStartLocal.Format("2006-01-02"),
			WindowEndDate:   windowEndLocal.Format("2006-01-02"),
		}, nil
	default:
		return scopeView{}, resyncRequired("unknown mobile-v2 sync scope")
	}
}

func resyncRequired(message string) error {
	return &handler.MobileV2ProtocolError{
		Status: http.StatusGone, Code: "resync_required", Message: message,
	}
}

func projectionRefresh(view scopeView) error {
	return &handler.MobileV2ProtocolError{
		Status: http.StatusConflict, Code: "projection_refresh_required",
		Message: "mobile-v2 sliding scope projection must be refreshed",
		Details: map[string]any{
			"projection_time_zone":        view.Binding.ProjectionTimeZone,
			"scope_generation":            view.Binding.ScopeGeneration,
			"scope_valid_until":           formatOptionalInstant(view.ScopeValidUntil),
			"projection_refresh_required": true,
		},
	}
}

func formatInstant(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000Z")
}

func formatOptionalInstant(value *time.Time) *string {
	if value == nil {
		return nil
	}
	formatted := formatInstant(*value)
	return &formatted
}

func sameOptionalString(left, right *string) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

var _ handler.MobileV2Service = (*Service)(nil)
