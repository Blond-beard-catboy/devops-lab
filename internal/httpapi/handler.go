package httpapi

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

//go:embed static/*
var webFiles embed.FS

type Tour struct {
	ID           int64  `json:"id"`
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Destination  string `json:"destination"`
	Description  string `json:"description"`
	DurationDays int    `json:"duration_days"`
	PriceCents   int64  `json:"price_cents"`
	Currency     string `json:"currency"`
	ImageURL     string `json:"image_url"`
}

type CreateBookingInput struct {
	TourID       int64  `json:"tour_id"`
	CustomerName string `json:"customer_name"`
	Email        string `json:"email"`
	Phone        string `json:"phone"`
}

type Booking struct {
	ID        int64     `json:"id"`
	TourID    int64     `json:"tour_id"`
	Status    string    `json:"status"`
	CreatedAt time.Time `json:"created_at"`
}

type Store interface {
	Ping(context.Context) error
	ListTours(context.Context) ([]Tour, error)
	GetTour(context.Context, int64) (Tour, error)
	CreateBooking(context.Context, CreateBookingInput) (Booking, error)
}

type Handler struct {
	store   Store
	logger  *slog.Logger
	metrics *httpMetrics
}

func New(store Store, logger *slog.Logger) http.Handler {
	registry := prometheus.NewRegistry()
	metrics := newHTTPMetrics(registry)
	h := &Handler{store: store, logger: logger, metrics: metrics}
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.Handle("GET /healthz", metrics.instrument("GET", "/healthz", http.HandlerFunc(h.health)))
	mux.Handle("GET /readyz", metrics.instrument("GET", "/readyz", http.HandlerFunc(h.ready)))
	mux.Handle("GET /api/v1/tours", metrics.instrument("GET", "/api/v1/tours", http.HandlerFunc(h.listTours)))
	mux.Handle("GET /api/v1/tours/{id}", metrics.instrument("GET", "/api/v1/tours/{id}", http.HandlerFunc(h.getTour)))
	mux.Handle("POST /api/v1/bookings", metrics.instrument("POST", "/api/v1/bookings", http.HandlerFunc(h.createBooking)))
	mux.Handle("GET /", metrics.instrument("GET", "/", http.HandlerFunc(h.index)))
	return h.accessLog(securityHeaders(mux))
}

func (h *Handler) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (h *Handler) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := h.store.Ping(ctx); err != nil {
		h.logger.Error("readiness check failed", "error", err)
		h.writeError(w, http.StatusServiceUnavailable, "service is not ready")
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ready\n"))
}

func (h *Handler) listTours(w http.ResponseWriter, r *http.Request) {
	tours, err := h.store.ListTours(r.Context())
	if err != nil {
		h.logger.Error("list tours failed", "error", err)
		h.writeError(w, http.StatusInternalServerError, "could not load tours")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"tours": tours})
}

func (h *Handler) getTour(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		h.writeError(w, http.StatusBadRequest, "tour id must be a positive integer")
		return
	}
	tour, err := h.store.GetTour(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "tour not found")
		return
	}
	if err != nil {
		h.logger.Error("get tour failed", "tour_id", id, "error", err)
		h.writeError(w, http.StatusInternalServerError, "could not load tour")
		return
	}
	h.writeJSON(w, http.StatusOK, tour)
}

func (h *Handler) createBooking(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil || len(raw) == 0 || raw[0] != '{' {
		h.writeError(w, http.StatusBadRequest, "request body must be a JSON object")
		return
	}
	if hasDuplicateJSONKeys(raw) {
		h.writeError(w, http.StatusBadRequest, "request body must not contain duplicate fields")
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, "request body must contain a single JSON object")
		return
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input CreateBookingInput
	if err := decoder.Decode(&input); err != nil {
		h.writeError(w, http.StatusBadRequest, "request body must be valid JSON")
		return
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		h.writeError(w, http.StatusBadRequest, "request body must contain a single JSON object")
		return
	}
	if message := validateBooking(input); message != "" {
		h.writeError(w, http.StatusUnprocessableEntity, message)
		return
	}
	input.CustomerName = strings.TrimSpace(input.CustomerName)
	input.Email = strings.TrimSpace(input.Email)
	input.Phone = strings.TrimSpace(input.Phone)
	booking, err := h.store.CreateBooking(r.Context(), input)
	if errors.Is(err, pgx.ErrNoRows) {
		h.writeError(w, http.StatusNotFound, "tour not found")
		return
	}
	if err != nil {
		h.logger.Error("create booking failed", "tour_id", input.TourID, "error", err)
		h.writeError(w, http.StatusInternalServerError, "could not create booking")
		return
	}
	h.metrics.bookingsCreated.Inc()
	h.writeJSON(w, http.StatusCreated, booking)
}

func hasDuplicateJSONKeys(raw []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value func() (bool, error)
	value = func() (bool, error) {
		token, err := decoder.Token()
		if err != nil {
			return false, err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return false, nil
		}
		switch delim {
		case '{':
			seen := make(map[string]struct{})
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return false, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return false, errors.New("invalid object key")
				}
				if _, exists := seen[key]; exists {
					return true, nil
				}
				seen[key] = struct{}{}
				duplicate, err := value()
				if err != nil || duplicate {
					return duplicate, err
				}
			}
			_, err = decoder.Token()
			return false, err
		case '[':
			for decoder.More() {
				duplicate, err := value()
				if err != nil || duplicate {
					return duplicate, err
				}
			}
			_, err = decoder.Token()
			return false, err
		default:
			return false, errors.New("unexpected JSON delimiter")
		}
	}
	duplicate, err := value()
	return err != nil || duplicate
}

func validateBooking(input CreateBookingInput) string {
	if input.TourID < 1 {
		return "tour_id must be a positive integer"
	}
	name := strings.TrimSpace(input.CustomerName)
	if utf8.RuneCountInString(name) < 2 || utf8.RuneCountInString(name) > 100 {
		return "customer_name must contain between 2 and 100 characters"
	}
	address, err := mail.ParseAddress(strings.TrimSpace(input.Email))
	if err != nil || address.Address != strings.TrimSpace(input.Email) || len(input.Email) > 254 {
		return "email must be a valid email address"
	}
	phone := strings.TrimSpace(input.Phone)
	if utf8.RuneCountInString(phone) < 7 || utf8.RuneCountInString(phone) > 25 {
		return "phone must contain between 7 and 25 characters"
	}
	return ""
}

func (h *Handler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data, err := webFiles.ReadFile("static/index.html")
	if err != nil {
		h.logger.Error("read web page failed", "error", err)
		http.Error(w, "page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (h *Handler) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		h.logger.Error("write JSON response failed", "error", err)
	}
}

func (h *Handler) writeError(w http.ResponseWriter, status int, message string) {
	h.writeJSON(w, status, map[string]string{"error": message})
}

func (h *Handler) accessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		wrapped := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(wrapped, r)
		h.logger.Info("http request", "method", r.Method, "path", r.URL.Path, "status", wrapped.status, "duration_ms", time.Since(start).Milliseconds())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
