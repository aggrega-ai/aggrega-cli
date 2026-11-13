// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-containerregistry/pkg/registry"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// What this file proves about reading ahead, and how.
//
// A walk over a lookup that reads ahead (closure.RegistryPrefetcher) and the same
// walk over a lookup that cannot are driven over one registry, each in a workspace
// of its own, and everything a reader sees is compared: the notices in the order
// written, the entities with the version, digest and source each was resolved
// from, the libraries, and the refusal. Reading ahead may change how long a walk
// waits and nothing else, so any difference is the defect.
//
// The registry is the go-containerregistry in-process server behind a handler that
// records every request and how many were in flight at once, so the requests a walk
// made are read as a list rather than inferred from a timing.

// readAheadCacheDir is the cache directory every walk below resolves against.
const readAheadCacheDir = ".aggrega/cache"

// readAheadRegistry is an in-process registry that records every request it
// answers, as its method and path, and the most it answered at once. Armed with
// awaitOverlap, it holds each request until a second is in flight, so a walk asking
// several at once is seen doing so however fast the registry answers; a walk asking
// one at a time leaves the first request waiting out overlapCeiling, after which the
// hold is lifted for good.
type readAheadRegistry struct {
	host  string
	inner http.Handler

	mu       sync.Mutex
	asked    []string
	inFlight int
	peak     int

	holding    bool
	overlapped chan struct{}
	lifted     bool
}

// overlapCeiling is a liveness ceiling, not a performance budget: it bounds how
// long a request waits for a second one before the hold is lifted, so a walk that
// never asks two at once finishes — and fails on its peak — rather than hanging.
const overlapCeiling = 10 * time.Second

func newReadAheadRegistry(t *testing.T) *readAheadRegistry {
	t.Helper()
	r := &readAheadRegistry{inner: registry.New(registry.Logger(log.New(io.Discard, "", 0)))}
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	r.host = strings.TrimPrefix(srv.URL, "http://")
	return r
}

func (r *readAheadRegistry) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.asked = append(r.asked, req.Method+" "+req.URL.Path)
	r.inFlight++
	r.peak = max(r.peak, r.inFlight)
	hold := r.holding && !r.lifted
	if hold && r.inFlight >= 2 {
		r.lift()
		hold = false
	}
	overlapped := r.overlapped
	r.mu.Unlock()
	if hold {
		select {
		case <-overlapped:
		case <-time.After(overlapCeiling):
			r.mu.Lock()
			r.lift()
			r.mu.Unlock()
		}
	}
	defer func() {
		r.mu.Lock()
		r.inFlight--
		r.mu.Unlock()
	}()
	r.inner.ServeHTTP(w, req)
}

// lift releases every held request and holds no more; r.mu is held.
func (r *readAheadRegistry) lift() {
	if !r.lifted {
		r.lifted = true
		close(r.overlapped)
	}
}

// awaitOverlap arms the hold and resets the peak, for a walk whose peak is read.
func (r *readAheadRegistry) awaitOverlap() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.holding, r.lifted, r.peak = true, false, 0
	r.overlapped = make(chan struct{})
}

// mark returns how many requests the registry has answered, for since.
func (r *readAheadRegistry) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.asked)
}

// since returns the requests answered from the n-th on, sorted, so two walks that
// asked the same things in different orders compare equal.
func (r *readAheadRegistry) since(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := append([]string(nil), r.asked[n:]...)
	slices.Sort(out)
	return out
}

func (r *readAheadRegistry) peakInFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peak
}

// publish pushes body under tag at the repository a pull binds kind and name to.
func (r *readAheadRegistry) publish(t *testing.T, kind, name, tag, mediaType, body string) {
	t.Helper()
	client, err := egress.HTTPClient(workspaceconfig.Config{})
	if err != nil {
		t.Fatalf("network client: %v", err)
	}
	repo, err := oci.NewRemoteRepository(RegistryReference(r.host, kind, "", name), client)
	if err != nil {
		t.Fatalf("repository for %s %s: %v", kind, name, err)
	}
	if _, err := oci.Push(context.Background(), repo, tag, mediaType, []byte(body), nil); err != nil {
		t.Fatalf("publish %s %s:%s: %v", kind, name, tag, err)
	}
}

