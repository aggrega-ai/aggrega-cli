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
					"Check[V, C, %s]: it enumerates the kind's coordinates, assembles each instance's "+
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
// scalar: the edge kind carries a single coordinate whose identity is a node and a
// socket taken together, and it exposes an accessor for each half of that pair. A
// criterion equating the two counts would fail that kind, whose shape the package
// argues for where the kind is declared.
//
// # What it cannot see
//
// It reads a parameter count, so a constructor accepting a coordinate and storing
// nothing satisfies it. That a coordinate reaches the check is proved
// behaviourally, per kind, by the traversal tests.
//
// It says nothing about a kind exposing no accessor at all: a coordinate the
// generated set declares and no accessor returns is outside its reach. That is
// the residual rather than a clean corner, so the census logged below reports how
// many enumerated kinds are in that state instead of leaving a reader to assume
// none are.
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

	exposing, bare := 0, 0
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
		if info.Traversal == TraversalPull {
			if len(accessors) > 0 {
				exposing++
			} else {
				bare++
			}
		}

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

	t.Logf("coordinate supply: %d of %d enumerated kinds expose at least one coordinate accessor; %d expose "+
		"none, so a check declaring one of those cannot name the instance it was invoked for — a residual "+
		"this assertion neither fails on nor can see",
		exposing, exposing+bare, bare)
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
// which is its third type argument.
func checkSubjectTypeArgument(expr ast.Expr) (string, bool) {
	instantiation, ok := expr.(*ast.IndexListExpr)
	if !ok || len(instantiation.Indices) != 3 {
		return "", false
	}
	if base, ok := instantiation.X.(*ast.Ident); !ok || base.Name != "Check" {
		return "", false
	}
	subject, ok := instantiation.Indices[2].(*ast.Ident)
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
		make:  func(f string) Subject { return newDocumentParameterSubject(f, "spec", "parameters", "db_size") },
	},
	SubjectDocumentAssertion: {
		iface: reflect.TypeOf((*DocumentAssertionSubject)(nil)).Elem(),
		make:  func(f string) Subject { return newDocumentAssertionSubject(f, "spec", "assertions", "port-set") },
	},
	SubjectNode: {
		iface: reflect.TypeOf((*NodeSubject)(nil)).Elem(),
		make:  func(f string) Subject { return newNodeSubject(f, "spec", "components", "web") },
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
			return newEdgeSubject(f, "web", "databases", "spec", "components", "web", "uses", "databases")
		},
	},
	SubjectNodeAttachment: {
		iface: reflect.TypeOf((*NodeAttachmentSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentSubject(f, "spec", "components", "web", "features", "tls")
		},
	},
	SubjectNodeAttachmentSocket: {
		iface: reflect.TypeOf((*NodeAttachmentSocketSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentSocketSubject(f, "spec", "components", "web", "features", "tls", "uses", "issuer")
		},
	},
	SubjectNodeSlot: {
		iface: reflect.TypeOf((*NodeSlotSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeSlotSubject(f, "spec", "components", "web", "features", "sidecar")
		},
	},
	SubjectNodeMechanism: {
		iface: reflect.TypeOf((*NodeMechanismSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeMechanismSubject(f, "spec", "components", "web", "inject", "env")
		},
	},
	SubjectNodeMechanismDeliveryKey: {
		iface: reflect.TypeOf((*NodeMechanismDeliveryKeySubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeMechanismDeliveryKeySubject(f, "spec", "components", "web", "inject", "env", "DB_HOST")
		},
	},
	SubjectNodePillarLeaf: {
		iface: reflect.TypeOf((*NodePillarLeafSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodePillarLeafSubject(f, "spec", "components", "web", "values", "app", "replicas")
		},
	},
	SubjectNodeParameter: {
		iface: reflect.TypeOf((*NodeParameterSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeParameterSubject(f, "spec", "components", "web", "parameters", "db_size")
		},
	},
	SubjectNodeAttachmentPillarLeaf: {
		iface: reflect.TypeOf((*NodeAttachmentPillarLeafSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newNodeAttachmentPillarLeafSubject(f, "spec", "components", "web", "features", "tls", "values", "app", "issuer")
		},
	},
	SubjectNodeLocalsKey: {
		iface: reflect.TypeOf((*NodeLocalsKeySubject)(nil)).Elem(),
		make:  func(f string) Subject { return newNodeLocalsKeySubject(f, "spec", "locals", "fullname") },
	},
	SubjectNodeProvidesKey: {
		iface: reflect.TypeOf((*NodeProvidesKeySubject)(nil)).Elem(),
		make:  func(f string) Subject { return newNodeProvidesKeySubject(f, "spec", "provides", "host") },
	},
	SubjectNodePipelineStep: {
		iface: reflect.TypeOf((*NodePipelineStepSubject)(nil)).Elem(),
		make:  func(f string) Subject { return newNodePipelineStepSubject(f, "spec", "pipeline", "render") },
	},
	SubjectBoundaryProviderPath: {
		iface: reflect.TypeOf((*BoundaryProviderPathSubject)(nil)).Elem(),
		make: func(f string) Subject {
			return newBoundaryProviderPathSubject(f, "prod", "helm", "a.yaml", "spec", "boundaries", "prod")
		},
	},
	SubjectNodeArtifact: {
		iface: reflect.TypeOf((*NodeArtifactSubject)(nil)).Elem(),
		make:  func(f string) Subject { return newNodeArtifactSubject(f, "spec", "artifacts", "values-yaml") },
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
