// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The advisory suite's own tests.
//
// Two properties are separable here and both are asserted. What each reading
// REPORTS is asserted through [RunAdvisories], which is the surface a build
// reaches. That each reading is actually INVOKED by that surface is a distinct
// claim: a rule nothing invokes raises nothing, and a table of silence cases
// would pass over it in perfect green. So every silence case below is paired
// with a positive case over the same fixture shape.

// socketCatalog returns a lookup whose one Component declares two sockets, so a
// near miss has something to be near.
func socketCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI != "core/devtool/adminer" {
				// The edge target resolves to a socket-less Component. It must
				// be FOUND, or the catalog-view consistency guard halts on it
				// and the halt-freedom assertion below would be measuring the
				// wrong thing.
				return types.Component{Metadata: types.ComponentMetadata{Name: "mariadb", Version: "1.0.0"}}, true
			}
			sockets := types.OrderedMap[types.UsesSchemaEntry]{}
			sockets.Set("databases", types.UsesSchemaEntry{Selector: "component.aggrega.ai/type=stateful-server"})
			sockets.Set("caches", types.UsesSchemaEntry{})
			return types.Component{
				Metadata: types.ComponentMetadata{Name: "adminer", Version: "1.0.0"},
				Spec:     types.ComponentSpec{UsesSchema: sockets},
			}, true
		},
	}
}

// slotCatalog returns a lookup whose one Component declares two attachment
// slots, so a near miss has something to be near.
func slotCatalog() CatalogLookup {
	return CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			if typeURI != "core/datastore/db" {
				return types.Component{}, false
			}
			slots := types.OrderedMap[types.AttachmentSlot]{}
			slots.Set("db_password", types.AttachmentSlot{Inject: []string{"env"}})
			slots.Set("app_user_password", types.AttachmentSlot{Required: true})
			return types.Component{
				Metadata: types.ComponentMetadata{Name: "db", Version: "1.0.0"},
				Spec:     types.ComponentSpec{FeaturesSchema: slots},
			}, true
		},
	}
}

func aggWithSockets(t *testing.T, socketNames []string) types.NormalizedAggregate {
	t.Helper()
	uses := types.OrderedMap[types.UsesSocketValue]{}
	for _, n := range socketNames {
		edges := types.OrderedMap[types.VerboseUsesEntry]{}
		edges.Set("db", types.VerboseUsesEntry{Target: "db"})
		uses.Set(n, types.UsesSocketValue{Verbose: edges})
	}
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/datastore/mariadb"})
	components.Set("web", types.ComponentInstance{
		Type: "core/devtool/adminer",
		Uses: uses,
	})
	return normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Components: components},
	})
}

func aggWithAttachments(t *testing.T, attachmentNames []string) types.NormalizedAggregate {
	t.Helper()
	features := types.OrderedMap[types.FeatureInstance]{}
	for _, n := range attachmentNames {
		features.Set(n, types.FeatureInstance{Type: "core/some-feature"})
	}
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{
		Type:     "core/datastore/db",
		Features: features,
	})
	return normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Components: components},
	})
}

// advisoriesOf asks the whole advisory suite and refuses a cancellation error: a
// row supplying no signal that reported one would mean the suite stopped for a
// reason no row asked for.
func advisoriesOf(t *testing.T, na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
	t.Helper()
	return advisoriesBoundTo(t, na, catalog, nil)
}

// advisoriesBoundTo is advisoriesOf with a Recipe binding supplied, for the one
// reading keyed inside a bound Recipe. The nil-binding form above is kept
// separate rather than folded into this one, so every row that offers no binding
// says so at the call and the two cases stay distinguishable in the source.
func advisoriesBoundTo(
	t *testing.T,
	na types.NormalizedAggregate,
	catalog CatalogLookup,
	recipeFor RecipeBinding,
) []*ResolverError {
	t.Helper()
	got, err := RunAdvisories(nil, na, nil, catalog, recipeFor, nil)
	if err != nil {
		t.Fatalf("RunAdvisories returned an error with no cancellation signal supplied: %v", err)
	}
	return got
}

