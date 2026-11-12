// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package resolver

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The tests in this file hold what folding a Release extends chain produces.
// The walk that establishes the chain is held next door; everything here starts
// from a chain that is already one and asks what the composition says.
//
// Each test drives the fold rather than reading it, and the reason is specific
// rather than general. The composed-shape rule was first written to read the
// top-level components block, which every link presents empty because a Release
// is normalized as it loads — so the rule was declared, invoked, and structurally
// unable to object, and returned success on the one input it exists to refuse.
// Reading it showed nothing; driving it showed everything. So every refusal below
// is paired with a legal input that must NOT trip it, and every advisory with an
// input that must NOT raise it: an assertion whose failure mode nobody has
// demonstrated proves only that some code ran.
//
// The properties held here:
//
//   - a child inherits its parent's boundary tree and replaces it key by key,
//     down to the first indivisible value, with parent-only keys surviving and
//     child-only keys introduced at every depth.
//   - no metadata field travels, so a child cannot carry its parent's
//     environment into its own provenance.
//   - a key the child writes as null is removed from what it inherited, and the
//     siblings inherited beside it are untouched.
//   - a chain whose links disagree about which routing key their authors wrote
//     is refused, naming both documents and both keys; a chain whose links agree
//     composes, whichever key they agreed on.
//   - an inherited literal output directory is advised about and never halts; an
//     inherited template directory is silent, and so is a directory the child
//     wrote for itself.
//   - folding one chain twice produces one result, key order included.
//   - the extends reference is spent by the fold and is not carried onto the
//     composed Release.
//   - parameters compose across links, with each link's import resolved against
//     that link's own directory before the fold.

// foldFixture is one Release file of a chain fixture: the name it is written
// under and the document it holds.
type foldFixture struct {
	name string
	body string
}

// writeFoldChain writes a chain fixture into dir and returns the first file
// loaded, which is the child a fold starts from.
//
// The fixtures are a slice rather than a map so the files are written in a fixed
// order and a failure reports them in the order they were declared.
func writeFoldChain(t *testing.T, dir string, files ...foldFixture) ResolvedRelease {
	t.Helper()
	if len(files) == 0 {
		t.Fatal("test setup: a chain fixture needs at least one Release")
	}
	for _, f := range files {
		writeReleaseFile(t, dir, f.name, f.body)
	}
	return loadReleaseLink(t, dir, files[0].name)
}

// writeFoldFile writes one supporting fixture file — a parameters file a
// Release imports, rather than a Release — into dir.
func writeFoldFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("test setup: write %s: %v", name, err)
	}
}

// foldNestedMap reports whether a value inside a composed open-content map is
// itself a map, in either storage form such a tree uses.
//
// It is deliberately a second implementation of the reader the merge itself
// uses, and the duplication is the mechanism rather than an oversight: a test
// that asked the merge's own reader which values are maps would agree with that
// reader when it is wrong, and "degraded a partially overridden group to
// wholesale replacement" is exactly the fault it would then be unable to see.
func foldNestedMap(v any) (types.OrderedMap[any], bool) {
	switch n := v.(type) {
	case types.OrderedMap[any]:
		return n, true
	case *types.OrderedMap[any]:
		if n == nil {
			return types.OrderedMap[any]{}, false
		}
		return *n, true
	}
	return types.OrderedMap[any]{}, false
}

// foldValueAt reads the value one path names inside an open-content map,
// walking the nested maps the path runs through, and reports whether the final
// key is present at all.
//
// Present-and-nil and absent are returned distinctly because removal is the
// difference between them: a key written as null must be ABSENT from the
// composition rather than present holding nothing, and a reader collapsing the
// two would pass either way.
func foldValueAt(t *testing.T, m types.OrderedMap[any], path ...string) (any, bool) {
	t.Helper()
	cur := m
	for i, key := range path {
		v, present := cur.Get(key)
		if !present {
			return nil, false
		}
		if i == len(path)-1 {
			return v, true
		}
		nested, isMap := foldNestedMap(v)
		if !isMap {
			t.Fatalf("path %v: %q holds %#v, which is not a map, so the path cannot continue", path, key, v)
		}
		cur = nested
	}
	return nil, false
}

// foldComponentInfra reads one routed component's open-content sizing out of a
// composed Release, failing the test when the boundary or the component the
// fixture named is not there.
func foldComponentInfra(
	t *testing.T,
	composed *types.Release,
	boundary, component string,
) types.OrderedMap[any] {
	t.Helper()
	b, ok := composed.Spec.Boundaries.Get(boundary)
	if !ok {
		t.Fatalf("composed Release carries no boundary %q; it carries %v", boundary, composed.Spec.Boundaries.Keys())
	}
	c, ok := b.Components.Get(component)
	if !ok {
		t.Fatalf("boundary %q carries no component %q; it carries %v", boundary, component, b.Components.Keys())
	}
	return c.Infra
}

