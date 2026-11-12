// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullverify_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// atOnceLiveness separates a pass that asks about several entities at once from
// one that asks about them one at a time, in which the first lookup waits for a
// second that never starts. It is a liveness bound, not a performance budget: the
// lookups it waits on start within microseconds of each other.
const atOnceLiveness = 30 * time.Second

// The registries the rows' entities are pulled from, each under its own policy.
const (
	keyedHost       = "keyed.example.com"
	advisoryHost    = "advisory.example.com"
	refusingHost    = "refusing.example.com"
	unreachableHost = "unreachable.example.com"
)

// lookupCounter counts the signature lookups in flight across every target sharing
// it, and holds the first lookup until a second one is in flight.
type lookupCounter struct {
	mu       sync.Mutex
	inFlight int
	peak     int
	started  bool
	second   chan struct{}
}

func newLookupCounter() *lookupCounter { return &lookupCounter{second: make(chan struct{})} }

func (c *lookupCounter) enter() {
	c.mu.Lock()
	c.inFlight++
	c.peak = max(c.peak, c.inFlight)
	first := !c.started
	c.started = true
	if c.inFlight == 2 {
		select {
		case <-c.second:
		default:
			close(c.second)
		}
	}
	c.mu.Unlock()
	if first {
		select {
		case <-c.second:
		case <-time.After(atOnceLiveness):
		}
	}
}

func (c *lookupCounter) leave() {
	c.mu.Lock()
	c.inFlight--
	c.mu.Unlock()
}

// countingTarget is an in-memory registry whose lookups of a signature's subject —
// the first request of every entity's signature lookup — are counted, and which
// calls its before hook, where one is set, ahead of answering such a lookup.
type countingTarget struct {
	*memory.Store
	counter *lookupCounter
	before  func() error
}

func (s countingTarget) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	s.counter.enter()
	defer s.counter.leave()
	if s.before != nil {
		if err := s.before(); err != nil {
			return ocispec.Descriptor{}, err
		}
	}
	return s.Store.Resolve(ctx, ref)
}

// atOnceFixture is the stores and the configuration the rows verify against: an
// artifact signed under the key keyedHost and advisoryHost pin, one signed under a
// key refusingHost does not pin, and one carrying no signature.
type atOnceFixture struct {
	signed, wrongKey, unsigned *memory.Store
	// digests holds the manifest digest each store's artifact is published under.
	digests map[*memory.Store]string
	cfg     workspaceconfig.Config
}

func newAtOnceFixture(t *testing.T) atOnceFixture {
	t.Helper()
	signed, signedDg, pub := signedStore(t)
	wrongKey, wrongKeyDg, _ := signedStore(t)
	unsigned := memory.New()
	unsignedDg, err := oci.Push(context.Background(), unsigned, "1.0.0", oci.MediaTypeComponent, []byte("kind: Component\n"), nil)
	if err != nil {
		t.Fatalf("Push: %v", err)
	}
	required := workspaceconfig.SigningConfig{Required: true}
	return atOnceFixture{
		signed: signed, wrongKey: wrongKey, unsigned: unsigned,
		digests: map[*memory.Store]string{signed: signedDg, wrongKey: wrongKeyDg, unsigned: unsignedDg},
		cfg: workspaceconfig.Config{Registries: []workspaceconfig.RegistryConfig{
			{Host: keyedHost, Signing: required, PublicKey: string(pub)},
			{Host: advisoryHost, Signing: required, Advisory: true, PublicKey: string(pub)},
			{Host: refusingHost, Signing: required, PublicKey: string(pub)},
			{Host: unreachableHost, Signing: required, PublicKey: string(pub)},
		}},
	}
}

// atOnceEntity is one entity a row verifies: where it is pulled from, the store
// serving it, and what runs before its lookup is answered in the row's own run.
type atOnceEntity struct {
	host   string
	store  *memory.Store
	before func() error
}

func (fx atOnceFixture) entityFor(i int, e atOnceEntity) pullverify.Entity {
	uri := fmt.Sprintf("%s/component/core/entity-%02d", e.host, i)
	return pullverify.Entity{URI: uri, Host: e.host, ManifestDigest: fx.digests[e.store], Kind: "Component"}
}

// verifyRun is what one Verify call produced: its results, the notices it handed
// Warn in order, and its refusal.
type verifyRun struct {
	results []pullverify.Result
	notices []string
	err     error
}

func runVerify(cfg workspaceconfig.Config, entities []pullverify.Entity, targets map[string]oras.ReadOnlyTarget) verifyRun {
	var run verifyRun
	run.results, run.err = pullverify.Verify(context.Background(), entities, pullverify.Options{
		Config: cfg,
		Target: func(e pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
			return targets[e.URI], "1.0.0", nil
		},
		Warn: func(n string) { run.notices = append(run.notices, n) },
	})
	return run
}

// oneAtATime is the reference a row's own run is held to: each entity verified by a
// Verify call of its own, in order, with the results and notices joined and the
// first refusal returned — a pass that asks about one entity at a time.
func oneAtATime(cfg workspaceconfig.Config, entities []pullverify.Entity, targets map[string]oras.ReadOnlyTarget) verifyRun {
	var joined verifyRun
	for _, e := range entities {
		run := runVerify(cfg, []pullverify.Entity{e}, targets)
		joined.notices = append(joined.notices, run.notices...)
		if run.err != nil {
			joined.err = run.err
			return joined
		}
		joined.results = append(joined.results, run.results...)
	}
	return joined
}

