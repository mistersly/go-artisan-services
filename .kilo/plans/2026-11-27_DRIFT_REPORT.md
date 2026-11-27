# Drift report du 2026-11-27 — clôture de la migration modulaire

> Flux `egm-drift-report` (plugin EGM, repo kilo) exécuté contre le code migré à l'issue de la Phase 8 (merge PR #3 `cfdf05c` + marqueur `ffcf57a`), master = source de vérification.
> Document cibles : le design + le plan-phases de la migration. Méthode : lecture séquentielle des deux documents, localisation des sources correspondantes, lecture de l'état actuel (greps ciblés + lectures de fichiers), comparaison intent↔code. Les références de lignes internes des documents seules (historiques « état initial », convention plan-phases l.90) ne sont pas traitées comme du drift.

Repos analysé : `cmd/api`, `internal/app`, `internal/platform/{config,redisdb,db,middleware,validate,httpx,mocks}`, `internal/modules/{article,auth,tenant,user,admin,invoice}` et leurs tests, `db/queries`, `Makefile`, `.github/workflows/ci.yml`, `app/playwright.config.ts`, `go.mod`.

## Vérifications sans drift (résumé de couverture)

Aucune divergence sur les affirmations cibles suivantes (preuves par grep/lecture au 2026-11-27) :

- Layout §4 du design : les 17 entrées cibles existent toutes à l'emplacement exact (`cmd/api`, `internal/app/router.go`, `internal/platform/{config,redisdb,db,middleware,validate,httpx,mocks}`, `internal/testutil`, 6 modules `article/auth/tenant/user/admin/invoice`, `db/{migrations,queries}`) ; `invoice/` root déménagé en Phase 8 (`git ls-files invoice` = 0) ; `SetupRouter` disparu (0 résidu hors plans).
- Décision 1-2 : code sqlc en `internal/platform/db` (+ mocks), `cmd/api/main.go` à jour (Makefile `run`/`e2e`, `swag init -g cmd/api/main.go` Makefile:88, build CI, webServer Playwright `./cmd/api`).
- Décision 3 : `TokenAuth`/`AdminAuth`/`RequirePermission` dans `internal/modules/auth/middleware.go` ; `internal/platform/*` : 0 import `internal/modules` (grep).
- Décision 4 : ports `UserAuthenticator`/`AdminAuthenticator`/`UserReader` définis dans `auth` (service.go:66-79), implémentés par `user`/`admin` (aliases) ; `TokenService` déclaré par les consommateurs (`user/service.go:28`, `admin/service.go:25`) ; aucune interface définie côté fournisseur.
- Décision 5 : `app.NewRouter` unique composition root (router.go), consommé par `cmd/api` ET `testRouter()` de la suite `tests/`.
- Décision 6 : `*pgdb.Queries` jamais en paramètre/retour de port cross-module (grep) ; Unit of Work `pgdb.WithinTx` (pool.go:78) + interface étroite `pgdb.InTx` (pool.go:60) inchangés.
- Décision 7 (soft-consume) : record STRING format actif/consommé identique au design (`repository.go:24-25`, Lua `consumed_state`), grâce 5 min, tests dédiés complets (rejeu/grâce/recovery unique/fails-closed/ownership/sweep ×2 — `soft_consume_test.go`, `refresh_ownership_test.go`, `unified_test.go`) ; ownership `TestRefreshOnlyConsumesMatchingOwner` conservé.
- Invariants multi-tenant : RLS `WithTenant` (aucun `SET` session-wide — pool_test), pools app_user/app_admin inchangés, login unifié (dummy bcrypt, non-recouvrement, 403 propagé — matrice `tests/`), erreurs pgx mappées (`httpx.MapPgxError`), SQL uniquement en `db/queries/*.sql`, pin TestMain `REDIS_DB=2`/`DB_NAME=artisan_test`.
- Non-objectifs respectés : go.mod inchangés (`go-redis/redis/v7 v7.4.1`, `golang-jwt/jwt/v4 v4.5.2`, gin v1.12.0), RBAC posé et NON câblé (`RequirePermission` absent d'`internal/app`/`cmd` — grep vide), origin CORS codée en dur (`middleware/cors.go:10`), tokens in-JSON inchangés, casse mixte `"Message"` conservée (`article/handler.go` ×5), réponses `err.Error()` brut conservées (`user/handler.go:79,82`).
- Plan-phases : marqueurs Phases 0-8 tous `[Fait]` cohérents avec les gates ; `binding.Validator` éliminé du composition root (P1.7(a) soldée — `internal/app` sans import `binding`/`validate`) ; guard CI « Check removed root packages » en place (ci.yml:94-100) ; provisioning `artisan_test` en CI (l.78-79) ; doc `AGENTS.md`/`README.md`/memory-bank régénérés au format final.

## 1795597264251-architecture-modulaire-par-domaine-design.md

- **Décision 6 (nommage littéral des interfaces)** : le design cite comme exemples d'interfaces explicites `TenantRepository` et `TokenRepository` ; aucun type Go ne porte ces deux noms. Réalité conforme au PRINCIPE (interfaces définies côté consommateur, uniquement quand un fake est utile — alternative 3) : l'accès tenant est taillé par ports conso (`TenantProvisioner` côté `user`, `TenantReader` côté `admin`) et l'entrepositage des tokens est porté par `SessionRepository` (consommateur intra-module `auth`). Divergence de nommage uniquement (la liste du design se termine par « … ») : aligner la formulation du design lors de sa prochaine révision au profit des noms réels, ou laisser comme exemple générique ouvert.
- **Référence de ligne stale §2.113** : « backlog `memory-bank/progress.md:56` » — le fichier a été renuméroté à chaque session ; l'item backlog existe toujours (section « Backlog déferré » de `progress.md`, fin de fichier). Mettre à jour l'ancre par un nom de section à la prochaine retouche du design.
- (Non-drift, conventions documentées) : références « état actuel » de la cartographie §1 (p.ex. `main.go:148-182`, `controllers/*`) décrivent l'état initial pré-migration par construction — la note plan-phases l.90 (« les numéros décrivent l'état initial… devient obsolète dès la première étape ») encadre ce cas ; le §3.9(c) « dette `binding.Validator` » est désormais exécuté (P8.3) et le §3.9 la marquait comme « élimination requise en Phase 8 » — intent et réalisation coïncident.

## 1795597264251-architecture-modulaire-par-domaine-plan-phases.md

- **P1.8 (l.163) webServer Playwright** : le plan vise `command: "go run ./cmd/api"` ; le code actuel porte `command: "go run -mod=readonly ./cmd/api"` (`app/playwright.config.ts:51`). Écart littéral mineur, conforme à la règle dépôt « toujours `-mod=readonly` » (aucune action recommandée — mise à jour du plan inutile, l'écart est dans le sens des conventions).
- (Non-drift) : cibles e2edb (`Makefile:65,165`, ci.yml:78), guards CI (`legacy drivers`, Redis index, fuite helpers, nouveau « removed root packages »), provisions `artisan_test`, `swag init -g cmd/api/main.go` — tous vérifiés conformes.

**Verdict de clôture : migration fidèle au design** — 2 divergences de nommage/référence uniquement (aucune divergence d'architecture, de comportement ou de contrat) ; backlog différé hors périmètre inchangé (montées go-redis/jwt, RBAC câblé, CORS configurable, refresh httpOnly, normalisation des payloads d'erreur).
