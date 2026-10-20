// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// The subject-kind completeness assertion.
//
// The kind set is derived, so it moves when the facts move: declare a fact keyed
// by something new and a kind appears; remove the last fact keyed by something
// and its kind goes. A set of hand-written subject types mirroring a generated
// set therefore needs a mechanism holding the mirror true, and the failure mode
// worth designing against is not an obviously missing type — it is a set that
// reads as complete while one kind has no type, discovered by the first check
// that cannot declare itself and has no way to say so.
//
// So this reads the generated set at test time. A test enumerating the kinds it
// expects would pass a set that grew one more, which is exactly the case it
// exists to catch.

// The same reasoning covers the traversal, which is why this assertion carries
// both halves rather than sitting beside a second one. A kind is realised when a
// check can declare it AND the framework can invoke a check over it; a kind with a
// type and no traversal is a kind a check can declare and nothing will ever run,
// which reads exactly like a kind that works. So the requirement is read off the
// generated set's own traversal model: an enumerated kind owes a traversal, and a
// kind the caller presents does not.

// TestEveryDerivedSubjectKindHasASealedSubjectTypeAndEveryPullKindATraversal is
// that assertion. It walks the generated kind set and requires, for each kind, a
// type of the name the kind's own coordinates derive, embedding the subject
// contract and carrying one unexported marker of its own — and, for each kind the
// framework enumerates, exactly one function taking a check declared over that
// type.
//
// Re-verifying the seal half means perturbing a subject TYPE, never a kind's
// coordinates, and the reason is that a type's name is DERIVED from those
// coordinates. Adding a coordinate to needle the seal renames the type; nothing
// declares the new name; the missing-type branch reports that and moves on to the
// next kind, so the seal checks below it are not reached at all and the message
// describes a defect nobody meant to introduce. Perturb the declaration instead —
// drop an existing subject type's marker method, or give two of them the same
// marker — and the branch under test is entered directly.
func TestEveryDerivedSubjectKindHasASealedSubjectTypeAndEveryPullKindATraversal(t *testing.T) {
	t.Parallel()

	if len(SubjectKinds) == 0 {
		t.Fatalf("the generated kind set is empty, so a clean result here certifies nothing")
	}

	declared := packageInterfaceDecls(t)
	traversals := packageTraversalsBySubjectType(t)

	// Calibration for the traversal half, on the same principle as the parse
	// calibration below: a discovery that found nothing would report every
	// enumerated kind as untraversed, and one that matched anything would report
	// every kind as traversed.
	if len(traversals["NodeSubject"]) == 0 {
		t.Fatalf("the traversal discovery does not see a function taking a check over NodeSubject, and "+
			"one is declared, so it is not a working instrument and its verdict on the other kinds means "+
			"nothing (saw %d subject type(s) with a traversal)", len(traversals))
	}
	if len(traversals["NoSuchDerivedKindSubject"]) != 0 {
		t.Fatalf("the traversal discovery claims a traversal over NoSuchDerivedKindSubject, which is " +
			"declared nowhere, so it would report every kind as traversed")
	}

	// Calibration. A parse that silently found nothing would report every kind
	// as missing; a name-derivation that matched anything would report every
	// kind as present. Require both instruments to place a known case before
	// believing what they say about the rest.
	if declared["DocumentSubject"] == nil {
		t.Fatalf("the package parse does not see DocumentSubject, which is declared, so it is not a "+
			"working instrument and its verdict on the other kinds means nothing (saw %d interfaces)",
			len(declared))
	}
	if declared["NoSuchDerivedKindSubject"] != nil {
		t.Fatalf("the package parse claims to see NoSuchDerivedKindSubject, which is declared nowhere, " +
			"so it would report every kind as present")
	}
	if got, want := subjectTypeNameFor([]string{"node", "mechanism", "delivery-key"}), "NodeMechanismDeliveryKeySubject"; got != want {
		t.Fatalf("the name derivation maps (node, mechanism, delivery-key) to %q; want %q — the "+
			"derivation is what links a generated kind to its type, so a wrong one would look for "+
			"names nothing declares", got, want)
	}

	markerOwners := map[string]string{}
	expected := map[string]SubjectKind{}

	for _, info := range SubjectKinds {
		name := subjectTypeNameFor(info.Coordinates)
		if other, clash := expected[name]; clash {
			t.Errorf("kinds %q and %q both derive the type name %s, so one of them cannot have a "+
				"distinct type. Two kinds indistinguishable at the type level defeat the seal: a check "+
				"declaring one could be handed the other.", other, info.Kind, name)
			continue
		}
		expected[name] = info.Kind

		if info.Traversal == TraversalPull {
			switch runners := traversals[name]; len(runners) {
			case 1:
			case 0:
				t.Errorf("the derived subject kind %s has no traversal. Declare one taking a "+
					"Check[V, %s]: it enumerates the kind's coordinates, assembles each instance's "+
					"provenance, and invokes the check once per instance.\n"+
					"INVARIANT: the framework owns traversal, so every kind it enumerates has exactly one "+
					"traversal and a check never writes the loop.\n"+
					"CONSEQUENCE: a kind with a type and no traversal is a kind a check can declare and "+
					"nothing will ever invoke. It reads as complete — the type exists, the check compiles "+
					"— and the rule is simply never evaluated, which is indistinguishable from a rule that "+
					"ran and found nothing.", info.Kind, name)
			default:
				sort.Strings(runners)
				t.Errorf("%s has %d traversals (%s); want exactly one. Two traversals over one kind are "+
					"two definitions of what \"once per instance\" means for it, and a check declaring the "+
					"kind cannot say which it wanted.", name, len(runners), strings.Join(runners, ", "))
			}
		}

		iface := declared[name]
		if iface == nil {
			t.Errorf("the derived subject kind %s has no subject type. Declare %s in this package: an "+
				"interface embedding Subject plus one unexported marker method, an unexported struct "+
				"embedding provenance, and an unexported constructor taking the source file and the "+
				"segments the author typed.\n"+
				"INVARIANT: every derived kind has a distinct sealed subject type, so a check declaring "+
				"one kind cannot be handed another.\n"+
				"CONSEQUENCE: a kind with no type is a kind no check can declare. The check exists, the "+
				"rule it enforces exists, and the only way to write it is outside the framework — with "+
				"no computed placement, no caller coverage and no framework-supplied provenance.",
				info.Kind, name)
			continue
		}

		// The seal asserted directly rather than through a proxy: the kind embeds
		// Subject, and it declares exactly one unexported method, uniquely its
		// own. Exported methods are admitted without limit — they are the
		// coordinates the kind carries, and they cannot be declared by a type
		// outside this package in a way that satisfies the kind, because the
		// unexported marker is still out of that type's reach.
		embedsSubject, markers, exportedMethods := interfaceShape(iface)
		if !embedsSubject {
			t.Errorf("%s does not embed Subject, so it is not a subject kind: it carries no provenance "+
				"and cannot raise", name)
		}
		switch len(markers) {
		case 1:
			marker := markers[0]
			if owner, taken := markerOwners[marker]; taken {
				t.Errorf("%s and %s both use the marker method %s, so the two kinds are the same type "+
					"to the compiler and a check declaring one can be handed the other", owner, name, marker)
				continue
			}
			markerOwners[marker] = name
		case 0:
			sort.Strings(exportedMethods)
			declares := "declares no method of its own"
			if len(exportedMethods) > 0 {
				declares = fmt.Sprintf("declares only exported methods (%s)", strings.Join(exportedMethods, ", "))
			}
			t.Errorf("%s %s, so it has no unexported marker and the seal is open: any other subject "+
				"kind satisfies it, and a type declared outside this package can satisfy it too by "+
				"declaring the same exported methods.\n"+
				"INVARIANT: a kind is sealed by one unexported method, because an unexported method name "+
				"is qualified by the package declaring it and so cannot be declared from outside.\n"+
				"CONSEQUENCE: an exported method cannot carry the seal. Add an unexported marker; keep "+
				"the exported methods, which are the kind's coordinates.", name, declares)
		default:
			sort.Strings(markers)
			t.Errorf("%s declares %d unexported methods (%s); want exactly one, so that the seal is one "+
				"fact rather than a set to keep in step. Exported methods are not counted here and are "+
				"not limited — the kind's coordinates are exported.",
				name, len(markers), strings.Join(markers, ", "))
		}
	}

	// The other direction: a subject type left behind after the last fact keyed
	// by its kind was removed. It reads as a kind that exists and does not.
	var orphans []string
	for name := range declared {
		if name == "Subject" || !strings.HasSuffix(name, "Subject") {
			continue
		}
		if _, ok := expected[name]; !ok {
			orphans = append(orphans, name)
		}
	}
	if len(orphans) > 0 {
		sort.Strings(orphans)
		t.Errorf("%s declare(s) a subject type for no derived kind. A kind exists if and only if some "+
			"declared fact is keyed by it, so a type with no kind is either a kind removed without its "+
			"type or a type invented ahead of a fact — and a check could declare it and never be "+
			"invoked.", strings.Join(orphans, ", "))
	}

	// And the same direction for the traversals: one left behind after its kind
	// was removed enumerates something the facts no longer key anything by.
	var strandedTraversals []string
	for name, runners := range traversals {
		if _, ok := expected[name]; !ok {
			sort.Strings(runners)
			strandedTraversals = append(strandedTraversals, strings.Join(runners, ", ")+" over "+name)
		}
	}
	if len(strandedTraversals) > 0 {
		sort.Strings(strandedTraversals)
		t.Errorf("%s traverse(s) a subject type for no derived kind, so the framework enumerates "+
			"something no declared fact is keyed by.", strings.Join(strandedTraversals, "; "))
	}

	pull, traversed := 0, 0
	for _, info := range SubjectKinds {
		if info.Traversal != TraversalPull {
			continue
		}
		pull++
		if len(traversals[subjectTypeNameFor(info.Coordinates)]) == 1 {
			traversed++
		}
	}
	t.Logf("kind coverage: %d derived kinds, %d subject types matched, %d distinct markers; "+
		"%d enumerated kinds, %d with exactly one traversal",
		len(SubjectKinds), len(expected), len(markerOwners), pull, traversed)
}

