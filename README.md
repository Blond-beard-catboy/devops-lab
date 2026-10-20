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
- Локальный Kubernetes-стенд описан манифестами для API, PostgreSQL и observability-стека.
- Prometheus собирает метрики API, Grafana показывает преднастроенный dashboard, Loki хранит логи, Grafana Alloy собирает и отправляет их.
- Платежи, учётные записи, админ-панель и реальные внешние интеграции не входят в первую версию.

## Запуск

Нужны Docker и Docker Compose. Из корня проекта выполните:

```sh
docker compose up --build -d
```

После запуска открой <http://localhost:8080>. Grafana доступна на <http://localhost:3000>, Prometheus — на <http://localhost:9090>. Локальный пароль Grafana задаётся через `GRAFANA_ADMIN_PASSWORD` в `.env`; по умолчанию используется `admin-local-only`.

В Grafana открой **Dashboards → Travel Agency → Travel Agency Overview**. Источники Prometheus и Loki подключаются автоматически. Alloy отправляет в Loki логи API и PostgreSQL через ограниченный Docker Socket Proxy; Alloy не получает прямой доступ к Docker socket.

API публикует `/metrics`, а Prometheus скрейпит его каждые 15 секунд. На странице **Alerts** Prometheus отображаются правила доступности API, ошибок HTTP 5xx и p95 latency. Для отправки уведомлений понадобится отдельный Alertmanager, он пока не входит в стенд.

Compose создаёт локальную базу и загружает демонстрационный каталог при первом создании volume. Чтобы удалить все локальные данные, включая базу, метрики, логи и состояние Grafana, выполни:

```sh
docker compose down -v
docker compose up --build -d
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

Для локальных Go-команд нужен Go 1.25 или новее. HTTP-обработчики покрыты тестами на успешные ответы, ошибки, валидацию и некорректные JSON-запросы. Запустить проверки локально:

```sh
go test ./...
go vet ./...
```

GitHub Actions запускает форматирование, `go vet`, Go-тесты и сборку контейнера при push в `main` и при Pull Request в `main`. Workflow не публикует образ.

## Локальный Kubernetes

Манифесты рассчитаны на локальный кластер Minikube с настроенным StorageClass. API и PostgreSQL запускаются в namespace `travel-demo`; база использует StatefulSet и PVC. Prometheus, Grafana, Loki и Alloy устанавливаются Kustomize-манифестами в тот же namespace. Grafana и API доступны через фиксированные NodePort `30300` и `30080`; для Minikube с Docker driver на Linux постоянный `kubectl port-forward` не нужен.

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

cp deploy/kubernetes/grafana-secrets.env.example deploy/kubernetes/grafana-secrets.env
# Замени локальный пароль в grafana-secrets.env.
kubectl create secret generic grafana-admin -n travel-demo \
  --from-env-file=deploy/kubernetes/grafana-secrets.env \
  --dry-run=client -o yaml | kubectl apply -f -
kubectl apply -k deploy/kubernetes/observability
kubectl rollout status deployment/prometheus -n travel-demo --timeout=180s
kubectl rollout status deployment/loki -n travel-demo --timeout=180s
kubectl rollout status deployment/alloy -n travel-demo --timeout=180s
kubectl rollout status deployment/grafana -n travel-demo --timeout=180s
```

Получить адреса можно командами:

```sh
echo "http://$(minikube ip):30080" # приложение
echo "http://$(minikube ip):30300" # Grafana
```

Логин Grafana — `admin`, пароль хранится в игнорируемом Git файле `deploy/kubernetes/grafana-secrets.env`. Prometheus и Loki доступны внутри кластера; для локальной диагностики можно временно запустить `kubectl port-forward -n travel-demo service/prometheus 9090:9090` или `kubectl port-forward -n travel-demo service/loki 3100:3100`.

Alloy читает логи только pod-ов API и PostgreSQL через Kubernetes API. Для этого ему выдан namespaced Role с доступом к pod metadata и `pods/log`; права на узлы и другие namespaces не нужны. Метрики API и Prometheus сохраняются в PVC, как и Grafana и Loki. Секретные env-файлы исключены из Git.

Для удаления учебного стенда:

```sh
kubectl delete namespace travel-demo
```

Удаление namespace удалит заявки, метрики, логи и PVC-объекты. Удаление файлов самих томов зависит от политики StorageClass Minikube.

## Дальнейший план

1. Ручная проверка интерфейса и API — выполнена.
2. Автоматические unit и HTTP/API-тесты — добавлены.
3. CI: форматирование, `go vet`, тесты и сборка контейнера — настроен.
4. Kubernetes-манифесты приложения и PostgreSQL — подготовлены, нужна ручная проверка в Minikube.
5. Prometheus, Grafana, Loki, Alloy, преднастроенный dashboard и базовые alert rules — добавлены, нужна ручная проверка.
6. Документация окружений, диагностики, релиза и отката.
