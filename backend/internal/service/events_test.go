package service

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestEventProjectIDCreateWithValidProjectValidatesAndStoresProjectID(t *testing.T) {
	projectID := " project-calendar-learning "
	trimmedProjectID := "project-calendar-learning"
	store := newEventProjectIDStore(trimmedProjectID)

	_, err := CreateEvent(context.Background(), store, &model.CreateEventRequest{
		Title:     "project event",
		StartTime: 1783568400,
		EndTime:   1783572000,
		Kind:      "work",
		ProjectID: &projectID,
	})
	if err != nil {
		t.Fatalf("create event: %v", err)
	}

	if store.tasks.getProjectCalls != 1 || store.tasks.lastProjectID != trimmedProjectID {
		t.Fatalf("GetProjectByID calls = %d with %q, want 1 with %q", store.tasks.getProjectCalls, store.tasks.lastProjectID, trimmedProjectID)
	}
	if store.events.createCalls != 1 {
		t.Fatalf("Create calls = %d, want 1", store.events.createCalls)
	}
	if store.events.created == nil || store.events.created.ProjectID == nil || *store.events.created.ProjectID != trimmedProjectID {
		t.Fatalf("created ProjectID = %v, want %q", projectIDValue(store.events.created), trimmedProjectID)
	}
}

func TestEventProjectIDCreateWithInvalidProjectReturnsErrorBeforeStorageCreate(t *testing.T) {
	projectID := "missing-project"
	store := newEventProjectIDStore("known-project")

	_, err := CreateEvent(context.Background(), store, &model.CreateEventRequest{
		Title:     "project event",
		StartTime: 1783568400,
		EndTime:   1783572000,
		Kind:      "work",
		ProjectID: &projectID,
	})
	if err == nil {
		t.Fatal("expected error for invalid project")
	}
	if !errors.Is(err, ErrInvalidEventProject) {
		t.Fatalf("error = %v, want ErrInvalidEventProject", err)
	}

	if store.tasks.getProjectCalls != 1 {
		t.Fatalf("GetProjectByID calls = %d, want 1", store.tasks.getProjectCalls)
	}
	if store.events.createCalls != 0 {
		t.Fatalf("Create calls = %d, want 0", store.events.createCalls)
	}
}

func TestEventProjectIDCreateWithProjectLookupErrorReturnsStorageErrorBeforeCreate(t *testing.T) {
	projectID := "project-calendar-learning"
	lookupErr := errors.New("project lookup failed")
	store := newEventProjectIDStore(projectID)
	store.tasks.lookupErr = lookupErr

	_, err := CreateEvent(context.Background(), store, &model.CreateEventRequest{
		Title:     "project event",
		StartTime: 1783568400,
		EndTime:   1783572000,
		Kind:      "work",
		ProjectID: &projectID,
	})
	if err == nil {
		t.Fatal("expected project lookup error")
	}
	if errors.Is(err, ErrInvalidEventProject) {
		t.Fatalf("error = %v, should not satisfy ErrInvalidEventProject", err)
	}
	if !errors.Is(err, lookupErr) {
		t.Fatalf("error = %v, want lookup error", err)
	}

	if store.tasks.getProjectCalls != 1 {
		t.Fatalf("GetProjectByID calls = %d, want 1", store.tasks.getProjectCalls)
	}
	if store.events.createCalls != 0 {
		t.Fatalf("Create calls = %d, want 0", store.events.createCalls)
	}
}

func TestEventProjectIDUpdateOmittedProjectKeepsExistingProjectWithoutValidation(t *testing.T) {
	store := newEventProjectIDStore("known-project")

	_, err := UpdateEvent(context.Background(), store, "event-1", &model.UpdateEventRequest{
		Title: stringPtr("renamed event"),
	})
	if err != nil {
		t.Fatalf("update event: %v", err)
	}

	if store.tasks.getProjectCalls != 0 {
		t.Fatalf("GetProjectByID calls = %d, want 0", store.tasks.getProjectCalls)
	}
	if store.events.updateCalls != 1 {
		t.Fatalf("Update calls = %d, want 1", store.events.updateCalls)
	}
	if store.events.updatedReq == nil || store.events.updatedReq.ProjectID != nil {
		t.Fatalf("updated ProjectID = %v, want nil", projectIDRequestValue(store.events.updatedReq))
	}
}

func TestEventProjectIDUpdateEmptyProjectClearsWithoutValidation(t *testing.T) {
	projectID := ""
	store := newEventProjectIDStore("known-project")

	_, err := UpdateEvent(context.Background(), store, "event-1", &model.UpdateEventRequest{
		ProjectID: &projectID,
	})
	if err != nil {
		t.Fatalf("update event: %v", err)
	}

	if store.tasks.getProjectCalls != 0 {
		t.Fatalf("GetProjectByID calls = %d, want 0", store.tasks.getProjectCalls)
	}
	if store.events.updateCalls != 1 {
		t.Fatalf("Update calls = %d, want 1", store.events.updateCalls)
	}
	if store.events.updatedReq == nil || store.events.updatedReq.ProjectID == nil || *store.events.updatedReq.ProjectID != "" {
		t.Fatalf("updated ProjectID = %v, want empty string", projectIDRequestValue(store.events.updatedReq))
	}
}