// wantValueAt asserts the value at one path inside an open-content map.
func wantValueAt(t *testing.T, m types.OrderedMap[any], want any, path ...string) {
	t.Helper()
	got, present := foldValueAt(t, m, path...)
	if !present {
		t.Errorf("%v is absent from the composition; want %#v", path, want)
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%v = %#v; want %#v", path, got, want)
	}
}

// wantAbsentAt asserts that no key at one path survives into the composition.
func wantAbsentAt(t *testing.T, m types.OrderedMap[any], path ...string) {
	t.Helper()
	got, present := foldValueAt(t, m, path...)
	if present {
		t.Errorf("%v survived the fold holding %#v; a key written null is removed, not emptied", path, got)
	}
}

// The chain the fold tests below compose. The base declares the routing the
// child inherits; the child redeclares a little of it and adds some of its own,
// so every arm of the merge — parent-only, child-only, and both — is exercised
// at the boundary, the component and the open-content level at once.
//
// The values are chosen so no two of them coincide. A parent value that happens
// to equal what the child writes, or what something downstream would default to,
// makes "inherited" and "replaced" render identically, and the assertion then
// passes whichever one the fold did.
const foldBaseRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: base-release
  environment: prod
  version: "2.4.0"
  description: the base a local Release extends
spec:
  aggregate: fold-app
  manifest: prod-manifest
  parameters:
    region: eu-west-1
    tier: gold
    db:
      size: 100Gi
      replicas: 3
  boundaries:
    edge:
      syncWave: 7
      components:
        web:
          infra:
            replicas: 9
            resources:
              cpu: "4"
              memory: 16Gi
        db:
          infra:
            replicas: 3
            storageClass: fast-ssd
    internal:
      components:
        worker:
          infra:
            replicas: 5
`

const foldChildRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: child-release
  environment: local
  version: "1.0.0"
spec:
  extends: ./base.yaml
  manifest: local-manifest
  parameters:
    tier: local
    logLevel: debug
    db:
      size: 10Gi
  boundaries:
    edge:
      components:
        web:
          infra:
            replicas: 1
            resources:
              memory: 512Mi
        cache:
          infra:
            replicas: 2
    perimeter:
      components:
        proxy:
          infra:
            replicas: 4
`

// TestResolveReleaseExtends_NoExtends_ReturnedUnchanged — a Release declaring no
// extends reference comes back exactly as it was loaded.
//
// It is the control every assertion below needs. Without it, a fold that
// returned its input untouched in every case would satisfy the "parent-only key
// survives" rows by doing nothing at all, and a fold that refused every chain
// would satisfy the refusal rows the same way.
func TestResolveReleaseExtends_NoExtends_ReturnedUnchanged(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	head := writeFoldChain(t, dir, foldFixture{"solo.yaml", releaseDoc("solo-release", "")})

	composed, advisories, err := ResolveReleaseExtends("", dir, head)
	if err != nil {
		t.Fatalf("a Release with no extends must fold to itself; got: %v", err)
	}
	if composed != head.Release {
		t.Errorf("a Release with no extends must be returned unchanged (same pointer)")
	}
	if len(advisories) != 0 {
		t.Errorf("a Release with no extends raised %d advisories; want none: %v", len(advisories), advisories)
	}
}

