package taskapp

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/hujinrun/flowspace/internal/taskdomain"
)

func TestFacadeCreateTaskResolvesRuntimeAndBuildsSemanticSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 30, 0, 0, time.UTC)
	factory := &factoryFake{snapshot: semanticSnapshotForTest()}
	tasks := &taskServiceFake{createResult: TaskCommandOutcome{TaskRevision: 1, ScheduleRevision: 1, LifecycleStatus: taskdomain.TaskLifecycleDraft, CommandID: "command-1"}}
	runtime := runtimeForTest(11, factory, tasks)
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime}}
	facade := NewFacade(resolver, fixedClock{now: now}, &idGeneratorFake{ids: []string{"task-1"}}, &commandIDGeneratorFake{ids: []string{"command-1"}})
	request := CreateTaskRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1",
		Project:  taskdomain.ProjectIdentity{WorkspaceID: "workspace-1", ProjectID: "project-1"},
		Roadmap:  &taskdomain.Roadmap{WorkspaceID: "workspace-1", ID: "roadmap-1", ProjectID: "project-1"},
		TaskNote: &taskdomain.TaskNoteIdentity{WorkspaceID: "workspace-1", NoteID: "note-1"},
		Title:    "Task", Description: "Description", Priority: 2, SortOrder: 3.5,
		Schedule: taskdomain.ScheduleInput{RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingUnscheduled, Timezone: "UTC"},
	}

	result, err := facade.CreateTask(context.Background(), request)
	if err != nil {
		t.Fatalf("CreateTask() error = %v", err)
	}
	if resolver.calls != 1 || !reflect.DeepEqual(resolver.workspaceIDs, []string{"workspace-1"}) {
		t.Fatalf("resolver calls/workspaces = %d/%v", resolver.calls, resolver.workspaceIDs)
	}
	if factory.calls != 1 || factory.input.TaskID != "task-1" || factory.input.ActorID != "user-1" || !factory.input.ActorTime.Equal(now) ||
		factory.input.WorkspaceID != request.WorkspaceID || factory.input.Project != request.Project || factory.input.Title != request.Title {
		t.Fatalf("factory input = %#v", factory.input)
	}
	if tasks.createCalls != 1 || tasks.createRequest.ExpectedRuntimeEpoch != 11 || tasks.createRequest.CommandID != "command-1" ||
		tasks.createRequest.ActorID != "user-1" || !tasks.createRequest.At.Equal(now) || !reflect.DeepEqual(tasks.createRequest.Snapshot, factory.snapshot) {
		t.Fatalf("task service request = %#v", tasks.createRequest)
	}
	if result.TaskID != "task-1" || result.TaskRevision != 1 || result.ScheduleRevision != 1 || result.LifecycleStatus != taskdomain.TaskLifecycleDraft || result.CommandID != "command-1" {
		t.Fatalf("application result = %#v", result)
	}
}

func TestFacadeCreateTaskReturnsDefensiveOccurrenceAfterImages(t *testing.T) {
	start := testFacadeNow().Add(time.Hour)
	end := start.Add(30 * time.Minute)
	due := end.Add(time.Hour)
	snapshot := semanticSnapshotForTest()
	snapshot.Occurrences = []taskdomain.OccurrenceRecord{{
		WorkspaceID: "workspace-1", ID: "occ-1", TaskID: "task-1", OccurrenceKey: "once",
		PlannedDate: "2026-07-22", PlannedStartAt: &start, PlannedEndAt: &end, DueAt: &due,
		ExecutionStatus: taskdomain.ExecutionStatusOpen, Revision: 1, GeneratedScheduleRevision: 1,
	}}
	factory := &factoryFake{snapshot: snapshot}
	tasks := &taskServiceFake{}
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtimeForTest(1, factory, tasks)}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{ids: []string{"task-1"}}, &commandIDGeneratorFake{ids: []string{"cmd-1"}})

	result, err := facade.CreateTask(context.Background(), CreateTaskRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", Project: taskdomain.ProjectIdentity{WorkspaceID: "workspace-1", ProjectID: "project-1"},
		Title: "Task", Schedule: taskdomain.ScheduleInput{RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingUnscheduled, Timezone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Occurrences) != 1 || result.Occurrences[0].ID != "occ-1" {
		t.Fatalf("create occurrences = %#v", result.Occurrences)
	}
	result.Occurrences[0].ID = "mutated"
	*result.Occurrences[0].PlannedStartAt = time.Time{}
	*result.Occurrences[0].PlannedEndAt = time.Time{}
	*result.Occurrences[0].DueAt = time.Time{}
	if factory.snapshot.Occurrences[0].ID != "occ-1" || tasks.createRequest.Snapshot.Occurrences[0].ID != "occ-1" ||
		factory.snapshot.Occurrences[0].PlannedStartAt.IsZero() || tasks.createRequest.Snapshot.Occurrences[0].PlannedEndAt.IsZero() ||
		tasks.createRequest.Snapshot.Occurrences[0].DueAt.IsZero() {
		t.Fatalf("result mutation leaked into snapshot: factory=%#v service=%#v", factory.snapshot.Occurrences, tasks.createRequest.Snapshot.Occurrences)
	}
}

func TestFacadeUsesFreshRuntimeEpochOnEveryRequest(t *testing.T) {
	factory := &factoryFake{snapshot: semanticSnapshotForTest()}
	tasks := &taskServiceFake{}
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{
		runtimeForTest(20, factory, tasks), runtimeForTest(21, factory, tasks),
	}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"command-1", "command-2"}})
	request := TaskLifecycleRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1", Command: taskdomain.TaskCommandPause,
		Expected: taskdomain.LifecycleExpectedRevisions{Task: 4, Schedule: 3},
	}

	if _, err := facade.ExecuteTaskLifecycle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.ExecuteTaskLifecycle(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 2 || !reflect.DeepEqual(tasks.lifecycleEpochs, []int64{20, 21}) {
		t.Fatalf("resolver calls/epochs = %d/%v", resolver.calls, tasks.lifecycleEpochs)
	}
}

