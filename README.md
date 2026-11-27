
[![License](https://img.shields.io/github/license/Massad/gin-boilerplate)](https://github.com/Massad/gin-boilerplate/blob/master/LICENSE) [![GitHub release (latest by date)](https://img.shields.io/github/v/release/Massad/gin-boilerplate)](https://github.com/Massad/gin-boilerplate/releases) [![Go Version](https://img.shields.io/github/go-mod/go-version/Massad/gin-boilerplate)](https://github.com/Massad/gin-boilerplate/blob/master/go.mod) [![DB Version](https://img.shields.io/badge/DB-PostgreSQL--latest-blue)](https://github.com/Massad/gin-boilerplate/blob/master/go.mod) [![DB Version](https://img.shields.io/badge/DB-Redis--latest-blue)](https://github.com/Massad/gin-boilerplate/blob/master/go.mod)

# Golang Gin Boilerplate v3

The fastest way to deploy a RESTful API with [Gin Framework](https://github.com/gin-gonic/gin/) — structured with **PostgreSQL** (multi-tenant Row-Level Security), **JWT** authentication stored in **Redis**, and ready to build on.

## What's Included

- [pgx](https://github.com/jackc/pgx): PostgreSQL driver and connection pooling (pgxpool, per-tenant and platform pools)
- [sqlc](https://github.com/sqlc-dev/sqlc): Type-safe Go code generated from versioned SQL queries
- [goose](https://github.com/pressly/goose): Versioned database migrations
- [jwt-go](https://github.com/golang-jwt/jwt): JSON Web Tokens (JWT) middleware
- [go-redis](https://github.com/go-redis/redis): Redis client
- [maroto](https://github.com/johnfercher/maroto): Pure Go PDF generation
- Built-in **CORS**, **RequestID**, and **Auth** middleware (tenant and admin tokens)
- Built-in **custom form validators** with reusable error translation
- **Multi-tenant isolation** — shared schema + `tenant_id` enforced by fail-closed PostgreSQL RLS
- **Admin backoffice foundation** — separate superadmin login, tenant status control, audit trail
- **Swagger API documentation**
- SSL support
- Go Modules

## Architecture Overview

The codebase is a **modular layout by domain** (Phases 0-8 of a certified migration):

- `cmd/api/` — the only entrypoint: env, flags, secrets validation, the binding-validator pin, the two pgx pools + Redis, and the router assembly.
- `internal/app/` — the **composition root** (`NewRouter`): middleware chain, `/v1` routes, Swagger, SPA hosting. Shared by the real binary and the integration suite (`app.NewRouter` everywhere — no duplicated route table).
- `internal/modules/<domain>/` — one package per business domain (`article`, `auth`, `tenant`, `user`, `admin`, `invoice`), each with the same anatomy: `handler.go` (HTTP bind/respond), `service.go` (business rules + consumer ports), `repository.go` (sqlc queries), `dto.go` (form validation), `model.go`.
- `internal/platform/` — cross-cutting infrastructure only: sqlc-generated code + hand-written pools, Redis, secrets validation, pure-Gin middleware, form validation, HTTP responders, mocks. It **never** imports `internal/modules`.
- Modules talk to each other **exclusively through interfaces defined on the consumer side** (e.g. `auth` declares the `UserAuthenticator`/`AdminAuthenticator`/`UserReader` ports that `user`/`admin` implement; each consumer declares the `TokenService` it needs). `*pgdb.Queries` never crosses a module boundary — cross-module transactional work goes through the explicit Unit of Work (`pgdb.WithinTx` + the narrow `pgdb.InTx` platform interface).
- Multi-tenant isolation relies on **two pools with two roles**: `app_user` (RLS-constrained, all business queries via `WithTenant`) and `app_admin` (BYPASSRLS, platform operations only) — see [Multi-Tenancy](#multi-tenancy).
- A CI guard forbids re-importing the removed legacy root packages (`controllers/`, `models/`, `forms/`, `middleware/`).

## Getting Started

### Prerequisites

- Go 1.26.11+
- Docker with Compose ≥ v2.17 (recommended) — the dev dependency stack starts
  with `make services_up`. If `docker ps` refuses permission:
  `sudo usermod -aG docker <user>` then log in again.
- A native PostgreSQL 13+ (uuid primary keys use `gen_random_uuid()`) and
  Redis also work, as long as `DB_PORT` and `REDIS_HOST` are re-pointed in
  `.env` (e.g. `5432` and `127.0.0.1:6379`).

### Setup

Clone the repository:

```bash
git clone https://github.com/Massad/gin-boilerplate.git
cd gin-boilerplate
```

Install dependencies:

```bash
go mod download
```

Set up your environment:

```bash
cp .env_rename_me .env
# Edit .env with your database credentials and signing keys
```

Generate each signing key separately with `openssl rand -hex 32`, then paste
results into `ACCESS_SECRET`, `REFRESH_SECRET`, and `ADMIN_ACCESS_SECRET`
(the superadmin access tokens) in your local `.env`. Keep that file private.
The application refuses to start with blank keys, keys shorter than 32 bytes,
or identical keys. Redis authentication uses
`REDIS_PASSWORD` when configured on your Redis server. The `REDIS_DB`
variable selects the Redis index: default 1 hosts the dev sessions, the Go
suites pin index 2 through each package's `TestMain`, and the e2e cycle runs
on index 3 — never set it in `.env`: the Makefile exports `.env` to every
recipe, so `make run` would follow it.

Start the dev dependency stack:

```bash
make services_up
```

### The Docker Dev Stack

**Docker is fully managed through the Makefile** — `docker-compose.yml` only
defines the stack and is never invoked by hand for the daily cycle. Every
operation runs a `make` target:

| Make target | Wraps |
|-------------|-------|
| `make services_up` | `make check_ports`, then `docker compose up -d --wait` (both containers; `--wait` blocks until their healthchecks pass) |
| `make postgres_up` / `make redis_up` | `docker compose up -d --wait postgres` / `redis` (one container) |
| `make services_down` | `docker compose down` (named volumes preserved) |
| `make check_ports` | Fails when the published ports in `docker-compose.yml` disagree with `DB_PORT`/`REDIS_HOST` from `.env`, or when `.env` is missing |

Raw `docker compose` commands are needed only to inspect (`ps`, `logs`),
update images (`pull`), or reset destructively (`down -v`).

The stack installs exactly **two dependency containers** — the application
itself is never containerized: it runs on the host (`make run`) and connects
to this stack. The compose project is pinned to the name `artisan-dev` (one
single project regardless of the clone):

| Container | Image | What it provides |
|-----------|-------|------------------|
| `artisan-dev-postgres-1` | `postgres:18-alpine` | PostgreSQL 18 pre-created with the dev database `golang_gin_db`, superuser `postgres`/`postgres` under trust auth (dev only), `shm_size` 256 MB, healthcheck `pg_isready` |
| `artisan-dev-redis-1` | `redis:8-alpine` | Redis 8 used by the auth module for the session tokens, healthcheck `redis-cli ping` |

- Both containers publish **loopback-only** on host ports deliberately chosen
  outside squatted local ranges: PostgreSQL on `127.0.0.1:15432` (container
  5432) and Redis on `127.0.0.1:16379` (container 6379). `.env` must stay in
  sync (`DB_PORT`, `REDIS_HOST`).
- Data persists across restarts in two named volumes (`pgdata`, `redisdata`):
  `docker compose down` keeps the databases and the dev sessions. A
  `docker compose down -v` is the **destructive reset** — it wipes the data
  and the Redis sessions (re-login required).
- What compose installs is dependencies only — **the SQL schema is not part
  of it**: `services_up` boots an empty cluster (the prerequisite covers
  `run`/`test`/`e2e` only, never the migrations), and the first
  `make migrate_up` creates the schema and the application roles.

Run the database migrations. The first run needs a PostgreSQL superuser
because the migrations themselves create the `app_user` and `app_admin`
application roles (the application must never connect as the superuser) —
override `DB_USER`/`DB_PASS` just for this command. The Makefile reads the
same `.env` as the application (`-include .env`), so no manual export of the
database variables is needed:

```bash
make migrate_up DB_USER=postgres DB_PASS='<superuser password>'
```

`make` must be available on Windows (for example `choco install make`);
under WSL, run these commands inside the distribution.

Migrations are versioned SQL files in `db/migrations/`, managed with
[goose](https://github.com/pressly/goose). All application queries live in
`db/queries/*.sql` and are compiled to type-safe Go code with
[sqlc](https://github.com/sqlc-dev/sqlc).

- `app_user`: non-superuser role constrained by Row-Level Security
- `app_admin`: bypasses RLS, used only for platform-level operations
  (login, registration, admin backoffice)
- In production, set the application role passwords with
  `ALTER ROLE ... PASSWORD`

To leave the Docker dev stack and go back to native services, re-point `.env`
to the native instance (`DB_PORT=5432`, `REDIS_HOST=127.0.0.1:6379`), drop the
`services_up` prerequisites from the Makefile (they are tracked in git), and
stop the stack with `docker compose down`.

Create the superadmin account (CLI bootstrap, no HTTP route involved):

```bash
go run -mod=readonly ./cmd/api -create-admin -email you@example.com -password 'a long random password'
```

### Running

```bash
make run
```

Or directly:

```bash
go run -mod=readonly ./cmd/api
```

### Building

```bash
go build -mod=readonly -o gin-boilerplate ./cmd/api
./gin-boilerplate
```

### Testing

Invoice PDF content tests use `pdftotext` to verify the rendered invoice number,
line items, and totals — install a real poppler first:

- macOS: `brew install poppler`
- Ubuntu: `sudo apt-get install poppler-utils`
- Windows: `winget install --id oschwartz10612.Poppler` (open a new terminal
  afterwards so the updated PATH is picked up)

Then run without database services:

```bash
go test -mod=readonly -count=1 -race -v ./internal/modules/invoice
```

The full integration suite also requires running PostgreSQL and Redis.
`make test` starts the Docker dev stack first (prerequisite `services_up`)
and sources `.env` automatically before the tools run, so no manual export
is needed. On Windows, make also masks Git-for-Windows' bundled
`pdftotext` from the recipe PATH so the suite resolves the real poppler.

```bash
make test
```

Use a dedicated test database and Redis instance. The connection parser tests
require PostgreSQL trust authentication because they exercise different password
values. CI provisions disposable services for these checks. The dev stack
isolates sessions by Redis index: the dev app runs on index 1 (default), the
Go suites pin index 2 through each package's `TestMain`, and the e2e cycle
runs on index 3 (`REDIS_DB=3`) — the test purge cycles never evict the dev
sessions. `make test` also recreates the disposable `artisan_test` database
(migrations included), and the suites pin it via each package's `TestMain`:
the dev PostgreSQL database is never written by the tests.

### SSL (Optional)

The Docker dev stack runs without TLS: keep `SSL=FALSE` and the API answers
on `http://localhost:9001`.

To enable SSL, set `SSL=TRUE` in `.env` and generate certificates:

```bash
mkdir cert/
sh generate-certificate.sh
```

To disable SSL, set `SSL=FALSE` in `.env`.

### Environment Variables

All live in `.env` (never committed); the Makefile sources it into every recipe.

| Variable | Purpose | Notes |
|----------|---------|-------|
| `ENV` | Set to `PRODUCTION` to run Gin in release mode and hide Swagger | non-prod mounts `/swagger/*any` |
| `PORT` | HTTP(S) listen port | API answers on `:9001` in dev |
| `SSL` | `TRUE`/`FALSE`, selects `RunTLS` vs `Run` | dev stack runs `FALSE` |
| `DB_HOST`/`DB_PORT` | PostgreSQL endpoint | dev Docker: `127.0.0.1:15432` |
| `DB_NAME` | Database name | dev: `golang_gin_db`; suites enforce `artisan_test` |
| `DB_USER`/`DB_PASS` | Application role `app_user` (RLS-constrained) | created by migration 000002; no superuser |
| `DB_ADMIN_USER`/`DB_ADMIN_PASS` | Platform role `app_admin` (BYPASSRLS) | login, register, refresh, backoffice |
| `REDIS_HOST` | Redis endpoint (`host:port`) | dev Docker: `127.0.0.1:16379` |
| `REDIS_PASSWORD` | Redis auth, when set | optional |
| `REDIS_DB` | Redis logical index | default `1` = dev sessions; **never put it in `.env`** (the Makefile would follow it): Go suites pin `2` via `TestMain`, e2e pins `3` |
| `ACCESS_SECRET` | Signs tenant access tokens | ≥ 32 bytes, all three distinct |
| `REFRESH_SECRET` | Signs tenant refresh tokens | ≥ 32 bytes |
| `ADMIN_ACCESS_SECRET` | Signs superadmin access tokens | ≥ 32 bytes |

The app refuses to start when a signing key is missing, shorter than 32 bytes,
or duplicated. In production, rotate role passwords with
`ALTER ROLE ... PASSWORD`.

## API Endpoints

### User
| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/v1/user/register` | No | Register a new user (creates the tenant and its owner) |
| POST | `/v1/user/login` | No | Login and receive JWT tokens with the tenant details |
| GET | `/v1/user/logout` | Bearer | Logout (invalidates token) |

### Article
| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/v1/article` | Bearer | Create an article |
| GET | `/v1/articles` | Bearer | Get all articles of the current tenant member |
| GET | `/v1/article/:id` | Bearer | Get one article |
| PUT | `/v1/article/:id` | Bearer | Update an article |
| DELETE | `/v1/article/:id` | Bearer | Delete an article |

### Auth
| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/v1/login` | No | Unified login: tenant user first, superadmin fallback (response discriminated by `kind`) |
| POST | `/v1/token/refresh` | No | Refresh access and refresh tokens |

### Admin
| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/v1/admin/login` | No | Superadmin login, returns an admin access token |
| GET | `/v1/admin/logout` | Admin | Logout (invalidates token) |
| GET | `/v1/admin/tenants` | Admin | List tenants |
| POST | `/v1/admin/tenants/:id/status` | Admin | Suspend or activate a tenant |

There is no public API: every data route requires a tenant (`Bearer`) or
admin token. Only the auth endpoints above (`/v1/user/register`,
`/v1/user/login`, `/v1/login`, `/v1/token/refresh`, `/v1/admin/login`) and
the SPA shell are reachable without identification.

## Swagger Docs

Generate and view API documentation:

```bash
make generate_docs
make run
```

Then open: `http://localhost:9001/swagger/index.html` (`https://…` if you
enabled SSL).

The Swagger UI is served only outside `ENV=PRODUCTION`; the generated
`docs/` spec stays available for tooling.

## Frontend (`app/`)

The `app/` directory is the SPA workspace (Vite + React 19 + TypeScript +
Tailwind CSS 4). Node 22.12+ and pnpm 11+ are required (versions pinned in
`package.json`; corepack supplies the exact pnpm):

```bash
cd app
pnpm install
pnpm dev            # Vite app — http://localhost:5173
pnpm storybook      # Storybook — http://localhost:6006
pnpm build          # tsc --noEmit + Vite build
pnpm lint           # ESLint (flat config)
pnpm build-storybook # Static Storybook build (storybook-static/)
```

Stories follow CSF and are colocated (`src/**/*.stories.tsx`). The
`src/design-system/` palette stories and the "Design System" docs pages are
generated at runtime from the CSS token contract in `src/index.css`
(`:root` oklch variables + the `.dark` class), so the Storybook theme toolbar
exercises the app's real dark-mode mechanism. See `app/README.md` for
details.

CI runs an independent `frontend` job (frozen-lockfile install, lint, build,
Storybook build smoke) next to the Go pipeline.

## End-to-End Testing (Playwright)

The E2E suite lives in `app/e2e/` and runs against a disposable database
(`artisan_e2e`) recreated at every cycle. The Go binary serves the built SPA
(`app/dist`) on a single origin (port 9100).

### Full Cycle

```bash
make e2e
```

This recreates the test database, runs migrations, seeds the superadmin and
owner tenant, builds the SPA, and executes the Playwright specs.

### Headed (Visual) Mode

By default Playwright runs headless. To watch the browser execute tests:

```bash
# From the app/ directory
pnpm exec playwright test --headed
```

Or use the Playwright UI for step-by-step debugging, traces, and watch mode:

```bash
pnpm exec playwright test --ui
```

> **WSL note**: On Windows 11, WSLg provides a Wayland compositor so headed
> Chromium windows appear on the Windows desktop automatically. On WSL 2
> without WSLg, you need an X server (e.g., VcXsrv) and `$DISPLAY` set.

### Prerequisites

- One-time browser install: `pnpm -C app exec playwright install chromium`
- PostgreSQL + Redis running
- `.env` present at the repo root
- `app/dist` built (done automatically by `make e2e`)

## CI Pipeline

`.github/workflows/ci.yml` runs two independent jobs:

**`test` (Go)** — services PostgreSQL 18 + Redis 8, migrations applied in
superuser, then a disposable `artisan_test` database (provisioned by
`cmd/e2edb` + migrations) so the suites run as the non-superuser
`app_user`/`app_admin` roles and the RLS is exercised for real:

1. `go mod verify`, build (`/tmp/gin-boilerplate ./cmd/api`), `go vet`
2. Generated-code check: `make generate_sqlc` then `git diff --exit-code internal/platform/db`
3. Guard rails (`! grep`-style, only quoted import paths are regressions):
   - no legacy drivers (`sqlx`, `lib/pq`)
   - no imports of the removed root packages (`controllers`/`models`/`forms`/`middleware`)
   - no hardcoded Redis index (`InitRedis(1)`)
   - no `internal/testutil` import outside `*_test.go` (helper leak)
4. Full suite `go test -mod=readonly -count=1 -p 1 -race -tags=all ./...`
5. Production smoke test: boots the real binary, probes `/` (SPA shell, gzip) and `/v1/articles` (401)
6. `govulncheck ./...`

**`frontend`** — Node 22 + pnpm (version resolved from `app/package.json`),
fresh `--frozen-lockfile` install: `pnpm lint` → `pnpm build` (tsc + Vite) →
`pnpm test` (vitest) → `pnpm build-storybook` smoke.

## Trusted Proxies & CORS

By default, `SetTrustedProxies(nil)` is configured in the composition root (`internal/app/router.go`) — Gin will not trust any proxy headers. If you deploy behind a reverse proxy (nginx, CloudFlare, etc.), set your trusted proxy IPs:

```go
r.SetTrustedProxies([]string{"192.168.1.0/24", "10.0.0.0/8"})
```

CORS is configured in `internal/platform/middleware/cors.go`. Update the `Access-Control-Allow-Origin` header for your domain in production.

## Authentication

This boilerplate uses **Bearer Token** authentication:

1. **Login** returns an `access_token` (15 min) and `refresh_token` (7 days).
    The unified `/v1/login` tries the tenant user first and falls back to the
    superadmin; the response is discriminated by `kind` (`tenant_user` with
    user + tenant + both tokens, `superadmin` with a single access token).
2. Include the access token in requests: `Authorization: Bearer <access_token>`
3. When the access token expires, use `/v1/token/refresh` with the refresh token to get new tokens
4. Both tokens are stored in Redis. Logout invalidates the presented access token;
   refresh tokens remain valid until rotation or expiry.

Refresh rotation checks the stored Redis owner and consumes the refresh token
atomically — consumption never deletes the record immediately (no migration
hazard): a consumed record keeps a short 5-minute grace window that allows
exactly one recovery re-issuance when the first delivery failed, and replays
of an already-delivered token are rejected (401). If you restrict Redis
commands with an ACL, allow `EVAL` and `EVALSHA` for this operation.

Admin sessions use a single access token signed with the separate
`ADMIN_ACCESS_SECRET` (no admin refresh endpoint). It lives ~8 hours and is
revoked immediately on logout.

## Multi-Tenancy

Every user belongs to a tenant created at registration (`company` becomes a
unique slug). Data isolation is enforced by PostgreSQL Row-Level Security on
a shared schema with a `tenant_id` column — fail-closed policies that return
zero rows when no tenant context is set — never by application-side filtering
alone. The tenant always comes from the signed JWT, never from a client
header or hostname. Platform operations (login, registration, admin
backoffice) run through a dedicated role that bypasses RLS, while all
business queries run through the constrained role inside a tenant-scoped
transaction. Integration tests run as non-superuser roles, so the policies
are exercised for real.

## Project Structure

```
cmd/api/                      Entrypoint: env, flags, secrets, binding validator pin, pools + Redis, router assembly
internal/app/                 Composition root: middleware chain, /v1 routes, Swagger, SPA host
internal/modules/article/     Article module (handler, service, repository, dto, model)
internal/modules/auth/        Sessions, JWT, unified login, refresh, TokenAuth/AdminAuth/RequirePermission
internal/modules/user/        Identity: register (tenant + owner in one tx), login
internal/modules/tenant/      Tenant slug validation + provisioning + backoffice reads
internal/modules/admin/       Platform backoffice: admin login, tenant status, audit
internal/modules/invoice/     PDF invoice generation library (no HTTP routes, no DB)
internal/platform/            db (sqlc-generated + pgx pools, RLS WithTenant, WithinTx), redis, config, httpx, validate, mocks
db/queries/                   SQL sources for sqlc — all application queries live here
db/migrations/                Versioned goose migrations
docs/                         Swagger documentation (auto-generated)
public/                       Static files and HTML templates
tests/                        Integration tests (build tag all, disposable artisan_test database)
app/                          Frontend workspace (Vite + React 19, Storybook)
```

## Adding a New Resource

1. Write the SQL queries in `db/queries/*.sql` and run `make generate_sqlc` (commit the regenerated `internal/platform/db/` code)
2. Create the module in `internal/modules/<domain>/` — `model.go`, `dto.go` (form structs with `binding:` tags + the `ValidationMessages` map), `service.go` (business rules + consumer ports), `repository.go` (sqlc queries via the tenant or admin store)
3. Create the handler in the module — use `validate.Translate(err, messages)` for validation errors
4. Register routes in `internal/app/router.go` (the composition root) and run `make generate_docs` to update Swagger

## Development Commands

| Command | What it does |
|---------|--------------|
| `make run` | Runs the app (`go run -mod=readonly ./cmd/api`) after starting the dev stack |
| `make test` | Recreates the disposable `artisan_test` DB + migrations, then runs the full suite (`-race -tags=all`, sequential) |
| `make e2e` | Full Playwright cycle: disposable `artisan_e2e` DB, seeds, `pnpm build`, specs against `./cmd/api` on port 9100 |
| `make services_up` / `services_down` / `check_ports` | Docker dev stack (PostgreSQL 18, Redis 8, loopback 15432/16379) |
| `make postgres_up` / `redis_up` | Start one dependency alone |
| `make migrate_up` / `migrate_down` | goose migrations (superuser required only when migrating) |
| `make generate_sqlc` | Regenerates `internal/platform/db/` from `db/queries/` + `db/migrations/` (commit the code; CI verifies drift) |
| `make generate_mocks` | Regenerates the committed mockery mocks (`internal/platform/mocks/`) |
| `make generate_docs` | Regenerates the Swagger `docs/` (swag) |
| `go run -mod=readonly ./cmd/api -create-admin -email … -password … [-name "…"]` | CLI superadmin bootstrap (idempotent upsert, no HTTP route) |
| `go run -mod=readonly ./cmd/e2edb` | Disposable-environment helper: drop/create a target DB + flush a Redis index (anti-DROP guardrails) |
| `go test -mod=readonly -count=1 -race -v ./internal/modules/invoice` | PDF tests without DB services (needs `pdftotext`) |
| `go build -mod=readonly ./cmd/api` / `go vet -mod=readonly ./...` | Build + static analysis (always with `-mod=readonly`) |

## Previous Versions

- **v2.0** — JWT authentication with Redis (current architecture)
- **v1.x** — Session/cookie authentication ([v1 branch](https://github.com/Massad/gin-boilerplate/tree/v1-session-cookies-auth))

## License

MIT
