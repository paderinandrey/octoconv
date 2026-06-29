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
  api/        — HTTP-сервер (приём файла, статусы)        [этап 6]
  worker/     — обработчик очереди (asynq)                [этап 7]
  migrate/    — применение миграций БД
internal/
  api/        — роуты и хендлеры                          [этап 6]
  convert/    — интерфейс Converter, реестр пар, hardened exec, libvips [этап 3]
  jobs/       — репозиторий задач в Postgres              [этап 5]
  queue/      — тип asynq-задачи и payload                [этап 4]
  storage/    — обёртка над MinIO/S3                      [этап 2]
  db/         — pgx pool + раннер миграций (+ migrations/*.sql)
```

> Пометки `[этап N]` — модули появляются по мере реализации (см. план проекта).
> Реализовано на данный момент: инфраструктура, схема БД и раннер миграций.

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
| postgres | postgres:18      | **5433**    | `octo / octo-pass / octo_db`     |
| redis    | redis:8          | 6379        | брокер asynq                     |
| minio    | minio/minio      | **9100** (API), **9101** (консоль) | `minioadmin / minioadmin`, бакет `octoconv` создаётся автоматически |

> Нестандартные хост-порты (5433, 9100/9101) выбраны, чтобы не конфликтовать с другими
> локальными стеками. Меняются в `docker-compose.yml` и `.env`.
> MinIO-консоль: http://localhost:9101

### 2. Применить миграции

```bash
set -a && . ./.env && set +a   # загрузить переменные окружения
go run ./cmd/migrate
```

Создаёт таблицы `clients`, `presets`, `jobs`, `job_inputs`, `job_outputs`, `job_events`,
`webhook_deliveries` (полный DDL — `internal/db/migrations/0001_init.sql`). Раннер идемпотентен:
применённые версии фиксируются в `schema_migrations`.

### 3. Запустить сервисы *(появятся на следующих этапах)*

```bash
go run ./cmd/api       # HTTP API на :8080         [этап 6]
go run ./cmd/worker    # воркер очереди image       [этап 7]
```

## API *(черновик, реализуется на этапе 6)*

Поставить задачу:

```bash
curl -F file=@report.png -F target=webp http://localhost:8080/v1/jobs
# {"job_id":"...","status":"queued"}
```

Статус / результат:

```bash
curl http://localhost:8080/v1/jobs/<job_id>
# {"job_id":"...","status":"done","download_url":"..."}
```

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
| `WORKER_CONCURRENCY` | число воркеров в процессе        |
| `ENGINE_TIMEOUT`  | таймаут на один запуск движка       |

## Остановка

```bash
docker compose down       # остановить
docker compose down -v    # остановить и удалить данные (тома)
```