// componentDoc, featureDoc and mappingLibraryDoc are published documents of each
// kind.
func componentDoc(name, version string) string {
	return fmt.Sprintf(`apiVersion: aggrega.ai/v1beta1
kind: Component
metadata:
  name: %s
  version: %q
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`, name, version)
}

func featureDoc(name, version string) string {
	return fmt.Sprintf("apiVersion: aggrega.ai/v1beta1\nkind: Feature\nmetadata:\n  name: %s\n  version: %q\nspec: {}\n", name, version)
}

func mappingLibraryDoc(name, version string) string {
	return fmt.Sprintf("apiVersion: aggrega.ai/v1beta1\nkind: MappingLibrary\nmetadata:\n  name: %s\n  version: %s\nspec:\n  mappings:\n    Web: 'image: {{ .Values.image }}'\n", name, version)
}

// withdrawalScreen withdraws the versions it holds per reference and writes its
// announcements to out. Screening reads only, so reads ahead may screen at once.
type withdrawalScreen struct {
	withdrawn map[string]map[string]string
	out       io.Writer
}

func (s *withdrawalScreen) ScreenVersions(reference string, tags []string) ([]string, []VersionExclusion) {
	withdrawn := s.withdrawn[reference]
	var eligible []string
	var excluded []VersionExclusion
	for _, tag := range oci.SortedSemverTags(tags) {
		if reason, ok := withdrawn[tag]; ok {
			excluded = append(excluded, VersionExclusion{Version: tag, Reason: reason})
			continue
		}
		eligible = append(eligible, tag)
	}
	return eligible, excluded
}

func (s *withdrawalScreen) ReportSelection(reference, selected string, excluded []VersionExclusion) {
	fmt.Fprintf(s.out, "  screen: %s took %s, withdrawn %s\n", reference, selected, formatVersionExclusions(excluded))
}

func (s *withdrawalScreen) ReportPinned(reference, version string) {
	if reason, ok := s.withdrawn[reference][version]; ok {
		fmt.Fprintf(s.out, "  screen: %s names withdrawn %s (%s)\n", reference, version, reason)
	}
}

// readAheadFixture is one registry publishing the entities and libraries the walks
// below resolve, and what a workspace needs to resolve them.
type readAheadFixture struct {
	reg       *readAheadRegistry
	withdrawn map[string]map[string]string
	// driftedPin is the prior pin of demo/delta:1.0.0: the bytes the registry serves.
	driftedPin Pin
}

func newReadAheadFixture(t *testing.T) *readAheadFixture {
	t.Helper()
	reg := newReadAheadRegistry(t)
	f := &readAheadFixture{reg: reg}
	for _, c := range []struct{ name, version string }{
		{"demo/alpha", "1.0.0"}, {"demo/alpha", "1.2.0"},
		{"demo/beta", "1.0.0"}, {"demo/beta", "2.0.0"},
		{"demo/gamma", "2.0.0"},
		{"demo/delta", "1.0.0"},
		{"demo/e1", "1.0.0"}, {"demo/e2", "1.0.0"}, {"demo/e3", "1.0.0"}, {"demo/e4", "1.0.0"},
	} {
		reg.publish(t, "Component", c.name, c.version, oci.MediaTypeComponent, componentDoc(c.name, c.version))
	}
	reg.publish(t, "Feature", "demo/aaa-feature", "1.0.0", oci.MediaTypeFeature, featureDoc("demo/aaa-feature", "1.0.0"))
	reg.publish(t, "Feature", "demo/aaa-feature", "1.1.0", oci.MediaTypeFeature, featureDoc("demo/aaa-feature", "1.1.0"))
	for _, l := range []struct{ name, version string }{{"LibOne", "1.0.0"}, {"LibOne", "1.1.0"}, {"LibTwo", "1.0.0"}} {
		reg.publish(t, "MappingLibrary", l.name, l.version, oci.MediaTypeMappingLibrary, mappingLibraryDoc(l.name, l.version))
	}
	f.withdrawn = map[string]map[string]string{
		RegistryReference(reg.host, "Component", "", "demo/beta"):      {"2.0.0": "broken release"},
		RegistryReference(reg.host, "Component", "", "demo/gamma"):     {"2.0.0": "superseded"},
		RegistryReference(reg.host, "Feature", "", "demo/aaa-feature"): {"1.1.0": "recalled"},
	}
	digest, err := PinDigest([]byte(componentDoc("demo/delta", "1.0.0")))
	if err != nil {
		t.Fatalf("pin digest: %v", err)
	}
	f.driftedPin = Pin{ResolvedVersion: "1.0.0", Digest: digest}
	return f
}

