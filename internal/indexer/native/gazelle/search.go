package gazelle

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

// vaArtist is the "various artists" sentinel Prowlarr skips for the artistname param —
// a VA compilation has no single artist to filter on.
const vaArtist = "VA"

// Search issues the authenticated browse request for the query and returns the parsed
// releases. RED/OPS hand a single request to the base ClassifyAuth403 dialect (401/403
// -> login.ErrLoginFailed so the registry records an auth_failure event, a rate-limit
// status -> RateLimitedError, any other non-2xx an error). AlphaRatio pages its fixed
// 50-row API and renews its session once after a redirect, 401/403, or auth-failure
// body. Credentials ride only in request headers and are never logged.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	if d.profile.cookieAuth {
		return d.searchPaged(ctx, q)
	}
	req, err := d.newRequest(ctx, d.buildBrowseURL(q))
	if err != nil {
		return nil, err
	}
	resp, err := d.Do(ctx, req, native.ClassifyAuth403)
	if err != nil {
		return nil, err
	}
	return d.parseBrowse(resp.Body, "")
}

func (d *driver) searchPage(ctx context.Context, rawURL string) ([]*normalizer.Release, error) {
	return d.searchPageAttempt(ctx, rawURL, true)
}

// searchPageAttempt fetches one AlphaRatio browse page through the base transport under
// the cookie-session dialect. A classified auth failure (redirect/401/403) or an
// auth-failure response body triggers a single session renewal and one retry; a
// rate-limit or other transport error propagates as-is (already redacted and
// sentinel-bearing from the base).
func (d *driver) searchPageAttempt(ctx context.Context, rawURL string, allowRenew bool) ([]*normalizer.Release, error) {
	if err := d.ensureSession(ctx); err != nil {
		return nil, err
	}
	session := d.sessionSnapshot()
	req, err := d.newCookieRequest(ctx, rawURL, session.cookie)
	if err != nil {
		return nil, err
	}
	resp, err := d.Do(d.requestContext(ctx), req, classifyARCookie)
	if err != nil {
		if errors.Is(err, login.ErrLoginFailed) {
			return d.retrySearchAfterAuthFailure(ctx, rawURL, session.generation, allowRenew)
		}
		return nil, err
	}
	releases, err := d.parseBrowse(resp.Body, session.cookie)
	if !errors.Is(err, login.ErrLoginFailed) {
		return releases, err
	}
	return d.retrySearchAfterAuthFailure(ctx, rawURL, session.generation, allowRenew)
}

func (d *driver) retrySearchAfterAuthFailure(ctx context.Context, rawURL string, generation uint64, allowRenew bool) ([]*normalizer.Release, error) {
	if !allowRenew {
		return nil, alphaRatioSessionRejected("search")
	}
	if err := d.renewSession(ctx, generation); err != nil {
		return nil, err
	}
	return d.searchPageAttempt(ctx, rawURL, false)
}

// searchPaged translates harbrr's arbitrary offset/limit window to AlphaRatio's fixed
// 50-row page API. It fetches only the pages overlapping the requested window, then
// trims the leading partial page and requested limit before the shared Torznab paging
// layer sees the result.
func (d *driver) searchPaged(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	pageSize := d.profile.pageSize
	limit := q.Limit
	if limit <= 0 {
		limit = pageSize
	}
	offset := max(q.Offset, 0)
	firstPage := offset/pageSize + 1
	leading := offset % pageSize
	pagesNeeded := (leading + limit + pageSize - 1) / pageSize

	releases := make([]*normalizer.Release, 0, pagesNeeded*pageSize)
	for page := firstPage; page < firstPage+pagesNeeded; page++ {
		pageReleases, err := d.searchPage(ctx, d.buildBrowseURL(q, page))
		if err != nil {
			return nil, err
		}
		releases = append(releases, pageReleases...)
		if len(pageReleases) < pageSize {
			break
		}
	}
	if leading >= len(releases) {
		return nil, nil
	}
	releases = releases[leading:]
	if len(releases) > limit {
		releases = releases[:limit]
	}
	return releases, nil
}

