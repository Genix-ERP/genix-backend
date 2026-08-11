package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/genixerp/genix-backend/internal/domain/entity"
	"github.com/gin-gonic/gin"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// helper to create a gin test context backed by an httptest.ResponseRecorder.
func newTestContext() (*gin.Context, *httptest.ResponseRecorder) {
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	return c, w
}

// parseResponse unmarshals the recorder body into a Response.
func parseResponse(t *testing.T, w *httptest.ResponseRecorder) Response {
	t.Helper()
	var resp Response
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response body: %v", err)
	}
	return resp
}

func TestSuccess(t *testing.T) {
	tests := []struct {
		name string
		data interface{}
	}{
		{"string data", "hello"},
		{"map data", map[string]string{"key": "value"}},
		{"nil data", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			Success(c, tt.data)

			if w.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
			}

			resp := parseResponse(t, w)
			if !resp.Success {
				t.Error("expected success to be true")
			}
			if resp.Error != nil {
				t.Error("expected error to be nil")
			}
			if resp.Meta != nil {
				t.Error("expected meta to be nil")
			}
		})
	}
}

func TestCreated(t *testing.T) {
	tests := []struct {
		name string
		data interface{}
	}{
		{"with struct", map[string]int{"id": 1}},
		{"with string", "created resource"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			Created(c, tt.data)

			if w.Code != http.StatusCreated {
				t.Errorf("expected status %d, got %d", http.StatusCreated, w.Code)
			}

			resp := parseResponse(t, w)
			if !resp.Success {
				t.Error("expected success to be true")
			}
			if resp.Error != nil {
				t.Error("expected error to be nil")
			}
		})
	}
}

func TestError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		code       string
		message    string
	}{
		{"generic error", http.StatusTeapot, "TEAPOT", "I'm a teapot"},
		{"server error", http.StatusInternalServerError, "INTERNAL_ERROR", "something broke"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			Error(c, tt.statusCode, tt.code, tt.message)

			if w.Code != tt.statusCode {
				t.Errorf("expected status %d, got %d", tt.statusCode, w.Code)
			}

			resp := parseResponse(t, w)
			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error == nil {
				t.Fatal("expected error info to be non-nil")
			}
			if resp.Error.Code != tt.code {
				t.Errorf("expected error code %q, got %q", tt.code, resp.Error.Code)
			}
			if resp.Error.Message != tt.message {
				t.Errorf("expected error message %q, got %q", tt.message, resp.Error.Message)
			}
		})
	}
}

func TestBadRequest(t *testing.T) {
	tests := []struct {
		name    string
		message string
	}{
		{"custom message", "invalid input"},
		{"another message", "missing field"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			BadRequest(c, tt.message)

			if w.Code != http.StatusBadRequest {
				t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
			}

			resp := parseResponse(t, w)
			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error == nil {
				t.Fatal("expected error info to be non-nil")
			}
			if resp.Error.Code != "BAD_REQUEST" {
				t.Errorf("expected error code BAD_REQUEST, got %q", resp.Error.Code)
			}
			if resp.Error.Message != tt.message {
				t.Errorf("expected error message %q, got %q", tt.message, resp.Error.Message)
			}
		})
	}
}

func TestUnauthorized(t *testing.T) {
	tests := []struct {
		name            string
		message         string
		expectedMessage string
	}{
		{"custom message", "token expired", "token expired"},
		{"empty message uses default", "", "Avval tizimga kiring"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			Unauthorized(c, tt.message)

			if w.Code != http.StatusUnauthorized {
				t.Errorf("expected status %d, got %d", http.StatusUnauthorized, w.Code)
			}

			resp := parseResponse(t, w)
			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error == nil {
				t.Fatal("expected error info to be non-nil")
			}
			if resp.Error.Code != "UNAUTHORIZED" {
				t.Errorf("expected error code UNAUTHORIZED, got %q", resp.Error.Code)
			}
			if resp.Error.Message != tt.expectedMessage {
				t.Errorf("expected error message %q, got %q", tt.expectedMessage, resp.Error.Message)
			}
		})
	}
}

func TestForbidden(t *testing.T) {
	tests := []struct {
		name            string
		message         string
		expectedMessage string
	}{
		{"custom message", "not allowed", "not allowed"},
		{"empty message uses default", "", "Bu amal uchun sizga ruxsat berilmagan"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			Forbidden(c, tt.message)

			if w.Code != http.StatusForbidden {
				t.Errorf("expected status %d, got %d", http.StatusForbidden, w.Code)
			}

			resp := parseResponse(t, w)
			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error == nil {
				t.Fatal("expected error info to be non-nil")
			}
			if resp.Error.Code != "FORBIDDEN" {
				t.Errorf("expected error code FORBIDDEN, got %q", resp.Error.Code)
			}
			if resp.Error.Message != tt.expectedMessage {
				t.Errorf("expected error message %q, got %q", tt.expectedMessage, resp.Error.Message)
			}
		})
	}
}

func TestNotFound(t *testing.T) {
	tests := []struct {
		name            string
		resource        string
		expectedMessage string
	}{
		{"with resource name", "User", "Foydalanuvchi topilmadi"},
		{"empty resource uses default", "", "Ma'lumot topilmadi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			NotFound(c, tt.resource)

			if w.Code != http.StatusNotFound {
				t.Errorf("expected status %d, got %d", http.StatusNotFound, w.Code)
			}

			resp := parseResponse(t, w)
			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error == nil {
				t.Fatal("expected error info to be non-nil")
			}
			if resp.Error.Code != "NOT_FOUND" {
				t.Errorf("expected error code NOT_FOUND, got %q", resp.Error.Code)
			}
			if resp.Error.Message != tt.expectedMessage {
				t.Errorf("expected error message %q, got %q", tt.expectedMessage, resp.Error.Message)
			}
		})
	}
}

