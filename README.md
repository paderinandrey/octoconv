# 🐙 OctoConv

Асинхронный сервис конвертации файлов. API принимает файл, кладёт его в S3-совместимое
хранилище и ставит задачу в очередь; воркеры запускают внешние движки (libvips, LibreOffice,
ffmpeg, …) и складывают результат обратно в хранилище. **Postgres — система записи** (source of
truth), Redis — брокер очереди и горячий прогресс.

Текущая итерация — сквозной рабочий срез (vertical slice) на одном классе движка: **image / libvips**.

## Стек

| Слой         | Выбор                          |
|--------------|--------------------------------|
| Язык         | Go 1.26                        |
| API          | chi                            |
| Очередь      | asynq + Redis (очередь на класс движка) |
| БД           | PostgreSQL 18 (source of truth)|
| Хранилище    | S3 / MinIO (presigned upload/download) |
| Процессы     | `os/exec` + context (таймауты, kill process group) |
| Деплой       | Docker / Docker Compose        |

## Структура

```
cmd/
  api/        — HTTP-сервер (приём файла, статусы)
  worker/     — обработчик очереди (asynq), движок image
  migrate/    — применение миграций БД
internal/
  api/        — роуты и хендлеры
  convert/    — интерфейс Converter, реестр пар, hardened exec, libvips
  jobs/       — репозиторий задач в Postgres
  queue/      — тип asynq-задачи и payload
  storage/    — обёртка над MinIO/S3
  worker/     — обработчик image:convert (скачать → vips → загрузить)
  db/         — pgx pool + раннер миграций (+ migrations/*.sql)
```

Реализован сквозной срез для класса движка **image** (libvips): png/jpg/webp/heic/tiff
между собой.

## Требования

- Go 1.26+
- Docker + Docker Compose

## Запуск

### 1. Поднять инфраструктуру

```bash
cp .env.example .env        # при необходимости поправить порты/креды
docker compose up -d
```

Поднимаются:

| Сервис   | Образ            | Порт (host) | Заметки                          |
|----------|------------------|-------------|----------------------------------|
| postgres | postgres:18      | **5434**    | `octo / octo-pass / octo_db`     |
| redis    | redis:8          | 6379        | брокер asynq                     |
| minio    | minio/minio      | **9100** (API), **9101** (консоль) | `minioadmin / minioadmin`, бакет `octoconv` создаётся автоматически |
| api      | Dockerfile.api   | **8090**    | HTTP API                         |
| worker   | Dockerfile.worker| —           | воркер image (libvips), под `nobody`, лимиты CPU/RAM |

> Нестандартные хост-порты (5434, 9100/9101, 8090) выбраны, чтобы не конфликтовать с другими
> локальными стеками (8080 занят локальным Ruby-приложением, 5433/5432 — локальным Rails-приложением).
> Меняются в `docker-compose.yml` и `.env`.
> MinIO-консоль: http://localhost:9101
>
> **Presigned URL в полном compose:** сервис `api` в контейнере presign'ит ссылки на
> внутренний endpoint `minio:9000`, недоступный с хоста. Для скачивания результата с хоста
> запускайте `api` локально (вариант ниже) — тогда ссылки идут на `localhost:9100`.

### 2. Применить миграции

```bash
set -a && . ./.env && set +a   # загрузить переменные окружения
go run ./cmd/migrate
```

Создаёт таблицы `clients`, `presets`, `jobs`, `job_inputs`, `job_outputs`, `job_events`,
`webhook_deliveries` (полный DDL — `internal/db/migrations/0001_init.sql`). Раннер идемпотентен:
применённые версии фиксируются в `schema_migrations`.

### 3. Запустить сервисы

Воркеру нужен `vips`, поэтому его удобнее запускать в контейнере. Рекомендуемый для локальной
разработки вариант — воркер в Docker, API на хосте (чтобы presigned-ссылки указывали на
`localhost:9100` и скачивались с хоста):

```bash
docker compose up -d --build worker     # воркер image с libvips, в compose-сети
set -a && . ./.env && set +a
go run ./cmd/api                         # HTTP API на :8090
```

Полностью в Docker (с оговоркой про presigned URL выше):

```bash
docker compose up -d --build            # postgres, redis, minio, api, worker
```

## Аутентификация

