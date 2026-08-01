package mobilev2service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/handler"
	"github.com/hujinrun/flowspace/internal/mobilev2command"
	"github.com/hujinrun/flowspace/internal/mobilev2projection"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/taskruntime"
	"github.com/hujinrun/flowspace/internal/voiceaudiocleanup"
)

type CommandExecutorConfig struct {
	Runtime RuntimeResolver
	Now     func() time.Time
}

type CommandExecutor struct {
	runtime RuntimeResolver
	now     func() time.Time
}

func NewCommandExecutor(config CommandExecutorConfig) (*CommandExecutor, error) {
	if config.Runtime == nil {
		return nil, errors.New("mobile-v2 command runtime is required")
	}
	if config.Now == nil {
		config.Now = func() time.Time { return time.Now().UTC() }
	}
	return &CommandExecutor{runtime: config.Runtime, now: config.Now}, nil
}

func drainTenantObjectCleanup(
	ctx context.Context,
	runtime taskruntime.MobileRuntimeSnapshot,
	objects objectstore.Store,
	now func() time.Time,
	owner string,
	limit int,
) error {
	if objects == nil || runtime.Store == nil || limit < 1 {
		return nil
	}
	worker := voiceaudiocleanup.NewWorker(runtime.Store, objects, owner)
	worker.Now = now
	for processed := 0; processed < limit; processed++ {
		claimed, err := worker.RunOne(ctx)
		if err != nil || !claimed {
			return err
		}
	}
	return nil
}

func (executor *CommandExecutor) ApplyCommand(ctx context.Context, request handler.MobileV2CommandRequest) (any, error) {
	envelope, err := mobilev2command.ParseEnvelope(request.RawEnvelope, request.Identity.WorkspaceID)
	if err != nil {
		return nil, commandProtocolError(err)
	}
	if isContentCommand(envelope.CommandType) {
		return executor.applyContentCommand(ctx, request.Identity, envelope)
	}
	return executor.applyTaskCommand(ctx, request.Identity, envelope)
}

func (executor *CommandExecutor) applyTaskCommand(
	ctx context.Context,
	identity handler.MobileV2Identity,
	envelope mobilev2command.Envelope,
) (any, error) {
	runtime, err := executor.runtime.ResolveMobileRuntime(ctx, identity.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if runtime.WorkspaceID != identity.WorkspaceID || runtime.Epoch < 1 || runtime.DB == nil || runtime.Writer == nil {
		return nil, errors.New("incomplete mobile-v2 command runtime")
	}
	ledger, dialect, err := commandLedger(runtime)
	if err != nil {
		return nil, err
	}
	command := envelope.LedgerCommand()
	var response mobilev2command.Response
	err = runtime.Writer.BeginFencedWrite(ctx, runtime.WorkspaceID, runtime.Epoch, func(tx storage.TenantWriteTx) error {
		mobileTx, ok := tx.(storage.MobileV2TenantWriteTx)
		if !ok || mobileTx.MobileV2SQLRunner() == nil {
			return errors.New("tenant transaction does not expose mobile-v2 capabilities")
		}
		runner := mobileTx.MobileV2SQLRunner()
		prepared, proceed, err := ledger.PrepareOnRunner(ctx, runner, command)
		if err != nil {
			return err
		}
		if !proceed {
			response = prepared
			return nil
		}
		envelope, dependencyState, err := resolveCommandDependencies(ctx, ledger, runner, envelope)
		if err != nil {
			return err
		}
		if dependencyState == dependencyPending {
			response = mobilev2command.Response{RetryLater: true}
			return nil
		}
		if dependencyState == dependencyRejected {
			response, err = ledger.FinalizeOnRunner(ctx, runner, command, executor.now().UTC(),
				mobilev2command.DynamicCommitResult{
					DomainResult: mobilev2command.DomainResult{Status: mobilev2command.StatusRejected},
				})
			return err
		}
		if envelope.CreatedRuntimeEpoch != strconv.FormatInt(runtime.Epoch, 10) {
			return mobilev2command.ErrStaleRuntimeEpoch
		}
		application, err := taskruntime.NewTransactionApplication(mobileTx, runtime.WorkspaceID, runtime.Epoch)
		if err != nil {
			return err
		}
		outcome, applyErr := dispatchTaskCommand(ctx, application, identity, envelope, executor.now().UTC())
		status, terminal := terminalDomainStatus(applyErr)
		if applyErr != nil && !terminal {
			return applyErr
		}
		outcome.Result.Status = status
		currentSequence, err := ledger.CurrentSequenceOnRunner(ctx, runner, runtime.WorkspaceID)
		if err != nil {
			return err
		}
		scopeChanges, afterImages, err := projectTaskCommandOutcome(
			ctx, runner, dialect, runtime.WorkspaceID, currentSequence+1, outcome,
		)
		if err != nil {
			return err
		}
		outcome.Result.AfterImages = afterImages
		response, err = ledger.FinalizeOnRunner(ctx, runner, command, executor.now().UTC(),
			mobilev2command.DynamicCommitResult{
				DomainResult: outcome.Result,
				ScopeChanges: scopeChanges,
			})
		return err
	})
	if err != nil {
		return nil, commandProtocolError(err)
	}
	return commandResponse(response)
}

func (executor *CommandExecutor) Receipt(ctx context.Context, request handler.MobileV2ReceiptRequest) (any, error) {
	if request.Identity.WorkspaceID == "" ||
		!validCommandPathUUID(request.OriginDeviceClientID) || !validCommandPathUUID(request.CommandID) {
		return nil, &handler.MobileV2ProtocolError{
			Status: http.StatusUnprocessableEntity, Code: "upgrade_required",
			Message: "invalid mobile-v2 receipt identity",
		}
	}
	runtime, err := executor.runtime.ResolveMobileRuntime(ctx, request.Identity.WorkspaceID)
	if err != nil {
		return nil, err
	}
	ledger, _, err := commandLedger(runtime)
	if err != nil {
		return nil, err
	}
	receipt, found, err := ledger.Lookup(
		ctx, request.Identity.WorkspaceID, request.OriginDeviceClientID, request.CommandID,
	)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, &handler.MobileV2ProtocolError{
			Status: http.StatusNotFound, Code: "resync_required",
			Message: "mobile-v2 terminal receipt was not found",
		}
	}
	return struct {
		SchemaVersion string                  `json:"schema_version"`
		Replayed      bool                    `json:"replayed"`
		Receipt       mobilev2command.Receipt `json:"receipt"`
	}{SchemaVersion: "mobile-v2", Replayed: true, Receipt: receipt}, nil
}

