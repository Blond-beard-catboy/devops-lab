package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"travel-agency-demo/internal/httpapi"
)

type Store struct {
	pool *pgxpool.Pool
}

func New(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

func (s *Store) Ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *Store) ListTours(ctx context.Context) ([]httpapi.Tour, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, slug, title, destination, description, duration_days, price_cents, currency, image_url
		FROM tours WHERE published = true ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("query tours: %w", err)
	}
	defer rows.Close()

	tours := make([]httpapi.Tour, 0)
	for rows.Next() {
		var tour httpapi.Tour
		if err := rows.Scan(&tour.ID, &tour.Slug, &tour.Title, &tour.Destination, &tour.Description, &tour.DurationDays, &tour.PriceCents, &tour.Currency, &tour.ImageURL); err != nil {
			return nil, fmt.Errorf("scan tour: %w", err)
		}
		tours = append(tours, tour)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tours: %w", err)
	}
	return tours, nil
}

func (s *Store) GetTour(ctx context.Context, id int64) (httpapi.Tour, error) {
	var tour httpapi.Tour
	err := s.pool.QueryRow(ctx, `
		SELECT id, slug, title, destination, description, duration_days, price_cents, currency, image_url
		FROM tours WHERE id = $1 AND published = true`, id).Scan(
		&tour.ID, &tour.Slug, &tour.Title, &tour.Destination, &tour.Description, &tour.DurationDays, &tour.PriceCents, &tour.Currency, &tour.ImageURL,
	)
	if err != nil {
		return httpapi.Tour{}, err
	}
	return tour, nil
}

func (s *Store) CreateBooking(ctx context.Context, input httpapi.CreateBookingInput) (httpapi.Booking, error) {
	var booking httpapi.Booking
	err := s.pool.QueryRow(ctx, `
		INSERT INTO bookings (tour_id, customer_name, email, phone)
		SELECT id, $2, $3, $4 FROM tours WHERE id = $1 AND published = true
		RETURNING id, tour_id, status, created_at`, input.TourID, input.CustomerName, input.Email, input.Phone).Scan(
		&booking.ID, &booking.TourID, &booking.Status, &booking.CreatedAt,
	)
	if err != nil {
		return httpapi.Booking{}, err
	}
	return booking, nil
}
