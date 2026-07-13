package nebulance

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/normalizer"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const (
	defaultLimit = 100
	maxBodyBytes = 16 << 20
)

var errResponseTooLarge = errors.New("nebulance: search response exceeds the size cap")

// Search sends a paged GET to NBL's JSON API and returns the requested Torznab
// result window. An unaligned offset may require two upstream pages. Unsupported
// short or season-only query shapes return an empty result without contacting the
// tracker, matching Prowlarr. Authentication, rate-limit, and parse failures retain
// their typed sentinel errors for registry health classification.
func (d *driver) Search(ctx context.Context, q search.Query) ([]*normalizer.Release, error) {
	rawURL, ok := d.buildSearchURL(q)
	if !ok {
		return []*normalizer.Release{}, nil
	}
	releases, err := d.searchPage(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	return d.completeOffsetPage(ctx, q, releases)
}

func (d *driver) searchPage(ctx context.Context, rawURL string) ([]*normalizer.Release, error) {
	resp, err := d.get(ctx, rawURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("nebulance: search unauthorized in non-interactive mode; verify or replace the configured API key: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("nebulance: search returned HTTP %d", resp.StatusCode)
	}

	body, err := readSearchBody(resp.Body)
	if err != nil {
		return nil, err
	}
	return d.parseReleases(body)
}

// completeOffsetPage removes the leading remainder from an upstream page and, when
// necessary, fetches the next page so callers receive the exact requested window.
func (d *driver) completeOffsetPage(ctx context.Context, q search.Query, releases []*normalizer.Release) ([]*normalizer.Release, error) {
	if q.Limit <= 0 || q.Offset <= 0 {
		return releases, nil
	}
	remainder := q.Offset % q.Limit
	if remainder == 0 {
		return releases, nil
	}
	if len(releases) <= remainder {
		return []*normalizer.Release{}, nil
	}

	page := append(make([]*normalizer.Release, 0, q.Limit), releases[remainder:]...)
	if len(page) >= q.Limit || len(releases) < q.Limit {
		return page[:min(len(page), q.Limit)], nil
	}

	nextQuery := q
	nextQuery.Offset += q.Limit - remainder
	nextURL, ok := d.buildSearchURL(nextQuery)
	if !ok {
		return page, nil
	}
	next, err := d.searchPage(ctx, nextURL)
	if err != nil {
		return nil, err
	}
	page = append(page, next...)
	return page[:min(len(page), q.Limit)], nil
}

func (d *driver) buildSearchURL(q search.Query) (string, bool) {
	params, ok := searchParams(q)
	if !ok {
		return "", false
	}
	params.Set("api_key", d.apikey)
	limit := q.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	params.Set("per_page", strconv.Itoa(limit))
	if q.Limit > 0 && q.Offset > 0 {
		if page := q.Offset / q.Limit; page > 0 {
			params.Set("page", strconv.Itoa(page))
		}
	}
	return d.baseURL + "api.php?" + params.Encode(), true
}

func searchParams(q search.Query) (url.Values, bool) {
	params := url.Values{"action": {"search"}, "age": {">0"}}
	if id := positiveID(q.TVMazeID); id != "" {
		params.Set("tvmaze", id)
	} else if id := normalizeIMDBID(q.IMDBID); id != "" {
		params.Set("imdb", id)
	}

	term := strings.TrimSpace(q.Keywords)
	if term != "" {
		params.Set("release", term)
	}
	if date, daily := dailyDate(q.Season, q.Ep); daily {
		if term != "" {
			params.Set("name", term)
		}
		params.Set("release", date)
	} else {
		setEpisodeParams(params, q.Season, q.Ep)
	}

	if unsupportedSeasonOnly(params) || tooShort(params.Get("name")) || tooShort(params.Get("release")) {
		return nil, false
	}
	return params, true
}

func setEpisodeParams(params url.Values, season, episode string) {
	if value, ok := nonNegativeInt(season); ok {
		params.Set("season", strconv.Itoa(value))
	}
	if value, ok := nonNegativeInt(episode); ok {
		params.Set("episode", strconv.Itoa(value))
	}
}

func unsupportedSeasonOnly(params url.Values) bool {
	if !params.Has("season") && !params.Has("episode") {
		return false
	}
	for _, key := range []string{"name", "release", "tvmaze", "imdb"} {
		if params.Get(key) != "" {
			return false
		}
	}
	return true
}

func tooShort(value string) bool {
	return value != "" && utf8.RuneCountInString(value) < 3
}

func dailyDate(season, episode string) (string, bool) {
	season = strings.TrimSpace(season)
	episode = strings.TrimSpace(episode)
	if len(season) != 4 || len(episode) != 5 {
		return "", false
	}
	parsed, err := time.Parse("2006 01/02", season+" "+episode)
	if err != nil {
		return "", false
	}
	return parsed.Format("2006.01.02"), true
}

func positiveID(raw string) string {
	value, ok := nonNegativeInt(raw)
	if !ok || value <= 0 {
		return ""
	}
	return strconv.Itoa(value)
}

func nonNegativeInt(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	value, err := strconv.Atoi(raw)
	return value, err == nil && value >= 0
}

func normalizeIMDBID(raw string) string {
	raw = strings.TrimSpace(raw)
	digits := strings.TrimPrefix(strings.ToLower(raw), "tt")
	value, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || value <= 0 {
		return ""
	}
	return fmt.Sprintf("tt%07d", value)
}

func (d *driver) get(ctx context.Context, rawURL string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("nebulance: build request to %s: %w", apphttp.SchemeHost(rawURL), apphttp.RedactURLError(err))
	}
	req.Header.Set("Accept", "application/json")
	resp, err := d.doer.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nebulance: request to %s: %w", apphttp.SchemeHost(rawURL), apphttp.RedactURLError(err))
	}
	return resp, nil
}

// readSearchBody reads at most the configured response cap plus one sentinel byte.
// Read and overflow failures are classified as parse errors without exposing body data.
func readSearchBody(r io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBodyBytes+1))
	if err != nil {
		return nil, fmt.Errorf("nebulance: read search response: %s: %w", apphttp.RedactError(err), search.ErrParseError)
	}
	if len(body) > maxBodyBytes {
		return nil, fmt.Errorf("%w: %w", errResponseTooLarge, search.ErrParseError)
	}
	return body, nil
}
