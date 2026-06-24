// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestResolveOverrideAbsolute: an absolute --plugin-style override is used
// verbatim.
func TestResolveOverrideAbsolute(t *testing.T) {
	got, err := Resolve("", "/abs/path/to/plugin.wasm")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "/abs/path/to/plugin.wasm" {
		t.Errorf("got %q; want /abs/path/to/plugin.wasm", got)
	}
}

// TestResolveOverrideRelative: a relative override is made absolute against the
// working directory and wins over any uri.
func TestResolveOverrideRelative(t *testing.T) {
	got, err := Resolve("ignored-uri.wasm", "rel/plugin.wasm")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("relative override not made absolute: %q", got)
	}
	if !strings.HasSuffix(got, filepath.Join("rel", "plugin.wasm")) {
		t.Errorf("got %q; want a path ending in rel/plugin.wasm", got)
	}
}

// TestResolveLocalURI: a local (non-OCI) URI is returned verbatim.
func TestResolveLocalURI(t *testing.T) {
	got, err := Resolve("/ws/plugins/linter.wasm", "")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got != "/ws/plugins/linter.wasm" {
		t.Errorf("got %q; want /ws/plugins/linter.wasm", got)
	}
}

// TestResolveRejectsOCIURI: an OCI URI is rejected with the load-bearing
// not-yet-wired substring.
func TestResolveRejectsOCIURI(t *testing.T) {
	_, err := Resolve("oci://hub.aggrega.ai/wasm/linter/aggrega-publishing:1.0.0", "")
	if err == nil {
		t.Fatal("expected error for OCI URI; got nil")
	}
	if !strings.Contains(err.Error(), "OCI plugin URI resolution not yet wired") {
		t.Errorf("error should describe OCI-URI deferral; got: %v", err)
	}
}

// TestResolveUnset: neither override nor uri yields the load-bearing
// no-plugin-path-configured substring.
func TestResolveUnset(t *testing.T) {
	_, err := Resolve("", "")
	if err == nil {
		t.Fatal("expected error for unset plugin path; got nil")
	}
	if !strings.Contains(err.Error(), "no plugin path configured") {
		t.Errorf("error should describe unset state; got: %v", err)
	}
}
