// Package nebulance implements the native Nebulance (NBL) JSON API driver.
package nebulance

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/mapper"
	"github.com/autobrr/harbrr/internal/indexer/cardigann/search"
	"github.com/autobrr/harbrr/internal/indexer/native"
)

const (
	defaultBaseURL      = "https://nebulance.io/"
	requestDelaySeconds = 2.0
)

type driver struct {
	def     *loader.Definition
	caps    *mapper.Capabilities
	apikey  string
	doer    search.Doer
	baseURL string
	clock   func() time.Time
	log     zerolog.Logger
}

var (
	_                 native.Driver      = (*driver)(nil)
	_                 native.OffsetPager = (*driver)(nil)
	errAPIKeyRequired                    = errors.New("nebulance: API key is required")
)

// Families returns Nebulance's native family registration.
func Families() []native.Family {
	return []native.Family{{Definition: Definition(), Factory: New}}
}

// Definition returns Nebulance's static settings and capabilities definition.
func Definition() *loader.Definition {
	delay := requestDelaySeconds
	allowRaw := true
	allowIMDB := true
	return &loader.Definition{
		ID:           "nebulance",
		Name:         "Nebulance",
		Description:  "Nebulance (NBL) — ratioless private TV tracker (native driver)",
		Language:     "en-US",
		Type:         "private",
		Encoding:     "UTF-8",
		Links:        []string{defaultBaseURL},
		RequestDelay: &delay,
		Settings: []loader.SettingsField{
			{Name: "apikey", Label: "API Key", Type: "text", Required: true},
		},
		Caps: loader.Caps{
			CategoryMappings: []loader.CategoryMapping{
				categoryMapping("1", "TV", "TV"),
				categoryMapping("2", "SD", "TV/SD"),
				categoryMapping("3", "HD", "TV/HD"),
				categoryMapping("4", "UHD", "TV/UHD"),
			},
			Modes: loader.Modes{
				Search:   []string{"q"},
				TVSearch: []string{"q", "season", "ep", "imdbid", "tvmazeid"},
			},
			AllowRawSearch:    &allowRaw,
			AllowTVSearchIMDB: &allowIMDB,
		},
	}
}

func categoryMapping(id, desc, category string) loader.CategoryMapping {
	return loader.CategoryMapping{
		ID:   loader.Scalar{Value: id, Set: true},
		Cat:  category,
		Desc: desc,
	}
}

// New builds one Nebulance driver from decrypted instance settings. It rejects a
// nil definition or a missing API key and returns capability-mapping errors.
func New(p native.Params) (native.Driver, error) {
	if p.Def == nil {
		return nil, errors.New("nebulance: nil definition")
	}
	apikey := strings.TrimSpace(p.Cfg["apikey"])
	if apikey == "" {
		return nil, errAPIKeyRequired
	}
	caps, err := mapper.Build(p.Def)
	if err != nil {
		return nil, fmt.Errorf("nebulance: build capabilities for %q: %w", p.Def.ID, err)
	}
	baseURL := p.BaseURL
	if baseURL == "" && len(p.Def.Links) > 0 {
		baseURL = p.Def.Links[0]
	}
	clock := p.Clock
	if clock == nil {
		clock = time.Now
	}
	return &driver{
		def:     p.Def,
		caps:    caps,
		apikey:  apikey,
		doer:    p.Doer,
		baseURL: strings.TrimRight(baseURL, "/") + "/",
		clock:   clock,
		log:     p.Logger,
	}, nil
}

// Capabilities returns the capabilities built from Nebulance's static definition.
func (d *driver) Capabilities() *mapper.Capabilities { return d.caps }

// NeedsResolver hides NBL's token-bearing download URL behind harbrr's /dl endpoint.
func (d *driver) NeedsResolver() bool { return true }

// DownloadNeedsAuth is false because the returned download URL carries its own token.
func (d *driver) DownloadNeedsAuth() bool { return false }

// SupportsOffsetPaging reports that NBL accepts page and per_page upstream.
func (d *driver) SupportsOffsetPaging() bool { return true }

// Test verifies the configured API key with a one-result browse request. It
// returns [login.ErrLoginFailed] when Nebulance rejects the credentials.
func (d *driver) Test(ctx context.Context) error {
	if strings.TrimSpace(d.apikey) == "" {
		return errAPIKeyRequired
	}
	_, err := d.Search(ctx, search.Query{Limit: 1})
	return err
}