func TestFacadeProjectCommandsInjectAuditAndPreserveExpectedRevision(t *testing.T) {
	projects := &projectServiceFake{}
	resolver := &runtimeResolverFake{runtimes: repeatRuntime(runtimeForTestWithProjects(9, projects), 5)}
	clock := fixedClock{now: testFacadeNow()}
	facade := NewFacade(resolver, clock, &idGeneratorFake{ids: []string{"project-new"}}, &commandIDGeneratorFake{ids: []string{"cmd-create", "cmd-update", "cmd-complete", "cmd-archive", "cmd-delete"}})

	if _, err := facade.CreateProject(context.Background(), CreateProjectRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", Name: "New", Kind: taskdomain.ProjectKindLearning,
		Horizon: taskdomain.ProjectHorizonLong, Status: taskdomain.ProjectStatusPlanning,
	}); err != nil {
		t.Fatal(err)
	}
	updated := taskdomain.Project{WorkspaceID: "workspace-1", ID: "project-1", Name: "Updated", Kind: taskdomain.ProjectKindStandard, Horizon: taskdomain.ProjectHorizonShort, Status: taskdomain.ProjectStatusActive}
	if _, err := facade.UpdateProject(context.Background(), UpdateProjectRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", ProjectID: "project-1", ExpectedProjectRevision: 4, Project: updated,
	}); err != nil {
		t.Fatal(err)
	}
	for _, execute := range []func() error{
		func() error {
			_, err := facade.CompleteProject(context.Background(), ExistingProjectRequest{WorkspaceID: "workspace-1", ActorID: "user-1", ProjectID: "project-1", ExpectedProjectRevision: 5})
			return err
		},
		func() error {
			_, err := facade.ArchiveProject(context.Background(), ExistingProjectRequest{WorkspaceID: "workspace-1", ActorID: "user-1", ProjectID: "project-1", ExpectedProjectRevision: 6})
			return err
		},
		func() error {
			_, err := facade.DeleteProject(context.Background(), ExistingProjectRequest{WorkspaceID: "workspace-1", ActorID: "user-1", ProjectID: "project-1", ExpectedProjectRevision: 7})
			return err
		},
	} {
		if err := execute(); err != nil {
			t.Fatal(err)
		}
	}

	if resolver.calls != 5 {
		t.Fatalf("resolver calls = %d, want 5", resolver.calls)
	}
	if projects.createRequest.ExpectedRuntimeEpoch != 9 || projects.createRequest.ExpectedProjectRevision != 0 ||
		projects.createRequest.Project.ID != "project-new" || projects.createRequest.Project.SystemRole != taskdomain.ProjectSystemRoleNone ||
		projects.createRequest.CommandID != "cmd-create" || projects.createRequest.ActorID != "user-1" || !projects.createRequest.At.Equal(clock.now) {
		t.Fatalf("create project request = %#v", projects.createRequest)
	}
	if projects.updateRequest.ExpectedRuntimeEpoch != 9 || projects.updateRequest.ExpectedProjectRevision != 4 || projects.updateRequest.Project != updated || projects.updateRequest.CommandID != "cmd-update" {
		t.Fatalf("update project request = %#v", projects.updateRequest)
	}
	wantCommands := []taskdomain.ProjectCommand{taskdomain.ProjectCommandComplete, taskdomain.ProjectCommandArchive, taskdomain.ProjectCommandDelete}
	wantRevisions := []int64{5, 6, 7}
	for index, request := range projects.existingRequests {
		if request.Command != wantCommands[index] || request.ExpectedProjectRevision != wantRevisions[index] || request.ExpectedRuntimeEpoch != 9 || request.ActorID != "user-1" {
			t.Fatalf("existing request[%d] = %#v", index, request)
		}
	}
}

func TestFacadeTaskLifecycleAndOccurrenceCommandsPreserveClientRevisions(t *testing.T) {
	tasks := &taskServiceFake{}
	occurrences := &occurrenceServiceFake{}
	runtime := runtimeForTest(13, &factoryFake{}, tasks)
	runtime.Occurrences = occurrences
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime, runtime}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"task-command", "occ-command"}})
	expectedLifecycle := taskdomain.LifecycleExpectedRevisions{Task: 8, Schedule: 6, Occurrences: map[string]int64{"occ-1": 3}}
	_, err := facade.ExecuteTaskLifecycle(context.Background(), TaskLifecycleRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1", Command: taskdomain.TaskCommandCancel, Expected: expectedLifecycle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if tasks.lifecycleRequest.ExpectedRuntimeEpoch != 13 || tasks.lifecycleRequest.Expected.Task != 8 || tasks.lifecycleRequest.Expected.Schedule != 6 ||
		!reflect.DeepEqual(tasks.lifecycleRequest.Expected.Occurrences, expectedLifecycle.Occurrences) || tasks.lifecycleRequest.CommandID != "task-command" {
		t.Fatalf("lifecycle request = %#v", tasks.lifecycleRequest)
	}
	expectedOccurrence := taskdomain.OccurrenceCommandExpectedRevisions{Task: 9, Schedule: 7, Occurrence: 4}
	_, err = facade.ExecuteOccurrence(context.Background(), OccurrenceRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1", OccurrenceID: "occ-1",
		Command: taskdomain.OccurrenceCommandBlock, Expected: expectedOccurrence, BlockedReason: "waiting", NextAction: "call owner",
	})
	if err != nil {
		t.Fatal(err)
	}
	if occurrences.request.ExpectedRuntimeEpoch != 13 || occurrences.request.Expected != expectedOccurrence || occurrences.request.CommandID != "occ-command" ||
		occurrences.request.BlockedReason != "waiting" || occurrences.request.NextAction != "call owner" || !occurrences.request.At.Equal(testFacadeNow()) {
		t.Fatalf("occurrence request = %#v", occurrences.request)
	}
}

func TestFacadeStopsAfterResolverFactoryGeneratorOrServiceError(t *testing.T) {
	resolverErr := errors.New("resolver failed")
	factoryErr := errors.New("factory failed")
	serviceErr := errors.New("service failed")
	idErr := errors.New("id failed")
	commandErr := errors.New("command id failed")

	newRequest := func() CreateTaskRequest {
		return CreateTaskRequest{
			WorkspaceID: "workspace-1", ActorID: "user-1",
			Project: taskdomain.ProjectIdentity{WorkspaceID: "workspace-1", ProjectID: "project-1"},
			Title:   "Task", Schedule: taskdomain.ScheduleInput{RecurrenceType: taskdomain.RecurrenceNone, TimingType: taskdomain.TimingUnscheduled, Timezone: "UTC"},
		}
	}
	tests := []struct {
		name       string
		resolver   *runtimeResolverFake
		factory    *factoryFake
		tasks      *taskServiceFake
		ids        *idGeneratorFake
		commands   *commandIDGeneratorFake
		want       error
		wantBuild  int
		wantCreate int
	}{
		{name: "resolver", resolver: &runtimeResolverFake{err: resolverErr}, factory: &factoryFake{}, tasks: &taskServiceFake{}, ids: &idGeneratorFake{}, commands: &commandIDGeneratorFake{}, want: resolverErr},
		{name: "entity id", factory: &factoryFake{}, tasks: &taskServiceFake{}, ids: &idGeneratorFake{err: idErr}, commands: &commandIDGeneratorFake{}, want: idErr},
		{name: "command id", factory: &factoryFake{}, tasks: &taskServiceFake{}, ids: &idGeneratorFake{ids: []string{"task-1"}}, commands: &commandIDGeneratorFake{err: commandErr}, want: commandErr},
		{name: "factory", factory: &factoryFake{err: factoryErr}, tasks: &taskServiceFake{}, ids: &idGeneratorFake{ids: []string{"task-1"}}, commands: &commandIDGeneratorFake{ids: []string{"command-1"}}, want: factoryErr, wantBuild: 1},
		{name: "service", factory: &factoryFake{snapshot: semanticSnapshotForTest()}, tasks: &taskServiceFake{createErr: serviceErr}, ids: &idGeneratorFake{ids: []string{"task-1"}}, commands: &commandIDGeneratorFake{ids: []string{"command-1"}}, want: serviceErr, wantBuild: 1, wantCreate: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.resolver == nil {
				tt.resolver = &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtimeForTest(2, tt.factory, tt.tasks)}}
			}
			facade := NewFacade(tt.resolver, fixedClock{now: testFacadeNow()}, tt.ids, tt.commands)
			result, err := facade.CreateTask(context.Background(), newRequest())
			if !errors.Is(err, tt.want) || !reflect.DeepEqual(result, CreateTaskResult{}) {
				t.Fatalf("result/error = %#v / %v", result, err)
			}
			if tt.factory.calls != tt.wantBuild || tt.tasks.createCalls != tt.wantCreate {
				t.Fatalf("factory/service calls = %d/%d", tt.factory.calls, tt.tasks.createCalls)
			}
		})
	}
}

