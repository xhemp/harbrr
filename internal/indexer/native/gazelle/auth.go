package gazelle

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	alphaRatioCookieSetting = "cookie"
	alphaRatioUserAgent     = "harbrr"
	maxLoginBodyBytes       = 1 << 20
)

// authHeader builds the Authorization header value for the configured site: the
// per-site prefix ("" for RED, "token " for OPS) concatenated with the API key. The
// returned string is secret-bearing and MUST NEVER be logged.
func (d *driver) authHeader() string {
	return d.profile.authPrefix + d.Cfg["apikey"]
}

// classifyARCookie is AlphaRatio's cookie-session status dialect handed to Base.Do. On
// top of the shared 401/403 auth codes it treats a redirect as an auth failure: an
// expired session bounces to login.php, and requestContext disables redirect following
// so that 3xx surfaces here instead of being swallowed as login HTML. Base.Do therefore
// returns login.ErrLoginFailed and Search/Grab renew once and retry. 429/503 stay rate
// limits (a RateLimitedError, never a renewal).
var classifyARCookie = native.ClassifyAuth403.AlsoAuth(
	stdhttp.StatusMovedPermanently,
	stdhttp.StatusFound,
	stdhttp.StatusSeeOther,
	stdhttp.StatusTemporaryRedirect,
	stdhttp.StatusPermanentRedirect,
)