// TestResolveReleaseExtends_FoldsPerKeyToTheFirstIndivisibleValue holds the fold
// itself: a child inherits its parent's boundary tree and replaces it key by
// key, all the way down.
//
// 🔴 All three arms are asserted at every level, and a row failing tells them
// apart. Parent-only must SURVIVE — a fold that simply took the child would pass
// every child-wins assertion and lose the entire inherited topology, which is the
// duplication this feature exists to remove. Child-only must be INTRODUCED — a
// fold that simply took the parent would pass every parent-only assertion. And a
// key both declare must take the CHILD's, which is the only arm a fold doing
// neither of those could still get right.
//
// The nested resources map is the discriminating case for depth: the child
// writes one of its two keys, so a fold that replaced the group whole rather
// than composing it would drop the sibling the child never mentioned while every
// shallower assertion kept passing.
func TestResolveReleaseExtends_FoldsPerKeyToTheFirstIndivisibleValue(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	head := writeFoldChain(t, dir,
		foldFixture{"child.yaml", foldChildRelease},
		foldFixture{"base.yaml", foldBaseRelease},
	)

	composed, _, err := ResolveReleaseExtends("", dir, head)
	if err != nil {
		t.Fatalf("the chain must fold; got: %v", err)
	}

	// Boundaries compose per name, base author order first and child-only names
	// appended in theirs. The order is asserted and not merely the membership:
	// it reaches the rendered artifact, so a fold rebuilding it from an unordered
	// walk would be non-deterministic while every membership check still passed.
	wantBoundaries := []string{"edge", "internal", "perimeter"}
	if got := composed.Spec.Boundaries.Keys(); !reflect.DeepEqual(got, wantBoundaries) {
		t.Errorf("composed boundaries = %v; want %v", got, wantBoundaries)
	}

	// A boundary only the parent declares survives whole, components and all.
	internalWorker := foldComponentInfra(t, composed, "internal", "worker")
	wantValueAt(t, internalWorker, 5, "replicas")

	// A boundary only the child declares is introduced.
	perimeterProxy := foldComponentInfra(t, composed, "perimeter", "proxy")
	wantValueAt(t, perimeterProxy, 4, "replicas")

	// A boundary both declare composes per key rather than being replaced: the
	// child restated neither the sync wave nor the component list, and both
	// stand.
	edge, ok := composed.Spec.Boundaries.Get("edge")
	if !ok {
		t.Fatal("composed Release lost the boundary both links declared")
	}
	if edge.SyncWave != 7 {
		t.Errorf("edge syncWave = %d; want the inherited 7 — a scalar the child never wrote", edge.SyncWave)
	}
	wantEdgeComponents := []string{"web", "db", "cache"}
	if got := edge.Components.Keys(); !reflect.DeepEqual(got, wantEdgeComponents) {
		t.Errorf("edge components = %v; want %v", got, wantEdgeComponents)
	}

	// A component only the parent declares survives whole.
	edgeDB := foldComponentInfra(t, composed, "edge", "db")
	wantValueAt(t, edgeDB, 3, "replicas")
	wantValueAt(t, edgeDB, "fast-ssd", "storageClass")

	// A component only the child declares is introduced.
	edgeCache := foldComponentInfra(t, composed, "edge", "cache")
	wantValueAt(t, edgeCache, 2, "replicas")

	// A component both declare composes per key, at every depth.
	edgeWeb := foldComponentInfra(t, composed, "edge", "web")
	wantValueAt(t, edgeWeb, 1, "replicas")                            // both wrote it: the child's
	wantValueAt(t, edgeWeb, "512Mi", "resources", "memory")           // both wrote it, nested
	wantValueAt(t, edgeWeb, "4", "resources", "cpu")                  // parent-only, nested
	if _, present := foldValueAt(t, edgeWeb, "resources"); !present { // the group itself survived
		t.Error("the nested resources group vanished from the composition")
	}

	// Parameters compose on the same three arms, through the merge that already
	// serves them rather than a second one written here.
	params := composed.Spec.Parameters.Inline
	wantValueAt(t, params, "eu-west-1", "region") // parent-only
	wantValueAt(t, params, "debug", "logLevel")   // child-only
	wantValueAt(t, params, "local", "tier")       // both: the child's
	wantValueAt(t, params, "10Gi", "db", "size")  // both, nested: the child's
	wantValueAt(t, params, 3, "db", "replicas")   // parent-only, nested

	// The Manifest pointer is one indivisible value, so the child's replaces.
	// The Aggregate pointer is the one the child is forbidden to declare, so the
	// parent's is what the composition must carry — this is the whole reason the
	// canonical child can be four lines.
	if composed.Spec.Manifest != "local-manifest" {
		t.Errorf("composed manifest = %q; want the child's local-manifest", composed.Spec.Manifest)
	}
	if composed.Spec.Aggregate != "fold-app" {
		t.Errorf("composed aggregate = %q; want the inherited fold-app", composed.Spec.Aggregate)
	}
}

// TestResolveReleaseExtends_IdentityAndProvenanceDoNotInherit holds that no
// metadata field travels along a chain.
//
// The whole of metadata is compared rather than the environment alone, because
// "environment does not inherit" is satisfiable one field at a time while a
// description, a version or a keyword list quietly arrives from the parent. The
// base is given a metadata block differing from the child's in every optional
// field precisely so that comparison cannot pass by the two being alike.
//
// 🔴 The environment is named separately as well, and it is the sharp case: it
// is required of every Release and reaches the provenance labels of everything
// rendered, so a child silently carrying its parent's would be wrong in a way
// nothing downstream can report — a parent's environment is a perfectly
// well-formed value to find there.
func TestResolveReleaseExtends_IdentityAndProvenanceDoNotInherit(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	head := writeFoldChain(t, dir,
		foldFixture{"child.yaml", foldChildRelease},
		foldFixture{"base.yaml", foldBaseRelease},
	)
	// The fixture is the control: if the two metadata blocks were alike, the
	// comparison below would hold for a fold that inherited everything.
	base := loadReleaseLink(t, dir, "base.yaml")
	if reflect.DeepEqual(base.Release.Metadata, head.Release.Metadata) {
		t.Fatal("test setup: the two links must declare different metadata, or this proves nothing")
	}

	composed, _, err := ResolveReleaseExtends("", dir, head)
	if err != nil {
		t.Fatalf("the chain must fold; got: %v", err)
	}

	if !reflect.DeepEqual(composed.Metadata, head.Release.Metadata) {
		t.Errorf("composed metadata =\n\t%#v\nwant the child's own\n\t%#v", composed.Metadata, head.Release.Metadata)
	}
	if composed.Metadata.Environment != "local" {
		t.Errorf("composed environment = %q; want the child's own local", composed.Metadata.Environment)
	}
	if composed.Metadata.Environment == base.Release.Metadata.Environment {
		t.Errorf("composed environment = %q; it came from the parent", composed.Metadata.Environment)
	}
	if composed.Metadata.Version != "1.0.0" {
		t.Errorf("composed version = %q; want the child's own 1.0.0", composed.Metadata.Version)
	}
	if composed.Metadata.Description != "" {
		t.Errorf("composed description = %q; the child declared none, so none may travel", composed.Metadata.Description)
	}
}