// messagesOfAdvisories renders the text an advisory set carries, joined by the
// caller when a row asserts that a spelling appears somewhere in the set.
func messagesOfAdvisories(advisories []*ResolverError) []string {
	out := make([]string, 0, len(advisories))
	for _, a := range advisories {
		out = append(out, a.Message)
	}
	return out
}

// TestRunAdvisories_SocketNearMiss covers the authored-socket reading through the
// surface a build reaches.
func TestRunAdvisories_SocketNearMiss(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		// sockets is what the component's own dependency block names.
		sockets []string
		catalog func() CatalogLookup
		// wantFields is the locator sequence, written out in full: it carries the
		// count, the order and the positioning in one assertion, and a sequence
		// compared against a second run of the same code would carry none of them.
		wantFields []string
		// wantNames are spellings every advisory must name between them, so a
		// reading that reported the right number of the wrong thing still fails.
		wantNames []string
	}{
		{
			// "databses" is one transposition from the declared "databases".
			name:       "typo reported",
			sockets:    []string{"databses"},
			wantFields: []string{".spec.components.web"},
			wantNames:  []string{"databses", "databases"},
		},
		{
			name:       "declared socket silent",
			sockets:    []string{"databases"},
			wantFields: nil,
		},
		{
			// The negative control that makes the reading honest rather than
			// noisy: a deliberate ad-hoc socket whose name resembles no declared
			// socket is ordinary authoring on this surface.
			name:       "deliberate ad-hoc socket silent",
			sockets:    []string{"telemetry"},
			wantFields: nil,
		},
		{
			// Two near misses on one component. One advisory per offending
			// socket, so the count is asserted over more than one occurrence.
			name:       "two offending sockets",
			sockets:    []string{"databses", "cahces"},
			wantFields: []string{".spec.components.web", ".spec.components.web"},
			wantNames:  []string{"databses", "cahces"},
		},
		{
			// Below the minimum length nearly every name is within one edit of
			// every other, so pairing them would produce noise rather than signal.
			name:    "short names not paired",
			sockets: []string{"dbz"},
			catalog: func() CatalogLookup {
				sockets := types.OrderedMap[types.UsesSchemaEntry]{}
				sockets.Set("dbs", types.UsesSchemaEntry{})
				return CatalogLookup{Component: func(string) (types.Component, bool) {
					return types.Component{Spec: types.ComponentSpec{UsesSchema: sockets}}, true
				}}
			},
			wantFields: nil,
		},
		{
			// A type declaring no socket has no near miss to report; every socket
			// an author names on it is ad-hoc by construction.
			name:    "type declaring no socket silent",
			sockets: []string{"databses"},
			catalog: func() CatalogLookup {
				return CatalogLookup{Component: func(string) (types.Component, bool) {
					return types.Component{Metadata: types.ComponentMetadata{Name: "bare"}}, true
				}}
			},
			wantFields: nil,
		},
		{
			name:       "catalog-less path silent",
			sockets:    []string{"databses"},
			catalog:    func() CatalogLookup { return CatalogLookup{} },
			wantFields: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			catalog := socketCatalog()
			if tc.catalog != nil {
				catalog = tc.catalog()
			}
			got := advisoriesOf(t, aggWithSockets(t, tc.sockets), catalog)

			if gotFields := fieldsOf(got); !equalStrings(gotFields, tc.wantFields) {
				t.Fatalf("locators = %v; want %v (advisories: %v)", gotFields, tc.wantFields, got)
			}
			for _, a := range got {
				if !errors.Is(a, ErrUsesSocketNearMiss()) {
					t.Errorf("advisory carries %v; want the socket near-miss sentinel", a.Err)
				}
			}
			joined := strings.Join(messagesOfAdvisories(got), " | ")
			for _, want := range tc.wantNames {
				if !strings.Contains(joined, want) {
					t.Errorf("the advisories do not name %q; got %q", want, joined)
				}
			}
		})
	}
}

