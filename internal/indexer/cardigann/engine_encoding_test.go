package cardigann

import (
	"strings"
	"testing"

	"github.com/autobrr/harbrr/internal/indexer/cardigann/loader"
)

const encodingEngineDef = `---
id: encengine
name: Encoding Engine Fixture
description: engine-level encoding resolution
language: en-US
type: public
encoding: UTF-8
links:
  - https://enc.invalid/
caps:
  categorymappings:
    - {id: 1, cat: Movies}
  modes:
    search: [q]
search:
  path: /browse
  inputs:
    q: "{{ .Keywords }}"
  rows:
    selector: div.row
  fields:
    category:
      text: Movies
    title:
      selector: a.title
    download:
      selector: a.title
      attribute: href
    size:
      selector: span.size
    seeders:
      selector: span.seeders
`

// TestNewEngine_UnknownEncoding proves the def encoding resolves at construction:
// a resolvable non-UTF-8 charset builds, while an unresolvable one is a LOUD
// construction error (never a silent UTF-8 fallback that would emit mojibake).
func TestNewEngine_UnknownEncoding(t *testing.T) {
	t.Parallel()

	def, err := loader.Parse([]byte(encodingEngineDef))
	if err != nil {
		t.Fatalf("loader.Parse: %v", err)
	}

	def.Encoding = "windows-1251"
	if _, err := NewEngine(def, WithClock(fixedClock())); err != nil {
		t.Fatalf("NewEngine with resolvable encoding: %v", err)
	}

	def.Encoding = "definitely-not-a-charset"
	_, err = NewEngine(def, WithClock(fixedClock()))
	if err == nil {
		t.Fatal("NewEngine with unknown encoding: err = nil, want a loud error")
	}
	if !strings.Contains(err.Error(), "encoding") {
		t.Errorf("error %q does not mention the encoding", err)
	}
}
