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
	base, err := ResolveManifest(dir, "./prod.yaml")
	if err != nil {
		t.Fatalf("resolve base: %v", err)
	}
	got, err := ResolveManifestExtends(dir, base)
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
	child, err := ResolveManifest(dir, "./local.yaml")
	if err != nil {
		t.Fatalf("resolve child: %v", err)
	}
	got, err := ResolveManifestExtends(dir, child)
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
	child, err := ResolveManifest(dir, "./leaf.yaml")
	if err != nil {
		t.Fatalf("resolve leaf: %v", err)
	}
	got, err := ResolveManifestExtends(dir, child)
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
	child, err := ResolveManifest(dir, "./a.yaml")
	if err != nil {
		t.Fatalf("resolve a: %v", err)
	}
	_, err = ResolveManifestExtends(dir, child)
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
	child, err := ResolveManifest(dir, "./child.yaml")
	if err != nil {
		t.Fatalf("resolve child: %v", err)
	}
	_, err = ResolveManifestExtends(dir, child)
	if err == nil {
		t.Fatalf("missing parent: expected error, got nil")
	}
	if !errors.Is(err, validator.ErrInheritanceMerge()) {
		t.Errorf("missing-parent error = %v; want wrapped ErrInheritanceMerge", err)
	}
}

// The tests from here to the end of this file characterise the Manifest extends
// walk against three faults, so that a repair is verifiable rather than argued.
// Two of the three are repaired and the rows holding them assert the repaired
// behaviour; the third is still present, and the rows holding it assert the
// defect rather than what the walk ought to do:
//
//   - REPAIRED — which of the two conditions a cyclic chain is reported as no
//     longer depends on which of the two legal ways of writing an extends
//     reference the author chose. Chain membership is keyed on each link's
//     resolved file path, so one file is one link however its reference is
//     spelled, and both forms change over at the same length.
//   - REPAIRED — the cycle diagnostic names the edge that closes the loop,
//     whichever way the references are written, rather than an earlier edge
//     whose extends declaration is legal.
//   - STILL WRONG — a cyclic chain of ten or more links is reported as an
//     over-deep chain, so the author is told to shorten a loop, which cannot
//     remove it. The bound is tested as the first statement of every step,
//     before that step can recognise it has returned to a file already in the
//     chain. Testing it after the walk instead, as the shared entity-inheritance
//     walk does, reports a loop as a loop at every length.
//
// Each test names again, at its own site, which part of what it asserts is the
// remaining defect and which part is behaviour a further repair must preserve.

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
	head, err := ResolveManifest(dir, style.ref(chainManifestName(0)))
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
// 🔴 The refusal is reported under the inheritance-merge condition — the same
// identity a parent that resolves to nothing reports — rather than under the
// condition that names an over-deep extends chain, which is what a Component's
// or a Feature's chain reports for this fault. A consumer matching on the
// identity therefore cannot tell the two apart. That is the before-picture, not
// the contract.
func TestResolveManifestExtends_DepthBoundAdmitsTenAndRefusesEleven(t *testing.T) {
	t.Parallel()
	for _, style := range []extendsRefStyle{pathStyleRef, identityStyleRef} {
		t.Run(style.String()+"/accepted at ten", func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeManifestChain(t, dir, 10, style, false)
			got, err := ResolveManifestExtends(dir, head)
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
			_, err := ResolveManifestExtends(dir, head)
			if err == nil {
				t.Fatal("a chain of eleven Manifests must halt")
			}
			if !errors.Is(err, validator.ErrInheritanceMerge()) {
				t.Errorf("depth refusal = %v; want the inheritance-merge condition", err)
			}
			// 🔴 The defect, pinned. Should this ever hold, the walk has been
			// repaired and this expectation is the one to update — it is not
			// evidence of a regression.
			if errors.Is(err, validator.ErrExtendsChainTooDeep()) {
				t.Error("a Manifest depth refusal now reports the over-deep-chain condition, which it does not today")
			}
			want := fmt.Sprintf("Manifest %q extends chain exceeds maximum depth of 10; reduce the chain length", chainManifestName(0))
			if !strings.Contains(err.Error(), want) {
				t.Errorf("depth refusal text = %q; want it to carry %q", err.Error(), want)
			}
		})
	}
}

