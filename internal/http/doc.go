// Package http is the shared HTTP support layer: auth/session, cookie jar, the
// pluggable FlareSolverr solver interface, and secret redaction.
//
// The redaction helpers (RedactURL, RedactURLIdentity, RedactError,
// HostAndRedactedQuery) are the single chokepoint every log/error/trace site
// routes URLs and error messages through, so passkeys, API keys, download
// tokens, and Cookie/Set-Cookie/Authorization values never reach a log line.
// They live here (rather than in a single engine stage) because the login,
// search, and download stages all reuse them.
//
// See AGENTS.md and docs/architecture.md.
package http
