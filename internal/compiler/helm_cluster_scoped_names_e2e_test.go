// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end coverage of the names a Kubernetes render declares for a resource the
// cluster keeps outside every namespace, and of the halt the build raises when two
// contributors to one output claim one.
//
// A ClusterRole or a CustomResourceDefinition is one object in the cluster whatever
// namespace a document writes, so two contributors to one output that each emit one
// under one name write that object twice — even when the Release places them in two
// namespaces. The helm render declares such a name in a key space of its kind alone,
// so the existing comparison of declared names meets them there. A namespaced kind,
// and a kind nothing in the render classifies, keeps the space of its kind and its
// namespace, so one name held in two namespaces still builds.
//
// Every cell is its own Release over one catalog, and every halting cell is asserted
// against the condition the halt carries rather than against any failure.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// clusterScopedNamesInput compiles one Release of the cluster-scoped-names fixture.
func clusterScopedNamesInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  "testdata/helm-cluster-scoped-names/" + release,
		CatalogPaths: []string{"testdata/helm-cluster-scoped-names/catalog"},
	}
}

// collisionFindings returns the text of every finding a name-collision halt
// carries. A halt over several findings wraps them in a report whose own text names
// only what they share, so they are read through the set it wraps; a halt over one
// finding is that finding, read with every frame of its cause chain.
func collisionFindings(t *testing.T, err error) []string {
	t.Helper()
	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	joined, isMulti := envelope.Unwrap().(interface{ Unwrap() []error })
	if !isMulti {
		return []string{cerrors.CauseChain(err)}
	}
	var texts []string
	for _, f := range joined.Unwrap() {
		texts = append(texts, f.Error())
	}
	return texts
}

