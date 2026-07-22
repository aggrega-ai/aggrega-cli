// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/plugintype"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// TestResolveCapabilityOnCrossCuttingSocketHalts proves that binding a
// cross-cutting socket by `capability:` — instead of the `uri:` those sockets
// require — halts resolution loudly. The attached plugin serves no capability,
// so a capability lookup would match nothing; catching it at load turns a late,
// opaque resolve miss into a legible diagnostic that names the socket and the
// offending capability. Each of the three sockets is covered so every branch of
// the fixed-order walk is exercised.
func TestResolveCapabilityOnCrossCuttingSocketHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		socket string
		yaml   string
	}{
		{"governance", "workspace: {}\nplugins:\n  governance:\n    - capability: helm\n"},
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
		"workspace: {}\nplugins:\n  governance:\n    - uri: ./base-governance.wasm\n      role: ir-verdict\n")
	child := filepath.Join(root, "svc")
	write(t, filepath.Join(child, ".aggrega-config.yaml"),
		"plugins:\n  governance_append:\n    - capability: helm\n")

	_, err := workspaceconfig.Resolve(child, "")
	if err == nil {
		t.Fatal("Resolve over an append-contributed capability binding returned nil; expected a loud halt")
	}
	if !errors.Is(err, workspaceconfig.ErrCapabilityOnCrossCuttingSocket) {
		t.Fatalf("append-contributed halt must be ErrCapabilityOnCrossCuttingSocket; got: %v", err)
	}
	if !strings.Contains(err.Error(), "governance") || !strings.Contains(err.Error(), "helm") {
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
		"workspace: {}\nplugins:\n  governance:\n    - uri: ./m.wasm\n      role: label-transform\n  importer:\n    - uri: oci://h/importer:1\n")

	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("Resolve over uri-attached cross-cutting sockets: %v", err)
	}
	if got := len(rw.Config().Plugins.Governance); got != 1 {
		t.Errorf("governance list: got %d, want 1", got)
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

// TestResolveInertFieldOnProviderSocketHalts proves that setting a field with no
// referent on a provider binding — `function:` or `with:` — halts resolution
// loudly rather than silently dropping the value. A provider drives a fixed set
// of phase-keyed exports (so `function:` names nothing) and takes its render
// configuration from the Recipe (so `with:` misplaces render config), so both
// fields are inert-by-design and rejected at authoring. Each field is covered so
// both branches of the per-binding walk are exercised.
func TestResolveInertFieldOnProviderSocketHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		field string
		yaml  string
	}{
		{"function", "workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n      function: custom_render\n"},
		{"with", "workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n      with:\n        settings:\n          foo: bar\n"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.field, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			write(t, filepath.Join(dir, ".aggrega-config.yaml"), tc.yaml)

			_, err := workspaceconfig.Resolve(dir, "")
			if err == nil {
				t.Fatalf("Resolve over a provider binding carrying %s returned nil; expected a loud halt", tc.field)
			}
			if !errors.Is(err, workspaceconfig.ErrInertFieldOnProviderSocket) {
				t.Fatalf("halt must be ErrInertFieldOnProviderSocket; got: %v", err)
			}
			if !strings.Contains(err.Error(), "provider") {
				t.Errorf("error must name the provider socket; got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("error must name the offending field %q; got: %v", tc.field, err)
			}
			if !strings.Contains(err.Error(), "helm") {
				t.Errorf("error must name the offending capability; got: %v", err)
			}
		})
	}
}

// TestResolveInertFieldOnProviderSocketViaAppendHalts proves the check runs on
// the FULLY composed provider list: an inert field contributed through a
// `providers_append` layer at a nearer scope is caught just as a base-list
// binding is.
func TestResolveInertFieldOnProviderSocketViaAppendHalts(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write(t, filepath.Join(root, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n")
	child := filepath.Join(root, "svc")
	write(t, filepath.Join(child, ".aggrega-config.yaml"),
		"plugins:\n  providers_append:\n    - capability: compose\n      uri: oci://h/compose:1\n      function: custom_render\n")

	_, err := workspaceconfig.Resolve(child, "")
	if err == nil {
		t.Fatal("Resolve over an append-contributed inert provider field returned nil; expected a loud halt")
	}
	if !errors.Is(err, workspaceconfig.ErrInertFieldOnProviderSocket) {
		t.Fatalf("append-contributed halt must be ErrInertFieldOnProviderSocket; got: %v", err)
	}
	if !strings.Contains(err.Error(), "function") || !strings.Contains(err.Error(), "compose") {
		t.Errorf("error must name the offending field and capability; got: %v", err)
	}
}

// TestResolveFunctionAndWithOnConsumingSocketsResolve proves non-regression: the
// sockets that DO consume `function:` and `with:` (governance, linter) are
// unaffected by the provider-socket inert-field rejection. A provider drives a
// fixed export set so those fields are inert on it, but the cross-cutting
// sockets have real referents for both, so a binding carrying them resolves
// cleanly.
func TestResolveFunctionAndWithOnConsumingSocketsResolve(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n"+
			"  governance:\n"+
			"    - uri: oci://h/mut:1\n      role: label-transform\n      function: custom_mutate\n"+
			"    - uri: ./pol.wasm\n      role: ir-verdict\n      with:\n        settings:\n          strict: true\n"+
			"  linter:\n    - uri: oci://h/lint:1\n      function: custom_lint\n")

	rw, err := workspaceconfig.Resolve(dir, "")
	if err != nil {
		t.Fatalf("Resolve over consuming sockets carrying function/with: %v", err)
	}
	if got := rw.Config().Plugins.GovernanceRefsForRole(plugintype.RoleLabelTransform)[0].Function; got != "custom_mutate" {
		t.Errorf("label-transform function must survive: got %q", got)
	}
	if rw.Config().Plugins.GovernanceRefsForRole(plugintype.RoleIRVerdict)[0].With == nil {
		t.Error("verdict with must survive: got nil")
	}
	if got := rw.Config().Plugins.Linter[0].Function; got != "custom_lint" {
		t.Errorf("linter function must survive: got %q", got)
	}
}
