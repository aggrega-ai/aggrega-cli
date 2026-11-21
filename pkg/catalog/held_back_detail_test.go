// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// heldBackComponentYAML is a loadable Component core/compute/server at version
// whose infra pillar declares image and each property in infra.
func heldBackComponentYAML(version string, infra ...string) string {
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Component\nmetadata:\n  name: core/compute/server\n  version: \"" +
		version + "\"\n  labels:\n    component.aggrega.ai/type: server\nspec:\n  schema:\n    infra:\n      image: { type: string }\n")
	for _, property := range infra {
		b.WriteString("      " + property + ": { type: integer }\n")
	}
	return b.String()
}

// TestHeldBackDetailNamesNoPrerelease holds the version the held-back detail names
// as declaring a property to the version selection's rule: the highest release
// among the versions the instance's constraints exclude, never a pre-release, which
// a reference naming no version never selects. The server is held at 1.0.0, which
// the Manifest key admits alone, at 1.1.0 and at 1.2.0-rc.1.
func TestHeldBackDetailNamesNoPrerelease(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeCatalogEntity(t, dir, "component/core/compute/server/1.0.0/component.yaml", heldBackComponentYAML("1.0.0"))
	writeCatalogEntity(t, dir, "component/core/compute/server/1.1.0/component.yaml",
		heldBackComponentYAML("1.1.0", "published_port"))
	writeCatalogEntity(t, dir, "component/core/compute/server/1.2.0-rc.1/component.yaml",
		heldBackComponentYAML("1.2.0-rc.1", "published_port", "debug_port"))
	writeCatalogEntity(t, dir, "recipe/compose/server/1.0.0/recipe.yaml",
		instanceVersionsRecipeYAML("server", "1.0.0", "core/compute/server:1.0.0"))
	manifest := instanceVersionsManifest([][2]string{{"core/compute/server:1.0.0", "compose/server:1.0.0"}}, nil)
	instances := []closure.InstanceReference{componentInstance("web", "core/compute/server")}

	eager, err := LoadAll("", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	lazy := NewLazyLookup(dir, "", []string{dir}, nil)
	for _, v := range []struct {
		name   string
		view   InstanceVersionView
		lookup resolver.CatalogLookup
	}{
		{name: "the eager catalog", view: eager, lookup: eager.CatalogLookup()},
		{name: "the lazy lookup", view: lazy, lookup: lazy.AsCatalogLookup()},
	} {
		t.Run(v.name, func(t *testing.T) {
			table, err := ResolveInstanceVersions(instanceVersionsRelease, instances, manifest, instanceVersionsManifestFile, v.view)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got, _ := selectedVersion(table, "core/compute/server"); got != "1.0.0" {
				t.Fatalf("selected %q, want 1.0.0", got)
			}
			detail := WithHeldBackDetail(ScopeToInstanceVersions(v.lookup, table), v.lookup, table, v.view).HeldBackDetail

			notes := detail("core/compute/server", "infra", "infra.published_port")
			if declaredBy := noteValue(notes, "declared by"); declaredBy != "core/compute/server:1.1.0" {
				t.Errorf("a property 1.1.0 and 1.2.0-rc.1 declare is declared by %q, want the release 1.1.0: %+v", declaredBy, notes)
			}
			if notes := detail("core/compute/server", "infra", "infra.debug_port"); len(notes) != 0 {
				t.Errorf("a property only a pre-release declares carries a held-back detail: %+v", notes)
			}
		})
	}
}

// noteValue returns the value of the note captioned caption, or "" for none.
func noteValue(notes []constraint.HeldBackNote, caption string) string {
	for _, n := range notes {
		if n.Caption == caption {
			return n.Value
		}
	}
	return ""
}