// TestResolveReleaseExtends_ExtendsIsSpentByTheFold holds that the composed
// Release carries no extends reference.
//
// Leaving it would describe the composition as still extending something while
// it also carries the Aggregate pointer it inherited — a pair no author may
// write, since declaring extends forbids declaring an aggregate. A consumer
// re-reading such a value would follow the chain a second time.
func TestResolveReleaseExtends_ExtendsIsSpentByTheFold(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	head := writeFoldChain(t, dir,
		foldFixture{"child.yaml", foldChildRelease},
		foldFixture{"base.yaml", foldBaseRelease},
	)
	// The control: the input genuinely declares one, so the assertion below is
	// not satisfied by a fixture that never had a reference to spend.
	if head.Release.Spec.Extends == "" {
		t.Fatal("test setup: the child must declare an extends reference")
	}

	composed, _, err := ResolveReleaseExtends("", dir, head)
	if err != nil {
		t.Fatalf("the chain must fold; got: %v", err)
	}
	if composed.Spec.Extends != "" {
		t.Errorf("composed Release still carries extends %q; the reference is spent once folded", composed.Spec.Extends)
	}
	// The pair that would then be unwritable: an inherited Aggregate pointer
	// beside a live extends reference.
	if composed.Spec.Aggregate == "" {
		t.Error("composed Release carries no Aggregate pointer; the child inherits its parent's")
	}
}

// TestResolveReleaseExtends_NullRemovesWhatWasInheritedAndLeavesSiblings holds
// the removal arm of the chain rule on both open-content surfaces a Release
// carries: a routed component's sizing, and the parameter set.
//
// 🔴 Every row asserts a sibling as well as the removal, and that pairing is the
// test. A fold that dropped the whole inherited group on seeing any null would
// satisfy every removal assertion on its own; only the surviving sibling
// separates "removed the key" from "replaced the group".
//
// The fixture values are chosen so that removal and non-removal cannot render
// alike. A parent value equal to what a consumer would otherwise supply makes
// the two outcomes indistinguishable downstream, and an assertion written
// against such a fixture passes whichever one the fold performed.
func TestResolveReleaseExtends_NullRemovesWhatWasInheritedAndLeavesSiblings(t *testing.T) {
	t.Parallel()

	t.Run("a routed component's sizing", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		head := writeFoldChain(t, dir,
			foldFixture{"child.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: child-release
  environment: local
  version: "1.0.0"
spec:
  extends: ./base.yaml
  boundaries:
    edge:
      components:
        web:
          infra:
            replicas: null
            resources:
              cpu: null
`},
			foldFixture{"base.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: base-release
  environment: prod
  version: "1.0.0"
spec:
  aggregate: fold-app
  manifest: prod-manifest
  boundaries:
    edge:
      components:
        web:
          infra:
            replicas: 9
            storageClass: fast-ssd
            resources:
              cpu: "4"
              memory: 16Gi
        db:
          infra:
            replicas: 3
`},
		)

		composed, _, err := ResolveReleaseExtends("", dir, head)
		if err != nil {
			t.Fatalf("a chain removing an inherited key must still fold; got: %v", err)
		}

		web := foldComponentInfra(t, composed, "edge", "web")
		wantAbsentAt(t, web, "replicas")
		wantAbsentAt(t, web, "resources", "cpu")
		// The siblings, at both depths and at component granularity.
		wantValueAt(t, web, "fast-ssd", "storageClass")
		wantValueAt(t, web, "16Gi", "resources", "memory")
		db := foldComponentInfra(t, composed, "edge", "db")
		wantValueAt(t, db, 3, "replicas")
	})

	t.Run("the parameter set", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		head := writeFoldChain(t, dir,
			foldFixture{"child.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: child-release
  environment: local
  version: "1.0.0"
spec:
  extends: ./base.yaml
  parameters:
    tier: null
    db:
      size: null
`},
			foldFixture{"base.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: base-release
  environment: prod
  version: "1.0.0"
spec:
  aggregate: fold-app
  manifest: prod-manifest
  parameters:
    tier: gold
    region: eu-west-1
    db:
      size: 100Gi
      replicas: 3
`},
		)

		composed, _, err := ResolveReleaseExtends("", dir, head)
		if err != nil {
			t.Fatalf("a chain removing an inherited parameter must still fold; got: %v", err)
		}

		params := composed.Spec.Parameters.Inline
		wantAbsentAt(t, params, "tier")
		wantAbsentAt(t, params, "db", "size")
		wantValueAt(t, params, "eu-west-1", "region")
		wantValueAt(t, params, 3, "db", "replicas")
	})
}

