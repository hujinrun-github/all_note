package planstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hujinrun/flowspace/internal/taskdomain"
	"github.com/hujinrun/flowspace/internal/webplanning"
)

type preparedItem struct {
	item     webplanning.Item
	snapshot taskdomain.TaskAggregateSnapshot
}

func (r *reader) project(ctx context.Context, id string) (taskdomain.ProjectSnapshot, error) {
	if r.runtime.Application.Reader == nil {
		return taskdomain.ProjectSnapshot{}, webplanning.ErrUnavailable
	}
	if id != "" {
		project, err := r.runtime.Application.Reader.GetProject(ctx, id)
		if errors.Is(err, taskdomain.ErrProjectNotFound) {
			return project, fmt.Errorf("%w: 目标项目不存在", webplanning.ErrInvalid)
		}
		return project, err
	}
	projects, err := r.runtime.Application.Reader.ListProjects(ctx, taskdomain.ProjectListFilter{})
	if err != nil {
		return taskdomain.ProjectSnapshot{}, err
	}
	for _, project := range projects {
		if project.Project.SystemRole == taskdomain.ProjectSystemRoleInbox {
			return project, nil
		}
	}
	return taskdomain.ProjectSnapshot{}, fmt.Errorf("%w: workspace inbox project is missing", webplanning.ErrInvalid)
}

func (r *reader) prepare(ctx context.Context, draft webplanning.Draft) (webplanning.Validation, []preparedItem, taskdomain.ProjectSnapshot, error) {
	v := webplanning.Validation{DraftRevision: draft.Revision, Issues: []string{}, Conflicts: []string{}}
	if draft.WorkspaceID != r.scope.WorkspaceID || draft.OwnerID != r.scope.ActorID {
		return v, nil, taskdomain.ProjectSnapshot{}, webplanning.ErrNotFound
	}
	project, err := r.project(ctx, draft.ProjectID)
	if err != nil {
		return v, nil, project, err
	}
	if len(draft.Items) > webplanning.MaxItems {
		v.Issues = append(v.Issues, "每份计划最多20个任务")
	}
	location, err := time.LoadLocation(draft.Timezone)
	if err != nil {
		v.Issues = append(v.Issues, "请选择有效时区")
		return v, nil, project, nil
	}
	now := r.now().UTC()
	seen := map[string]bool{}
	prepared := []preparedItem{}
	for _, item := range draft.Items {
		if !item.Selected {
			continue
		}
		if strings.TrimSpace(item.ID) == "" || seen[item.ID] {
			v.Issues = append(v.Issues, "任务标识缺失或重复")
			continue
		}
		seen[item.ID] = true
		if item.DueAt != nil && item.Schedule.RecurrenceType != taskdomain.RecurrenceNone {
			v.Issues = append(v.Issues, item.Title+"：重复任务暂不支持独立截止时刻")
			continue
		}
		snapshot, _, err := taskdomain.BuildTaskAggregateSnapshot(taskdomain.TaskCreationInput{
			WorkspaceID: r.scope.WorkspaceID, Project: project.Project.Identity(), TaskID: itemTaskID(r.scope.WorkspaceID, draft.ID, item.ID), ActorID: r.scope.ActorID, ActorTime: now,
			Title: item.Title, Description: item.Description, Priority: item.Priority, SortOrder: float64(len(prepared)), Schedule: item.Schedule.Input(), DueAt: item.DueAt,
		})
		if err != nil {
			v.Issues = append(v.Issues, fmt.Sprintf("%s：%s", item.Title, err))
			continue
		}
		prepared = append(prepared, preparedItem{item: item, snapshot: snapshot})
	}
	if len(prepared) == 0 {
		v.Issues = append(v.Issues, "请至少选择一个有效任务")
	}
	local := now.In(location)
	from := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location)
	to := from.AddDate(0, 0, 14)
	existing, err := r.runtime.Application.Reader.ListOccurrences(ctx, taskdomain.OccurrenceListFilter{Scope: taskdomain.OccurrenceListCalendar, From: from, To: to, Timezone: draft.Timezone})
	if err != nil {
		return v, nil, project, err
	}
	type span struct {
		title      string
		start, end time.Time
	}
	busy := []span{}
	for _, occ := range existing {
		if occ.LifecycleStatus != taskdomain.TaskLifecycleActive || occ.PlannedStartAt == nil || occ.PlannedEndAt == nil || occ.Status == taskdomain.ExecutionStatusCancelled || occ.Status == taskdomain.ExecutionStatusSkipped || occ.Status == taskdomain.ExecutionStatusDone {
			continue
		}
		busy = append(busy, span{occ.Title, *occ.PlannedStartAt, *occ.PlannedEndAt})
	}
	for _, candidate := range prepared {
		for _, occ := range candidate.snapshot.Occurrences {
			if occ.PlannedStartAt == nil || occ.PlannedEndAt == nil || !occ.PlannedStartAt.Before(to) || !occ.PlannedEndAt.After(from) {
				continue
			}
			for _, other := range busy {
				if occ.PlannedStartAt.Before(other.end) && other.start.Before(*occ.PlannedEndAt) {
					v.Conflicts = append(v.Conflicts, fmt.Sprintf("%s 与 %s 在 %s 时间重叠", candidate.item.Title, other.title, occ.PlannedStartAt.In(location).Format("01-02 15:04")))
				}
			}
			busy = append(busy, span{candidate.item.Title, *occ.PlannedStartAt, *occ.PlannedEndAt})
		}
	}
	return v, prepared, project, nil
}