// buildBrowseURL composes the ajax.php?action=browse request URL. order_by=time and
// order_way=desc are always set; searchstr carries the free-text term (dots replaced by
// spaces, per Prowlarr); the fielded music params artistname/groupname/year are set when
// present (artistname is skipped for an empty value or "VA"); and one filter_cat[<id>]=1
// is emitted per requested tracker category. recordlabel is intentionally NOT sent —
// RED/OPS do not advertise or use a Label param (Prowlarr RED/OPS RequestGenerator).
// AlphaRatio adds its freeleech/scene/IMDB/page parameters. The URL carries no secret.
func (d *driver) buildBrowseURL(q search.Query, requestedPage ...int) string {
	page := 0
	if len(requestedPage) > 0 {
		page = requestedPage[0]
	}
	params := url.Values{}
	params.Set("action", "browse")
	params.Set("order_by", "time")
	params.Set("order_way", "desc")
	if term := sanitizeTerm(q.Keywords); term != "" {
		params.Set("searchstr", term)
	}
	if artist := strings.TrimSpace(q.Artist); artist != "" && artist != vaArtist {
		params.Set("artistname", artist)
	}
	if album := strings.TrimSpace(q.Album); album != "" {
		params.Set("groupname", album)
	}
	if year := strings.TrimSpace(q.Year); year != "" {
		params.Set("year", year)
	}
	if d.profile.site == "alpharatio" {
		// AlphaRatio stores imdb ids as "tt#######" tags (parse.go's imdbTag mirrors
		// this). The torznab imdbid param arrives as bare digits, so it must be
		// rendered as the full form — Jackett normalizes via GetFullImdbId before its
		// GazelleTracker sets taglist — or the tag filter matches nothing.
		if imdbID := fullIMDBID(q.IMDBID); imdbID != "" {
			params.Set("taglist", imdbID)
		}
		if truthy(d.Cfg["freeleech_only"]) {
			params.Set("freetorrent", "1")
		}
		if truthy(d.Cfg["exclude_scene"]) {
			params.Set("scene", "0")
		}
		if page > 1 {
			params.Set("page", strconv.Itoa(page))
		}
	}
	encoded := params.Encode()
	if cats := filterCats(q.Categories); cats != "" {
		encoded += "&" + cats
	}
	return fmt.Sprintf("%sajax.php?%s", d.BaseURL, encoded)
}

func truthy(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "on", "yes":
		return true
	default:
		return false
	}
}

// sanitizeTerm trims the free-text term and replaces dots with spaces, matching
// Prowlarr's GazelleRequestGenerator term handling (Trim + Replace(".", " ")).
func sanitizeTerm(keywords string) string {
	return strings.ReplaceAll(strings.TrimSpace(keywords), ".", " ")
}

// fullIMDBID renders an imdb id as Jackett's GetFullImdbId ("tt" + the numeric id, a
// minimum of seven digits): a leading "tt" is stripped, the rest parsed and
// zero-padded. A non-numeric id yields "" (the taglist param is then omitted).
func fullIMDBID(raw string) string {
	s := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "tt")
	n, err := strconv.Atoi(s)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("tt%07d", n)
}

// filterCats renders the per-category filter_cat[<id>]=1 params Prowlarr emits, one per
// requested tracker category, de-duplicated in request order. q.Categories already holds
// the tracker category ids (the Torznab layer mapped the newznab cats to tracker cats
// before building the query), so each id is emitted verbatim. The "[" / "]" in the key
// are percent-encoded so the URL is well-formed.
func filterCats(cats []string) string {
	seen := make(map[string]struct{}, len(cats))
	parts := make([]string, 0, len(cats))
	for _, c := range cats {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if _, dup := seen[c]; dup {
			continue
		}
		seen[c] = struct{}{}
		parts = append(parts, url.QueryEscape(fmt.Sprintf("filter_cat[%s]", c))+"=1")
	}
	return strings.Join(parts, "&")
}
