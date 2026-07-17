package gazelle

import (
	"context"
	"errors"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// usetokenParam is the query suffix that requests a freeleech token on a download. The
// freeleech fallback strips it (its presence is also the trigger condition for the
// fallback, matching Prowlarr's link.Query.Contains("usetoken=1") guard).
const usetokenParam = "&usetoken=1"

// Grab fetches the authenticated download URL server-side and returns the .torrent
// bytes. The link itself carries no secret (fetchTorrent adds the API-key header or
// session cookie); the served feed therefore exposes the link and routes the fetch
// through the /dl proxy, which is what this server-side Grab drives.
//
// Freeleech-token fallback (Prowlarr's Redacted/Orpheus Download override): when the
// freeleech-token setting is on and the link requested a token (usetoken=1) but the
// response body is not a bencoded torrent (first byte != 'd'), the site returned an HTML
// "no tokens left" page instead of a torrent — so retry the SAME id with usetoken
// stripped. OPS never sees usetoken=0 because the retry removes the param entirely.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	body, contentType, err := d.fetchTorrent(ctx, link)
	if err != nil {
		return nil, err
	}
	if d.useFreeleechToken() && isTokenRequest(link) && !isBencoded(body) {
		retryLink := strings.Replace(link, usetokenParam, "", 1)
		body, contentType, err = d.fetchTorrent(ctx, retryLink)
		if err != nil {
			return nil, err
		}
	}
	return &search.GrabResult{Body: body, ContentType: contentType}, nil
}

// fetchTorrent fetches one download URL server-side. RED/OPS hand the request to the base
// DoDownload with the ClassifyAuth403 dialect; the base owns the torrent size cap
// (native.ErrDownloadTooLarge rather than a silent truncation), status classification,
// and host-only redaction. AlphaRatio routes through the cookie-session path so an
// expired session renews once and retries.
func (d *driver) fetchTorrent(ctx context.Context, link string) ([]byte, string, error) {
	if d.profile.cookieAuth {
		return d.fetchTorrentAttempt(ctx, link, true)
	}
	req, err := d.newRequest(ctx, link)
	if err != nil {
		return nil, "", err
	}
	resp, err := d.DoDownload(ctx, req, native.ClassifyAuth403)
	if err != nil {
		return nil, "", err
	}
	return resp.Body, resp.Header.Get("Content-Type"), nil
}

// fetchTorrentAttempt fetches one AlphaRatio download through the base transport under the
// cookie-session dialect. A classified auth failure (redirect/401/403) renews the session
// once and retries; a rate-limit, size-cap, or other transport error propagates as-is
// (already redacted and sentinel-bearing from the base).
func (d *driver) fetchTorrentAttempt(ctx context.Context, link string, allowRenew bool) ([]byte, string, error) {
	if err := d.ensureSession(ctx); err != nil {
		return nil, "", err
	}
	session := d.sessionSnapshot()
	req, err := d.newCookieRequest(ctx, link, session.cookie)
	if err != nil {
		return nil, "", err
	}
	resp, err := d.DoDownload(d.requestContext(ctx), req, classifyARCookie)
	if err == nil {
		return resp.Body, resp.Header.Get("Content-Type"), nil
	}
	// Only a classified auth failure (redirect/401/403) is renewable; a rate-limit,
	// size-cap, or other transport error propagates as-is.
	if !errors.Is(err, login.ErrLoginFailed) {
		return nil, "", err
	}
	if !allowRenew {
		return nil, "", alphaRatioSessionRejected("download")
	}
	if rerr := d.renewSession(ctx, session.generation); rerr != nil {
		return nil, "", rerr
	}
	return d.fetchTorrentAttempt(ctx, link, false)
}

// isTokenRequest reports whether a download link requested a freeleech token, mirroring
// Prowlarr's link.Query.Contains("usetoken=1") guard for the fallback.
func isTokenRequest(link string) bool {
	return strings.Contains(link, usetokenParam)
}

// isBencoded reports whether body looks like a bencoded .torrent (a bencoded dict starts
// with 'd'). An HTML "no tokens left" page does not, which is the freeleech fallback's
// signal. An empty body is treated as not bencoded.
func isBencoded(body []byte) bool {
	return len(body) > 0 && body[0] == 'd'
}
