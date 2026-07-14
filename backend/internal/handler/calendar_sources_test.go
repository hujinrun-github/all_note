package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/storage"
)

func TestCalendarProjectSourcesGetReturnsSourcesResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	want := handlerCalendarProjectSourcesFixture()
	store := newHandlerCalendarProjectSourcesStore(want)
	router := calendarProjectSourcesHandlerRouter(store)
	req := httptest.NewRequest(http.MethodGet, "/api/calendar/project-sources", nil)
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	got := decodeCalendarProjectSourcesResponse(t, w.Body.String())
	if !reflect.DeepEqual(got.Data, *want) {
		t.Fatalf("data = %#v, want %#v", got.Data, *want)
	}
	if store.repo.listCalls != 1 {
		t.Fatalf("ListProjectSources calls = %d, want 1", store.repo.listCalls)
	}
}

func TestCalendarProjectSourcesPutSavesSourcesAndReturnsSourcesResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)
	want := handlerCalendarProjectSourcesFixture()
	store := newHandlerCalendarProjectSourcesStore(want)
	router := calendarProjectSourcesHandlerRouter(store)
	body := `{"sources":[{"project_id":"learning-n2","enabled":true,"color":"#3b82f6","order_index":2}]}`
	req := httptest.NewRequest(http.MethodPut, "/api/calendar/project-sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusOK, w.Body.String())
	}
	got := decodeCalendarProjectSourcesResponse(t, w.Body.String())
	if !reflect.DeepEqual(got.Data, *want) {
		t.Fatalf("data = %#v, want %#v", got.Data, *want)
	}
	wantSaved := []model.CalendarProjectSourceInput{
		{ProjectID: "learning-n2", Enabled: true, Color: "#3b82f6", OrderIndex: 2},
	}
	if !reflect.DeepEqual(store.repo.saved, wantSaved) {
		t.Fatalf("saved sources = %#v, want %#v", store.repo.saved, wantSaved)
	}
}

func TestCalendarProjectSourcesPutRejectsInvalidJSON(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := newHandlerCalendarProjectSourcesStore(handlerCalendarProjectSourcesFixture())
	router := calendarProjectSourcesHandlerRouter(store)
	req := httptest.NewRequest(http.MethodPut, "/api/calendar/project-sources", strings.NewReader(`{"sources":`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if store.repo.saveCalls != 0 {
		t.Fatalf("SaveProjectSources calls = %d, want 0", store.repo.saveCalls)
	}
}

func TestCalendarProjectSourcesHandlersReturnInternalErrorOnServiceFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		method string
		body   string
		setup  func(*handlerCalendarProjectSourcesRepository)
	}{
		{
			name:   "get",
			method: http.MethodGet,
			setup: func(repo *handlerCalendarProjectSourcesRepository) {
				repo.listErr = errors.New("list failed")
			},
		},
		{
			name:   "put",
			method: http.MethodPut,
			body:   `{"sources":[]}`,
			setup: func(repo *handlerCalendarProjectSourcesRepository) {
				repo.saveErr = errors.New("save failed")
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			store := newHandlerCalendarProjectSourcesStore(handlerCalendarProjectSourcesFixture())
			tc.setup(store.repo)
			router := calendarProjectSourcesHandlerRouter(store)
			req := httptest.NewRequest(tc.method, "/api/calendar/project-sources", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			if w.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want %d; body = %s", w.Code, http.StatusInternalServerError, w.Body.String())
			}
		})
	}
}

func calendarProjectSourcesHandlerRouter(store storage.Store) *gin.Engine {
	router := gin.New()
	router.GET("/api/calendar/project-sources", GetCalendarProjectSources(store))
	router.PUT("/api/calendar/project-sources", SaveCalendarProjectSources(store))
	return router
}

type calendarProjectSourcesAPIResponse struct {
	Data  model.CalendarProjectSourcesResponse `json:"data"`
	Error *model.APIError                      `json:"error,omitempty"`
}

func decodeCalendarProjectSourcesResponse(t *testing.T, body string) calendarProjectSourcesAPIResponse {
	t.Helper()
	var response calendarProjectSourcesAPIResponse
	if err := json.Unmarshal([]byte(body), &response); err != nil {
		t.Fatalf("decode response: %v; body = %s", err, body)
	}
	if response.Error != nil {
		t.Fatalf("unexpected error response: %#v", response.Error)
	}
	return response
}

type handlerCalendarProjectSourcesStore struct {
	storage.Store
	repo *handlerCalendarProjectSourcesRepository
}

func newHandlerCalendarProjectSourcesStore(response *model.CalendarProjectSourcesResponse) *handlerCalendarProjectSourcesStore {
	return &handlerCalendarProjectSourcesStore{
		repo: &handlerCalendarProjectSourcesRepository{response: response},
	}
}

func (s *handlerCalendarProjectSourcesStore) Calendar() storage.CalendarRepository {
	return s.repo
}

type handlerCalendarProjectSourcesRepository struct {
	storage.CalendarRepository
	response  *model.CalendarProjectSourcesResponse
	listErr   error
	saveErr   error
	listCalls int
	saveCalls int
	saved     []model.CalendarProjectSourceInput
}

func (r *handlerCalendarProjectSourcesRepository) ListProjectSources(context.Context) (*model.CalendarProjectSourcesResponse, error) {
	r.listCalls++
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.response, nil
}

func (r *handlerCalendarProjectSourcesRepository) SaveProjectSources(_ context.Context, sources []model.CalendarProjectSourceInput) (*model.CalendarProjectSourcesResponse, error) {
	r.saveCalls++
	r.saved = append([]model.CalendarProjectSourceInput(nil), sources...)
	if r.saveErr != nil {
		return nil, r.saveErr
	}
	return r.response, nil
}

func handlerCalendarProjectSourcesFixture() *model.CalendarProjectSourcesResponse {
	return &model.CalendarProjectSourcesResponse{
		Sources: []model.CalendarProjectSource{
			{ProjectID: "personal", Name: "Personal", Type: "personal", Enabled: true, Default: true, Color: "#f97316", OrderIndex: 0},
		},
		AvailableProjects: []model.CalendarProjectSource{
			{ProjectID: "learning-n2", Name: "N2", Type: "learning", Enabled: false, Default: false, Color: "", OrderIndex: 2},
		},
	}
}
