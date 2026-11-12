// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pullops

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
	"oras.land/oras-go/v2"
	"oras.land/oras-go/v2/registry"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// markerPassLiveness separates a marker pass that asks about several artifacts at
// once from one that asks about them one at a time, in which the first lookup waits
// for a second that never starts. It is a liveness bound, not a performance budget.
const markerPassLiveness = 30 * time.Second

// haltingMarkerHost is a registry whose policy requires a signature, so a marker
// published there that does not verify halts the pull.
const haltingMarkerHost = "halting.example.com"

// lookupsInFlight counts the subject lookups in flight across every target sharing
// it, and holds the first until a second has started.
type lookupsInFlight struct {
	mu       sync.Mutex
	inFlight int
	peak     int
	started  bool
	second   chan struct{}
}

func newLookupsInFlight() *lookupsInFlight { return &lookupsInFlight{second: make(chan struct{})} }

func (c *lookupsInFlight) enter() {
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
		case <-time.After(markerPassLiveness):
		}
	}
}

func (c *lookupsInFlight) leave() {
	c.mu.Lock()
	c.inFlight--
	c.mu.Unlock()
}

// countedLookups wraps a target the marker pass asks, counting its lookups of a
// subject — the first request of the marker listing and of a marker's own signature
// listing — and calling onResolve, where set, with this target's own count of them
// ahead of answering one. It keeps the target's own referrer listing.
type countedLookups struct {
	refusesMarkerListing
	counter   *lookupsInFlight
	refuses   bool
	mu        *sync.Mutex
	resolved  *int
	onResolve func(n int)
}

func (s countedLookups) Resolve(ctx context.Context, ref string) (ocispec.Descriptor, error) {
	s.counter.enter()
	defer s.counter.leave()
	if s.onResolve != nil {
		s.mu.Lock()
		*s.resolved++
		n := *s.resolved
		s.mu.Unlock()
		s.onResolve(n)
	}
	return s.Store.Resolve(ctx, ref)
}

func (s countedLookups) Referrers(ctx context.Context, desc ocispec.Descriptor, artifactType string, fn func([]ocispec.Descriptor) error) error {
	if s.refuses {
		return s.refusesMarkerListing.Referrers(ctx, desc, artifactType, fn)
	}
	refs, err := registry.Referrers(ctx, s.Store, desc, artifactType)
	if err != nil {
		return err
	}
	return fn(refs)
}

// markerQuestion is one artifact a marker-pass row asks about: the registry it is
// pulled from, the shape of what it carries — "tagged", an unsigned marker; "", no
// marker; "refused", a marker listing that cannot complete — and what runs ahead of
// each of its subject lookups in the row's own run.
type markerQuestion struct {
	host      string
	shape     string
	onResolve func(n int)
}

