// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helmcore_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// gizmoDefinition is a CustomResourceDefinition declaring the custom kind Gizmo in
// the API group example.com at scope.
func gizmoDefinition(name, group, scope string) string {
	return `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: ` + name + `
spec:
  group: ` + group + `
  names:
    kind: Gizmo
    plural: gizmos
  scope: ` + scope + `
  versions:
    - name: v1
      served: true
      storage: true
`
}

// gizmo is a resource of the custom kind Gizmo writing no namespace.
const gizmo = "apiVersion: example.com/v1\nkind: Gizmo\nmetadata:\n  name: shared\n"

// TestDeclaredResourceNames_AClusterScopedKindIsDeclaredWithoutANamespace is the
// shape of the declaration for a kind the Kubernetes API keeps outside every
// namespace: the space carries the kind alone, whatever namespace the document
// writes and whatever the node falls back to. One ClusterRole written under no
// namespace and under team-a is one name twice in one space, and the
// CustomResourceDefinition, itself cluster-scoped, is declared the same way.
func TestDeclaredResourceNames_AClusterScopedKindIsDeclaredWithoutANamespace(t *testing.T) {
	t.Parallel()

	files := []rendercore.RenderedFile{
		file("a.yaml", "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: reader\n"),
		file("b.yaml", "apiVersion: rbac.authorization.k8s.io/v1\nkind: ClusterRole\nmetadata:\n  name: reader\n  namespace: team-a\n"),
		file("c.yaml", gizmoDefinition("gizmos.example.com", "example.com", "Namespaced")),
	}
	for _, fallback := range []string{"shop", "other", "default"} {
		declared := helmcore.DeclaredResourceNames(files, fallback)
		wantSpaces := []string{"resource-names:ClusterRole", "resource-names:CustomResourceDefinition"}
		if !reflect.DeepEqual(declared.Keys(), wantSpaces) {
			t.Fatalf("under fallback %q the spaces are %v, want %v", fallback, declared.Keys(), wantSpaces)
		}
		if names, _ := declared.Get("resource-names:ClusterRole"); !reflect.DeepEqual(names, []string{"reader", "reader"}) {
			t.Errorf("under fallback %q the ClusterRole space carries %v, want the one name twice", fallback, names)
		}
		if names, _ := declared.Get("resource-names:CustomResourceDefinition"); !reflect.DeepEqual(names, []string{"gizmos.example.com"}) {
			t.Errorf("under fallback %q the definition space carries %v", fallback, names)
		}
	}
}