// The coordinate-supply assertion, and what it deliberately does not claim.
//
// A kind exposes an exported accessor per coordinate it carries, and the value
// each returns arrives where the framework's traversal constructs the instance.
// Those are two declarations in two places — an interface and a constructor — so
// they can disagree, and the disagreement is silent in the direction that costs
// most: an accessor with no parameter to fill it returns the zero value on every
// invocation, and every fact is keyed, so the check reads facts for the empty
// instance and decides on evidence belonging to nothing.
//
// # The criterion
//
// For each derived kind, the constructor named new<KindTypeName> takes exactly
// one non-variadic parameter per exported accessor the kind's interface declares,
// plus one for the source file.
//
// The right-hand side is the accessor count and deliberately NOT the coordinate
// count the generated set carries, because a coordinate need not map to one
// scalar: the edge kind carries a single coordinate whose identity is a node, a
// socket and an alias taken together, and it exposes an accessor for each element
// of that triple. A criterion equating the two counts would fail that kind, whose
// shape the package argues for where the kind is declared.
//
// # What it cannot see
//
// It reads a parameter count, so a constructor accepting a coordinate and storing
// nothing satisfies it. That a coordinate reaches the check is proved
// behaviourally, per kind, by the traversal tests.
//
// It says nothing about a kind exposing no accessor at all: a coordinate the
// generated set declares and no accessor returns is outside its reach, because
// this is a pairing property and a kind exposing nothing pairs vacuously. That
// gap is not a residual any more — it is gated by
// [TestEveryDeclaredCoordinateHasAnAccessor], which derives the accessor set each
// kind owes from the generated coordinates. The two are deliberately separate
// assertions: this one asks whether every accessor can be filled, that one asks
// whether every coordinate has an accessor, and neither implies the other.
//
// It inspects only the constructor whose name the kind's type name derives. A kind
// may declare a second one taking provenance inputs — how the instance was
// reached, which the locator rule needs — and those are not coordinate supply.
func TestEveryCoordinateAccessorHasAConstructorParameterToFillIt(t *testing.T) {
	t.Parallel()

	if len(SubjectKinds) == 0 {
		t.Fatalf("the generated kind set is empty, so a clean result here certifies nothing")
	}

	declared := packageInterfaceDecls(t)
	funcs := packageFuncDeclsByName(t)

	// Calibration in both directions the instrument can be wrong: a lookup finding
	// nothing would report every kind as missing a constructor, one matching
	// anything would report every kind as supplied, and a counter reading a grouped
	// parameter declaration as one parameter would report the shipped kinds as
	// under-supplied.
	known := funcs["newNodeSocketSubject"]
	if known == nil {
		t.Fatalf("the declaration lookup does not see newNodeSocketSubject, which is declared, so it is "+
			"not a working instrument and its verdict on the other kinds means nothing (saw %d function(s))",
			len(funcs))
	}
	if funcs["newNoSuchDerivedKindSubject"] != nil {
		t.Fatalf("the declaration lookup claims to see newNoSuchDerivedKindSubject, which is declared " +
			"nowhere, so it would report every kind as supplied")
	}
	if got, want := nonVariadicParams(known), 3; got != want {
		t.Fatalf("the parameter count reads newNodeSocketSubject as taking %d non-variadic parameter(s); "+
			"want %d — that constructor declares them in one group, which is the form the count has to read",
			got, want)
	}

	paired, accessorTotal := 0, 0
	for _, info := range SubjectKinds {
		name := subjectTypeNameFor(info.Coordinates)
		iface, ctor := declared[name], funcs["new"+name]
		if iface == nil || ctor == nil {
			// Both absences belong to the completeness assertion above, which
			// reports each with its own consequence. Repeating them here would
			// double one failure into two.
			continue
		}

		_, _, accessors := interfaceShape(iface)
		paired++
		accessorTotal += len(accessors)

		if got, want := nonVariadicParams(ctor)-1, len(accessors); got != want {
			sort.Strings(accessors)
			t.Errorf("new%s takes %d coordinate parameter(s) against %d coordinate accessor(s) on %s (%s).\n"+
				"INVARIANT: a coordinate a kind exposes is supplied where the framework's traversal "+
				"constructs the instance, so an accessor and a constructor parameter exist in pairs.\n"+
				"CONSEQUENCE: an accessor with no parameter to fill it returns the zero value on every "+
				"invocation, and a check reads every fact keyed by that coordinate for the empty instance "+
				"— a rule evaluated against the wrong thing, or against nothing, while reading as a rule "+
				"that ran and found nothing. A parameter with no accessor is the mirror: a coordinate the "+
				"traversal supplies and no check can name.",
				name, got, want, name, strings.Join(accessors, ", "))
		}
	}

	// The census reports what was compared and claims no residual, because the one
	// this line used to name — kinds exposing no accessor at all — is now gated by
	// [TestEveryDeclaredCoordinateHasAnAccessor]. What this assertion still cannot
	// see is stated in its own documentation above: it reads a parameter count, so a
	// constructor taking a coordinate and storing nothing satisfies it.
	t.Logf("coordinate pairing: %d kind(s) compared, %d accessor(s) each paired with a constructor parameter",
		paired, accessorTotal)
}

