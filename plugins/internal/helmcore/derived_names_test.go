// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package helmcore_test

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// file is one emitted render output, written out at the call site so each case
// reads as the bytes the render handed back.
func file(path, content string) rendercore.RenderedFile {
	return rendercore.RenderedFile{Path: path, Content: []byte(content)}
}

const svcAlpha = `apiVersion: v1
kind: Service
metadata:
  name: alpha
spec:
  type: ClusterIP
`

// TestDeclaredResourceNames_ReadsEachEmittedResourceUnderItsKindAndNamespace is
// the shape of the declaration: the name is the string the document carries in
// metadata.name, and the space carries the two coordinates that bound it.
//
// The declared name is asserted to be byte-identical to the authored one, because
// that identity is what makes a refusal actionable: the name in the halt doubles
// as the term the author searches their own templates for.
func TestDeclaredResourceNames_ReadsEachEmittedResourceUnderItsKindAndNamespace(t *testing.T) {
	t.Parallel()

	declared := helmcore.DeclaredResourceNames(
		[]rendercore.RenderedFile{file("a.yaml", svcAlpha)}, "default")

	if want := []string{"resource-names:Service--default"}; !reflect.DeepEqual(declared.Keys(), want) {
		t.Fatalf("key spaces = %v, want %v", declared.Keys(), want)
	}
	names, ok := declared.Get("resource-names:Service--default")
	if !ok {
		t.Fatal("the space the resource was declared into is absent")
	}
	if want := []string{"alpha"}; !reflect.DeepEqual(names, want) {
		t.Errorf("declared names = %v, want %v — the name must reach the host exactly as authored", names, want)
	}
}

// TestDeclaredResourceNames_KindAndNamespaceBothBoundTheName covers the two
// coordinates in the direction that matters: a name declared under a different
// kind, or in a different namespace, lands in a DIFFERENT space and so is never
// compared against the first.
//
// Both directions are asserted rather than only one, because each is a whole class
// of correct authoring a space missing that coordinate would refuse. A Service and
// a Deployment named alike is ordinary Kubernetes; so is one name held
// independently in two namespaces.
func TestDeclaredResourceNames_KindAndNamespaceBothBoundTheName(t *testing.T) {
	t.Parallel()

	declared := helmcore.DeclaredResourceNames([]rendercore.RenderedFile{
		file("svc.yaml", svcAlpha),
		file("dep.yaml", "kind: Deployment\nmetadata:\n  name: alpha\n"),
		file("other-ns.yaml", "kind: Service\nmetadata:\n  name: alpha\n  namespace: staging\n"),
	}, "default")

	if declared.Len() != 3 {
		t.Fatalf("three resources differing in kind or namespace produced %d space(s), want 3: %v",
			declared.Len(), declared.Keys())
	}
	for _, space := range declared.Keys() {
		names, _ := declared.Get(space)
		if len(names) != 1 {
			t.Errorf("space %q carries %v; each of these three names is alone in its space", space, names)
		}
	}
}

// TestDeclaredResourceNames_TheSpaceIsInjectiveOverKindAndNamespace is the
// property a plain separator join would not have.
//
// The pair is chosen so a single-hyphen join folds it: kind `a` in namespace `b-c`
// and kind `a-b` in namespace `c` both reach "a-b-c". A namespace legitimately
// carries a hyphen, so the fold is reachable from real input rather than
// contrived — and folded, the two would be compared against each other and a name
// each held alone would halt the build.
func TestDeclaredResourceNames_TheSpaceIsInjectiveOverKindAndNamespace(t *testing.T) {
	t.Parallel()

	declared := helmcore.DeclaredResourceNames([]rendercore.RenderedFile{
		file("one.yaml", "kind: a\nmetadata:\n  name: shared\n  namespace: b-c\n"),
		file("two.yaml", "kind: a-b\nmetadata:\n  name: shared\n  namespace: c\n"),
	}, "default")

	if declared.Len() != 2 {
		t.Fatalf("the folding pair reached %d space(s), want 2 — the two coordinates ran together: %v",
			declared.Len(), declared.Keys())
	}
}

// TestDeclaredResourceNames_AnAuthoredNamespaceWinsOverTheFallback pins which
// namespace bounds the name. A document naming its own namespace lands there
// whatever the node was annotated with, so that is the coordinate declared; a
// document naming none lands wherever the node targets, which is the fallback.
func TestDeclaredResourceNames_AnAuthoredNamespaceWinsOverTheFallback(t *testing.T) {
	t.Parallel()

	declared := helmcore.DeclaredResourceNames([]rendercore.RenderedFile{
		file("own.yaml", "kind: Service\nmetadata:\n  name: alpha\n  namespace: chart-said-this\n"),
		file("none.yaml", svcAlpha),
	}, "node-was-annotated-this")

	// A hyphen an author wrote between two admitted characters is carried through,
	// so these two read as authored: the encoding escapes what the alphabet refuses
	// and leaves an ordinary name alone, which is what keeps a space legible.
	for _, want := range []string{
		"resource-names:Service--chart-said-this",
		"resource-names:Service--node-was-annotated-this",
	} {
		if _, ok := declared.Get(want); !ok {
			t.Errorf("no space %q; got %v", want, declared.Keys())
		}
	}
}