func commandLedger(runtime taskruntime.MobileRuntimeSnapshot) (*mobilev2command.SQLLedger, mobilev2projection.Dialect, error) {
	switch runtime.Driver {
	case storage.DriverSQLite:
		return mobilev2command.NewSQLLedger(runtime.DB, mobilev2command.SQLDialectSQLite),
			mobilev2projection.DialectSQLite, nil
	case storage.DriverPostgres:
		return mobilev2command.NewSQLLedger(runtime.DB, mobilev2command.SQLDialectPostgres),
			mobilev2projection.DialectPostgres, nil
	default:
		return nil, "", errors.New("unsupported mobile-v2 command storage driver")
	}
}

func commandResponse(response mobilev2command.Response) (any, error) {
	if response.RetryLater {
		return nil, &handler.MobileV2ProtocolError{
			Status: http.StatusConflict, Code: "receipt_history_ambiguous",
			Message: "mobile-v2 command dependency is not committed yet",
		}
	}
	if response.Receipt == nil {
		return nil, errors.New("mobile-v2 command completed without a terminal receipt")
	}
	return struct {
		SchemaVersion string                  `json:"schema_version"`
		Replayed      bool                    `json:"replayed"`
		Receipt       mobilev2command.Receipt `json:"receipt"`
	}{
		SchemaVersion: "mobile-v2", Replayed: response.Replayed, Receipt: *response.Receipt,
	}, nil
}

func commandProtocolError(err error) error {
	if err == nil {
		return nil
	}
	var protocolError *handler.MobileV2ProtocolError
	if errors.As(err, &protocolError) {
		return err
	}
	switch {
	case errors.Is(err, mobilev2command.ErrInvalidCommandEnvelope),
		errors.Is(err, mobilev2command.ErrRequestDigestMismatch),
		errors.Is(err, mobilev2command.ErrExpectedRevisionRequired):
		return &handler.MobileV2ProtocolError{
			Status: http.StatusUnprocessableEntity, Code: "upgrade_required",
			Message: "command does not match the mobile-v2 contract",
		}
	case errors.Is(err, mobilev2command.ErrStaleRuntimeEpoch):
		return &handler.MobileV2ProtocolError{
			Status: http.StatusConflict, Code: "stale_runtime_epoch",
			Message: "command was created for a stale workspace runtime",
		}
	case errors.Is(err, mobilev2command.ErrReceiptHistoryAmbiguous):
		return &handler.MobileV2ProtocolError{
			Status: http.StatusConflict, Code: "receipt_history_ambiguous",
			Message: "terminal receipt history cannot prove this command is new",
		}
	default:
		return err
	}
}

func terminalDomainStatus(err error) (mobilev2command.ResultStatus, bool) {
	if err == nil {
		return mobilev2command.StatusApplied, true
	}
	switch {
	case errors.Is(err, taskdomain.ErrProjectRevisionConflict),
		errors.Is(err, taskdomain.ErrTaskRevisionConflict),
		errors.Is(err, taskdomain.ErrScheduleRevisionConflict),
		errors.Is(err, taskdomain.ErrOccurrenceRevisionConflict),
		errors.Is(err, taskdomain.ErrAggregateRevisionConflict),
		errors.Is(err, taskdomain.ErrProjectNotFound),
		errors.Is(err, taskdomain.ErrTaskNotFound),
		errors.Is(err, taskdomain.ErrOccurrenceNotFound):
		return mobilev2command.StatusConflict, true
	}
	if taskdomain.ErrorCodeOf(err) != "" ||
		errors.Is(err, taskdomain.ErrInvalidProjectCommand) ||
		errors.Is(err, taskdomain.ErrInvalidTaskCommand) ||
		errors.Is(err, taskdomain.ErrInvalidScheduleCommand) ||
		errors.Is(err, taskdomain.ErrInvalidTaskCreation) {
		return mobilev2command.StatusRejected, true
	}
	return "", false
}