// TestResolveReleaseExtends_ChainDisagreeingAboutRoutingShapeIsRefused holds the
// refusal of a chain whose links' authors wrote different routing keys, and the
// legal chains it must leave alone.
//
// 🔴 The three rows are one A/B and no row is worth anything alone. The refusing
// row says a mixed chain halts. The two composing rows say the rule is about the
// MIX: a chain whose links all wrote top-level components composes, and so does
// one whose links all wrote named boundaries. A rule that refused every chain
// passes the first row and fails the other two.
//
// The composing-components row is the one that catches the fault this rule was
// first written with. A Release is normalized as it loads — a top-level
// components block is folded into a boundary named default and the block is
// cleared — so a rule reading that block finds it empty on every link, clears
// every chain, and reports nothing. Both composing rows would pass under such a
// rule; only the refusing row fails, and only when driven.
//
// The condition is matched through its exported accessor rather than against its
// registered code: a string comparison type-checks, reads like a match, and
// silently stops matching the day the condition is re-registered.
func TestResolveReleaseExtends_ChainDisagreeingAboutRoutingShapeIsRefused(t *testing.T) {
	t.Parallel()

	const boundariesBase = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: base-release
  environment: prod
  version: "1.0.0"
spec:
  aggregate: fold-app
  manifest: prod-manifest
  boundaries:
    edge:
      components:
        web:
          infra:
            replicas: 3
`
	const componentsBase = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: base-release
  environment: prod
  version: "1.0.0"
spec:
  aggregate: fold-app
  manifest: prod-manifest
  components:
    web:
      infra:
        replicas: 3
`
	const boundariesChild = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: child-release
  environment: local
  version: "1.0.0"
spec:
  extends: ./base.yaml
  boundaries:
    edge:
      components:
        web:
          infra:
            replicas: 1
`
	const componentsChild = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: child-release
  environment: local
  version: "1.0.0"
spec:
  extends: ./base.yaml
  components:
    web:
      infra:
        replicas: 1
`

	t.Run("links disagreeing about the routing key are refused", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		head := writeFoldChain(t, dir,
			foldFixture{"child.yaml", componentsChild},
			foldFixture{"base.yaml", boundariesBase},
		)

		composed, _, err := ResolveReleaseExtends("", dir, head)
		if err == nil {
			t.Fatal("a chain whose links wrote different routing keys must halt")
		}
		if composed != nil {
			t.Error("a refused chain handed back a composition; nothing downstream may read one")
		}
		if !errors.Is(err, ErrReleaseChainComposesBothRoutingKeys()) {
			t.Errorf("routing-shape refusal = %v; want the composed-routing-shape condition", err)
		}
		// The condition is this decision's own. The chain-structure conditions
		// describe a cycle, a depth bound or a parent that does not resolve, and
		// a consumer matching on one of those must be able to tell them apart
		// from a disagreement about what the authors wrote.
		for _, wrong := range []struct {
			name      string
			condition error
		}{
			{"cycle", validator.ErrCyclicExtension()},
			{"over-deep chain", validator.ErrExtendsChainTooDeep()},
			{"unresolvable parent", validator.ErrInheritanceMerge()},
		} {
			if errors.Is(err, wrong.condition) {
				t.Errorf("routing-shape refusal also reports the %s condition: %v", wrong.name, err)
			}
		}
		// Both documents and both keys are named. Neither document is at fault
		// on its own — the envelope accepts each as written — so a message
		// naming only the one the resolution started from sends its author to
		// the file with nothing wrong in it.
		base := filepath.Join(dir, "base.yaml")
		child := filepath.Join(dir, "child.yaml")
		for _, want := range []string{base, child, "boundaries declared by", "components declared by"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("routing-shape refusal = %q; want it to name %q", err.Error(), want)
			}
		}
	})

	for _, tc := range []struct {
		name         string
		child, base  string
		wantBoundary string
	}{
		{
			name:         "links all writing top-level components compose",
			child:        componentsChild,
			base:         componentsBase,
			wantBoundary: "default",
		},
		{
			name:         "links all writing named boundaries compose",
			child:        boundariesChild,
			base:         boundariesBase,
			wantBoundary: "edge",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeFoldChain(t, dir,
				foldFixture{"child.yaml", tc.child},
				foldFixture{"base.yaml", tc.base},
			)

			composed, _, err := ResolveReleaseExtends("", dir, head)
			if err != nil {
				t.Fatalf("a chain whose links agree about the routing key must compose; got: %v", err)
			}
			if _, ok := composed.Spec.Boundaries.Get(tc.wantBoundary); !ok {
				t.Fatalf("composed boundaries = %v; want %q among them", composed.Spec.Boundaries.Keys(), tc.wantBoundary)
			}
			// Composing is asserted by what the composition CARRIES, not by the
			// absence of an error: a fold returning an empty Release would pass
			// a bare no-error check while losing the chain entirely.
			web := foldComponentInfra(t, composed, tc.wantBoundary, "web")
			wantValueAt(t, web, 1, "replicas")
			// Normalization has already moved a top-level components block into
			// the synthesized boundary, so the composition presents boundaries
			// whichever key the authors wrote.
			if composed.Spec.Components.Len() != 0 {
				t.Errorf("composed top-level components = %v; every component is routed through a boundary", composed.Spec.Components.Keys())
			}
		})
	}
}

