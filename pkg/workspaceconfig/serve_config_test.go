// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"errors"
	"testing"
)

// TestLoadServeRenderDiagnosticsTrue confirms the serve block parses and
// its render_diagnostics key flows into the typed Config.
func TestLoadServeRenderDiagnosticsTrue(t *testing.T) {
	t.Parallel()
	content := `
serve:
  render_diagnostics: true
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Serve.RenderDiagnostics {
		t.Errorf("Serve.RenderDiagnostics: got false, want true")
	}
}

// TestLoadServeAbsentDefaultsFalse confirms a config with no serve block
// loads and leaves render_diagnostics at its documented default (false).
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
	if cfg.Serve.RenderDiagnostics {
		t.Errorf("Serve.RenderDiagnostics: got true, want false (default)")
	}
}

// TestLoadServeUnknownSubKeyFailsValidation confirms an unknown key under
// the serve block is rejected by schema validation (additionalProperties
// is false on the serve object).
func TestLoadServeUnknownSubKeyFailsValidation(t *testing.T) {
	t.Parallel()
	content := `
serve:
  render_diagnostics: true
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
