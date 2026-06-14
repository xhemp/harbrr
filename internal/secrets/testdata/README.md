# Daemon-foundation divergences (Phase 4)

Where harbrr's daemon foundation — the secrets store, persistence, auth, and
server — deliberately or knowingly differs from its reference sibling
**autobrr/qui** or from the `docs/ideas.md` §9 security model. Each entry carries
exactly one disposition (see `docs/divergences.md` for the vocabulary):
`[Deliberate]` (intentional design choice), `[Accepted]` (a kept difference, no
work planned), or `[Tracked: Phase N]` (a real gap with a `docs/plan.md` item).

The behaviours below are pinned by tests in `internal/secrets`, `internal/auth`,
`internal/database`, `internal/web/api`, and `internal/server`.

## Improvements over qui (all `[Deliberate]`)

- **AEAD is AAD-bound.** Tracker-credential ciphertext is AES-256-GCM with AAD =
  `"<instanceID>\x00<setting>"`, so a blob cannot be replayed across rows or
  fields. qui passes no AAD. (`internal/secrets/aead.go`,
  `TestKeyringAADBoundToRow`.)
- **A `key_id` is stored per record.** `hex(SHA-256(key))[:16]` is recorded with
  every encrypted value and in the startup canary, enabling key rotation later and
  the fail-loud changed-key check now. qui has no key id. (`keyring.go`,
  `canary.go`, `TestVerifyCanaryFailsOnChangedKey`.)
- **The encryption key is separate from the session secret.** harbrr's encryption
  key comes from `secrets.encryption_key`/`key_file`/an auto-generated keyfile,
  and the DB-backed SCS session store uses random server-side tokens with no
  signing secret — so §9's "keep the encryption key separate from the session
  secret" holds by construction. qui derives its AES key from the session secret.

## Deliberate divergences from §9 / qui

- **Tracker `username` is stored plaintext.** §9 lists "login user/pass" as
  encrypted tracker credentials; harbrr encrypts the password (and cookies, api
  keys, passkeys, 2fa tokens, pins) but stores the **username** plaintext as a
  low-sensitivity identifier, so the edit UX can show it. The secret-field
  classifier (`loader.SettingsField.IsSecret`) reflects this; the corpus audit
  (`internal/indexer/cardigann/loader/testdata/secret_audit.txt`) pins the full
  classification. `[Deliberate]`
- **No CSRF token (qui model).** §9 names "CSRF on cookie-authenticated mutating
  endpoints." harbrr satisfies this the way qui/autobrr do — `SameSite=Lax`
  `HttpOnly` session cookies, `RenewToken` on login, and route separation (the
  cookie-authed surface is the JSON management API; programmatic clients use the
  `X-API-Key` header; Torznab uses `?apikey`) — rather than a synchronizer token.
  Verified by `TestSetupLoginLogoutFlow` (cookie flags) +
  `TestAuthDisabledIgnoresUntrustedXFF`. `[Deliberate]`
- **Single connection, not qui's dual pool.** The SQLite store uses one
  `*sql.DB` with `SetMaxOpenConns(1)`, fully serializing access (no `SQLITE_BUSY`
  / nested-tx) — simplest correct choice for a single-user daemon. qui's
  writer/reader pool split is a performance optimization not needed yet.
  `[Accepted]`
- **Custom SCS store, not `scs/sqlite3store`.** harbrr's session store is
  driver-agnostic (`internal/database/sessionstore.go`); the official
  `alexedwards/scs/sqlite3store` imports the cgo `mattn` driver, which would break
  the pure-Go `CGO_ENABLED=0` cross-build. `[Deliberate]`

## Tracked gaps (carry a `docs/plan.md` item)

- **OIDC is stubbed.** `/api/auth/oidc/*` returns 501; only a config seam exists.
  `[Tracked: Phase 8]` (with the web UI / app auth).
- **Interactive Swagger UI is deferred.** The embedded spec is served at
  `/api/openapi.yaml`; the rendered Swagger UI lands with the web UI.
  `[Tracked: Phase 8]`
- **`api_keys.last_used_at` is never written.** Validation is a pure read (no
  write on the request path); a debounced "touch" lands later.
  `[Tracked: Phase 6]` (health/stats).
