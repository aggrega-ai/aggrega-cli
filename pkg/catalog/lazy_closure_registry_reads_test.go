// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"context"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// What this file proves about the lazy view's registry tier, and why each proof
// is built the way it is.
//
// The view reads a closure's entities after the walk has resolved them, at
// several places: library discovery, closure-scoped validation, the overlap lint
// and the extends gate. Each read runs the Component and Feature cascades, whose
// last tier is a registry. Two properties are asserted, and both are invisible
// from the outside, because a lookup that waits on a registry and one that does
// not return the same answer once the registry has answered.
//
// The first is which references reach the registry at all. A reference the walk
// collected as a Recipe, a plugin or one of the Release's own documents was
// resolved as that kind, so asking a registry for it as a Component or a Feature
// is a round trip that can only miss. The set of what was asked is compared
// against the set expected and the difference is named, rather than counted,
// because two counts short by one each still agree.
//
// The second is that the caller's context reaches the registry call. It is read
// by marking the context with a value and having the registry record, per call,
// whether the mark arrived: a call that received a background context instead
// carries no mark. That needs no clock, and a plain accessor called alongside is
// the control that shows the mark is not simply always present.

// registryCall is one request the recording registry received: the identity it
// was asked for and the kind it was asked as, and whether the caller's mark was
// on the context it was asked under.
type registryCall struct {
	identity string
	kind     string
	marked   bool
}

// callerMark is the context key the tests mark a caller's context with.
type callerMark struct{}

// kindRecordingPuller stands in for a registry that holds nothing. It records
// every entity request it receives and answers each as not found, which is what a
// registry answers for an identity it does not serve.
type kindRecordingPuller struct {
	mu    sync.Mutex
	calls []registryCall
}

func (p *kindRecordingPuller) Pull(ctx context.Context, reference, _ string) (*oci.PullResult, error) {
	p.record(ctx, reference, "")
	return nil, oci.ErrFetchNotFound()
}

func (p *kindRecordingPuller) PullEntity(ctx context.Context, identity, kind, _ string) (*oci.PullResult, string, error) {
	p.record(ctx, identity, kind)
	return nil, "", oci.ErrFetchNotFound()
}

func (p *kindRecordingPuller) record(ctx context.Context, identity, kind string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, registryCall{identity: identity, kind: kind, marked: ctx.Value(callerMark{}) != nil})
}

// asked returns each identity/kind pair the registry was asked for, once each,
// in sorted order.
func (p *kindRecordingPuller) asked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []string
	for _, c := range p.calls {
		pair := c.kind + " " + c.identity
		if !slices.Contains(out, pair) {
			out = append(out, pair)
		}
	}
	slices.Sort(out)
	return out
}

// unmarked returns each call that arrived without the caller's mark.
func (p *kindRecordingPuller) unmarked() []registryCall {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []registryCall
	for _, c := range p.calls {
		if !c.marked {
			out = append(out, c)
		}
	}
	return out
}

func (p *kindRecordingPuller) reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = nil
}

// extendingChildYAML is a local Component whose extends chain names a parent no
// local tier holds, so the chain's parent is the one read that reaches a registry.
const extendingChildYAML = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/extending-child
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: server
spec:
  extends: demo/remote-parent
  schema: inherited
