// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package editops

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aggerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// authoredRelease is the deployment-side fixture. The authoring schema gives a
// Release's component surface operator concerns only — no developer values, and
// no way to declare a component the Aggregate did not — so it is the document
// the invariant refusals below are driven against.
const authoredRelease = `# The production deployment.
apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: storefront-prod
  environment: production
  version: 1.4.2
spec:
  aggregate: storefront
  manifest: prod-manifest
  components:
    web:
      infra:
        replicas: 5
`

func writeRelease(t *testing.T) (root, rel string) {
	t.Helper()
	root = t.TempDir()
	rel = "prod.release.yaml"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(authoredRelease), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return root, rel
}

// TestTheReleaseFixtureIsValidToBeginWith is the positive control for every
// refusal below.
//
// Without it a refusal could be reported for a document that was never valid,
// and the whole set would pass while proving nothing about the edit. It drives a
// legitimate deployment-side edit — an operator concern the Release surface does
// carry — and asserts it lands.
func TestTheReleaseFixtureIsValidToBeginWith(t *testing.T) {
	root, rel := writeRelease(t)

	res, err := Apply(t.Context(), root, Options{
		File:  rel,
		Kind:  KindAttachFeature,
		Node:  []string{"spec", "components", "web"},
		Name:  "budget-alert",
		Value: "core/finops/budget-alert:1.0.0",
	})
	if err != nil {
		t.Fatalf("a legitimate deployment-side edit was refused: %v", err)
	}
	if len(res.Written) != 1 {
		t.Fatalf("Written = %v, want the one document", res.Written)
	}
	if on := readBack(t, root, rel); !strings.Contains(on, "          type: core/finops/budget-alert:1.0.0\n") {
		t.Errorf("the attachment did not land\n--- document ---\n%s", on)
	}
}

