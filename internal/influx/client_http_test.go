package influx

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, srv *httptest.Server, token string) *Client {
	t.Helper()
	c, err := New(srv.URL, token, "testdb")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c
}

func TestQuery_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v3/query_sql" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("unexpected content-type: %s", r.Header.Get("Content-Type"))
		}
		_, _ = w.Write([]byte(`[{"sport":"running","distance_m":5000}]`))
	}))
	defer srv.Close()

	rows, err := newTestClient(t, srv, "").Query(context.Background(), "SELECT * FROM activity LIMIT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0]["sport"] != "running" {
		t.Errorf("unexpected sport: %v", rows[0]["sport"])
	}
}

func TestQuery_EmptyResult(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	rows, err := newTestClient(t, srv, "").Query(context.Background(), "SELECT * FROM activity WHERE 1=0")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestQuery_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`bad sql`))
	}))
	defer srv.Close()

	_, err := newTestClient(t, srv, "").Query(context.Background(), "INVALID SQL")
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestQuery_SendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	_, _ = newTestClient(t, srv, "mytoken").Query(context.Background(), "SELECT 1")
	if gotAuth != "Bearer mytoken" {
		t.Errorf("expected 'Bearer mytoken', got %q", gotAuth)
	}
}

func TestQuery_NoAuthHeaderWhenTokenEmpty(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	_, _ = newTestClient(t, srv, "").Query(context.Background(), "SELECT 1")
	if gotAuth != "" {
		t.Errorf("expected no auth header, got %q", gotAuth)
	}
}

func TestQueryWithParams_SendsParams(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	params := map[string]any{"sport": "running", "limit": float64(10)}
	_, _ = newTestClient(t, srv, "").QueryWithParams(context.Background(),
		"SELECT * FROM activity WHERE sport=$sport LIMIT $limit", params)

	got, ok := body["params"].(map[string]any)
	if !ok {
		t.Fatalf("params not in request body: %v", body)
	}
	if got["sport"] != "running" {
		t.Errorf("unexpected sport param: %v", got["sport"])
	}
	if got["limit"] != float64(10) {
		t.Errorf("unexpected limit param: %v", got["limit"])
	}
}

func TestQueryWithParams_OmitsParamsWhenNil(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	_, _ = newTestClient(t, srv, "").QueryWithParams(context.Background(), "SELECT 1", nil)
	if _, hasParams := body["params"]; hasParams {
		t.Error("params key should be absent when nil")
	}
}

func TestWritePoints_SendsLineProtocol(t *testing.T) {
	var gotBody, gotDB string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v3/write_lp" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		gotDB = r.URL.Query().Get("db")
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := NewPoint("training_load").SetField("atl_7day", 42.3)
	if err := newTestClient(t, srv, "").WritePoints(context.Background(), p); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotDB != "testdb" {
		t.Errorf("expected db=testdb, got %q", gotDB)
	}
	if !strings.Contains(gotBody, "training_load") {
		t.Errorf("body missing measurement: %q", gotBody)
	}
	if !strings.Contains(gotBody, "atl_7day=42.3") {
		t.Errorf("body missing field: %q", gotBody)
	}
}

func TestWritePoints_MultiplePointsJoinedByNewline(t *testing.T) {
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		gotBody = string(data)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p1 := NewPoint("m1").SetField("f", 1.0)
	p2 := NewPoint("m2").SetField("f", 2.0)
	_ = newTestClient(t, srv, "").WritePoints(context.Background(), p1, p2)

	lines := strings.Split(strings.TrimRight(gotBody, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 lines, got %d: %q", len(lines), gotBody)
	}
	if !strings.HasPrefix(lines[0], "m1 ") {
		t.Errorf("first line should be m1: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "m2 ") {
		t.Errorf("second line should be m2: %q", lines[1])
	}
}

func TestWritePoints_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`unauthorized`))
	}))
	defer srv.Close()

	err := newTestClient(t, srv, "").WritePoints(context.Background(), NewPoint("m").SetField("f", 1.0))
	if err == nil {
		t.Fatal("expected error for 401 response")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should mention status code: %v", err)
	}
}

func TestQuery_NullBodyReturnsEmptySlice(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`null`))
	}))
	defer srv.Close()

	rows, err := newTestClient(t, srv, "").Query(context.Background(), "SELECT 1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rows == nil {
		t.Error("rows should be non-nil empty slice, not nil")
	}
	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestWritePoints_NilPointReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	var p *Point
	err := newTestClient(t, srv, "").WritePoints(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for nil *Point")
	}
}

func TestWritePoints_EmptyFieldsReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := NewPoint("training_load")
	err := newTestClient(t, srv, "").WritePoints(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for point with no fields")
	}
}

func TestWritePoints_EmptyMeasurementReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	p := NewPoint("").SetField("x", 1.0)
	err := newTestClient(t, srv, "").WritePoints(context.Background(), p)
	if err == nil {
		t.Fatal("expected error for point with empty measurement")
	}
}

func TestWritePoints_NoOpOnEmpty(t *testing.T) {
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	}))
	defer srv.Close()

	if err := newTestClient(t, srv, "").WritePoints(context.Background()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("WritePoints with no points should not make an HTTP request")
	}
}

func TestClose_ReturnsNil(t *testing.T) {
	c, _ := New("http://localhost:8181", "", "garmin")
	if err := c.Close(); err != nil {
		t.Errorf("Close should return nil, got: %v", err)
	}
}
