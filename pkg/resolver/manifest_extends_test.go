// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// writeManifestFile writes a Manifest YAML fixture into dir.
func writeManifestFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

const prodManifest = `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: prod
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        core/api-server: api-server-helm:1.0.0
        core/postgres: postgres-helm:1.0.0
      name:
        legacy-billing: legacy-worker:3.0.0
    features:
      type:
        core/aws-secrets-manager: aws-secrets-fr:1.0.0
        core/otel: otel-fr:1.0.0
`

// TestResolveManifestExtends_NoExtends_Unchanged — a Manifest with no extends
// reference resolves exactly as authored (non-regression for the common case).
func TestResolveManifestExtends_NoExtends_Unchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "prod.yaml", prodManifest)
	base, err := ResolveManifest("", dir, "", "./prod.yaml")
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	got, err := ResolveManifestExtends("", dir, "", base)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	if got != base.Manifest {
		t.Errorf("a Manifest with no extends must be returned unchanged (same pointer)")
	}
	uri, ok := got.Spec.Mappings.Components.Type.Get("core/api-server")
	if !ok || uri != "api-server-helm:1.0.0" {
		t.Errorf("base mapping = %q,%v; want api-server-helm:1.0.0", uri, ok)
	}
}

// TestResolveManifestExtends_ShallowKeyReplacement — a child overrides one
// parent key within a tier; sibling parent keys survive; child-only keys are
// added; and a separate tier (name) inherits untouched.
func TestResolveManifestExtends_ShallowKeyReplacement(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "prod.yaml", prodManifest)
	// local overlay: swap the one credential FeatureRecipe and the postgres
	// component recipe; add a child-only component key; leave everything else.
	writeManifestFile(t, dir, "local.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: local
  version: 1.0.0
spec:
  extends: ./prod.yaml
  mappings:
    components:
      type:
        core/postgres: postgres-local:9.9.9
        core/cache: redis-local:1.0.0
    features:
      type:
        core/aws-secrets-manager: host-env-injector:1.0.0
`)
	child, err := ResolveManifest("", dir, "", "./local.yaml")
	if err != nil {
		t.Fatalf("resolve child: %v", err)
	}
	got, err := ResolveManifestExtends("", dir, "", child)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}

	comps := got.Spec.Mappings.Components.Type
	// child overrides parent key
	if uri, _ := comps.Get("core/postgres"); uri != "postgres-local:9.9.9" {
		t.Errorf("core/postgres = %q; want child override postgres-local:9.9.9", uri)
	}
	// sibling parent key survives
	if uri, _ := comps.Get("core/api-server"); uri != "api-server-helm:1.0.0" {
		t.Errorf("core/api-server = %q; want inherited api-server-helm:1.0.0", uri)
	}
	// child-only key added
	if uri, _ := comps.Get("core/cache"); uri != "redis-local:1.0.0" {
		t.Errorf("core/cache = %q; want child-only redis-local:1.0.0", uri)
	}
	// feature tier: child overrides one key
	feats := got.Spec.Mappings.Features.Type
	if uri, _ := feats.Get("core/aws-secrets-manager"); uri != "host-env-injector:1.0.0" {
		t.Errorf("core/aws-secrets-manager = %q; want child override host-env-injector:1.0.0", uri)
	}
	// feature sibling parent key survives
	if uri, _ := feats.Get("core/otel"); uri != "otel-fr:1.0.0" {
		t.Errorf("core/otel = %q; want inherited otel-fr:1.0.0", uri)
	}
	// name tier inherited untouched (child declared none)
	if uri, _ := got.Spec.Mappings.Components.Name.Get("legacy-billing"); uri != "legacy-worker:3.0.0" {
		t.Errorf("name-tier legacy-billing = %q; want inherited legacy-worker:3.0.0", uri)
	}
	// the composed Manifest is structurally standalone — extends is cleared
	if got.Spec.Extends != "" {
		t.Errorf("composed Manifest still carries extends %q; want empty", got.Spec.Extends)
	}
	// identity comes from the child
	if got.Metadata.Name != "local" {
		t.Errorf("composed identity = %q; want child name local", got.Metadata.Name)
	}
}

// TestResolveManifestExtends_MultiLevelChildWins — a three-deep chain folds
// root → middle → child so the most-derived value wins on a key all three set.
func TestResolveManifestExtends_MultiLevelChildWins(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "root.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: root
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        core/db: db-root:1.0.0
        core/only-root: only-root:1.0.0
`)
	writeManifestFile(t, dir, "mid.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: mid
  version: 1.0.0
spec:
  extends: ./root.yaml
  mappings:
    components:
      type:
        core/db: db-mid:2.0.0
        core/only-mid: only-mid:1.0.0
`)
	writeManifestFile(t, dir, "leaf.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: leaf
  version: 1.0.0
spec:
  extends: ./mid.yaml
  mappings:
    components:
      type:
        core/db: db-leaf:3.0.0
`)
	child, err := ResolveManifest("", dir, "", "./leaf.yaml")
	if err != nil {
		t.Fatalf("resolve leaf: %v", err)
	}
	got, err := ResolveManifestExtends("", dir, "", child)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	comps := got.Spec.Mappings.Components.Type
	if uri, _ := comps.Get("core/db"); uri != "db-leaf:3.0.0" {
		t.Errorf("core/db = %q; want leaf wins db-leaf:3.0.0", uri)
	}
	if uri, _ := comps.Get("core/only-root"); uri != "only-root:1.0.0" {
		t.Errorf("core/only-root = %q; want inherited only-root:1.0.0", uri)
	}
	if uri, _ := comps.Get("core/only-mid"); uri != "only-mid:1.0.0" {
		t.Errorf("core/only-mid = %q; want inherited only-mid:1.0.0", uri)
	}
}