// TestDeclaredResourceNames_DeclaresNothingItCannotName covers every input the
// reader skips. None of them is a failure: declaring is an addition to what a
// render already does, so a document it cannot name must cost the render nothing
// rather than giving it a second way to fail.
//
// The rows are the shapes a real chart emits. A document with no name is a
// generateName resource, whose name the cluster completes at admission. A document
// with no kind is a List wrapper. A null document is what a false conditional
// leaves behind. A sequence root and unparseable bytes are not resources at all.
func TestDeclaredResourceNames_DeclaresNothingItCannotName(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		content string
	}{
		{"no metadata.name — a generateName resource", "kind: Service\nmetadata:\n  generateName: alpha-\n"},
		{"no kind — a List wrapper", "metadata:\n  name: alpha\n"},
		{"a null document a false conditional left", "---\nnull\n"},
		{"an empty body", ""},
		{"a sequence root", "- one\n- two\n"},
		{"a bare scalar root", "just-a-string\n"},
		{"bytes that do not parse as YAML", "kind: Service\n\tmetadata: [oops\n"},
		{"a non-scalar metadata.name", "kind: Service\nmetadata:\n  name: [a, b]\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			declared := helmcore.DeclaredResourceNames(
				[]rendercore.RenderedFile{file("x.yaml", tc.content)}, "default")
			if declared.Len() != 0 {
				t.Errorf("declared %v, want nothing", declared.Keys())
			}
		})
	}
}

// TestDeclaredResourceNames_APositiveControlBesideThoseSkips is the calibration
// the case above needs. Every row there asserts an absence, and a reader that
// declared nothing whatever would satisfy all of them — so the same reader is run
// over a document it must name, with a skipped document beside it in the same
// file.
func TestDeclaredResourceNames_APositiveControlBesideThoseSkips(t *testing.T) {
	t.Parallel()

	declared := helmcore.DeclaredResourceNames([]rendercore.RenderedFile{
		file("mixed.yaml", "kind: Service\nmetadata:\n  generateName: alpha-\n---\n"+svcAlpha),
	}, "default")

	names, ok := declared.Get("resource-names:Service--default")
	if !ok {
		t.Fatalf("the nameable document in a multi-document file was not declared; got %v", declared.Keys())
	}
	if want := []string{"alpha"}; !reflect.DeepEqual(names, want) {
		t.Errorf("declared names = %v, want %v", names, want)
	}
}

// TestDeclaredResourceNames_TwoEmittedFilesHoldingOneIdentityDeclareItTwice is
// the case the host's own rule reads as one contributor's derivation failing to be
// injective. Two emitted files carrying one kind, namespace and name are two
// resources with one identity: applied together, the second holds the name and the
// first is gone. The declaration says so by carrying the name twice rather than
// collapsing it.
func TestDeclaredResourceNames_TwoEmittedFilesHoldingOneIdentityDeclareItTwice(t *testing.T) {
	t.Parallel()

	declared := helmcore.DeclaredResourceNames([]rendercore.RenderedFile{
		file("a.yaml", svcAlpha),
		file("b.yaml", svcAlpha+"  clusterIP: None\n"),
	}, "default")

	names, _ := declared.Get("resource-names:Service--default")
	if want := []string{"alpha", "alpha"}; !reflect.DeepEqual(names, want) {
		t.Errorf("declared names = %v, want %v — a repeat within one declaration is what the host reads", names, want)
	}
}

// TestDeclaredResourceNames_TheDeclarationIsAFunctionOfTheEmittedOrder pins the
// determinism the payload depends on: the space order and each space's name order
// follow the order the render emitted, so identical input yields identical
// declared bytes rather than whatever a map walk produced.
func TestDeclaredResourceNames_TheDeclarationIsAFunctionOfTheEmittedOrder(t *testing.T) {
	t.Parallel()

	files := []rendercore.RenderedFile{
		file("z.yaml", "kind: Zeta\nmetadata:\n  name: z\n"),
		file("a.yaml", "kind: Alpha\nmetadata:\n  name: a\n"),
	}
	first := helmcore.DeclaredResourceNames(files, "default")
	for i := 0; i < 50; i++ {
		again := helmcore.DeclaredResourceNames(files, "default")
		if !reflect.DeepEqual(first.Keys(), again.Keys()) {
			t.Fatalf("space order moved between runs: %v then %v", first.Keys(), again.Keys())
		}
	}
	// Emission order, not lexical order: the render's own sequence is what the
	// declaration follows.
	if want := []string{"resource-names:Zeta--default", "resource-names:Alpha--default"}; !reflect.DeepEqual(first.Keys(), want) {
		t.Errorf("space order = %v, want %v", first.Keys(), want)
	}
}

// TestEffectiveNamespace_ResolvesTheAnnotationThenTheClusterDefault covers the one
// function both the render's release options and the declaration read, so the
// namespace a resource is rendered into and the namespace its name is declared
// under cannot disagree.
func TestEffectiveNamespace_ResolvesTheAnnotationThenTheClusterDefault(t *testing.T) {
	t.Parallel()

	if got := helmcore.EffectiveNamespace(rendercore.NodeRenderContext{}); got != "default" {
		t.Errorf("an unannotated node resolved to %q, want %q", got, "default")
	}
	annotated := rendercore.NodeRenderContext{
		Annotations: map[string]string{rendercore.AnnotationNamespace: "team-a"},
	}
	if got := helmcore.EffectiveNamespace(annotated); got != "team-a" {
		t.Errorf("an annotated node resolved to %q, want %q", got, "team-a")
	}
}
