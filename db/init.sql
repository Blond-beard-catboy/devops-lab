CREATE TABLE tours (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    destination TEXT NOT NULL,
    description TEXT NOT NULL,
    duration_days INTEGER NOT NULL CHECK (duration_days > 0),
    price_cents BIGINT NOT NULL CHECK (price_cents >= 0),
    currency CHAR(3) NOT NULL DEFAULT 'RUB',
    image_url TEXT NOT NULL DEFAULT '',
    published BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE bookings (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    tour_id BIGINT NOT NULL REFERENCES tours(id),
    customer_name TEXT NOT NULL,
    email TEXT NOT NULL,
    phone TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'confirmed', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX bookings_created_at_idx ON bookings (created_at DESC);

INSERT INTO tours (slug, title, destination, description, duration_days, price_cents, image_url) VALUES
('sochi-week', 'Неделя у Чёрного моря', 'Сочи', 'Морской воздух, прогулки по набережной и поездка в горы.', 7, 6890000, 'https://images.unsplash.com/photo-1507525428034-b723cf961d3e?auto=format&fit=crop&w=1200&q=80'),
('altai-trails', 'Тропами Алтая', 'Республика Алтай', 'Горные пейзажи, чистые реки и небольшая группа путешественников.', 8, 9450000, 'https://images.unsplash.com/photo-1464822759023-fed622ff2c3b?auto=format&fit=crop&w=1200&q=80'),
('kazan-weekend', 'Выходные в Казани', 'Казань', 'Исторический центр, татарская кухня и прогулка по набережной.', 3, 3290000, 'https://images.unsplash.com/photo-1519608487953-e999c86e7455?auto=format&fit=crop&w=1200&q=80');