func TestInternalError(t *testing.T) {
	tests := []struct {
		name            string
		message         string
		expectedMessage string
	}{
		{"custom message", "database failure", "database failure"},
		{"empty message uses default", "", "Nimadir xato ketdi — qaytadan urinib ko'ring"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			InternalError(c, tt.message)

			if w.Code != http.StatusInternalServerError {
				t.Errorf("expected status %d, got %d", http.StatusInternalServerError, w.Code)
			}

			resp := parseResponse(t, w)
			if resp.Success {
				t.Error("expected success to be false")
			}
			if resp.Error == nil {
				t.Fatal("expected error info to be non-nil")
			}
			if resp.Error.Code != "INTERNAL_ERROR" {
				t.Errorf("expected error code INTERNAL_ERROR, got %q", resp.Error.Code)
			}
			if resp.Error.Message != tt.expectedMessage {
				t.Errorf("expected error message %q, got %q", tt.expectedMessage, resp.Error.Message)
			}
		})
	}
}

func TestPaginated(t *testing.T) {
	tests := []struct {
		name               string
		data               interface{}
		page, limit, total int
		expectedTotalPages int
		expectedHasNext    bool
		expectedHasPrev    bool
	}{
		{
			name:               "first page with more pages",
			data:               []string{"a", "b"},
			page:               1,
			limit:              2,
			total:              5,
			expectedTotalPages: 3,
			expectedHasNext:    true,
			expectedHasPrev:    false,
		},
		{
			name:               "middle page",
			data:               []string{"c", "d"},
			page:               2,
			limit:              2,
			total:              5,
			expectedTotalPages: 3,
			expectedHasNext:    true,
			expectedHasPrev:    true,
		},
		{
			name:               "last page",
			data:               []string{"e"},
			page:               3,
			limit:              2,
			total:              5,
			expectedTotalPages: 3,
			expectedHasNext:    false,
			expectedHasPrev:    true,
		},
		{
			name:               "exact fit single page",
			data:               []string{"a", "b"},
			page:               1,
			limit:              2,
			total:              2,
			expectedTotalPages: 1,
			expectedHasNext:    false,
			expectedHasPrev:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, w := newTestContext()
			Paginated(c, tt.data, tt.page, tt.limit, tt.total)

			if w.Code != http.StatusOK {
				t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
			}

			resp := parseResponse(t, w)
			if !resp.Success {
				t.Error("expected success to be true")
			}
			if resp.Error != nil {
				t.Error("expected error to be nil")
			}
			if resp.Meta == nil {
				t.Fatal("expected meta to be non-nil")
			}
			if resp.Meta.Page != tt.page {
				t.Errorf("expected meta.page %d, got %d", tt.page, resp.Meta.Page)
			}
			if resp.Meta.Limit != tt.limit {
				t.Errorf("expected meta.limit %d, got %d", tt.limit, resp.Meta.Limit)
			}
			if resp.Meta.Total != tt.total {
				t.Errorf("expected meta.total %d, got %d", tt.total, resp.Meta.Total)
			}
			if resp.Meta.TotalPages != tt.expectedTotalPages {
				t.Errorf("expected meta.total_pages %d, got %d", tt.expectedTotalPages, resp.Meta.TotalPages)
			}
			if resp.Meta.HasNext != tt.expectedHasNext {
				t.Errorf("expected meta.has_next %v, got %v", tt.expectedHasNext, resp.Meta.HasNext)
			}
			if resp.Meta.HasPrev != tt.expectedHasPrev {
				t.Errorf("expected meta.has_prev %v, got %v", tt.expectedHasPrev, resp.Meta.HasPrev)
			}
		})
	}
}

func TestSuccessWithMeta(t *testing.T) {
	c, w := newTestContext()
	pagination := &entity.Pagination{
		Page:    2,
		Limit:   10,
		Total:   25,
		Pages:   3,
		HasNext: true,
		HasPrev: true,
	}
	SuccessWithMeta(c, []string{"item"}, pagination)

	if w.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, w.Code)
	}

	resp := parseResponse(t, w)
	if !resp.Success {
		t.Error("expected success to be true")
	}
	if resp.Meta == nil {
		t.Fatal("expected meta to be non-nil")
	}
	if resp.Meta.Page != 2 {
		t.Errorf("expected page 2, got %d", resp.Meta.Page)
	}
	if resp.Meta.TotalPages != 3 {
		t.Errorf("expected total_pages 3, got %d", resp.Meta.TotalPages)
	}
}

func TestErrorWithDetails(t *testing.T) {
	c, w := newTestContext()
	details := map[string]string{"field": "email", "reason": "invalid format"}
	ErrorWithDetails(c, http.StatusBadRequest, "VALIDATION_ERROR", "Validation failed", details)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected status %d, got %d", http.StatusBadRequest, w.Code)
	}

	resp := parseResponse(t, w)
	if resp.Success {
		t.Error("expected success to be false")
	}
	if resp.Error == nil {
		t.Fatal("expected error info to be non-nil")
	}
	if resp.Error.Details == nil {
		t.Fatal("expected details to be non-nil")
	}
	if resp.Error.Details["field"] != "email" {
		t.Errorf("expected detail field=email, got %q", resp.Error.Details["field"])
	}
}

func TestNoContent(t *testing.T) {
	w := httptest.NewRecorder()
	r := gin.New()
	r.GET("/", func(c *gin.Context) {
		NoContent(c)
	})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Errorf("expected status %d, got %d", http.StatusNoContent, w.Code)
	}
}