func TestFacadeRejectsInvalidWorkspaceActorAndEntityIDsBeforeResolve(t *testing.T) {
	resolver := &runtimeResolverFake{}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{})
	tests := []struct {
		name    string
		execute func() error
	}{
		{name: "create task actor", execute: func() error {
			_, err := facade.CreateTask(context.Background(), CreateTaskRequest{WorkspaceID: "workspace-1"})
			return err
		}},
		{name: "create task project id", execute: func() error {
			_, err := facade.CreateTask(context.Background(), CreateTaskRequest{
				WorkspaceID: "workspace-1", ActorID: "user-1",
				Project: taskdomain.ProjectIdentity{WorkspaceID: "workspace-1"},
			})
			return err
		}},
		{name: "create project workspace", execute: func() error {
			_, err := facade.CreateProject(context.Background(), CreateProjectRequest{ActorID: "user-1"})
			return err
		}},
		{name: "update project id", execute: func() error {
			_, err := facade.UpdateProject(context.Background(), UpdateProjectRequest{WorkspaceID: "workspace-1", ActorID: "user-1"})
			return err
		}},
		{name: "lifecycle task id", execute: func() error {
			_, err := facade.ExecuteTaskLifecycle(context.Background(), TaskLifecycleRequest{WorkspaceID: "workspace-1", ActorID: "user-1"})
			return err
		}},
		{name: "occurrence id", execute: func() error {
			_, err := facade.ExecuteOccurrence(context.Background(), OccurrenceRequest{WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1"})
			return err
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.execute(); !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("error = %v, want %v", err, ErrInvalidRequest)
			}
		})
	}
	if resolver.calls != 0 {
		t.Fatalf("invalid inputs resolved runtime %d times", resolver.calls)
	}
}

func TestFacadeScheduleCommandsUseFreshEpochAndApplicationAudit(t *testing.T) {
	now := testFacadeNow()
	schedules := &scheduleServiceFake{
		occurrenceResult: ScheduleCommandOutcome{TaskRevision: 8, ScheduleRevision: 5, OccurrenceRevision: 4, ScheduleVersion: 2},
		followingResult:  ScheduleCommandOutcome{TaskRevision: 9, ScheduleRevision: 6, ScheduleVersion: 3},
	}
	first := runtimeForTest(30, &factoryFake{}, &taskServiceFake{})
	first.Schedules = schedules
	second := first
	second.Epoch = 31
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{first, second}}
	facade := NewFacade(resolver, fixedClock{now: now}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"schedule-1", "schedule-2"}})
	offset := 3600

	occurrenceResult, err := facade.RescheduleOccurrence(context.Background(), RescheduleOccurrenceRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1", OccurrenceID: "occ-1",
		ExpectedTaskRevision: 7, ExpectedScheduleRevision: 5, ExpectedOccurrenceRevision: 3,
		Timing: taskdomain.OccurrenceTimingInput{
			TimingType: taskdomain.TimingTimeBlock, Timezone: "Asia/Shanghai", PlannedDate: "2026-07-23",
			LocalStartTime: "09:00", DurationMinutes: 45, SelectedOffsetSeconds: &offset,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	selected := map[string]int{"2026-11-01": -14400}
	followingResult, err := facade.RescheduleThisAndFollowing(context.Background(), RescheduleThisAndFollowingRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1",
		ExpectedTaskRevision: 8, ExpectedScheduleRevision: 5, EffectiveFrom: "2026-07-24", GenerateThroughExclusive: "2026-08-24",
		Schedule:        taskdomain.ScheduleInput{RecurrenceType: taskdomain.RecurrenceDaily, TimingType: taskdomain.TimingDate, Timezone: "UTC", StartsOn: "2026-07-24"},
		SelectedOffsets: selected,
	})
	if err != nil {
		t.Fatal(err)
	}

	if resolver.calls != 2 || schedules.occurrenceCalls != 1 || schedules.followingCalls != 1 {
		t.Fatalf("resolve/schedule calls = %d/%d/%d", resolver.calls, schedules.occurrenceCalls, schedules.followingCalls)
	}
	if schedules.occurrenceRequest.ExpectedRuntimeEpoch != 30 || schedules.occurrenceRequest.TaskID != "task-1" ||
		schedules.occurrenceRequest.ExpectedTaskRevision != 7 || schedules.occurrenceRequest.ExpectedScheduleRevision != 5 ||
		schedules.occurrenceRequest.ExpectedOccurrenceRevision != 3 {
		t.Fatalf("occurrence schedule request = %#v", schedules.occurrenceRequest)
	}
	if schedules.followingRequest.ExpectedRuntimeEpoch != 31 || schedules.followingRequest.ExpectedTaskRevision != 8 ||
		schedules.followingRequest.ExpectedScheduleRevision != 5 || !reflect.DeepEqual(schedules.followingRequest.SelectedOffsets, selected) {
		t.Fatalf("following schedule request = %#v", schedules.followingRequest)
	}
	if schedules.occurrenceMetadata != (CommandMetadata{ActorID: "user-1", CommandID: "schedule-1", At: now}) ||
		schedules.followingMetadata != (CommandMetadata{ActorID: "user-1", CommandID: "schedule-2", At: now}) {
		t.Fatalf("schedule metadata = %#v / %#v", schedules.occurrenceMetadata, schedules.followingMetadata)
	}
	selected["2026-11-01"] = 123
	if schedules.followingRequest.SelectedOffsets["2026-11-01"] != -14400 {
		t.Fatal("selected offsets were not defensively copied")
	}
	if occurrenceResult.CommandID != "schedule-1" || followingResult.CommandID != "schedule-2" || occurrenceResult.OccurrenceRevision != 4 {
		t.Fatalf("schedule results = %#v / %#v", occurrenceResult, followingResult)
	}
}

