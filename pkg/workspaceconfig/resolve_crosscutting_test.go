// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestResolveCapabilityOnCrossCuttingSocketHalts proves that binding a
// cross-cutting socket by `capability:` — instead of the `uri:` those sockets
// require — halts resolution loudly. The attached plugin serves no capability,
// so a capability lookup would match nothing; catching it at load turns a late,
// opaque resolve miss into a legible diagnostic that names the socket and the
// offending capability. Each of the four sockets is covered so every branch of
// the fixed-order walk is exercised.
func TestResolveCapabilityOnCrossCuttingSocketHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		socket string
		yaml   string
	}{
		{"mutator", "workspace: {}\nplugins:\n  mutator:\n    - capability: helm\n"},
		{"policy", "workspace: {}\nplugins:\n  policy:\n    - capability: helm\n"},
		{"linter", "workspace: {}\nplugins:\n  linter:\n    - capability: compose\n"},
		{"importer", "workspace: {}\nplugins:\n  importer:\n    - capability: compose\n"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.socket, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			write(t, filepath.Join(dir, ".aggrega-config.yaml"), tc.yaml)

			_, err := workspaceconfig.Resolve(dir, "")
			if err == nil {
				t.Fatalf("Resolve over a capability-bound %s socket returned nil; expected a loud halt", tc.socket)
			}
			if !errors.Is(err, workspaceconfig.ErrCapabilityOnCrossCuttingSocket) {
				t.Fatalf("halt must be ErrCapabilityOnCrossCuttingSocket; got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.socket) {
				t.Errorf("error must name the offending socket %q; got: %v", tc.socket, err)
			}
			cap := "helm"
			if tc.socket == "linter" || tc.socket == "importer" {
				cap = "compose"
			}
			if !strings.Contains(err.Error(), cap) {
				t.Errorf("error must name the offending capability %q; got: %v", cap, err)
			}
		})
	}
}

// TestResolveCapabilityOnCrossCuttingSocketViaAppendHalts proves the check runs
// on the FULLY composed list: a capability binding contributed through an
// `_append` layer at a nearer scope is caught just as a base-list binding is.
func TestResolveCapabilityOnCrossCuttingSocketViaAppendHalts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  policy:\n    - uri: ./base-policy.wasm\n")
	child := filepath.Join(root, "svc")
	write(t, filepath.Join(child, ".aggrega-config.yaml"),
		"plugins:\n  policy_append:\n    - capability: helm\n")

	_, err := workspaceconfig.Resolve(child, "")
	if err == nil {
		t.Fatal("Resolve over an append-contributed capability binding returned nil; expected a loud halt")
	}
	if !errors.Is(err, workspaceconfig.ErrCapabilityOnCrossCuttingSocket) {
		t.Fatalf("append-contributed halt must be ErrCapabilityOnCrossCuttingSocket; got: %v", err)
	}
	if !strings.Contains(err.Error(), "policy") || !strings.Contains(err.Error(), "helm") {
		t.Errorf("error must name the socket and capability; got: %v", err)
	}
}

// TestResolveUriAttachedCrossCuttingSocketResolves proves the valid path is
// untouched: a cross-cutting socket attached by `uri:` (local path or oci://)
// resolves cleanly.
func TestResolveUriAttachedCrossCuttingSocketResolves(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  mutator:\n    - uri: ./m.wasm\n  importer:\n    - uri: oci://h/importer:1\n")

	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("Resolve over uri-attached cross-cutting sockets: %v", err)
	}
	if got := len(rw.Config().Plugins.Mutator); got != 1 {
		t.Errorf("mutator list: got %d, want 1", got)
	}
	if got := len(rw.Config().Plugins.Importer); got != 1 {
		t.Errorf("importer list: got %d, want 1", got)
	}
}

// TestResolveCapabilityOnProviderSocketResolves proves the legitimate
// resolve-by-capability path is not caught by the cross-cutting check: a
// provider binding names a capability by design, and it resolves cleanly.
func TestResolveCapabilityOnProviderSocketResolves(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n")

	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("Resolve over a capability-bound provider socket: %v", err)
	}
	if got := rw.Config().Plugins.ProviderTargetURIs()["helm"]; got != "oci://h/helm:1" {
		t.Errorf("provider capability binding must survive: got %q", got)
	}
}