type taskCommandOutcome struct {
	Result        mobilev2command.DomainResult
	Scopes        []mobilev2sync.ScopeName
	ProjectIDs    map[string]struct{}
	TaskIDs       map[string]struct{}
	OccurrenceIDs map[string]struct{}
	Deleted       []storage.MobileV2DeletedEntity
}

func newTaskCommandOutcome(scopes ...mobilev2sync.ScopeName) taskCommandOutcome {
	return taskCommandOutcome{
		Scopes: scopes, ProjectIDs: map[string]struct{}{},
		TaskIDs: map[string]struct{}{}, OccurrenceIDs: map[string]struct{}{},
	}
}

func projectTaskCommandOutcome(
	ctx context.Context,
	runner storage.TenantSQLRunner,
	dialect mobilev2projection.Dialect,
	workspaceID string,
	sequence uint64,
	outcome taskCommandOutcome,
) ([]mobilev2command.ScopeChange, [][]byte, error) {
	scopeChanges := make([]mobilev2command.ScopeChange, 0, len(outcome.Scopes))
	all := make([][]byte, 0)
	now := time.Now().UTC()
	for _, scope := range outcome.Scopes {
		var projected []json.RawMessage
		var err error
		switch scope {
		case mobilev2sync.ScopeIPhoneTaskCore:
			projected, err = mobilev2projection.Project(ctx, runner, dialect, mobilev2projection.Projection{
				WorkspaceID: workspaceID, Scope: scope, AsOf: now, Sequence: sequence,
			})
		case mobilev2sync.ScopeIPhoneOccurrenceWindow, mobilev2sync.ScopeWatchOccurrenceWindow:
			projected, err = mobilev2projection.Project(ctx, runner, dialect, mobilev2projection.Projection{
				WorkspaceID: workspaceID, Scope: scope, AsOf: now, Sequence: sequence,
				WindowStart:     time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC),
				WindowEnd:       time.Date(9999, 12, 31, 0, 0, 0, 0, time.UTC),
				WindowStartDate: "1970-01-01", WindowEndDate: "9999-12-31",
			})
		default:
			return nil, nil, fmt.Errorf("unsupported task command scope %q", scope)
		}
		if err != nil {
			return nil, nil, err
		}
		filtered, err := filterTaskCommandImages(projected, outcome)
		if err != nil {
			return nil, nil, err
		}
		for _, deleted := range outcome.Deleted {
			if (scope == mobilev2sync.ScopeIPhoneTaskCore && deleted.EntityType == "task_occurrence") ||
				(scope != mobilev2sync.ScopeIPhoneTaskCore && deleted.EntityType != "task_occurrence") {
				continue
			}
			tombstone, err := mobilev2projection.Tombstone(
				deleted.EntityType, deleted.EntityID, deleted.Revision, now,
			)
			if err != nil {
				return nil, nil, err
			}
			filtered = append(filtered, tombstone)
		}
		images := rawMessagesToBytes(filtered)
		scopeChanges = append(scopeChanges, mobilev2command.ScopeChange{Scope: scope, AfterImages: images})
		all = append(all, images...)
	}
	return scopeChanges, all, nil
}

func filterTaskCommandImages(images []json.RawMessage, outcome taskCommandOutcome) ([]json.RawMessage, error) {
	result := make([]json.RawMessage, 0)
	for _, image := range images {
		var header struct {
			EntityType string `json:"entity_type"`
			EntityID   string `json:"entity_id"`
			Payload    struct {
				TaskID string `json:"task_id"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(image, &header); err != nil {
			return nil, err
		}
		include := false
		switch header.EntityType {
		case "project":
			_, include = outcome.ProjectIDs[header.EntityID]
		case "task", "task_schedule":
			_, include = outcome.TaskIDs[header.EntityID]
		case "schedule_version":
			_, include = outcome.TaskIDs[header.Payload.TaskID]
		case "task_occurrence":
			_, include = outcome.OccurrenceIDs[header.EntityID]
		}
		if include {
			result = append(result, append(json.RawMessage(nil), image...))
		}
	}
	return result, nil
}

func rawMessagesToBytes(messages []json.RawMessage) [][]byte {
	result := make([][]byte, len(messages))
	for index, message := range messages {
		result[index] = append([]byte(nil), message...)
	}
	return result
}

func isContentCommand(commandType string) bool {
	return strings.HasPrefix(commandType, "note.") ||
		strings.HasPrefix(commandType, "inbox.") ||
		strings.HasPrefix(commandType, "voice") ||
		strings.HasPrefix(commandType, "transcription.")
}

func validCommandPathUUID(value string) bool {
	parsed, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil && parsed.String() == strings.ToLower(strings.TrimSpace(value))
}

var _ CommandService = (*CommandExecutor)(nil)
