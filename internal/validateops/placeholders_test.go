// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validateops

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/scaffoldops"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator/envelope"
)

// draft returns the drafted document for a kind token, exactly as every
// drafting surface hands it out.
func draft(t *testing.T, token, name string) string {
	t.Helper()
	doc, err := scaffoldops.Skeleton(token, name)
	if err != nil {
		t.Fatalf("draft a %s: %v", token, err)
	}
	return doc
}

// fill replaces each placeholder line of a drafted document with a filled one,
// given as alternating old and new lines. A line the document does not carry
// fails the test, so a fill that changed nothing cannot pass for one that did.
func fill(t *testing.T, doc string, pairs ...string) string {
	t.Helper()
	for i := 0; i+1 < len(pairs); i += 2 {
		if !strings.Contains(doc, pairs[i]) {
			t.Fatalf("the document carries no %q, so filling it asserts nothing:\n%s", pairs[i], doc)
		}
		doc = strings.Replace(doc, pairs[i], pairs[i+1], 1)
	}
	return doc
}

// validateDocument writes a document into a fresh workspace and runs the
// single-entity scope over it with no catalog directory.
func validateDocument(t *testing.T, doc string) (EntityResult, error) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "entity.yaml")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatalf("write the document: %v", err)
	}
	return ValidateEntity(context.Background(), dir, filepath.Join(dir, "cache"), nil, nil, path, false)
}

const (
	versionDrafted = `  version: ""`
	versionFilled  = `  version: "1.0.0"`
)