// TestResolveManifestExtends_Cycle — a chain that revisits a Manifest halts
// with ErrCyclicExtension rather than looping.
func TestResolveManifestExtends_Cycle(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "a.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: a
  version: 1.0.0
spec:
  extends: ./b.yaml
  mappings:
    components:
      type:
        core/x: x:1.0.0
`)
	writeManifestFile(t, dir, "b.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: b
  version: 1.0.0
spec:
  extends: ./a.yaml
  mappings:
    components:
      type:
        core/y: y:1.0.0
`)
	child, err := ResolveManifest("", dir, "", "./a.yaml")
	if err != nil {
		t.Fatalf("resolve a: %v", err)
	}
	_, err = ResolveManifestExtends("", dir, "", child)
	if err == nil {
		t.Fatalf("cycle: expected error, got nil")
	}
	if !errors.Is(err, validator.ErrCyclicExtension()) {
		t.Errorf("cycle error = %v; want wrapped ErrCyclicExtension", err)
	}
}

// TestResolveManifestExtends_UnresolvableParent — an extends pointing at a
// missing Manifest halts with a merge error rather than silently dropping
// inheritance.
func TestResolveManifestExtends_UnresolvableParent(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "child.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: child
  version: 1.0.0
spec:
  extends: ./does-not-exist.yaml
  mappings:
    components:
      type:
        core/x: x:1.0.0
`)
	child, err := ResolveManifest("", dir, "", "./child.yaml")
	if err != nil {
		t.Fatalf("resolve child: %v", err)
	}
	_, err = ResolveManifestExtends("", dir, "", child)
	if err == nil {
		t.Fatalf("missing parent: expected error, got nil")
	}
	if !errors.Is(err, validator.ErrInheritanceMerge()) {
		t.Errorf("missing-parent error = %v; want wrapped ErrInheritanceMerge", err)
	}
}

// The tests from here to the end of this file hold the properties a Manifest
// extends chain is walked under. Each was written against a fault the walk
// carried, and each now asserts the repaired behaviour:
//
//   - which of the conditions a cyclic chain is reported as no longer depends
//     on which of the two legal ways of writing an extends reference the author
//     chose. Chain membership is keyed on each link's resolved file path, so one
//     file is one link however its reference is spelled.
//   - the cycle diagnostic names the edge that closes the loop, whichever way
//     the references are written, rather than an earlier edge whose extends
//     declaration is legal.
//   - a loop is reported as a loop at every length. The depth bound is judged
//     once the walk has ended, so a loop longer than the bound still closes on
//     itself first and is reported as the loop it is — the author is never told
//     to shorten a chain whose length was not the problem.
//   - a chain that ends cleanly and runs past the bound reports the condition
//     that names an over-deep chain, which is the condition a Component's or a
//     Feature's chain reports for the same fault.
//   - a reference that resolves to nothing keeps the advice a Manifest author
//     can act on — the Release's own directory, never the catalog — and carries
//     the failure that looking there produced.
//
// Each test names again, at its own site, which property it holds and what
// breaking it would look like.

// extendsRefStyle spells an extends reference in one of the two forms a
// Manifest's extends accepts: an explicit relative path to the file, or the bare
// metadata.name of a Manifest in the Release's directory. Which form an author
// chooses is not meant to change what a fault is reported as, and it does not:
// both forms name one file, and the walk keys a link on the file.
type extendsRefStyle int

const (
	pathStyleRef extendsRefStyle = iota
	identityStyleRef
)

func (s extendsRefStyle) String() string {
	if s == pathStyleRef {
		return "path-style"
	}
	return "identity-style"
}

// ref spells a reference to the Manifest named name in this style.
func (s extendsRefStyle) ref(name string) string {
	if s == pathStyleRef {
		return "./" + name + ".yaml"
	}
	return name
}

// chainManifestName names the i-th Manifest of a generated chain. The width is
// fixed so the names sort in chain order wherever a failure prints them.
func chainManifestName(i int) string { return fmt.Sprintf("m%02d", i) }

// manifestChainDoc renders one Manifest of a generated chain: its own name, the
// extends reference it declares (omitted when empty), and one component mapping
// keyed on its own name, so a composed result shows which links were folded in.
func manifestChainDoc(name, extends string) string {
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1alpha1\nkind: Manifest\nmetadata:\n  name: ")
	b.WriteString(name)
	b.WriteString("\n  version: 1.0.0\nspec:\n")
	if extends != "" {
		b.WriteString("  extends: " + extends + "\n")
	}
	b.WriteString("  mappings:\n    components:\n      type:\n        core/")
	b.WriteString(name + ": " + name + "-recipe:1.0.0\n")
	return b.String()
}

// writeManifestChain writes n Manifests into dir, each extending the next in the
// given reference style. When cyclic the last extends the first; otherwise the
// last extends nothing. It returns the head of the chain, resolved the way a
// Release's own manifest reference resolves.
func writeManifestChain(t *testing.T, dir string, n int, style extendsRefStyle, cyclic bool) ResolvedManifest {
	t.Helper()
	for i := range n {
		extends := ""
		switch {
		case i < n-1:
			extends = style.ref(chainManifestName(i + 1))
		case cyclic:
			extends = style.ref(chainManifestName(0))
		}
		writeManifestFile(t, dir, chainManifestName(i)+".yaml", manifestChainDoc(chainManifestName(i), extends))
	}
	head, err := ResolveManifest("", dir, "", style.ref(chainManifestName(0)))
	if err != nil {
		t.Fatalf("test setup: resolve chain head: %v", err)
	}
	return head
}

// TestResolveManifestExtends_DepthBoundAdmitsTenAndRefusesEleven pins the depth
// bound a Manifest extends chain is held to.
//
// The bound admits a chain of ten Manifests and refuses eleven, and that holds
// for both ways of writing the reference because an acyclic chain never consults
// the visited set. The accepted row is the control: without it every refusal
// here could be satisfied by a walk that refused everything.
//
// 🔴 The refusal is reported under the condition that names an over-deep extends
// chain, which is the condition a Component's or a Feature's chain reports for
// the same fault, and NOT under the inheritance-merge condition a parent that
// resolves to nothing reports. Both halves are asserted: a consumer matching on
// the identity has to be able to tell an over-deep chain from an absent parent,
// and one identity standing for both is what made that impossible.
func TestResolveManifestExtends_DepthBoundAdmitsTenAndRefusesEleven(t *testing.T) {
	t.Parallel()
	for _, style := range []extendsRefStyle{pathStyleRef, identityStyleRef} {
		t.Run(style.String()+"/accepted at ten", func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeManifestChain(t, dir, 10, style, false)
			got, err := ResolveManifestExtends("", dir, "", head)
			if err != nil {
				t.Fatalf("a chain of ten Manifests must be accepted; got: %v", err)
			}
			// The root's own mapping proves the whole chain folded, rather than
			// the walk merely declining to fail.
			root := chainManifestName(9)
			if uri, ok := got.Spec.Mappings.Components.Type.Get("core/" + root); !ok || uri != root+"-recipe:1.0.0" {
				t.Errorf("composed mappings missing the root link %s = %q,%v; want %s-recipe:1.0.0", root, uri, ok, root)
			}
		})
		t.Run(style.String()+"/refused at eleven", func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeManifestChain(t, dir, 11, style, false)
			_, err := ResolveManifestExtends("", dir, "", head)
			if err == nil {
				t.Fatal("a chain of eleven Manifests must halt")
			}
			if !errors.Is(err, validator.ErrExtendsChainTooDeep()) {
				t.Errorf("depth refusal = %v; want the over-deep-chain condition", err)
			}
			if errors.Is(err, validator.ErrInheritanceMerge()) {
				t.Errorf("depth refusal = %v; it must not also report the condition an absent parent reports", err)
			}
			want := fmt.Sprintf(
				"%s (Manifest %s, rule extends). "+
					"the bound is 10 and this chain reaches 11; shorten the chain, or extend a "+
					"base that sits closer to its root",
				validator.ErrExtendsChainTooDeep().Error(), chainManifestName(0))
			if err.Error() != want {
				t.Errorf("depth refusal message =\n\t%q\nwant\n\t%q", err.Error(), want)
			}
		})
	}
}

// TestResolveManifestExtends_ALoopIsReportedAsALoopAtEveryLength pins the whole
// of what a cyclic Manifest chain is reported as: the cycle condition, at every
// length, on both ways of writing an extends reference.
//
// Two independent faults are held here and a row failing tells them apart.
//
// The lengths cover both sides of the depth bound. A loop that closes before the
// bound is reached was always reported as a loop; one longer than the bound is
// the case the bound used to claim first, and the advice the author was handed —
// shorten the chain — cannot remove a loop. The bound is judged once the walk has
// ended, so a chain that closes on itself is a loop whatever its length, and only
// a chain that ends cleanly can be too deep. The final row is that other side: an
// acyclic chain past the bound, which must report depth and never the cycle, so
// the rows above cannot be satisfied by a walk that simply stopped judging length.
//
// Both reference styles run at every length because a link is a file, so the two
// legal ways of naming one file are one link. A walk keying chain membership on
// anything spelled differently in the two forms — a reference compared against a
// name — takes an extra lap around a path-style loop, which moves the path-style
// rows and leaves the identity-style rows where they are.
func TestResolveManifestExtends_ALoopIsReportedAsALoopAtEveryLength(t *testing.T) {
	t.Parallel()
	for _, style := range []extendsRefStyle{pathStyleRef, identityStyleRef} {
		for _, length := range []int{2, 3, 8, 9, 10, 12, 25} {
			t.Run(fmt.Sprintf("%s/loop of %d", style, length), func(t *testing.T) {
				t.Parallel()
				dir := t.TempDir()
				head := writeManifestChain(t, dir, length, style, true)
				_, err := ResolveManifestExtends("", dir, "", head)
				if err == nil {
					t.Fatalf("a %s loop of %d must halt", style, length)
				}
				if !errors.Is(err, validator.ErrCyclicExtension()) {
					t.Errorf("a %s loop of %d must report the cycle condition; got: %v", style, length, err)
				}
				if errors.Is(err, validator.ErrExtendsChainTooDeep()) {
					t.Errorf("a %s loop of %d reports the over-deep-chain condition; shortening a loop does not remove it: %v", style, length, err)
				}
				// Matched without regard to case, so the assertion keeps biting if
				// the advice is ever reworded to open a sentence differently: a
				// phrase this looks for in one casing alone would pass trivially
				// the moment that casing moved, and nothing would say so.
				if strings.Contains(strings.ToLower(err.Error()), "shorten the chain") {
					t.Errorf("a %s loop of %d is answered with advice to shorten it: %v", style, length, err)
				}
			})
		}
		t.Run(style.String()+"/acyclic chain past the bound still reports depth", func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeManifestChain(t, dir, 11, style, false)
			_, err := ResolveManifestExtends("", dir, "", head)
			if err == nil {
				t.Fatalf("an acyclic %s chain of eleven must halt", style)
			}
			if !errors.Is(err, validator.ErrExtendsChainTooDeep()) {
				t.Errorf("an acyclic %s chain of eleven must report the over-deep-chain condition; got: %v", style, err)
			}
			if errors.Is(err, validator.ErrCyclicExtension()) {
				t.Errorf("an acyclic %s chain of eleven must not report a cycle; got: %v", style, err)
			}
		})
	}
}

// TestResolveManifestExtends_CycleNamesWhichEdge pins which edge a cycle
// diagnostic sends the author to.
//
// A two-link loop cannot tell the edge that closes the loop from the one the
// walk started on, because they are the same edge; three links is the shortest
// loop that can. Naming the edge rather than one endpoint is the whole point of
// the diagnostic — the Manifest a resolution was requested for is rarely the one
// carrying the link that closes the loop.
//
// 🔴 Both rows name the same edge, and that agreement is the property under test
// rather than two expectations that happen to coincide. Chain membership is
// keyed on each link's resolved file path, so the two legal ways of writing an
// extends reference name one link and the walk meets the loop on the same step
// whichever was used. A walk keying on anything spelled differently in the two
// forms — a reference compared against a name, say — meets a path-style loop one
// lap late and names an earlier edge, sending the author to a file whose extends
// declaration is legal. That is the failure these rows refuse; the
// identity-style row is the control the path-style row converged onto, and it is
// unchanged.
//
// The edge is what this test holds; that the diagnostic is reached at every
// length is held by the loop test above.
func TestResolveManifestExtends_CycleNamesWhichEdge(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		// style is how every link in the loop is written.
		style extendsRefStyle
		// from and link are the two halves of the edge the diagnostic names,
		// given as indices into the generated chain.
		from, link int
	}{
		{"path-style names the edge that closes the loop", pathStyleRef, 2, 0},
		{"identity-style names the edge that closes the loop", identityStyleRef, 2, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeManifestChain(t, dir, 3, tc.style, true)
			_, err := ResolveManifestExtends("", dir, "", head)
			if err == nil {
				t.Fatalf("a %s loop of three must halt", tc.style)
			}
			if !errors.Is(err, validator.ErrCyclicExtension()) {
				t.Fatalf("a %s loop of three must report the cycle condition; got: %v", tc.style, err)
			}
			from := chainManifestName(tc.from)
			want := fmt.Sprintf(
				"%s (Manifest %s, rule extends). "+
					"%s extends %s, which is already in the chain; remove the extends on %s, or "+
					"point it at an entity outside the chain",
				validator.ErrCyclicExtension().Error(), chainManifestName(0),
				from, tc.style.ref(chainManifestName(tc.link)), from)
			if err.Error() != want {
				t.Errorf("cycle diagnostic =\n\t%q\nwant\n\t%q", err.Error(), want)
			}
		})
	}
}

// TestResolveManifestExtends_MissingParentCarriesDirectoryAdviceAndResolveError
// pins the whole diagnostic a Manifest whose parent cannot be resolved receives,
// for both ways of writing the reference.
//
// Two properties are load-bearing here and neither is reachable from the
// condition's identity alone, which is why the assertion is on the rendered
// message. The advice names the Release's own directory, because a Manifest
// extends reference resolves there — a Manifest is operator configuration
// rather than catalog content, and a reference to one never names a catalog
// entity. And the underlying resolve failure travels in the text, so the author
// reads which file could not be opened, or which name matched nothing, rather
// than only that something did not resolve.
//
// The expected text is composed from the resolve failure this directory actually
// produces, so the assertion is on the walk carrying that failure through rather
// than on the wording of the failure underneath. Note that the failure travels
// as text and not as a wrapped chain, so it is unreachable by errors.Is: the
// assertion on the rendered message is the only thing that can hold it.
//
// The catalog-shaped wording an entity-catalog walk would render is asserted
// absent by its own two sentences rather than by the word "catalog", which the
// unresolvable-identity failure uses legitimately when listing where it looked.
func TestResolveManifestExtends_MissingParentCarriesDirectoryAdviceAndResolveError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		ref  string
		// wantUnderlying is a fragment of the resolve failure that must survive
		// into the composed message. The path-style row's full text names a
		// temporary directory and the operating system's own wording, so only
		// the part this repository controls is spelled out.
		wantUnderlying string
	}{
		{
			name:           "a relative path naming no file",
			ref:            "./does-not-exist.yaml",
			wantUnderlying: "failed to read file:",
		},
		{
			name:           "an identity naming no Manifest in the directory",
			ref:            "no-such-manifest",
			wantUnderlying: `no Manifest named "no-such-manifest" in the Release directory, catalog, or registry`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeManifestFile(t, dir, "child.yaml", manifestChainDoc("child", tc.ref))
			child, err := ResolveManifest("", dir, "", "./child.yaml")
			if err != nil {
				t.Fatalf("test setup: resolve child: %v", err)
			}
			// The same resolution the walk performs, taken independently, so the
			// expectation below is not a restatement of the walk's own output.
			_, resolveErr := ResolveManifest("", dir, "", tc.ref)
			if resolveErr == nil {
				t.Fatalf("test setup: %q must not resolve", tc.ref)
			}

			_, err = ResolveManifestExtends("", dir, "", child)
			if err == nil {
				t.Fatal("a parent that cannot be resolved must halt")
			}
			if !errors.Is(err, validator.ErrInheritanceMerge()) {
				t.Errorf("missing-parent refusal = %v; want the inheritance-merge condition", err)
			}
			want := fmt.Sprintf(
				"%s (Manifest child, at .spec.extends, child extends %s, which could not be resolved: %v). "+
					"Verify the extends reference is a relative path or the name of a Manifest in the Release's directory",
				validator.ErrInheritanceMerge().Error(), tc.ref, resolveErr)
			if err.Error() != want {
				t.Errorf("missing-parent message =\n\t%q\nwant\n\t%q", err.Error(), want)
			}
			if !strings.Contains(err.Error(), tc.wantUnderlying) {
				t.Errorf("missing-parent message = %q; want it to carry the resolve failure %q", err.Error(), tc.wantUnderlying)
			}
			for _, catalogWording := range []string{
				"which no catalog entity provides",
				"Verify the extends reference names an entity present in the catalog",
			} {
				if strings.Contains(err.Error(), catalogWording) {
					t.Errorf("missing-parent message = %q; it must not send a Manifest author to the catalog (%q)", err.Error(), catalogWording)
				}
			}
		})
	}
}

// TestResolveManifestExtends_TwoFilesSharingOneNameCompose pins that two
// Manifest files declaring one metadata.name inside a single chain compose
// normally.
//
// The case is reachable because a relative-path reference reads the file it
// names directly: the same-directory index, which refuses two Manifests claiming
// one name, is never consulted on this path. So the two files are legal input,
// and a walk deciding that a chain had closed on itself because a name recurred
// would refuse a chain with no loop in it — a refusal of input that works today.
//
// Every link's own mapping is asserted present, which is what distinguishes
// composing the whole chain from stopping early without an error.
func TestResolveManifestExtends_TwoFilesSharingOneNameCompose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeManifestFile(t, dir, "first.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: shared
  version: 1.0.0
spec:
  extends: ./second.yaml
  mappings:
    components:
      type:
        core/first: first-recipe:1.0.0
`)
	writeManifestFile(t, dir, "second.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: shared
  version: 1.0.0
spec:
  extends: ./third.yaml
  mappings:
    components:
      type:
        core/second: second-recipe:1.0.0
`)
	writeManifestFile(t, dir, "third.yaml", `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: tail
  version: 1.0.0
spec:
  mappings:
    components:
      type:
        core/third: third-recipe:1.0.0
`)

	head, err := ResolveManifest("", dir, "", "./first.yaml")
	if err != nil {
		t.Fatalf("test setup: resolve first: %v", err)
	}
	got, err := ResolveManifestExtends("", dir, "", head)
	if err != nil {
		t.Fatalf("two Manifest files sharing one metadata.name must compose; got: %v", err)
	}
	// A slice rather than a map, so a failure reports the links in chain order
	// rather than in whatever order a map walk produced.
	for _, want := range []struct{ key, uri string }{
		{"core/first", "first-recipe:1.0.0"},
		{"core/second", "second-recipe:1.0.0"},
		{"core/third", "third-recipe:1.0.0"},
	} {
		if uri, ok := got.Spec.Mappings.Components.Type.Get(want.key); !ok || uri != want.uri {
			t.Errorf("composed mapping %s = %q,%v; want %q", want.key, uri, ok, want.uri)
		}
	}
}
