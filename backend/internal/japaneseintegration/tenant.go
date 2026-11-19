package japaneseintegration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/hujinrun/flowspace/internal/storage"
	"github.com/hujinrun/flowspace/internal/taskapp"
	"github.com/hujinrun/flowspace/internal/taskdomain"
)

type tenantTx struct {
	sql       storage.TenantSQLRunner
	app       taskapp.RuntimeSnapshot
	tx        storage.MobileV2TenantWriteTx
	driver    storage.Driver
	workspace string
}

func (t tenantTx) q(query string) string { return bind(t.driver, query) }
func (s *Service) transaction(ctx context.Context, c Connection, taskWrite bool, fn func(tenantTx) error) error {
	runtime, e := s.cfg.Runtimes.ResolveMobileRuntime(ctx, c.WorkspaceID)
	if e != nil {
		return e
	}
	if runtime.WorkspaceID != c.WorkspaceID || runtime.Writer == nil {
		return ErrUnavailable
	}
	callback := func(raw storage.MobileV2TenantWriteTx) error {
		app, e := transactionApplication(raw, c.WorkspaceID, runtime.Epoch)
		if e != nil {
			return e
		}
		return fn(tenantTx{sql: raw.MobileV2SQLRunner(), app: app, tx: raw, driver: runtime.Driver, workspace: c.WorkspaceID})
	}
	if taskWrite {
		return runtime.Writer.BeginFencedWrite(ctx, c.WorkspaceID, runtime.Epoch, func(tx storage.TenantWriteTx) error {
			v, ok := tx.(storage.MobileV2TenantWriteTx)
			if !ok {
				return ErrUnavailable
			}
			return callback(v)
		})
	}
	writer, ok := runtime.Writer.(storage.MobileV2TenantFencedWriter)
	if !ok {
		return ErrUnavailable
	}
	return writer.BeginFencedMobileV2Write(ctx, c.WorkspaceID, runtime.Epoch, callback)
}
func (s *Service) initializeConnection(ctx context.Context, c Connection) error {
	return s.transaction(ctx, c, false, func(t tenantTx) error {
		_, e := t.sql.ExecContext(ctx, t.q(`INSERT INTO japanese_integration_catalogs(workspace_id,connection_id) VALUES(?,?) ON CONFLICT DO NOTHING`), c.WorkspaceID, c.ID)
		return e
	})
}
func lockCatalog(ctx context.Context, t tenantTx, connection string) error {
	result, e := t.sql.ExecContext(ctx, t.q(`UPDATE japanese_integration_catalogs SET catalog_revision=catalog_revision WHERE workspace_id=? AND connection_id=?`), t.workspace, connection)
	if e != nil {
		return e
	}
	n, _ := result.RowsAffected()
	if n != 1 {
		return ErrNotFound
	}
	return nil
}
func loadCatalog(ctx context.Context, t tenantTx, connection string) (Catalog, string, error) {
	var raw, hash string
	e := t.sql.QueryRowContext(ctx, t.q(`SELECT catalog_json,catalog_hash FROM japanese_integration_catalogs WHERE workspace_id=? AND connection_id=?`), t.workspace, connection).Scan(&raw, &hash)
	if e != nil {
		return Catalog{}, "", e
	}
	if raw == "" {
		return Catalog{}, hash, nil
	}
	var c Catalog
	e = json.Unmarshal([]byte(raw), &c)
	return c, hash, e
}
func validateCatalog(c Catalog) error {
	if c.SchemaVersion != 1 || !validID(c.SourceInstanceID) || !validID(c.SourceUserID) || c.CatalogRevision < 1 {
		return ErrInvalid
	}
	if c.Plan == nil {
		return nil
	}
	p := c.Plan
	if !validID(p.PlanID) || p.Revision < 1 || !validID(p.Title) || !validDate(p.StartDate) || !validDate(p.EndDate) || p.EndDate < p.StartDate || len(p.TimeSlots) > 24 || len(p.Weekdays) == 0 {
		return ErrInvalid
	}
	start, _ := time.Parse("2006-01-02", p.StartDate)
	end, _ := time.Parse("2006-01-02", p.EndDate)
	if end.Sub(start) > 366*24*time.Hour {
		return ErrInvalid
	}
	if _, e := time.LoadLocation(p.Timezone); e != nil {
		return ErrInvalid
	}
	if !validWeekdays(p.Weekdays) {
		return ErrInvalid
	}
	slots := map[string]bool{}
	for _, slot := range p.TimeSlots {
		if !validID(slot.ID) || slots[slot.ID] || !validID(slot.Title) || slot.DurationMinutes < 1 || slot.DurationMinutes > 480 || len(slot.Tasks) > 24 {
			return ErrInvalid
		}
		if _, e := time.Parse("15:04", slot.Time); e != nil {
			return ErrInvalid
		}
		slots[slot.ID] = true
		tasks := map[string]bool{}
		for _, task := range slot.Tasks {
			if !validID(task.ID) || tasks[task.ID] || !validID(task.Title) || !strings.HasPrefix(task.Path, "/") || strings.HasPrefix(task.Path, "//") || len(task.Path) > 2000 || task.Minutes < 1 || !validWeekdays(task.Weekdays) {
				return ErrInvalid
			}
			tasks[task.ID] = true
		}
	}
	return nil
}
func validWeekdays(days []int) bool {
	seen := map[int]bool{}
	for _, d := range days {
		if d < 0 || d > 6 || seen[d] {
			return false
		}
		seen[d] = true
	}
	return true
}
func validDate(date string) bool {
	parsed, e := time.Parse("2006-01-02", date)
	return e == nil && parsed.Format("2006-01-02") == date
}
func (s *Service) PutCatalog(ctx context.Context, connection Connection, catalog Catalog) error {
	c, e := s.active(ctx, connection.ID)
	if e != nil {
		return e
	}
	if catalog.SourceInstanceID != c.SourceInstanceID || catalog.SourceUserID != c.SourceUserID {
		return ErrUnauthorized
	}
	if e = validateCatalog(catalog); e != nil {
		return e
	}
	raw, e := json.Marshal(catalog)
	if e != nil {
		return e
	}
	if len(raw) > 128*1024 {
		return ErrInvalid
	}
	hash := digest(raw)
	return s.transaction(ctx, c, false, func(t tenantTx) error {
		if e := lockCatalog(ctx, t, c.ID); e != nil {
			return e
		}
		current, oldHash, e := loadCatalog(ctx, t, c.ID)
		if e != nil {
			return e
		}
		if current.CatalogRevision > catalog.CatalogRevision {
			return nil
		}
		if current.CatalogRevision == catalog.CatalogRevision {
			if oldHash != hash {
				return ErrConflict
			}
			return nil
		}
		_, e = t.sql.ExecContext(ctx, t.q(`UPDATE japanese_integration_catalogs SET catalog_revision=?,catalog_hash=?,catalog_json=? WHERE workspace_id=? AND connection_id=?`), catalog.CatalogRevision, hash, string(raw), c.WorkspaceID, c.ID)
		if e != nil {
			return e
		}
		return disableObsoleteBindings(ctx, t, c.ID, catalog)
	})
}
func sourceTask(c Catalog, plan, slotID, taskID string) (*Plan, Slot, StudyTask, error) {
	if c.Plan == nil || c.Plan.PlanID != plan {
		return nil, Slot{}, StudyTask{}, ErrNotFound
	}
	for _, slot := range c.Plan.TimeSlots {
		if slot.ID == slotID {
			for _, task := range slot.Tasks {
				if task.ID == taskID {
					return c.Plan, slot, task, nil
				}
			}
		}
	}
	return nil, Slot{}, StudyTask{}, ErrNotFound
}
func fingerprint(p *Plan, slot Slot, task StudyTask) string {
	// Presentation-only edits deliberately do not invalidate a task identity.
	value := struct {
		PlanID, Timezone, Start, End, SlotID, Time, TaskID, Path string
		Duration, Minutes                                        int
		PlanDays, TaskDays                                       []int
	}{p.PlanID, p.Timezone, p.StartDate, p.EndDate, slot.ID, slot.Time, task.ID, task.Path, slot.DurationMinutes, task.Minutes, p.Weekdays, task.Weekdays}
	raw, _ := json.Marshal(value)
	return digest(raw)
}