// TestADraftedDocumentIsRefusedNamingEveryPlaceholder drives drafted documents
// through the single-entity scope, as drafted and part-filled, and reads the
// refusal each one meets.
//
// Each row is refused for exactly the placeholders it still carries, every one
// named at once with what it takes, and the text the envelope gate renders for
// an empty value does not stand in for that line. The FeatureRecipe row with
// its version filled, and the Recipe row with its version and a source filled,
// are the ones the gate accepts: their schema admits the empty references, so
// what refuses them is the reading alone, and they carry no short code.
//
// A drafted Recipe with only its version filled is still refused by the gate,
// and not for a placeholder: a Recipe whose capability is not `eval` has to
// name at least one input — a source, a values mapping, a workload or
// artifacts — and the draft leaves that choice to the author, because the
// schema asks for it only conditionally. That finding reaches the author in the
// gate's own words ahead of the placeholder list, and the refusal carries the
// gate's condition.
func TestADraftedDocumentIsRefusedNamingEveryPlaceholder(t *testing.T) {
	t.Parallel()
	const (
		version       = "5:3: metadata.version takes a semantic version, such as 1.0.0"
		componentType = "7:5: metadata.labels.component.aggrega.ai/type takes one of: server, worker, task, function, stateful-server, resource"
		frCapability  = "7:3: spec.capability takes what its schema describes: The explicit provider capability this recipe binds to (e.g., 'helm', 'terraform', 'compose') — the provider kind that renders it."
		frImplements  = "8:3: spec.implements takes what its schema describes: Declares which catalog Feature type this recipe is able to implement"
		rcCapability  = "7:3: spec.capability takes what its schema describes: The explicit provider capability this recipe binds to (e.g., 'helm', 'terraform', 'compose', 'eval') — the provider kind that renders it."
		rcImplements  = "8:3: spec.implements takes what its schema describes: Declares which catalog component type this recipe is able to implement"
		pluginType    = "6:3: metadata.pluginType takes one of: provider, linter, pipeline-step, governance, cli, parser, importer"
	)
	cases := []struct {
		name  string
		token string
		fill  []string
		want  []string
		// gate is whether the envelope gate refuses the same bytes, which is
		// what decides whether the refusal carries the gate's condition.
		gate bool
		// rest is a finding the gate reports that is not about a placeholder,
		// which the refusal must still carry in the gate's own words; empty
		// where every finding the gate has is about one.
		rest string
	}{
		{name: "a Component as drafted", token: "component", want: []string{version, componentType}, gate: true},
		{name: "a Component with its version filled", token: "component", fill: []string{versionDrafted, versionFilled}, want: []string{componentType}, gate: true},
		{name: "a FeatureRecipe as drafted", token: "featurerecipe", want: []string{version, frCapability, frImplements}, gate: true},
		{name: "a FeatureRecipe with its version filled", token: "featurerecipe", fill: []string{versionDrafted, versionFilled}, want: []string{frCapability, frImplements}, gate: false},
		{name: "a Recipe with its version filled", token: "recipe", fill: []string{versionDrafted, versionFilled}, want: []string{rcCapability, rcImplements},
			gate: true, rest: "6:1: spec: no anyOf branch matched"},
		{name: "a Recipe with its version and a source filled", token: "recipe", fill: []string{
			versionDrafted, versionFilled,
			`  implements: ""`, `  implements: ""` + "\n" + `  source: "./src"`,
		}, want: []string{rcCapability, rcImplements}, gate: false},
		{name: "a Plugin as drafted", token: "plugin", want: []string{version, pluginType}, gate: true},
		{name: "a Feature as drafted", token: "feature", want: []string{version}, gate: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := fill(t, draft(t, tc.token, "my-entity"), tc.fill...)
			res, err := validateDocument(t, doc)
			if err == nil {
				t.Fatalf("the document was accepted with its placeholders unfilled, so an empty required value "+
					"reads to this product as a value:\n%s", doc)
			}
			var refusal *placeholderError
			if !errors.As(err, &refusal) {
				t.Fatalf("the document was refused, but not for its placeholders: %v", err)
			}
			text := err.Error()
			noun := "placeholders"
			if len(tc.want) == 1 {
				noun = "placeholder"
			}
			if head := strconv.Itoa(len(tc.want)) + " " + noun + " still owed in"; !strings.Contains(text, head) {
				t.Errorf("the refusal does not count the placeholders it names (want %q):\n%s", head, text)
			}
			at := 0
			for _, line := range tc.want {
				i := strings.Index(text[at:], line)
				if i < 0 {
					t.Errorf("the refusal does not name this placeholder with what it takes, in document order:\n  %s\nrefusal:\n%s", line, text)
					continue
				}
				at += i + len(line)
			}
			for _, dump := range []string{"does not match regular expression", "does not equal any of"} {
				if strings.Contains(text, dump) {
					t.Errorf("the gate's rendering of an empty value still stands in for the placeholder line (%q):\n%s", dump, text)
				}
			}
			if got := errors.Is(err, envelope.ErrEntityEnvelope()); got != tc.gate {
				t.Errorf("carries the envelope condition = %v, want %v: the refusal must carry the gate's condition "+
					"exactly when the gate refused the same bytes", got, tc.gate)
			}
			if tc.gate && !strings.Contains(text, envelope.ErrEntityEnvelope().Error()) {
				t.Errorf("the refusal carries the envelope condition without its registered sentence:\n%s", text)
			}
			if tc.rest != "" && !strings.Contains(text, tc.rest) {
				t.Errorf("the gate's finding that is not about a placeholder was dropped (want %q):\n%s", tc.rest, text)
			}
			for _, line := range res.Diagnostics {
				if strings.HasPrefix(line, "Schema:") {
					t.Errorf("the report went on past the placeholders to %q", line)
				}
			}
		})
	}
}

