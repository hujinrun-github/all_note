package japaneseintegration

import (
	"context"
	"encoding/json"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func (s *Service) Status(ctx context.Context, workspace string) (Status, error) {
	status := Status{Connections: []Connection{}, Catalogs: []CatalogEntry{}, Bindings: []Binding{}, Events: []Receipt{}, Targets: []Target{}}
	connections, e := s.connections(ctx, workspace)
	if e != nil {
		return status, e
	}
	status.Connections = connections
	// Management HTTP adapters authorize the requesting user, while all tenant
	// access remains fixed to the explicit workspace through the runtime resolver.
	runtime, e := s.cfg.Runtimes.ResolveMobileRuntime(ctx, workspace)
	if e != nil {
		return status, e
	}
	rows, e := runtime.DB.QueryContext(ctx, bind(runtime.Driver, `SELECT connection_id,catalog_json FROM japanese_integration_catalogs WHERE workspace_id=? AND catalog_json<>''`), workspace)
	if e != nil {
		return status, e
	}
	for rows.Next() {
		var c CatalogEntry
		var raw string
		if e = rows.Scan(&c.ConnectionID, &raw); e != nil {
			rows.Close()
			return status, e
		}
		if e = json.Unmarshal([]byte(raw), &c.Catalog); e != nil {
			rows.Close()
			return status, e
		}
		status.Catalogs = append(status.Catalogs, c)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return status, e
	}
	rows.Close()
	rows, e = runtime.DB.QueryContext(ctx, bind(runtime.Driver, `SELECT `+bindingColumns+` FROM japanese_integration_bindings WHERE workspace_id=? ORDER BY effective_from DESC,binding_version DESC`), workspace)
	if e != nil {
		return status, e
	}
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			rows.Close()
			return status, err
		}
		status.Bindings = append(status.Bindings, b)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return status, e
	}
	rows.Close()
	rows, e = runtime.DB.QueryContext(ctx, bind(runtime.Driver, `SELECT receipt_json FROM japanese_integration_inbox WHERE workspace_id=? ORDER BY received_at DESC,event_id DESC LIMIT 100`), workspace)
	if e != nil {
		return status, e
	}
	for rows.Next() {
		var raw string
		var r Receipt
		if e = rows.Scan(&raw); e != nil {
			rows.Close()
			return status, e
		}
		if e = json.Unmarshal([]byte(raw), &r); e != nil {
			rows.Close()
			return status, e
		}
		status.Events = append(status.Events, r)
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return status, e
	}
	rows.Close()
	definitions, e := runtime.Application.Reader.ListTaskDefinitions(ctx, taskdomain.TaskDefinitionListFilter{})
	if e != nil {
		return status, e
	}
	for _, definition := range definitions {
		if definition.Task.LifecycleStatus != taskdomain.TaskLifecycleActive {
			continue
		}
		aggregate, e := runtime.Application.Reader.GetTaskAggregate(ctx, definition.Task.ID)
		if e != nil {
			return status, e
		}
		schedule, e := currentSchedule(aggregate)
		if e != nil {
			return status, e
		}
		if !aggregate.Aggregate.Recurring || schedule.TimingType == taskdomain.TimingUnscheduled {
			continue
		}
		status.Targets = append(status.Targets, Target{TaskID: definition.Task.ID, Title: definition.Task.Title, Timezone: schedule.Timezone, RecurrenceType: string(schedule.RecurrenceType)})
	}
	return status, nil
}
