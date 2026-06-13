package handler

import (
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestGetPaginationClampsLargePageSizeToMax(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/api/tasks?page=2&page_size=300", nil)

	page, pageSize := getPagination(c)

	if page != 2 {
		t.Fatalf("page = %d, want 2", page)
	}
	if pageSize != 100 {
		t.Fatalf("pageSize = %d, want 100", pageSize)
	}
}
