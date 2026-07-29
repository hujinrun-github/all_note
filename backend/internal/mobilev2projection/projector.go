package mobilev2projection

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/mobilev2sync"
)

type Dialect string

const (
	DialectSQLite   Dialect = "sqlite"
	DialectPostgres Dialect = "postgres"
)

type Projection struct {
	WorkspaceID     string
	Scope           mobilev2sync.ScopeName
	AsOf            time.Time
	WindowStart     time.Time
	WindowEnd       time.Time
	WindowStartDate string
	WindowEndDate   string
	Sequence        uint64
}

type Runner interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type aggregateRevisions struct {
	ProjectRevision    *string `json:"project_revision"`
	TaskRevision       *string `json:"task_revision"`
	ScheduleRevision   *string `json:"schedule_revision"`
	OccurrenceRevision *string `json:"occurrence_revision"`
}

type entityEnvelope struct {
	EntityType         string             `json:"entity_type"`
	EntityID           string             `json:"entity_id"`
	ClientID           *string            `json:"client_id"`
	EntityRevision     string             `json:"entity_revision"`
	AggregateRevisions aggregateRevisions `json:"aggregate_revisions"`
	DeletedAt          *string            `json:"deleted_at"`
	Payload            any                `json:"payload"`
}

func Project(ctx context.Context, runner Runner, dialect Dialect, projection Projection) ([]json.RawMessage, error) {
	if runner == nil || strings.TrimSpace(projection.WorkspaceID) == "" || projection.AsOf.IsZero() ||
		(dialect != DialectSQLite && dialect != DialectPostgres) {
		return nil, errors.New("invalid mobile-v2 projection")
	}
	switch projection.Scope {
	case mobilev2sync.ScopeIPhoneContent:
		return projectContent(ctx, runner, dialect, projection)
	case mobilev2sync.ScopeIPhoneTaskCore:
		return projectTaskCore(ctx, runner, dialect, projection)
	case mobilev2sync.ScopeIPhoneOccurrenceWindow, mobilev2sync.ScopeWatchOccurrenceWindow:
		return projectOccurrenceWindow(ctx, runner, dialect, projection)
	default:
		return nil, errors.New("invalid mobile-v2 projection scope")
	}
}

func appendEnvelope(result []json.RawMessage, envelope entityEnvelope) ([]json.RawMessage, error) {
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return nil, err
	}
	return append(result, encoded), nil
}

func Tombstone(entityType, entityID string, entityRevision int64, deletedAt time.Time) (json.RawMessage, error) {
	if strings.TrimSpace(entityType) == "" || strings.TrimSpace(entityID) == "" ||
		entityRevision < 1 || deletedAt.IsZero() {
		return nil, errors.New("invalid mobile-v2 tombstone")
	}
	revisions := aggregateRevisions{}
	switch entityType {
	case "project":
		revisions.ProjectRevision = revision(entityRevision)
	case "task":
		revisions.TaskRevision = revision(entityRevision)
	case "task_schedule", "schedule_version":
		revisions.ScheduleRevision = revision(entityRevision)
	case "task_occurrence":
		revisions.OccurrenceRevision = revision(entityRevision)
	}
	at := deletedAt.UTC().Format("2006-01-02T15:04:05.000Z")
	return json.Marshal(entityEnvelope{
		EntityType: entityType, EntityID: entityID,
		EntityRevision:     strconv.FormatInt(entityRevision, 10),
		AggregateRevisions: revisions, DeletedAt: &at, Payload: nil,
	})
}

func revision(value int64) *string {
	if value < 1 {
		return nil
	}
	result := strconv.FormatInt(value, 10)
	return &result
}

func optionalClientID(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	trimmed := strings.TrimSpace(value.String)
	if _, err := uuid.Parse(trimmed); err != nil {
		return nil
	}
	return &trimmed
}

func optionalString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	result := value.String
	return &result
}

func instantString(value flexibleInstant) *string {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC().Format("2006-01-02T15:04:05.000Z")
	return &result
}

func requiredInstantString(value flexibleInstant) string {
	result := instantString(value)
	if result == nil {
		return ""
	}
	return *result
}

func decodeObject(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	var result map[string]any
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if result == nil {
		result = map[string]any{}
	}
	return result, nil
}

func decodeStrings(raw []byte) ([]string, error) {
	if len(raw) == 0 {
		return []string{}, nil
	}
	var result []string
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result == nil {
		result = []string{}
	}
	return result, nil
}

func bind(dialect Dialect, query string) string {
	if dialect != DialectPostgres {
		return query
	}
	var builder strings.Builder
	parameter := 1
	for _, character := range query {
		if character == '?' {
			fmt.Fprintf(&builder, "$%d", parameter)
			parameter++
		} else {
			builder.WriteRune(character)
		}
	}
	return builder.String()
}

type flexibleInstant struct {
	Time  time.Time
	Valid bool
}

func (instant *flexibleInstant) Scan(source any) error {
	if source == nil {
		instant.Valid = false
		instant.Time = time.Time{}
		return nil
	}
	switch value := source.(type) {
	case time.Time:
		instant.Time = value.UTC()
		instant.Valid = true
		return nil
	case int64:
		instant.Time = time.Unix(value, 0).UTC()
		instant.Valid = true
		return nil
	case float64:
		instant.Time = time.Unix(int64(value), 0).UTC()
		instant.Valid = true
		return nil
	}
	raw := ""
	switch value := source.(type) {
	case string:
		raw = value
	case []byte:
		raw = string(value)
	default:
		return fmt.Errorf("unsupported mobile-v2 instant type %T", source)
	}
	if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil {
		instant.Time = time.Unix(seconds, 0).UTC()
		instant.Valid = true
		return nil
	}
	for _, layout := range []string{
		time.RFC3339Nano,
		"2006-01-02 15:04:05.999999999 -0700 MST",
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05",
	} {
		parsed, err := time.Parse(layout, raw)
		if err == nil {
			instant.Time = parsed.UTC()
			instant.Valid = true
			return nil
		}
	}
	return fmt.Errorf("invalid mobile-v2 instant %q", raw)
}
