package mobilev2command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hujinrun/flowspace/internal/mobilev2contract"
)

var (
	ErrRequestDigestMismatch       = errors.New("mobile-v2 request digest mismatch")
	ErrStaleRuntimeEpoch           = errors.New("mobile-v2 stale runtime epoch proven uncommitted")
	ErrReceiptHistoryAmbiguous     = errors.New("mobile-v2 receipt history ambiguous")
	ErrExpectedRevisionRequired    = errors.New("mobile-v2 expected revision required")
	ErrInvalidTerminalStatus       = errors.New("mobile-v2 invalid terminal status")
	ErrWorkspaceModeForbidsCommand = errors.New("mobile-v2 workspace mode forbids command")
	ErrUpgradeRequired             = errors.New("mobile-v2 client upgrade required")
)

type ResultStatus string

const (
	StatusApplied    ResultStatus = "applied"
	StatusNoOp       ResultStatus = "no_op"
	StatusConflict   ResultStatus = "conflict"
	StatusRejected   ResultStatus = "rejected"
	StatusRetryLater ResultStatus = "retry_later"
)

type Command struct {
	WorkspaceID           string
	OriginDeviceClientID  string
	CommandID             string
	RequestDigest         string
	CommandType           string
	CreatedRuntimeEpoch   string
	ExpectedRevisionNames []string
	RawEnvelope           []byte
	ForwardedByDeviceID   *string
}

type IdentityMapping struct {
	EntityType string  `json:"entity_type"`
	ClientID   *string `json:"client_id"`
	EntityID   *string `json:"entity_id"`
}

type AffectedRevision struct {
	EntityType string `json:"entity_type"`
	EntityID   string `json:"entity_id"`
	Revision   string `json:"revision"`
}

type DomainResult struct {
	Status            ResultStatus
	IdentityMappings  []IdentityMapping
	AffectedRevisions []AffectedRevision
	AfterImages       [][]byte
}

type Receipt struct {
	WorkspaceID          string             `json:"workspace_id"`
	OriginDeviceClientID string             `json:"origin_device_client_id"`
	CommandID            string             `json:"command_id"`
	RequestDigest        string             `json:"request_digest"`
	Status               ResultStatus       `json:"status"`
	CommitSequence       uint64             `json:"-"`
	IdentityMappings     []IdentityMapping  `json:"identity_mappings"`
	AffectedRevisions    []AffectedRevision `json:"affected_revisions"`
	CompletedAt          time.Time          `json:"completed_at"`
}

func (receipt Receipt) MarshalJSON() ([]byte, error) {
	type wireReceipt struct {
		WorkspaceID          string             `json:"workspace_id"`
		OriginDeviceClientID string             `json:"origin_device_client_id"`
		CommandID            string             `json:"command_id"`
		RequestDigest        string             `json:"request_digest"`
		Status               ResultStatus       `json:"status"`
		CommitSequence       string             `json:"commit_sequence"`
		IdentityMappings     []IdentityMapping  `json:"identity_mappings"`
		AffectedRevisions    []AffectedRevision `json:"affected_revisions"`
		CompletedAt          string             `json:"completed_at"`
	}
	return json.Marshal(wireReceipt{
		WorkspaceID: receipt.WorkspaceID, OriginDeviceClientID: receipt.OriginDeviceClientID,
		CommandID: receipt.CommandID, RequestDigest: receipt.RequestDigest, Status: receipt.Status,
		CommitSequence:    strconv.FormatUint(receipt.CommitSequence, 10),
		IdentityMappings:  cloneMappings(receipt.IdentityMappings),
		AffectedRevisions: cloneRevisions(receipt.AffectedRevisions),
		CompletedAt:       receipt.CompletedAt.UTC().Format("2006-01-02T15:04:05.000Z"),
	})
}

