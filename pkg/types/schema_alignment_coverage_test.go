// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Population coverage for the bidirectional schema↔struct alignment suite.
// The suite itself walks a hand-listed set of cases; this gate closes the hole
// that hand-listing opens — a document kind declared in the envelope package and
// forgotten here would be alignment-checked by nothing, and no test would say
// so. Every declared kind must be accounted for as either carrying an alignment
// case or named in the exclusion set with the reason it is out, and the two sets
// must not overlap or claim a kind that is not declared.
package types_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// envelopeKindSource is the file declaring the document-kind vocabulary,
// addressed relative to this package directory.
//
// The kinds are read from source rather than from an enumerator because Go
// cannot enumerate a type's constants reflectively, and the alternative — an
// exported enumerator on the envelope package — is a permanent API commitment
// on a package other code depends on, added for one test's convenience. Reading
// source is the narrower cost, and it is bounded: the parse refuses to certify
// anything when it recognises no constants (see declaredEnvelopeKinds).
const envelopeKindSource = "../validator/envelope/envelope.go"

// alignmentExclusions names each document kind the alignment suite deliberately
// does not cover, mapped to the reason it is out. A kind may be excluded only by
// name — there is no wildcard — so adding a kind and forgetting it is a failure
// rather than a silent omission, and every entry here is a claim a reader can
// check against the reason given.
var alignmentExclusions = map[envelope.Kind]string{
	// The intermediate representation's Go types carry json tags and no yaml
	// tags, while this suite's schema-to-Go direction reads yaml tags only. The
	// walk over the IR root therefore resolves almost nothing: it finds a
	// handful of incidental tags, reached through sub-structs the IR shares with
	// the yaml-authored entity types, against the schema's full property
	// surface. Covering the kind here would report the schema as overwhelmingly
	// unmatched rather than align anything, and papering that over with
	// exceptions would document a mechanical mismatch as a set of deliberate
	// non-1:1 positions. Emitted IR is validated against its schema on the
	// emission side instead, where the instance is real rather than reflected.
	envelope.KindIR: "IR types are json-tagged; the schema-to-Go direction reads yaml tags, so the walk cannot see the IR surface. Emitted IR is schema-validated on the emission side.",
}

// TestAlignmentSuiteCoversEveryEnvelopeKind walks the declared kind population
// against the alignment suite's cases and the exclusion set, in both directions
// plus non-overlap. The reverse directions are what keep the exclusion set from
// rotting: an entry naming a kind that no longer exists, or one that has since
// gained a case, is reported here rather than quietly suppressing a kind nobody
// meant to exclude.
func TestAlignmentSuiteCoversEveryEnvelopeKind(t *testing.T) {
	t.Parallel()

	declared := declaredEnvelopeKinds(t)
	declaredSet := map[envelope.Kind]bool{}
	for _, k := range declared {
		declaredSet[k] = true
	}

	covered := map[envelope.Kind]bool{}
	for _, c := range alignmentCases() {
		covered[c.kind] = true
	}

	for _, k := range declared {
		_, excluded := alignmentExclusions[k]
		if !covered[k] && !excluded {
			t.Errorf("kind %q is declared but has neither an alignment case nor a named exclusion, "+
				"so nothing checks its schema against its Go type: add a case to alignmentCases(), "+
				"or add it to alignmentExclusions with the reason it cannot be covered", k)
		}
		if covered[k] && excluded {
			t.Errorf("kind %q is claimed both by an alignment case and by the exclusion set; "+
				"remove the exclusion", k)
		}
	}

	for k := range covered {
		if !declaredSet[k] {
			t.Errorf("alignmentCases() carries a case for %q, which is not a declared kind", k)
		}
	}

	for k, reason := range alignmentExclusions {
		if !declaredSet[k] {
			t.Errorf("stale exclusion %q — no such kind is declared any more; remove the exclusion", k)
			continue
		}
		if strings.TrimSpace(reason) == "" {
			t.Errorf("exclusion %q carries no reason; an unexplained exclusion is indistinguishable "+
				"from an oversight", k)
		}
	}

	// Each parsed name must be a kind the envelope package can actually
	// validate with. This is what makes reading the vocabulary out of source
	// safe: a misparse would otherwise invent a kind, and the invented kind
	// would then be satisfiable by an equally invented exclusion.
	for _, k := range declared {
		if _, err := envelope.ValidateInstance(k, map[string]any{}); err != nil {
			t.Errorf("kind %q was read from %s but the envelope package cannot validate with it "+
				"(%v); the parsed vocabulary and the shipped one disagree", k, envelopeKindSource, err)
		}
	}
}

// declaredEnvelopeKinds returns every document kind declared as a Kind-typed
// string constant, sorted.
//
// Only specs carrying an explicit Kind type are read, which is exact rather
// than approximate: a const spec that omits the type does not inherit the
// preceding spec's type in Go — it takes its own from its value — so such a
// constant genuinely is not a Kind. The one form that would inherit, a spec
// with neither type nor value repeating the previous expression list, is
// rejected outright below: it cannot be read reliably here, and skipping it
// silently would undercount the population, which is the one failure this gate
// must never have.
func declaredEnvelopeKinds(t *testing.T) []envelope.Kind {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.FromSlash(envelopeKindSource), nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", envelopeKindSource, err)
	}

	var out []envelope.Kind
	for _, decl := range file.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.CONST {
			continue
		}
		sawKindSpec := false
		for _, spec := range gd.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			if vs.Type == nil && len(vs.Values) == 0 {
				if sawKindSpec {
					t.Fatalf("%s: const spec %v carries neither a type nor a value, so it repeats the "+
						"preceding Kind-typed spec and cannot be read here; give it an explicit "+
						"Kind type and value", envelopeKindSource, specNames(vs))
				}
				continue
			}
			ident, ok := vs.Type.(*ast.Ident)
			if !ok || ident.Name != "Kind" {
				continue
			}
			sawKindSpec = true
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					t.Fatalf("%s: Kind const %s has no value", envelopeKindSource, name.Name)
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					t.Fatalf("%s: Kind const %s is not assigned a string literal, so its value cannot "+
						"be read from source", envelopeKindSource, name.Name)
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("%s: unquote Kind const %s value %q: %v", envelopeKindSource, name.Name, lit.Value, err)
				}
				out = append(out, envelope.Kind(s))
			}
		}
	}

	// A parse that recognises nothing must fail rather than certify an empty
	// population: reading the vocabulary out of source is only sound while the
	// read demonstrably worked, and a silently empty result would make this
	// whole gate pass by covering nothing.
	if len(out) == 0 {
		t.Fatalf("%s: found no Kind-typed string constants, so the declared population could not be "+
			"read and this gate would certify nothing; the kind declarations moved or changed shape",
			envelopeKindSource)
	}

	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

func specNames(vs *ast.ValueSpec) []string {
	var out []string
	for _, n := range vs.Names {
		out = append(out, n.Name)
	}
	return out
}
