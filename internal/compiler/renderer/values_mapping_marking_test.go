// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// valuesMarkingSecret is the declared value the marking tables read, spelled apart from
// both replacement texts so a run that printed it is told from a substituted one.
const valuesMarkingSecret = "s3cr3t-value"

// valuesMarkingContext is a node's render context carrying a declared value, an
// undeclared one, and an object holding a declared leaf beside an undeclared one.
func valuesMarkingContext(secret string) *types.TemplateContext {
	values := types.OrderedMap[any]{}
	values.Set("secret", secret)
	values.Set("note", "public")
	values.Set("creds", map[string]any{"user": "u", "password": "p"})
	return &types.TemplateContext{Values: values}
}

// valuesMarkingKeys declares the context's secret and the password inside its creds.
func valuesMarkingKeys() redact.Keys {
	return redact.Keys{Values: map[string]bool{"secret": true, "creds.password": true}}
}

// assertMarkedAndDecodedAsRendered evaluates one values mapping through
// evalValuesMapping and fails unless the marking is the one wanted and what it
// decoded equals what the evaluation every other value surface goes through
// decodes from the same input — the marking feeds nothing the build emits.
func assertMarkedAndDecodedAsRendered(t *testing.T, mapping string, data any, withheld redact.Keys, wantMarked bool) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("the evaluation panicked: %v", r)
		}
	}()
	var got valuesMappingOutput
	marked, evalErr, decodeErr := evalValuesMapping("valuesMapping[marking]", mapping, data, withheld, &got)
	if evalErr != nil || decodeErr != nil {
		t.Fatalf("the mapping failed: evaluation %v, decode %v", evalErr, decodeErr)
	}
	if marked != wantMarked {
		t.Errorf("marked = %v, want %v, for the mapping\n%s", marked, wantMarked, mapping)
	}
	var want valuesMappingOutput
	if _, evalErr, decodeErr := EvalTemplateDecoded("valuesMapping[marking]", mapping, data, withheld, &want); evalErr != nil || decodeErr != nil {
		t.Fatalf("the reference evaluation failed: evaluation %v, decode %v", evalErr, decodeErr)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("the decoded values are %v, want the rendered %v", got, want)
	}
}

// TestEvalValuesMapping_MarksOnlyOutputThatDependsOnADeclaredValue holds the
// marking of a values mapping to its output: a mapping whose references reach a
// declared value is marked where an evaluation over that value replaced fails or
// produces different text, and only there. A read of the object holding the value
// that prints only its keys, and a presence test, produce the same text whatever
// the value holds; an interpolation, a printed length, and a branch that fails on
// another value do not. A real value equal to one replacement text is still told
// apart by the other.
func TestEvalValuesMapping_MarksOnlyOutputThatDependsOnADeclaredValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		secret  string
		mapping string
		marked  bool
	}{
		{name: "reads nothing declared", secret: valuesMarkingSecret, mapping: `a: "{{ .values.note }}"`},
		{name: "interpolates the declared value", secret: valuesMarkingSecret, mapping: `a: "{{ .values.secret }}"`, marked: true},
		{name: "ranges over an object holding a declared leaf, printing keys", secret: valuesMarkingSecret,
			mapping: "{{ range $k, $_ := .values.creds }}{{ $k }}: x\n{{ end }}"},
		{name: "tests presence only", secret: valuesMarkingSecret, mapping: `a: "{{ if .values.secret }}set{{ end }}"`},
		{name: "a substituted run fails where the real passes", secret: valuesMarkingSecret,
			mapping: `{{ if ne .values.secret "s3cr3t-value" }}{{ fail "changed" }}{{ end }}a: b`, marked: true},
		{name: "prints the declared value's length", secret: valuesMarkingSecret, mapping: `a: "{{ len .values.secret }}"`, marked: true},
		{name: "the real value equals the first replacement text", secret: redact.Placeholder(),
			mapping: `a: "{{ .values.secret }}"`, marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assertMarkedAndDecodedAsRendered(t, tc.mapping, valuesMarkingContext(tc.secret), valuesMarkingKeys(), tc.marked)
		})
	}
}

