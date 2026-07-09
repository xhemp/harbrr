// The server injects window.__HARBRR_BASE_URL__ (and __HARBRR_VERSION__) into
// index.html at serve time; under `pnpm dev` neither is set and the base is "".

// getBaseUrl returns the base path with no trailing slash ("" at root), so the
// router basepath and API prefixes compose by simple concatenation.
export function getBaseUrl(): string {
  const raw = window.__HARBRR_BASE_URL__ ?? ""
  if (raw === "/") return ""
  return raw.endsWith("/") ? raw.slice(0, -1) : raw
}

// getApiBaseUrl returns the prefix every management-API call goes through.
export function getApiBaseUrl(): string {
  return `${getBaseUrl()}/api`
}

// defaultHarbrrUrl is the best-effort prefill for a form's "harbrr URL" field:
// how this browser reaches harbrr is usually how an app can too (the operator
// adjusts for container-network names as needed). Shared so the connection and
// announce forms cannot drift.
export function defaultHarbrrUrl(): string {
  return `${window.location.origin}${getBaseUrl()}`
}

// urlPort returns a URL's effective port — its explicit port, or the
// scheme's default (443 for https, 80 for anything else) when none is
// given — or null when the string doesn't parse as a URL.
export function urlPort(url: string): number | null {
  try {
    const parsed = new URL(url)
    if (parsed.port !== "") return Number(parsed.port)
    return parsed.protocol === "https:" ? 443 : 80
  } catch {
    return null
  }
}

// urlHasExplicitPort reports whether url's authority names a port outright,
// as opposed to relying on the scheme's default. A harbrrUrl fronted by a
// reverse proxy (docs/security.md's supported deployment) is typically
// written without one — TLS terminates on the proxy's standard 443/80 and
// that number has no relationship to harbrr's own internal listen port.
// Callers that compare a stored port against harbrr's live listen port
// (ConnectionCard's stale-port check) must skip URLs without an explicit
// port: the comparison is only meaningful when the URL targets harbrr's
// listener directly. Returns false when the string doesn't parse as a URL.
export function urlHasExplicitPort(url: string): boolean {
  try {
    return new URL(url).port !== ""
  } catch {
    return false
  }
}

// withPort returns url with only its port replaced, leaving scheme, host,
// and path untouched (setting the scheme's default port drops it entirely,
// per standard URL semantics). Returns url unchanged when it doesn't parse.
export function withPort(url: string, port: number): string {
  try {
    const parsed = new URL(url)
    parsed.port = String(port)
    return parsed.toString()
  } catch {
    return url
  }
}
