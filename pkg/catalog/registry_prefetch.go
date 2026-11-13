// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"context"
	"errors"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// PrefetchRegistryReads satisfies closure.RegistryPrefetcher over this view: probe
// resolves through a lookup that makes this view's local probes with the view
// probing — reading what it holds and writing nothing (LazyLookup.probing) — and
// then asks the probing copy of the base (catalogLookup.probing), whose registry
// tiers stop at the puller; the fetches that copy noted are then made ahead
// (readAheadFor). Each probing resolution takes the view's lock as every resolution
// does; the fetches run once probe has returned, outside it.
func (x *lazyClosureLookup) PrefetchRegistryReads(ctx context.Context, probe func(closure.Lookup)) {
	base, ok := x.base.(*catalogLookup)
	if !ok {
		return
	}
	base.readAheadFor(ctx, probe, func(probing *catalogLookup) closure.Lookup {
		return &lazyClosureLookup{l: x.l, base: probing, probing: true}
	})
}

// readAheadFor is the read-ahead behind lazyClosureLookup.PrefetchRegistryReads, run
// over the view's base l. It hands probe the lookup that over builds around l's
// probing copy — the view's probing lookup — then makes every fetch the
// copy noted through l's own puller, without announcing anything, at most
// oci.RegistryRequestsAtOnce at once, into the memo of registry reads ctx holds
// (oci.WithRegistryReadMemo), where the walk's own fetches take their answers.
//
// It reads ahead only where both halves exist: a memo in ctx to keep the answers in,
// and a puller that can make a fetch's reads quietly (registryReadAheadPuller). With
// either missing it calls nothing, and the walk reads one reference at a time.
func (l *catalogLookup) readAheadFor(ctx context.Context, probe func(closure.Lookup), over func(*catalogLookup) closure.Lookup) {
	if !oci.HoldsRegistryReadMemo(ctx) {
		return
	}
	probing, noted, puller, ok := l.probing()
	if !ok {
		return
	}
	probe(over(probing))
	oci.EachAtOnce(ctx, len(noted.fetches), oci.RegistryRequestsAtOnce, func(ctx context.Context, i int) {
		puller.readAhead(ctx, noted.fetches[i])
	})
}

// registryReadAheadPuller is a puller a lookup can read ahead through. It answers to
// every capability the registry tiers ask a puller for, so the fetchRecorder that
// stands in for it during a probe — which answers to the same set — is handed the
// fetch the puller itself would be handed, through the same branch; and it can make
// a fetch's reads without announcing anything (readAhead).
type registryReadAheadPuller interface {
	fetchCapabilities
	readAhead(ctx context.Context, f registryFetch)
}

// fetchCapabilities is the set of puller capabilities the registry tiers branch on.
type fetchCapabilities interface {
	OCIPuller
	EntityPuller
	LibraryPuller
	AggregatePuller
	RegistryPrefix() string
}

var (
	_ registryReadAheadPuller = DefaultOCIPuller{}
	_ fetchCapabilities       = (*fetchRecorder)(nil)
)

// readAhead makes the fetch f names, through the same pull the walk's fetch makes,
// on a copy of p whose withdrawal screen announces nothing (quietScreen), under a
// context whose reads keep their answers in the run's memo
// (oci.PrefetchingRegistryReads). What it fetches is discarded here; the answers
// wait in the memo for the walk's own fetch, which announces what the screen did.
func (p DefaultOCIPuller) readAhead(ctx context.Context, f registryFetch) {
	quiet := p
	if p.retraction != nil {
		quiet.retraction = quietScreen{screen: p.retraction}
	}
	ctx = oci.PrefetchingRegistryReads(ctx)
	switch f.shape {
	case fetchEntity:
		_, _, _ = quiet.PullEntity(ctx, f.name, f.kind, f.floor)
	case fetchLibrary:
		_, _, _ = quiet.PullLibrary(ctx, f.name, f.kind, f.floor)
	case fetchAggregate:
		_, _, _ = quiet.PullAggregate(ctx, f.name, f.floor)
	case fetchByReference:
		_, _ = quiet.Pull(ctx, f.name, f.floor)
	}
}

// quietScreen screens versions exactly as the withdrawal screen it wraps does and
// announces nothing, so a read ahead selects the version the walk's fetch will
// select and leaves every announcement to that fetch.
//
// It holds the screen in a field and forwards ScreenVersions by name rather than
// embedding it, so a method added to RetractionScreen does not pass through to the
// wrapped screen unseen: quietScreen stops satisfying the interface, and the
// assignment in readAhead fails to compile until the new method is answered here.
type quietScreen struct {
	screen RetractionScreen
}