// newRequest builds an API-key-authenticated GET for RED/OPS: the key rides in the
// Authorization header (never the URL, never logged) and Accept advertises JSON.
// Transport, status classification, and redaction all live in the base Do/DoDownload the
// request is handed to.
func (d *driver) newRequest(ctx context.Context, rawURL string) (*stdhttp.Request, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gazelle: build request: %w", err)
	}
	req.Header.Set("Authorization", d.authHeader())
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// newCookieRequest builds an AlphaRatio session-cookie GET. The production client's jar
// attaches the cookie; a test doer without a jar receives it as an explicit header
// (never the URL). cookie is the exact session snapshot the caller committed to, so a
// concurrent renewal cannot swap it mid-request. Transport, status classification, and
// redaction live in the base Do/DoDownload the request is handed to.
func (d *driver) newCookieRequest(ctx context.Context, rawURL, cookie string) (*stdhttp.Request, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("gazelle: build request: %w", err)
	}
	req.Header.Set("User-Agent", alphaRatioUserAgent)
	if d.jar == nil {
		req.Header.Set("Cookie", cookie)
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

// requestContext disables redirect following for cookie-authenticated operations. An
// expired AlphaRatio session redirects to login; Search/Grab must see that response so
// they can renew once and retry instead of consuming login HTML.
func (d *driver) requestContext(ctx context.Context) context.Context {
	if d.profile.cookieAuth {
		return apphttp.WithNoRedirectFollow(ctx)
	}
	return ctx
}

func (d *driver) sessionSnapshot() sessionState {
	d.sessionMu.RLock()
	defer d.sessionMu.RUnlock()
	return d.session
}

// ensureSession returns an existing AlphaRatio session or creates one while holding the
// single-login gate. Waiting for another login observes ctx cancellation, and the
// session is rechecked after acquisition so concurrent callers share its result.
func (d *driver) ensureSession(ctx context.Context) error {
	if d.sessionSnapshot().cookie != "" {
		return nil
	}
	if err := d.loginGate.Acquire(ctx, 1); err != nil {
		return fmt.Errorf("gazelle: wait for automatic login: %w", err)
	}
	defer d.loginGate.Release(1)
	if d.sessionSnapshot().cookie != "" {
		return nil
	}
	return d.loginLocked(ctx)
}

// renewSession replaces the failed AlphaRatio session while holding the single-login
// gate. Waiting observes ctx cancellation; after acquisition, a newer non-empty
// generation suppresses duplicate renewal so the caller can retry with that session.
func (d *driver) renewSession(ctx context.Context, failedGeneration uint64) error {
	if err := d.loginGate.Acquire(ctx, 1); err != nil {
		return fmt.Errorf("gazelle: wait for automatic session renewal: %w", err)
	}
	defer d.loginGate.Release(1)
	current := d.sessionSnapshot()
	if current.cookie != "" && current.generation != failedGeneration {
		return nil
	}
	return d.loginLocked(ctx)
}

// loginLocked creates a clean AlphaRatio session with the configured credentials,
// verifies a same-site post-login logout link, then persists the replacement cookie
// before publishing it to concurrent requests. Callers must hold loginGate.
func (d *driver) loginLocked(ctx context.Context) error {
	form, err := d.alphaRatioLoginForm()
	if err != nil {
		return err
	}

	previous := d.sessionSnapshot()
	d.replaceJarCookies("")
	restore := true
	defer func() {
		if restore {
			d.replaceJarCookies(previous.cookie)
		}
	}()

	cookie, err := d.requestAlphaRatioLogin(ctx, form)
	if err != nil {
		return err
	}
	if d.persist != nil {
		if err := d.persist(ctx, alphaRatioCookieSetting, cookie); err != nil {
			return alphaRatioLoginError("persist replacement session", err)
		}
	}

	d.sessionMu.Lock()
	d.session = sessionState{cookie: cookie, generation: previous.generation + 1}
	d.sessionMu.Unlock()
	restore = false
	return nil
}

func (d *driver) alphaRatioLoginForm() (url.Values, error) {
	username := d.Cfg["username"]
	password := d.Cfg["password"]
	if strings.TrimSpace(username) == "" || strings.TrimSpace(password) == "" {
		return nil, alphaRatioLoginError("username or password is empty", nil)
	}
	return url.Values{
		"username":   {username},
		"password":   {password},
		"keeplogged": {"1"},
	}, nil
}

// requestAlphaRatioLogin submits the login form and returns the bounded response's
// serialized session cookies only after authenticated-page validation. It updates the
// private cookie jar when present and wraps transport, response, and validation failures
// with [login.ErrLoginFailed].
func (d *driver) requestAlphaRatioLogin(ctx context.Context, form url.Values) (string, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodPost, d.BaseURL+"login.php", strings.NewReader(form.Encode()))
	if err != nil {
		return "", alphaRatioLoginError("build login request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Referer", d.BaseURL+"login.php")
	req.Header.Set("User-Agent", alphaRatioUserAgent)

	resp, err := d.Doer.Do(req)
	if err != nil {
		return "", alphaRatioLoginError("login request failed", apphttp.RedactURLError(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", alphaRatioLoginError(fmt.Sprintf("login returned HTTP %d", resp.StatusCode), nil)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLoginBodyBytes+1))
	if err != nil {
		return "", alphaRatioLoginError("read login response", err)
	}
	if len(body) > maxLoginBodyBytes {
		return "", alphaRatioLoginError("login response exceeded size cap", nil)
	}
	if !d.alphaRatioAuthenticatedPage(body) {
		return "", alphaRatioLoginError("post-login page did not confirm authentication", nil)
	}

	if d.jar != nil && len(resp.Cookies()) > 0 {
		d.jar.SetCookies(d.cookieURL, resp.Cookies())
	}
	cookie := d.cookieHeader(resp.Cookies())
	if cookie == "" {
		return "", alphaRatioLoginError("login returned no usable session cookie", nil)
	}
	return cookie, nil
}

// alphaRatioAuthenticatedPage reports whether body contains an actual logout link whose
// path is /logout.php and whose host is relative or matches the configured tracker.
// Plain-text mentions and links to other hosts are not authentication evidence.
func (d *driver) alphaRatioAuthenticatedPage(body []byte) bool {
	doc, err := goquery.NewDocumentFromReader(bytes.NewReader(body))
	if err != nil {
		return false
	}
	authenticated := false
	doc.Find("a[href]").EachWithBreak(func(_ int, link *goquery.Selection) bool {
		href, exists := link.Attr("href")
		if !exists {
			return true
		}
		parsed, err := url.Parse(strings.TrimSpace(href))
		if err != nil {
			return true
		}
		sameHost := parsed.Host == "" || strings.EqualFold(parsed.Host, d.cookieURL.Host)
		authenticated = sameHost && (parsed.Path == "/logout.php" || parsed.Path == "logout.php")
		return !authenticated
	})
	return authenticated
}

func (d *driver) cookieHeader(responseCookies []*stdhttp.Cookie) string {
	if d.jar != nil {
		return serializeCookies(d.jar.Cookies(d.cookieURL))
	}
	return serializeCookies(responseCookies)
}

func (d *driver) replaceJarCookies(raw string) {
	if d.jar == nil {
		return
	}
	current := d.jar.Cookies(d.cookieURL)
	expired := make([]*stdhttp.Cookie, 0, len(current))
	for _, cookie := range current {
		//nolint:gosec // G124: deletion cookies are written only into the private jar; response security attributes are irrelevant.
		expired = append(expired, &stdhttp.Cookie{
			Name:    cookie.Name,
			Value:   "",
			Path:    "/",
			MaxAge:  -1,
			Expires: time.Unix(1, 0),
		})
	}
	if len(expired) > 0 {
		d.jar.SetCookies(d.cookieURL, expired)
	}
	if cookies := parseCookieHeader(raw); len(cookies) > 0 {
		d.jar.SetCookies(d.cookieURL, cookies)
	}
}

func doerCookieJar(doer search.Doer) stdhttp.CookieJar {
	if client, ok := doer.(*stdhttp.Client); ok {
		return client.Jar
	}
	if owner, ok := doer.(search.JarOwner); ok {
		return owner.CookieJar()
	}
	return nil
}

func parseCookieHeader(raw string) []*stdhttp.Cookie {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	req := &stdhttp.Request{Header: stdhttp.Header{"Cookie": []string{raw}}}
	return req.Cookies()
}

func serializeCookies(cookies []*stdhttp.Cookie) string {
	usable := make([]*stdhttp.Cookie, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie != nil && strings.TrimSpace(cookie.Name) != "" && cookie.Value != "" {
			usable = append(usable, cookie)
		}
	}
	sort.Slice(usable, func(i, j int) bool { return usable[i].Name < usable[j].Name })
	req := &stdhttp.Request{Header: stdhttp.Header{}}
	for _, cookie := range usable {
		req.AddCookie(cookie)
	}
	return req.Header.Get("Cookie")
}

func alphaRatioLoginError(reason string, cause error) error {
	err := fmt.Errorf("gazelle: AlphaRatio automatic login failed; verify configured username/password: %s: %w", reason, login.ErrLoginFailed)
	if cause == nil {
		return err
	}
	return errors.Join(err, cause)
}

func alphaRatioSessionRejected(operation string) error {
	return fmt.Errorf("gazelle: AlphaRatio automatic session renewal did not authenticate %s; verify configured username/password: %w", operation, login.ErrLoginFailed)
}