// TestTheMarkerPassAsksAboutSeveralArtifactsAtOnceAndReportsAsOneAtATime runs the
// deprecation-marker pass over rows of more artifacts than it asks about at once,
// through targets that count the lookups in flight and hold the first until a
// second has started — so a pass asking about one artifact at a time cannot pass.
// Each row's notices and halt are held to the same artifacts asked about one pass
// at a time: the same notices, in the same order, and the same halt.
//
// Planted defects and the rows they fail:
//   - lookups made one at a time: every row, on the peak in flight;
//   - no bound on the lookups in flight: every row, whose peak must stay within
//     oci.RegistryRequestsAtOnce;
//   - notices written as answers arrive: "unsigned, absent and unfinished markers",
//     whose notices are compared in question order;
//   - the halt of whichever answer arrived first: "two markers that halt, the later
//     artifact's answered first", where the later artifact's lookup completes before
//     the earlier's is answered — the halt must be the earlier artifact's.
func TestTheMarkerPassAsksAboutSeveralArtifactsAtOnceAndReportsAsOneAtATime(t *testing.T) {
	t.Parallel()
	rows := []struct {
		name      string
		questions func(order chan struct{}) []markerQuestion
		halts     bool
	}{
		{
			name: "unsigned, absent and unfinished markers",
			questions: func(chan struct{}) []markerQuestion {
				out := make([]markerQuestion, 2*oci.RegistryRequestsAtOnce+1)
				for i := range out {
					out[i] = markerQuestion{host: unfinishedLookupHost, shape: []string{"tagged", "", "refused"}[i%3]}
				}
				return out
			},
		},
		{
			name: "two markers that halt, the later artifact's answered first",
			questions: func(order chan struct{}) []markerQuestion {
				out := make([]markerQuestion, 2*oci.RegistryRequestsAtOnce)
				for i := range out {
					out[i] = markerQuestion{host: unfinishedLookupHost, shape: []string{"tagged", ""}[i%2]}
				}
				// The earlier artifact's first lookup is answered only once the later
				// artifact has reached its marker's own signature lookup, the last of its
				// lookups.
				out[3] = markerQuestion{host: haltingMarkerHost, shape: "tagged", onResolve: func(n int) {
					if n == 1 {
						select {
						case <-order:
						case <-time.After(markerPassLiveness):
						}
					}
				}}
				out[4] = markerQuestion{host: haltingMarkerHost, shape: "tagged", onResolve: func(n int) {
					if n == 2 {
						close(order)
					}
				}}
				return out
			},
			halts: true,
		},
	}
	wsCfg := workspaceconfig.Config{Registries: []workspaceconfig.RegistryConfig{{
		Host: haltingMarkerHost, Signing: workspaceconfig.SigningConfig{Required: true},
	}}}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			planned := row.questions(make(chan struct{}))
			counter := newLookupsInFlight()
			questions := make([]signatureQuestion, len(planned))
			counted := map[string]oras.ReadOnlyTarget{}
			alone := map[string]oras.ReadOnlyTarget{}
			for i, p := range planned {
				shape := p.shape
				if shape == "refused" {
					shape = ""
				}
				store, e := unfinishedLookupArtifact(t, shape)
				e.Host = p.host
				e.URI = fmt.Sprintf("%s/plugin/linter/withdrawn-%02d", p.host, i)
				questions[i] = signatureQuestion{
					row:    lockfile.RowKey{Section: "plugins", EntityKey: closure.EntityKey{Kind: "plugin", URI: e.URI}},
					entity: e,
				}
				base := refusesMarkerListing{Store: store}
				counted[e.URI] = countedLookups{refusesMarkerListing: base, counter: counter, refuses: p.shape == "refused",
					mu: &sync.Mutex{}, resolved: new(int), onResolve: p.onResolve}
				open := newLookupsInFlight()
				close(open.second)
				alone[e.URI] = countedLookups{refusesMarkerListing: base, counter: open, refuses: p.shape == "refused",
					mu: &sync.Mutex{}, resolved: new(int)}
			}
			targetsFrom := func(targets map[string]oras.ReadOnlyTarget) pullverify.TargetFor {
				return func(e pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
					return targets[e.URI], unfinishedLookupVersion, nil
				}
			}

			var got bytes.Buffer
			gotErr := discoverClosureDeprecations(context.Background(), &lockfile.Lockfile{}, questions, nil, wsCfg, targetsFrom(counted), &got)

			var want bytes.Buffer
			var wantErr error
			for _, q := range questions {
				if wantErr = discoverClosureDeprecations(context.Background(), &lockfile.Lockfile{}, []signatureQuestion{q}, nil, wsCfg,
					targetsFrom(alone), &want); wantErr != nil {
					break
				}
			}

			if counter.peak < 2 || counter.peak > oci.RegistryRequestsAtOnce {
				t.Errorf("the most lookups in flight at once was %d; want more than one and at most %d", counter.peak, oci.RegistryRequestsAtOnce)
			}
			if (gotErr != nil) != row.halts || (wantErr != nil) != row.halts {
				t.Fatalf("halt: at once %v, one at a time %v; want a halt %t", gotErr, wantErr, row.halts)
			}
			if row.halts && gotErr.Error() != wantErr.Error() {
				t.Errorf("the halt differs from the one the artifacts asked about one at a time give:\n at once:       %v\n one at a time: %v", gotErr, wantErr)
			}
			if got.String() != want.String() {
				t.Errorf("the notices differ from those of the artifacts asked about one at a time:\n at once:\n%s\n one at a time:\n%s", got.String(), want.String())
			}
			if !row.halts && (!strings.Contains(want.String(), unhonoredMarkerNotice) || !strings.Contains(want.String(), unfinishedLookupNotice)) {
				t.Fatalf("the row printed no notice of each kind, so it proves nothing about their order:\n%s", want.String())
			}
		})
	}
}