// coordinateAccessors maps a coordinate name to the accessor name or names that
// return it. It is the one place that mapping lives.
//
// It is keyed by coordinate name and deliberately NOT by kind. A coordinate name
// is the vocabulary of the derived kind set itself — fewer names than there are
// kinds, each name meaning one thing wherever it appears — whereas a per-kind
// table would be a hand-written second copy of a set the fact declarations already
// derive, which is exactly the arrangement that reads as current while lagging.
// Neither figure is written here: both move whenever a fact is declared, and a
// count beside a derived set is the thing that goes stale.
//
// The mapping is a judgement rather than a transform, and the shipped names are
// what prove no transform exists: a node yields NodeID, a socket yields
// SocketName, and an emitted path yields EmittedPath, which does not name its
// coordinate at all. A coordinate written with a hyphen could not be a Go
// identifier however it was spelled mechanically, and the rows are the statement
// of which ones are.
//
// The suffix names what the value is, so a coordinate whose value is a path says
// so: a subsystem yields SubsystemPath, a boundary yields BoundaryPath and a
// module yields ModulePath, because each of those scopes nests and the identity
// of each is the path through the nesting rather than one authored key. No such
// axis is the exception, and the rows are the statement of which they are — a
// count of them here would be a second copy of the table below. A Name suffix on
// any of them would assert a single key and be false for every nested scope, and
// an accessor whose name misdescribes its value teaches the check that reads it
// to use it wrongly.
//
// Two entries are not one-to-one, and both are named rows here rather than
// special cases buried in the assertion:
//
//   - edge maps to the triple NodeID, SocketName and AliasName. An edge's identity
//     is the node, the socket and the alias taken together, which is what a fact
//     keyed by an edge takes as its key, and the edge itself names no scalar a
//     check could use. The alias is in the identity because one socket carries many
//     connections, each with its own target and configuration, so a key without it
//     would answer for two connections at once. So one coordinate legitimately
//     carries three accessors.
//   - document maps to SourceFile, which the subject contract already supplies. A
//     document is the whole file, so the coordinate and the provenance are the
//     same string, and the kind owes no accessor of its own for it.
//   - originated-connection maps to six: the edge's own three, the two that name
//     the origin, and the node the connection resolves to. A connection's identity
//     is the edge triple together with the surface that drew it, because two
//     attachments on one node may each write one socket and one alias and the
//     component itself may write the same pair — so the triple alone names up to
//     three separately authored connections. The origin is two accessors rather
//     than one for the reason [ConnectionOrigin] gives: the kinds are a closed
//     vocabulary and the attachment name is whatever the author typed, so one
//     field carrying both would need a reserved spelling nothing enforces. The
//     target is in the identity because two origins writing one socket and one
//     alias may name different components, and each delivers against the one it
//     named.
var coordinateAccessors = map[string][]string{
	"document":  {"SourceFile"},
	"parameter": {"ParameterName"},
	"assertion": {"AssertionName"},
	// The suffix says the value is the key the author wrote at the top of the
	// schema block, which is what identifies one entry among the block's. A Name
	// suffix would suggest the pillar it was meant to name, and the coordinate is
	// exactly the case where the key names no pillar at all.
	"schema-pillar": {"SchemaPillarKey"},
	// The suffix says the value is the key a binding is declared under, which is
	// what identifies one binding among the registry's entries. A Name suffix
	// would suggest the binding carries a name of its own beside its key.
	"annotation-binding": {"AnnotationBindingKey"},
	// Not one-to-one either, and the reason is the mapping-entry row's applied to
	// a surface with two authoring blocks. An output-label declaration's identity
	// is the block it was written in together with the key the author wrote: one
	// key legitimately appears in each block — which is the pair a rule about the
	// two meeting exists to refuse — so the key alone names up to two
	// declarations. Each accessor is the coordinate's own name plus what the value
	// is, and both are values the author typed, which is also why the locator is
	// assembled from the same two.
	"output-label": {"OutputLabelBlock", "OutputLabelKey"},
	// The third entry that is not one-to-one, and the reason is the one the
	// edge row gives applied to an authoring table. A mapping entry's identity
	// is the tier it is written under, the axis it keys on and the key the
	// author wrote, taken together: a tier holds two axes and an axis holds many
	// keys, so a key alone names as many entries as there are axes, and the
	// entry itself names no scalar a check could use. Each accessor is the
	// coordinate's own name plus what the value is, and all three are values the
	// author typed — which is also why the locator is assembled from the same
	// three.
	"mapping-entry": {"MappingTier", "MappingAxis", "MappingKey"},
	// The fourth entry that is not one-to-one, and the reason is the
	// originated-connection row's applied to an authoring surface rather than to a
	// graph one. An annotation entry's identity is the document it was written
	// in, the scope inside that document, the component key it was written under,
	// the annotation key and the subkey within the value, all taken together: one
	// annotation key is legitimately written at a scope and again on a component
	// inside it, in both documents, with a subkey each, so no shorter tuple names
	// an entry. The document is carried apart from the scope because the two
	// documents compose their scope paths with separators of their own, and one
	// joined value would rest on those separators staying different with nothing
	// comparing them. The subkey's accessor carries no suffix for the reason the
	// delivery-key and locals-key rows carry none: the coordinate's own word
	// already says what the value is.
	"authored-annotation": {
		"AuthoringDocumentKind", "AnnotationScopePath", "AnnotationComponentName",
		"AnnotationKey", "AnnotationSubkey",
	},
	// The fifth entry that is not one-to-one, and the reason is the identity the
	// coordinate has to carry in order to be an identity at all. A placement's
	// identity is the document it was written in, the boundary it sits in, what
	// the entry addresses and the block it was written in, all taken together:
	// one boundary can name a component directly under the id it carries once the
	// composition is flattened AND reach the same component through a routing
	// entry, and a routed sub-module's dotted prefix can equal a component's id,
	// so any two of the three leave pairs nothing could tell apart — which is
	// exactly the collision the rule over this kind exists to report. The
	// document is carried even though one authoring document places anything
	// today, because the locator assembly selects the scope nesting from it
	// through the same closed pair every other authored-site traversal selects
	// through. The reference's accessor says reference rather than component for
	// the boundary-component-reference row's reason, and carries the placement's
	// own word because the value is a sub-module prefix on one of the three axes.
	"boundary-placement": {
		"AuthoringDocumentKind", "BoundaryPath", "PlacementReference", "PlacementAxis",
	},
	// The sixth entry that is not one-to-one, and the reason is the edge row's
	// applied to an authored entry rather than to a drawn connection. An entry's
	// identity is the document it was written in, the boundary it sits in, and
	// the consuming component, socket and alias it was keyed under, all taken
	// together: the last three are the connection's own identity, and the
	// boundary joins them because the Release writes this surface inside a
	// boundary's component entry, which is the block an author edits. The
	// document is carried even though one authoring document writes this surface,
	// because the locator assembly selects the scope nesting from it through the
	// same closed pair every other authored-site traversal selects through. The
	// last three reuse the edge row's accessor names rather than spelling
	// Release-specific ones, because one accessor name means one thing wherever
	// it appears and which set a value came from is said by the kind carrying it.
	"release-edge-configuration": {
		"AuthoringDocumentKind", "BoundaryPath", "NodeID", "SocketName", "AliasName",
	},
	"node":   {"NodeID"},
	"socket": {"SocketName"},
	"edge":   {"NodeID", "SocketName", "AliasName"},
	"originated-connection": {
		"NodeID", "OriginKind", "OriginAttachment", "SocketName", "AliasName", "TargetNodeID",
	},
	"attachment":    {"AttachmentName"},
	"slot":          {"SlotName"},
	"mechanism":     {"MechanismName"},
	"delivery-key":  {"DeliveryKey"},
	"pillar":        {"PillarName"},
	"leaf":          {"LeafName"},
	"locals-key":    {"LocalsKey"},
	"provides-key":  {"ProvidesKey"},
	"pipeline-step": {"StepID"},
	// The accessor says the value is a template FIELD rather than a key, because
	// the coordinate is not one authored key: it labels a whole template-bearing
	// field, which for a keyed block carries the entry's own key inside the label.
	"template-field": {"TemplateField"},
	// A separate row from the one above rather than a second use of it, because
	// the two axes are not the same axis reached differently. This one labels the
	// path fields a Release scope writes, whose admitted reference set differs
	// per field, where that one labels the template fields of a document whose
	// fields share one set. The accessor spells the distinction so a check cannot
	// be written against the wrong axis and still compile.
	"path-template-field": {"PathTemplateField"},
	"boundary":            {"BoundaryPath"},
	// The accessor says the value is a reference rather than a component id,
	// because the axis is what a Release ASKED FOR: an entry addressing an id
	// nothing answers to is enumerated exactly as one that resolves is, and an
	// accessor named for the component would tell a check reading it that the
	// component is there.
	"boundary-component-reference": {"ComponentReference"},
	"subsystem":                    {"SubsystemPath"},
	"module":                       {"ModulePath"},
	"provider":                     {"ProviderName"},
	"assembly-path":                {"AssemblyPath"},
	"path":                         {"EmittedPath"},
	"artifact":                     {"ArtifactName"},
	// The suffix says the value is the key the author wrote in the artifacts
	// block, which is what identifies one entry among that block's before
	// anything resolves what the entry stands for. A Name suffix would be the row
	// above's accessor and would assert the output the entry resolves to — which
	// for an entry importing a library bundle is one name per file that bundle
	// holds, and none at all where the import failed. The two rows are the same
	// authored block read at two moments, so the accessor names are what keep a
	// check from being written against the wrong one and still compiling.
	"artifacts-entry": {"ArtifactsEntryKey"},
	// The value is the Recipe's own spec-root key the author wrote an `$import:`
	// under, so the accessor names the field rather than anything that field's
	// content resolves to. A template-field accessor would be the wrong reading
	// twice over: it names a field carrying text this document holds, and an
	// imported field holds none.
	"recipe-import-field": {"RecipeImportField"},
	// The value is the Recipe's own spec-root key a source entry is written
	// under, and the accessor says so rather than naming the source itself: the
	// coordinate is a POSITION in the document, present whether the author wrote
	// anything at it or not, and an accessor named for the source would tell a
	// check reading it that a source is there.
	"recipe-source-field": {"RecipeSourceField"},
	// The value is the Recipe's own spec-root key its boundary block is written
	// under — a position, present whether the author wrote the block or not — so
	// the accessor names the position rather than the files the block declares.
	"recipe-boundary-block": {"RecipeBoundaryBlock"},
}

