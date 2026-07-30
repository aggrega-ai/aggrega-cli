// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"testing"
)

// TestLoadServeBuildOnInspectTrue confirms the serve block parses and
// its build_on_inspect key flows into the typed Config.
func TestLoadServeBuildOnInspectTrue(t *testing.T) {
	t.Parallel()
	content := `
serve:
  build_on_inspect: true
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Serve.BuildOnInspect {
		t.Errorf("Serve.BuildOnInspect: got false, want true")
	}
}

// TestLoadServeAbsentDefaultsFalse confirms a config with no serve block
// loads and leaves build_on_inspect at its documented default (false).
func TestLoadServeAbsentDefaultsFalse(t *testing.T) {
	t.Parallel()
	content := `
default_registry: my-registry.example.com
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Serve.BuildOnInspect {
		t.Errorf("Serve.BuildOnInspect: got true, want false (default)")
	}
}

// TestLoadServeUnknownSubKeyFailsValidation confirms an unknown key under
// the serve block is rejected by schema validation (additionalProperties
// is false on the serve object).
func TestLoadServeUnknownSubKeyFailsValidation(t *testing.T) {
	t.Parallel()
	content := `
serve:
  build_on_inspect: true
  no_such_key: 1
`
	path := writeFixture(t, content)
	_, err := load(path)
	if err == nil {
		t.Fatal("expected validation error for unknown serve sub-key; got nil")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
}

// TestLoadServeSupersededKeyFailsValidation pins the refusal of the key the
// dial no longer answers to. The dial carries exactly one name, so the former
// spelling must be rejected outright rather than silently ignored — a config
// still carrying it would otherwise load and leave the dial at its default,
// which reads as the setting having been honored.
func TestLoadServeSupersededKeyFailsValidation(t *testing.T) {
	t.Parallel()
	content := `
serve:
  render_diagnostics: true
`
	path := writeFixture(t, content)
	_, err := load(path)
	if err == nil {
		t.Fatal("expected validation error for the superseded serve key; got nil")
	}
	if !errors.Is(err, ErrConfigInvalid) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
}