func (receipt *Receipt) UnmarshalJSON(data []byte) error {
	if receipt == nil {
		return errors.New("mobile-v2 receipt destination is nil")
	}
	type wireReceipt struct {
		WorkspaceID          string             `json:"workspace_id"`
		OriginDeviceClientID string             `json:"origin_device_client_id"`
		CommandID            string             `json:"command_id"`
		RequestDigest        string             `json:"request_digest"`
		Status               ResultStatus       `json:"status"`
		CommitSequence       string             `json:"commit_sequence"`
		IdentityMappings     []IdentityMapping  `json:"identity_mappings"`
		AffectedRevisions    []AffectedRevision `json:"affected_revisions"`
		CompletedAt          string             `json:"completed_at"`
	}
	var wire wireReceipt
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	sequence, err := strconv.ParseUint(wire.CommitSequence, 10, 64)
	if err != nil {
		return err
	}
	completedAt, err := time.Parse("2006-01-02T15:04:05.000Z", wire.CompletedAt)
	if err != nil {
		return err
	}
	*receipt = Receipt{
		WorkspaceID: wire.WorkspaceID, OriginDeviceClientID: wire.OriginDeviceClientID,
		CommandID: wire.CommandID, RequestDigest: wire.RequestDigest, Status: wire.Status,
		CommitSequence: sequence, IdentityMappings: cloneMappings(wire.IdentityMappings),
		AffectedRevisions: cloneRevisions(wire.AffectedRevisions), CompletedAt: completedAt.UTC(),
	}
	return nil
}

type ChangeBatch struct {
	Sequence             uint64
	CausedByCommandID    string
	OriginDeviceClientID string
	Receipt              Receipt
	AfterImages          [][]byte
}

type Response struct {
	Receipt    *Receipt
	Replayed   bool
	RetryLater bool
}

type Engine struct {
	mu              sync.Mutex
	runtimeEpoch    string
	sequence        uint64
	ledger          map[string]Receipt
	changes         []ChangeBatch
	historyComplete bool
	now             func() time.Time
}

func NewEngine(runtimeEpoch string) *Engine {
	return &Engine{
		runtimeEpoch: runtimeEpoch, ledger: make(map[string]Receipt), historyComplete: true,
		now: func() time.Time { return time.Now().UTC() },
	}
}

func (engine *Engine) Execute(ctx context.Context, command Command, apply func(context.Context) (DomainResult, error)) (Response, error) {
	computedDigest, err := mobilev2contract.RequestDigest(command.RawEnvelope)
	if err != nil || computedDigest != command.RequestDigest {
		return Response{}, ErrRequestDigestMismatch
	}
	if command.WorkspaceID == "" || command.OriginDeviceClientID == "" || command.CommandID == "" || command.CreatedRuntimeEpoch == "" {
		return Response{}, fmt.Errorf("invalid command identity")
	}
	if err := ValidateExpectedRevisions(command.CommandType, command.ExpectedRevisionNames); err != nil {
		return Response{}, err
	}

	engine.mu.Lock()
	defer engine.mu.Unlock()
	key := receiptKey(command.WorkspaceID, command.OriginDeviceClientID, command.CommandID)
	if receipt, found := engine.ledger[key]; found {
		if receipt.RequestDigest != command.RequestDigest {
			return Response{}, ErrRequestDigestMismatch
		}
		copy := cloneReceipt(receipt)
		return Response{Receipt: &copy, Replayed: true}, nil
	}
	if !engine.historyComplete {
		return Response{}, ErrReceiptHistoryAmbiguous
	}
	if command.CreatedRuntimeEpoch != engine.runtimeEpoch {
		return Response{}, ErrStaleRuntimeEpoch
	}

	result, err := apply(ctx)
	if err != nil {
		return Response{}, err
	}
	if result.Status == StatusRetryLater {
		return Response{RetryLater: true}, nil
	}
	if !terminalStatus(result.Status) {
		return Response{}, ErrInvalidTerminalStatus
	}
	engine.sequence++
	receipt := Receipt{
		WorkspaceID: command.WorkspaceID, OriginDeviceClientID: command.OriginDeviceClientID,
		CommandID: command.CommandID, RequestDigest: command.RequestDigest, Status: result.Status,
		CommitSequence: engine.sequence, IdentityMappings: cloneMappings(result.IdentityMappings),
		AffectedRevisions: cloneRevisions(result.AffectedRevisions), CompletedAt: engine.now(),
	}
	engine.ledger[key] = receipt
	engine.changes = append(engine.changes, ChangeBatch{
		Sequence: engine.sequence, CausedByCommandID: command.CommandID,
		OriginDeviceClientID: command.OriginDeviceClientID, Receipt: cloneReceipt(receipt),
		AfterImages: cloneImages(result.AfterImages),
	})
	copy := cloneReceipt(receipt)
	return Response{Receipt: &copy}, nil
}

