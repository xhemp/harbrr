package cardigann

import (
	"io"
	stdhttp "net/http"
	"net/http/cookiejar"
	"strings"
	"sync"
	"testing"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// rotatingSessionTransport scripts the PHP session_regenerate_id shape by path:
//   - GET  /login.php    : the form landing page, Set-Cookie session=stale-prelogin.
//   - POST /takelogin.php: 302 to /index.php ROTATING the cookie to
//     session=fresh-postlogin — the hop only the client's jar sees, because the
//     client follows the redirect and hands the engine the final response.
//   - GET  /index.php    : the landed logged-in page.
//   - GET  /browse       : the search; its raw Cookie header is recorded.
type rotatingSessionTransport struct {
	mu           sync.Mutex
	searchCookie string
	searches     int
}

const (
	rotLanding = `<html><body><form id="loginform" action="/takelogin.php" method="post">` +
		`<input name="username"><input name="password" type="password"></form></body></html>`
	rotLanded  = `<html><body>welcome back</body></html>`
	rotResults = `<html><body><table><tr class="row"><td><a class="title">Rotated Result</a></td>` +
		`<td class="size">1 GB</td><td class="seeders">10</td>` +
		`<td><a class="dl" href="/dl/1.torrent">dl</a></td></tr></table></body></html>`
)

func (rt *rotatingSessionTransport) RoundTrip(req *stdhttp.Request) (*stdhttp.Response, error) {
	header := stdhttp.Header{}
	status := stdhttp.StatusOK
	body := ""
	switch req.URL.Path {
	case "/login.php":
		header.Add("Set-Cookie", "session=stale-prelogin; Path=/")
		body = rotLanding
	case "/takelogin.php":
		status = stdhttp.StatusFound
		header.Add("Set-Cookie", "session=fresh-postlogin; Path=/")
		header.Set("Location", "/index.php")
	case "/index.php":
		body = rotLanded
	case "/browse":
		rt.mu.Lock()
		rt.searchCookie = req.Header.Get("Cookie")
		rt.searches++
		rt.mu.Unlock()
		body = rotResults
	}
	return &stdhttp.Response{
		StatusCode: status,
		Header:     header,
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}, nil
}

// jarOwnerDoer wraps the client the way the registry's paced client does: a
// plain Doer that exposes the client's jar via CookieJar() (search.JarOwner).
type jarOwnerDoer struct{ client *stdhttp.Client }

func (d jarOwnerDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	return d.client.Do(req) //nolint:gosec // G704: offline replay transport; no request leaves the process.
}
func (d jarOwnerDoer) CookieJar() stdhttp.CookieJar { return d.client.Jar }

// TestSearch_RotatedSessionCookieSingleJar pins the single-cookie-jar invariant
// end to end: a form login whose POST 302 rotates the session cookie must leave
// the search carrying EXACTLY `session=fresh-postlogin` — one pair, the rotated
// value. With a second jar in play the wire showed
// `session=stale-prelogin; session=fresh-postlogin`; PHP reads the FIRST pair,
// so the tracker saw the logged-out session, the bounded relogin repeated the
// identical exchange, and the instance was permanently logged out.
func TestSearch_RotatedSessionCookieSingleJar(t *testing.T) {
	t.Parallel()

	// Both production Doer shapes must behave identically: the parity harness
	// hands the engine the *http.Client itself; the registry hands it a wrapper
	// exposing the client's jar via search.JarOwner.
	shapes := map[string]func(*stdhttp.Client) search.Doer{
		"bare http.Client": func(c *stdhttp.Client) search.Doer { return c },
		"JarOwner wrapper": func(c *stdhttp.Client) search.Doer { return jarOwnerDoer{client: c} },
	}
	for name, wrap := range shapes {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rt := &rotatingSessionTransport{}
			jar, err := cookiejar.New(nil)
			if err != nil {
				t.Fatalf("cookiejar.New: %v", err)
			}
			client := &stdhttp.Client{Transport: rt, Jar: jar, CheckRedirect: apphttp.RedirectPolicy}

			def := loadFixtureDef(t, "rotating_session.yml")
			eng, err := NewEngine(def, WithClock(fixedClock()), WithDoer(wrap(client)))
			if err != nil {
				t.Fatalf("NewEngine: %v", err)
			}

			releases, err := eng.Search(t.Context(), Query{Keywords: "rotate"})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if len(releases) != 1 {
				t.Fatalf("releases = %d, want 1", len(releases))
			}

			rt.mu.Lock()
			cookie, searches := rt.searchCookie, rt.searches
			rt.mu.Unlock()
			if searches != 1 {
				t.Fatalf("searches = %d, want 1 (no relogin loop)", searches)
			}
			if cookie != "session=fresh-postlogin" {
				t.Errorf("search Cookie = %d pair(s), want exactly the rotated session pair (stale/duplicate cookie on the wire)",
					strings.Count(cookie, "session="))
			}
		})
	}
}