// TestEvalValuesMapping_ReadsAConnectionByAKeyFromALocal is the shape of a mapping
// that reads a connection's provides by a key it takes from a local while it runs:
// the reference reaches every key the target publishes, the one declared sensitive
// among them, so the reading of references alone flags the mapping whatever the
// local names. The output depends on the declared key only where the local names
// it.
func TestEvalValuesMapping_ReadsAConnectionByAKeyFromALocal(t *testing.T) {
	t.Parallel()

	const mapping = `{{ $p := .context.uses.db.provides }}{{ with .locals.key }}a: "{{ index $p . }}"{{ else }}a: none{{ end }}`
	for _, tc := range []struct {
		key    string
		marked bool
	}{
		{key: ""},
		{key: "host"},
		{key: "token", marked: true},
	} {
		t.Run("local naming "+tc.key, func(t *testing.T) {
			t.Parallel()
			provides := types.OrderedMap[string]{}
			provides.Set("host", "db.example.com")
			provides.Set("token", "t0ken")
			uses := types.OrderedMap[types.EnrichedEdge]{}
			uses.Set("db", types.EnrichedEdge{Target: "db", Socket: "database", Component: "core/test/token-store", Provides: provides})
			locals := types.OrderedMap[string]{}
			locals.Set("key", tc.key)
			ctx := &types.TemplateContext{Context: types.ContextBlock{InstanceID: "api", Uses: uses}, Locals: locals}
			withheld := redact.NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{},
				redact.CrossNode{Provides: map[string]map[string]bool{"db": {"token": true}}}, nil, nil, redact.Derived{})
			assertMarkedAndDecodedAsRendered(t, mapping, ctx, withheld, tc.marked)
		})
	}
}

// TestEvalValuesMapping_ComparesCrossBoundaryCopiesWithAnUnsubstitutedRun holds the
// marking over a context whose connection crosses into another output, where the
// rendered output carries each value read off the connection in the form a
// provider locates rather than as the template read it. The substituted runs are
// compared with a run over the same data unsubstituted, so a mapping copying an
// undeclared value off the link and only testing whether the declared one is set
// is unmarked, and one copying the declared value is marked.
func TestEvalValuesMapping_ComparesCrossBoundaryCopiesWithAnUnsubstitutedRun(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		mapping string
		marked  bool
	}{
		{name: "copies the undeclared key and tests the declared key's presence",
			mapping: "host: \"{{ .context.uses.primary.provides.host }}\"\n{{ if .context.uses.primary.provides.token }}secured: true{{ end }}\n"},
		{name: "copies the declared key", mapping: "token: \"{{ .context.uses.primary.provides.token }}\"\n", marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provides := types.OrderedMap[string]{}
			provides.Set("host", "!GetAtt Db.Endpoint.Address")
			provides.Set("token", "t0ken")
			edge := types.IREdge{Source: "consumer", Target: "producer", Socket: "db", Alias: "primary"}
			links := newCrossBoundaryLinks()
			links.fires[linkTripleOf(edge)] = true
			links.aliases[linkTripleOf(edge)] = "primary"
			uses := types.OrderedMap[types.EnrichedEdge]{}
			uses.Set("primary", types.EnrichedEdge{Target: "producer", Socket: "db", Provides: provides})
			ctx := &types.TemplateContext{Context: types.ContextBlock{InstanceID: "consumer", Uses: uses}}
			stampCrossBoundaryLinkReads(ctx, "consumer", &types.IRGraph{Spec: types.IRSpec{Edges: []types.IREdge{edge}}}, links)
			if linkReadsOf(ctx) == nil {
				t.Fatal("the context carries no link read placeholders, so this case reads nothing it names")
			}
			withheld := redact.NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{},
				redact.CrossNode{Provides: map[string]map[string]bool{"primary": {"token": true}}}, nil, nil, redact.Derived{})
			assertMarkedAndDecodedAsRendered(t, tc.mapping, ctx, withheld, tc.marked)
		})
	}
}

// TestExecutePhaseBForFeature_MarksOnlyOutputThatDependsOnADeclaredValue holds a
// FeatureRecipe's values mapping to the same marking a Recipe's is held to: a
// mapping printing only the keys of an object holding a declared leaf is unmarked,
// and one interpolating the declared value is marked.
func TestExecutePhaseBForFeature_MarksOnlyOutputThatDependsOnADeclaredValue(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		mapping string
		marked  bool
	}{
		{name: "ranges over an object holding a declared leaf, printing keys", mapping: "{{ range $k, $_ := .values.creds }}{{ $k }}: x\n{{ end }}"},
		{name: "interpolates the declared value", mapping: `a: "{{ .values.secret }}"`, marked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fr := &types.FeatureRecipe{
				Metadata: types.FeatureRecipeMetadata{Name: "marking"},
				Spec:     types.FeatureRecipeSpec{ValuesMapping: types.ValuesMapping{Inline: tc.mapping}},
			}
			got, err := ExecutePhaseBForFeature(valuesMarkingContext(valuesMarkingSecret), fr, nil, valuesMarkingKeys())
			if err != nil {
				t.Fatalf("the mapping failed: %v", err)
			}
			if got.Sensitive != tc.marked {
				t.Errorf("Sensitive = %v, want %v", got.Sensitive, tc.marked)
			}
			if !got.Mapped {
				t.Errorf("a values mapping ran, so the values are mapped")
			}
		})
	}
}
