package nebulance

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/login"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

const testDownloadURL = "https://nebulance.io/api.php?action=download&apikey=NBL-SYNTHETIC-DOWNLOAD-TOKEN&torrentid=101"

type observedBody struct {
	io.Reader
	closed bool
}

func (b *observedBody) Close() error {
	b.closed = true
	return nil
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) {
	return 0, errors.New("synthetic read failure")
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func TestGrabReturnsTorrent(t *testing.T) {
	t.Parallel()
	body := &observedBody{Reader: strings.NewReader("d4:name7:examplee")}
	doer := &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		resp := response(stdhttp.StatusOK, "")
		resp.Body = body
		resp.Header.Set("Content-Type", "application/x-bittorrent")
		return resp, nil
	}}
	driver := liveDriver(t, doer)
	result, err := driver.Grab(context.Background(), testDownloadURL)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(result.Body) != "d4:name7:examplee" || result.ContentType != "application/x-bittorrent" || result.Redirect != "" {
		t.Error("grab result mismatch")
	}
	if len(doer.reqs) != 1 || doer.reqs[0].method != stdhttp.MethodGet {
		t.Error("grab must issue one GET")
	}
	if !body.closed {
		t.Error("response body was not closed")
	}
}

func TestGrabTransportErrorRedactsToken(t *testing.T) {
	t.Parallel()
	transportErr := &url.Error{Op: "Get", URL: testDownloadURL, Err: errors.New("connection refused")}
	driver := liveDriver(t, &errorDoer{err: transportErr})
	_, err := driver.Grab(context.Background(), testDownloadURL)
	if err == nil {
		t.Fatal("want transport error")
	}
	if strings.Contains(err.Error(), "NBL-SYNTHETIC-DOWNLOAD-TOKEN") || strings.Contains(err.Error(), "apikey=") {
		t.Error("grab error leaked download token")
	}
}

func TestGrabContextErrors(t *testing.T) {
	t.Parallel()
	for _, want := range []error{context.Canceled, context.DeadlineExceeded} {
		driver := liveDriver(t, &errorDoer{err: want})
		if _, err := driver.Grab(context.Background(), testDownloadURL); !errors.Is(err, want) {
			t.Errorf("err = %v, want %v", err, want)
		}
	}
}

func TestGrabStatusDispatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		status   int
		wantAuth bool
		wantRate bool
		wantHTTP string
	}{
		{name: "unauthorized", status: stdhttp.StatusUnauthorized, wantAuth: true},
		{name: "forbidden", status: stdhttp.StatusForbidden, wantAuth: true},
		{name: "rate limited", status: stdhttp.StatusTooManyRequests, wantRate: true},
		{name: "unavailable", status: stdhttp.StatusServiceUnavailable, wantRate: true},
		{name: "generic", status: stdhttp.StatusInternalServerError, wantHTTP: "HTTP 500"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := &observedBody{Reader: strings.NewReader("error")}
			driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
				resp := response(tt.status, "")
				resp.Body = body
				if tt.wantRate {
					resp.Header.Set("Retry-After", "120")
				}
				return resp, nil
			}})
			_, err := driver.Grab(context.Background(), testDownloadURL)
			if tt.wantAuth {
				assertActionableAuthError(t, err)
			} else if errors.Is(err, login.ErrLoginFailed) {
				t.Errorf("auth classification = %v, want %v: %v", errors.Is(err, login.ErrLoginFailed), tt.wantAuth, err)
			}
			var rateLimited *search.RateLimitedError
			gotRate := errors.As(err, &rateLimited)
			if tt.wantRate {
				if !gotRate {
					t.Fatalf("err = %v, want RateLimitedError", err)
				}
				if rateLimited.StatusCode != tt.status || rateLimited.RetryAfter != 2*time.Minute {
					t.Errorf("rate-limit metadata = (%d, %s), want (%d, 2m)", rateLimited.StatusCode, rateLimited.RetryAfter, tt.status)
				}
			} else if gotRate {
				t.Errorf("unexpected rate-limit classification: %v", err)
			}
			if tt.wantHTTP != "" && (err == nil || !strings.Contains(err.Error(), tt.wantHTTP)) {
				t.Errorf("err = %v, want %q", err, tt.wantHTTP)
			}
			if !body.closed {
				t.Error("response body was not closed")
			}
		})
	}
}

func TestReadTorrentBoundaries(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		reader   io.Reader
		wantSize int
		wantErr  error
	}{
		{name: "exactly max", reader: strings.NewReader("0123456789abcdef"), wantSize: 16},
		{name: "max plus one", reader: strings.NewReader("0123456789abcdefg"), wantErr: errDownloadTooLarge},
		{name: "read failure", reader: failingReader{}, wantErr: errDownloadRequestFailed},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := readTorrent(tt.reader, 16)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr == nil && len(body) != tt.wantSize {
				t.Errorf("body size = %d, want %d", len(body), tt.wantSize)
			}
		})
	}
}

func TestGrabClosesBodyOnReadFailure(t *testing.T) {
	t.Parallel()
	body := &observedBody{Reader: failingReader{}}
	driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		resp := response(stdhttp.StatusOK, "")
		resp.Body = body
		return resp, nil
	}})
	if _, err := driver.Grab(context.Background(), testDownloadURL); !errors.Is(err, errDownloadRequestFailed) {
		t.Errorf("err = %v, want errDownloadRequestFailed", err)
	}
	if !body.closed {
		t.Error("response body was not closed")
	}
}

func TestGrabClosesBodyOnOverflow(t *testing.T) {
	body := &observedBody{Reader: io.LimitReader(zeroReader{}, maxTorrentBytes+1)}
	driver := liveDriver(t, &scriptDoer{handler: func(*stdhttp.Request) (*stdhttp.Response, error) {
		resp := response(stdhttp.StatusOK, "")
		resp.Body = body
		return resp, nil
	}})
	if _, err := driver.Grab(context.Background(), testDownloadURL); !errors.Is(err, errDownloadTooLarge) {
		t.Errorf("err = %v, want errDownloadTooLarge", err)
	}
	if !body.closed {
		t.Error("response body was not closed")
	}
}