// TestResolveReleaseExtends_InheritedLiteralDirectoryAdvisesWithoutHalting holds
// the whole of the inherited-directory reading: which shape raises it, which two
// do not, and that none of them stops the composition.
//
// 🔴 The rows are one A/B and the pair is the test. The literal row alone would
// be satisfied by a reading that advised about every inherited boundary; the
// template row alone by one that advised about none. The third row closes it
// from the other side: a literal directory the CHILD wrote raises nothing,
// because the reading is about what was inherited rather than about the value
// being literal.
//
// 🔴 Every row asserts the composition still succeeded. The advisory reports a
// path an author may perfectly well have meant — a child swapping only its
// rendering Recipes writes where its parent writes on purpose — so a reading
// that halted would refuse legal input on an inference about intent.
func TestResolveReleaseExtends_InheritedLiteralDirectoryAdvisesWithoutHalting(t *testing.T) {
	t.Parallel()

	baseWith := func(directory string) string {
		return `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: base-release
  environment: prod
  version: "1.0.0"
spec:
  aggregate: fold-app
  manifest: prod-manifest
  boundaries:
    edge:
      directory: ` + directory + `
      components:
        web:
          infra:
            replicas: 3
`
	}
	const childInheritingEverything = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: child-release
  environment: local
  version: "1.0.0"
spec:
  extends: ./base.yaml
`
	const childWritingItsOwnDirectory = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: child-release
  environment: local
  version: "1.0.0"
spec:
  extends: ./base.yaml
  boundaries:
    edge:
      directory: dist/local
`

	for _, tc := range []struct {
		name          string
		baseDirectory string
		child         string
		wantAdvisory  bool
		wantDirectory string
	}{
		{
			name:          "an inherited literal directory is advised about",
			baseDirectory: `"dist/prod-eu-west"`,
			child:         childInheritingEverything,
			wantAdvisory:  true,
			wantDirectory: "dist/prod-eu-west",
		},
		{
			name:          "an inherited template directory is not",
			baseDirectory: `"dist/{{ .parameters.region }}"`,
			child:         childInheritingEverything,
			wantAdvisory:  false,
			wantDirectory: "dist/{{ .parameters.region }}",
		},
		{
			name:          "a literal directory the child wrote for itself is not",
			baseDirectory: `"dist/prod-eu-west"`,
			child:         childWritingItsOwnDirectory,
			wantAdvisory:  false,
			wantDirectory: "dist/local",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			head := writeFoldChain(t, dir,
				foldFixture{"child.yaml", tc.child},
				foldFixture{"base.yaml", baseWith(tc.baseDirectory)},
			)

			composed, advisories, err := ResolveReleaseExtends("", dir, head)
			if err != nil {
				t.Fatalf("the reading must not halt the composition; got: %v", err)
			}
			if composed == nil {
				t.Fatal("the composition is absent; the reading advises, it does not refuse")
			}
			// The composition is intact whether or not anything was advised
			// about, which is what "reported and passed over" means.
			edge, ok := composed.Spec.Boundaries.Get("edge")
			if !ok {
				t.Fatalf("composed boundaries = %v; want edge among them", composed.Spec.Boundaries.Keys())
			}
			if edge.Directory != tc.wantDirectory {
				t.Errorf("composed edge directory = %q; want %q", edge.Directory, tc.wantDirectory)
			}
			web := foldComponentInfra(t, composed, "edge", "web")
			wantValueAt(t, web, 3, "replicas")

			if !tc.wantAdvisory {
				if len(advisories) != 0 {
					t.Errorf("advisories = %v; want none", advisories)
				}
				return
			}
			if len(advisories) != 1 {
				t.Fatalf("advisories = %v; want exactly one, naming the boundary", advisories)
			}
			advisory := advisories[0]
			if !errors.Is(advisory, ErrInheritedBoundaryDirectoryIsLiteral()) {
				t.Errorf("advisory = %v; want the inherited-literal-directory condition", advisory)
			}
			// It names the boundary rather than the composition, because the
			// boundary is what an author would change.
			if !strings.Contains(advisory.Error(), "edge") {
				t.Errorf("advisory = %q; want it to name the boundary edge", advisory.Error())
			}
		})
	}
}