const bindingColumns = `id,connection_id,plan_id,slot_id,source_task_id,target_task_id,target_title,binding_version,effective_from,effective_to,schedule_revision,source_fingerprint,enabled`

func scanBinding(row interface{ Scan(...any) error }) (Binding, error) {
	var b Binding
	var enabled int
	e := row.Scan(&b.ID, &b.ConnectionID, &b.PlanID, &b.SlotID, &b.TaskID, &b.TargetTaskID, &b.TargetTitle, &b.Version, &b.EffectiveFrom, &b.EffectiveTo, &b.ScheduleRevision, &b.Fingerprint, &enabled)
	b.Enabled = enabled == 1
	return b, e
}
func currentSchedule(aggregate taskdomain.TaskAggregateQueryResult) (taskdomain.ScheduleVersion, error) {
	for _, v := range aggregate.Versions {
		if v.ScheduleRevision == aggregate.Schedule.CurrentScheduleRevision {
			return v, nil
		}
	}
	return taskdomain.ScheduleVersion{}, ErrUnavailable
}
func (s *Service) SaveBinding(ctx context.Context, workspace, actor string, input BindingInput) (Binding, error) {
	if e := s.authorize(ctx, workspace, actor); e != nil {
		return Binding{}, e
	}
	c, e := s.active(ctx, input.ConnectionID)
	if e != nil {
		return Binding{}, e
	}
	if c.WorkspaceID != workspace {
		return Binding{}, ErrUnauthorized
	}
	if !validID(input.PlanID) || !validID(input.SlotID) || !validID(input.TaskID) || !validID(input.TargetTaskID) {
		return Binding{}, ErrInvalid
	}
	var result Binding
	e = s.transaction(ctx, c, false, func(t tenantTx) error {
		if e := lockCatalog(ctx, t, c.ID); e != nil {
			return e
		}
		// SQLite already serializes writes. An advisory workspace lock prevents
		// cross-connection target interval races on PostgreSQL without upgrading
		// the runtime fence's shared row lock (which can cause deadlocks).
		if t.driver == storage.DriverPostgres {
			if _, e := t.sql.ExecContext(ctx, t.q(`SELECT pg_advisory_xact_lock(hashtextextended(?,0))`), "japanese-bindings:"+workspace); e != nil {
				return e
			}
		}
		catalog, _, e := loadCatalog(ctx, t, c.ID)
		if e != nil {
			return e
		}
		plan, slot, source, e := sourceTask(catalog, input.PlanID, input.SlotID, input.TaskID)
		if e != nil {
			return e
		}
		aggregate, e := t.app.Reader.GetTaskAggregate(ctx, input.TargetTaskID)
		if e != nil {
			return e
		}
		schedule, e := currentSchedule(aggregate)
		if e != nil {
			return e
		}
		if !aggregate.Aggregate.Recurring || schedule.TimingType == taskdomain.TimingUnscheduled || schedule.Timezone != plan.Timezone || aggregate.Task.LifecycleStatus != taskdomain.TaskLifecycleActive {
			return ErrInvalid
		}
		location, _ := time.LoadLocation(plan.Timezone)
		today := s.cfg.Now().In(location).Format("2006-01-02")
		previous, previousErr := scanBinding(t.sql.QueryRowContext(ctx, t.q(`SELECT `+bindingColumns+` FROM japanese_integration_bindings WHERE workspace_id=? AND connection_id=? AND plan_id=? AND slot_id=? AND source_task_id=? AND effective_to='' ORDER BY binding_version DESC LIMIT 1`), workspace, c.ID, input.PlanID, input.SlotID, input.TaskID))
		if previousErr != nil && !errors.Is(previousErr, sql.ErrNoRows) {
			return previousErr
		}
		from := input.EffectiveFrom
		if from == "" {
			from = today
			if from < plan.StartDate {
				from = plan.StartDate
			}
			if previousErr == nil {
				d, _ := time.Parse("2006-01-02", today)
				from = d.AddDate(0, 0, 1).Format("2006-01-02")
			}
		}
		if !validDate(from) || from < today || from < plan.StartDate || from > plan.EndDate {
			return ErrInvalid
		}
		if previousErr == nil && from <= previous.EffectiveFrom {
			return ErrConflict
		}
		enabled := input.Enabled == nil || *input.Enabled
		if enabled {
			if e = validateBindingDates(ctx, t, plan, source, input.TargetTaskID, from); e != nil {
				return e
			}
		}
		if previousErr == nil {
			if _, e = t.sql.ExecContext(ctx, t.q(`UPDATE japanese_integration_bindings SET effective_to=? WHERE workspace_id=? AND id=? AND effective_to=''`), from, workspace, previous.ID); e != nil {
				return e
			}
		}
		if enabled {
			var overlaps int
			if e = t.sql.QueryRowContext(ctx, t.q(`SELECT COUNT(*) FROM japanese_integration_bindings WHERE workspace_id=? AND target_task_id=? AND enabled=1 AND (effective_to='' OR effective_to>?)`), workspace, input.TargetTaskID, from).Scan(&overlaps); e != nil {
				return e
			}
			if overlaps > 0 {
				return fmt.Errorf("%w: target binding intervals overlap", ErrConflict)
			}
		}
		version := int64(1)
		if previousErr == nil {
			version = previous.Version + 1
		}
		id, e := randomID()
		if e != nil {
			return e
		}
		result = Binding{ID: id, ConnectionID: c.ID, PlanID: input.PlanID, SlotID: input.SlotID, TaskID: input.TaskID, TargetTaskID: input.TargetTaskID, TargetTitle: aggregate.Task.Title, Version: version, EffectiveFrom: from, ScheduleRevision: aggregate.Schedule.Revision, Fingerprint: fingerprint(plan, slot, source), Enabled: enabled}
		_, e = t.sql.ExecContext(ctx, t.q(`INSERT INTO japanese_integration_bindings(workspace_id,id,connection_id,plan_id,slot_id,source_task_id,target_task_id,target_title,binding_version,effective_from,effective_to,schedule_revision,source_fingerprint,enabled) VALUES(?,?,?,?,?,?,?,?,?,?,'',?,?,?)`), workspace, result.ID, c.ID, result.PlanID, result.SlotID, result.TaskID, result.TargetTaskID, result.TargetTitle, result.Version, from, result.ScheduleRevision, result.Fingerprint, boolInt(enabled))
		if e != nil {
			return fmt.Errorf("%w: overlapping source or target binding", ErrConflict)
		}
		return nil
	})
	return result, e
}
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func (s *Service) bindingFor(ctx context.Context, t tenantTx, c Connection, event Event) (Binding, error) {
	b, e := scanBinding(t.sql.QueryRowContext(ctx, t.q(`SELECT `+bindingColumns+` FROM japanese_integration_bindings WHERE workspace_id=? AND connection_id=? AND plan_id=? AND slot_id=? AND source_task_id=? AND effective_from<=? AND (effective_to='' OR effective_to>?) AND enabled=1 ORDER BY binding_version DESC LIMIT 1`), c.WorkspaceID, c.ID, event.PlanID, event.SlotID, event.TaskID, event.StudyDate, event.StudyDate))
	if errors.Is(e, sql.ErrNoRows) {
		return b, ErrNotFound
	}
	return b, e
}