func TestFacadeRescheduleOccurrenceCanResolveTaskFromOccurrenceID(t *testing.T) {
	reader := &readerFake{occurrence: taskdomain.QueryOccurrenceSnapshot{WorkspaceID: "workspace-1", TaskID: "task-1", OccurrenceID: "occ-1"}}
	schedules := &scheduleServiceFake{}
	runtime := runtimeForTest(14, &factoryFake{}, &taskServiceFake{})
	runtime.Reader = reader
	runtime.Schedules = schedules
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"schedule-1"}})

	_, err := facade.RescheduleOccurrence(context.Background(), RescheduleOccurrenceRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", OccurrenceID: "occ-1",
		ExpectedTaskRevision: 3, ExpectedScheduleRevision: 2, ExpectedOccurrenceRevision: 1,
		Timing: taskdomain.OccurrenceTimingInput{TimingType: taskdomain.TimingUnscheduled, Timezone: "UTC"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || reader.occurrenceCalls != 1 || schedules.occurrenceRequest.TaskID != "task-1" {
		t.Fatalf("resolve/read/task = %d/%d/%q", resolver.calls, reader.occurrenceCalls, schedules.occurrenceRequest.TaskID)
	}
}

func TestFacadeExecuteOccurrenceByIDUsesOneRuntimeForReadAndWrite(t *testing.T) {
	reader := &readerFake{occurrence: taskdomain.QueryOccurrenceSnapshot{WorkspaceID: "workspace-1", TaskID: "task-9", OccurrenceID: "occ-9"}}
	occurrences := &occurrenceServiceFake{}
	runtime := runtimeForTest(16, &factoryFake{}, &taskServiceFake{})
	runtime.Reader = reader
	runtime.Occurrences = occurrences
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"occ-command"}})

	_, err := facade.ExecuteOccurrenceByID(context.Background(), OccurrenceByIDRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", OccurrenceID: "occ-9", Command: taskdomain.OccurrenceCommandStart,
		Expected: taskdomain.OccurrenceCommandExpectedRevisions{Task: 4, Schedule: 2, Occurrence: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || reader.occurrenceCalls != 1 || occurrences.calls != 1 || occurrences.request.TaskID != "task-9" || occurrences.request.ExpectedRuntimeEpoch != 16 {
		t.Fatalf("resolve/read/write = %d/%d/%d request=%#v", resolver.calls, reader.occurrenceCalls, occurrences.calls, occurrences.request)
	}
}

func TestFacadeOccurrenceByIDStopsOnReaderError(t *testing.T) {
	readerErr := errors.New("occurrence lookup failed")
	reader := &readerFake{occurrenceErr: readerErr}
	occurrences := &occurrenceServiceFake{}
	runtime := runtimeForTest(16, &factoryFake{}, &taskServiceFake{})
	runtime.Reader = reader
	runtime.Occurrences = occurrences
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime}}
	commands := &commandIDGeneratorFake{ids: []string{"unused"}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, commands)

	_, err := facade.ExecuteOccurrenceByID(context.Background(), OccurrenceByIDRequest{WorkspaceID: "workspace-1", ActorID: "user-1", OccurrenceID: "occ-1"})
	if !errors.Is(err, readerErr) || resolver.calls != 1 || reader.occurrenceCalls != 1 || occurrences.calls != 0 || commands.calls != 0 {
		t.Fatalf("error/calls = %v %d/%d/%d/%d", err, resolver.calls, reader.occurrenceCalls, occurrences.calls, commands.calls)
	}
}

func TestFacadePatchProjectReadsAndAppliesOnlyProvidedFields(t *testing.T) {
	name := "Renamed"
	status := taskdomain.ProjectStatusCompleted
	reader := &readerFake{project: taskdomain.ProjectSnapshot{Project: taskdomain.Project{
		WorkspaceID: "workspace-1", ID: "project-1", Name: "Original", Kind: taskdomain.ProjectKindLearning,
		Horizon: taskdomain.ProjectHorizonLong, Status: taskdomain.ProjectStatusActive,
	}, Revision: 4}}
	projects := &projectServiceFake{}
	runtime := runtimeForTestWithProjects(12, projects)
	runtime.Reader = reader
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"patch-1"}})

	_, err := facade.PatchProject(context.Background(), PatchProjectRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", ProjectID: "project-1", ExpectedProjectRevision: 4,
		Name: &name, Status: &status,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || reader.projectCalls != 1 || projects.updateRequest.ExpectedRuntimeEpoch != 12 ||
		projects.updateRequest.Project.Name != "Renamed" || projects.updateRequest.Project.Status != taskdomain.ProjectStatusCompleted ||
		projects.updateRequest.Project.Kind != taskdomain.ProjectKindLearning || projects.updateRequest.Project.Horizon != taskdomain.ProjectHorizonLong {
		t.Fatalf("patch boundary/request = %d/%d %#v", resolver.calls, reader.projectCalls, projects.updateRequest)
	}
	name = "mutated"
	if projects.updateRequest.Project.Name != "Renamed" {
		t.Fatal("project patch retained caller pointer")
	}
}