// TestFillingThePlaceholdersIsAllADraftNeeds is the other half of the refusal
// above: the same drafts with their placeholders filled and nothing else
// changed validate, so what refused them was the placeholders and not their
// shape.
func TestFillingThePlaceholdersIsAllADraftNeeds(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		token string
		fill  []string
	}{
		{name: "a Component", token: "component", fill: []string{
			versionDrafted, versionFilled,
			`    component.aggrega.ai/type: ""`, `    component.aggrega.ai/type: "server"`,
		}},
		{name: "a FeatureRecipe", token: "featurerecipe", fill: []string{
			versionDrafted, versionFilled,
			`  capability: ""`, `  capability: "helm"`,
			`  implements: ""`, `  implements: "example/observability"`,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			doc := fill(t, draft(t, tc.token, "my-entity"), tc.fill...)
			if _, err := validateDocument(t, doc); err != nil {
				t.Fatalf("filling the placeholders and changing nothing else left the document refused: %v\n%s", err, doc)
			}
		})
	}
}

// TestAnEmptyValueNoRequiredListNamesIsNotAPlaceholder pins the reading's
// bound. An optional field left empty, and a field required only inside an
// optional list the author chose to write, are not values every document of the
// kind owes, so neither is refused here.
func TestAnEmptyValueNoRequiredListNamesIsNotAPlaceholder(t *testing.T) {
	t.Parallel()
	doc := fill(t, draft(t, "featurerecipe", "my-entity"),
		versionDrafted, versionFilled+"\n  description: \"\"\n  maintainers:\n    - name: \"\"",
		`  capability: ""`, `  capability: "helm"`,
		`  implements: ""`, `  implements: "example/observability"`,
	)
	if _, err := validateDocument(t, doc); err != nil {
		t.Fatalf("an empty value no required list reaches from the root was refused: %v\n%s", err, doc)
	}
}

// TestAFindingThatIsNotAPlaceholderIsStillReported holds that the reading
// replaces only what the gate says about an empty value. A finding elsewhere in
// the same document still reaches the author, in the gate's own words, beside
// the placeholder list.
func TestAFindingThatIsNotAPlaceholderIsStillReported(t *testing.T) {
	t.Parallel()
	doc := draft(t, "featurerecipe", "my-entity") + "stray_key: true\n"
	_, err := validateDocument(t, doc)
	if err == nil {
		t.Fatalf("a document with an undeclared key and unfilled placeholders was accepted:\n%s", doc)
	}
	text := err.Error()
	if !strings.Contains(text, "stray_key") {
		t.Errorf("the finding that is not about a placeholder was dropped:\n%s", text)
	}
	if !strings.Contains(text, "5:3: metadata.version takes a semantic version, such as 1.0.0") {
		t.Errorf("the placeholder list is missing beside the other finding:\n%s", text)
	}
	if !errors.Is(err, envelope.ErrEntityEnvelope()) {
		t.Errorf("the gate refused the document, and the refusal no longer carries its condition: %v", err)
	}
}

// admittedEmpty classifies every required value the schema of an accepted kind
// admits as the empty string, reached from the document root through required
// properties alone. Each entry says why an empty one is never a legal value,
// which is what makes it a placeholder the reading refuses rather than a value.
//
// It is a register on purpose: the schemas are read from the binary, so a kind
// whose schema starts admitting an empty required value fails the test below
// until someone decides here whether an empty one is ever legal.
var admittedEmpty = map[string]string{
	"Recipe spec.capability":        "it names the provider that renders the recipe, and an empty one names none",
	"Recipe spec.implements":        "it names the Component type the recipe implements, and an empty one names none",
	"FeatureRecipe spec.capability": "it names the provider that renders the recipe, and an empty one names none",
	"FeatureRecipe spec.implements": "it names the Feature type the recipe implements, and an empty one names none",
}

