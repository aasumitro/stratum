package response

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// --- helpers ---

func ginCtxWithQuery(params map[string]string) *gin.Context {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	q := req.URL.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	req.URL.RawQuery = q.Encode()
	c.Request = req
	return c
}

func newCtx(reqID, query string) (*gin.Context, *httptest.ResponseRecorder) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/"+query, nil)
	if reqID != "" {
		c.Writer.Header().Set(requestIDHeader, reqID)
	}
	return c, w
}

func decode(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("decode body: %v (raw: %s)", err, w.Body.String())
	}
	return m
}

func statusField(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	s, ok := body["status"].(map[string]any)
	if !ok {
		t.Fatalf("status field missing or wrong type: %v", body["status"])
	}
	return s
}

// --- parseInt ---

func TestParseInt(t *testing.T) {
	for _, tc := range []struct {
		s        string
		fb, want int
	}{
		{"42", 0, 42},
		{"-1", 0, -1},
		{"0", 9, 0},
		{"abc", 5, 5},
		{"", 3, 3},
		{"1e3", 0, 0}, // not decimal integer
	} {
		if got := parseInt(tc.s, tc.fb); got != tc.want {
			t.Errorf("parseInt(%q, %d) = %d, want %d", tc.s, tc.fb, got, tc.want)
		}
	}
}

// --- paginationParams ---

func TestPaginationParams(t *testing.T) {
	for _, tc := range []struct {
		params              map[string]string
		limit, offset, page int
	}{
		{nil, 20, 0, 1}, // all defaults
		{map[string]string{"limit": "5", "page": "3"}, 5, 10, 3},      // page * limit offset
		{map[string]string{"limit": "10", "offset": "25"}, 10, 25, 1}, // explicit offset overrides
		{map[string]string{"limit": "abc"}, 20, 0, 1},                 // bad limit → default
		{map[string]string{"limit": "0"}, 20, 0, 1},                   // zero limit rejected (n > 0)
		{map[string]string{"page": "0"}, 20, 0, 1},                    // zero page rejected
		{map[string]string{"page": "2"}, 20, 20, 2},                   // page 2 default limit
		{map[string]string{"offset": "0"}, 20, 0, 1},                  // explicit zero offset accepted (n >= 0)
	} {
		l, o, p := paginationParams(ginCtxWithQuery(tc.params))
		if l != tc.limit || o != tc.offset || p != tc.page {
			t.Errorf("params=%v: got limit=%d offset=%d page=%d, want %d %d %d",
				tc.params, l, o, p, tc.limit, tc.offset, tc.page)
		}
	}
}

// --- Payload.JSON ---

func TestPayloadJSON_SuccessShape(t *testing.T) {
	c, w := newCtx("req-123", "")
	Success(gin.H{"key": "val"}).JSON(c, http.StatusOK)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := decode(t, w)
	s := statusField(t, body)
	if s["error"] != false {
		t.Errorf("status.error = %v, want false", s["error"])
	}
	if s["request_id"] != "req-123" {
		t.Errorf("status.request_id = %v, want req-123", s["request_id"])
	}
	data, _ := body["data"].(map[string]any)
	if data["key"] != "val" {
		t.Errorf("data.key = %v, want val", data["key"])
	}
}

func TestPayloadJSON_ErrorShape(t *testing.T) {
	c, w := newCtx("", "")
	Error("ERR_CODE", "oops").JSON(c, http.StatusBadRequest)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	s := statusField(t, decode(t, w))
	if s["error"] != true {
		t.Errorf("status.error = %v, want true", s["error"])
	}
	if s["code"] != "ERR_CODE" {
		t.Errorf("status.code = %v, want ERR_CODE", s["code"])
	}
	if s["message"] != "oops" {
		t.Errorf("status.message = %v, want oops", s["message"])
	}
}

func TestPayloadJSON_PaginationAdded(t *testing.T) {
	c, w := newCtx("", "?limit=2&page=1")
	List([]string{"a", "b"}, 10).JSON(c, http.StatusOK)

	body := decode(t, w)
	pg, ok := body["pagination"].(map[string]any)
	if !ok {
		t.Fatal("pagination field missing when totalData > limit")
	}
	if pg["total_items"] != float64(10) {
		t.Errorf("pagination.total_items = %v, want 10", pg["total_items"])
	}
	if pg["total_pages"] != float64(5) {
		t.Errorf("pagination.total_pages = %v, want 5", pg["total_pages"])
	}
}

func TestPayloadJSON_NoPaginationWhenFitsOnePage(t *testing.T) {
	c, w := newCtx("", "")
	List([]string{"a", "b"}, 2).JSON(c, http.StatusOK) // total=2 <= default limit=20

	if body := decode(t, w); body["pagination"] != nil {
		t.Error("pagination should be absent when totalData <= limit")
	}
}

func TestPayloadJSON_NoPaginationForNilData(t *testing.T) {
	c, w := newCtx("", "")
	List(nil, 100).JSON(c, http.StatusOK) // data=nil skips pagination

	if body := decode(t, w); body["pagination"] != nil {
		t.Error("pagination should be absent when data is nil")
	}
}

// --- Error builder ---

func TestError_NoDetails(t *testing.T) {
	p := Error("CODE", "msg")
	if p.Status.Code != "CODE" || p.Status.Message != "msg" || p.Status.Details != nil {
		t.Errorf("Error() = %+v", p.Status)
	}
}

func TestError_SingleDetail(t *testing.T) {
	p := Error("CODE", "msg", "detail")
	if p.Status.Details != "detail" {
		t.Errorf("single detail = %v, want detail", p.Status.Details)
	}
}

func TestError_MultipleDetails(t *testing.T) {
	p := Error("CODE", "msg", "d1", "d2")
	details, ok := p.Status.Details.([]any)
	if !ok || len(details) != 2 {
		t.Errorf("multiple details = %v (%T), want []any len=2", p.Status.Details, p.Status.Details)
	}
}
