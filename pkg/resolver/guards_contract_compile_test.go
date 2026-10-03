// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The refusal a declared contract earns by not compiling, asserted at the surface
// that composes it.
//
// Three contracts can carry the fault and each is authored in its own field: the
// component type's own contract, and the two per-edge contracts a dependency
// socket declares. All three are exercised, because each is read at a different
// site and each collapsed into the same absence before this refusal existed —
// so a fixture covering one of them says nothing about the other two.
//
// Every case here drives [RunGuards], which is the surface a build's verdict comes
// from. Asserting against the classification alone would prove the resolution kept
// the failure and leave the thing under test — that a build refuses it — unmeasured.

// contractFixtureSocket is the socket every socket-side fixture below declares.
const contractFixtureSocket = "datastores"

// socketContractComponent builds a catalog Component declaring one dependency
// socket whose two per-edge contracts carry the supplied authored content, each
// omitted when its argument is empty.
//
// The socket is declared unrequired so that a fixture exercising a broken contract
// raises that condition and not the separate one about an unpopulated mandatory
// socket. One fault per fixture is what lets each assertion below name the
// condition it pins instead of accepting any refusal at all.
func socketContractComponent(t *testing.T, valueSchema, infraSchema string) types.Component {
	t.Helper()
	entry := types.UsesSchemaEntry{Required: false}
	if valueSchema != "" {
		entry.ValueSchema = *contractNode(t, valueSchema)
	}
	if infraSchema != "" {
		entry.InfraSchema = *contractNode(t, infraSchema)
	}
	comp := types.Component{Metadata: types.ComponentMetadata{Name: "app/svc", Version: "1.0.0"}}
	comp.Spec.UsesSchema.Set(contractFixtureSocket, entry)
	return comp
}

// contractNode parses authored contract content into the node shape a socket's
// field actually holds.
//
// Unmarshalling standalone text yields a document wrapper, and a socket's field
// never carries one: loading an entity decodes the field from inside a larger
// document, so the field holds the mapping directly. A fixture that kept the
// wrapper would present the compile with a shape production cannot produce — and
// it would fail, so every row would refuse and the whole table would pass while
// measuring the fixture instead of the contract.
func contractNode(t *testing.T, content string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatalf("contract fixture parse: %v", err)
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) == 1 {
		return doc.Content[0]
	}
	t.Fatalf("contract fixture did not parse to a single document: kind %v", doc.Kind)
	return nil
}

// guardFindings asks the whole declared suite over one Aggregate and returns
// what a build would halt on.
func guardFindings(t *testing.T, catalog CatalogLookup) []*ResolverError {
	t.Helper()
	na := oneInstanceAggregate(t, "svc", types.OrderedMap[any]{})
	errs, err := RunGuards(nil, na, nil, "", catalog, nil, nil)
	if err != nil {
		t.Fatalf("the guard suite returned an error: %v", err)
	}
	return errs
}

// findingOn returns the finding carrying the compile-failure condition at the
// named authored field, and reports whether the suite raised one there.
func findingOn(errs []*ResolverError, field string) (*ResolverError, bool) {
	for _, e := range errs {
		if errors.Is(e, ErrSchemaContractCompileFailure()) && e.Field == field {
			return e, true
		}
	}
	return nil, false
}

