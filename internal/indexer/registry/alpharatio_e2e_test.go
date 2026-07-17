package registry_test

import (
	"context"
	"errors"
	"io"
	stdhttp "net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/registry"
	"github.com/autobrr/harbrr/internal/secrets"
)

const (
	alphaRatioE2EUsername        = "ar-e2e-synthetic-user"
	alphaRatioE2EUpdatedUsername = "ar-e2e-updated-user"
	alphaRatioE2EPassword        = "AR-E2E-SYNTHETIC-PASSWORD-0000000000"
	alphaRatioE2ECookie          = "session=AR-E2E-SYNTHETIC-SESSION-0000000000"
	alphaRatioTorrent            = "d8:announce11:fake-tracker4:infod6:lengthi1ee"
)

type alphaRatioDoer struct {
	browseBody string

	mu         sync.Mutex
	reqs       []*stdhttp.Request
	loginForm  url.Values
	loginCalls int
}

func (d *alphaRatioDoer) Do(req *stdhttp.Request) (*stdhttp.Response, error) {
	d.mu.Lock()
	d.reqs = append(d.reqs, req.Clone(req.Context()))
	d.mu.Unlock()

	switch req.URL.Path {
	case "/login.php":
		if err := req.ParseForm(); err != nil {
			return nil, err
		}
		d.mu.Lock()
		d.loginCalls++
		d.loginForm = req.PostForm
		d.mu.Unlock()
		return &stdhttp.Response{
			StatusCode: stdhttp.StatusOK,
			Header:     stdhttp.Header{"Set-Cookie": []string{alphaRatioE2ECookie + "; Path=/; HttpOnly"}},
			Body:       io.NopCloser(strings.NewReader(`<a href="/logout.php">Logout</a>`)),
		}, nil
	case "/ajax.php", "/torrents.php":
		if req.Header.Get("Cookie") != alphaRatioE2ECookie {
			return &stdhttp.Response{
				StatusCode: stdhttp.StatusFound,
				Header:     stdhttp.Header{"Location": []string{"/login.php"}},
				Body:       stdhttp.NoBody,
			}, nil
		}
	}

	if req.URL.Path == "/torrents.php" {
		return &stdhttp.Response{
			StatusCode: stdhttp.StatusOK,
			Header:     stdhttp.Header{"Content-Type": []string{"application/x-bittorrent"}},
			Body:       io.NopCloser(strings.NewReader(alphaRatioTorrent)),
		}, nil
	}
	return &stdhttp.Response{
		StatusCode: stdhttp.StatusOK,
		Header:     stdhttp.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(d.browseBody)),
	}, nil
}

func (d *alphaRatioDoer) snapshot() ([]*stdhttp.Request, url.Values, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	reqs := make([]*stdhttp.Request, len(d.reqs))
	copy(reqs, d.reqs)
	return reqs, d.loginForm, d.loginCalls
}

func TestAlphaRatioRequiredSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		settings map[string]string
		field    string
	}{
		{name: "missing username", settings: map[string]string{"password": alphaRatioE2EPassword}, field: "username"},
		{name: "empty username", settings: map[string]string{"username": "  ", "password": alphaRatioE2EPassword}, field: "username"},
		{name: "missing password", settings: map[string]string{"username": alphaRatioE2EUsername}, field: "password"},
		{name: "empty password", settings: map[string]string{"username": alphaRatioE2EUsername, "password": "\t"}, field: "password"},
		{name: "redacted password on add", settings: map[string]string{"username": alphaRatioE2EUsername, "password": secrets.Redacted}, field: "password"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reg, _ := newRegistry(t, &alphaRatioDoer{})
			_, err := reg.Add(context.Background(), registry.AddParams{
				Slug: "ar", DefinitionID: "alpharatio", Settings: test.settings,
			})
			if !errors.Is(err, registry.ErrInvalid) {
				t.Fatalf("Add error = %v, want registry.ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), `setting "`+test.field+`" is required`) {
				t.Errorf("Add error = %q, want field %q", err, test.field)
			}
			instances, listErr := reg.List(context.Background())
			if listErr != nil {
				t.Fatalf("List: %v", listErr)
			}
			if len(instances) != 0 {
				t.Errorf("failed Add persisted %d instances", len(instances))
			}
		})
	}
}