// transactionApplication composes existing occurrence commands over the
// already-fenced SQL transaction; there is no nested transaction or SQL domain
// state update at the integration boundary.
func transactionApplication(tx storage.MobileV2TenantWriteTx, workspace string, epoch int64) (taskapp.RuntimeSnapshot, error) {
	reader := tx.TaskDomainReader()
	states, ok := reader.(taskdomain.TaskAggregateStateReader)
	if !ok {
		return taskapp.RuntimeSnapshot{}, ErrUnavailable
	}
	fencer := integrationTransactionFencer{tx: tx, workspace: workspace, epoch: epoch}
	return taskapp.RuntimeSnapshot{WorkspaceID: workspace, Epoch: epoch, Reader: reader, Occurrences: taskapp.DomainOccurrenceServiceAdapter{Service: taskdomain.NewOccurrenceService(fencer, states)}}, nil
}

type integrationTransactionFencer struct {
	tx        storage.MobileV2TenantWriteTx
	workspace string
	epoch     int64
}

func (f integrationTransactionFencer) BeginFencedWrite(_ context.Context, workspace string, epoch int64, callback func(taskdomain.TaskDomainFencedTx) error) error {
	if workspace != f.workspace || epoch != f.epoch || callback == nil || f.tx == nil {
		return taskdomain.ErrInvalidTaskCommand
	}
	return callback(f.tx)
}