// TestADeclaredContractThatDoesNotCompileHaltsTheBuild covers all three fields a
// schema contract can be declared in, plus a positive control per field.
//
// The control is what makes each refusal attributable. A broken contract in one
// field and a well-formed one in the same field are the only difference between
// the rows of a pair, so a row that refuses proves the field is read — and a
// control that refuses would mean the fixture, not the fault, produced the halt.
//
// INVARIANT: a contract that was declared and did not compile refuses the build, in
// whichever of the three fields it was declared.
// CONSEQUENCE: were a row to stop refusing, every payload measured against that
// contract would go unvalidated with nothing in the build saying so — which is a
// check retired in silence rather than a check removed.
func TestADeclaredContractThatDoesNotCompileHaltsTheBuild(t *testing.T) {
	t.Parallel()

	// A keyword the compact form does not carry. It sits beside a well-formed
	// constraint on purpose: the sibling is what would have been enforced, so a
	// row that does not refuse is one where a working constraint went unread.
	const brokenPillar = "pool_size: { type: integer }\ntimeout: { type: integer, minimun: 1 }\n"
	const soundPillar = "pool_size: { type: integer }\ntimeout: { type: integer, minimum: 1 }\n"

	cases := []struct {
		name string
		// catalog is what the fixture type resolves to.
		catalog CatalogLookup
		// field is the authored field the refusal must name.
		field string
		// wantHalt is whether a build carrying this catalog is refused.
		wantHalt bool
	}{
		{
			name: "component contract does not compile",
			catalog: singleTypeCatalog(schemaComponent(t, "app/svc",
				"values:\n  "+strings.ReplaceAll(brokenPillar, "\n", "\n  "))),
			field:    ".spec.schema",
			wantHalt: true,
		},
		{
			name: "component contract compiles",
			catalog: singleTypeCatalog(schemaComponent(t, "app/svc",
				"values:\n  "+strings.ReplaceAll(soundPillar, "\n", "\n  "))),
			field:    ".spec.schema",
			wantHalt: false,
		},
		{
			name:     "socket developer contract does not compile",
			catalog:  singleTypeCatalog(socketContractComponent(t, brokenPillar, "")),
			field:    ".spec.usesSchema." + contractFixtureSocket + ".valueSchema",
			wantHalt: true,
		},
		{
			name:     "socket developer contract compiles",
			catalog:  singleTypeCatalog(socketContractComponent(t, soundPillar, "")),
			field:    ".spec.usesSchema." + contractFixtureSocket + ".valueSchema",
			wantHalt: false,
		},
		{
			name:     "socket operator contract does not compile",
			catalog:  singleTypeCatalog(socketContractComponent(t, "", brokenPillar)),
			field:    ".spec.usesSchema." + contractFixtureSocket + ".infraSchema",
			wantHalt: true,
		},
		{
			name:     "socket operator contract compiles",
			catalog:  singleTypeCatalog(socketContractComponent(t, "", soundPillar)),
			field:    ".spec.usesSchema." + contractFixtureSocket + ".infraSchema",
			wantHalt: false,
		},
		{
			name:     "no contract declared anywhere",
			catalog:  singleTypeCatalog(socketContractComponent(t, "", "")),
			field:    ".spec.usesSchema." + contractFixtureSocket + ".valueSchema",
			wantHalt: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, halted := findingOn(guardFindings(t, tc.catalog), tc.field)
			if halted != tc.wantHalt {
				t.Fatalf("the suite refused at %s: %v; want %v (finding: %v).\n"+
					"INVARIANT: a declared contract that did not compile refuses the build; a "+
					"field declaring no contract, or one declaring a contract that compiles, "+
					"does not.\n"+
					"CONSEQUENCE: a build either goes unvalidated with nothing saying so, or "+
					"refuses authoring the compiler accepts.",
					tc.field, halted, tc.wantHalt, got)
			}
		})
	}
}