// TestResolveManifestExtends_ReportedConditionDependsOnReferenceStyle pins which
// of two conditions a cyclic Manifest chain is reported as, at the length where
// the answer changes and on both ways of writing an extends reference.
//
// The name states a fault the walk no longer has. Chain membership is keyed on
// each link's resolved file path, so a file is one link whichever legal form
// names it, and the two forms change over at the same length. Holding that
// convergence is now the rows' job: a walk keying on anything spelled
// differently in the two forms takes an extra lap on a path-style loop, which
// moves the path-style rows while leaving the identity-style rows where they
// are.
//
// The changeover length follows from the depth bound being tested as the first
// statement of every step:
//
//	loop of L links   the walk is back at the head on step L with the chain L
//	                  long; the bound is tested first in that step and refuses
//	                  from a chain length of 10 — so a loop of nine or fewer is
//	                  reported as a loop, and one of ten or more as an over-deep
//	                  chain
//
// 🔴 A row expecting depth pins what is STILL WRONG: a loop is a loop at every
// length, and shortening one does not remove it, so the advice that author is
// handed cannot fix their files. Testing the bound after the walk completes, as
// the shared entity-inheritance walk does, makes every length report the loop,
// and a row expecting depth is then the one to update. A row expecting the cycle
// asserts behaviour that is correct now, and that repair must preserve it.
func TestResolveManifestExtends_ReportedConditionDependsOnReferenceStyle(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name      string
		style     extendsRefStyle
		length    int
		wantDepth bool
	}{
		{"path-style loop of eight reports the cycle", pathStyleRef, 8, false},
		{"path-style loop of nine reports the cycle, as identity-style does", pathStyleRef, 9, false},
		{"identity-style loop of nine reports the cycle", identityStyleRef, 9, false},
		{"identity-style loop of ten reports depth", identityStyleRef, 10, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeManifestChain(t, dir, tc.length, tc.style, true)
			_, err := ResolveManifestExtends(dir, head)
			if err == nil {
				t.Fatalf("a %s loop of %d must halt", tc.style, tc.length)
			}
			cycle := errors.Is(err, validator.ErrCyclicExtension())
			depth := errors.Is(err, validator.ErrInheritanceMerge()) &&
				strings.Contains(err.Error(), "exceeds maximum depth")
			if tc.wantDepth {
				if !depth {
					t.Errorf("a %s loop of %d reports depth today; got: %v", tc.style, tc.length, err)
				}
				// 🔴 Reporting the cycle here is the RIGHT answer and is not what
				// happens today. Should this hold, the walk has been repaired and
				// this row is the one to update.
				if cycle {
					t.Errorf("a %s loop of %d now reports the cycle condition, which it does not today; got: %v", tc.style, tc.length, err)
				}
				return
			}
			if !cycle {
				t.Errorf("a %s loop of %d must report the cycle condition; got: %v", tc.style, tc.length, err)
			}
			if depth {
				t.Errorf("a %s loop of %d must not report depth; got: %v", tc.style, tc.length, err)
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
// Nothing this test asserts is still wrong. What is still wrong sits at a length
// this test does not reach: a loop of ten or more links trips the depth bound
// and never reaches this diagnostic at all, which the reference-style test above
// holds instead.
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
			_, err := ResolveManifestExtends(dir, head)
			if err == nil {
				t.Fatalf("a %s loop of three must halt", tc.style)
			}
			if !errors.Is(err, validator.ErrCyclicExtension()) {
				t.Fatalf("a %s loop of three must report the cycle condition; got: %v", tc.style, err)
			}
			want := fmt.Sprintf("Manifest %q extends %q, which is already in the chain; remove the circular reference",
				chainManifestName(tc.from), tc.style.ref(chainManifestName(tc.link)))
			if !strings.Contains(err.Error(), want) {
				t.Errorf("cycle diagnostic = %q; want it to name the edge %q", err.Error(), want)
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
			child, err := ResolveManifest(dir, "./child.yaml")
			if err != nil {
				t.Fatalf("test setup: resolve child: %v", err)
			}
			// The same resolution the walk performs, taken independently, so the
			// expectation below is not a restatement of the walk's own output.
			_, resolveErr := ResolveManifest(dir, tc.ref)
			if resolveErr == nil {
				t.Fatalf("test setup: %q must not resolve", tc.ref)
			}

			_, err = ResolveManifestExtends(dir, child)
			if err == nil {
				t.Fatal("a parent that cannot be resolved must halt")
			}
			if !errors.Is(err, validator.ErrInheritanceMerge()) {
				t.Errorf("missing-parent refusal = %v; want the inheritance-merge condition", err)
			}
			want := fmt.Sprintf(
				"%s: Manifest %q extends %q, which could not be resolved: %v; "+
					"verify the extends reference is a relative path or the name of a Manifest in the Release's directory",
				validator.ErrInheritanceMerge().Error(), "child", tc.ref, resolveErr)
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

	head, err := ResolveManifest(dir, "./first.yaml")
	if err != nil {
		t.Fatalf("test setup: resolve first: %v", err)
	}
	got, err := ResolveManifestExtends(dir, head)
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