func TestFacadeLifecycleOutcomeCopiesOccurrenceRevisions(t *testing.T) {
	tasks := &taskServiceFake{lifecycleResult: TaskCommandOutcome{OccurrenceRevisions: map[string]int64{"occ-1": 5}}}
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtimeForTest(10, &factoryFake{}, tasks)}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"cmd-1"}})
	result, err := facade.ExecuteTaskLifecycle(context.Background(), TaskLifecycleRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1", Command: taskdomain.TaskCommandCancel,
		Expected: taskdomain.LifecycleExpectedRevisions{Task: 3, Schedule: 2, Occurrences: map[string]int64{"occ-1": 4}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result.OccurrenceRevisions["occ-1"] = 999
	if tasks.lifecycleResult.OccurrenceRevisions["occ-1"] != 5 {
		t.Fatal("facade returned service-owned occurrence revision map")
	}
}

func TestFacadeOccurrenceQueriesUseOneRuntimeAndExpectedScope(t *testing.T) {
	recurring := true
	statuses := []taskdomain.ExecutionStatus{taskdomain.ExecutionStatusOpen}
	reader := &readerFake{listResult: []taskdomain.QueryOccurrenceSnapshot{{
		WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-1", OccurrenceID: "occ-1",
		Title: "Task", TimingType: taskdomain.TimingUnscheduled, Timezone: "UTC", Status: taskdomain.ExecutionStatusOpen,
		MarkedForToday: true, Revision: 1,
	}}}
	runtime := runtimeForTest(18, &factoryFake{}, &taskServiceFake{})
	runtime.Reader = reader
	resolver := &runtimeResolverFake{runtimes: repeatRuntime(runtime, 7)}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{})
	request := OccurrenceQueryRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", Scope: taskdomain.OccurrenceListUpcoming,
		From: testFacadeNow(), To: testFacadeNow().Add(24 * time.Hour), Timezone: "UTC", ProjectID: "project-1",
		Statuses: statuses, Recurring: &recurring,
	}

	if _, err := facade.ListOccurrences(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if result, err := facade.Today(context.Background(), request); err != nil || len(result.Default) != 1 {
		t.Fatalf("Today() = %#v, %v", result, err)
	}
	if _, err := facade.Upcoming(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Overdue(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Unscheduled(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Completed(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if _, err := facade.Calendar(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	wantScopes := []taskdomain.OccurrenceListScope{
		taskdomain.OccurrenceListUpcoming, taskdomain.OccurrenceListToday, taskdomain.OccurrenceListUpcoming,
		taskdomain.OccurrenceListOverdue, taskdomain.OccurrenceListUnscheduled, taskdomain.OccurrenceListCompleted, taskdomain.OccurrenceListCalendar,
	}
	if resolver.calls != 7 || reader.listCalls != 7 {
		t.Fatalf("resolve/read calls = %d/%d", resolver.calls, reader.listCalls)
	}
	for index, filter := range reader.filters {
		if filter.Scope != wantScopes[index] || filter.ProjectID != "project-1" || filter.Timezone != "UTC" {
			t.Fatalf("filter[%d] = %#v", index, filter)
		}
	}
	statuses[0] = taskdomain.ExecutionStatusDone
	recurring = false
	if reader.filters[0].Statuses[0] != taskdomain.ExecutionStatusOpen || reader.filters[0].Recurring == nil || !*reader.filters[0].Recurring {
		t.Fatal("query filter retained caller-owned values")
	}
}

func TestFacadeScheduleErrorsPreserveOnlyActionableCandidates(t *testing.T) {
	serviceErr := errors.New("schedule write failed")
	schedules := &scheduleServiceFake{occurrenceErr: serviceErr}
	runtime := runtimeForTest(20, &factoryFake{}, &taskServiceFake{})
	runtime.Schedules = schedules
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime, runtime}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"cmd-1", "cmd-2"}})
	request := RescheduleOccurrenceRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1", OccurrenceID: "occ-1",
		ExpectedTaskRevision: 3, ExpectedScheduleRevision: 2, ExpectedOccurrenceRevision: 1,
		Timing: taskdomain.OccurrenceTimingInput{TimingType: taskdomain.TimingUnscheduled, Timezone: "UTC"},
	}

	result, err := facade.RescheduleOccurrence(context.Background(), request)
	if !errors.Is(err, serviceErr) || !reflect.DeepEqual(result, ScheduleCommandOutcome{}) {
		t.Fatalf("generic schedule result/error = %#v / %v", result, err)
	}
	candidate := taskdomain.OffsetCandidate{OffsetSeconds: -14400, UTC: testFacadeNow()}
	schedules.occurrenceResult = ScheduleCommandOutcome{Candidates: []taskdomain.OffsetCandidate{candidate}}
	schedules.occurrenceErr = taskdomain.ErrAmbiguousLocalTime
	result, err = facade.RescheduleOccurrence(context.Background(), request)
	if !errors.Is(err, taskdomain.ErrAmbiguousLocalTime) || result.CommandID != "cmd-2" || !reflect.DeepEqual(result.Candidates, []taskdomain.OffsetCandidate{candidate}) {
		t.Fatalf("ambiguous schedule result/error = %#v / %v", result, err)
	}
}

func TestFacadePatchAndQueryStopBeforeMutationOnReadError(t *testing.T) {
	readErr := errors.New("read failed")
	reader := &readerFake{projectErr: readErr, listErr: readErr}
	projects := &projectServiceFake{}
	runtime := runtimeForTestWithProjects(8, projects)
	runtime.Reader = reader
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime, runtime}}
	commands := &commandIDGeneratorFake{ids: []string{"unused"}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, commands)
	name := "new"

	projectResult, err := facade.PatchProject(context.Background(), PatchProjectRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", ProjectID: "project-1", ExpectedProjectRevision: 1, Name: &name,
	})
	if !errors.Is(err, readErr) || !reflect.DeepEqual(projectResult, ProjectCommandOutcome{}) || commands.calls != 0 {
		t.Fatalf("patch result/error/commands = %#v / %v / %d", projectResult, err, commands.calls)
	}
	queryResult, err := facade.ListOccurrences(context.Background(), OccurrenceQueryRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", Scope: taskdomain.OccurrenceListUpcoming,
	})
	if !errors.Is(err, readErr) || queryResult != nil || reader.listCalls != 1 {
		t.Fatalf("query result/error/calls = %#v / %v / %d", queryResult, err, reader.listCalls)
	}
}

