// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package closure_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// walkOrderRuns is how many times each row below drives the walk, and it is
// chosen so that a walk visiting its keys in map order cannot pass by luck.
//
// In every row the outcome the walk must produce is one a map-ordered walk produces
// only when it visits one particular key first, and the fixture records that key in
// SECOND place. The Go runtime holds a map of at most eight entries, none deleted,
// in one group of eight slots filled in insertion order, and starts a range over it
// at a uniformly random slot and wraps, so the second-recorded key comes first only
// when that start is slot 1: one run in eight, whether the map holds two keys or
// three. Twenty runs then pass a map-ordered walk with probability 8^-20, about
// 10^-18, per row. The count does not rest on that layout alone: were one run in
// five to agree instead, twenty runs would still pass a map-ordered walk only about
// once in 10^14.
const walkOrderRuns = 20

// walkOrderGeneratedAt is the provenance stamp every lockfile below carries. It is
// fixed so that two lockfiles differ only where the resolutions they record do.
const walkOrderGeneratedAt = "2000-01-01T00:00:00Z"

// walkOrderLibraryDoc is the one library a catalog directory holds, authored at its
// full version.
const walkOrderLibraryDoc = `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: Base
  version: 1.0.0
spec:
  schemas:
    X:
      size: { type: string, default: "10Gi" }
`

// The two sibling sub-aggregates the descent row includes, the one that sorts
// first named second.
const (
	uriSubZZ = "hub.aggrega.ai/aggregate/example/zz:1.0.0"
	uriSubAA = "hub.aggrega.ai/aggregate/example/aa:1.0.0"
)

// refusedLoad is what the descent row's loader returns for a sub-aggregate it holds
// no body for. It carries the URI, so the row reads which sub-aggregate the descent
// tried first through errors.As rather than out of the refusal's text.
type refusedLoad struct{ uri string }

func (e *refusedLoad) Error() string { return "the fixture holds no body for " + e.uri }

// refusingLoader serves the bodies it holds and refuses every other sub-aggregate.
type refusingLoader map[string]*types.Aggregate

func (l refusingLoader) LoadAggregate(uri, _, _ string) (*types.Aggregate, error) {
	if body, ok := l[uri]; ok {
		return body, nil
	}
	return nil, &refusedLoad{uri: uri}
}