func (r *reader) ValidatePlan(ctx context.Context, draft webplanning.Draft) (webplanning.Validation, error) {
	if err := r.owned(); err != nil {
		return webplanning.Validation{}, err
	}
	v, _, _, err := r.prepare(ctx, draft)
	return v, err
}

func itemTaskID(workspace, draft, item string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan-task\x00"+workspace+"\x00"+draft+"\x00"+item)).String()
}

func (t *transaction) CommitPlan(ctx context.Context, request webplanning.CommitRequest) (webplanning.Receipt, error) {
	var empty webplanning.Receipt
	if !t.taskWrite || strings.TrimSpace(request.RequestID) == "" || request.ExpectedRevision < 1 {
		return empty, webplanning.ErrInvalid
	}
	encoded, err := json.Marshal(struct {
		DraftID                 string
		Revision                int64
		Publish, AllowConflicts bool
		NewProject              any
	}{request.DraftID, request.ExpectedRevision, request.Publish, request.AllowConflicts, request.NewProject})
	if err != nil {
		return empty, err
	}
	sum := sha256.Sum256(encoded)
	hash := hex.EncodeToString(sum[:])
	if receipt, err := t.GetCommitByKey(ctx, request.RequestID); err == nil {
		if receipt.DraftID != request.DraftID || receipt.RequestHash != hash {
			return empty, webplanning.ErrConflict
		}
		return receipt, nil
	} else if !errors.Is(err, webplanning.ErrNotFound) {
		return empty, err
	}
	if receipt, err := t.GetCommitByDraft(ctx, request.DraftID); err == nil {
		if receipt.RequestHash != hash {
			return empty, webplanning.ErrConflict
		}
		return receipt, nil
	} else if !errors.Is(err, webplanning.ErrNotFound) {
		return empty, err
	}
	draft, err := t.GetDraft(ctx, request.DraftID)
	if err != nil {
		return empty, err
	}
	if draft.Revision != request.ExpectedRevision || draft.Status == "committed" || draft.Status == "discarded" {
		return empty, webplanning.ErrConflict
	}
	app := t.runtime.Application
	now := t.now().UTC()
	if request.NewProject != nil {
		input := request.NewProject
		if strings.TrimSpace(input.Name) == "" || draft.ProjectID != "" {
			return empty, webplanning.ErrInvalid
		}
		kind, horizon := input.Kind, input.Horizon
		if kind == "" {
			kind = taskdomain.ProjectKindStandard
		}
		if horizon == "" {
			horizon = taskdomain.ProjectHorizonShort
		}
		id := uuid.NewSHA1(uuid.NameSpaceOID, []byte("plan-project\x00"+t.scope.WorkspaceID+"\x00"+draft.ID)).String()
		_, err = app.Projects.CreateProject(ctx, taskdomain.CreateProjectRequest{WorkspaceID: t.scope.WorkspaceID, ExpectedRuntimeEpoch: t.runtime.Epoch, Project: taskdomain.Project{WorkspaceID: t.scope.WorkspaceID, ID: id, Name: strings.TrimSpace(input.Name), Kind: kind, Horizon: horizon, Status: taskdomain.ProjectStatusPlanning}, CommandID: request.RequestID, ActorID: t.scope.ActorID, At: now})
		if err != nil {
			if errors.Is(err, taskdomain.ErrInvalidProject) || errors.Is(err, taskdomain.ErrInvalidProjectCommand) {
				return empty, fmt.Errorf("%w: 新项目设置无效", webplanning.ErrInvalid)
			}
			return empty, err
		}
		draft.ProjectID = id
	}
	validation, items, project, err := t.prepare(ctx, draft)
	if err != nil {
		return empty, err
	}
	if len(validation.Issues) > 0 {
		return empty, fmt.Errorf("%w: %s", webplanning.ErrInvalid, strings.Join(validation.Issues, "；"))
	}
	if request.Publish && len(validation.Conflicts) > 0 && !request.AllowConflicts {
		return empty, fmt.Errorf("%w: %s", webplanning.ErrConflict, strings.Join(validation.Conflicts, "；"))
	}
	if project.Project.Status != taskdomain.ProjectStatusPlanning && project.Project.Status != taskdomain.ProjectStatusActive {
		return empty, fmt.Errorf("%w: 项目必须处于规划或进行中", webplanning.ErrConflict)
	}
	if request.Publish && project.Project.Status == taskdomain.ProjectStatusPlanning {
		_, err = app.Projects.ActivateProject(ctx, taskdomain.ExistingProjectRequest{WorkspaceID: t.scope.WorkspaceID, ProjectID: project.Project.ID, Command: taskdomain.ProjectCommandActivate, ExpectedRuntimeEpoch: t.runtime.Epoch, ExpectedProjectRevision: project.Revision, CommandID: request.RequestID, ActorID: t.scope.ActorID, At: now})
		if err != nil {
			return empty, err
		}
	}
	receipt := webplanning.Receipt{DraftID: draft.ID, ProjectID: project.Project.ID, TaskIDs: []string{}, OccurrenceIDs: []string{}, RequestID: request.RequestID, RequestHash: hash, CreatedAt: now}
	for _, item := range items {
		snapshot := item.snapshot
		outcome, err := app.Tasks.CreateTask(ctx, taskdomain.CreateTaskRequest{WorkspaceID: t.scope.WorkspaceID, ExpectedRuntimeEpoch: t.runtime.Epoch, Snapshot: snapshot, CommandID: request.RequestID, ActorID: t.scope.ActorID, At: now})
		if err != nil {
			return empty, err
		}
		if request.Publish {
			_, err = app.Tasks.ExecuteLifecycleCommand(ctx, taskdomain.LifecycleCommandRequest{WorkspaceID: t.scope.WorkspaceID, TaskID: snapshot.Task.ID, Command: taskdomain.TaskCommandPublish, ExpectedRuntimeEpoch: t.runtime.Epoch, Expected: taskdomain.LifecycleExpectedRevisions{Task: outcome.TaskRevision, Schedule: outcome.ScheduleRevision, Occurrences: map[string]int64{}}, CommandID: request.RequestID, ActorID: t.scope.ActorID, At: now})
			if err != nil {
				return empty, err
			}
		}
		receipt.TaskIDs = append(receipt.TaskIDs, snapshot.Task.ID)
		for _, occ := range snapshot.Occurrences {
			receipt.OccurrenceIDs = append(receipt.OccurrenceIDs, occ.ID)
		}
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return empty, err
	}
	_, err = t.runner.ExecContext(ctx, t.bind(`INSERT INTO plan_commits(workspace_id,owner_id,draft_id,request_id,request_hash,receipt_json,created_at) VALUES(?,?,?,?,?,?,?)`), t.scope.WorkspaceID, t.scope.ActorID, draft.ID, request.RequestID, hash, string(raw), stamp(now))
	if err != nil {
		return empty, err
	}
	expected := draft.Revision
	draft.Revision++
	draft.Status = "committed"
	draft.ProjectID = project.Project.ID
	draft.UpdatedAt = now
	if err := t.PutDraft(ctx, draft, expected); err != nil {
		return empty, err
	}
	return receipt, nil
}