// TestTheRefusalTellsTheTwoAbsencesApart is the distinction the whole condition
// exists for: the classification is asserted on the condition's identity, and
// the occurrence detail — the offending keyword and the key it was written on —
// on the emitted text, because no identity carries either.
//
// The split is deliberate. An earlier form asserted the classification by
// looking for a fragment of the condition's registered sentence in the message,
// which reads like a stronger check and is a weaker one: it fails the day the
// sentence is reworded, when nothing about the behaviour has moved, and it would
// equally pass on any other condition whose sentence happened to share the
// fragment.
//
// "Nothing was declared" and "something was declared and cannot be read" are
// different situations for an author: the first is a socket they may leave alone,
// the second is a file they must edit. A build that reported them the same way
// would send an author looking for a payload fault that is not there.
//
// INVARIANT: the two absences produce different build outcomes, and the one that
// refuses says what is wrong with the declaration.
// CONSEQUENCE: were this to fail, the refusal would either not distinguish the
// cases or would not say which of them it met — leaving the author to guess.
func TestTheRefusalTellsTheTwoAbsencesApart(t *testing.T) {
	t.Parallel()

	declaredNone := guardFindings(t, singleTypeCatalog(socketContractComponent(t, "", "")))
	for _, e := range declaredNone {
		if errors.Is(e, ErrSchemaContractCompileFailure()) {
			t.Fatalf("a socket declaring no contract was refused: %v.\n"+
				"INVARIANT: declaring no contract is a choice the compiler accepts.\n"+
				"CONSEQUENCE: opt-in per-edge conformance becomes mandatory, refusing every "+
				"catalog entity that leaves a socket's configuration unconstrained.", e)
		}
	}

	declaredBroken := guardFindings(t, singleTypeCatalog(socketContractComponent(t,
		"timeout: { type: integer, minimun: 1 }\n", "")))
	got, halted := findingOn(declaredBroken,
		".spec.usesSchema."+contractFixtureSocket+".valueSchema")
	if !halted {
		t.Fatalf("a socket declaring a contract that does not compile was accepted.\n" +
			"INVARIANT: the two absences differ in the build's verdict.\n" +
			"CONSEQUENCE: the distinction this condition exists for is not observable.")
	}

	if !errors.Is(got, ErrSchemaContractCompileFailure()) {
		t.Errorf("the refusal does not carry the compile-failure condition: %v\n"+
			"INVARIANT: the refusal is matchable as the condition it met.\n"+
			"CONSEQUENCE: a caller routing on the condition cannot tell this refusal from "+
			"any other, and the two absences stop being distinguishable programmatically.", got)
	}

	text := got.Error()
	for _, want := range []string{"minimun", "timeout"} {
		if !strings.Contains(text, want) {
			t.Errorf("the emitted diagnostic does not carry %q: %s\n"+
				"INVARIANT: the refusal names the offending keyword and the key it was "+
				"written on.\n"+
				"CONSEQUENCE: an author is told a contract is unreadable and not which part of "+
				"it to correct.", want, text)
		}
	}
}

// TestASocketContractFailureIsLocatedWhereItWasAuthored pins the one thing about
// this diagnostic that cannot be read off the fixture: the path it reports.
//
// A socket declares its contract as bare pillar content, and the compile the
// conformance machinery needs wraps that content under a synthetic pillar key. The
// key is not in the author's file, so a diagnostic carrying it locates the mistake
// at a path the author cannot find.
//
// INVARIANT: the reported path is one the authored document contains.
// CONSEQUENCE: were this to fail, an author would be sent to a key that exists
// only inside the compiler — the most expensive kind of wrong diagnostic, because
// it looks precise.
func TestASocketContractFailureIsLocatedWhereItWasAuthored(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		// pillar is the synthetic key the compile wraps this field's content in.
		pillar string
		// component carries the broken contract in one of the socket's two fields.
		component types.Component
		// field is the authored field the refusal must name.
		field string
	}{
		{
			name:      "developer pillar",
			pillar:    "values",
			component: socketContractComponent(t, "timeout: { type: integer, minimun: 1 }\n", ""),
			field:     ".spec.usesSchema." + contractFixtureSocket + ".valueSchema",
		},
		{
			name:      "operator pillar",
			pillar:    "infra",
			component: socketContractComponent(t, "", "timeout: { type: integer, minimun: 1 }\n"),
			field:     ".spec.usesSchema." + contractFixtureSocket + ".infraSchema",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, halted := findingOn(guardFindings(t, singleTypeCatalog(tc.component)), tc.field)
			if !halted {
				t.Fatalf("the fixture was accepted; there is no diagnostic to read.\n" +
					"INVARIANT: the fixture must reach the refusal.\n" +
					"CONSEQUENCE: the path assertion below passes over nothing.")
			}
			if strings.Contains(got.Message, tc.pillar+".timeout") {
				t.Errorf("the diagnostic locates the fault at %q: %s\n"+
					"INVARIANT: the synthetic pillar key the compile wraps the content in does "+
					"not reach the reported path.\n"+
					"CONSEQUENCE: the author is sent to a key their file does not contain, at a "+
					"path that reads as though it were theirs.", tc.pillar+".timeout", got.Message)
			}
			if !strings.Contains(got.Message, "timeout") {
				t.Errorf("the diagnostic names no key at all: %s\n"+
					"INVARIANT: trimming the synthetic key leaves the authored one.\n"+
					"CONSEQUENCE: the fix above removed the location instead of correcting it.",
					got.Message)
			}
		})
	}
}