// TestRunAdvisories_AttachmentNearMiss covers the attachment-name reading through
// the surface a build reaches.
func TestRunAdvisories_AttachmentNearMiss(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name        string
		attachments []string
		catalog     func() CatalogLookup
		wantFields  []string
		wantNames   []string
	}{
		{
			// "db_passwrod" is one transposition from the declared "db_password".
			name:        "typo reported",
			attachments: []string{"db_passwrod"},
			wantFields:  []string{".spec.components.db.features.db_passwrod"},
			wantNames:   []string{"db_passwrod", "db_password"},
		},
		{
			name:        "declared slot silent",
			attachments: []string{"db_password"},
			wantFields:  nil,
		},
		{
			// A free attachment whose name resembles no slot is ordinary
			// authoring: the surface is open, so this must stay quiet or the
			// reading becomes noise.
			name:        "distinct free attachment silent",
			attachments: []string{"monitoring"},
			wantFields:  nil,
		},
		{
			// Two offending attachments, each near a different declared slot.
			// The locators differ, so this row also pins that the reading is
			// invoked once per attachment rather than once per component.
			name:        "two offending attachments",
			attachments: []string{"db_passwor", "app_user_passwrod"},
			wantFields: []string{
				".spec.components.db.features.db_passwor",
				".spec.components.db.features.app_user_passwrod",
			},
			wantNames: []string{"db_passwor", "app_user_passwrod"},
		},
		{
			name:        "short names not paired",
			attachments: []string{"kez"},
			catalog: func() CatalogLookup {
				slots := types.OrderedMap[types.AttachmentSlot]{}
				slots.Set("key", types.AttachmentSlot{})
				return CatalogLookup{Component: func(string) (types.Component, bool) {
					return types.Component{Spec: types.ComponentSpec{FeaturesSchema: slots}}, true
				}}
			},
			wantFields: nil,
		},
		{
			name:        "type declaring no slot silent",
			attachments: []string{"db_passwrod"},
			catalog: func() CatalogLookup {
				return CatalogLookup{Component: func(string) (types.Component, bool) {
					return types.Component{Metadata: types.ComponentMetadata{Name: "bare"}}, true
				}}
			},
			wantFields: nil,
		},
		{
			name:        "catalog-less path silent",
			attachments: []string{"db_passwrod"},
			catalog:     func() CatalogLookup { return CatalogLookup{} },
			wantFields:  nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			catalog := slotCatalog()
			if tc.catalog != nil {
				catalog = tc.catalog()
			}
			got := advisoriesOf(t, aggWithAttachments(t, tc.attachments), catalog)

			if gotFields := fieldsOf(got); !equalStrings(gotFields, tc.wantFields) {
				t.Fatalf("locators = %v; want %v (advisories: %v)", gotFields, tc.wantFields, got)
			}
			for _, a := range got {
				if !errors.Is(a, ErrAttachmentSlotNearMiss()) {
					t.Errorf("advisory carries %v; want the attachment near-miss sentinel", a.Err)
				}
			}
			joined := strings.Join(messagesOfAdvisories(got), " | ")
			for _, want := range tc.wantNames {
				if !strings.Contains(joined, want) {
					t.Errorf("the advisories do not name %q; got %q", want, joined)
				}
			}
		})
	}
}

// TestRunAdvisories_OneObjectionPerPair pins the multiplicity the declarations
// state: one objection per near-miss PAIR, so a single authored name sitting one
// edit from two declared names is reported against both.
//
// It is a separate test because it needs a catalog built for the property: the
// fixtures above declare names that are far apart, and over those a per-name
// reading and a per-pair reading are indistinguishable.
func TestRunAdvisories_OneObjectionPerPair(t *testing.T) {
	t.Parallel()

	t.Run("socket near two declared sockets", func(t *testing.T) {
		t.Parallel()
		sockets := types.OrderedMap[types.UsesSchemaEntry]{}
		sockets.Set("cachet", types.UsesSchemaEntry{})
		sockets.Set("caches", types.UsesSchemaEntry{})
		catalog := CatalogLookup{Component: func(string) (types.Component, bool) {
			return types.Component{Spec: types.ComponentSpec{UsesSchema: sockets}}, true
		}}

		// "cachex" is one substitution from BOTH declared names.
		got := advisoriesOf(t, aggWithSockets(t, []string{"cachex"}), catalog)
		if len(got) != 2 {
			t.Fatalf("advisories = %d; want 2, one per pair: %v", len(got), got)
		}
		joined := strings.Join(messagesOfAdvisories(got), " | ")
		for _, want := range []string{"cachet", "caches"} {
			if !strings.Contains(joined, want) {
				t.Errorf("the pair naming %q is missing; got %q", want, joined)
			}
		}
	})

	t.Run("attachment near two declared slots", func(t *testing.T) {
		t.Parallel()
		slots := types.OrderedMap[types.AttachmentSlot]{}
		slots.Set("token_a", types.AttachmentSlot{})
		slots.Set("token_b", types.AttachmentSlot{})
		catalog := CatalogLookup{Component: func(string) (types.Component, bool) {
			return types.Component{Spec: types.ComponentSpec{FeaturesSchema: slots}}, true
		}}

		// "token_c" is one substitution from BOTH declared slots.
		got := advisoriesOf(t, aggWithAttachments(t, []string{"token_c"}), catalog)
		if len(got) != 2 {
			t.Fatalf("advisories = %d; want 2, one per pair: %v", len(got), got)
		}
		joined := strings.Join(messagesOfAdvisories(got), " | ")
		for _, want := range []string{"token_a", "token_b"} {
			if !strings.Contains(joined, want) {
				t.Errorf("the pair naming %q is missing; got %q", want, joined)
			}
		}
	})
}