// TestTheSignaturePassAsksAboutSeveralEntitiesAtOnceAndAnswersAsOneAtATime verifies
// rows of more entities than the pass asks about at once, through targets that count
// the signature lookups in flight and hold the first until a second has started —
// so a pass asking one entity at a time cannot pass. Each row's run is held to the
// same entities verified one Verify call at a time: the same results, the same
// notices in the same order, and the same refusal. The most lookups in flight must
// exceed one and stay within oci.RegistryRequestsAtOnce.
//
// Planted defects and the rows they fail:
//   - lookups made one at a time: every row, on the peak in flight;
//   - no bound on the lookups in flight: "entities signed and unsigned under an
//     advisory registry", whose peak must stay within the bound;
//   - results or notices gathered in the order the lookups finish: the same row,
//     whose notices and results are compared in entity order;
//   - the refusal of whichever entity's lookup finished first: "two refusals, the
//     later entity's answered first", where the later entity's lookup fails before
//     the earlier entity's lookup is answered — the refusal must be the earlier
//     entity's, the same refusal it gets verified on its own (the refusal the
//     wrong-key case of TestVerifyKeyedRejectsWrongKey asserts).
func TestTheSignaturePassAsksAboutSeveralEntitiesAtOnceAndAnswersAsOneAtATime(t *testing.T) {
	t.Parallel()
	fx := newAtOnceFixture(t)
	errUnreachable := errors.New("the registry could not be reached for this entity")

	rows := []struct {
		name string
		// entities builds the row's entities, given a channel the row may use to
		// order two lookups.
		entities func(order chan struct{}) []atOnceEntity
		refuses  bool
	}{
		{
			name: "entities signed and unsigned under an advisory registry",
			entities: func(chan struct{}) []atOnceEntity {
				out := make([]atOnceEntity, 3*oci.RegistryRequestsAtOnce)
				for i := range out {
					out[i] = atOnceEntity{host: keyedHost, store: fx.signed}
					if i%3 == 0 {
						out[i] = atOnceEntity{host: advisoryHost, store: fx.unsigned}
					}
				}
				return out
			},
		},
		{
			name: "two refusals, the later entity's answered first",
			entities: func(order chan struct{}) []atOnceEntity {
				out := make([]atOnceEntity, 2*oci.RegistryRequestsAtOnce)
				for i := range out {
					out[i] = atOnceEntity{host: keyedHost, store: fx.signed}
				}
				// The earlier refusal's lookup is answered only once the later one's has
				// failed.
				out[3] = atOnceEntity{host: refusingHost, store: fx.wrongKey, before: func() error {
					select {
					case <-order:
					case <-time.After(atOnceLiveness):
					}
					return nil
				}}
				out[4] = atOnceEntity{host: unreachableHost, store: fx.signed, before: func() error {
					close(order)
					return errUnreachable
				}}
				return out
			},
			refuses: true,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			counter := newLookupCounter()
			planned := row.entities(make(chan struct{}))
			entities := make([]pullverify.Entity, len(planned))
			counted := map[string]oras.ReadOnlyTarget{}
			plain := map[string]oras.ReadOnlyTarget{}
			for i, p := range planned {
				entities[i] = fx.entityFor(i, p)
				counted[entities[i].URI] = countingTarget{Store: p.store, counter: counter, before: p.before}
				plain[entities[i].URI] = p.store
				if p.host == unreachableHost {
					// Verified on its own, the entity fails its lookup the same way.
					plain[entities[i].URI] = countingTarget{Store: p.store, counter: newOpenCounter(), before: func() error { return errUnreachable }}
				}
			}

			got := runVerify(fx.cfg, entities, counted)
			want := oneAtATime(fx.cfg, entities, plain)

			if counter.peak < 2 || counter.peak > oci.RegistryRequestsAtOnce {
				t.Errorf("the most lookups in flight at once was %d; want more than one and at most %d", counter.peak, oci.RegistryRequestsAtOnce)
			}
			if (got.err != nil) != row.refuses || (want.err != nil) != row.refuses {
				t.Fatalf("refusal: at once %v, one at a time %v; want a refusal %t", got.err, want.err, row.refuses)
			}
			if row.refuses && got.err.Error() != want.err.Error() {
				t.Errorf("the refusal differs from the one the entities verified one at a time give:\n at once:        %v\n one at a time:  %v", got.err, want.err)
			}
			if !slices.Equal(got.notices, want.notices) {
				t.Errorf("the notices differ from those of the entities verified one at a time:\n at once:       %q\n one at a time: %q", got.notices, want.notices)
			}
			if !row.refuses {
				if len(got.results) != len(want.results) {
					t.Fatalf("%d results; want %d, as one at a time", len(got.results), len(want.results))
				}
				for i := range got.results {
					g, w := got.results[i], want.results[i]
					if g.URI != w.URI || g.Mode != w.Mode || g.Identity != w.Identity || g.BundleDigest != w.BundleDigest {
						t.Errorf("result %d is (%s, %s, %s); want (%s, %s, %s), as one at a time", i, g.URI, g.Mode, g.BundleDigest, w.URI, w.Mode, w.BundleDigest)
					}
				}
				if len(want.notices) == 0 || len(want.results) == 0 {
					t.Fatalf("the row produced no notice or no result, so it proves nothing about their order")
				}
			}
		})
	}
}

// newOpenCounter is a counter whose first lookup is not held, for a target asked
// about one entity alone.
func newOpenCounter() *lookupCounter {
	c := newLookupCounter()
	close(c.second)
	return c
}
