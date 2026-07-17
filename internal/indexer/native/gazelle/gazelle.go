// Package gazelle is the native driver for trackers backed by Gazelle's
// ajax.php?action=browse API: Redacted, Orpheus, and AlphaRatio. RED/OPS authenticate
// with an API key in the Authorization header; AlphaRatio logs in with username/password
// and rides the resulting session cookie. Music groups flatten their nested torrents,
// while AlphaRatio's non-music groups are already one release each. All downloads are
// fetched server-side through /dl so header/cookie credentials never reach feed
// consumers. Everything but the Gazelle request/parse dialect and the per-site profile
// (plus AlphaRatio's cookie-session state) lives in the embedded native.Base, whose
// Do/DoDownload own the paced transport, host-only redaction, status classification, and
// capped body reads.
package gazelle

import (
	"context"
	"fmt"
	stdhttp "net/http"
	"net/url"
	"strings"
	"sync"

	"golang.org/x/sync/semaphore"

	"github.com/autobrr/harbrr/internal/indexer/native"
)

// driver is one configured Gazelle-family instance. It is built once per instance and
// cached by the registry. RED/OPS carry a static API key and hold no session state;
// AlphaRatio keeps a persisted cookie session and serializes automatic login/renewal
// through loginGate. The cookie-session fields are unused for the API-key sites.
type driver struct {
	native.Base
	profile profile

	persist   func(ctx context.Context, name, value string) error
	jar       stdhttp.CookieJar
	cookieURL *url.URL
	loginGate *semaphore.Weighted
	sessionMu sync.RWMutex
	session   sessionState
}

// sessionState is an immutable snapshot copied under sessionMu. generation advances
// whenever automatic login publishes a replacement cookie, allowing failed requests to
// suppress duplicate renewal without confusing their cookie with the current session.
type sessionState struct {
	cookie     string
	generation uint64
}

var _ native.Driver = (*driver)(nil)

// profile captures per-site auth, download, paging, and seeding behavior keyed by
// definition id. The zero/default profile is Redacted; Orpheus changes its auth prefix;
// AlphaRatio switches to cookie auth and fixed-page, torrents.php downloads.
type profile struct {
	site                string
	authPrefix          string
	cookieAuth          bool
	downloadViaTorrents bool
	pageSize            int
	minimumRatio        float64
	minimumSeedTime     int64
}

func profileFor(id string) profile {
	switch id {
	case "alpharatio":
		return profile{
			site:                id,
			cookieAuth:          true,
			downloadViaTorrents: true,
			pageSize:            50,
			minimumRatio:        1,
			minimumSeedTime:     259200,
		}
	case "orpheus":
		return profile{site: id, authPrefix: "token "}
	default:
		return profile{site: id}
	}
}

// New is the native.Factory for every Gazelle-family site. It builds the shared base
// (capabilities, normalised base URL, clock), resolves the per-site profile from the
// definition id, and — for AlphaRatio — seeds the cookie session from the stored
// setting and primes the doer's cookie jar when it has one.
func New(p native.Params) (native.Driver, error) {
	b, err := native.NewBase("gazelle", p)
	if err != nil {
		return nil, err
	}
	cookieURL, err := url.Parse(b.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("gazelle: parse base URL: %w", err)
	}
	session := sessionState{cookie: strings.TrimSpace(p.Cfg[alphaRatioCookieSetting])}
	if session.cookie != "" {
		session.generation = 1
	}
	jar := doerCookieJar(p.Doer)
	if jar != nil && session.cookie != "" {
		jar.SetCookies(cookieURL, parseCookieHeader(session.cookie))
	}
	return &driver{
		Base:      b,
		profile:   profileFor(p.Def.ID),
		persist:   p.PersistSetting,
		jar:       jar,
		cookieURL: cookieURL,
		loginGate: semaphore.NewWeighted(1),
		session:   session,
	}, nil
}

// NeedsResolver is false: the download URL carries no passkey, so the served feed link
// is safe to expose. Authentication is added server-side at grab time, which
// DownloadNeedsAuth signals instead.
func (d *driver) NeedsResolver() bool { return false }

// DownloadNeedsAuth is true: the download authenticates out-of-band via an API-key
// header or AlphaRatio session cookie, so the served feed routes through the /dl proxy
// and the driver's Grab fetches the torrent server-side with credentials attached.
func (d *driver) DownloadNeedsAuth() bool { return true }

// SupportsOffsetPaging reports true only for AlphaRatio. Its API exposes fixed 50-row
// pages; Search fetches enough upstream pages to satisfy harbrr's requested window.
func (d *driver) SupportsOffsetPaging() bool { return d.profile.pageSize > 0 }

// Test exercises the credentials with an empty browse query: for AlphaRatio this drives
// an automatic login, and any site surfaces a 401/403 as login.ErrLoginFailed (the
// registry records an auth_failure health event), while a parseable empty page confirms
// the credentials work.
func (d *driver) Test(ctx context.Context) error {
	return native.TestViaSearch(ctx, d)
}
