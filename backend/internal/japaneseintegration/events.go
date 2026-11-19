package japaneseintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func validateEvent(e Event) error {
	if e.SchemaVersion != 1 || e.Type != "study_task.state_changed.v1" || !validID(e.EventID) || !validID(e.SourceInstanceID) || !validID(e.SourceUserID) || !validID(e.PlanID) || !validID(e.SlotID) || !validID(e.TaskID) || !validDate(e.StudyDate) || e.StateRevision < 1 || e.PlanRevision < 1 || e.CatalogRevision < 1 {
		return ErrInvalid
	}
	if _, err := time.LoadLocation(e.Timezone); err != nil {
		return ErrInvalid
	}
	if _, err := time.Parse(time.RFC3339Nano, e.OccurredAt); err != nil {
		return ErrInvalid
	}
	return nil
}
func sourceKey(e Event) string {
	raw, _ := json.Marshal([]string{e.SourceInstanceID, e.SourceUserID, e.PlanID, e.SlotID, e.TaskID, e.StudyDate})
	return digest(raw)
}
func (s *Service) AcceptEvent(ctx context.Context, connection Connection, event Event) (Receipt, error) {
	c, err := s.active(ctx, connection.ID)
	if err != nil {
		return Receipt{}, err
	}
	if event.SourceInstanceID != c.SourceInstanceID || event.SourceUserID != c.SourceUserID {
		return Receipt{}, ErrUnauthorized
	}
	if err = validateEvent(event); err != nil {
		return Receipt{}, err
	}
	raw, _ := json.Marshal(event)
	hash := digest(raw)
	receipt := Receipt{EventID: event.EventID, ConnectionID: c.ID, Status: "received", StudyDate: event.StudyDate, SourceTaskID: event.TaskID, Completed: event.Completed, SourceOccurredAt: event.OccurredAt}
	err = s.transaction(ctx, c, false, func(t tenantTx) error {
		if e := lockCatalog(ctx, t, c.ID); e != nil {
			return e
		}
		var previousHash, previousReceipt string
		e := t.sql.QueryRowContext(ctx, t.q(`SELECT payload_hash,receipt_json FROM japanese_integration_inbox WHERE workspace_id=? AND connection_id=? AND event_id=?`), c.WorkspaceID, c.ID, event.EventID).Scan(&previousHash, &previousReceipt)
		if e == nil {
			if previousHash != hash {
				return ErrConflict
			}
			return json.Unmarshal([]byte(previousReceipt), &receipt)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		// A source version describes one immutable business fact, even if a
		// sender accidentally assigns it another transport event identifier.
		var sameVersionRaw string
		versionErr := t.sql.QueryRowContext(ctx, t.q(`SELECT event_json FROM japanese_integration_inbox WHERE workspace_id=? AND connection_id=? AND source_key=? AND state_revision=? LIMIT 1`), c.WorkspaceID, c.ID, sourceKey(event), event.StateRevision).Scan(&sameVersionRaw)
		if versionErr == nil {
			var previous Event
			if e := json.Unmarshal([]byte(sameVersionRaw), &previous); e != nil {
				return e
			}
			previous.EventID = event.EventID
			semantic, _ := json.Marshal(previous)
			if digest(semantic) != hash {
				return ErrConflict
			}
		} else if !errors.Is(versionErr, sql.ErrNoRows) {
			return versionErr
		}
		binding, e := s.bindingFor(ctx, t, c, event)
		bindingID := ""
		if e == nil {
			bindingID = binding.ID
			receipt.TargetTaskID = binding.TargetTaskID
			receipt.BindingVersion = binding.Version
		} else if !errors.Is(e, ErrNotFound) {
			return e
		} else {
			receipt.Status = "needs_attention"
			receipt.ErrorCode = "unbound"
		}
		encoded, _ := json.Marshal(receipt)
		_, e = t.sql.ExecContext(ctx, t.q(`INSERT INTO japanese_integration_inbox(workspace_id,connection_id,event_id,payload_hash,event_json,source_key,state_revision,binding_id,receipt_json,status,received_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`), c.WorkspaceID, c.ID, event.EventID, hash, string(raw), sourceKey(event), event.StateRevision, bindingID, string(encoded), receipt.Status, s.cfg.Now().UTC().Format(time.RFC3339Nano))
		return e
	})
	return receipt, err
}
func (s *Service) Event(ctx context.Context, connection Connection, eventID string) (Receipt, error) {
	c, e := s.active(ctx, connection.ID)
	if e != nil {
		return Receipt{}, e
	}
	var receipt Receipt
	e = s.transaction(ctx, c, false, func(t tenantTx) error {
		var raw string
		e := t.sql.QueryRowContext(ctx, t.q(`SELECT receipt_json FROM japanese_integration_inbox WHERE workspace_id=? AND connection_id=? AND event_id=?`), c.WorkspaceID, c.ID, eventID).Scan(&raw)
		if errors.Is(e, sql.ErrNoRows) {
			return ErrNotFound
		}
		if e != nil {
			return e
		}
		return json.Unmarshal([]byte(raw), &receipt)
	})
	return receipt, e
}
func (s *Service) Retry(ctx context.Context, workspace, eventID string) error {
	// HTTP adapter authorizes the caller; worker still verifies the connection owner.
	connections, e := s.connections(ctx, workspace)
	if e != nil {
		return e
	}
	for _, c := range connections {
		if c.State != "active" {
			continue
		}
		found := false
		e = s.transaction(ctx, c, false, func(t tenantTx) error {
			if e := lockCatalog(ctx, t, c.ID); e != nil {
				return e
			}
			var raw, receiptRaw, bindingID, status string
			e := t.sql.QueryRowContext(ctx, t.q(`SELECT event_json,receipt_json,binding_id,status FROM japanese_integration_inbox WHERE workspace_id=? AND connection_id=? AND event_id=?`), workspace, c.ID, eventID).Scan(&raw, &receiptRaw, &bindingID, &status)
			if errors.Is(e, sql.ErrNoRows) {
				return nil
			}
			if e != nil {
				return e
			}
			found = true
			if status == "applied" || status == "already_done" || status == "stale" {
				return nil
			}
			var event Event
			var receipt Receipt
			if e = json.Unmarshal([]byte(raw), &event); e != nil {
				return e
			}
			if e = json.Unmarshal([]byte(receiptRaw), &receipt); e != nil {
				return e
			}
			if bindingID == "" {
				return ErrConflict // Historical routing needs a separate explicit preview flow.
			}

			receipt.Status = "received"
			receipt.ErrorCode = ""
			receipt.ProcessedAt = ""
			encoded, _ := json.Marshal(receipt)
			_, e = t.sql.ExecContext(ctx, t.q(`UPDATE japanese_integration_inbox SET status='received',retry_unix=0,binding_id=?,receipt_json=? WHERE workspace_id=? AND connection_id=? AND event_id=?`), bindingID, string(encoded), workspace, c.ID, eventID)
			return e
		})
		if e != nil {
			return e
		}
		if found {
			return nil
		}
	}
	return ErrNotFound
}
func (s *Service) connections(ctx context.Context, workspace string) ([]Connection, error) {
	query := `SELECT ` + connectionColumns + ` FROM japanese_integration_connections`
	var args []any
	if workspace != "" {
		query += ` WHERE workspace_id=?`
		args = append(args, workspace)
	}
	query += ` ORDER BY created_at DESC`
	rows, e := s.cfg.ControlDB.QueryContext(ctx, s.control(query), args...)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	result := []Connection{}
	for rows.Next() {
		c, e := scanConnection(rows)
		if e != nil {
			return nil, e
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *Service) ProcessPending(ctx context.Context) error {
	connections, e := s.connections(ctx, "")
	if e != nil {
		return e
	}
	var errs []error
	for _, c := range connections {
		if c.State != "active" {
			continue
		}
		if e = s.authorize(ctx, c.WorkspaceID, c.ActorID); e != nil {
			continue
		}
		runtime, resolveErr := s.cfg.Runtimes.ResolveMobileRuntime(ctx, c.WorkspaceID)
		if resolveErr != nil {
			errs = append(errs, resolveErr)
			continue
		}
		rows, queryErr := runtime.DB.QueryContext(ctx, bind(runtime.Driver, `SELECT event_id FROM japanese_integration_inbox WHERE workspace_id=? AND connection_id=? AND status IN ('received','retry_wait') AND retry_unix<=? ORDER BY received_at,event_id LIMIT 100`), c.WorkspaceID, c.ID, s.cfg.Now().Unix())
		if queryErr != nil {
			errs = append(errs, queryErr)
			continue
		}
		ids := []string{}
		for rows.Next() {
			var id string
			if e := rows.Scan(&id); e != nil {
				errs = append(errs, e)
				break
			}
			ids = append(ids, id)
		}
		if e := rows.Err(); e != nil {
			errs = append(errs, e)
		}
		_ = rows.Close()
		for _, id := range ids {
			if e = s.process(ctx, c, id); e != nil {
				errs = append(errs, e)
			}
		}
	}
	return errors.Join(errs...)
}
func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if e := s.ProcessPending(ctx); e != nil && ctx.Err() == nil {
			slog.Warn("Japanese integration processing will retry", "error_type", fmt.Sprintf("%T", e))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
func (s *Service) process(ctx context.Context, connection Connection, id string) error {
	c, e := s.active(ctx, connection.ID)
	if e != nil {
		return e
	}
	return s.transaction(ctx, c, true, func(t tenantTx) error {
		if e := lockCatalog(ctx, t, c.ID); e != nil {
			return e
		}
		var raw, receiptRaw, bindingID, status string
		e := t.sql.QueryRowContext(ctx, t.q(`SELECT event_json,receipt_json,binding_id,status FROM japanese_integration_inbox WHERE workspace_id=? AND connection_id=? AND event_id=?`), c.WorkspaceID, c.ID, id).Scan(&raw, &receiptRaw, &bindingID, &status)
		if e != nil {
			return e
		}
		if status != "received" && status != "retry_wait" {
			return nil
		}
		var event Event
		var receipt Receipt
		if e = json.Unmarshal([]byte(raw), &event); e != nil {
			return e
		}
		if e = json.Unmarshal([]byte(receiptRaw), &receipt); e != nil {
			return e
		}
		finish := func(state, code string) error {
			receipt.Status = state
			receipt.ErrorCode = code
			receipt.ProcessedAt = s.cfg.Now().UTC().Format(time.RFC3339Nano)
			encoded, _ := json.Marshal(receipt)
			retry := int64(0)
			if state == "retry_wait" {
				retry = s.cfg.Now().Add(5 * time.Second).Unix()
			}
			_, e := t.sql.ExecContext(ctx, t.q(`UPDATE japanese_integration_inbox SET status=?,retry_unix=?,receipt_json=? WHERE workspace_id=? AND connection_id=? AND event_id=?`), state, retry, string(encoded), c.WorkspaceID, c.ID, id)
			return e
		}
		key := sourceKey(event)
		var highest int64
		if e = t.sql.QueryRowContext(ctx, t.q(`SELECT MAX(state_revision) FROM japanese_integration_inbox WHERE workspace_id=? AND connection_id=? AND source_key=?`), c.WorkspaceID, c.ID, key).Scan(&highest); e != nil {
			return e
		}
		if event.StateRevision < highest {
			return finish("stale", "")
		}
		var priorRevision int64
		var priorCompleted int
		var priorOccurrence, priorTarget string
		e = t.sql.QueryRowContext(ctx, t.q(`SELECT state_revision,completed,occurrence_id,target_task_id FROM japanese_integration_source_states WHERE workspace_id=? AND connection_id=? AND source_key=?`), c.WorkspaceID, c.ID, key).Scan(&priorRevision, &priorCompleted, &priorOccurrence, &priorTarget)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if priorRevision >= event.StateRevision {
			return finish("stale", "")
		}
		storeState := func() error {
			_, e := t.sql.ExecContext(ctx, t.q(`INSERT INTO japanese_integration_source_states(workspace_id,connection_id,source_key,state_revision,completed,occurrence_id,target_task_id) VALUES(?,?,?,?,?,?,?) ON CONFLICT(workspace_id,connection_id,source_key) DO UPDATE SET state_revision=excluded.state_revision,completed=excluded.completed,occurrence_id=excluded.occurrence_id,target_task_id=excluded.target_task_id`), c.WorkspaceID, c.ID, key, event.StateRevision, boolInt(event.Completed), receipt.OccurrenceID, receipt.TargetTaskID)
			return e
		}
		if !event.Completed {
			receipt.OccurrenceID = priorOccurrence
			receipt.TargetTaskID = priorTarget
			if e = storeState(); e != nil {
				return e
			}
			code := ""
			if priorOccurrence != "" {
				code = "source_reopened"
			}
			return finish("applied", code)
		}
		catalog, _, e := loadCatalog(ctx, t, c.ID)
		if e != nil {
			return e
		}
		if catalog.CatalogRevision < event.CatalogRevision {
			return finish("retry_wait", "catalog_pending")
		}
		binding, e := scanBinding(t.sql.QueryRowContext(ctx, t.q(`SELECT `+bindingColumns+` FROM japanese_integration_bindings WHERE workspace_id=? AND id=?`), c.WorkspaceID, bindingID))
		if errors.Is(e, sql.ErrNoRows) {
			return finish("needs_attention", "unbound")
		}
		if e != nil {
			return e
		}
		plan, slot, source, e := sourceTask(catalog, event.PlanID, event.SlotID, event.TaskID)
		if e != nil {
			return finish("needs_attention", "source_plan_changed")
		}
		if !binding.Enabled || fingerprint(plan, slot, source) != binding.Fingerprint || event.Timezone != plan.Timezone || event.StudyDate < binding.EffectiveFrom || (binding.EffectiveTo != "" && event.StudyDate >= binding.EffectiveTo) {
			return finish("needs_attention", "binding_changed")
		}
		if !scheduledOn(plan, source, event.StudyDate) {
			return finish("needs_attention", "source_not_scheduled")
		}
		aggregate, e := t.app.Reader.GetTaskAggregate(ctx, binding.TargetTaskID)
		if errors.Is(e, taskdomain.ErrTaskNotFound) {
			return finish("needs_attention", "target_deleted")
		}
		if e != nil {
			return e
		}
		if aggregate.Task.LifecycleStatus != taskdomain.TaskLifecycleActive {
			return finish("needs_attention", "target_inactive")
		}
		if aggregate.Schedule.Revision != binding.ScheduleRevision {
			return finish("needs_attention", "target_schedule_changed")
		}
		var occurrence taskdomain.QueryOccurrenceSnapshot
		if priorOccurrence != "" {
			occurrence, e = t.app.Reader.GetOccurrence(ctx, priorOccurrence)
			if errors.Is(e, taskdomain.ErrOccurrenceNotFound) {
				return finish("needs_attention", "occurrence_changed")
			}
			if e == nil && (occurrence.TaskID != binding.TargetTaskID || occurrence.OccurrenceKey != event.StudyDate) {
				return finish("needs_attention", "occurrence_changed")
			}
		} else {
			occurrence, e = resolveOccurrence(ctx, t, binding, event, aggregate)
		}
		if errors.Is(e, ErrNotFound) {
			return finish("needs_attention", "occurrence_missing")
		}
		if e != nil {
			return e
		}
		if priorOccurrence != "" && priorOccurrence != occurrence.OccurrenceID {
			return finish("needs_attention", "occurrence_changed")
		}
		receipt.OccurrenceID = occurrence.OccurrenceID
		receipt.TargetTaskID = binding.TargetTaskID
		if occurrence.Status == taskdomain.ExecutionStatusDone {
			if e = storeState(); e != nil {
				return e
			}
			return finish("already_done", "")
		}
		if occurrence.Status != taskdomain.ExecutionStatusOpen && occurrence.Status != taskdomain.ExecutionStatusActive {
			return finish("needs_attention", "target_state_conflict")
		}
		if !taskdomain.TaskCompletionRequirementsSatisfied(aggregate.Task.CompletionRequirements) {
			return finish("needs_attention", "completion_requirements")
		}
		_, e = t.app.Occurrences.Execute(ctx, taskdomain.OccurrenceCommandRequest{WorkspaceID: c.WorkspaceID, TaskID: binding.TargetTaskID, OccurrenceID: occurrence.OccurrenceID, Command: taskdomain.OccurrenceCommandComplete, ExpectedRuntimeEpoch: t.app.Epoch, Expected: taskdomain.OccurrenceCommandExpectedRevisions{Task: occurrence.TaskRevision, Schedule: occurrence.ScheduleRevision, Occurrence: occurrence.Revision}, CommandID: "japanese-" + c.ID + "-" + id, ActorID: c.ActorID, At: s.cfg.Now().UTC()})
		if e != nil {
			return e
		}
		if e = storeState(); e != nil {
			return e
		}
		return finish("applied", "")
	})
}
func scheduledOn(p *Plan, task StudyTask, date string) bool {
	if date < p.StartDate || date > p.EndDate {
		return false
	}
	parsed, _ := time.Parse("2006-01-02", date)
	day := int(parsed.Weekday())
	has := func(days []int) bool {
		for _, d := range days {
			if d == day {
				return true
			}
		}
		return false
	}
	return has(p.Weekdays) && (len(task.Weekdays) == 0 || has(task.Weekdays))
}
func resolveOccurrence(ctx context.Context, t tenantTx, b Binding, event Event, aggregate taskdomain.TaskAggregateQueryResult) (taskdomain.QueryOccurrenceSnapshot, error) {
	for _, o := range aggregate.Occurrences {
		if o.OccurrenceKey == event.StudyDate {
			return o, nil
		}
	}
	// Materialize only the requested source date with the existing generator's
	// native deterministic identity and schedule validation, including DST.
	targets, e := t.tx.ListGenerationTargets(ctx)
	if e != nil {
		return taskdomain.QueryOccurrenceSnapshot{}, e
	}
	date, _ := time.Parse("2006-01-02", event.StudyDate)
	window := taskdomain.OccurrenceWindow{From: event.StudyDate, To: date.AddDate(0, 0, 1).Format("2006-01-02")}
	for _, target := range targets {
		if target.TaskID != b.TargetTaskID || !target.GenerationEnabled {
			continue
		}
		for _, version := range target.Versions {
			keys, e := taskdomain.CalculateOccurrenceKeys(version.Schedule, version.Effective, window)
			if e != nil {
				return taskdomain.QueryOccurrenceSnapshot{}, e
			}
			for _, key := range keys {
				if key != event.StudyDate {
					continue
				}
				candidate := taskdomain.GenerationOccurrence{WorkspaceID: t.workspace, TaskID: b.TargetTaskID, ID: taskdomain.DeterministicOccurrenceID(t.workspace, b.TargetTaskID, key), OccurrenceKey: key, PlannedDate: key, GeneratedScheduleRevision: version.Revision}
				if version.Schedule.TimingType == taskdomain.TimingTimeBlock {
					times, _, e := taskdomain.ResolveTimeBlockUTC(key, version.Schedule.LocalStartTime, version.Schedule.Timezone, version.Schedule.DurationMinutes, nil)
					if e != nil {
						return taskdomain.QueryOccurrenceSnapshot{}, e
					}
					candidate.PlannedStartAt = &times.StartUTC
					candidate.PlannedEndAt = &times.EndUTC
				}
				if e = t.tx.InsertMissingOccurrences(ctx, taskdomain.GenerationInsert{WorkspaceID: t.workspace, TaskID: b.TargetTaskID, ExpectedScheduleRevision: target.ScheduleRevision, Occurrences: []taskdomain.GenerationOccurrence{candidate}}); e != nil {
					return taskdomain.QueryOccurrenceSnapshot{}, e
				}
				return t.app.Reader.GetOccurrence(ctx, candidate.ID)
			}
		}
	}
	return taskdomain.QueryOccurrenceSnapshot{}, ErrNotFound
}