// TestRunAdvisories_SubsystemComponentWalked pins that a component a declared
// scope holds is walked, and that its locator names the scope the author wrote
// rather than a flattened path.
func TestRunAdvisories_SubsystemComponentWalked(t *testing.T) {
	t.Parallel()

	uses := types.OrderedMap[types.UsesSocketValue]{}
	edges := types.OrderedMap[types.VerboseUsesEntry]{}
	edges.Set("db", types.VerboseUsesEntry{Target: "db"})
	uses.Set("databses", types.UsesSocketValue{Verbose: edges})

	inner := types.OrderedMap[types.ComponentInstance]{}
	inner.Set("web", types.ComponentInstance{Type: "core/devtool/adminer", Uses: uses})
	subs := types.OrderedMap[types.Subsystem]{}
	subs.Set("edge", types.Subsystem{Components: inner})

	na := normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Subsystems: subs},
	})
	got := advisoriesOf(t, na, socketCatalog())

	want := []string{".spec.subsystems.edge.components.web"}
	if gotFields := fieldsOf(got); !equalStrings(gotFields, want) {
		t.Fatalf("locators = %v; want %v (advisories: %v)", gotFields, want, got)
	}
}

// TestRunAdvisories_NeverReachesTheHaltingSuite is the in-package half of the
// severity contract: an input tripping either reading yields no guard finding at
// all, so nothing the advisory suite reports can reach a build's verdict through
// the halting suite.
//
// The out-of-package half — that a real resolve over such an input returns no
// error — is asserted where the two channels meet, at the pipeline seam.
func TestRunAdvisories_NeverReachesTheHaltingSuite(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		na      func(*testing.T) types.NormalizedAggregate
		catalog CatalogLookup
	}{
		{
			name:    "socket near miss",
			na:      func(t *testing.T) types.NormalizedAggregate { return aggWithSockets(t, []string{"databses"}) },
			catalog: socketCatalog(),
		},
		{
			name:    "attachment near miss",
			na:      func(t *testing.T) types.NormalizedAggregate { return aggWithAttachments(t, []string{"db_passwrod"}) },
			catalog: slotCatalog(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			na := tc.na(t)
			errs, err := RunGuards(nil, na, nil, tc.catalog, nil, nil)
			if err != nil || len(errs) != 0 {
				t.Fatalf("the near-miss reading must not halt through the guard suite; got %v (err %v)", errs, err)
			}
			// The positive control: without it the assertion above would pass
			// over an input that trips nothing at all.
			if got := advisoriesOf(t, na, tc.catalog); len(got) != 1 {
				t.Fatalf("expected exactly one advisory from the advisory suite; got %v", got)
			}
		})
	}
}

// TestRunAdvisories_Deterministic asserts a byte-identical advisory sequence
// across runs of identical input, over an input yielding more than one advisory —
// a single-advisory input cannot tell one ordering from another.
func TestRunAdvisories_Deterministic(t *testing.T) {
	t.Parallel()

	na := aggWithSockets(t, []string{"databses", "cahces"})
	first := advisoriesOf(t, na, socketCatalog())
	if len(first) != 2 {
		t.Fatalf("expected 2 advisories; got %d: %v", len(first), first)
	}
	for i := range 8 {
		again := advisoriesOf(t, na, socketCatalog())
		if len(again) != len(first) {
			t.Fatalf("run %d: length %d != %d", i, len(again), len(first))
		}
		for j := range first {
			if again[j].Error() != first[j].Error() {
				t.Fatalf("run %d, advisory %d: %q != %q", i, j, again[j].Error(), first[j].Error())
			}
		}
	}
}

