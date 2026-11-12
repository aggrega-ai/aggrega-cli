// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// TestLoadServeLintOnValidateTrue confirms the serve block's
// lint_on_validate key is declared by the schema the loader validates every
// config file against, and that it flows into the typed Config.
//
// The schema half is the load-bearing half. The serve object forbids
// additional properties, so a Go field with no declared property would leave
// the key a load failure however correctly the struct tag were written — the
// value would never reach the field. Loading a config that sets it is the only
// assertion that covers both.
func TestLoadServeLintOnValidateTrue(t *testing.T) {
	t.Parallel()
	content := `
serve:
  lint_on_validate: true
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Serve.LintOnValidate {
		t.Errorf("Serve.LintOnValidate: got false, want true")
	}
}

// TestLoadServeLintOnValidateDefaultsFalse pins the default the dial's whole
// design rests on: an author who never asked for publishing-conventions advice
// does not receive it.
//
// It is asserted over a config that sets the sibling key, not over an absent
// serve block, so the assertion cannot be satisfied by the block being ignored
// wholesale. A block that parsed and left this key alone is what the default
// actually has to survive.
func TestLoadServeLintOnValidateDefaultsFalse(t *testing.T) {
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
		t.Fatal("precondition: the serve block did not parse, so the default below would prove nothing")
	}
	if cfg.Serve.LintOnValidate {
		t.Errorf("Serve.LintOnValidate: got true, want false (the documented default)")
	}
}

// TestLoadServeBothDialsTogether confirms the two dials are independent keys
// rather than one setting reachable under two names: a config setting both
// loads, and each field carries its own value.
func TestLoadServeBothDialsTogether(t *testing.T) {
	t.Parallel()
	content := `
serve:
  build_on_inspect: false
  lint_on_validate: true
`
	path := writeFixture(t, content)
	cfg, err := load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Serve.BuildOnInspect {
		t.Error("Serve.BuildOnInspect: got true, want false")
	}
	if !cfg.Serve.LintOnValidate {
		t.Error("Serve.LintOnValidate: got false, want true")
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
	if !errors.Is(err, ErrConfigInvalid()) {
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
	if !errors.Is(err, ErrConfigInvalid()) {
		t.Errorf("error should wrap ErrConfigInvalid; got %v", err)
	}
}