// validateBindingDates previews the complete bounded source plan through the
// native generator. Extra target dates are allowed; a missing source date or
// empty source selection must be resolved before claiming a successful link.
func validateBindingDates(ctx context.Context, t tenantTx, plan *Plan, source StudyTask, targetID, from string) error {
	end, _ := time.Parse("2006-01-02", plan.EndDate)
	window := taskdomain.OccurrenceWindow{From: from, To: end.AddDate(0, 0, 1).Format("2006-01-02")}
	targets, e := t.tx.ListGenerationTargets(ctx)
	if e != nil {
		return e
	}
	dates := map[string]bool{}
	for _, target := range targets {
		if target.TaskID != targetID || !target.GenerationEnabled {
			continue
		}
		for _, version := range target.Versions {
			keys, e := taskdomain.CalculateOccurrenceKeys(version.Schedule, version.Effective, window)
			if e != nil {
				return e
			}
			for _, key := range keys {
				dates[key] = true
			}
		}
	}
	date, _ := time.Parse("2006-01-02", from)
	count := 0
	for !date.After(end) {
		key := date.Format("2006-01-02")
		if scheduledOn(plan, source, key) {
			count++
			if !dates[key] {
				return fmt.Errorf("%w: target schedule does not cover source study date %s", ErrInvalid, key)
			}
		}
		date = date.AddDate(0, 0, 1)
	}
	if count == 0 {
		return fmt.Errorf("%w: no scheduled source tasks in binding interval", ErrInvalid)
	}
	return nil
}