// TestClusterScopedNames_OneObjectTwoContributorsOfOneOutputWriteHalts holds every
// cell where one output carries one cluster-wide object twice: two components of
// one boundary placed in two namespaces, a node declaring no namespace beside one
// declaring one, an artifacts body reaching two hosts, two boundaries naming one
// directory, and one chart writing the object under two namespaces. Each halts under the derived-name condition, naming
// the name in the space of its kind alone, every contributor involved, and no space
// that carries a namespace.
//
// The namespaced built-in in one namespace is the control row: it halted before a
// cluster-scoped kind was declared without a namespace, and still halts in the space
// of its kind and namespace.
func TestClusterScopedNames_OneObjectTwoContributorsOfOneOutputWriteHalts(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		release string
		// findings is how many findings the halt carries: one per contributor per
		// colliding name, or one for a contributor repeating a name on its own.
		findings int
		want     []string
		// refused is a key-space prefix no finding may name: the space this name
		// would carry if its namespace were still part of it.
		refused string
	}{
		{
			name:     "a ClusterRole and a CustomResourceDefinition from two components placed in two namespaces",
			release:  "release-two-namespaces.yaml",
			findings: 4,
			want: []string{
				`"shared-reader"`, `key space "resource-names:ClusterRole"`,
				`"widgets.example.com"`, `key space "resource-names:CustomResourceDefinition"`,
				`derived by component "store"`, `derived by component "edge"`,
			},
			refused: `key space "resource-names:ClusterRole--`,
		},
		{
			name:     "a built-in cluster-scoped kind",
			release:  "release-cluster-role.yaml",
			findings: 2,
			want: []string{
				`"shared-reader"`, `key space "resource-names:ClusterRole"`,
				`derived by component "store"`, `derived by component "edge"`,
			},
			refused: `key space "resource-names:ClusterRole--`,
		},
		{
			name:     "the CustomResourceDefinition itself",
			release:  "release-definition.yaml",
			findings: 2,
			want: []string{
				`"widgets.example.com"`, `key space "resource-names:CustomResourceDefinition"`,
				`derived by component "store"`, `derived by component "edge"`,
			},
			refused: `key space "resource-names:CustomResourceDefinition--`,
		},
		{
			name:     "a custom kind a definition in the same render declares cluster-scoped",
			release:  "release-custom-cluster.yaml",
			findings: 2,
			want: []string{
				`"shared-gizmo"`, `key space "resource-names:Gizmo"`,
				`derived by component "store"`, `derived by component "edge"`,
			},
			refused: `key space "resource-names:Gizmo--`,
		},
		{
			name:     "one chart writing a ClusterRole under two namespaces",
			release:  "release-split-role.yaml",
			findings: 1,
			want: []string{
				`"split-reader"`, `key space "resource-names:ClusterRole"`,
				`derived more than once by component "store"`,
			},
			refused: `key space "resource-names:ClusterRole--`,
		},
		{
			name:     "an artifacts body naming a ClusterRole, attached to two hosts in two namespaces",
			release:  "release-artifacts.yaml",
			findings: 2,
			want: []string{
				`"aux-reader"`, `key space "resource-names:ClusterRole"`,
				`derived by component "alpha", carrying attachment "aux"`,
				`derived by component "beta", carrying attachment "aux"`,
			},
			refused: `key space "resource-names:ClusterRole--`,
		},
		{
			name:     "a node declaring no namespace beside one declaring one",
			release:  "release-no-namespace-beside-one.yaml",
			findings: 2,
			want: []string{
				`"shared-reader"`, `key space "resource-names:ClusterRole"`,
				`derived by component "store"`, `derived by component "edge"`,
				`dispatch path "dist/mixed/helm"`,
			},
			refused: `key space "resource-names:ClusterRole--`,
		},
		{
			name:     "two boundaries in two namespaces naming one directory",
			release:  "release-shared-directory.yaml",
			findings: 2,
			want: []string{
				`"shared-reader"`, `key space "resource-names:ClusterRole"`,
				`derived by component "store"`, `derived by component "edge"`,
				`dispatch path "dist/shared/helm"`,
			},
			refused: `key space "resource-names:ClusterRole--`,
		},
		{
			name:     "control: a namespaced built-in written twice in one namespace",
			release:  "release-settings-one-namespace.yaml",
			findings: 2,
			want: []string{
				`"shared-settings"`, `key space "resource-names:ConfigMap--shop"`,
				`derived by component "store"`, `derived by component "edge"`,
			},
			refused: `key space "resource-names:ConfigMap"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := pipelinetest.BuildE(t, clusterScopedNamesInput(tc.release))
			if err == nil {
				t.Fatalf("%s built cleanly; want the derived-name halt", tc.release)
			}
			if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
				t.Fatalf("%s failed for a different reason than the name collision: %s", tc.release, cerrors.CauseChain(err))
			}
			findings := collisionFindings(t, err)
			whole := strings.Join(findings, "\n")
			if len(findings) != tc.findings {
				t.Fatalf("the halt carries %d finding(s), want %d:\n%s", len(findings), tc.findings, whole)
			}
			for _, want := range tc.want {
				if !strings.Contains(whole, want) {
					t.Errorf("the halt does not carry %q\ngot:\n%s", want, whole)
				}
			}
			if strings.Contains(whole, tc.refused) {
				t.Errorf("the halt names a space opening %q, which is not where this name is compared:\n%s", tc.refused, whole)
			}
		})
	}
}

// TestClusterScopedNames_OneNameInTwoNamespacesBuildsWhereTheKindIsNamespaced holds
// the cells that must keep building: a custom kind a definition in the same render
// declares namespaced, a custom kind nothing in the render classifies, and a
// namespaced built-in — each written under one name by two components placed in two
// namespaces. Each row also asserts the shared name was emitted twice, so a fixture
// that stopped emitting it could not pass here by building.
func TestClusterScopedNames_OneNameInTwoNamespacesBuildsWhereTheKindIsNamespaced(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		release string
		kind    string
		shared  string
		// namespaces is what the two documents write at metadata.namespace, in the
		// order the build's files sort; "" where a document writes none.
		namespaces []string
	}{
		{
			name:       "a custom kind a definition in the same render declares namespaced",
			release:    "release-custom-namespaced.yaml",
			kind:       "Gizmo",
			shared:     "shared-gizmo",
			namespaces: []string{"other", "shop"},
		},
		{
			name:    "a custom kind nothing in the render classifies",
			release: "release-unclassified.yaml",
			kind:    "Probe",
			shared:  "shared-probe",
			// Left without a namespace by the placement, and compared in each node's
			// own namespace by the declaration.
			namespaces: []string{"", ""},
		},
		{
			name:       "a namespaced built-in",
			release:    "release-settings-two-namespaces.yaml",
			kind:       "ConfigMap",
			shared:     "shared-settings",
			namespaces: []string{"other", "shop"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			result := pipelinetest.Build(t, clusterScopedNamesInput(tc.release))
			var got []string
			for _, d := range emittedDocuments(t, result.Artifacts) {
				if d.Kind != tc.kind || d.Metadata.Name != tc.shared {
					continue
				}
				ns := ""
				if d.Metadata.Namespace != nil {
					ns = *d.Metadata.Namespace
				}
				got = append(got, ns)
			}
			if len(got) != 2 {
				t.Fatalf("the build emitted %d %s named %q, want 2 — the fixture no longer writes one name twice", len(got), tc.kind, tc.shared)
			}
			for i := range got {
				if got[i] != tc.namespaces[i] {
					t.Errorf("the two %s named %q write namespaces %q, want %q", tc.kind, tc.shared, got, tc.namespaces)
					break
				}
			}
		})
	}
}

// TestClusterScopedNames_DeclaringMovesNoEmittedByte is the property that keeps the
// declaration safe to change: the declared names travel beside the files and move
// none of them, run after run.
func TestClusterScopedNames_DeclaringMovesNoEmittedByte(t *testing.T) {
	t.Parallel()

	pipelinetest.AssertDeterministic(t, clusterScopedNamesInput("release-custom-namespaced.yaml"))
}