Все `/v1/*` эндпоинты требуют API-ключ клиента (`/healthz` — исключение, остаётся публичным).
Ключи выпускаются и управляются через CLI `manage-clients`, а не через API.

### Выпустить ключ

```bash
docker compose up -d                       # если инфраструктура ещё не поднята
set -a && . ./.env && set +a
go run ./cmd/migrate                       # применить миграции (идемпотентно)
go run ./cmd/manage-clients create "имя-клиента"
# client id: <uuid>
# api key (save now, shown once): <raw-key>
```

**Ключ печатается ровно один раз** — сохраните его сразу, он никогда не хранится и не
логируется в открытом виде (в БД — только salted SHA-256 хеш).

### Ротация без даунтайма и отзыв

Схема поддерживает два одновременно активных ключа на клиента (primary/secondary):

```bash
go run ./cmd/manage-clients add-key <client-id>
# добавляет второй активный ключ — оба валидны, пока не отозван старый
# api key (save now, shown once): <new-raw-key>

go run ./cmd/manage-clients revoke <client-id> <primary|secondary>
# отзывает конкретный слот; запись клиента не удаляется — история задач сохраняется
```

## API

Поставить задачу:

```bash
curl -H "Authorization: ApiKey <raw-key>" \
  -F file=@report.png -F target=webp http://localhost:8090/v1/jobs
# {"job_id":"...","status":"queued"}
```

Статус / результат:

```bash
curl -H "Authorization: ApiKey <raw-key>" http://localhost:8090/v1/jobs/<job_id>
# {"job_id":"...","status":"done","download_url":"..."}
```

Без ключа или с неверным/отозванным ключом — `401`. Чужой job (не принадлежащий клиенту) —
`404`, как и реально несуществующий (никогда `403` — не подтверждаем существование чужого job).
Превышение лимита запросов — `429` с заголовком `Retry-After`.

## Конфигурация (`.env`)

| Переменная        | Назначение                          |
|-------------------|-------------------------------------|
| `DATABASE_URL`    | DSN Postgres                        |
| `REDIS_ADDR`      | адрес Redis для asynq               |
| `S3_ENDPOINT`     | endpoint MinIO/S3                   |
| `S3_ACCESS_KEY` / `S3_SECRET_KEY` | креды хранилища     |
| `S3_BUCKET`       | бакет (по умолчанию `octoconv`)     |
| `S3_USE_SSL`      | `true`/`false`                      |
| `API_ADDR`        | адрес HTTP API                      |
| `MAX_UPLOAD_BYTES`| лимит размера загрузки (100 MiB)    |
| `API_KEY_SALT`    | server-side pepper для хеширования API-ключей (обязателен для `cmd/api` и `cmd/manage-clients`) |
| `RATE_LIMIT_IP_RPM` | грубый pre-auth лимит по IP, запросов/мин (по умолчанию 60) |
| `RATE_LIMIT_CLIENT_RPM` | per-client лимит, запросов/мин (по умолчанию 120) |
| `WORKER_CONCURRENCY` | число воркеров в процессе        |
| `ENGINE_TIMEOUT`  | таймаут на один запуск движка       |


## Frontend

Минимальный веб-интерфейс находится в `frontend/`: React + Vite + TypeScript. Он позволяет
ввести API-ключ, выбрать файл и целевой формат, поставить задачу через `POST /v1/jobs`, затем
опросить `GET /v1/jobs/<job_id>` до готовности и показать ссылку на скачивание результата.

Используются актуальные версии основных библиотек на момент добавления фронтенда:

| Пакет | Версия |
|-------|--------|
| React / React DOM | 19.2.7 |
| Vite | 8.1.3 |
| TypeScript | 6.0.3 |
| @vitejs/plugin-react | 6.0.3 |

Локальный запуск:

```bash
cd frontend
npm install
npm run dev
```

По умолчанию Vite dev server слушает `http://localhost:5173` и проксирует `/v1` и `/healthz`
на `http://localhost:8090`, поэтому CORS для локальной разработки не требуется. Если API
запущен на другом адресе, задайте `VITE_API_PROXY_TARGET`:

```bash
VITE_API_PROXY_TARGET=http://localhost:8090 npm run dev
```

Для production-сборки:

```bash
cd frontend
npm run build
```

## Остановка

```bash
docker compose down       # остановить
docker compose down -v    # остановить и удалить данные (тома)
```