// TestAnEditTheAuthoringSchemaRefusesWritesNothing is the invariant proof, and
// it is the reason the write is gated on validation rather than on a hand-coded
// rule.
//
// Both rows are things the deployment surface may not declare: developer values,
// and a component the developer's own document did not. Neither prohibition is
// restated in this package — the authoring schema carries them, and the same
// refusal would meet the same document typed by hand.
func TestAnEditTheAuthoringSchemaRefusesWritesNothing(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  Options
		names string
	}{
		{
			name: "a deployment document cannot declare developer values",
			opts: Options{
				Kind:  KindSetValue,
				Node:  []string{"spec", "components", "web"},
				Name:  "replicas",
				Value: 9,
			},
			// The schema names the property it does not admit, which is the
			// container the value would have gone in rather than the value's own
			// key — so that is what the reader is sent to.
			names: `unexpected property: "values"`,
		},
		{
			name: "a deployment document cannot declare a component of its own",
			opts: Options{
				Kind:  KindAddComponent,
				Node:  []string{"spec", "components"},
				Name:  "worker",
				Value: "core/api-server:1.0.0",
			},
			names: `unexpected property: "type"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, rel := writeRelease(t)
			opts := tc.opts
			opts.File = rel

			res, err := Apply(t.Context(), root, opts)
			if !errors.Is(err, ErrEditRefusedInvalidResult()) {
				t.Fatalf("want the invalid-result refusal; got %v", err)
			}
			if len(res.Written) != 0 {
				t.Errorf("a refused edit reported writing %v", res.Written)
			}
			if on := readBack(t, root, rel); on != authoredRelease {
				t.Errorf("a refused edit changed the document on disk\n--- document ---\n%s", on)
			}
			// The refusal names the property the schema does not admit, and
			// carries a position, so a caller is told what to change and where.
			if !strings.Contains(err.Error(), tc.names) {
				t.Errorf("the refusal does not name %s: %s", tc.names, err.Error())
			}
			if !strings.Contains(err.Error(), "correct the document to match its kind's schema") {
				t.Errorf("the refusal carries no advice: %s", err.Error())
			}
		})
	}
}

// TestTheRefusedConditionCarriesItsIdentifiers pins the registered condition's
// two identifiers, which are what a reader quotes and what a consumer outside Go
// matches on.
//
// Both are read off the error through the shared accessors rather than compared
// against a literal in the raise path, so this is a test of what a caller sees.
func TestTheRefusedConditionCarriesItsIdentifiers(t *testing.T) {
	root, rel := writeRelease(t)

	_, err := Apply(t.Context(), root, Options{
		File:  rel,
		Kind:  KindSetValue,
		Node:  []string{"spec", "components", "web"},
		Name:  "replicas",
		Value: 9,
	})
	if err == nil {
		t.Fatal("want a refusal")
	}
	code, ok := aggerrors.CodeOf(err)
	if !ok || code != "edit-refused-invalid-result" {
		t.Errorf("CodeOf = %q, %v; want the editops refusal code", code, ok)
	}
	short, ok := aggerrors.ShortCodeOf(err)
	if !ok || short != "E0275" {
		t.Errorf("ShortCodeOf = %q, %v; want E0275", short, ok)
	}
	if sentence, ok := aggerrors.MessageOf(err); !ok || !strings.Contains(sentence, "nothing was written") {
		t.Errorf("MessageOf = %q, %v; want the registered sentence", sentence, ok)
	}
}

// TestAnAlreadyInvalidDocumentIsReportedRatherThanEdited pins that the
// validation runs on the result whatever the document was before.
//
// A caller must never be told an edit landed in a document the product would
// refuse to read. Gating only on "the edit made it worse" would have let that
// through.
func TestAnAlreadyInvalidDocumentIsReportedRatherThanEdited(t *testing.T) {
	root := t.TempDir()
	rel := "broken.aggregate.yaml"
	// The version does not match the strict-semver pattern the envelope
	// requires, so the document is invalid before anything is edited.
	broken := strings.Replace(authoredAggregate, "version: 1.4.2", "version: not-a-version", 1)
	if err := os.WriteFile(filepath.Join(root, rel), []byte(broken), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := Apply(t.Context(), root, Options{
		File:  rel,
		Kind:  KindAddComponent,
		Node:  []string{"spec", "components"},
		Name:  "cache",
		Value: "core/redis:6.2.0",
	})
	if !errors.Is(err, ErrEditRefusedInvalidResult()) {
		t.Fatalf("want the invalid-result refusal; got %v", err)
	}
	if readBack(t, root, rel) != broken {
		t.Error("a refused edit changed the document")
	}
}

// TestARequestThatAddressesNoEditIsRefused covers the request-shape refusals.
//
// Each names what the caller must change in the vocabulary of the request — a
// field a protocol caller can send — rather than in any one surface's spelling
// of it. None of them is a registered condition: none halts anything, and each
// says one request's arguments do not address a place an entry can go.
func TestARequestThatAddressesNoEditIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name string
		opts Options
		want error
	}{
		{
			name: "no document named",
			opts: Options{Kind: KindAddComponent, Node: []string{"spec", "components"}, Name: "x", Value: "a/b:1.0.0"},
			want: errNoFile,
		},
		{
			name: "the document climbs out of the workspace",
			opts: Options{File: "../escaped.yaml", Kind: KindAddComponent,
				Node: []string{"spec", "components"}, Name: "x", Value: "a/b:1.0.0"},
			want: errFileOutsideWorkspace,
		},
		{
			name: "the document is addressed absolutely, outside the workspace",
			opts: Options{File: "/etc/passwd", Kind: KindAddComponent,
				Node: []string{"spec", "components"}, Name: "x", Value: "a/b:1.0.0"},
			want: errFileOutsideWorkspace,
		},
		{
			name: "an edit this capability does not apply",
			opts: Options{File: "storefront.aggregate.yaml", Kind: Kind("delete-component"),
				Node: []string{"spec", "components"}, Name: "web"},
			want: errUnknownKind,
		},
		{
			name: "no node path",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindAttachFeature,
				Name: "x", Value: "a/b:1.0.0"},
			want: errNoNodePath,
		},
		{
			name: "a node path carrying an empty segment",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindAttachFeature,
				Node: []string{"spec", "", "web"}, Name: "x", Value: "a/b:1.0.0"},
			want: errNoNodePath,
		},
		{
			name: "no name for the entry",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindAttachFeature,
				Node: []string{"spec", "components", "web"}, Value: "a/b:1.0.0"},
			want: errNoEntryName,
		},
		{
			name: "an edge with no socket",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindWireEdge,
				Node: []string{"spec", "components", "web"}, Name: "alias", Value: "db"},
			want: errNoSocket,
		},
		{
			name: "setting a value to a mapping, which would add a block",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindSetValue,
				Node: []string{"spec", "components", "web"}, Name: "limits",
				Value: map[string]any{"cpu": "2"}},
			want: errValueNotScalar,
		},
		{
			name: "setting a value to a sequence, which would add a block",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindSetValue,
				Node: []string{"spec", "components", "web"}, Name: "ports",
				Value: []any{8080}},
			want: errValueNotScalar,
		},
		{
			name: "an entity reference that is not a string",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindAddComponent,
				Node: []string{"spec", "components"}, Name: "x", Value: 7},
			want: errValueNotAReference,
		},
		{
			name: "an empty entity reference",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindAddComponent,
				Node: []string{"spec", "components"}, Name: "x", Value: "   "},
			want: errValueNotAReference,
		},
		{
			name: "the node path reaches nothing",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindSetValue,
				Node: []string{"spec", "components", "no-such-component"}, Name: "k", Value: "v"},
			want: errNodeAbsent,
		},
		{
			name: "the node path runs through a scalar",
			opts: Options{File: "storefront.aggregate.yaml", Kind: KindSetValue,
				Node: []string{"spec", "components", "web", "type", "deeper"}, Name: "k", Value: "v"},
			want: errContainerNotAMapping,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, _ := writeWorkspace(t, authoredAggregate)
			before := ""
			if tc.opts.File == "storefront.aggregate.yaml" {
				before = readBack(t, root, tc.opts.File)
			}

			_, err := Apply(t.Context(), root, tc.opts)
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v; got %v", tc.want, err)
			}
			if before != "" && readBack(t, root, "storefront.aggregate.yaml") != before {
				t.Error("a refused request changed the document")
			}
		})
	}
}

// TestAnEditOutsideTheSetIsRefusedWithTheSetNamed pins that refusing an edit
// this capability does not apply also says which edits it does.
//
// The expectation is read from the enumeration rather than written here, so an
// edit added to the enumeration is required of the refusal too rather than
// quietly missing from it.
//
// Every site that raises the condition is driven, because they raise one
// condition and a caller reaching any of them is owed one sentence. Two are not
// reachable through Apply, which refuses an edit outside the set before it
// composes anything; they are called directly so the set cannot go missing from
// one of them while the reachable one still names it.
func TestAnEditOutsideTheSetIsRefusedWithTheSetNamed(t *testing.T) {
	outside := Options{File: "storefront.aggregate.yaml", Kind: Kind("delete-component"),
		Node: []string{"spec", "components"}, Name: "web", Value: "a/b:1.0.0"}
	root, _ := writeWorkspace(t, authoredAggregate)

	_, applyErr := Apply(t.Context(), root, outside)
	_, containerErr := containerFor(outside)
	_, entryErr := entryValue(outside)

	for _, tc := range []struct {
		name string
		err  error
	}{
		{"the request check", applyErr},
		{"the container derivation", containerErr},
		{"the entry rendering", entryErr},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !errors.Is(tc.err, errUnknownKind) {
				t.Fatalf("want the refusal for an edit outside the set; got %v", tc.err)
			}
			got := tc.err.Error()
			if !strings.Contains(got, `"delete-component"`) {
				t.Errorf("the refusal does not quote the edit that was named: %s", got)
			}
			kinds := Kinds()
			if len(kinds) == 0 {
				t.Fatal("the enumeration is empty, so the loop below asserts nothing")
			}
			for _, k := range kinds {
				if !strings.Contains(got, string(k)) {
					t.Errorf("the refusal does not name the %q edit, which a caller may ask for: %s", k, got)
				}
			}
		})
	}
}

// TestNoWorkspaceIsRefused covers the one refusal that cannot be driven through
// the fixture, because it is about the root rather than the document.
func TestNoWorkspaceIsRefused(t *testing.T) {
	if _, err := Apply(t.Context(), "  ", Options{File: "a.yaml", Kind: KindSetValue,
		Node: []string{"spec"}, Name: "k", Value: "v"}); !errors.Is(err, errNoWorkspace) {
		t.Fatalf("want the no-workspace refusal; got %v", err)
	}
}

// TestADocumentThatDoesNotParseIsRefused pins that a malformed document is
// reported as such rather than edited into a different malformed shape.
func TestADocumentThatDoesNotParseIsRefused(t *testing.T) {
	root := t.TempDir()
	rel := "unparseable.yaml"
	// A mapping key repeated at the same level, which the parser refuses.
	junk := "kind: Aggregate\nspec:\n  a: 1\n   b: 2\n"
	if err := os.WriteFile(filepath.Join(root, rel), []byte(junk), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	_, err := Apply(t.Context(), root, Options{File: rel, Kind: KindSetValue,
		Node: []string{"spec"}, Name: "k", Value: "v"})
	if !errors.Is(err, errFileUnparseable) {
		t.Fatalf("want the unparseable refusal; got %v", err)
	}
	if readBack(t, root, rel) != junk {
		t.Error("a refused edit changed the document")
	}
}

// TestADocumentWithNoUsableKindIsRefused pins that the schema the result is
// checked against is read off the document, and that a document naming no
// usable kind is refused rather than edited unchecked.
//
// A caller-supplied kind could name the wrong schema, which would admit exactly
// the edit the validation exists to refuse — so the kind is never taken on trust
// and a document that does not declare one has no validation to pass.
func TestADocumentWithNoUsableKindIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    error
	}{
		{
			name:    "no kind at all",
			content: "apiVersion: aggrega.ai/v1alpha1\nspec:\n  components: {}\n",
			want:    errNoDocumentKind,
		},
		{
			name:    "a kind this product does not validate",
			content: "apiVersion: aggrega.ai/v1alpha1\nkind: NotAnAggregaKind\nspec:\n  components:\n    web:\n      type: a/b:1.0.0\n",
			want:    errUnknownDocumentKind,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			rel := "doc.yaml"
			if err := os.WriteFile(filepath.Join(root, rel), []byte(tc.content), 0o644); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			_, err := Apply(t.Context(), root, Options{File: rel, Kind: KindAddComponent,
				Node: []string{"spec", "components"}, Name: "x", Value: "a/b:1.0.0"})
			if !errors.Is(err, tc.want) {
				t.Fatalf("want %v; got %v", tc.want, err)
			}
			if readBack(t, root, rel) != tc.content {
				t.Error("a refused edit changed the document")
			}
		})
	}
}

// TestAFlowCollectionIsRefusedRatherThanRewritten pins the one shape that could
// not be edited without disturbing bytes the author wrote.
//
// Appending a block entry to a collection written on one line means rewriting
// that line, which is an edit rather than an insertion — so it is refused and
// named, instead of being silently converted to block style.
func TestAFlowCollectionIsRefusedRatherThanRewritten(t *testing.T) {
	flow := strings.Replace(authoredAggregate,
		"      values:\n        replicas: 3",
		"      values: { replicas: 3 }", 1)
	root, rel := writeWorkspace(t, flow)

	_, err := Apply(t.Context(), root, Options{File: rel, Kind: KindSetValue,
		Node: []string{"spec", "components", "web"}, Name: "timeout", Value: 30})
	if !errors.Is(err, errContainerFlowStyle) {
		t.Fatalf("want the flow-collection refusal; got %v", err)
	}
	if readBack(t, root, rel) != flow {
		t.Error("a refused edit changed the document")
	}
}

// TestASocketAuthoredAsAListIsRefusedRatherThanReshaped pins the other shape the
// insertion cannot address.
//
// The authoring surface lets a socket be written either as a list of targets or
// as a mapping of aliases. Adding an alias to the list form would mean
// converting what the author wrote from one form to the other, which is a
// rewrite rather than an addition, so it is refused and named.
func TestASocketAuthoredAsAListIsRefusedRatherThanReshaped(t *testing.T) {
	listForm := strings.Replace(authoredAggregate,
		"      uses:\n        database:\n          primary:\n            target: db",
		"      uses:\n        database:\n          - db", 1)
	root, rel := writeWorkspace(t, listForm)

	_, err := Apply(t.Context(), root, Options{File: rel, Kind: KindWireEdge,
		Node: []string{"spec", "components", "web"}, Socket: "database", Name: "replica", Value: "db"})
	if !errors.Is(err, errContainerNotAMapping) {
		t.Fatalf("want the not-a-mapping refusal; got %v", err)
	}
	if readBack(t, root, rel) != listForm {
		t.Error("a refused edit changed the document")
	}
}

// TestAnAdHocSocketOnAnAggregateIsAdmitted pins the refusal this package
// deliberately does NOT make, and it is the one most likely to be "helpfully"
// added later.
//
// The consumer may invent a connection: an Aggregate may author a socket the
// component it instantiates does not pre-declare, and a name one edit away from
// a declared one is reported as advice elsewhere rather than halted. What may be
// consumed across a connection is the producer's to declare, and checking that
// belongs to the resolution the compiler performs — not here. A guard in this
// package would refuse documents the product accepts.
func TestAnAdHocSocketOnAnAggregateIsAdmitted(t *testing.T) {
	root, rel := writeWorkspace(t, authoredAggregate)

	res, err := Apply(t.Context(), root, Options{File: rel, Kind: KindWireEdge,
		Node: []string{"spec", "components", "web"}, Socket: "a-socket-no-catalog-entity-declares",
		Name: "alias", Value: "db"})
	if err != nil {
		t.Fatalf("an ad-hoc socket on an Aggregate was refused, and it must not be: %v", err)
	}
	if len(res.Written) != 1 {
		t.Fatalf("Written = %v, want the one document", res.Written)
	}
	if on := readBack(t, root, rel); !strings.Contains(on, "        a-socket-no-catalog-entity-declares:\n") {
		t.Errorf("the ad-hoc socket did not land\n--- document ---\n%s", on)
	}
}