func (engine *Engine) Lookup(workspaceID, originDeviceClientID, commandID string) (Receipt, bool) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	receipt, found := engine.ledger[receiptKey(workspaceID, originDeviceClientID, commandID)]
	return cloneReceipt(receipt), found
}

func (engine *Engine) ChangesAfter(sequence uint64) []ChangeBatch {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	result := make([]ChangeBatch, 0)
	for _, change := range engine.changes {
		if change.Sequence <= sequence {
			continue
		}
		copy := change
		copy.Receipt = cloneReceipt(change.Receipt)
		copy.AfterImages = cloneImages(change.AfterImages)
		result = append(result, copy)
	}
	return result
}

func (engine *Engine) CompactChanges(through uint64) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	kept := engine.changes[:0]
	for _, change := range engine.changes {
		if change.Sequence > through {
			kept = append(kept, change)
		}
	}
	engine.changes = kept
}

func (engine *Engine) MarkReceiptHistoryAmbiguous() {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.historyComplete = false
}

func ValidateExpectedRevisions(commandType string, names []string) error {
	required := requiredRevisionNames(commandType)
	if required == nil {
		return ErrExpectedRevisionRequired
	}
	got := append([]string(nil), names...)
	sort.Strings(got)
	sort.Strings(required)
	if strings.Join(got, ",") != strings.Join(required, ",") {
		return ErrExpectedRevisionRequired
	}
	return nil
}

func ValidateWorkspaceCommandMode(mode string) error {
	switch mode {
	case "v2-active":
		return nil
	case "upgrade-required":
		return ErrUpgradeRequired
	default:
		return ErrWorkspaceModeForbidsCommand
	}
}

func requiredRevisionNames(commandType string) []string {
	switch {
	case commandType == "note.create" || commandType == "inbox.create" || commandType == "voice.create":
		return []string{}
	case strings.HasPrefix(commandType, "note.") || strings.HasPrefix(commandType, "inbox.") ||
		commandType == "voice.update" || commandType == "voice_audio.delete" || commandType == "voice_note.delete" ||
		strings.HasPrefix(commandType, "transcription."):
		return []string{"entity"}
	case commandType == "project.create":
		return []string{}
	case strings.HasPrefix(commandType, "project."):
		return []string{"project"}
	case commandType == "task.create":
		return []string{"project"}
	case commandType == "task.update":
		return []string{"schedule", "task"}
	case strings.HasPrefix(commandType, "task."):
		return []string{"occurrences", "schedule", "task"}
	case strings.HasPrefix(commandType, "occurrence."):
		return []string{"occurrence", "schedule", "task"}
	case commandType == "schedule.reschedule-this-and-following":
		return []string{"schedule", "task"}
	default:
		return nil
	}
}

func terminalStatus(status ResultStatus) bool {
	return status == StatusApplied || status == StatusNoOp || status == StatusConflict || status == StatusRejected
}

func receiptKey(workspaceID, originDeviceClientID, commandID string) string {
	return workspaceID + "\x00" + originDeviceClientID + "\x00" + commandID
}

func cloneReceipt(receipt Receipt) Receipt {
	receipt.IdentityMappings = cloneMappings(receipt.IdentityMappings)
	receipt.AffectedRevisions = cloneRevisions(receipt.AffectedRevisions)
	return receipt
}

func cloneMappings(values []IdentityMapping) []IdentityMapping {
	return append([]IdentityMapping(nil), values...)
}

func cloneRevisions(values []AffectedRevision) []AffectedRevision {
	return append([]AffectedRevision(nil), values...)
}

func cloneImages(values [][]byte) [][]byte {
	result := make([][]byte, len(values))
	for index, value := range values {
		result[index] = append([]byte(nil), value...)
	}
	return result
}