func TestEventProjectIDUpdateWithValidProjectValidatesBeforeStorageUpdate(t *testing.T) {
	projectID := " project-calendar-learning "
	trimmedProjectID := "project-calendar-learning"
	store := newEventProjectIDStore(trimmedProjectID)

	_, err := UpdateEvent(context.Background(), store, "event-1", &model.UpdateEventRequest{
		ProjectID: &projectID,
	})
	if err != nil {
		t.Fatalf("update event: %v", err)
	}

	if store.tasks.getProjectCalls != 1 || store.tasks.lastProjectID != trimmedProjectID {
		t.Fatalf("GetProjectByID calls = %d with %q, want 1 with %q", store.tasks.getProjectCalls, store.tasks.lastProjectID, trimmedProjectID)
	}
	if store.events.updateCalls != 1 {
		t.Fatalf("Update calls = %d, want 1", store.events.updateCalls)
	}
	if store.events.updatedReq == nil || store.events.updatedReq.ProjectID == nil || *store.events.updatedReq.ProjectID != trimmedProjectID {
		t.Fatalf("updated ProjectID = %v, want %q", projectIDRequestValue(store.events.updatedReq), trimmedProjectID)
	}
}

func TestEventProjectIDUpdateWithInvalidProjectReturnsErrorBeforeStorageUpdate(t *testing.T) {
	projectID := "missing-project"
	store := newEventProjectIDStore("known-project")

	_, err := UpdateEvent(context.Background(), store, "event-1", &model.UpdateEventRequest{
		ProjectID: &projectID,
	})
	if err == nil {
		t.Fatal("expected error for invalid project")
	}
	if !errors.Is(err, ErrInvalidEventProject) {
		t.Fatalf("error = %v, want ErrInvalidEventProject", err)
	}

	if store.tasks.getProjectCalls != 1 {
		t.Fatalf("GetProjectByID calls = %d, want 1", store.tasks.getProjectCalls)
	}
	if store.events.updateCalls != 0 {
		t.Fatalf("Update calls = %d, want 0", store.events.updateCalls)
	}
}

func TestEventProjectIDUpdateWithProjectLookupErrorReturnsStorageErrorBeforeUpdate(t *testing.T) {
	projectID := "project-calendar-learning"
	lookupErr := errors.New("project lookup failed")
	store := newEventProjectIDStore(projectID)
	store.tasks.lookupErr = lookupErr

	_, err := UpdateEvent(context.Background(), store, "event-1", &model.UpdateEventRequest{
		ProjectID: &projectID,
	})
	if err == nil {
		t.Fatal("expected project lookup error")
	}
	if errors.Is(err, ErrInvalidEventProject) {
		t.Fatalf("error = %v, should not satisfy ErrInvalidEventProject", err)
	}
	if !errors.Is(err, lookupErr) {
		t.Fatalf("error = %v, want lookup error", err)
	}

	if store.tasks.getProjectCalls != 1 {
		t.Fatalf("GetProjectByID calls = %d, want 1", store.tasks.getProjectCalls)
	}
	if store.events.updateCalls != 0 {
		t.Fatalf("Update calls = %d, want 0", store.events.updateCalls)
	}
}

type eventProjectIDStore struct {
	storage.Store
	events *eventProjectIDEventRepository
	tasks  *eventProjectIDTaskRepository
}

func newEventProjectIDStore(validProjectID string) *eventProjectIDStore {
	return &eventProjectIDStore{
		events: &eventProjectIDEventRepository{},
		tasks:  &eventProjectIDTaskRepository{validProjectID: validProjectID},
	}
}

func (s *eventProjectIDStore) Events() storage.EventRepository {
	return s.events
}

func (s *eventProjectIDStore) Tasks() storage.TaskRepository {
	return s.tasks
}

type eventProjectIDEventRepository struct {
	storage.EventRepository
	createCalls int
	updateCalls int
	created     *model.Event
	updatedReq  *model.UpdateEventRequest
}

func (r *eventProjectIDEventRepository) Create(_ context.Context, event *model.Event) error {
	r.createCalls++
	captured := *event
	r.created = &captured
	return nil
}

func (r *eventProjectIDEventRepository) Update(_ context.Context, id string, req *model.UpdateEventRequest) (*model.Event, error) {
	r.updateCalls++
	capturedReq := *req
	r.updatedReq = &capturedReq
	return &model.Event{ID: id, ProjectID: req.ProjectID}, nil
}

type eventProjectIDTaskRepository struct {
	storage.TaskRepository
	validProjectID  string
	lookupErr       error
	getProjectCalls int
	lastProjectID   string
}

func (r *eventProjectIDTaskRepository) GetProjectByID(_ context.Context, id string) (*model.TaskProject, error) {
	r.getProjectCalls++
	r.lastProjectID = id
	if r.lookupErr != nil {
		return nil, r.lookupErr
	}
	if id != r.validProjectID {
		return nil, sql.ErrNoRows
	}
	return &model.TaskProject{ID: id, Name: "Calendar Learning", Type: "learning"}, nil
}

func stringPtr(value string) *string {
	return &value
}

func projectIDValue(event *model.Event) any {
	if event == nil || event.ProjectID == nil {
		return nil
	}
	return *event.ProjectID
}

func projectIDRequestValue(req *model.UpdateEventRequest) any {
	if req == nil || req.ProjectID == nil {
		return nil
	}
	return *req.ProjectID
}