func TestFacadeEntityQueriesResolveOnceAndReturnDefensiveSnapshots(t *testing.T) {
	actualStart := testFacadeNow()
	plannedStart := actualStart.Add(time.Hour)
	reader := &readerFake{
		project: taskdomain.ProjectSnapshot{Project: taskdomain.Project{WorkspaceID: "workspace-1", ID: "project-1", Name: "Project"}, Revision: 2},
		occurrence: taskdomain.QueryOccurrenceSnapshot{
			WorkspaceID: "workspace-1", TaskID: "task-1", OccurrenceID: "occ-1", TimingType: taskdomain.TimingUnscheduled,
			Timezone: "UTC", ActualStartAt: &actualStart,
		},
		task: taskdomain.TaskAggregateQueryResult{
			Aggregate: taskdomain.TaskAggregate{WorkspaceID: "workspace-1", TaskID: "task-1", Occurrences: []taskdomain.Occurrence{{
				WorkspaceID: "workspace-1", ID: "occ-1", TaskID: "task-1", ActualStartAt: &actualStart,
			}}},
			Task:     taskdomain.TaskRecord{WorkspaceID: "workspace-1", ID: "task-1"},
			Schedule: taskdomain.ScheduleHeader{WorkspaceID: "workspace-1", TaskID: "task-1"},
			Versions: []taskdomain.ScheduleVersion{{WorkspaceID: "workspace-1", TaskID: "task-1", ScheduleRevision: 1}},
			Occurrences: []taskdomain.QueryOccurrenceSnapshot{{
				WorkspaceID: "workspace-1", TaskID: "task-1", OccurrenceID: "occ-1", TimingType: taskdomain.TimingTimeBlock,
				Timezone: "UTC", PlannedStartAt: &plannedStart,
			}},
		},
	}
	runtime := runtimeForTest(6, &factoryFake{}, &taskServiceFake{})
	runtime.Reader = reader
	resolver := &runtimeResolverFake{runtimes: repeatRuntime(runtime, 3)}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{})

	project, err := facade.GetProject(context.Background(), EntityQueryRequest{WorkspaceID: "workspace-1", ActorID: "user-1", EntityID: "project-1"})
	if err != nil {
		t.Fatal(err)
	}
	occurrence, err := facade.GetOccurrence(context.Background(), EntityQueryRequest{WorkspaceID: "workspace-1", ActorID: "user-1", EntityID: "occ-1"})
	if err != nil {
		t.Fatal(err)
	}
	task, err := facade.GetTask(context.Background(), EntityQueryRequest{WorkspaceID: "workspace-1", ActorID: "user-1", EntityID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 3 || reader.projectCalls != 1 || reader.occurrenceCalls != 1 || reader.taskCalls != 1 || project.Revision != 2 {
		t.Fatalf("resolve/read result = %d/%d/%d/%d %#v", resolver.calls, reader.projectCalls, reader.occurrenceCalls, reader.taskCalls, project)
	}
	*occurrence.ActualStartAt = time.Time{}
	*task.Aggregate.Occurrences[0].ActualStartAt = time.Time{}
	*task.Occurrences[0].PlannedStartAt = time.Time{}
	task.Versions[0].ScheduleRevision = 999
	if reader.occurrence.ActualStartAt.IsZero() || reader.task.Aggregate.Occurrences[0].ActualStartAt.IsZero() ||
		reader.task.Occurrences[0].PlannedStartAt.IsZero() || reader.task.Versions[0].ScheduleRevision != 1 {
		t.Fatal("entity query returned reader-owned memory")
	}
}

func TestFacadeOccurrenceQueryDefaultsToAllAndForwardsTaskID(t *testing.T) {
	reader := &readerFake{listResult: []taskdomain.QueryOccurrenceSnapshot{{WorkspaceID: "workspace-1", TaskID: "task-1", OccurrenceID: "occ-1"}}}
	runtime := runtimeForTest(4, &factoryFake{}, &taskServiceFake{})
	runtime.Reader = reader
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{})

	result, err := facade.ListOccurrences(context.Background(), OccurrenceQueryRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || resolver.calls != 1 || reader.listCalls != 1 || reader.filters[0].Scope != taskdomain.OccurrenceListAll || reader.filters[0].TaskID != "task-1" {
		t.Fatalf("default occurrence query = result:%#v resolve:%d filters:%#v", result, resolver.calls, reader.filters)
	}
}

func TestFacadeCatalogAndCalendarQueriesResolveOnceAndReturnDefensiveResults(t *testing.T) {
	plannedStart := testFacadeNow().Add(time.Hour)
	reader := &readerFake{
		projectListResult: []taskdomain.ProjectSnapshot{{Project: taskdomain.Project{WorkspaceID: "workspace-1", ID: "project-1", Name: "Project"}, Revision: 7}},
		taskListResult: []taskdomain.TaskDefinitionSnapshot{{
			Task:             taskdomain.TaskRecord{WorkspaceID: "workspace-1", ID: "task-1", ProjectID: "project-1", Title: "Task", Revision: 5},
			ScheduleRevision: 3, CurrentScheduleRevision: 2,
		}},
		listResult: []taskdomain.QueryOccurrenceSnapshot{{
			WorkspaceID: "workspace-1", ProjectID: "project-1", TaskID: "task-1", OccurrenceID: "occ-1",
			ProjectRevision: 7, TimingType: taskdomain.TimingTimeBlock, Timezone: "UTC", PlannedStartAt: &plannedStart,
		}},
	}
	runtime := runtimeForTest(4, &factoryFake{}, &taskServiceFake{})
	runtime.Reader = reader
	resolver := &runtimeResolverFake{runtimes: repeatRuntime(runtime, 3)}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{})
	kind := taskdomain.ProjectKindStandard
	status := taskdomain.TaskLifecycleActive

	projects, err := facade.ListProjects(context.Background(), ListProjectsRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", Kind: &kind,
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := facade.ListTaskDefinitions(context.Background(), ListTaskDefinitionsRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", ProjectID: "project-1", LifecycleStatus: &status,
	})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := facade.CalendarEntries(context.Background(), CalendarEntriesRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", From: testFacadeNow(), To: testFacadeNow().Add(24 * time.Hour),
		Timezone: "UTC", ProjectID: "project-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 3 || reader.projectListCalls != 1 || reader.taskListCalls != 1 || reader.listCalls != 1 {
		t.Fatalf("resolve/read calls = %d/%d/%d/%d", resolver.calls, reader.projectListCalls, reader.taskListCalls, reader.listCalls)
	}
	if reader.projectListFilters[0].Kind == nil || *reader.projectListFilters[0].Kind != kind ||
		reader.taskListFilters[0].ProjectID != "project-1" || reader.taskListFilters[0].LifecycleStatus == nil || *reader.taskListFilters[0].LifecycleStatus != status {
		t.Fatalf("catalog filters = %#v / %#v", reader.projectListFilters, reader.taskListFilters)
	}
	if reader.filters[0].Scope != taskdomain.OccurrenceListCalendar || reader.filters[0].ProjectID != "project-1" ||
		entries[0].ProjectRevision != 7 || entries[0].Occurrence.ProjectRevision != 7 {
		t.Fatalf("calendar filter/result = %#v / %#v", reader.filters[0], entries)
	}

	projects[0].Project.Name = "mutated"
	tasks[0].Task.Title = "mutated"
	*entries[0].Occurrence.PlannedStartAt = time.Time{}
	if reader.projectListResult[0].Project.Name != "Project" || reader.taskListResult[0].Task.Title != "Task" || reader.listResult[0].PlannedStartAt.IsZero() {
		t.Fatal("query results retained reader-owned memory")
	}
}

func TestFacadePatchTaskBuildsTypedReferencesAndUsesOneRuntime(t *testing.T) {
	reader := &readerFake{task: taskdomain.TaskAggregateQueryResult{
		Aggregate: taskdomain.TaskAggregate{WorkspaceID: "workspace-1", TaskID: "task-1", Revision: 5},
		Task:      taskdomain.TaskRecord{WorkspaceID: "workspace-1", ID: "task-1", ProjectID: "project-old", Title: "Before", Revision: 5},
	}}
	tasks := &taskServiceFake{patchResult: TaskCommandOutcome{TaskRevision: 6, ScheduleRevision: 3, Task: taskdomain.TaskRecord{WorkspaceID: "workspace-1", ID: "task-1", Title: "After", Revision: 6}}}
	runtime := runtimeForTest(22, &factoryFake{}, tasks)
	runtime.Reader = reader
	resolver := &runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime}}
	facade := NewFacade(resolver, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"patch-1"}})
	title, projectID, roadmapID, noteID := "After", "project-new", "roadmap-new", "note-new"

	result, err := facade.PatchTask(context.Background(), PatchTaskRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1",
		ExpectedTaskRevision: 5, ExpectedScheduleRevision: 3,
		Title: &title, ProjectID: &projectID, RoadmapNodeID: &roadmapID, NoteID: &noteID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolver.calls != 1 || reader.taskCalls != 1 || tasks.patchCalls != 1 || tasks.patchRequest.ExpectedRuntimeEpoch != 22 ||
		tasks.patchRequest.ExpectedTaskRevision != 5 || tasks.patchRequest.ExpectedScheduleRevision != 3 ||
		tasks.patchRequest.Patch.Project == nil || tasks.patchRequest.Patch.Project.ProjectID != "project-new" ||
		!tasks.patchRequest.Patch.RoadmapSet || tasks.patchRequest.Patch.Roadmap == nil || tasks.patchRequest.Patch.Roadmap.ProjectID != "project-new" ||
		!tasks.patchRequest.Patch.TaskNoteSet || tasks.patchRequest.Patch.TaskNote == nil || tasks.patchRequest.Patch.TaskNote.WorkspaceID != "workspace-1" ||
		tasks.patchRequest.CommandID != "patch-1" || tasks.patchRequest.ActorID != "user-1" || !tasks.patchRequest.At.Equal(testFacadeNow()) {
		t.Fatalf("patch task boundary/request = resolve:%d read:%d calls:%d request:%#v", resolver.calls, reader.taskCalls, tasks.patchCalls, tasks.patchRequest)
	}
	title = "mutated"
	projectID = "mutated"
	if *tasks.patchRequest.Patch.Title != "After" || tasks.patchRequest.Patch.Project.ProjectID != "project-new" || result.Task.Title != "After" || result.CommandID != "patch-1" {
		t.Fatalf("patch request/result retained caller values = %#v / %#v", tasks.patchRequest, result)
	}
}