// TestTheSchemaAdmitsAnEmptyRequiredValueOnlyWhereClassified is the census of
// the accepted kinds' schemas, taken on every run rather than once.
//
// Each kind is drafted, and the draft carries exactly the values the kind
// requires, with an empty string wherever a string is owed. Every one of those
// is read as a placeholder; the ones the envelope gate does not refuse are the
// ones the schema admits empty, and each must be classified above.
func TestTheSchemaAdmitsAnEmptyRequiredValueOnlyWhereClassified(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, kind := range SingleEntityKinds() {
		doc := draft(t, entitykind.Segment(entitykind.Kind(kind)), "my-entity")
		found, err := readPlaceholders(envelope.Kind(kind), []byte(doc), "draft.yaml")
		if err != nil {
			t.Fatalf("%s: read the draft's placeholders: %v", kind, err)
		}
		if got, want := len(found), countEmptyStrings(t, doc); got != want {
			t.Errorf("%s: the draft carries %d empty values and the reading found %d, so the reading and the "+
				"drafting disagree about what a %s requires:\n%s", kind, want, got, kind, doc)
		}
		refusedAt := map[string]bool{}
		if _, gateErr := envelope.ValidateBytes(envelope.Kind(kind), []byte(doc), "draft.yaml"); gateErr != nil {
			var gate *envelope.Error
			if !errors.As(gateErr, &gate) {
				t.Fatalf("%s: the gate could not validate the draft: %v", kind, gateErr)
			}
			for _, f := range gate.Findings {
				refusedAt[f.FieldPath] = true
			}
		}
		for _, p := range found {
			if refusedAt[p.path] {
				continue
			}
			key := kind + " " + p.path
			seen[key] = true
			if _, classified := admittedEmpty[key]; !classified {
				t.Errorf("the %s schema admits an empty %s, and no entry classifies it. Decide whether an empty one "+
					"is ever a legal value: if it never is, add it with the reason; if it sometimes is, the reading "+
					"must not refuse it and has to learn when", kind, p.path)
			}
		}
	}
	keys := make([]string, 0, len(admittedEmpty))
	for k := range admittedEmpty {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if !seen[k] {
			t.Errorf("%q is classified as admitted empty, and its schema no longer admits it; the entry is stale", k)
		}
	}
}

// countEmptyStrings counts the empty-string scalars in a YAML document.
func countEmptyStrings(t *testing.T, doc string) int {
	t.Helper()
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &root); err != nil {
		t.Fatalf("parse the draft: %v", err)
	}
	var count func(n *yaml.Node) int
	count = func(n *yaml.Node) int {
		if n.Kind == yaml.ScalarNode {
			if n.ShortTag() == "!!str" && n.Value == "" {
				return 1
			}
			return 0
		}
		total := 0
		for _, c := range n.Content {
			total += count(c)
		}
		return total
	}
	return count(&root)
}

