// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// newShapePlugin is a minimal schema-conforming Plugin entity in the current
// shape: a bare metadata.name plus a metadata.pluginType scope, with abiVersion
// the sole required spec field. The .wasm location and the catalog coordinate
// are derived from (pluginType, name), not carried in the spec.
func newShapePlugin(name, pluginType string) string {
	return `apiVersion: aggrega.ai/v1beta1
kind: Plugin
metadata:
  name: ` + name + `
  version: 1.0.0
  pluginType: ` + pluginType + `
spec:
  abiVersion: v1beta1
`
}

// TestPluginCoordinateDistinguishesSockets pins the plugin-identity invariant:
// two plugins that share a bare metadata.name but sit in different sockets
// (distinct metadata.pluginType) are DISTINCT plugins. They must receive
// distinct catalog index keys (so one does not shadow the other in the Plugins
// map) and distinct OCI locations (so one does not overwrite the other at
// publish), both derived from the (pluginType, name) coordinate.
func TestPluginCoordinateDistinguishesSockets(t *testing.T) {
	t.Parallel()

	// Coordinate + location derivation are the two single-source rules the
	// distinction rests on; assert each keeps the sockets apart before proving the
	// catalog honors them.
	if a, b := types.PluginCoordinate("provider", "helm"), types.PluginCoordinate("linter", "helm"); a == b {
		t.Fatalf("PluginCoordinate must distinguish sockets: provider=%q linter=%q", a, b)
	}
	if got := types.PluginCoordinate("provider", "helm"); got != "provider/helm" {
		t.Fatalf("PluginCoordinate(provider, helm) = %q; want %q", got, "provider/helm")
	}
	if a, b := OCILocation("Plugin", "provider", "helm"), OCILocation("Plugin", "linter", "helm"); a == b {
		t.Fatalf("OCILocation must distinguish plugin sockets: provider=%q linter=%q", a, b)
	}
	if got := OCILocation("Plugin", "provider", "helm"); got != "plugin/provider/helm" {
		t.Fatalf("OCILocation(Plugin, provider, helm) = %q; want %q", got, "plugin/provider/helm")
	}

	// A workspace holding both same-named plugins: the catalog must index two
	// distinct entries and resolve each by its coordinate URI to the right socket.
	dir := t.TempDir()
	writeFixture(t, dir, "provider-helm.yaml", newShapePlugin("helm", "provider"))
	writeFixture(t, dir, "linter-helm.yaml", newShapePlugin("helm", "linter"))

	cat, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if got := len(cat.Plugins); got != 2 {
		t.Fatalf("Plugins: want 2 distinct entries for same-named plugins in different sockets, got %d (a shared index key shadowed one)", got)
	}

	prov, ok := cat.PluginByURI("provider/helm")
	if !ok {
		t.Fatalf("PluginByURI(provider/helm): not found")
	}
	if prov.Metadata.PluginType != "provider" {
		t.Errorf("provider/helm resolved to pluginType %q; want provider", prov.Metadata.PluginType)
	}

	lint, ok := cat.PluginByURI("linter/helm")
	if !ok {
		t.Fatalf("PluginByURI(linter/helm): not found")
	}
	if lint.Metadata.PluginType != "linter" {
		t.Errorf("linter/helm resolved to pluginType %q; want linter", lint.Metadata.PluginType)
	}

	// Both carry the same bare name; only the socket tells them apart.
	if prov.Metadata.Name != "helm" || lint.Metadata.Name != "helm" {
		t.Fatalf("both plugins must keep the bare name %q; got provider=%q linter=%q", "helm", prov.Metadata.Name, lint.Metadata.Name)
	}
}