func TestFacadePatchTaskClearsOptionalLinksAndPreservesOmittedFields(t *testing.T) {
	reader := &readerFake{task: taskdomain.TaskAggregateQueryResult{
		Aggregate: taskdomain.TaskAggregate{WorkspaceID: "workspace-1", TaskID: "task-1", Revision: 2},
		Task:      taskdomain.TaskRecord{WorkspaceID: "workspace-1", ID: "task-1", ProjectID: "project-1", Revision: 2},
	}}
	tasks := &taskServiceFake{}
	runtime := runtimeForTest(7, &factoryFake{}, tasks)
	runtime.Reader = reader
	facade := NewFacade(&runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime}}, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, &commandIDGeneratorFake{ids: []string{"patch-1"}})
	clear := ""

	if _, err := facade.PatchTask(context.Background(), PatchTaskRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1", ExpectedTaskRevision: 2, ExpectedScheduleRevision: 1,
		RoadmapNodeID: &clear, NoteID: &clear,
	}); err != nil {
		t.Fatal(err)
	}
	if !tasks.patchRequest.Patch.RoadmapSet || tasks.patchRequest.Patch.Roadmap != nil ||
		!tasks.patchRequest.Patch.TaskNoteSet || tasks.patchRequest.Patch.TaskNote != nil || tasks.patchRequest.Patch.Project != nil || tasks.patchRequest.Patch.Title != nil {
		t.Fatalf("clear/omitted patch = %#v", tasks.patchRequest.Patch)
	}
}

func TestFacadePatchTaskStopsOnReaderError(t *testing.T) {
	readErr := errors.New("task read failed")
	reader := &readerFake{taskErr: readErr}
	tasks := &taskServiceFake{}
	runtime := runtimeForTest(7, &factoryFake{}, tasks)
	runtime.Reader = reader
	commands := &commandIDGeneratorFake{ids: []string{"unused"}}
	facade := NewFacade(&runtimeResolverFake{runtimes: []RuntimeSnapshot{runtime}}, fixedClock{now: testFacadeNow()}, &idGeneratorFake{}, commands)
	title := "After"

	result, err := facade.PatchTask(context.Background(), PatchTaskRequest{
		WorkspaceID: "workspace-1", ActorID: "user-1", TaskID: "task-1", ExpectedTaskRevision: 2, ExpectedScheduleRevision: 1, Title: &title,
	})
	if !errors.Is(err, readErr) || !reflect.DeepEqual(result, TaskCommandOutcome{}) || tasks.patchCalls != 0 || commands.calls != 0 {
		t.Fatalf("patch read failure = %#v / %v / service:%d commands:%d", result, err, tasks.patchCalls, commands.calls)
	}
}

func runtimeForTest(epoch int64, factory TaskFactory, tasks TaskService) RuntimeSnapshot {
	return RuntimeSnapshot{
		WorkspaceID: "workspace-1", Epoch: epoch, Factory: factory, Tasks: tasks,
		Occurrences: &occurrenceServiceFake{}, Projects: &projectServiceFake{}, Schedules: &scheduleServiceFake{}, Reader: &readerFake{},
	}
}

func runtimeForTestWithProjects(epoch int64, projects ProjectService) RuntimeSnapshot {
	return RuntimeSnapshot{
		WorkspaceID: "workspace-1", Epoch: epoch, Factory: &factoryFake{}, Tasks: &taskServiceFake{},
		Occurrences: &occurrenceServiceFake{}, Projects: projects, Schedules: &scheduleServiceFake{}, Reader: &readerFake{},
	}
}

func repeatRuntime(runtime RuntimeSnapshot, count int) []RuntimeSnapshot {
	result := make([]RuntimeSnapshot, count)
	for index := range result {
		result[index] = runtime
	}
	return result
}

func semanticSnapshotForTest() taskdomain.TaskAggregateSnapshot {
	return taskdomain.TaskAggregateSnapshot{
		Task:     taskdomain.TaskRecord{WorkspaceID: "workspace-1", ID: "task-1", ProjectID: "project-1", Title: "Task", LifecycleStatus: taskdomain.TaskLifecycleDraft, Revision: 1},
		Schedule: taskdomain.ScheduleHeader{WorkspaceID: "workspace-1", TaskID: "task-1", Revision: 1, CurrentScheduleRevision: 1},
	}
}

func testFacadeNow() time.Time { return time.Date(2026, 7, 22, 14, 0, 0, 0, time.UTC) }

type runtimeResolverFake struct {
	runtimes     []RuntimeSnapshot
	err          error
	calls        int
	workspaceIDs []string
}

func (resolver *runtimeResolverFake) Resolve(_ context.Context, workspaceID string) (RuntimeSnapshot, error) {
	resolver.calls++
	resolver.workspaceIDs = append(resolver.workspaceIDs, workspaceID)
	if resolver.err != nil {
		return RuntimeSnapshot{}, resolver.err
	}
	if len(resolver.runtimes) == 0 {
		return RuntimeSnapshot{}, errors.New("no runtime")
	}
	runtime := resolver.runtimes[0]
	resolver.runtimes = resolver.runtimes[1:]
	return runtime, nil
}

type fixedClock struct{ now time.Time }

func (clock fixedClock) Now() time.Time { return clock.now }

type idGeneratorFake struct {
	ids   []string
	err   error
	calls int
}

func (generator *idGeneratorFake) NewID(context.Context) (string, error) {
	generator.calls++
	if generator.err != nil {
		return "", generator.err
	}
	if len(generator.ids) == 0 {
		return "", errors.New("no entity id")
	}
	id := generator.ids[0]
	generator.ids = generator.ids[1:]
	return id, nil
}

type commandIDGeneratorFake struct {
	ids   []string
	err   error
	calls int
}

func (generator *commandIDGeneratorFake) NewCommandID(context.Context) (string, error) {
	generator.calls++
	if generator.err != nil {
		return "", generator.err
	}
	if len(generator.ids) == 0 {
		return "", errors.New("no command id")
	}
	id := generator.ids[0]
	generator.ids = generator.ids[1:]
	return id, nil
}

type factoryFake struct {
	input    taskdomain.TaskCreationInput
	snapshot taskdomain.TaskAggregateSnapshot
	details  taskdomain.TaskCreationDetails
	err      error
	calls    int
}

func (factory *factoryFake) Build(input taskdomain.TaskCreationInput) (taskdomain.TaskAggregateSnapshot, taskdomain.TaskCreationDetails, error) {
	factory.calls++
	factory.input = input
	return factory.snapshot, factory.details, factory.err
}

