package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

type fakeStore struct {
	pingErr, listErr, getErr, createErr error
	tours                               []Tour
	tour                                Tour
	booking                             Booking
	gotID                               int64
	gotInput                            CreateBookingInput
	pingCalled                          bool
}

func (s *fakeStore) Ping(context.Context) error                { s.pingCalled = true; return s.pingErr }
func (s *fakeStore) ListTours(context.Context) ([]Tour, error) { return s.tours, s.listErr }
func (s *fakeStore) GetTour(_ context.Context, id int64) (Tour, error) {
	s.gotID = id
	return s.tour, s.getErr
}
func (s *fakeStore) CreateBooking(_ context.Context, in CreateBookingInput) (Booking, error) {
	s.gotInput = in
	return s.booking, s.createErr
}

func testHandler(s *fakeStore) http.Handler {
	return New(s, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func request(h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func assertError(t *testing.T, w *httptest.ResponseRecorder, status int) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status = %d, want %d; body=%s", w.Code, status, w.Body.String())
	}
	var got map[string]string
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || got["error"] == "" {
		t.Fatalf("expected JSON error, got %q (err=%v)", w.Body.String(), err)
	}
}

func TestHealthReadinessAndHeaders(t *testing.T) {
	s := &fakeStore{}
	h := testHandler(s)
	w := request(h, http.MethodGet, "/healthz", "")
	if w.Code != http.StatusOK || w.Body.String() != "ok\n" {
		t.Fatalf("health = %d %q", w.Code, w.Body.String())
	}
	if s.pingCalled {
		t.Fatal("health check must not depend on database")
	}
	for name, value := range map[string]string{"X-Content-Type-Options": "nosniff", "X-Frame-Options": "DENY", "Referrer-Policy": "strict-origin-when-cross-origin"} {
		if got := w.Header().Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	w = request(h, http.MethodGet, "/readyz", "")
	if w.Code != http.StatusOK || !s.pingCalled {
		t.Fatalf("ready = %d, ping called=%v", w.Code, s.pingCalled)
	}
	s.pingErr = errors.New("secret database detail")
	w = request(h, http.MethodGet, "/readyz", "")
	assertError(t, w, http.StatusServiceUnavailable)
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("readiness leaked internal error")
	}
}

func TestToursRoutes(t *testing.T) {
	s := &fakeStore{tours: []Tour{{ID: 1, Title: "Sochi"}}, tour: Tour{ID: 7, Title: "Altai"}}
	h := testHandler(s)
	w := request(h, http.MethodGet, "/api/v1/tours", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"tours":[{"id":1`) {
		t.Fatalf("list response: %d %s", w.Code, w.Body.String())
	}
	w = request(h, http.MethodGet, "/api/v1/tours/7", "")
	if w.Code != http.StatusOK || s.gotID != 7 || !strings.Contains(w.Body.String(), `"id":7`) {
		t.Fatalf("detail response: %d %s (id=%d)", w.Code, w.Body.String(), s.gotID)
	}
	for _, path := range []string{"/api/v1/tours/0", "/api/v1/tours/-1", "/api/v1/tours/1.5", "/api/v1/tours/999999999999999999999"} {
		w = request(h, http.MethodGet, path, "")
		assertError(t, w, http.StatusBadRequest)
	}
	s.getErr = pgx.ErrNoRows
	assertError(t, request(h, http.MethodGet, "/api/v1/tours/999", ""), http.StatusNotFound)
	s.getErr = errors.New("internal secret")
	w = request(h, http.MethodGet, "/api/v1/tours/1", "")
	assertError(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("detail route leaked internal error")
	}
	s.listErr = errors.New("internal secret")
	w = request(h, http.MethodGet, "/api/v1/tours", "")
	assertError(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), "secret") {
		t.Fatal("list route leaked internal error")
	}
}

func TestCreateBookingValidationAndStrictJSON(t *testing.T) {
	s := &fakeStore{booking: Booking{ID: 9, TourID: 1, Status: "pending", CreatedAt: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)}}
	h := testHandler(s)
	valid := `{"tour_id":1,"customer_name":" Анна Иванова ","email":" anna@example.com ","phone":" +79990000000 "}`
	w := request(h, http.MethodPost, "/api/v1/bookings", valid)
	if w.Code != http.StatusCreated {
		t.Fatalf("valid booking = %d %s", w.Code, w.Body.String())
	}
	if s.gotInput.CustomerName != "Анна Иванова" || s.gotInput.Email != "anna@example.com" || s.gotInput.Phone != "+79990000000" {
		t.Fatalf("input not normalized: %+v", s.gotInput)
	}
	var response Booking
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil || response.Status != "pending" || response.ID != 9 {
		t.Fatalf("booking response: %+v err=%v", response, err)
	}
	bad := []string{
		`{`, `null`, `[]`, `{"tour_id":1,"customer_name":"Valid Name","email":"a@example.com","phone":"1234567","admin":true}`,
		`{"tour_id":1,"customer_name":"Valid Name","email":"a@example.com","phone":"1234567"} {}`,
		`{"tour_id":1,"customer_name":"Valid Name","email":"a@example.com","phone":"1234567","tour_id":2}`,
		`{"tour_id":1,"customer_name":"Valid Name","email":"a@example.com","phone":"1234567","customer_name":"evil"}`,
	}
	for _, body := range bad {
		assertError(t, request(h, http.MethodPost, "/api/v1/bookings", body), http.StatusBadRequest)
	}
	if s.gotInput.CustomerName == "evil" || s.gotInput.TourID != 1 {
		t.Fatal("malformed request reached store")
	}
	tooLarge := `{"tour_id":1,"customer_name":"` + strings.Repeat("x", 33<<10) + `","email":"a@example.com","phone":"1234567"}`
	assertError(t, request(h, http.MethodPost, "/api/v1/bookings", tooLarge), http.StatusBadRequest)
	invalid := []string{
		`{"tour_id":0,"customer_name":"Valid Name","email":"a@example.com","phone":"1234567"}`,
		`{"tour_id":-1,"customer_name":"Valid Name","email":"a@example.com","phone":"1234567"}`,
		`{"tour_id":1,"customer_name":"x","email":"a@example.com","phone":"1234567"}`,
		`{"tour_id":1,"customer_name":"` + strings.Repeat("界", 101) + `","email":"a@example.com","phone":"1234567"}`,
		`{"tour_id":1,"customer_name":"Valid Name","email":"Name <a@example.com>","phone":"1234567"}`,
		`{"tour_id":1,"customer_name":"Valid Name","email":"not-an-email","phone":"1234567"}`,
		`{"tour_id":1,"customer_name":"Valid Name","email":"` + strings.Repeat("a", 250) + `@example.com","phone":"1234567"}`,
		`{"tour_id":1,"customer_name":"Valid Name","email":"a@example.com","phone":"123456"}`,
		`{"tour_id":1,"customer_name":"Valid Name","email":"a@example.com","phone":"` + strings.Repeat("1", 26) + `"}`,
	}
	for _, body := range invalid {
		assertError(t, request(h, http.MethodPost, "/api/v1/bookings", body), http.StatusUnprocessableEntity)
	}
	if s.gotInput.CustomerName != "Анна Иванова" {
		t.Fatal("invalid request reached store")
	}
}

func TestCreateBookingStoreFailuresAndSQLPayload(t *testing.T) {
	s := &fakeStore{booking: Booking{ID: 1, Status: "pending"}}
	h := testHandler(s)
	valid := `{"tour_id":1,"customer_name":"Robert'); DROP TABLE tours;--","email":"x@example.com","phone":"1234567"}`
	s.createErr = pgx.ErrNoRows
	assertError(t, request(h, http.MethodPost, "/api/v1/bookings", valid), http.StatusNotFound)
	s.createErr = errors.New("database password leaked")
	w := request(h, http.MethodPost, "/api/v1/bookings", valid)
	assertError(t, w, http.StatusInternalServerError)
	if strings.Contains(w.Body.String(), "password") {
		t.Fatal("booking route leaked internal error")
	}
	s.createErr = nil
	w = request(h, http.MethodPost, "/api/v1/bookings", valid)
	if w.Code != http.StatusCreated || s.gotInput.CustomerName != "Robert'); DROP TABLE tours;--" {
		t.Fatalf("SQL-like customer text was rejected/changed: %d %+v", w.Code, s.gotInput)
	}
}

func TestStaticPageAndUnknownPaths(t *testing.T) {
	h := testHandler(&fakeStore{})
	w := request(h, http.MethodGet, "/", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("index response: %d %#v", w.Code, w.Header())
	}
	for _, path := range []string{"/not-found", "/index.html", "/..%2fREADME.md"} {
		w = request(h, http.MethodGet, path, "")
		if w.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", path, w.Code)
		}
	}
	w = request(h, http.MethodPost, "/api/v1/tours", "")
	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("wrong method = %d, want 405", w.Code)
	}
}

func TestBookingBodyLimitWithReader(t *testing.T) {
	// Exercise a body that crosses the limit while decoding, rather than relying on a short fixed request.
	s := &fakeStore{}
	h := testHandler(s)
	body := bytes.NewReader([]byte(`{"tour_id":1,"customer_name":"` + strings.Repeat("a", 40<<10) + `"}`))
	r := httptest.NewRequest(http.MethodPost, "/api/v1/bookings", body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	assertError(t, w, http.StatusBadRequest)
}