// subjectContractAccessors names the accessors [Subject] itself supplies, so a
// coordinate mapping onto one of them adds no method to the kind's own interface.
//
// It exists because the completeness comparison is against the methods a kind
// declares itself: an embedded contract's methods are not among them, so an
// expectation naming one would be unsatisfiable by construction.
var subjectContractAccessors = map[string]bool{"SourceFile": true}

// TestEveryDeclaredCoordinateHasAnAccessor is the completeness assertion, and it
// is the half [TestEveryCoordinateAccessorHasAConstructorParameterToFillIt]
// cannot reach.
//
// # The property
//
// For each derived kind, the set of accessors its own interface declares equals
// the set derived by mapping each of its declared coordinates through
// [coordinateAccessors], minus whatever the subject contract already supplies.
//
// # Why it keys on the coordinate set and never on a count
//
// The declared coordinate count is provably not the accessor count: the edge kind
// declares one coordinate and carries two accessors, and the document kind
// declares one and carries none of its own. A criterion comparing two numbers
// reds both of those, and a criterion a correct kind reds is not a criterion. So
// the comparison is between sets of names, derived through one mapping, and no
// count enters it.
//
// # An unmapped coordinate is a refusal
//
// A coordinate name with no row in the mapping fails this assertion saying so,
// rather than being passed over. That is what keeps the mapping complete as the
// fact declarations grow a coordinate, without a second instrument watching it —
// and a table that silently ignores what it does not know is the blind instrument
// this framework exists to remove.
//
// # What it cannot see, and no assertion at this layer can
//
// It compares declared interface methods against a derived expectation. It never
// calls an accessor, so it cannot check that one returns the RIGHT value — that
// SocketName returns the socket rather than the node, or returns anything at all
// rather than the zero value. A kind can satisfy this assertion completely while
// every accessor on it returns the empty string. That property is proved
// separately and behaviourally, per kind, by
// [TestCoordinateAccessorsCarryTheEnumeratedCoordinateValues].
func TestEveryDeclaredCoordinateHasAnAccessor(t *testing.T) {
	t.Parallel()

	if len(SubjectKinds) == 0 {
		t.Fatalf("the generated kind set is empty, so a clean result here certifies nothing")
	}
	if len(coordinateAccessors) == 0 {
		t.Fatalf("the coordinate mapping is empty, so every kind would be reported as unmapped")
	}

	declared := packageInterfaceDecls(t)

	// Calibration, on the same principle as every other assertion over this corpus:
	// an instrument that saw nothing would report every kind as missing every
	// accessor, and one that matched anything would report every kind as complete.
	if declared["NodeSocketSubject"] == nil {
		t.Fatalf("the package parse does not see NodeSocketSubject, which is declared, so it is not a "+
			"working instrument and its verdict on the other kinds means nothing (saw %d interfaces)",
			len(declared))
	}
	if _, _, exported := interfaceShape(declared["NodeSocketSubject"]); len(exported) != 2 {
		t.Fatalf("the accessor reader sees %d exported method(s) on NodeSocketSubject; want 2 — it is the "+
			"shipped two-coordinate kind, so a reader disagreeing here would misjudge every kind",
			len(exported))
	}

	unmappedSeen := map[string]bool{}
	usedCoordinates := map[string]bool{}
	compared := 0

	for _, info := range SubjectKinds {
		name := subjectTypeNameFor(info.Coordinates)
		iface := declared[name]
		if iface == nil {
			// The missing type belongs to the completeness assertion over the kind
			// set, which reports it with its own consequence.
			continue
		}

		// Built in the generated coordinate order so a failure reads in the order the
		// kind nests, and so two runs report identically.
		owed := make([]string, 0, len(info.Coordinates))
		owedBy := map[string]string{}
		unmapped := false
		for _, coordinate := range info.Coordinates {
			accessors, mapped := coordinateAccessors[coordinate]
			if !mapped {
				unmapped = true
				if !unmappedSeen[coordinate] {
					unmappedSeen[coordinate] = true
					t.Errorf("the coordinate %q, declared by the kind %s, has no entry in the coordinate "+
						"mapping, so this assertion cannot say what accessor it owes.\n"+
						"INVARIANT: every coordinate name a derived kind carries maps to the accessor name "+
						"returning it, through one mapping keyed by coordinate name.\n"+
						"CONSEQUENCE: an unmapped coordinate is refused rather than skipped. Were it skipped, "+
						"a coordinate could be declared, carry no accessor, and pass — the check would be "+
						"invoked once per instance of it and be unable to name which instance, while this "+
						"assertion reported the kind complete.\n"+
						"Add a row for %q naming the accessor that returns it, or the accessor the subject "+
						"contract already supplies for it.", coordinate, info.Kind, coordinate)
				}
				continue
			}
			usedCoordinates[coordinate] = true
			for _, accessor := range accessors {
				if subjectContractAccessors[accessor] {
					continue
				}
				if _, already := owedBy[accessor]; already {
					continue
				}
				owedBy[accessor] = coordinate
				owed = append(owed, accessor)
			}
		}
		if unmapped {
			// Comparing against a partial expectation would report accessors as
			// unexpected merely because their coordinate had no row.
			continue
		}

		_, _, got := interfaceShape(iface)
		compared++

		declaredSet := map[string]bool{}
		for _, accessor := range got {
			declaredSet[accessor] = true
		}

		for _, accessor := range owed {
			if declaredSet[accessor] {
				continue
			}
			t.Errorf("%s declares no %s(), which the coordinate %q it carries owes.\n"+
				"INVARIANT: a kind exposes one accessor per coordinate it carries, so a check invoked once "+
				"per instance can name the instance it was invoked for.\n"+
				"CONSEQUENCE: every fact is keyed. A coordinate with no accessor is a coordinate the check "+
				"cannot read, so it cannot key a fact to its own instance and cannot name it in what it "+
				"raises — the rule runs the right number of times and decides on evidence belonging to no "+
				"instance in particular.\n"+
				"Declare %s() string on %s and add the parameter filling it to new%s, which the pairing "+
				"assertion requires in the same edit.", name, accessor, owedBy[accessor], accessor, name, name)
		}

		owedSet := map[string]bool{}
		for _, accessor := range owed {
			owedSet[accessor] = true
		}
		var unexpected []string
		for _, accessor := range got {
			if !owedSet[accessor] {
				unexpected = append(unexpected, accessor)
			}
		}
		if len(unexpected) > 0 {
			sort.Strings(unexpected)
			t.Errorf("%s declares %s, which no coordinate it carries (%s) owes.\n"+
				"INVARIANT: an accessor exists because the kind carries that coordinate, and for no other "+
				"reason — never because a predicate would find the value convenient.\n"+
				"CONSEQUENCE: a value that is a projection of a coordinate rather than a coordinate itself "+
				"is read as the declared fact keyed by that coordinate. Reading it from the subject instead "+
				"hides a real dependency from the point where a check's placement is worked out, because a "+
				"fact carries the point it completes at and a subject carries none.",
				name, strings.Join(unexpected, ", "), strings.Join(info.Coordinates, ", "))
		}
	}

	// The mirror of the refusal above: a row for a coordinate no kind declares. It
	// is dead vocabulary, and left alone it accumulates until the mapping stops
	// describing the derived set.
	var orphanRows []string
	for coordinate := range coordinateAccessors {
		if !usedCoordinates[coordinate] {
			orphanRows = append(orphanRows, coordinate)
		}
	}
	if len(orphanRows) > 0 {
		sort.Strings(orphanRows)
		t.Errorf("the coordinate mapping has row(s) for %s, which no derived kind declares. A coordinate "+
			"exists because a declared fact is keyed by it, so a row for one that no kind carries names "+
			"vocabulary the facts have dropped.", strings.Join(orphanRows, ", "))
	}

	if compared == 0 {
		t.Fatalf("no kind was compared, so a clean result certifies nothing")
	}
	t.Logf("coordinate completeness: %d kind(s) compared against the mapping, %d coordinate name(s) used "+
		"of %d rows; this assertion reads declarations only and never calls an accessor",
		compared, len(usedCoordinates), len(coordinateAccessors))
}