// TestTheContractRefusalPrecedesTheFindingsItSuppressed pins the order the two
// conditions arrive in.
//
// A contract that did not compile is why the rules over it report nothing, so it
// is reported ahead of them. Reported afterwards it would sit beneath findings the
// silence produced, and a reader scanning a refusal from the top would act on a
// consequence before seeing its cause.
//
// INVARIANT: the compile-failure findings lead the collection a build halts on.
// CONSEQUENCE: were this to fail, a build refusing for several reasons at once
// would bury the one that explains the others.
func TestTheContractRefusalPrecedesTheFindingsItSuppressed(t *testing.T) {
	t.Parallel()

	// One type, two faults: a socket contract that does not compile, and a
	// configuration value that fails the type's own contract, which does. The
	// second is a rule finding, so the collection carries one of each and their
	// order is observable. Both contracts are on one type on purpose — a fixture
	// splitting them across two would leave the order to depend on which type
	// sorted first rather than on which kind of finding it is.
	entry := types.UsesSchemaEntry{}
	entry.ValueSchema = *contractNode(t, "timeout: { type: integer, minimun: 1 }\n")
	comp := types.Component{Metadata: types.ComponentMetadata{Name: "app/svc", Version: "1.0.0"}}
	comp.Spec.UsesSchema.Set(contractFixtureSocket, entry)
	comp.Spec.Schema = *contractNode(t, "values:\n  top_k: { type: integer }\n")

	values := types.OrderedMap[any]{}
	values.Set("top_k", "not an integer")
	na := oneInstanceAggregate(t, "svc", values)
	errs, err := RunGuards(nil, na, nil, "", singleTypeCatalog(comp), nil, nil)
	if err != nil {
		t.Fatalf("the guard suite returned an error: %v", err)
	}
	if len(errs) < 2 {
		t.Fatalf("the fixture produced %d finding(s): %v.\n"+
			"INVARIANT: the fixture must carry a rule finding as well as the compile failure.\n"+
			"CONSEQUENCE: an ordering assertion over one finding measures nothing.",
			len(errs), errs)
	}
	if !errors.Is(errs[len(errs)-1], ErrSchemaConformance()) {
		t.Fatalf("the fixture's second fault is %v, not a conformance finding.\n"+
			"INVARIANT: the fixture carries one finding of each kind.\n"+
			"CONSEQUENCE: the order below is asserted over two findings of the same kind, which "+
			"proves nothing about which kind leads.", errs[len(errs)-1])
	}
	if !errors.Is(errs[0], ErrSchemaContractCompileFailure()) {
		t.Errorf("the collection leads with %v.\n"+
			"INVARIANT: the cause of a silence is reported ahead of what the silence "+
			"suppressed.\n"+
			"CONSEQUENCE: a reader acting on the first refusal chases a consequence.", errs[0])
	}
}
