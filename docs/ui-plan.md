# harbrr Web UI — plan

Status: design / pre-build. The engine parity gate (Phase 9) and app-sync (Phase 10) are closed, so
the management UI is now in-scope. This is the **product surface** layer: a single-page app embedded
in the Go binary, talking only to the existing authenticated REST API under `internal/web/api`.

Guiding decision: **same stack as autobrr/qui, latest versions, trimmed to what harbrr actually
needs.** Mirror qui's interface/look where it maps onto harbrr's data; diverge only where harbrr's
surface (indexer management, search, app-sync) differs from qui's qBittorrent surface. Consistency
with the family is the feature — it lets claude design anchor to qui and keeps patterns shared.

## 1. Stack

Versions verified latest as of 2026-06-22; pin to whatever `latest` is at scaffold time.

| Layer | Choice | Version (floor) |
|---|---|---|
| Framework | React + TypeScript | react 19.2, typescript 6.0 |
| Build | Vite + `@vitejs/plugin-react` | vite 8.0 |
| Package manager | pnpm | latest |
| Router | TanStack Router (code-gen routes) | latest |
| Server state | TanStack Query | 5.10x |
| Tables | TanStack Table + Virtual | latest |
| Forms | TanStack Form + zod | latest |
| Styling | Tailwind v4 via `@tailwindcss/vite` (no PostCSS config) | 4.3 |
| Components | shadcn/ui = Radix primitives + CVA + clsx + tailwind-merge | latest |
| Icons / toasts / theme | lucide-react / sonner / next-themes | latest |
| Lint + format | ESLint 10 + typescript-eslint + `@stylistic/eslint-plugin` (no Prettier) | latest |
| Test | Vitest + Testing Library + jsdom | latest |

### Dependencies we deliberately DROP from qui's set (add only when a screen needs them)

- `parse-torrent`, `@types/parse-torrent`, `react-dropzone` — qBittorrent torrent upload; harbrr has none.
- `@dnd-kit/*` — drag-reorder; defer until indexer/connection ordering needs it.
- `culori`, `@types/culori`, `flag-icons` — color math / country flags; not needed at start.
- `vaul`, `react-resizable-panels`, `cmdk`, `react-hotkeys-hook`, `motion` — nice-to-haves; defer.
- `vite-plugin-pwa`, `workbox-window` — PWA; defer (self-hosted single-user, low value early).
- `i18next` / `react-i18next` — defer; single-user self-hosted. Revisit only if the family standardizes.

Radix primitives are added per shadcn component as we use them (dialog, dropdown-menu, select, label,
checkbox, switch, tabs, tooltip, popover, separator, scroll-area, slot, alert-dialog), not all upfront.

## 2. Screens ↔ existing API

Every screen maps onto endpoints that already exist in `internal/web/api/router.go`. No new backend
work is assumed; gaps get raised as separate API tasks.

| Screen | Purpose | API endpoints | Key components |
|---|---|---|---|
| **Setup** | First-run admin password | `GET/POST /api/auth/setup` | Form + zod |
| **Login** | Session auth; OIDC entry (stubbed) | `POST /api/auth/login`, `GET /api/auth/oidc/*` | Form |
| **App shell** | Sidebar nav, theme toggle, identity, logout | `GET /api/auth/me`, `POST /api/auth/logout` | next-themes, sonner |
| **Indexers list** | Enabled indexers, status, enable/disable, test | `GET /api/indexers`, `.../enable`, `.../disable`, `.../test`, `.../status` | Table |
| **Add indexer** | Pick from catalog, fill dynamic settings | `GET /api/definitions`, `GET /api/definitions/{id}`, `POST /api/indexers` | Catalog grid + dynamic Form |
| **Indexer detail** | Edit settings, capabilities, delete | `GET/PATCH/DELETE /api/indexers/{slug}`, `.../capabilities` | Form, Tabs |
| **Search** | Manual/test search of an indexer | `GET /api/indexers/{slug}/search` | Table + Virtual |
| **App connections** | Sonarr/Radarr/qui sync targets, CRUD, test, sync, indexer selection | `GET/POST /api/app-connections`, `{id}` `GET/PATCH/DELETE`, `.../enable`, `.../disable`, `.../test`, `.../sync`, `.../status`, `PUT .../indexers` | Form, Table, multi-select |
| **Settings → API keys** | Mint/list/revoke keys | `GET/POST /api/apikeys`, `DELETE /api/apikeys/{id}` | Table, Dialog |
| **Settings → Security** | Change password; link to Swagger `/api/docs` | `POST /api/auth/change-password` | Form |