// TestRunAdvisories_NoAggregateDocumentIsSilent pins the one input that ends
// the suite before any traversal: with no application document there is nothing
// to ask a reading about.
func TestRunAdvisories_NoAggregateDocumentIsSilent(t *testing.T) {
	t.Parallel()

	got, err := RunAdvisories(nil, types.NormalizedAggregate{}, nil, socketCatalog(), nil, nil)
	if err != nil {
		t.Fatalf("RunAdvisories: %v", err)
	}
	if got != nil {
		t.Fatalf("expected no advisories with no application document; got %v", got)
	}
}

// countingCancellation returns a signal that counts its reads and turns non-nil
// once it has been read stopAfter times.
//
// The count is the measurement that matters. An error return alone cannot tell a
// walk that stopped early from one that enumerated everything and reported the
// cancellation at the end, because both hand back the same error.
func countingCancellation(stopAfter int, stop error) (constraint.Cancellation, *int) {
	reads := 0
	return func() error {
		reads++
		if reads > stopAfter {
			return stop
		}
		return nil
	}, &reads
}

// errAdvisoryWalkCancelled is the reason a cancelled advisory walk hands back.
var errAdvisoryWalkCancelled = errors.New("advisory walk cancelled")

// TestRunAdvisories_ObservesCancellationPerIteration measures that each reading
// reads the cancellation signal once per outer iteration and stops PARTWAY.
//
// Each rule's traversal is driven directly rather than through [RunAdvisories],
// because the suite runs both in sequence: a signal that turned during the first
// would leave the second unmeasured, and the claim is made per reading.
func TestRunAdvisories_ObservesCancellationPerIteration(t *testing.T) {
	t.Parallel()

	const instances = 400
	const stopAfter = 3

	for _, tc := range []struct {
		name string
		// build returns a document with n offending instances and the catalog to
		// read it against.
		build func(*testing.T, int) (types.NormalizedAggregate, CatalogLookup)
		// run drives one reading's own traversal.
		run func(constraint.Cancellation, *normalizedView) ([]constraint.Objection, error)
	}{
		{
			name: "authored socket reading",
			build: func(t *testing.T, n int) (types.NormalizedAggregate, CatalogLookup) {
				return manyOffendingComponents(t, n), socketCatalog()
			},
			run: func(cancelled constraint.Cancellation, v *normalizedView) ([]constraint.Objection, error) {
				return constraint.RunOnNodes(
					cancelled, newCoordinateSet(v), constraint.NormalizedView(v),
					authoredSocketNameNearADeclaredOne)
			},
		},
		{
			name: "attachment reading",
			build: func(t *testing.T, n int) (types.NormalizedAggregate, CatalogLookup) {
				return manyOffendingAttachments(t, n), slotCatalog()
			},
			run: func(cancelled constraint.Cancellation, v *normalizedView) ([]constraint.Objection, error) {
				coordinates := newCoordinateSet(v)
				return constraint.RunOnNodeAttachments(
					cancelled, coordinates, coordinates, constraint.NormalizedView(v),
					attachmentNameNearADeclaredSlot)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			na, catalog := tc.build(t, instances)
			view := newNormalizedView(na, nil, catalog, nil)
			// The outer iteration count is the node count, which is MEASURED off
			// the enumeration rather than assumed equal to the offender count: a
			// fixture may carry a component that offends nothing, and an assumed
			// total would make the comparison below report the fixture's shape.
			outerIterations := len(newCoordinateSet(view).Nodes())
			if outerIterations < instances {
				t.Fatalf("the fixture enumerates %d node(s) for %d offending instance(s); the walk "+
					"cannot reach them all", outerIterations, instances)
			}

			// The uncancelled walk first, so the total the cancelled walk is
			// compared against is measured rather than assumed.
			uncancelled, readsWhole := countingCancellation(outerIterations*2, errAdvisoryWalkCancelled)
			whole, err := tc.run(uncancelled, view)
			if err != nil {
				t.Fatalf("uncancelled walk: %v", err)
			}
			if len(whole) != instances {
				t.Fatalf("uncancelled walk raised %d objection(s); want one per offending instance (%d)",
					len(whole), instances)
			}
			if *readsWhole != outerIterations {
				t.Fatalf("uncancelled walk read the signal %d time(s); want once per outer iteration (%d)",
					*readsWhole, outerIterations)
			}

			cancelled, readsPartial := countingCancellation(stopAfter, errAdvisoryWalkCancelled)
			partial, err := tc.run(cancelled, view)
			if !errors.Is(err, errAdvisoryWalkCancelled) {
				t.Fatalf("cancelled walk returned %v; want the signal's own reason", err)
			}
			if partial != nil {
				t.Errorf("cancelled walk handed back %d objection(s); a stopped walk hands back none",
					len(partial))
			}
			// The assertion an error return cannot make: the walk performed a
			// small number of iterations rather than all of them.
			if *readsPartial != stopAfter+1 {
				t.Fatalf("cancelled walk read the signal %d time(s); want %d — one read per iteration "+
					"until the signal turned", *readsPartial, stopAfter+1)
			}
			if *readsPartial >= *readsWhole {
				t.Fatalf("cancelled walk performed %d iteration(s) of %d; it did not stop partway",
					*readsPartial, *readsWhole)
			}
		})
	}
}

