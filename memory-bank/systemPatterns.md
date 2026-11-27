# systemPatterns.md — go-artisan-services

## Architecture (migration modulaire : Phases 0-8, mise en œuvre complète)

```
cmd/api/main.go (dotenv, flags, secrets, pin binding.Validator, pools, Redis)  →  internal/app (composition root : middleware + /v1 + SPA)
internal/modules/<domaine>/ : handler.go (HTTP) → service.go (métier + ports consommateurs) → repository.go (sqlc via pgdb Store injecté)
internal/platform/ : db (pgdb), redisdb, config, middleware, validate, httpx, mocks — n'importe JAMAIS internal/modules/*
db/queries/*.sql --sqlc--> internal/platform/db/*.sql.go (généré, commité, ne pas éditer)
db/migrations/*.sql (goose) ← schema source de sqlc
```

- Modules interconnectés EXCLUSIVEMENT par des interfaces définies côté consommateur ; `*pgdb.Queries` ne franchit jamais une frontière de module — échanges in-Tx via `pgdb.WithinTx` + `pgdb.InTx`. Le routeur ne mute pas l'état global : `binding.Validator` épinglé à l'entrypoint (tests : `TestMain` du paquet ou `*_test.go` isolé).

## Deux pools, deux rôles (défense en profondeur RLS)

- `TenantStore()` — pool `app_user` non-superuser : tout le métier, contraint à 100 % par RLS.
- `AdminStore()` — pool `app_admin BYPASSRLS` : opérations « plateforme » uniquement (login, register, refresh, backoffice admin) — contournement du problème œuf-poule (un SELECT user sans contexte tenant renverrait 0 ligne).

## Contexte tenant (pattern critique)

```go
pgdb.WithTenant(ctx, tenantID, func(q *pgdb.Queries) error { … })
```
= transaction + `SELECT set_config('app.tenant_id', $1, true)` (auto-reset au commit/rollback) → pool-safe. Jamais de `SET` session-wide. Les requêtes sqlc tenant-scoped portent aussi `@tenant_id` (double défense : RLS + paramètre).

## Auth (internal/modules/auth)

- Tenant : access `authorized/access_uuid/user_id/tenant_id/role` + refresh `refresh_uuid/user_id/tenant_id` ; valeur Redis `userID:tenantID:role` ; index sessions `user_sessions:{userID}` (SADD/SREM/revocation, TTL aligné).
- Admin : un seul token ~8h (claims `admin_id`, `role:"superadmin"`), signé `ADMIN_ACCESS_SECRET`, Redis préfixé `admin:`, **pas de refresh admin** ; rejet croisé des tokens par direction.
- Refresh : soft-consume (record STRING `consumed:<1|0|recovered>:…`, grâce 5 min, échec fermé), puis RELIT le user via le port `UserReader` (rôle frais, existence, `tenant.status`) — jamais le rôle des claims ; ré-émission `recovered` unique sous grâce (décision 7).
- Unified login : `Orchestrator.UnifiedLogin` (tenant d'abord → dummy bcrypt à coût constant → fallback superadmin ; 406 génériques, 403 propagé).
- Middleware : `TokenAuth(svc)` set `userID/tenantID/role` (parsing via `httpx.ParseUUIDParam` côté handlers), `AdminAuth(svc)`, `RequirePermission` (map `rolePermissions`, posée non câblée).

## Conventions transverses

- Erreurs : `pgx.ErrNoRows` → 404 ; `pgconn.PgError` par Code (`23505` → 406/409, `23503` → 409, `42501` → 500) ; jamais de texte SQL brut au client ; validation via `validate.Translate(err, <module>.XxxMessages)`.
- Réponses `gin.H{"message": ...}` ; JSON tenant embarqué au login (`user`, `tenant`, `token`) ; Register ne renvoie pas de tokens.
- Docs : godoc sur les exportés, pas de commentaires superflus ; Swagger régénéré via `make generate_docs` (swag v1.16.6) après toute annotation.

## Tests (pattern à respecter pour toute feature)

- Unitaires sans tag (config secrets, tenant slugify, fakes des ports par module, PDF invoice via poppler) + intégration sous `//go:build all` (httptest + `app.NewRouter` via `testRouter()`, suite séquentielle à variables de package : `TestIntDB` init, `TestCleanUp` cleanup ciblé) exécutée sur la base jetable `artisan_test` (`DB_NAME` épinglé par le `TestMain` du paquet, recréée+migrée par chaque `make test`).
- La CI tourne en rôles non-superuser → la RLS est réellement prouvée ; une requête sans contexte renvoie 0 ligne et un insert cross-tenant lève 42501 : comportement **attendu**, ne jamais « corriger » au superuser.