// readAheadWalk is what one walk showed its reader.
type readAheadWalk struct {
	notices   string
	entities  []string
	libraries []string
	err       error
	closure   closure.Closure
}

// workspacePlaceholder stands for a walk's own workspace root in the notices it
// printed (readAheadFixture.walk).
const workspacePlaceholder = "<workspace>"

// withoutReadAhead hides every capability of the lookup it wraps but the Lookup
// methods, so a walk over it reads one reference at a time.
type withoutReadAhead struct{ closure.Lookup }

// walk resolves the Release and Aggregate in a fresh workspace under ctx, reading
// ahead where readAhead is set, then runs one library round, and returns what the
// walk showed. The workspace's catalog directories both declare demo/local, so the
// view announces a shadow, and its cache holds a demo/delta slot that drifted from
// the one prior pin the walk holds.
func (f *readAheadFixture) walk(t *testing.T, ctx context.Context, ws, release, aggregate string, readAhead bool) readAheadWalk {
	t.Helper()
	var notices strings.Builder
	dirs := []string{filepath.Join(ws, "a"), filepath.Join(ws, "b")}
	for _, dir := range dirs {
		writeCatalogFile(t, dir, "local.yaml", componentDoc("demo/local", "1.0.0"))
	}
	seedPinnedArtifact(t, ws, []byte(componentDoc("demo/delta", "1.0.0")+"# edited in place\n"), oci.MediaTypeComponent, "demo/delta:1.0.0", "1.0.0")
	pins := Pins{PinKey{Kind: componentKind, URI: "demo/delta:1.0.0"}: f.driftedPin}
	cfg := workspaceconfig.Config{DefaultRegistry: f.reg.host}
	puller := NewOCIPullerWithRetractionScreen(cfg, &withdrawalScreen{withdrawn: f.withdrawn, out: &notices})
	view := NewLazyLookup(ws, readAheadCacheDir, dirs, puller)
	view.WriteNoticesTo(&notices)
	var lookup closure.Lookup = view.ClosureLookup(puller, ws, readAheadCacheDir, nil, nil, &ReuseVerification{
		PriorPins: pins,
		Warn:      func(n string) { fmt.Fprintf(&notices, "  reuse: %s\n", n) },
	})
	if !readAhead {
		lookup = withoutReadAhead{lookup}
	}

	var rel types.Release
	if err := yaml.Unmarshal([]byte(release), &rel); err != nil {
		t.Fatalf("release: %v", err)
	}
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(aggregate), &agg); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	var out readAheadWalk
	cl, err := closure.ComputeClosure(ctx, &rel, nil, types.Normalize(&agg), cfg, lookup)
	if err == nil {
		err = cl.DiscoverTemplateLibraries(ctx, closure.LibraryAxisNamed("MappingLibraries"), lookup,
			[]closure.TemplateConsumer{
				{URI: "demo/recipe", Kind: closure.KindRecipe, Reference: "LibOne#/mappings/Web"},
				{URI: "demo/recipe", Kind: closure.KindRecipe, Reference: "LibTwo:1.0.0#/mappings/Web"},
			},
			func(closure.ResolvedLibrary) ([]string, bool, error) { return nil, false, nil })
	}
	out.err = err
	out.closure = cl
	// Each walk runs in a workspace of its own, and a notice naming a catalog file
	// names it by its path there; the root is written as one placeholder so two
	// walks compare byte for byte on everything else.
	out.notices = strings.ReplaceAll(notices.String(), ws, workspacePlaceholder)
	for _, e := range cl.Entities {
		out.entities = append(out.entities, fmt.Sprintf("%s %s %s %s %s", e.URI, e.ResolvedVersion, e.Digest, e.ManifestDigest, e.ResolutionPath))
	}
	for _, l := range cl.MappingLibraries {
		out.libraries = append(out.libraries, fmt.Sprintf("%s %s %s %s", l.URI, l.ResolvedVersion, l.Digest, l.ResolutionPath))
	}
	return out
}

