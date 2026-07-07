package registry

import (
	"context"
	stdhttp "net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
)

// TestNewDoerExposesClientJar pins the single-jar wiring: the production doer
// must report the *http.Client's OWN jar via CookieJar() (search.JarOwner), so
// the engine seeds login cookies into the SAME jar the transport applies and
// records on every hop. A second jar puts duplicate — and after a login-time
// session rotation, stale-first — Cookie pairs on the wire, which a tracker
// reads as the logged-out session.
func TestNewDoerExposesClientJar(t *testing.T) {
	t.Parallel()
	d, err := newDoer(ClientParams{RateInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("newDoer: %v", err)
	}
	jo, ok := d.(search.JarOwner)
	if !ok {
		t.Fatalf("newDoer returned %T, want a search.JarOwner", d)
	}
	pd, ok := d.(*pacedDoer)
	if !ok {
		t.Fatalf("newDoer returned %T, want *pacedDoer", d)
	}
	client, ok := pd.base.(*stdhttp.Client)
	if !ok {
		t.Fatalf("paced base is %T, want *http.Client", pd.base)
	}
	if client.Jar == nil {
		t.Fatal("production client has no cookie jar")
	}
	if jo.CookieJar() != client.Jar {
		t.Error("CookieJar() is not the http.Client's own jar")
	}
}

// TestNewDoerNoProxyUsesDefaultTransport guards the typed-nil Transport panic:
// buildTransport returns a nil *http.Transport for the common no-proxy case, and
// assigning that to http.Client.Transport (a RoundTripper interface) used to make it
// a non-nil interface wrapping a nil pointer — so the stdlib called into a nil
// *Transport and panicked (alternateRoundTripper) on the first request instead of
// falling back to http.DefaultTransport. Offline tests inject a replay Doer and never
// build this client, so the panic only surfaced on a live run.
func TestNewDoerNoProxyUsesDefaultTransport(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, _ *stdhttp.Request) {
		w.WriteHeader(stdhttp.StatusOK)
	}))
	t.Cleanup(srv.Close)

	d, err := newDoer(ClientParams{RateInterval: time.Millisecond})
	if err != nil {
		t.Fatalf("newDoer: %v", err)
	}

	// Structural: with no proxy, Transport must stay a TRUE nil interface (not a
	// typed-nil *http.Transport), so the client falls back to http.DefaultTransport.
	if pd, ok := d.(*pacedDoer); ok {
		if c, ok := pd.base.(*stdhttp.Client); ok && c.Transport != nil {
			t.Errorf("no-proxy client Transport is non-nil (typed-nil bug): %#v", c.Transport)
		}
	}

	// Functional: a real request must not panic and must reach the server.
	req, err := stdhttp.NewRequestWithContext(context.Background(), stdhttp.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := d.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != stdhttp.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}