// TestResolveReleaseExtends_FoldingOneChainTwiceProducesOneResult holds the
// determinism the composed Release owes.
//
// The chain is loaded twice from the same files rather than folded twice from
// one loaded value, so what is compared is the fold as a function of the
// documents rather than of one in-memory tree it might share.
//
// The whole composed value is compared, and key order with it. An ordered map
// carries its key order in a field, so a deep comparison catches a fold that
// rebuilt one from an unordered walk — and the key orders are named separately
// afterwards so a failure says which ordering moved rather than printing two
// whole Releases.
func TestResolveReleaseExtends_FoldingOneChainTwiceProducesOneResult(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFoldChain(t, dir,
		foldFixture{"child.yaml", foldChildRelease},
		foldFixture{"base.yaml", foldBaseRelease},
	)

	fold := func() *types.Release {
		t.Helper()
		head := loadReleaseLink(t, dir, "child.yaml")
		composed, _, err := ResolveReleaseExtends("", dir, head)
		if err != nil {
			t.Fatalf("the chain must fold; got: %v", err)
		}
		return composed
	}

	first, second := fold(), fold()
	if first == second {
		t.Fatal("test setup: the two folds returned one value, so nothing was compared")
	}

	// Both orders are asserted against a spelled-out expectation rather than
	// only against each other. Two folds agreeing is satisfied by two runs that
	// happened to walk one Go map the same way, and a map walk is free to differ
	// on the next run and on the next machine — so the concrete order is what
	// makes a single run capable of failing, and the agreement is what catches
	// the rest.
	//
	// The parameter order is sorted rather than authored, because the merge the
	// fold composes with works in plain maps and carries no author order to
	// keep. Sorted is the only order that is the same every time, which is what
	// the composed Release owes.
	for _, tc := range []struct {
		name string
		want []string
		got  [2][]string
	}{
		{
			name: "boundaries, base author order then child-only names",
			want: []string{"edge", "internal", "perimeter"},
			got:  [2][]string{first.Spec.Boundaries.Keys(), second.Spec.Boundaries.Keys()},
		},
		{
			name: "parameters, sorted",
			want: []string{"db", "logLevel", "region", "tier"},
			got:  [2][]string{first.Spec.Parameters.Inline.Keys(), second.Spec.Parameters.Inline.Keys()},
		},
	} {
		for fold, got := range tc.got {
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("%s: fold %d = %v; want %v", tc.name, fold+1, got, tc.want)
			}
		}
	}

	if !reflect.DeepEqual(first, second) {
		t.Errorf("folding one chain twice produced two results:\n\t%#v\n\t%#v", first, second)
	}
}

