// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"testing"
)

// TestLazyLookup_ResolvesSchemaLibrariesViaWalk proves daemon/CLI IR parity for
// cross-library schema defaults. The daemon resolves Components/Features through
// the lazy lookup; cross-library schema `$ref` resolution needs the whole
// SchemaLibrary set, which the lazy backing now resolves ITSELF by walking its
// catalog directories — it no longer depends on being Prime-fed an eager Catalog's
// libraries. AsCatalogLookup must expose those walked libraries so the daemon's IR
// materializes cross-library defaults identically to the CLI. Without this, the
// daemon's /ir would serve content-divergent IR for cross-library Components.
func TestLazyLookup_ResolvesSchemaLibrariesViaWalk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFixture(t, dir, "library.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseComponents
  version: 1.0.0
spec:
  schemas:
    Storage:
      size: { type: string, default: "10Gi" }
`)

	// No Prime — the lazy backing resolves libraries through its own catalog walk.
	lazy := NewLazyLookup("", "", []string{dir}, nil)

	cl := lazy.AsCatalogLookup()
	lib, ok := cl.SchemaLibraries["BaseComponents"]
	if !ok {
		t.Fatalf("lazy lookup must resolve SchemaLibraries via its own catalog walk " +
			"(daemon IR would otherwise omit cross-library defaults)")
	}
	if lib.Metadata.Name != "BaseComponents" {
		t.Errorf("library Metadata.Name = %q, want BaseComponents", lib.Metadata.Name)
	}
}