type taskServiceFake struct {
	createRequest    taskdomain.CreateTaskRequest
	createResult     TaskCommandOutcome
	createErr        error
	createCalls      int
	patchRequest     taskdomain.PatchTaskRequest
	patchResult      TaskCommandOutcome
	patchErr         error
	patchCalls       int
	lifecycleRequest taskdomain.LifecycleCommandRequest
	lifecycleEpochs  []int64
	lifecycleResult  TaskCommandOutcome
	lifecycleErr     error
}

func (service *taskServiceFake) CreateTask(_ context.Context, request taskdomain.CreateTaskRequest) (TaskCommandOutcome, error) {
	service.createCalls++
	service.createRequest = request
	return service.createResult, service.createErr
}

func (service *taskServiceFake) PatchTask(_ context.Context, request taskdomain.PatchTaskRequest) (TaskCommandOutcome, error) {
	service.patchCalls++
	service.patchRequest = request
	return service.patchResult, service.patchErr
}

func (service *taskServiceFake) ExecuteLifecycleCommand(_ context.Context, request taskdomain.LifecycleCommandRequest) (TaskCommandOutcome, error) {
	service.lifecycleRequest = request
	service.lifecycleEpochs = append(service.lifecycleEpochs, request.ExpectedRuntimeEpoch)
	return service.lifecycleResult, service.lifecycleErr
}

type occurrenceServiceFake struct {
	request taskdomain.OccurrenceCommandRequest
	result  OccurrenceCommandOutcome
	err     error
	calls   int
}

func (service *occurrenceServiceFake) Execute(_ context.Context, request taskdomain.OccurrenceCommandRequest) (OccurrenceCommandOutcome, error) {
	service.calls++
	service.request = request
	return service.result, service.err
}

type projectServiceFake struct {
	createRequest    taskdomain.CreateProjectRequest
	updateRequest    taskdomain.UpdateProjectRequest
	existingRequests []taskdomain.ExistingProjectRequest
}

func (service *projectServiceFake) CreateProject(_ context.Context, request taskdomain.CreateProjectRequest) (ProjectCommandOutcome, error) {
	service.createRequest = request
	return ProjectCommandOutcome{}, nil
}
func (service *projectServiceFake) UpdateProject(_ context.Context, request taskdomain.UpdateProjectRequest) (ProjectCommandOutcome, error) {
	service.updateRequest = request
	return ProjectCommandOutcome{}, nil
}
func (service *projectServiceFake) CompleteProject(_ context.Context, request taskdomain.ExistingProjectRequest) (ProjectCommandOutcome, error) {
	service.existingRequests = append(service.existingRequests, request)
	return ProjectCommandOutcome{}, nil
}
func (service *projectServiceFake) ArchiveProject(_ context.Context, request taskdomain.ExistingProjectRequest) (ProjectCommandOutcome, error) {
	service.existingRequests = append(service.existingRequests, request)
	return ProjectCommandOutcome{}, nil
}
func (service *projectServiceFake) DeleteProject(_ context.Context, request taskdomain.ExistingProjectRequest) (ProjectCommandOutcome, error) {
	service.existingRequests = append(service.existingRequests, request)
	return ProjectCommandOutcome{}, nil
}

type scheduleServiceFake struct {
	occurrenceRequest  taskdomain.RescheduleOccurrenceRequest
	occurrenceMetadata CommandMetadata
	occurrenceResult   ScheduleCommandOutcome
	occurrenceErr      error
	occurrenceCalls    int
	followingRequest   taskdomain.RescheduleThisAndFutureRequest
	followingMetadata  CommandMetadata
	followingResult    ScheduleCommandOutcome
	followingErr       error
	followingCalls     int
}

func (service *scheduleServiceFake) RescheduleOccurrence(_ context.Context, request taskdomain.RescheduleOccurrenceRequest, metadata CommandMetadata) (ScheduleCommandOutcome, error) {
	service.occurrenceCalls++
	service.occurrenceRequest = request
	service.occurrenceMetadata = metadata
	return service.occurrenceResult, service.occurrenceErr
}

func (service *scheduleServiceFake) RescheduleThisAndFollowing(_ context.Context, request taskdomain.RescheduleThisAndFutureRequest, metadata CommandMetadata) (ScheduleCommandOutcome, error) {
	service.followingCalls++
	service.followingRequest = request
	service.followingMetadata = metadata
	return service.followingResult, service.followingErr
}

type readerFake struct {
	project            taskdomain.ProjectSnapshot
	projectErr         error
	projectCalls       int
	projectID          string
	task               taskdomain.TaskAggregateQueryResult
	taskErr            error
	taskCalls          int
	taskID             string
	occurrence         taskdomain.QueryOccurrenceSnapshot
	occurrenceErr      error
	occurrenceCalls    int
	occurrenceID       string
	listResult         []taskdomain.QueryOccurrenceSnapshot
	listErr            error
	listCalls          int
	filters            []taskdomain.OccurrenceListFilter
	projectListResult  []taskdomain.ProjectSnapshot
	projectListErr     error
	projectListCalls   int
	projectListFilters []taskdomain.ProjectListFilter
	taskListResult     []taskdomain.TaskDefinitionSnapshot
	taskListErr        error
	taskListCalls      int
	taskListFilters    []taskdomain.TaskDefinitionListFilter
}

func (reader *readerFake) GetProject(_ context.Context, projectID string) (taskdomain.ProjectSnapshot, error) {
	reader.projectCalls++
	reader.projectID = projectID
	return reader.project, reader.projectErr
}

func (reader *readerFake) GetOccurrence(_ context.Context, occurrenceID string) (taskdomain.QueryOccurrenceSnapshot, error) {
	reader.occurrenceCalls++
	reader.occurrenceID = occurrenceID
	return reader.occurrence, reader.occurrenceErr
}

func (reader *readerFake) GetTaskAggregate(_ context.Context, taskID string) (taskdomain.TaskAggregateQueryResult, error) {
	reader.taskCalls++
	reader.taskID = taskID
	return reader.task, reader.taskErr
}

func (reader *readerFake) ListOccurrences(_ context.Context, filter taskdomain.OccurrenceListFilter) ([]taskdomain.QueryOccurrenceSnapshot, error) {
	reader.listCalls++
	reader.filters = append(reader.filters, filter)
	return reader.listResult, reader.listErr
}

func (reader *readerFake) ListProjects(_ context.Context, filter taskdomain.ProjectListFilter) ([]taskdomain.ProjectSnapshot, error) {
	reader.projectListCalls++
	reader.projectListFilters = append(reader.projectListFilters, filter)
	return reader.projectListResult, reader.projectListErr
}

func (reader *readerFake) ListTaskDefinitions(_ context.Context, filter taskdomain.TaskDefinitionListFilter) ([]taskdomain.TaskDefinitionSnapshot, error) {
	reader.taskListCalls++
	reader.taskListFilters = append(reader.taskListFilters, filter)
	return reader.taskListResult, reader.taskListErr
}