// TestRunAdvisories_SuitePropagatesCancellation pins that the suite itself hands
// the signal's reason back rather than swallowing it into a partial set.
func TestRunAdvisories_SuitePropagatesCancellation(t *testing.T) {
	t.Parallel()

	na := manyOffendingComponents(t, 40)
	cancelled, reads := countingCancellation(2, errAdvisoryWalkCancelled)
	got, err := RunAdvisories(cancelled, na, nil, socketCatalog(), nil, nil)
	if !errors.Is(err, errAdvisoryWalkCancelled) {
		t.Fatalf("RunAdvisories returned %v; want the signal's own reason", err)
	}
	if got != nil {
		t.Errorf("a stopped suite hands back no findings; got %d", len(got))
	}
	if *reads > 3 {
		t.Errorf("the suite read the signal %d time(s) after it turned at 3; it did not stop", *reads)
	}
}

// manyOffendingComponents builds a document with n components, each naming one
// dependency socket one edit from a declared one.
func manyOffendingComponents(t *testing.T, n int) types.NormalizedAggregate {
	t.Helper()
	components := types.OrderedMap[types.ComponentInstance]{}
	components.Set("db", types.ComponentInstance{Type: "core/datastore/mariadb"})
	for i := range n {
		uses := types.OrderedMap[types.UsesSocketValue]{}
		edges := types.OrderedMap[types.VerboseUsesEntry]{}
		edges.Set("db", types.VerboseUsesEntry{Target: "db"})
		uses.Set("databses", types.UsesSocketValue{Verbose: edges})
		components.Set(instanceName(i), types.ComponentInstance{
			Type: "core/devtool/adminer",
			Uses: uses,
		})
	}
	return normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Components: components},
	})
}

// manyOffendingAttachments builds a document with n components, each carrying one
// attachment named one edit from a declared slot.
func manyOffendingAttachments(t *testing.T, n int) types.NormalizedAggregate {
	t.Helper()
	components := types.OrderedMap[types.ComponentInstance]{}
	for i := range n {
		features := types.OrderedMap[types.FeatureInstance]{}
		features.Set("db_passwrod", types.FeatureInstance{Type: "core/some-feature"})
		components.Set(instanceName(i), types.ComponentInstance{
			Type:     "core/datastore/db",
			Features: features,
		})
	}
	return normalized(t, &types.Aggregate{
		Metadata: types.AggregateMetadata{Name: "test-app"},
		Spec:     types.AggregateSpec{Components: components},
	})
}

// instanceName spells one generated instance's name, zero-padded so the
// name-sorted walk order matches the order they were built in.
func instanceName(i int) string {
	return fmt.Sprintf("web%04d", i)
}
