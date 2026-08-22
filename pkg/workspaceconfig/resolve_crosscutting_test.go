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
			if !errors.Is(err, workspaceconfig.ErrCapabilityOnCrossCuttingSocket()) {
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
	if !errors.Is(err, workspaceconfig.ErrCapabilityOnCrossCuttingSocket()) {
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

// TestResolveGovernanceRoleMissingHalts proves a governance attachment authored
// with no role halts resolution loudly rather than being silently dropped. The
// role selects which pipeline runner (and facet) an attachment feeds, and the
// dispatch reads attachments by role, so an attachment with no role would attach
// to no runner and vanish without a diagnostic; catching it at load turns that
// silent drop into a legible halt that names the attachment and the valid roles.
// A role is optional on every attachment shape the schema declares (linter,
// importer and provider entries carry none), so its absence on a governance entry
// is caught here rather than by the schema.
func TestResolveGovernanceRoleMissingHalts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  governance:\n    - uri: oci://h/gov:1\n")

	_, err := workspaceconfig.Resolve(dir, "")
	if err == nil {
		t.Fatal("Resolve over a role-less governance attachment returned nil; expected a loud halt")
	}
	if !errors.Is(err, workspaceconfig.ErrGovernanceRoleInvalid()) {
		t.Fatalf("halt must be ErrGovernanceRoleInvalid; got: %v", err)
	}
	if !strings.Contains(err.Error(), "oci://h/gov:1") {
		t.Errorf("error must name the offending attachment; got: %v", err)
	}
	if !strings.Contains(err.Error(), string(plugintype.RoleLabelTransform)) {
		t.Errorf("error must list the valid roles; got: %v", err)
	}
}

// TestResolveGovernanceRoleUnknownHalts proves a governance attachment authored
// with a role outside the known set is rejected loudly at load. An unknown role
// value is not one of the four the attachment surface admits, so the config
// schema's role enum rejects it before dispatch — a typo'd role never reaches a
// runner as a silently ignored attachment.
func TestResolveGovernanceRoleUnknownHalts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write(t, filepath.Join(dir, ".aggrega-config.yaml"),
		"workspace: {}\nplugins:\n  governance:\n    - uri: oci://h/gov:1\n      role: mutate\n")

	_, err := workspaceconfig.Resolve(dir, "")
	if err == nil {
		t.Fatal("Resolve over an unknown governance role returned nil; expected a loud halt")
	}
	if !errors.Is(err, workspaceconfig.ErrConfigInvalid()) {
		t.Fatalf("halt must be ErrConfigInvalid (schema role enum); got: %v", err)
	}
}

// TestResolveInertFieldOnProviderSocketHalts proves that setting a field with no
// referent on a provider binding halts resolution loudly rather than silently
// dropping the value. A provider takes its render configuration from the Recipe,
// so `with:` on a provider binding misplaces render config across the authoring
// boundary and is rejected at authoring.
//
// The table covers `with:` alone, and that is the whole reachable surface of this
// check rather than a narrowing of the assertion. The other inert field on a
// provider binding is `function:`, which the schema does not declare there at
// all, so it is refused before the typed decode and never reaches this check —
// asserted separately, against the class that now pins it, by
// TestResolveFunctionOnFixedEntrySocketRefusedBySchema.
func TestResolveInertFieldOnProviderSocketHalts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		field string
		yaml  string
	}{
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
			if !errors.Is(err, workspaceconfig.ErrInertFieldOnProviderSocket()) {
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
		"plugins:\n  providers_append:\n    - capability: compose\n      uri: oci://h/compose:1\n      with:\n        settings:\n          foo: bar\n")

	_, err := workspaceconfig.Resolve(child, "")
	if err == nil {
		t.Fatal("Resolve over an append-contributed inert provider field returned nil; expected a loud halt")
	}
	if !errors.Is(err, workspaceconfig.ErrInertFieldOnProviderSocket()) {
		t.Fatalf("append-contributed halt must be ErrInertFieldOnProviderSocket; got: %v", err)
	}
	if !strings.Contains(err.Error(), "with") || !strings.Contains(err.Error(), "compose") {
		t.Errorf("error must name the offending field and capability; got: %v", err)
	}
}

// TestResolveFunctionOnFixedEntrySocketRefusedBySchema proves that `function:` is
// not writable at all on a socket whose entry point the socket itself fixes. The
// importer socket calls one entry function its socket contract names, and a
// provider drives a fixed set of phase-keyed exports rather than one selectable
// entry, so neither has an entry for `function:` to select. The schema declares
// no such key on either list, and every config file is validated before its typed
// decode, so the value is refused per file — including a file that contributes
// only an `_append` layer, which is validated in its own right and not merely as
// part of a merged result.
//
// The importer case is the one that changes what a user can ship: an authored
// `function:` there used to be accepted, validated and then dropped, because the
// socket reads the fixed export and never the field. The refusal must name the
// offending key and the position that refused it, or a halt is indistinguishable
// from any other malformed document.
func TestResolveFunctionOnFixedEntrySocketRefusedBySchema(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		base     string
		child    string
		position string
	}{
		{
			name:     "providers",
			base:     "workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n      function: custom_render\n",
			position: "plugins.providers[0].function",
		},
		{
			name:     "providers_append",
			base:     "workspace: {}\nplugins:\n  providers:\n    - capability: helm\n      uri: oci://h/helm:1\n",
			child:    "plugins:\n  providers_append:\n    - capability: compose\n      uri: oci://h/compose:1\n      function: custom_render\n",
			position: "plugins.providers_append[0].function",
		},
		{
			name:     "importer",
			base:     "workspace: {}\nplugins:\n  importer:\n    - uri: oci://h/imp:1\n      function: custom_import\n",
			position: "plugins.importer[0].function",
		},
		{
			name:     "importer_append",
			base:     "workspace: {}\nplugins:\n  importer:\n    - uri: oci://h/imp:1\n",
			child:    "plugins:\n  importer_append:\n    - uri: oci://h/imp2:1\n      function: custom_import\n",
			position: "plugins.importer_append[0].function",
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			write(t, filepath.Join(root, ".aggrega-config.yaml"), tc.base)
			start := root
			if tc.child != "" {
				start = filepath.Join(root, "svc")
				write(t, filepath.Join(start, ".aggrega-config.yaml"), tc.child)
			}

			_, err := workspaceconfig.Resolve(start, "")
			if err == nil {
				t.Fatalf("Resolve over an entry in %s carrying function: returned nil; expected a loud halt", tc.name)
			}
			if !errors.Is(err, workspaceconfig.ErrConfigInvalid()) {
				t.Fatalf("halt must be ErrConfigInvalid (the schema declares no such key here); got: %v", err)
			}
			if !strings.Contains(err.Error(), tc.position) {
				t.Errorf("error must name the refusing position %q; got: %v", tc.position, err)
			}
			if !strings.Contains(err.Error(), "function") {
				t.Errorf("error must name the offending key; got: %v", err)
			}
		})
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
