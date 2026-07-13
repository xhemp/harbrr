package nebulance

import (
	"context"
	"errors"
	"fmt"
	"io"
	stdhttp "net/http"

	apphttp "github.com/autobrr/harbrr/internal/http"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const maxTorrentBytes = 64 << 20

var (
	errDownloadTooLarge      = errors.New("nebulance: download exceeds the size cap")
	errDownloadRequestFailed = errors.New("nebulance: download request failed")
)

// Grab fetches NBL's token-bearing download URL server-side and returns bounded
// torrent bytes. It preserves cancellation, authentication, rate-limit, and size
// errors while sanitizing other transport and read failures.
func (d *driver) Grab(ctx context.Context, link string) (*search.GrabResult, error) {
	resp, err := d.fetch(ctx, link)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch {
	case resp.StatusCode == stdhttp.StatusUnauthorized || resp.StatusCode == stdhttp.StatusForbidden:
		return nil, fmt.Errorf("nebulance: download unauthorized in non-interactive mode; verify or replace the configured API key: %w", login.ErrLoginFailed)
	case search.IsRateLimitStatus(resp.StatusCode):
		return nil, &search.RateLimitedError{
			StatusCode: resp.StatusCode,
			RetryAfter: search.ParseRetryAfter(resp.Header.Get("Retry-After"), d.clock),
		}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, fmt.Errorf("nebulance: download returned HTTP %d", resp.StatusCode)
	}

	body, err := readTorrent(resp.Body, maxTorrentBytes)
	if err != nil {
		return nil, sanitizeGrabError(err)
	}
	return &search.GrabResult{Body: body, ContentType: resp.Header.Get("Content-Type")}, nil
}

func (d *driver) fetch(ctx context.Context, rawURL string) (*stdhttp.Response, error) {
	req, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, rawURL, nil)
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	resp, err := d.doer.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, context.DeadlineExceeded
		}
		return nil, fmt.Errorf("%w: %w", errDownloadRequestFailed, apphttp.RedactURLError(err))
	}
	return resp, nil
}

// readTorrent reads at most maxBytes plus one sentinel byte and rejects oversized
// or unreadable downloads without returning partial data.
func readTorrent(r io.Reader, maxBytes int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil {
		return nil, errDownloadRequestFailed
	}
	if int64(len(body)) > maxBytes {
		return nil, errDownloadTooLarge
	}
	return body, nil
}

// sanitizeGrabError preserves errors safe for callers and collapses all other
// failures to a token-free download error.
func sanitizeGrabError(err error) error {
	switch {
	case errors.Is(err, login.ErrLoginFailed),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, errDownloadTooLarge),
		errors.Is(err, errDownloadRequestFailed):
		return err
	}
	var rateLimited *search.RateLimitedError
	if errors.As(err, &rateLimited) {
		return err
	}
	return errDownloadRequestFailed
}