`

// difference returns the members of a missing from b, for a failure that names
// what differs rather than how many.
func difference(a, b []string) []string {
	var out []string
	for _, s := range a {
		if !slices.Contains(b, s) {
			out = append(out, s)
		}
	}
	return out
}

// TestAClosureEntityIsAskedOfARegistryOnlyAsTheKindItWasCollectedAs drives every
// closure-scoped read of the lazy view over a closure whose references were
// collected as every kind the walk records, none of which a local tier holds, and
// reads back what the registry was asked.
//
// Each reference collected as a Component is asked as a Component and nothing
// else, each collected as a Feature as a Feature, and the rest — a Manifest
// carrying no kind, the Release's own Aggregate, a Recipe, a plugin — are never
// asked at all. The control at the end asks the plain accessor for the Recipe's
// reference and must reach the registry: it shows the registry was reachable for
// that reference all along, so its absence above is the gate's doing, and that a
// read with the registry closed left no remembered miss behind to answer for it.
//
// A planted defect makes this fail: open the registry tier in closureComponent
// whatever the reference's kind, and the Manifest, the Aggregate, the Recipe and
// the plugin all appear in the asked set as Components.
func TestAClosureEntityIsAskedOfARegistryOnlyAsTheKindItWasCollectedAs(t *testing.T) {
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	// The catalog directory holds an entity the closure does not reach, so the
	// directory index is real and every closure reference misses it.
	writeCatalogFile(t, catalogDir, "extending-child.yaml", extendingChildYAML)

	puller := &kindRecordingPuller{}
	view := NewLazyLookup(ws, ".aggrega/cache", []string{catalogDir}, puller)

	cl := closure.Closure{Entities: []closure.ResolvedRef{
		{URI: "demo/app-manifest"},
		{URI: "demo/app-aggregate", KindHint: closure.ReleaseAggregateKindHint},
		{URI: "demo/server-recipe", KindHint: "recipe"},
		{URI: "demo/plugin-entity", KindHint: "plugin"},
		{URI: "demo/absent-component", KindHint: "component"},
		{URI: "demo/absent-feature", KindHint: "feature"},
	}}
	ctx := t.Context()

	_ = view.ValidateClosureScopedPillars(ctx, cl)
	_ = view.ClosureLintWarnings(ctx, cl)
	_ = view.ResolveClosureExtends(ctx, cl)
	for _, ref := range cl.Entities {
		view.ClosureComponent(ctx, ref)
		view.ClosureFeature(ctx, ref)
	}

	want := []string{"Component demo/absent-component", "Feature demo/absent-feature"}
	got := puller.asked()
	if extra := difference(got, want); len(extra) > 0 {
		t.Errorf("the registry was asked for references the walk did not collect as that kind: %v", extra)
	}
	if missing := difference(want, got); len(missing) > 0 {
		t.Errorf("the registry was never asked for references the walk collected as that kind, "+
			"so the reads above did not reach the registry tier at all: %v", missing)
	}

	puller.reset()
	view.ComponentContext(ctx, "demo/server-recipe")
	if got := puller.asked(); !slices.Equal(got, []string{"Component demo/server-recipe"}) {
		t.Errorf("the plain Component read of the Recipe's reference asked the registry for %v; want exactly a "+
			"Component read of it — without one, the absence above proves nothing about the gate", got)
	}
}

// TestTheCallersContextReachesTheRegistryReadOfEveryClosureRead holds each read
// that can reach a registry to the context its caller handed it: the
// context-carrying accessors, the catalog lookup bound to a context, the
// closure-entity reads, and the extends chain the closure-scoped passes merge —
// whose parent, held by no local tier, is the read that leaves the process.
//
// The control is the plain Component accessor, which reads under a background
// context: its request must arrive WITHOUT the mark, which shows the mark is
// carried rather than present on every request.
//
// A planted defect makes this fail: hand context.Background() to the parent
// lookup in componentContext, and the extends rows arrive unmarked.
func TestTheCallersContextReachesTheRegistryReadOfEveryClosureRead(t *testing.T) {
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	writeCatalogFile(t, catalogDir, "child.yaml", extendingChildYAML)

	childRef := closure.ResolvedRef{URI: "demo/extending-child", KindHint: "component"}
	absentRef := closure.ResolvedRef{URI: "demo/absent-component", KindHint: "component"}
	cl := closure.Closure{Entities: []closure.ResolvedRef{childRef}}
	marked := context.WithValue(t.Context(), callerMark{}, true)

	for _, tc := range []struct {
		name string
		read func(view *LazyLookup)
	}{
		{"the context-carrying Component accessor", func(v *LazyLookup) { v.ComponentContext(marked, absentRef.URI) }},
		{"the context-carrying Feature accessor", func(v *LazyLookup) { v.FeatureContext(marked, "demo/absent-feature") }},
		{"the catalog lookup bound to a context", func(v *LazyLookup) { v.AsCatalogLookupContext(marked).Component(absentRef.URI) }},
		{"the closure-entity Component read", func(v *LazyLookup) { v.ClosureComponent(marked, absentRef) }},
		{"the extends chain a closure-entity read merges", func(v *LazyLookup) { v.ClosureComponent(marked, childRef) }},
		{"the closure-scoped validation's extends chain", func(v *LazyLookup) { _ = v.ValidateClosureScopedPillars(marked, cl) }},
		{"the overlap lint's extends chain", func(v *LazyLookup) { _ = v.ClosureLintWarnings(marked, cl) }},
		{"the extends gate", func(v *LazyLookup) { _ = v.ResolveClosureExtends(marked, cl) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			puller := &kindRecordingPuller{}
			// A fresh view per row, so no row is answered from a miss an earlier row
			// remembered.
			tc.read(NewLazyLookup(ws, ".aggrega/cache", []string{catalogDir}, puller))
			if len(puller.asked()) == 0 {
				t.Fatal("precondition: the read never reached the registry, so there is no request to read a context from")
			}
			if unmarked := puller.unmarked(); len(unmarked) > 0 {
				t.Errorf("the registry was asked under a context that is not the caller's: %+v", unmarked)
			}
		})
	}

	t.Run("control: the plain accessor reads under a background context", func(t *testing.T) {
		puller := &kindRecordingPuller{}
		NewLazyLookup(ws, ".aggrega/cache", []string{catalogDir}, puller).Component(absentRef.URI)
		if len(puller.asked()) == 0 {
			t.Fatal("precondition: the plain accessor never reached the registry")
		}
		if len(puller.unmarked()) == 0 {
			t.Error("the plain accessor's request carried the mark, so the mark is on every request and the rows above prove nothing")
		}
	})
}