// TestDeclaredResourceNames_ACustomKindTakesTheScopeItsDefinitionDeclares covers
// every way a custom kind is classified by what the same render emits. Only a
// definition naming the kind's own group and declaring Cluster takes the namespace
// out of its space; a namespaced definition, two definitions that disagree, no
// definition, a definition for another group, and a resource naming no apiVersion
// all keep the kind and the namespace.
func TestDeclaredResourceNames_ACustomKindTakesTheScopeItsDefinitionDeclares(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		files    []rendercore.RenderedFile
		wantInto string
	}{
		{
			name: "a definition declaring Cluster",
			files: []rendercore.RenderedFile{
				file("crd.yaml", gizmoDefinition("gizmos.example.com", "example.com", "Cluster")), file("g.yaml", gizmo),
			},
			wantInto: "resource-names:Gizmo",
		},
		{
			name: "the definition emitted after the resource",
			files: []rendercore.RenderedFile{
				file("g.yaml", gizmo), file("z-crd.yaml", gizmoDefinition("gizmos.example.com", "example.com", "Cluster")),
			},
			wantInto: "resource-names:Gizmo",
		},
		{
			name: "a definition declaring Namespaced",
			files: []rendercore.RenderedFile{
				file("crd.yaml", gizmoDefinition("gizmos.example.com", "example.com", "Namespaced")), file("g.yaml", gizmo),
			},
			wantInto: "resource-names:Gizmo--shop",
		},
		{
			name: "two definitions that disagree",
			files: []rendercore.RenderedFile{
				file("a-crd.yaml", gizmoDefinition("gizmos.example.com", "example.com", "Cluster")),
				file("b-crd.yaml", gizmoDefinition("more-gizmos.example.com", "example.com", "Namespaced")),
				file("g.yaml", gizmo),
			},
			wantInto: "resource-names:Gizmo--shop",
		},
		{
			name:     "no definition in the render",
			files:    []rendercore.RenderedFile{file("g.yaml", gizmo)},
			wantInto: "resource-names:Gizmo--shop",
		},
		{
			name: "a definition of the kind in another API group",
			files: []rendercore.RenderedFile{
				file("crd.yaml", gizmoDefinition("gizmos.example.org", "example.org", "Cluster")), file("g.yaml", gizmo),
			},
			wantInto: "resource-names:Gizmo--shop",
		},
		{
			name: "a resource naming no apiVersion",
			files: []rendercore.RenderedFile{
				file("crd.yaml", gizmoDefinition("gizmos.example.com", "example.com", "Cluster")),
				file("g.yaml", "kind: Gizmo\nmetadata:\n  name: shared\n"),
			},
			wantInto: "resource-names:Gizmo--shop",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			declared := helmcore.DeclaredResourceNames(tc.files, "shop")
			names, ok := declared.Get(tc.wantInto)
			if !ok || !reflect.DeepEqual(names, []string{"shared"}) {
				t.Errorf("the Gizmo is not declared in %q alone; the declaration is %v", tc.wantInto, spaces(declared.Keys(), declared.Get))
			}
		})
	}
}

// TestDeclaredResourceNames_AClusterScopedSpaceMeetsNoNamespacedSpace is the
// property the spelling of a cluster-scoped space rests on: written as the kind
// alone, it is a space no namespaced resource of that kind reaches, whatever its
// namespace — including one spelled like the encoding's own marks, and an empty one.
func TestDeclaredResourceNames_AClusterScopedSpaceMeetsNoNamespacedSpace(t *testing.T) {
	t.Parallel()

	const clusterSpace = "resource-names:Gizmo"
	cluster := helmcore.DeclaredResourceNames([]rendercore.RenderedFile{
		file("crd.yaml", gizmoDefinition("gizmos.example.com", "example.com", "Cluster")), file("g.yaml", gizmo),
	}, "shop")
	if names, ok := cluster.Get(clusterSpace); !ok || !reflect.DeepEqual(names, []string{"shared"}) {
		t.Fatalf("the cluster-scoped Gizmo is not declared in %q alone: %v", clusterSpace, spaces(cluster.Keys(), cluster.Get))
	}

	for _, ns := range []string{"", "-", "--", "z", "ze", "-ze", "Gizmo", "default", "shop"} {
		// The document writes no namespace and falls back to ns, so an empty ns
		// reaches the space the encoding gives an absent segment.
		namespaced := helmcore.DeclaredResourceNames([]rendercore.RenderedFile{
			file("crd.yaml", gizmoDefinition("gizmos.example.com", "example.com", "Namespaced")), file("g.yaml", gizmo),
		}, ns)
		declaredGizmo := false
		for _, space := range namespaced.Keys() {
			if space == clusterSpace {
				t.Errorf("a namespaced Gizmo under namespace %q reached the cluster-scoped space %q", ns, space)
			}
			if strings.HasPrefix(space, clusterSpace+"--") {
				declaredGizmo = true
			}
		}
		// The positive control: the namespaced Gizmo was declared, so the absence
		// above is about the space it reached and not about a name never declared.
		if !declaredGizmo {
			t.Errorf("under namespace %q the namespaced Gizmo was not declared at all: %v", ns, spaces(namespaced.Keys(), namespaced.Get))
		}
	}
}