// The instance-binding assertion, which is a floor and is named as one.
//
// A traversal enumerates a kind's coordinates and hands the check one subject per
// instance. A range statement binding neither its key nor its value enumerates
// instances while discarding which one it is on, so every subject built inside it
// is built from the outer coordinates alone and the innermost coordinate reaches
// nothing at all.
//
// # The criterion
//
// Inside a pull kind's traversal — including the function literals it declares,
// which is where the node-rooted traversals build their subjects — no range
// statement binds neither a key nor a value.
//
// It is a syntactic property and deliberately not a match on names. Comparing a
// bound identifier against its coordinate's name cannot work over this set: four
// declared coordinates are written with a hyphen and can never be a Go identifier
// at all, the node coordinate reaches a constructor as a field selector rather
// than a bare name, and a short local name is legitimate. Such a criterion would
// fail traversals that are correct.
//
// # What it cannot see
//
// It reads binding, not use. A value bound to an identifier and then never
// passed, or passed to the wrong parameter, satisfies it.
//
// It says nothing about coordinate state. A bound value spent only on the locator
// leaves a check just as unable to name its instance, and that is the state most
// enumerated kinds are in — the census in
// [TestEveryCoordinateAccessorHasAConstructorParameterToFillIt] reports how many.
//
// It inspects a traversal's own body and the function literals within it, never
// the shared helpers it calls, so a discard inside one of those is out of reach.
//
// An indexed range binds a key and passes, whether or not the element is read.
func TestNoPullTraversalDiscardsAnInstanceItEnumerates(t *testing.T) {
	t.Parallel()

	fset, decls := packageTraversalDeclsBySubjectType(t)

	// Calibration on the exact surface class, in both directions: the detector has
	// to fire on a discarded range value and stay silent on a bound one, or a clean
	// verdict would say only that it never matches anything.
	const calibration = `package p

func discards(xs []string) {
	for range xs {
		_ = 1
	}
}

func binds(xs []string) {
	for _, x := range xs {
		_ = x
	}
}
`
	probe, err := parser.ParseFile(token.NewFileSet(), "calibration.go", calibration, 0)
	if err != nil {
		t.Fatalf("parse the calibration source: %v", err)
	}
	calibrated := 0
	for _, decl := range probe.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		calibrated++
		fired, want := len(discardedRanges(fn)) > 0, fn.Name.Name == "discards"
		if fired != want {
			t.Fatalf("the detector reports fired=%v on the calibration function %s; want %v — it is not a "+
				"working instrument and its verdict on the traversals means nothing", fired, fn.Name.Name, want)
		}
	}
	if calibrated != 2 {
		t.Fatalf("the calibration source yielded %d function(s); want 2, one of each direction", calibrated)
	}

	inspected := 0
	var findings []string
	for _, info := range SubjectKinds {
		if info.Traversal != TraversalPull {
			continue
		}
		for _, fn := range decls[subjectTypeNameFor(info.Coordinates)] {
			inspected++
			for _, pos := range discardedRanges(fn) {
				findings = append(findings, fmt.Sprintf("%s, the traversal of %s, at %s",
					fn.Name.Name, info.Kind, fset.Position(pos)))
			}
		}
	}
	if inspected == 0 {
		t.Fatalf("no traversal was inspected, so a clean result certifies nothing")
	}
	if len(findings) > 0 {
		sort.Strings(findings)
		t.Errorf("%d range statement(s) bind neither a key nor a value: %s.\n"+
			"INVARIANT: a traversal hands the check a subject naming the instance it was invoked for, so "+
			"every coordinate it enumerates is bound and threaded into that subject.\n"+
			"CONSEQUENCE: a discarded range value is a coordinate the kind declares and the traversal "+
			"cannot supply. The check is invoked the right number of times and cannot tell which instance "+
			"it is looking at, so it can neither key a fact to it nor name it in what it raises — a rule "+
			"that runs and reports nothing, which reads exactly like a rule that ran and found nothing.\n"+
			"Bind the value and pass it to the kind's constructor as a coordinate. It is not a locator "+
			"segment unless the author wrote it.",
			len(findings), strings.Join(findings, "; "))
	}
	t.Logf("instance binding: %d pull traversal(s) inspected, %d discarded range(s)", inspected, len(findings))
}

// discardedRanges reports the position of every range statement within fn,
// including the function literals it declares, that binds neither a key nor a
// value.
func discardedRanges(fn *ast.FuncDecl) []token.Pos {
	var out []token.Pos
	ast.Inspect(fn, func(node ast.Node) bool {
		if rng, ok := node.(*ast.RangeStmt); ok && rng.Key == nil && rng.Value == nil {
			out = append(out, rng.For)
		}
		return true
	})
	return out
}

// packageTraversalDeclsBySubjectType returns, per subject type name, the
// declarations of the exported functions in this package that take a check
// declared over it, together with the file set their positions resolve against.
//
// Taking a Check is the criterion rather than a naming convention, because it is
// the property that matters: a function reaching a check's predicate through the
// declaration is a traversal, and one named like a traversal that takes a bare
// predicate is not. It reads the non-test sources, so a traversal declared only in
// a test cannot satisfy the assertion.
//
// This is the one place that criterion lives, and
// [packageTraversalsBySubjectType] is its name-only projection: two assertions
// over the traversals cannot disagree about what one is.
func packageTraversalDeclsBySubjectType(t *testing.T) (*token.FileSet, map[string][]*ast.FuncDecl) {
	t.Helper()

	fset, files := packageNonTestFiles(t)

	out := map[string][]*ast.FuncDecl{}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || !fn.Name.IsExported() || fn.Type.Params == nil {
				continue
			}
			for _, param := range fn.Type.Params.List {
				subject, ok := checkSubjectTypeArgument(param.Type)
				if !ok {
					continue
				}
				out[subject] = append(out[subject], fn)
			}
		}
	}
	return fset, out
}

// packageTraversalsBySubjectType names the traversals
// [packageTraversalDeclsBySubjectType] discovered.
func packageTraversalsBySubjectType(t *testing.T) map[string][]string {
	t.Helper()

	_, decls := packageTraversalDeclsBySubjectType(t)

	out := map[string][]string{}
	for subject, fns := range decls {
		for _, fn := range fns {
			out[subject] = append(out[subject], fn.Name.Name)
		}
	}
	return out
}

// packageNonTestFiles parses this package's non-test sources and returns them
// with the file set their positions resolve against.
//
// Every AST assertion in this file reads that one corpus — non-test sources, so
// that a declaration existing only in a test satisfies none of them — and it is
// parsed in one place so two assertions both claiming to cover the package cannot
// be covering different sets of files.
func packageNonTestFiles(t *testing.T) (*token.FileSet, []*ast.File) {
	t.Helper()

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse this package's sources: %v", err)
	}

	var files []*ast.File
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			files = append(files, file)
		}
	}
	if len(files) == 0 {
		t.Fatalf("no non-test source was parsed, so every assertion over this corpus certifies nothing")
	}
	return fset, files
}

// packageFuncDeclsByName returns every top-level function this package declares
// in its non-test sources, keyed by name.
func packageFuncDeclsByName(t *testing.T) map[string]*ast.FuncDecl {
	t.Helper()

	_, files := packageNonTestFiles(t)

	out := map[string]*ast.FuncDecl{}
	for _, file := range files {
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil {
				out[fn.Name.Name] = fn
			}
		}
	}
	return out
}

// nonVariadicParams counts the parameters a function takes before its variadic
// one, counting each name in a grouped declaration separately: "sourceFile,
// boundary string" is two parameters and not one.
func nonVariadicParams(fn *ast.FuncDecl) int {
	if fn.Type.Params == nil {
		return 0
	}
	count := 0
	for _, field := range fn.Type.Params.List {
		if _, variadic := field.Type.(*ast.Ellipsis); variadic {
			continue
		}
		if len(field.Names) == 0 {
			count++
			continue
		}
		count += len(field.Names)
	}
	return count
}

// checkSubjectTypeArgument reports the subject type a Check instantiation names,
// which is its last type argument.
//
// The arity is asserted rather than the position being counted from the end of
// whatever it finds, because a wrong arity here is silent: this walk reports
// "not a Check" for a shape it does not recognise, so an instantiation that
// gained or lost a parameter would simply stop being discovered and the guards
// reading this would go on passing over an empty set. The callers each refuse a
// zero for that reason, and this assertion is what makes the refusal legible.
func checkSubjectTypeArgument(expr ast.Expr) (string, bool) {
	instantiation, ok := expr.(*ast.IndexListExpr)
	if !ok || len(instantiation.Indices) != 2 {
		return "", false
	}
	if base, ok := instantiation.X.(*ast.Ident); !ok || base.Name != "Check" {
		return "", false
	}
	subject, ok := instantiation.Indices[1].(*ast.Ident)
	if !ok {
		return "", false
	}
	return subject.Name, true
}