func (q quietScreen) ScreenVersions(reference string, tags []string) ([]string, []VersionExclusion) {
	return q.screen.ScreenVersions(reference, tags)
}

func (quietScreen) ReportSelection(string, string, []VersionExclusion) {}

func (quietScreen) ReportPinned(string, string) {}

// registryFetchShape says which of a puller's fetches a registryFetch is.
type registryFetchShape uint8

const (
	fetchEntity registryFetchShape = iota + 1
	fetchLibrary
	fetchAggregate
	fetchByReference
)

// registryFetch is one fetch a registry tier hands its puller, as the arguments it
// hands it: name is the identity an entity, library or aggregate fetch binds, or the
// located reference a fetch by reference reads; kind is the catalog kind of an
// entity or library fetch; floor is the version asked for — empty where the fetch
// selects one — or the tag a fetch by reference reads.
type registryFetch struct {
	shape registryFetchShape
	name  string
	kind  string
	floor string
}

// errProbeStopsAtTheRegistry is what the fetchRecorder answers every fetch with.
// It is no fetch condition a registry tier reads as a miss, so each tier returns it
// at once, before writing anything for the reference.
var errProbeStopsAtTheRegistry = errors.New("catalog: a probe stops where a registry would be read")

// fetchRecorder stands in for a lookup's puller while the lookup is probed: it notes
// each fetch it is handed, once and in the order first handed, and refuses it.
type fetchRecorder struct {
	prefix  string
	fetches []registryFetch
	noted   map[registryFetch]struct{}
}

func (r *fetchRecorder) note(f registryFetch) {
	if _, seen := r.noted[f]; seen {
		return
	}
	r.noted[f] = struct{}{}
	r.fetches = append(r.fetches, f)
}

func (r *fetchRecorder) Pull(_ context.Context, reference, tag string) (*oci.PullResult, error) {
	r.note(registryFetch{shape: fetchByReference, name: reference, floor: tag})
	return nil, errProbeStopsAtTheRegistry
}

func (r *fetchRecorder) PullEntity(_ context.Context, identity, kind, floorVersion string) (*oci.PullResult, string, error) {
	r.note(registryFetch{shape: fetchEntity, name: identity, kind: kind, floor: floorVersion})
	return nil, "", errProbeStopsAtTheRegistry
}

func (r *fetchRecorder) PullLibrary(_ context.Context, name, kind, floorVersion string) (*oci.PullResult, string, error) {
	r.note(registryFetch{shape: fetchLibrary, name: name, kind: kind, floor: floorVersion})
	return nil, "", errProbeStopsAtTheRegistry
}

func (r *fetchRecorder) PullAggregate(_ context.Context, name, floorVersion string) (*oci.PullResult, string, error) {
	r.note(registryFetch{shape: fetchAggregate, name: name, floor: floorVersion})
	return nil, "", errProbeStopsAtTheRegistry
}

// RegistryPrefix reports the prefix of the puller the recorder stands in for, so a
// tier that reads it reads what it would read for real.
func (r *fetchRecorder) RegistryPrefix() string { return r.prefix }

// probing returns the copy of l a probe resolves through, the recorder standing in
// for its puller, and the puller itself — or false where l's puller cannot read
// ahead. The copy shares what l only reads and holds its own of everything l
// writes: its puller is the recorder, its record of the cache slots the reuse check
// passed over is its own (and empty), and it has no notice sink.
func (l *catalogLookup) probing() (*catalogLookup, *fetchRecorder, registryReadAheadPuller, bool) {
	puller, ok := l.ociPuller.(registryReadAheadPuller)
	if !ok {
		return nil, nil, nil, false
	}
	noted := &fetchRecorder{prefix: puller.RegistryPrefix(), noted: map[registryFetch]struct{}{}}
	var passedOver map[PinKey]passedOverSlot
	if l.refetchPending != nil {
		passedOver = map[PinKey]passedOverSlot{}
	}
	// The fields are listed by position on purpose: a field added to catalogLookup
	// does not compile here until it is placed and marked as shared or as the
	// probe's own, so none is shared with the probe by accident.
	return &catalogLookup{
		l.catalog,       // shared, read only: the index the catalog tier selects from
		noted,           // the probe's own: notes the fetch instead of making it
		l.workspaceRoot, // shared, read only
		l.cacheDir,      // shared, read only: the cache tier reads slots, the probe writes none
		l.externals,     // shared, read only
		l.registrySeeds, // shared, read only
		l.priorPins,     // shared, read only
		passedOver,      // the probe's own: what the reuse check passed over
		nil,             // the probe's own: no notice sink
	}, noted, puller, true
}