// TestKindScopes_OneDefinitionMovesThePlacementAndTheNamesTogether renders one
// chart through the render the provider guest calls, its kinds classified by one
// test-only CustomResourceDefinition, and reads both readers of that
// classification: the namespace placement, over the emitted files, and the names
// declaration, over the same files. Declaring the custom kind Cluster leaves its
// resource without the declared namespace AND declares its name without one;
// declaring it Namespaced moves both back. That is what one classification looks
// like from the outside: no definition can move one reader and not the other.
func TestKindScopes_OneDefinitionMovesThePlacementAndTheNamesTogether(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		scope     string
		file      string
		placed    bool
		wantSpace string
	}{
		{scope: "Cluster", file: "api-gizmo-shared.yaml", placed: false, wantSpace: "resource-names:Gizmo"},
		{scope: "Namespaced", file: "api-gizmo-shop-shared.yaml", placed: true, wantSpace: "resource-names:Gizmo--shop"},
	} {
		t.Run(tc.scope, func(t *testing.T) {
			t.Parallel()
			rendered := renderPlaced(t, map[string]string{
				"Chart.yaml":            placementChart["Chart.yaml"],
				"templates/gizmo.yaml":  gizmo,
				"templates/z-crds.yaml": gizmoDefinition("gizmos.example.com", "example.com", tc.scope),
			}, "shop")

			e, ok := emittedNamespaces(t, rendered.Files)["Gizmo/shared"]
			if !ok {
				t.Fatalf("the Gizmo was not emitted: %v", sortedFiles(rendered.Files))
			}
			if e.file != tc.file || e.present != tc.placed || (tc.placed && e.value != "shop") {
				t.Errorf("the placement emits the Gizmo %+v; want file %s, placed in shop = %v", e, tc.file, tc.placed)
			}

			paths := sortedFiles(rendered.Files)
			files := make([]rendercore.RenderedFile, 0, len(paths))
			for _, p := range paths {
				files = append(files, rendercore.RenderedFile{Path: p, Content: rendered.Files[p]})
			}
			declared, _, err := helmcore.ReportEmitted(files, "shop")
			if err != nil {
				t.Fatalf("ReportEmitted: %v", err)
			}
			if names, ok := declared.Get(tc.wantSpace); !ok || !reflect.DeepEqual(names, []string{"shared"}) {
				t.Errorf("the names declaration does not put the Gizmo in %q alone; it declares %v",
					tc.wantSpace, spaces(declared.Keys(), declared.Get))
			}
			if len(rendered.Advisories) != 0 {
				t.Errorf("a render whose every kind is classified raised advisories: %v", rendered.Advisories)
			}
		})
	}
}

// TestKindScopes_TheNamesAreClassifiedWhereNoNamespaceIsDeclared renders a chart
// whose node declares no namespace, where the placement places nothing, and reads
// the names: a ClusterRole is still declared without a namespace and a Service in
// the cluster default, because the classification takes no namespace and is read
// whether or not one is declared.
func TestKindScopes_TheNamesAreClassifiedWhereNoNamespaceIsDeclared(t *testing.T) {
	t.Parallel()

	rendered := renderPlaced(t, placementChart, "")
	paths := sortedFiles(rendered.Files)
	files := make([]rendercore.RenderedFile, 0, len(paths))
	for _, p := range paths {
		files = append(files, rendercore.RenderedFile{Path: p, Content: rendered.Files[p]})
	}
	declared, _, err := helmcore.ReportEmitted(files, "default")
	if err != nil {
		t.Fatalf("ReportEmitted: %v", err)
	}
	for space, want := range map[string][]string{
		"resource-names:ClusterRole":      {"reader"},
		"resource-names:StorageClass":     {"fast"},
		"resource-names:Service--default": {"web"},
	} {
		if names, _ := declared.Get(space); !reflect.DeepEqual(names, want) {
			t.Errorf("space %q carries %v, want %v; the declaration is %v", space, names, want, spaces(declared.Keys(), declared.Get))
		}
	}
}

// spaces renders a declaration for a failure message, space by space in its own
// order.
func spaces(keys []string, get func(string) ([]string, bool)) []string {
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		names, _ := get(k)
		out = append(out, k+"="+strings.Join(names, ","))
	}
	return out
}