// subjectKindBindings binds each derived kind to its interface type and a call of
// its constructor. It is checked against the generated set rather than trusted:
// [TestSubjectKindTypesAreMutuallyDistinct] fails on a kind this table omits, so
// the table cannot silently lag the facts.
//
// The segments each entry supplies are representative of what a traversal would
// contribute for that kind, which is also what keeps every constructor exercised.
var subjectKindBindings = map[SubjectKind]struct {
	iface reflect.Type
	make  func(sourceFile string) Subject
}{
	SubjectDocument: {
		iface: reflect.TypeOf((*DocumentSubject)(nil)).Elem(),
		make:  func(f string) Subject { return newDocumentSubject(f) },
	},
	SubjectDocumentParameter: {
		iface: reflect.TypeOf((*DocumentParameterSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newDocumentParameterSubject(f, "db_size", "spec", "parameters", "db_size")
		},
	},
	SubjectDocumentAssertion: {
		iface: reflect.TypeOf((*DocumentAssertionSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newDocumentAssertionSubject(f, "port-set", "spec", "assertions", "port-set")
		},
	},
	SubjectDocumentSchemaPillar: {
		iface: reflect.TypeOf((*DocumentSchemaPillarSubject)(nil)).Elem(),
		// A key the block's own contract admits, because the coordinate is the key
		// the author wrote and nothing else. A binding built from a key naming no
		// pillar would read as though the coordinate carried a verdict on the key,
		// which is the one thing this kind is defined not to do.
		make: func(f string) Subject {
			return newDocumentSchemaPillarSubject(f, "values", "spec", "schema", "values")
		},
	},
	SubjectDocumentAnnotationBinding: {
		iface: reflect.TypeOf((*DocumentAnnotationBindingSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newDocumentAnnotationBindingSubject(f, "feature.example/rewrite-host",
				"spec", "mappings", "features", "annotations", "feature.example/rewrite-host")
		},
	},
	SubjectDocumentOutputLabel: {
		iface: reflect.TypeOf((*DocumentOutputLabelSubject)(nil)).Elem(),
		// A key in neither reserved namespace, because the coordinate is the key the
		// author wrote and carries no verdict on it — a binding built from a reserved
		// key would read as though it did. It is also separator-free, which is what
		// exercises the locator assembly to full depth: a reserved key necessarily
		// carries the dialect's separator, so its own segment would be dropped and
		// the constructor's deepest argument would go unexercised.
		make: func(f string) Subject {
			return newDocumentOutputLabelSubject(f, OutputLabelBlockPromoted, "hosting",
				"spec", OutputLabelBlockPromoted, "hosting")
		},
	},
	SubjectDocumentMappingEntry: {
		iface: reflect.TypeOf((*DocumentMappingEntrySubject)(nil)).Elem(),
		// The features tier on the name axis, because it is the arm whose two
		// authored keys differ from the component tier's: a binding that reached
		// for the wrong tier or the wrong axis would still produce a locator of
		// the right depth.
		make: func(f string) Subject {
			return newDocumentMappingEntrySubject(f,
				MappingTierFeatures, MappingAxisName, "logs",
				"spec", "mappings", "features", "name", "logs")
		},
	},
	SubjectNode: {
		iface: reflect.TypeOf((*NodeSubject)(nil)).Elem(),
		make:  func(f string) Subject { return newNodeSubject(f, "web", "spec", "components", "web") },
	},
	SubjectNodeSocket: {
		iface: reflect.TypeOf((*NodeSocketSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeSocketSubject(f, "web", "databases", "spec", "components", "web", "uses", "databases")
		},
	},
	SubjectEdge: {
		iface: reflect.TypeOf((*EdgeSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newEdgeSubject(f, "web", "databases", "primary",
				"spec", "components", "web", "uses", "databases", "primary")
		},
	},
	SubjectOriginatedConnection: {
		iface: reflect.TypeOf((*OriginatedConnectionSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newOriginatedConnectionSubject(f, "web", OriginAttachment, "tls", "databases", "primary", "db",
				"spec", "components", "web", "features", "tls", "uses", "databases", "primary")
		},
	},
	SubjectNodeAttachment: {
		iface: reflect.TypeOf((*NodeAttachmentSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentSubject(f, "web", "tls", "spec", "components", "web", "features", "tls")
		},
	},
	SubjectNodeAttachmentSocket: {
		iface: reflect.TypeOf((*NodeAttachmentSocketSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentSocketSubject(f, "web", "tls", "issuer",
				"spec", "components", "web", "features", "tls", "uses", "issuer")
		},
	},
	SubjectNodeSlot: {
		iface: reflect.TypeOf((*NodeSlotSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeSlotSubject(f, "web", "sidecar", "spec", "components", "web", "features", "sidecar")
		},
	},
	SubjectNodeMechanism: {
		iface: reflect.TypeOf((*NodeMechanismSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeMechanismSubject(f, "web", "env", "spec", "components", "web", "inject", "env")
		},
	},
	SubjectNodeMechanismDeliveryKey: {
		iface: reflect.TypeOf((*NodeMechanismDeliveryKeySubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeMechanismDeliveryKeySubject(f, "web", "env", "DB_HOST",
				"spec", "components", "web", "inject", "env", "DB_HOST")
		},
	},
	SubjectNodePillarLeaf: {
		iface: reflect.TypeOf((*NodePillarLeafSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodePillarLeafSubject(f, "web", "app", "replicas",
				"spec", "components", "web", "values", "app", "replicas")
		},
	},
	SubjectNodeParameter: {
		iface: reflect.TypeOf((*NodeParameterSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeParameterSubject(f, "web", "db_size", "spec", "components", "web", "parameters", "db_size")
		},
	},
	SubjectNodeAttachmentPillarLeaf: {
		iface: reflect.TypeOf((*NodeAttachmentPillarLeafSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentPillarLeafSubject(f, "web", "tls", "app", "issuer",
				"spec", "components", "web", "features", "tls", "values", "app", "issuer")
		},
	},
	SubjectNodeLocalsKey: {
		iface: reflect.TypeOf((*NodeLocalsKeySubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeLocalsKeySubject(f, "web", "fullname", "spec", "locals", "fullname")
		},
	},
	SubjectNodeAttachmentLocalsKey: {
		iface: reflect.TypeOf((*NodeAttachmentLocalsKeySubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentLocalsKeySubject(f, "web", "tls", "fullname",
				"spec", "locals", "fullname")
		},
	},
	SubjectBoundaryAttachmentLocalsKey: {
		iface: reflect.TypeOf((*BoundaryAttachmentLocalsKeySubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newBoundaryAttachmentLocalsKeySubject(f, "prod", "shared-db", "endpoint",
				"spec", "locals", "endpoint")
		},
	},
	SubjectNodeTemplateField: {
		iface: reflect.TypeOf((*NodeTemplateFieldSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeTemplateFieldSubject(f, "web", "artifacts[cm.yaml]",
				"spec", "artifacts", "cm.yaml")
		},
	},
	SubjectNodeAttachmentTemplateField: {
		iface: reflect.TypeOf((*NodeAttachmentTemplateFieldSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentTemplateFieldSubject(f, "web", "tls", "pipeline[stamp].when",
				"spec", "pipeline", "stamp", "when")
		},
	},
	SubjectBoundaryAttachmentTemplateField: {
		iface: reflect.TypeOf((*BoundaryAttachmentTemplateFieldSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newBoundaryAttachmentTemplateFieldSubject(f, "prod", "shared-db", "provides[dsn]",
				"spec", "provides", "dsn")
		},
	},
	SubjectBoundaryPathTemplateField: {
		iface: reflect.TypeOf((*BoundaryPathTemplateFieldSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newBoundaryPathTemplateFieldSubject(f, "prod/edge", "directory",
				"spec", "boundaries", "prod", "boundaries", "edge", "directory")
		},
	},
	SubjectNodeProvidesKey: {
		iface: reflect.TypeOf((*NodeProvidesKeySubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeProvidesKeySubject(f, "web", "host", "spec", "provides", "host")
		},
	},
	SubjectNodePipelineStep: {
		iface: reflect.TypeOf((*NodePipelineStepSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodePipelineStepSubject(f, "web", "render", "spec", "pipeline", "render")
		},
	},
	SubjectNodeAttachmentPipelineStep: {
		iface: reflect.TypeOf((*NodeAttachmentPipelineStepSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentPipelineStepSubject(f, "web", "tls", "stamp",
				"spec", "pipeline", "stamp")
		},
	},
	SubjectBoundaryAttachmentPipelineStep: {
		iface: reflect.TypeOf((*BoundaryAttachmentPipelineStepSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newBoundaryAttachmentPipelineStepSubject(f, "prod", "shared-db", "annotate",
				"spec", "pipeline", "annotate")
		},
	},
	SubjectAssemblyPathProvider: {
		iface: reflect.TypeOf((*AssemblyPathProviderSubject)(nil)).Elem(),
		// No segments, and that is the kind's own shape rather than an omission
		// here: an assembly path is evaluated output and a provider is selected,
		// so a traversal has nothing authored to contribute.
		make: func(f string) Subject {
			return newAssemblyPathProviderSubject(f, "dist/shared/merged", "helm")
		},
	},
	SubjectNodePath: {
		iface: reflect.TypeOf((*NodePathSubject)(nil)).Elem(),
		// The component's own site is the whole of the locator. An emitted name is
		// produced by whichever of the node's blocks emitted it and is a key no
		// document holds, so it contributes no segment — which is why the segments
		// here stop at the component rather than naming the file.
		make: func(f string) Subject {
			return newNodePathSubject(f, "web", "deployment.yaml", "spec", "components", "web")
		},
	},
	SubjectNodeArtifact: {
		iface: reflect.TypeOf((*NodeArtifactSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeArtifactSubject(f, "web", "values-yaml", "spec", "artifacts", "values-yaml")
		},
	},
	SubjectNodeArtifactsEntry: {
		iface: reflect.TypeOf((*NodeArtifactsEntrySubject)(nil)).Elem(),
		// A separator-free key, though the ordinary authored key on this axis is an
		// output filename and carries the dialect's separator. That shape is what
		// the traversal transcript exercises; here it would stop the locator
		// assembly at the block and leave the constructor's deepest argument
		// unexercised, which is the one thing a binding is here to reach.
		make: func(f string) Subject {
			return newNodeArtifactsEntrySubject(f, "web", "values-yaml", "spec", "artifacts", "values-yaml")
		},
	},
	SubjectNodeRecipeImportField: {
		iface: reflect.TypeOf((*NodeRecipeImportFieldSubject)(nil)).Elem(),
		// The authoring surface fixes this axis's coordinate to a Recipe spec-root
		// key, so the value carries no dialect separator and the locator assembles
		// in full — which is what reaches the constructor's deepest argument.
		make: func(f string) Subject {
			return newNodeRecipeImportFieldSubject(f, "web", "valuesMapping", "spec", "valuesMapping")
		},
	},
	SubjectNodeRecipeSourceField: {
		iface: reflect.TypeOf((*NodeRecipeSourceFieldSubject)(nil)).Elem(),
		// The coordinate is a position the authoring surface fixes, so it carries
		// no dialect separator and the locator assembles in full — which is what
		// reaches the constructor's deepest argument.
		make: func(f string) Subject {
			return newNodeRecipeSourceFieldSubject(f, "web", "source", "spec", "source")
		},
	},
	SubjectNodeRecipeBoundaryBlock: {
		iface: reflect.TypeOf((*NodeRecipeBoundaryBlockSubject)(nil)).Elem(),
		// A position the authoring surface fixes, as the source field's is, so the
		// locator assembles in full and reaches the constructor's deepest argument.
		make: func(f string) Subject {
			return newNodeRecipeBoundaryBlockSubject(f, "web", "boundary", "spec", "boundary")
		},
	},
	SubjectNodeAttachmentArtifact: {
		iface: reflect.TypeOf((*NodeAttachmentArtifactSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentArtifactSubject(f, "web", "tls", "values.yaml",
				"spec", "artifacts", "values.yaml")
		},
	},
	SubjectBoundaryAttachmentArtifact: {
		iface: reflect.TypeOf((*BoundaryAttachmentArtifactSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newBoundaryAttachmentArtifactSubject(f, "prod", "shared-db", "dashboard.json",
				"spec", "artifacts", "dashboard.json")
		},
	},
	SubjectNodeAttachmentPath: {
		iface: reflect.TypeOf((*NodeAttachmentPathSubject)(nil)).Elem(),
		// No segments at all. An emitted name is produced by whichever of the
		// render's blocks emitted it and is a key no document holds, so it
		// contributes none; and the locator's file is the FeatureRecipe rather than
		// a path inside it, so neither the node nor the attachment contributes one
		// either. Both remain coordinates, which is what tells this render's
		// emission from the host node's own.
		make: func(f string) Subject {
			return newNodeAttachmentPathSubject(f, "web", "tls", "deployment.yaml")
		},
	},
	SubjectBoundaryAttachmentPath: {
		iface: reflect.TypeOf((*BoundaryAttachmentPathSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newBoundaryAttachmentPathSubject(f, "prod", "shared-db", "statefulset.yaml")
		},
	},
	SubjectBoundaryAttachment: {
		iface: reflect.TypeOf((*BoundaryAttachmentSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newBoundaryAttachmentSubject(f, "prod", "shared-db",
				"spec", "boundaries", "prod", "features", "shared-db")
		},
	},
	SubjectSubsystemAttachment: {
		iface: reflect.TypeOf((*SubsystemAttachmentSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newSubsystemAttachmentSubject(f, "billing.payments", "tls",
				"spec", "subsystems", "billing", "subsystems", "payments", "features", "tls")
		},
	},
	SubjectAuthoredAnnotation: {
		iface: reflect.TypeOf((*AuthoredAnnotationSubject)(nil)).Elem(),
		// The component-keyed Release case, because it is the one carrying every
		// element of the identity at once: a nested scope, a component inside it, and
		// a subkey the locator deliberately does not name. The trailing segment is
		// the annotation key, which the dialect drops because it carries a period —
		// so the locator names the block that holds it.
		make: func(f string) Subject {
			return newAuthoredAnnotationSubject(f, AuthoringDocumentRelease, "prod/eu", "api",
				"feature.myco/db-pool", "pool_size",
				"spec", "boundaries", "prod", "boundaries", "eu",
				"components", "api", "annotations", "feature.myco/db-pool")
		},
	},
	SubjectModule: {
		iface: reflect.TypeOf((*ModuleSubject)(nil)).Elem(),
		// The nested case, whose segments show the rule that separates this axis
		// from the two scope axes above: a module's levels live one per document,
		// so the locator names only the level the declaring file spells.
		make: func(f string) Subject {
			return newModuleSubject(f, "data.inner", "spec", "aggregates", "inner")
		},
	},
	SubjectBoundaryPlacement: {
		iface: reflect.TypeOf((*BoundaryPlacementSubject)(nil)).Elem(),
		// The routed case under a nested boundary, because it is the one carrying
		// every element of the identity at once and the one where the coordinate
		// and the locator's last segment are different strings: the Release
		// writes the component's local name inside the routing entry, and the id
		// it addresses is that name under the routed module's own prefix, which
		// appears in no file. Every segment is separator-free, which is what
		// exercises the locator assembly to full depth — a segment carrying the
		// dialect's separator stops it, so the constructor's deepest arguments
		// would go unexercised.
		make: func(f string) Subject {
			return newBoundaryPlacementSubject(f, AuthoringDocumentRelease, "prod/edge",
				"data.store", PlacementRoutedComponent,
				"spec", "boundaries", "prod", "boundaries", "edge",
				"aggregates", "data", "components", "store")
		},
	},
	SubjectReleaseEdgeConfiguration: {
		iface: reflect.TypeOf((*ReleaseEdgeConfigurationSubject)(nil)).Elem(),
		// The entry on a routed component under a nested boundary, because it is
		// the one carrying every element of the identity at once and reaching the
		// locator's full depth: the boundary spells each level under its own
		// container key, the routing entry contributes two more keys, and the
		// `uses` container, the socket and the alias follow the component. Every
		// segment is separator-free, which is what exercises the assembly to that
		// depth — a segment carrying the dialect's separator stops it, so the
		// constructor's deepest arguments would go unexercised.
		make: func(f string) Subject {
			return newReleaseEdgeConfigurationSubject(f, AuthoringDocumentRelease, "prod/edge",
				"data.store", "database", "primary",
				"spec", "boundaries", "prod", "boundaries", "edge",
				"aggregates", "data", "components", "store",
				"uses", "database", "primary")
		},
	},
	SubjectBoundaryComponentReference: {
		iface: reflect.TypeOf((*BoundaryComponentReferenceSubject)(nil)).Elem(),
		// The routed case, because it is the one where the coordinate and the
		// locator's last segment are different strings: the Release writes the
		// component's local name inside the routing entry, and the id it addresses
		// is that name under the routed module's own prefix.
		make: func(f string) Subject {
			return newBoundaryComponentReferenceSubject(f, "data.reader",
				"spec", "boundaries", "prod", "aggregates", "data", "components", "reader")
		},
	},
}

// TestSubjectKindTypesAreMutuallyDistinct proves the seal the completeness
// assertion above only inspects: no kind's concrete type satisfies another
// kind's interface, so a check declaring one kind cannot be handed another. It
// also exercises every kind's constructor and raise path, which is what stops a
// kind from being declared and never actually usable.
func TestSubjectKindTypesAreMutuallyDistinct(t *testing.T) {
	t.Parallel()

	const sourceFile = "catalog/aggregate.yaml"

	var missing []string
	for _, info := range SubjectKinds {
		if _, ok := subjectKindBindings[info.Kind]; !ok {
			missing = append(missing, string(info.Kind))
		}
	}
	if len(missing) > 0 {
		t.Fatalf("no binding for the derived kind(s) %s, so this test would certify a subset as "+
			"complete. Add each to subjectKindBindings.", strings.Join(missing, ", "))
	}
	if len(subjectKindBindings) != len(SubjectKinds) {
		t.Errorf("subjectKindBindings has %d entries against %d derived kinds, so it binds a kind the "+
			"generated set does not carry", len(subjectKindBindings), len(SubjectKinds))
	}

	type resolved struct {
		kind     SubjectKind
		iface    reflect.Type
		concrete reflect.Type
	}
	// Built in the generated set's order, so the pairwise report below is
	// identical on every run.
	all := make([]resolved, 0, len(SubjectKinds))

	for _, info := range SubjectKinds {
		binding := subjectKindBindings[info.Kind]
		subject := binding.make(sourceFile)
		concrete := reflect.TypeOf(subject)

		if !concrete.Implements(binding.iface) {
			t.Errorf("%s's constructor returns %s, which does not satisfy %s", info.Kind, concrete, binding.iface)
			continue
		}
		if got := subject.SourceFile(); got != sourceFile {
			t.Errorf("%s's subject reports source file %q; want %q — provenance must reach the subject "+
				"the framework supplies", info.Kind, got, sourceFile)
		}
		if locator := subject.Locator(); locator != "" && !strings.HasPrefix(locator, ".") {
			t.Errorf("%s's subject reports locator %q, which is not in the dotted dialect a consumer "+
				"splits on", info.Kind, locator)
		}
		objection := subject.Raise(errRuleBroken, "detail")
		if objection.Sentinel() == nil {
			t.Errorf("%s's subject raised an objection carrying no sentinel, so the kind cannot halt or "+
				"warn anything", info.Kind)
		}
		if objection.SourceFile() != subject.SourceFile() || objection.Locator() != subject.Locator() {
			t.Errorf("%s's objection provenance (%q, %q) differs from its subject's (%q, %q); provenance "+
				"is supplied by construction and no step may drop it", info.Kind,
				objection.SourceFile(), objection.Locator(), subject.SourceFile(), subject.Locator())
		}

		all = append(all, resolved{kind: info.Kind, iface: binding.iface, concrete: concrete})
	}

	if len(all) < 2 {
		t.Fatalf("%d kind(s) resolved, so no pair was compared and distinctness is unproven", len(all))
	}

	crossed := 0
	for _, subject := range all {
		for _, other := range all {
			if subject.kind == other.kind {
				continue
			}
			crossed++
			if subject.concrete.Implements(other.iface) {
				t.Errorf("%s's concrete type %s satisfies %s, the interface of kind %s.\n"+
					"INVARIANT: subject kinds are distinct at compile time, so a check declaring one "+
					"kind cannot be handed another.\n"+
					"CONSEQUENCE: the framework invokes a check once per instance of its declared "+
					"subject. A kind that satisfies another kind's interface can be handed the wrong "+
					"instances, so the check runs the wrong number of times over the wrong things and "+
					"the type system says nothing. A kind embedding another kind's interface rather "+
					"than Subject is the way this happens: coordinates nest, kinds do not.",
					subject.kind, subject.concrete, other.iface, other.kind)
			}
		}
	}
	t.Logf("distinctness: %d kinds, %d ordered pairs compared, none cross-satisfying", len(all), crossed)
}

// TestACheckDeclaringOneSubjectKindCannotBeHandedAnother closes the same seal
// from outside the package, where a check is actually written. It is a compile
// gate rather than a runtime assertion because the property it defends is a
// compile-time one: the program that violates it must not build.
func TestACheckDeclaringOneSubjectKindCannotBeHandedAnother(t *testing.T) {
	t.Parallel()

	// Calibration first. The two programs differ in exactly one thing — the
	// subject kind the check declares — so without the control a framework that
	// stopped compiling would make the gate "pass" while proving nothing.
	if out, err := compileTestdata(t, "legitimate_check"); err != nil {
		t.Fatalf("the calibration program under testdata/legitimate_check must compile, otherwise the "+
			"mismatch case below would fail for an unrelated reason.\ngo build error: %v\noutput:\n%s",
			err, out)
	}

	out, err := compileTestdata(t, "kind_mismatch")
	if err == nil {
		t.Fatalf("testdata/kind_mismatch compiled, so a check declaring one subject kind can be handed " +
			"another. The framework invokes a check once per instance of its declared subject; if the " +
			"kinds are interchangeable, a check runs over the wrong instances and no declaration says so.")
	}
	if !containsAnyGroup(out, [][]string{{"NodeSubject", "DocumentSubject"}}) {
		t.Errorf("testdata/kind_mismatch failed to compile, but for an unrecognised reason — the failure "+
			"must be the two subject kinds refusing to substitute, not something else.\n"+
			"want output naming both NodeSubject and DocumentSubject\ngot:\n%s", out)
	}
}

// subjectTypeNameFor derives a kind's Go type name from its coordinates, which
// is what links the generated set to the declarations without a hand-written
// table between them.
func subjectTypeNameFor(coordinates []string) string {
	var b strings.Builder
	for _, coordinate := range coordinates {
		for _, part := range strings.Split(coordinate, "-") {
			if part == "" {
				continue
			}
			b.WriteString(strings.ToUpper(part[:1]))
			b.WriteString(part[1:])
		}
	}
	b.WriteString("Subject")
	return b.String()
}

// packageInterfaceDecls returns every interface this package declares, read from
// its non-test sources so that a type declared only in a test cannot satisfy the
// completeness assertion.
func packageInterfaceDecls(t *testing.T) map[string]*ast.InterfaceType {
	t.Helper()

	_, files := packageNonTestFiles(t)

	out := map[string]*ast.InterfaceType{}
	for _, file := range files {
		for _, decl := range interfaceDecls(file) {
			if iface, ok := decl.spec.Type.(*ast.InterfaceType); ok {
				out[decl.name] = iface
			}
		}
	}
	return out
}

// interfaceShape reports whether an interface embeds the subject contract, the
// unexported methods it declares itself — its markers — and the exported ones.
//
// The split is the seal's actual shape rather than a convenience. Forgery is
// prevented by a marker being unexported: an unexported method name is
// qualified by the package that declares it, so a type declared elsewhere
// cannot declare that method and cannot satisfy the kind, however many exported
// methods the kind also carries. The count that seals a kind is therefore the
// count of *unexported* markers, and exported methods are admitted — they are
// the coordinates the kind carries, which a check needs in order to key a fact
// accessor to the instance it was invoked for.
//
// Reporting them separately rather than counting every named method together is
// what lets the caller assert the property instead of a proxy for it. A count
// over all named methods stands in for the seal only while a kind carries
// nothing but its marker, and it forbids coordinates while protecting nothing
// extra.
func interfaceShape(iface *ast.InterfaceType) (embedsSubject bool, markers, exported []string) {
	for _, method := range iface.Methods.List {
		if len(method.Names) == 0 {
			if id, ok := method.Type.(*ast.Ident); ok && id.Name == "Subject" {
				embedsSubject = true
			}
			continue
		}
		for _, name := range method.Names {
			if token.IsExported(name.Name) {
				exported = append(exported, name.Name)
				continue
			}
			markers = append(markers, name.Name)
		}
	}
	return embedsSubject, markers, exported
}