func TestAlphaRatioUpdateRejectsBlankRequiredSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		patch map[string]string
		field string
	}{
		{name: "username", patch: map[string]string{"username": ""}, field: "username"},
		{name: "password", patch: map[string]string{"password": " "}, field: "password"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reg, _ := newRegistry(t, &alphaRatioDoer{})
			ctx := context.Background()
			if _, err := reg.Add(ctx, registry.AddParams{
				Slug: "ar", DefinitionID: "alpharatio",
				Settings: map[string]string{"username": alphaRatioE2EUsername, "password": alphaRatioE2EPassword},
			}); err != nil {
				t.Fatalf("Add: %v", err)
			}
			err := reg.Update(ctx, "ar", registry.UpdateParams{Settings: test.patch})
			if !errors.Is(err, registry.ErrInvalid) {
				t.Fatalf("Update error = %v, want registry.ErrInvalid", err)
			}
			if !strings.Contains(err.Error(), `setting "`+test.field+`" is required`) {
				t.Errorf("Update error = %q, want field %q", err, test.field)
			}
		})
	}
}

func TestAlphaRatioEndToEnd(t *testing.T) {
	golden, err := os.ReadFile("../native/gazelle/testdata/alpharatio_response.json")
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	doer := &alphaRatioDoer{browseBody: string(golden)}
	reg, _ := newRegistry(t, doer)
	ctx := context.Background()

	if _, err := reg.Add(ctx, registry.AddParams{
		Slug:         "ar",
		DefinitionID: "alpharatio",
		Settings: map[string]string{
			"username": alphaRatioE2EUsername,
			"password": alphaRatioE2EPassword,
		},
	}); err != nil {
		t.Fatalf("Add(alpharatio): %v", err)
	}
	if err := reg.Update(ctx, "ar", registry.UpdateParams{Settings: map[string]string{
		"username": alphaRatioE2EUpdatedUsername,
		"password": secrets.Redacted,
	}}); err != nil {
		t.Fatalf("Update(alpharatio, redacted password): %v", err)
	}
	_, views, err := reg.Get(ctx, "ar")
	if err != nil {
		t.Fatalf("Get(alpharatio): %v", err)
	}
	byName := make(map[string]registry.SettingView, len(views))
	for _, view := range views {
		byName[view.Name] = view
	}
	if password := byName["password"]; !password.Secret || password.Value != secrets.Redacted {
		t.Errorf("password view = %+v, want secret redaction", password)
	}
	if username := byName["username"]; username.Secret || username.Value != alphaRatioE2EUpdatedUsername {
		t.Errorf("username view = %+v, want updated non-secret value", username)
	}

	idx, ok := reg.Indexer(ctx, "ar")
	if !ok {
		t.Fatal("alpharatio indexer should resolve")
	}
	if idx.NeedsResolver() || !idx.DownloadNeedsAuth() {
		t.Errorf("resolver flags NeedsResolver=%v DownloadNeedsAuth=%v", idx.NeedsResolver(), idx.DownloadNeedsAuth())
	}
	releases, err := idx.Search(ctx, search.Query{Keywords: "Example Movie", Limit: 50})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1", len(releases))
	}
	grab, err := idx.Grab(ctx, releases[0].Link)
	if err != nil {
		t.Fatalf("Grab: %v", err)
	}
	if string(grab.Body) != alphaRatioTorrent {
		t.Errorf("grab body = %q", grab.Body)
	}

	requests, loginForm, loginCalls := doer.snapshot()
	if loginCalls != 1 {
		t.Errorf("login calls = %d, want 1", loginCalls)
	}
	if loginForm.Get("username") != alphaRatioE2EUpdatedUsername || loginForm.Get("password") != alphaRatioE2EPassword || loginForm.Get("keeplogged") != "1" {
		t.Errorf("automatic login did not use effective stored credentials")
	}
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want login + search + grab", len(requests))
	}
	for _, req := range requests[1:] {
		if req.Header.Get("Cookie") != alphaRatioE2ECookie || req.Header.Get("User-Agent") != "harbrr" {
			t.Errorf("request auth headers cookie=%q user-agent=%q", req.Header.Get("Cookie"), req.Header.Get("User-Agent"))
		}
		if strings.Contains(req.URL.String(), alphaRatioE2ECookie) {
			t.Errorf("cookie leaked into URL: %q", req.URL)
		}
	}

	_, views, err = reg.Get(ctx, "ar")
	if err != nil {
		t.Fatalf("Get after login: %v", err)
	}
	byName = make(map[string]registry.SettingView, len(views))
	for _, view := range views {
		byName[view.Name] = view
	}
	if cookie := byName["cookie"]; !cookie.Secret || cookie.Value != secrets.Redacted {
		t.Errorf("generated cookie view = %+v, want persisted secret redaction", cookie)
	}
}