The **dynamic indexer settings form** (driven by `GET /api/definitions/{id}`) is the highest-risk
screen — it renders fields from the definition's settings schema. Design and build it last/most
carefully; let the API's schema (not invented fields) drive it.

## 3. Backend integration (the qui pattern)

1. Frontend lives in `web/`, builds to `web/dist`.
2. Makefile copies `web/dist` → `internal/web/dist`, which is `//go:embed`-ed and served as an SPA
   with a fallback to `index.html` for client-side routes (new `internal/web/embed.go` + a static
   handler mounted in the chi router, below the `/api` and Torznab routes).
3. Build flow: `make build` = `frontend` (`pnpm install && pnpm build` + copy) then `backend`
   (`go build`). Add `frontend`, `backend` targets; keep existing Go-only targets working.

**`//go:embed` gotcha:** the embedded `dist/` must exist at compile time or `go build ./...` fails.
Commit a placeholder `internal/web/dist/index.html` and `.gitignore` the rest of `dist/`, so the
existing Go CI (`go build ./...`) and `make test` stay green without a Node toolchain. The Docker
image build does the real frontend build (multi-stage).

`Dockerfile`: add a `node:22` + pnpm stage that produces `dist`, copied into the Go build stage.

## 4. CI additions

New workflow `.github/workflows/web.yml`, path-filtered to `web/**` (off the critical path for
Go-only changes), pinned action SHAs to match the existing hardened style:

- `pnpm/action-setup` + `actions/setup-node` (cache pnpm store)
- `pnpm install --frozen-lockfile`
- `pnpm lint` (eslint) → `pnpm typecheck` (`tsc --noEmit`) → `pnpm test` (vitest) → `pnpm build`

Other changes:
- **Dependabot/Renovate:** add an `npm` ecosystem entry (large, fast-moving JS surface).
- **Docker job (`ci.yml`):** build the real frontend via the multi-stage Dockerfile so the shipped
  image isn't the placeholder stub. Existing `go build` / cross-build jobs unchanged (placeholder dist).
- **Secrets:** gitleaks already runs repo-wide; ensure no keys land in client bundles — everything
  flows through the authenticated API, so this is natural.
- **Deferred:** Playwright e2e until there are real screens worth smoke-testing.

## 5. Milestones

1. **Scaffold** — `web/` skeleton (Vite + React + TS + Tailwind v4 + shadcn init), embed wiring,
   placeholder dist, Makefile/Dockerfile, `web.yml`. App shell + theme + login/setup against live API.
2. **Indexers** — list + status + enable/disable/test; add-indexer catalog + dynamic settings form;
   indexer detail/edit. (Highest-value, highest-risk; the dynamic form is the crux.)
3. **Search** — manual search screen + results table (validates the engine end-to-end through the UI).
4. **App connections** — Sonarr/Radarr/qui CRUD, test, sync, indexer selection.
5. **Settings** — API keys, change password, Swagger link. Polish, empty/error/loading states.

## 6. Design workflow (claude design)

> **Project:** https://claude.ai/design/p/353cce7f-8720-4ded-b678-f1ab55e0dc51
> First screen (app shell + indexers list): append `?file=Indexers.dc.html`. This work is stored
> server-side in claude.ai and persists independently of the repo.


claude design is used to settle each screen's layout/look **before** writing React, anchored to qui's
system and seeded by §2's data shapes so generated options match real fields. Iterate screen-by-screen
(the §2 table is the work queue), compare options with `render_preview`, finalize, then implement
against the live API. Use claude design (match qui) rather than the distinctive `frontend-design`
skill — here consistency with the family beats novelty.
