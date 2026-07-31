// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
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

// TestEveryDerivedSubjectKindHasASealedSubjectType is that assertion. It walks
// the generated kind set and requires, for each kind, a type of the name the
// kind's own coordinates derive, embedding the subject contract and carrying one
// unexported marker of its own.
func TestEveryDerivedSubjectKindHasASealedSubjectType(t *testing.T) {
	t.Parallel()

	if len(SubjectKinds) == 0 {
		t.Fatalf("the generated kind set is empty, so a clean result here certifies nothing")
	}

	declared := packageInterfaceDecls(t)

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

		embedsSubject, markers := interfaceShape(iface)
		if !embedsSubject {
			t.Errorf("%s does not embed Subject, so it is not a subject kind: it carries no provenance "+
				"and cannot raise", name)
		}
		switch len(markers) {
		case 1:
			marker := markers[0]
			if marker != strings.ToLower(marker[:1])+marker[1:] {
				t.Errorf("%s's marker method %s is exported, so a type outside this package could "+
					"declare it and satisfy the kind", name, marker)
			}
			if owner, taken := markerOwners[marker]; taken {
				t.Errorf("%s and %s both use the marker method %s, so the two kinds are the same type "+
					"to the compiler and a check declaring one can be handed the other", owner, name, marker)
				continue
			}
			markerOwners[marker] = name
		case 0:
			t.Errorf("%s adds no marker method of its own, so any other subject kind satisfies it and "+
				"the kinds are not distinct", name)
		default:
			sort.Strings(markers)
			t.Errorf("%s adds %d marker methods (%s); want exactly one, so that the seal is one fact "+
				"rather than a set to keep in step", name, len(markers), strings.Join(markers, ", "))
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

	t.Logf("kind coverage: %d derived kinds, %d subject types matched, %d distinct markers",
		len(SubjectKinds), len(expected), len(markerOwners))
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
			return newNodeSocketSubject(f, "spec", "components", "web", "uses", "databases")
		},
	},
	SubjectEdge: {
		iface: reflect.TypeOf((*EdgeSubject)(nil)).Elem(),
		make:  func(f string) Subject { return newEdgeSubject(f, "spec", "components", "web", "uses", "databases") },
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
		make:  func(f string) Subject { return newBoundaryProviderPathSubject(f, "spec", "boundaries", "prod") },
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

	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse this package's sources: %v", err)
	}

	out := map[string]*ast.InterfaceType{}
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range interfaceDecls(file) {
				if iface, ok := decl.spec.Type.(*ast.InterfaceType); ok {
					out[decl.name] = iface
				}
			}
		}
	}
	return out
}

// interfaceShape reports whether an interface embeds the subject contract, and
// the names of the methods it declares itself.
func interfaceShape(iface *ast.InterfaceType) (embedsSubject bool, markers []string) {
	for _, method := range iface.Methods.List {
		if len(method.Names) == 0 {
			if id, ok := method.Type.(*ast.Ident); ok && id.Name == "Subject" {
				embedsSubject = true
			}
			continue
		}
		for _, name := range method.Names {
			markers = append(markers, name.Name)
		}
	}
	return embedsSubject, markers
}