// TestTheLibraryAndSubAggregateWalksVisitInAnOrderTheInputFixes holds four more
// things a resolution shows its reader to the input alone: driven again and again
// over one input, by one binary, it refuses naming one library, reports one set of
// consumers in a version conflict, records one version — and so writes one
// lockfile — for a library two consumers pin in two spellings of one version, and
// refuses naming one sub-aggregate.
//
// Each turns on an order the walk would otherwise take from a map. The library walk
// stops at the first library that does not resolve. Version selection holds every
// pin to the first consumer's major, stops at the first consumer that disagrees,
// and would keep whichever of two equal-ranked pins it met first. The sub-aggregate
// descent stops at the first sub-aggregate it cannot load. Libraries resolve in URI
// order, consumers are visited in URI order, of two pins naming one version the
// fuller spelling wins, and each descent level loads in URI order — so the refusal
// names Alpha, the conflict names comp/a and comp/b, the lockfile records 1.0.0,
// and the descent names the aa sub-aggregate.
//
// Each run builds its walk afresh, as each command does, and the runs are
// sequential; what varies between them is only the order the runtime would range a
// map in.
func TestTheLibraryAndSubAggregateWalksVisitInAnOrderTheInputFixes(t *testing.T) {
	ws := t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")
	if err := os.MkdirAll(catalogDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(catalogDir, "base.yaml"), []byte(walkOrderLibraryDoc), 0o644); err != nil {
		t.Fatalf("write library: %v", err)
	}
	lockPath := filepath.Join(t.TempDir(), "release.lock")

	// discover runs the library walk over a fresh catalog view reading dirs, with one
	// consumer per pair — its URI and the single $ref its schema carries — recorded
	// in the order given.
	discover := func(t *testing.T, dirs []string, consumers ...[2]string) (closure.Closure, error) {
		t.Helper()
		in := make([]closure.SchemaConsumer, 0, len(consumers))
		for _, c := range consumers {
			in = append(in, closure.SchemaConsumer{URI: c[0], Schema: mustParse(t, `$ref: "`+c[1]+`"`)})
		}
		view := catalog.NewLazyLookup(ws, ".aggrega/cache", dirs, nil)
		lookup := view.ClosureLookup(nil, ws, ".aggrega/cache", nil, nil, nil)
		var cl closure.Closure
		err := cl.DiscoverSchemaLibraries(context.Background(), lookup, in, noLibraryBody)
		return cl, err
	}

	// The lockfile the first run of its row writes, which every later run's must
	// match byte for byte.
	var firstLockfile []byte

	cases := []struct {
		name string
		// observe drives one run and returns what it shows its reader that the order
		// decides.
		observe func(t *testing.T) string
		want    string
	}{
		{
			name: "the library a refusal names when two are missing",
			// Zeta is recorded first and Alpha second; no catalog holds either.
			observe: func(t *testing.T) string {
				t.Helper()
				_, err := discover(t, nil,
					[2]string{"comp/a", "Zeta#/schemas/X"},
					[2]string{"comp/b", "Alpha#/schemas/Y"})
				if !errors.Is(err, closure.ErrUnresolvableURI()) {
					t.Fatalf("no catalog holds either library, so the walk refuses as unresolvable; got: %v", err)
				}
				return namedIn(t, err.Error(), "Alpha", "Zeta")
			},
			want: "Alpha",
		},
		{
			name: "the consumers a version conflict names",
			// comp/c is recorded first, then comp/a, then comp/b. Visited in URI order,
			// comp/a sets the major, comp/b disagrees with it, and comp/c is not reached.
			observe: func(t *testing.T) string {
				t.Helper()
				_, err := discover(t, nil,
					[2]string{"comp/c", "Base:1.5.0#/schemas/X"},
					[2]string{"comp/a", "Base:1.0.0#/schemas/X"},
					[2]string{"comp/b", "Base:2.0.0#/schemas/X"})
				if !errors.Is(err, closure.ErrMVSConflict()) {
					t.Fatalf("the pins span two majors, so selection refuses as a conflict; got: %v", err)
				}
				var named []string
				for _, consumer := range []string{"comp/a", "comp/b", "comp/c"} {
					if strings.Contains(err.Error(), consumer+" wants ") {
						named = append(named, consumer)
					}
				}
				return strings.Join(named, ", ")
			},
			want: "comp/a, comp/b",
		},
		{
			name: "the version a lockfile records when two pins spell it differently",
			// comp/a pins Base:1.0 and is recorded first; comp/b pins Base:1.0.0 and is
			// recorded second. The catalog directory holds the library at 1.0.0.
			observe: func(t *testing.T) string {
				t.Helper()
				cl, err := discover(t, []string{catalogDir},
					[2]string{"comp/a", "Base:1.0#/schemas/X"},
					[2]string{"comp/b", "Base:1.0.0#/schemas/X"})
				if err != nil {
					t.Fatalf("the catalog holds the library, so the walk resolves it; got: %v", err)
				}
				if err := lockfile.WriteLockfile(lockPath, lockfileOfLibraries(cl)); err != nil {
					t.Fatalf("write lockfile: %v", err)
				}
				written, err := os.ReadFile(lockPath)
				if err != nil {
					t.Fatalf("read lockfile: %v", err)
				}
				if firstLockfile == nil {
					firstLockfile = written
				} else if !bytes.Equal(written, firstLockfile) {
					t.Fatalf("the lockfile differs from the one the first run wrote\nfirst run:\n%s\nthis run:\n%s", firstLockfile, written)
				}
				lf, _, err := lockfile.LoadLockfile(lockPath)
				if err != nil {
					t.Fatalf("load lockfile: %v", err)
				}
				if len(lf.SchemaLibraries) != 1 {
					t.Fatalf("the lockfile pins %d schema libraries; want the one both consumers use", len(lf.SchemaLibraries))
				}
				return lf.SchemaLibraries[0].URI + " " + lf.SchemaLibraries[0].ResolvedVersion
			},
			want: "Base 1.0.0",
		},
		{
			name: "the sub-aggregate a refusal names when two siblings fail to load",
			// The top-level Aggregate includes a, and a includes zz then aa; both
			// resolve, and the loader holds a body for neither.
			observe: func(t *testing.T) string {
				t.Helper()
				ents := map[string]closure.Resolution{}
				for _, u := range []string{uriTop, uriA, uriSubZZ, uriSubAA} {
					ents[u] = resolutionOK("1.0.0", "sha256:"+fakeBaseName(u), "tier-3-pull")
				}
				loader := refusingLoader{uriA: subAggregate(t, "a", false, uriSubZZ, uriSubAA)}
				_, err := runDescent(t, subAggregate(t, "top", false, uriA), &fakeLookup{entities: ents}, loader)
				var refused *refusedLoad
				if !errors.As(err, &refused) {
					t.Fatalf("neither sibling loads, so the descent refuses naming one of them; got: %v", err)
				}
				return refused.uri
			},
			want: uriSubAA,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for run := 1; run <= walkOrderRuns; run++ {
				if got := tc.observe(t); got != tc.want {
					t.Fatalf("run %d of %d: got %q; want %q on every run", run, walkOrderRuns, got, tc.want)
				}
			}
		})
	}
}

// noLibraryBody reports every library's body as unavailable, so discovery resolves
// the libraries the consumers name and descends no further.
func noLibraryBody(closure.ResolvedLibrary) ([]*yaml.Node, bool, error) { return nil, false, nil }

// namedIn returns the one of names the message names, each written as a quoted
// string the way the refusal writes a library's name, and fails unless exactly one
// of them is.
func namedIn(t *testing.T, msg string, names ...string) string {
	t.Helper()
	var found []string
	for _, n := range names {
		if strings.Contains(msg, strconv.Quote(n)) {
			found = append(found, n)
		}
	}
	if len(found) != 1 {
		t.Fatalf("the refusal names %d of %v; want exactly one\nrefusal: %s", len(found), names, msg)
	}
	return found[0]
}

// lockfileOfLibraries copies a closure's schema-library rows into a lockfile field
// for field, as a pull does before it writes one, so that the bytes the lockfile
// writer produces for them can be compared from run to run.
func lockfileOfLibraries(cl closure.Closure) *lockfile.Lockfile {
	lf := &lockfile.Lockfile{Version: 1, Release: "release.yaml", GeneratedAt: walkOrderGeneratedAt}
	for _, l := range cl.SchemaLibraries {
		row := lockfile.SchemaLibrary{
			URI:             l.URI,
			ResolvedVersion: l.ResolvedVersion,
			Digest:          l.Digest,
			ManifestDigest:  l.ManifestDigest,
		}
		for _, c := range l.Consumers {
			row.Consumers = append(row.Consumers, lockfile.LibraryConsumer{
				ConsumerURI: c.ConsumerURI,
				Fragments:   append([]string(nil), c.Fragments...),
			})
		}
		lf.SchemaLibraries = append(lf.SchemaLibraries, row)
	}
	return lf
}
