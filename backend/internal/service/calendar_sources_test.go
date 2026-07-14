package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestCalendarProjectSourcesListReturnsStorageResponse(t *testing.T) {
	want := calendarProjectSourcesFixture()
	store := newCalendarProjectSourcesStore(want)

	got, err := ListCalendarProjectSources(context.Background(), store)
	if err != nil {
		t.Fatalf("list calendar project sources: %v", err)
	}

	if store.repo.listCalls != 1 {
		t.Fatalf("ListProjectSources calls = %d, want 1", store.repo.listCalls)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func TestCalendarProjectSourcesListPropagatesStorageError(t *testing.T) {
	wantErr := errors.New("list failed")
	store := newCalendarProjectSourcesStore(nil)
	store.repo.listErr = wantErr

	_, err := ListCalendarProjectSources(context.Background(), store)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

func TestCalendarProjectSourcesSavePassesInputsAndReturnsStorageResponse(t *testing.T) {
	want := calendarProjectSourcesFixture()
	store := newCalendarProjectSourcesStore(want)
	inputs := []model.CalendarProjectSourceInput{
		{ProjectID: "learning-n2", Enabled: true, Color: "#3b82f6", OrderIndex: 2},
		{ProjectID: "personal", Enabled: false, Color: "#f97316", OrderIndex: 0},
	}

	got, err := SaveCalendarProjectSources(context.Background(), store, inputs)
	if err != nil {
		t.Fatalf("save calendar project sources: %v", err)
	}

	if store.repo.saveCalls != 1 {
		t.Fatalf("SaveProjectSources calls = %d, want 1", store.repo.saveCalls)
	}
	if !reflect.DeepEqual(store.repo.saved, inputs) {
		t.Fatalf("saved inputs = %#v, want %#v", store.repo.saved, inputs)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response = %#v, want %#v", got, want)
	}
}

func TestCalendarProjectSourcesSavePropagatesStorageError(t *testing.T) {
	wantErr := errors.New("save failed")
	store := newCalendarProjectSourcesStore(nil)
	store.repo.saveErr = wantErr

	_, err := SaveCalendarProjectSources(context.Background(), store, []model.CalendarProjectSourceInput{
		{ProjectID: "learning-n2", Enabled: true, Color: "#3b82f6", OrderIndex: 2},
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}

type calendarProjectSourcesStore struct {
	storage.Store
	repo *calendarProjectSourcesRepository
}

func newCalendarProjectSourcesStore(response *model.CalendarProjectSourcesResponse) *calendarProjectSourcesStore {
	return &calendarProjectSourcesStore{
		repo: &calendarProjectSourcesRepository{response: response},
	}
}

func (s *calendarProjectSourcesStore) Calendar() storage.CalendarRepository {
	return s.repo
}

type calendarProjectSourcesRepository struct {
	storage.CalendarRepository
	response  *model.CalendarProjectSourcesResponse
	listErr   error
	saveErr   error
	listCalls int
	saveCalls int
	saved     []model.CalendarProjectSourceInput
}

func (r *calendarProjectSourcesRepository) ListProjectSources(context.Context) (*model.CalendarProjectSourcesResponse, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.response, nil
}

func (r *calendarProjectSourcesRepository) SaveProjectSources(_ context.Context, sources []model.CalendarProjectSourceInput) (*model.CalendarProjectSourcesResponse, error) {
	r.saveCalls++
	r.saved = append([]model.CalendarProjectSourceInput(nil), sources...)
	if r.saveErr != nil {
		return nil, r.saveErr
	}
	return r.response, nil
}

func calendarProjectSourcesFixture() *model.CalendarProjectSourcesResponse {
	return &model.CalendarProjectSourcesResponse{
		Sources: []model.CalendarProjectSource{
			{ProjectID: "personal", Name: "Personal", Type: "personal", Enabled: true, Default: true, Color: "#f97316", OrderIndex: 0},
		},
		AvailableProjects: []model.CalendarProjectSource{
			{ProjectID: "learning-n2", Name: "N2", Type: "learning", Enabled: false, Default: false, Color: "", OrderIndex: 2},
		},
	}
}
