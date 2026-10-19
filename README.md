# Travel Agency Demo

Учебный пет-проект турагентства: небольшой каталог путешествий и форма отправки заявки. Основной фокус — Go API, PostgreSQL и дальнейшая практика эксплуатации приложения.

## Текущий сценарий

1. Посетитель открывает каталог туров.
2. Интерфейс получает туры через `GET /api/v1/tours`.
3. Посетитель отправляет заявку на выбранный тур.
4. API валидирует запрос и сохраняет заявку в PostgreSQL со статусом `pending`.

В первой версии обработка синхронная. RabbitMQ и кэш не используются.

## Технологии и границы

- Go HTTP-сервис отдаёт API и статические файлы интерфейса.
- PostgreSQL хранит туры и заявки.
- Docker Compose используется для локального ручного запуска.
- Локальный Kubernetes-стенд описан манифестами для API и PostgreSQL.
- Метрики Prometheus, Grafana, Loki и Grafana Alloy запланированы следующим этапом.
- Платежи, учётные записи, админ-панель и реальные внешние интеграции не входят в первую версию.

## Запуск

Нужны Docker и Docker Compose. Из корня проекта выполните:

```sh
docker compose up --build
```

После запуска откройте <http://localhost:8080>. API доступен по адресу <http://localhost:8080/api/v1/tours>; состояние сервиса — `/healthz` и `/readyz`.

Compose создаёт локальную базу и загружает демонстрационный каталог при первом создании volume. Чтобы удалить локальные данные и загрузить начальные данные заново:

```sh
docker compose down -v
docker compose up --build
```

## API

### `GET /api/v1/tours`

Возвращает список опубликованных туров.

### `GET /api/v1/tours/{id}`

Возвращает тур или `404`.

### `POST /api/v1/bookings`

Создаёт заявку. Тело запроса:

```json
{
  "tour_id": 1,
  "customer_name": "Анна Иванова",
  "email": "anna@example.com",
  "phone": "+79990000000"
}
```

Успешный ответ — `201 Created` с идентификатором и статусом `pending`. Ошибки возвращаются в JSON с полем `error`.

## Ручная проверка первой версии

- Страница загружается, каталог содержит демонстрационные туры.
- Для каждого тура можно отправить заявку с корректными контактными данными.
- При успешной отправке показывается подтверждение с номером заявки.
- Пустые и некорректные значения показывают понятную ошибку.
- При недоступности API интерфейс сообщает, что данные не удалось загрузить.

## Автоматические проверки

HTTP-обработчики покрыты тестами на успешные ответы, ошибки, валидацию и некорректные JSON-запросы. Запустить проверки локально:

```sh
go test ./...
go vet ./...
```

GitHub Actions запускает форматирование, `go vet`, Go-тесты и сборку контейнера при push в `main` и при Pull Request в `main`. Workflow не публикует образ.

## Локальный Kubernetes

Манифесты рассчитаны на локальный кластер Minikube с настроенным StorageClass. API и PostgreSQL запускаются в namespace `travel-demo`; база использует StatefulSet и PVC. Порт приложения пробрасывается локально через `kubectl port-forward`, Ingress не требуется.

```sh
cp deploy/kubernetes/local-secrets.env.example deploy/kubernetes/local-secrets.env
# При необходимости поменяй локальный пароль в файле и DATABASE_URL.
docker build -t travel-agency-demo:local .
minikube image load travel-agency-demo:local
kubectl apply -f deploy/kubernetes/namespace.yaml
kubectl create secret generic travel-secrets -n travel-demo \
  --from-env-file=deploy/kubernetes/local-secrets.env \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl create configmap db-init -n travel-demo \
  --from-file=001-init.sql=db/init.sql \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -f deploy/kubernetes/database.yaml
kubectl rollout status statefulset/postgres -n travel-demo --timeout=180s
kubectl apply -f deploy/kubernetes/api.yaml
kubectl rollout status deployment/travel-api -n travel-demo --timeout=180s
kubectl port-forward -n travel-demo service/travel-api 8080:8080
```

Открой <http://localhost:8080>. Секретный env-файл исключён из Git. Для удаления учебного стенда:

```sh
kubectl delete namespace travel-demo
```

Удаление namespace удалит заявки и PVC-объект. Удаление самих файлов базы зависит от политики удаления томов выбранного StorageClass.

## Дальнейший план

1. Ручная проверка интерфейса и API — выполнена.
2. Автоматические unit и HTTP/API-тесты — добавлены.
3. CI: форматирование, `go vet`, тесты и сборка контейнера — настроен.
4. Kubernetes-манифесты приложения и PostgreSQL — подготовлены, нужна ручная проверка в Minikube.
5. Метрики Prometheus, логи в Loki через Grafana Alloy, дашборды Grafana и базовые алерты.
6. Документация окружений, диагностики, релиза и отката.