const readAheadRelease = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: prod
  version: 1.0.0
spec:
  boundaries:
    primary:
      components:
        api:
          features:
            edge:
              type: demo/aaa-feature
`

// readAheadAggregate is an Aggregate declaring one component of each type given.
func readAheadAggregate(components ...string) string {
	var b strings.Builder
	b.WriteString("apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: app\n  version: 1.0.0\nspec:\n  components:\n")
	for i, typ := range components {
		fmt.Fprintf(&b, "    c%d:\n      type: %s\n", i, typ)
	}
	return b.String()
}

// everyCase is an Aggregate exercising each thing a walk reports: a bare reference
// and a floored one sharing one cache slot, a withdrawn version the screen skips, a
// withdrawn version named outright, a drifted slot fetched again, an entity a
// catalog directory holds, and several more the registry serves.
var everyCase = readAheadAggregate(
	"demo/alpha", "demo/alpha:1.2.0", "demo/beta", "demo/gamma:2.0.0", "demo/delta:1.0.0",
	"demo/local", "demo/e1", "demo/e2", "demo/e3", "demo/e4",
)

// TestAWalkReadingAheadShowsWhatAWalkReadingOneAtATimeShows drives each row's walk
// twice, once over a lookup that reads ahead and once over the same lookup with
// that capability hidden, and requires the two to show their reader the same
// notices in the same order, the same entities and libraries, each with the same
// version, digests and source, and the same refusal.
//
// Each row first holds its own fixture to what it is there for — the notices it
// must print, the sources it must report — so an equality over a walk that
// exercised nothing cannot pass.
//
// Planted defects that make it fail: announce in the read ahead (have quietScreen's
// two Report methods forward to the wrapped screen), and the screen's notices
// appear twice in the walk reading ahead; write a notice a probe's index build meets
// at once (drop the holdingFor case of LazyLookup.writeNotice), and the shadow notice
// moves ahead of the Feature's withdrawal notice; have the lazy view's probe answer
// a reference a catalog directory holds as held nowhere (return false from
// resolveComponentForClosure while probing), and the walk reading ahead asks the
// registry for demo/local, which the walk reading one at a time never does.
func TestAWalkReadingAheadShowsWhatAWalkReadingOneAtATimeShows(t *testing.T) {
	f := newReadAheadFixture(t)
	missing := readAheadAggregate("demo/alpha", "demo/missing-b", "demo/e1", "demo/missing-d", "demo/e3")

	cases := []struct {
		name      string
		aggregate string
		// hold checks what the walk reading one at a time showed, so the row is known
		// to exercise what it is there for.
		hold func(t *testing.T, w readAheadWalk)
		// sameRequests requires the two walks to have asked the registry the same
		// things, which holds for a walk that resolves everything it reads ahead for.
		sameRequests bool
	}{
		{
			name:         "every reference resolves",
			aggregate:    everyCase,
			sameRequests: true,
			hold: func(t *testing.T, w readAheadWalk) {
				t.Helper()
				if w.err != nil {
					t.Fatalf("the walk refused: %v", w.err)
				}
				for _, notice := range []string{
					"took 1.0.0, withdrawn 1.1.0 (recalled)",
					"took 1.0.0, withdrawn 2.0.0 (broken release)",
					"names withdrawn 2.0.0 (superseded)",
					"reuse: cache entry for demo/delta:1.0.0",
					"is shadowed (first-win precedence)",
				} {
					if !strings.Contains(w.notices, notice) {
						t.Fatalf("the walk printed no notice containing %q; it printed:\n%s", notice, w.notices)
					}
				}
				feature := strings.Index(w.notices, "withdrawn 1.1.0")
				shadow := strings.Index(w.notices, "is shadowed")
				if feature > shadow {
					t.Fatalf("the Feature's notice should precede the Component index's shadow notice, which only the "+
						"first Component reference prints; got:\n%s", w.notices)
				}
				for _, want := range []string{
					"demo/alpha 1.2.0", "demo/alpha:1.2.0 1.2.0", "demo/local 1.0.0",
				} {
					if !slices.ContainsFunc(w.entities, func(e string) bool { return strings.HasPrefix(e, want+" ") }) {
						t.Fatalf("no entity %q among %q", want, w.entities)
					}
				}
				if source := entitySource(w.closure, "demo/alpha:1.2.0"); source != closure.ResolutionPathCache {
					t.Fatalf("demo/alpha:1.2.0 resolved from %q; want the slot demo/alpha wrote (%q)", source, closure.ResolutionPathCache)
				}
				if source := entitySource(w.closure, "demo/alpha"); source != closure.ResolutionPathRegistry {
					t.Fatalf("demo/alpha resolved from %q; want the registry", source)
				}
				if len(w.libraries) != 2 {
					t.Fatalf("libraries = %q; want LibOne and LibTwo", w.libraries)
				}
			},
		},
		{
			name:      "two references the registry does not publish",
			aggregate: missing,
			hold: func(t *testing.T, w readAheadWalk) {
				t.Helper()
				var unresolved *closure.UnresolvedEntityError
				if !errors.As(w.err, &unresolved) || unresolved.URI != "demo/missing-b" {
					t.Fatalf("the walk should refuse naming demo/missing-b, the first in order of the two; got %v", w.err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := f.reg.mark()
			serial := f.walk(t, oci.WithRegistryReadMemo(context.Background()), t.TempDir(), readAheadRelease, tc.aggregate, false)
			serialAsked := f.reg.since(before)
			tc.hold(t, serial)

			before = f.reg.mark()
			ahead := f.walk(t, oci.WithRegistryReadMemo(context.Background()), t.TempDir(), readAheadRelease, tc.aggregate, true)
			aheadAsked := f.reg.since(before)

			if ahead.notices != serial.notices {
				t.Errorf("notices differ\nreading ahead:\n%s\none at a time:\n%s", ahead.notices, serial.notices)
			}
			if !slices.Equal(ahead.entities, serial.entities) {
				t.Errorf("entities differ\nreading ahead: %q\none at a time: %q", ahead.entities, serial.entities)
			}
			if !slices.Equal(ahead.libraries, serial.libraries) {
				t.Errorf("libraries differ\nreading ahead: %q\none at a time: %q", ahead.libraries, serial.libraries)
			}
			if fmt.Sprint(ahead.err) != fmt.Sprint(serial.err) {
				t.Errorf("refusals differ\nreading ahead: %v\none at a time: %v", ahead.err, serial.err)
			}
			if tc.sameRequests {
				if extra := multisetDifference(aheadAsked, serialAsked); len(extra) > 0 {
					t.Errorf("the walk reading ahead asked the registry for what the walk reading one at a time did not: %q", extra)
				}
				if missing := multisetDifference(serialAsked, aheadAsked); len(missing) > 0 {
					t.Errorf("the walk reading ahead never asked for what the walk reading one at a time did: %q", missing)
				}
			}
		})
	}
}

// entitySource is the source the closure reports uri resolved from.
func entitySource(cl closure.Closure, uri string) string {
	for _, e := range cl.Entities {
		if e.URI == uri {
			return e.ResolutionPath
		}
	}
	return ""
}

// multisetDifference returns what a holds more times than b, sorted.
func multisetDifference(a, b []string) []string {
	left := map[string]int{}
	for _, s := range b {
		left[s]++
	}
	var out []string
	for _, s := range a {
		if left[s] > 0 {
			left[s]--
			continue
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// TestAWalkReadsAheadSeveralAtOnceWithinTheBound reads the most requests the
// registry had in flight at once while the entity pass of a cold walk ran: above
// one, so the walk did read ahead at once, and at most oci.RegistryRequestsAtOnce.
// The registry holds each request until a second arrives (readAheadRegistry), so
// the reading is the walk's and not the registry's speed.
//
// A planted defect makes it fail: pass a limit of 1 to oci.EachAtOnce in
// catalogLookup.readAheadFor, and the peak is one.
func TestAWalkReadsAheadSeveralAtOnceWithinTheBound(t *testing.T) {
	f := newReadAheadFixture(t)
	f.reg.awaitOverlap()
	w := f.walk(t, oci.WithRegistryReadMemo(context.Background()), t.TempDir(), readAheadRelease, everyCase, true)
	if w.err != nil {
		t.Fatalf("the walk refused: %v", w.err)
	}
	if peak := f.reg.peakInFlight(); peak < 2 || peak > oci.RegistryRequestsAtOnce {
		t.Errorf("at most %d requests were in flight at once; want more than one and at most %d", peak, oci.RegistryRequestsAtOnce)
	}
}

// TestAWalkReadsAheadNothingItHasNoNeedToFetch holds a walk whose every reference
// a local tier answers to no request at all: a warm walk over the cache a cold walk
// left, held to the pins that walk resolved, and a walk whose one entity a catalog
// directory holds. The cold walk before them is the control that shows the registry
// was asked when there was something to fetch.
//
// A planted defect makes it fail: have the lazy view's probe answer every reference
// as held nowhere (return false from resolveComponentForClosure while probing), and
// the walk over demo/local reads ahead for it.
func TestAWalkReadsAheadNothingItHasNoNeedToFetch(t *testing.T) {
	f := newReadAheadFixture(t)
	ws := t.TempDir()
	before := f.reg.mark()
	cold := f.walk(t, oci.WithRegistryReadMemo(context.Background()), ws, readAheadRelease, everyCase, true)
	if cold.err != nil {
		t.Fatalf("the cold walk refused: %v", cold.err)
	}
	if len(f.reg.since(before)) == 0 {
		t.Fatal("control: the cold walk asked the registry nothing, so the rows below prove nothing")
	}
	pins, err := PinsFromClosure(cold.closure)
	if err != nil {
		t.Fatalf("pins from the cold walk: %v", err)
	}

	for _, tc := range []struct {
		name      string
		ws        string
		aggregate string
		pins      Pins
	}{
		{"a warm walk over the cache a cold walk left", ws, everyCase, pins},
		{"a walk whose entity a catalog directory holds", t.TempDir(), readAheadAggregate("demo/local"), Pins{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			release := "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: prod\n  version: 1.0.0\nspec: {}\n"
			if tc.ws == ws {
				release = readAheadRelease
			}
			before := f.reg.mark()
			w := f.walkEntitiesOnly(t, tc.ws, release, tc.aggregate, tc.pins)
			if w.err != nil {
				t.Fatalf("the walk refused: %v", w.err)
			}
			if asked := f.reg.since(before); len(asked) > 0 {
				t.Errorf("the walk asked the registry %q; want nothing", asked)
			}
		})
	}
}

// walkEntitiesOnly is walk's entity pass alone, reading ahead, over the pins given.
func (f *readAheadFixture) walkEntitiesOnly(t *testing.T, ws, release, aggregate string, pins Pins) readAheadWalk {
	t.Helper()
	dirs := []string{filepath.Join(ws, "a"), filepath.Join(ws, "b")}
	for _, dir := range dirs {
		writeCatalogFile(t, dir, "local.yaml", componentDoc("demo/local", "1.0.0"))
	}
	cfg := workspaceconfig.Config{DefaultRegistry: f.reg.host}
	puller := NewOCIPullerWithRetractionScreen(cfg, &withdrawalScreen{withdrawn: f.withdrawn, out: io.Discard})
	view := NewLazyLookup(ws, readAheadCacheDir, dirs, puller)
	lookup := view.ClosureLookup(puller, ws, readAheadCacheDir, nil, nil, &ReuseVerification{PriorPins: pins, Warn: func(string) {}})
	var rel types.Release
	if err := yaml.Unmarshal([]byte(release), &rel); err != nil {
		t.Fatalf("release: %v", err)
	}
	var agg types.Aggregate
	if err := yaml.Unmarshal([]byte(aggregate), &agg); err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	cl, err := closure.ComputeClosure(oci.WithRegistryReadMemo(context.Background()), &rel, nil, types.Normalize(&agg), cfg, lookup)
	return readAheadWalk{closure: cl, err: err}
}