// TestEveryVersionPatternReadsAsASemanticVersion reads each accepted kind's
// metadata.version pattern from the schema the binary embeds, so a pattern that
// stopped behaving as the semantic-version grammar shows up here rather than as
// a refusal quietly describing it less briefly.
func TestEveryVersionPatternReadsAsASemanticVersion(t *testing.T) {
	t.Parallel()
	read := 0
	for _, kind := range SingleEntityKinds() {
		raw, ok := schemas.SchemaByKind(entitykind.Segment(entitykind.Kind(kind)))
		if !ok {
			t.Fatalf("%s: no embedded schema", kind)
		}
		var doc struct {
			Properties struct {
				Metadata struct {
					Properties struct {
						Version struct {
							Pattern string `json:"pattern"`
						} `json:"version"`
					} `json:"properties"`
				} `json:"metadata"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(raw), &doc); err != nil {
			t.Fatalf("%s: decode the schema: %v", kind, err)
		}
		pattern := doc.Properties.Metadata.Properties.Version.Pattern
		if pattern == "" {
			continue
		}
		read++
		if !isSemanticVersionPattern(pattern) {
			t.Errorf("%s: metadata.version's pattern does not behave as the semantic-version grammar: %q", kind, pattern)
		}
	}
	if read == 0 {
		t.Fatal("no kind declared a version pattern, so this asserted nothing")
	}
	for _, pattern := range []string{`^[a-z]+$`, `^.*$`, `^\d+\.\d+$`, `(`} {
		if isSemanticVersionPattern(pattern) {
			t.Errorf("%q reads as a semantic-version pattern and is not one", pattern)
		}
	}
}

// TestFirstSentence pins where a description's opening sentence ends.
func TestFirstSentence(t *testing.T) {
	t.Parallel()
	cases := []struct{ in, want string }{
		{"One sentence. Another one.", "One sentence."},
		{"Names a type (e.g. core/api. or core/web). Then more.", "Names a type (e.g. core/api. or core/web)."},
		{"Carries a version such as 1.0.0 inside. Then more.", "Carries a version such as 1.0.0 inside."},
		{"No full stop at all", "No full stop at all"},
		{"  Ends here.  ", "Ends here."},
	}
	for _, tc := range cases {
		if got := firstSentence(tc.in); got != tc.want {
			t.Errorf("firstSentence(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestEveryEntityKindIsAcceptedOrRouted binds the refusal's routes to the
// entity-kind vocabulary: every entity kind is either one this scope validates
// or one its refusal names another scope for, never both and never neither.
func TestEveryEntityKindIsAcceptedOrRouted(t *testing.T) {
	t.Parallel()
	for _, k := range entitykind.Kinds() {
		_, accepted := knownEntityKind(string(k))
		routed := unacceptedKindRoute(k) != ""
		if accepted == routed {
			t.Errorf("%s: accepted = %v, routed = %v; an entity kind is exactly one of the two", k, accepted, routed)
		}
	}
	for _, k := range SingleEntityKinds() {
		if !entitykind.IsKind(entitykind.Kind(k)) {
			t.Errorf("%s is accepted by the single-entity scope and is not an entity kind", k)
		}
	}
}

// TestAnUnacceptedKindNamesTheAcceptedKindsAndWhereItIsChecked drives a drafted
// document of each routed kind through the scope, and one of a kind outside the
// entity vocabulary.
func TestAnUnacceptedKindNamesTheAcceptedKindsAndWhereItIsChecked(t *testing.T) {
	t.Parallel()
	const accepted = "the kinds this scope accepts are Component, Feature, Recipe, FeatureRecipe, Workload and Plugin"
	cases := []struct {
		name  string
		doc   string
		route string
	}{
		{name: "an Aggregate", doc: draft(t, "aggregate", "my-app"),
			route: "An Aggregate is checked by the whole-catalog scope when it sits in a catalog directory, and by the Release scope over a Release that deploys it"},
		{name: "a Manifest", doc: draft(t, "manifest", "my-manifest"),
			route: "A Manifest is checked by the Release scope over a Release that names it"},
		{name: "a Release", doc: draft(t, "release", "my-release"),
			route: "A Release is checked by the Release scope: validate it as the Release rather than as an entity"},
		{name: "a SchemaLibrary", doc: draft(t, "schemalibrary", "MyLibrary"),
			route: "A SchemaLibrary is checked by the whole-catalog scope when it sits in a catalog directory"},
		{name: "a kind outside the entity vocabulary", doc: "apiVersion: aggrega.ai/v1alpha1\nkind: NotAKind\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := validateDocument(t, tc.doc)
			if err == nil {
				t.Fatalf("the single-entity scope accepted a kind it does not validate:\n%s", tc.doc)
			}
			text := err.Error()
			if !strings.Contains(text, "is not a single-entity validation target") || !strings.Contains(text, accepted) {
				t.Errorf("the refusal does not name the kinds this scope accepts:\n%s", text)
			}
			if tc.route == "" {
				if !strings.HasSuffix(text, accepted) {
					t.Errorf("a kind outside the entity vocabulary was routed somewhere:\n%s", text)
				}
				return
			}
			if !strings.Contains(text, tc.route) {
				t.Errorf("the refusal does not name the scope that checks this kind (want %q):\n%s", tc.route, text)
			}
		})
	}
}