// TestResolveReleaseExtends_ParametersComposeAcrossLinksWithImportsResolvedPerLink
// holds how a parameter set composes along a chain, including the link that
// supplies its parameters from a file.
//
// 🔴 The import is resolved against the directory holding the link that declared
// it, and the fixture is built to prove it rather than to assume it: the base
// sits in a sub-directory beside the file it imports, and a decoy of the same
// name sits beside the CHILD. A fold resolving the import from the directory the
// resolution started in finds the decoy and composes real values from the wrong
// file — which an assertion on "the import resolved" would accept, and which the
// distinct decoy values are what catch.
//
// It also holds that the two authoring modes compose across links while "inline
// or imported, never both" stays a property of each authored document: the base
// imports, the child writes inline, and neither document breaks the rule its own
// author was held to.
func TestResolveReleaseExtends_ParametersComposeAcrossLinksWithImportsResolvedPerLink(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	baseDir := filepath.Join(dir, "base")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		t.Fatalf("test setup: mkdir: %v", err)
	}

	// Beside the base: the file its import names.
	writeFoldFile(t, baseDir, "params.yaml", `region: eu-west-1
tier: gold
db:
  size: 100Gi
  replicas: 3
`)
	// Beside the child: a decoy of the same name, which nothing may read.
	writeFoldFile(t, dir, "params.yaml", `region: DECOY-REGION
tier: DECOY-TIER
db:
  size: DECOY-SIZE
  replicas: 999
`)
	writeReleaseFile(t, baseDir, "base.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: base-release
  environment: prod
  version: "1.0.0"
spec:
  aggregate: fold-app
  manifest: prod-manifest
  parameters:
    $import: ./params.yaml
`)
	writeReleaseFile(t, dir, "child.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: child-release
  environment: local
  version: "1.0.0"
spec:
  extends: ./base/base.yaml
  parameters:
    tier: local
    db:
      size: 10Gi
`)
	head := loadReleaseLink(t, dir, "child.yaml")
	// The control for the two modes: the base carries only an import and the
	// child only inline content, which is the exclusivity each author is held
	// to. A fixture where either link carried both would be testing a document
	// the envelope refuses.
	base := loadReleaseLink(t, baseDir, "base.yaml")
	if base.Release.Spec.Parameters.Import == "" || base.Release.Spec.Parameters.Inline.Len() != 0 {
		t.Fatal("test setup: the base must supply its parameters by import alone")
	}
	if head.Release.Spec.Parameters.Import != "" || head.Release.Spec.Parameters.Inline.Len() == 0 {
		t.Fatal("test setup: the child must supply its parameters inline alone")
	}

	composed, _, err := ResolveReleaseExtends("", dir, head)
	if err != nil {
		t.Fatalf("a chain whose base imports its parameters must fold; got: %v", err)
	}

	params := composed.Spec.Parameters.Inline
	wantValueAt(t, params, "eu-west-1", "region") // imported, parent-only
	wantValueAt(t, params, "local", "tier")       // both: the child's
	wantValueAt(t, params, "10Gi", "db", "size")  // both, nested: the child's
	wantValueAt(t, params, 3, "db", "replicas")   // imported, nested, parent-only

	// The decoy, named explicitly: a fold reading the wrong directory composes
	// a perfectly well-formed parameter set out of the wrong file, and only its
	// distinct values say so.
	for _, decoyed := range []struct {
		path  []string
		decoy any
	}{
		{[]string{"region"}, "DECOY-REGION"},
		{[]string{"db", "replicas"}, 999},
	} {
		got, present := foldValueAt(t, params, decoyed.path...)
		if present && reflect.DeepEqual(got, decoyed.decoy) {
			t.Errorf("%v = %#v, which is the decoy beside the child: the import resolved against the wrong directory",
				decoyed.path, got)
		}
	}

	// The composed block carries its parameters inline, so the import is spent
	// the way the extends reference is.
	if composed.Spec.Parameters.Import != "" {
		t.Errorf("composed parameters still carry the import %q; it is resolved before the fold",
			composed.Spec.Parameters.Import)
	}
}

// TestParametersBlockModesAreExclusivePerDocument pins the premise the chain's
// removal arm rests on.
//
// The parameter merge removes a key an author wrote as null, and that arm is
// reached only when both a base and an overlay are in hand. Along a chain that
// is the ordinary case — each link contributes and the next composes onto it.
// At the one other place the merge is called, the two sides come from a single
// document's parameters block, and the block's two authoring modes are
// exclusive: a document declaring an import declares no inline content and vice
// versa, so one of the merge's single-sided arms always answers and the removal
// arm is never reached from there.
//
// That exclusivity is what makes the removal arm safe to have added, so it is
// held here rather than left as a reading of the decoder. The refusing row is
// the other half: a document mixing the two modes is refused outright, which is
// what keeps the property from being merely conventional.
//
// It lives beside the fold's tests because the fold is what the arm was added
// for, and a later reader asking why the arm is safe is reading these.
func TestParametersBlockModesAreExclusivePerDocument(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		parameters  string
		wantRefused bool
		wantImport  string
		wantInline  bool
	}{
		{
			name:       "inline content alone",
			parameters: "\n    tier: gold\n    region: eu-west-1",
			wantInline: true,
		},
		{
			name:       "an import alone",
			parameters: "\n    $import: ./params.yaml",
			wantImport: "./params.yaml",
		},
		{
			name:        "an import beside inline content is refused",
			parameters:  "\n    $import: ./params.yaml\n    tier: gold",
			wantRefused: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			writeReleaseFile(t, dir, "release.yaml", `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: solo-release
  environment: dev
  version: "1.0.0"
spec:
  aggregate: fold-app
  manifest: prod-manifest
  parameters:`+tc.parameters+"\n")

			rel, err := LoadRelease(filepath.Join(dir, "release.yaml"))
			if tc.wantRefused {
				if err == nil {
					t.Fatalf("a parameters block mixing the two modes must be refused; it loaded as %#v",
						rel.Spec.Parameters)
				}
				return
			}
			if err != nil {
				t.Fatalf("a single-mode parameters block must load; got: %v", err)
			}
			if got := rel.Spec.Parameters.Import; got != tc.wantImport {
				t.Errorf("import = %q; want %q", got, tc.wantImport)
			}
			if got := rel.Spec.Parameters.Inline.Len() > 0; got != tc.wantInline {
				t.Errorf("inline content present = %v; want %v", got, tc.wantInline)
			}
			// The property itself, stated once for every accepted row: the two
			// modes are never both populated, so a merge over one document's
			// block always has a nil side.
			if rel.Spec.Parameters.Import != "" && rel.Spec.Parameters.Inline.Len() > 0 {
				t.Error("the parameters block carries both modes; the merge over one document would then reach its two-sided arm")
			}
		})
	}
}