// disableObsoleteBindings releases target reservations when the authoritative
// source removes or changes a task. IDs and routing history remain intact, so
// a queued event cannot silently migrate to the replacement binding.
func disableObsoleteBindings(ctx context.Context, t tenantTx, connectionID string, catalog Catalog) error {
	rows, e := t.sql.QueryContext(ctx, t.q(`SELECT `+bindingColumns+` FROM japanese_integration_bindings WHERE workspace_id=? AND connection_id=? AND enabled=1`), t.workspace, connectionID)
	if e != nil {
		return e
	}
	ids := []string{}
	for rows.Next() {
		binding, e := scanBinding(rows)
		if e != nil {
			rows.Close()
			return e
		}
		plan, slot, source, e := sourceTask(catalog, binding.PlanID, binding.SlotID, binding.TaskID)
		if errors.Is(e, ErrNotFound) || (e == nil && fingerprint(plan, slot, source) != binding.Fingerprint) {
			ids = append(ids, binding.ID)
		} else if e != nil {
			rows.Close()
			return e
		}
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return e
	}
	if e = rows.Close(); e != nil {
		return e
	}
	for _, id := range ids {
		if _, e = t.sql.ExecContext(ctx, t.q(`UPDATE japanese_integration_bindings SET enabled=0 WHERE workspace_id=? AND id=?`), t.workspace, id); e != nil {
			return e
		}
	}
	return nil
}
