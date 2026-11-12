// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package gotmpl

import (
	"bytes"
	"sort"
	"strings"
	"testing"
	"text/template"
	"text/template/parse"
)

func mustParseForRoots(t *testing.T, text string) parse.Node {
	t.Helper()
	tree, err := ParseTree(text, "{{", "}}")
	if err != nil {
		t.Fatalf("parse %q: %v", text, err)
	}
	return tree
}

func refPaths(refs []RootRef) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Path)
	}
	return out
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Inside a range or with body the dot is rebound to the element, so a field read
// there names a field of that element and is not a context root. Treating it as
// one would reject every iterating template in the catalog. Asserted on the walk
// itself, since what a rebound field resolves to at execute time is the
// evaluator's business and not the root check's.
func TestCollectRootRefs_DotRebinding(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpl string
		want []string
	}{
		{`{{ range .values }}{{ .anything }}{{ end }}`, []string{"values"}},
		{`{{ range $k, $v := .infra }}{{ $k }}{{ .whatever }}{{ end }}`, []string{"infra"}},
		{`{{ with .metadata }}{{ .release_name }}{{ end }}`, []string{"metadata"}},
		{`{{ with .values.absentkey }}{{ .deep.deeper }}{{ else }}{{ .infra.replicas }}{{ end }}`, []string{"values", "infra"}},
		{`{{ range .context.uses }}{{ .target }}{{ .provides.host }}{{ end }}`, []string{"context"}},
		{`{{ range .steps }}{{ range .outputs }}{{ .deep }}{{ end }}{{ end }}`, []string{"steps"}},
	}
	for _, tc := range cases {
		got := map[string]bool{}
		for _, ref := range CollectRootRefs(mustParseForRoots(t, tc.tmpl)) {
			got[ref.Segs[0]] = true
		}
		if len(got) != len(tc.want) {
			t.Errorf("%q: collected roots %v, want exactly %v", tc.tmpl, keysOf(got), tc.want)
			continue
		}
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("%q: collected roots %v, want %v", tc.tmpl, keysOf(got), tc.want)
			}
		}
	}
}

// A chained field hangs off the receiver's value, not off the dot, so it is not
// a root. This is what lets a defensive wrapper stay legal while the root it
// wraps is still checked.
func TestCollectRootRefs_ChainIsNotARoot(t *testing.T) {
	t.Parallel()
	tree := mustParseForRoots(t, `{{ (default dict .values.tls).ca }}`)
	refs := CollectRootRefs(tree)
	if len(refs) != 1 || refs[0].Segs[0] != "values" {
		t.Fatalf("expected exactly the wrapped root .values.tls, got %v", refPaths(refs))
	}
}

// $ names the context root under any rebinding, so a root-anchored reference
// inside a ranged body is still checked.
func TestCollectRootRefs_RootVariableInsideRebinding(t *testing.T) {
	t.Parallel()
	tree := mustParseForRoots(t, `{{ $notroot := .values }}{{ range .values }}{{ $.infra.replicas }}{{ $notroot.x }}{{ end }}`)
	got := map[string]bool{}
	for _, r := range CollectRootRefs(tree) {
		got[r.Segs[0]] = true
	}
	if !got["values"] || !got["infra"] {
		t.Errorf("expected roots values and infra, got %v", keysOf(got))
	}
	if got["notroot"] || got["x"] {
		t.Errorf("a template-declared variable must not read as a context root: %v", keysOf(got))
	}
}

// What the walk deliberately does NOT name, pinned so the residue is not later
// mistaken for a second hole and closed by guesswork. A range body's dot is one
// element of the container, and what a function yields is decided at evaluation
// rather than by the text — so neither is a path the template names, and
// attributing either would refuse authoring on an inference the walk has no basis
// to make. The last case is the shipped edge-configuration idiom: attributing the
// inner reads there would treat an edge's own fields as context pillars.
func TestCollectRootRefs_UnnameableDotIsNotAttributed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpl string
		want []string
	}{
		{`{{ range .context.uses }}{{ .target }}{{ end }}`, []string{".context.uses"}},
		{`{{ range $k, $v := .infra }}{{ $k }}{{ $v.anything }}{{ end }}`, []string{".infra"}},
		{`{{ with default dict .metadata }}{{ .no_such_key }}{{ end }}`, []string{".metadata"}},
		{`{{ $m := default dict .metadata }}{{ $m.no_such_key }}`, []string{".metadata"}},
		{`{{ with index .context.uses "main" }}{{ with .values }}{{ .protocol }}{{ end }}{{ end }}`, []string{".context.uses"}},
	}
	for _, tc := range cases {
		got := refPaths(CollectRootRefs(mustParseForRoots(t, tc.tmpl)))
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%q collected %v, want exactly %v", tc.tmpl, got, tc.want)
		}
	}
}

// A path bound to a variable and then iterated through it is one reference, iterated.
// Resolving the alias to the path it aliases is what lets the two occurrences share a
// single entry so the flag merges: without the merge the binding's non-iterated entry
// would have an empty string seeded at the leaf, and the range would then fail on a
// string where an absent reference would have iterated as nothing.
func TestCollectRootRefs_AliasedIterationMergesOntoOneReference(t *testing.T) {
	t.Parallel()
	refs := CollectRootRefs(mustParseForRoots(t, `{{ $list := .values.items }}{{ range $list }}{{ . }}{{ end }}`))
	if got := refPaths(refs); strings.Join(got, ",") != ".values.items" {
		t.Fatalf("collected %v, want exactly [.values.items]", got)
	}
	if !refs[0].Iterated {
		t.Error("a path iterated through an alias must be marked iterated, or its leaf is seeded as a scalar")
	}
}

// A variable reassigned to something the walk cannot name UNBINDS rather than
// keeping the path it used to hold, so a later read is not attributed to a root the
// author never wrote at that point.
func TestCollectRootRefs_ReassignedVariableDoesNotKeepAStalePath(t *testing.T) {
	t.Parallel()
	refs := CollectRootRefs(mustParseForRoots(t,
		`{{ $m := .metadata }}{{ range .values }}{{ $m := . }}{{ $m.no_such_key }}{{ end }}`))
	for _, r := range refs {
		if r.Path == ".metadata.no_such_key" {
			t.Errorf("a shadowed variable must not resolve through its outer binding: %v", refPaths(refs))
		}
	}
}

// The four rules the seeding is defined by, each asserted on what the map holds
// afterwards rather than on rendered text, so a failure names the rule that broke.
// A rendered-output form of the same rules sits on each consuming surface; this is
// the primitive's own contract.
func TestSeedAbsentLeafKeys_SuppliesOnlyWhereTheRulesAllow(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		tmpl    string
		data    map[string]any
		wantAt  []string // dotted paths that must hold the empty string afterwards
		wantOut []string // dotted paths that must still be absent afterwards
	}{
		{
			name:   "absent leaf under a resolving parent is supplied",
			tmpl:   `{{ .parameters.missing }}`,
			data:   map[string]any{"parameters": map[string]any{}},
			wantAt: []string{"parameters.missing"},
		},
		{
			name:   "a deeper chain is supplied at its leaf only",
			tmpl:   `{{ .parameters.db.missing }}`,
			data:   map[string]any{"parameters": map[string]any{"db": map[string]any{}}},
			wantAt: []string{"parameters.db.missing"},
		},
		{
			name:    "an ITERATED reference is left alone",
			tmpl:    `{{ range .parameters.items }}{{ . }}{{ end }}`,
			data:    map[string]any{"parameters": map[string]any{}},
			wantOut: []string{"parameters.items"},
		},
		{
			name:    "a reference whose PARENT is absent is left alone",
			tmpl:    `{{ .parameters.nosuch.deeper }}`,
			data:    map[string]any{"parameters": map[string]any{}},
			wantOut: []string{"parameters.nosuch"},
		},
		{
			name:    "a one-segment ROOT the surface does not define is left alone",
			tmpl:    `{{ .parameters }}{{ .undeclared }}`,
			data:    map[string]any{"parameters": map[string]any{}},
			wantOut: []string{"undeclared"},
		},
		{
			name:    "a parent resolving to a NON-map is left alone",
			tmpl:    `{{ .parameters.scalar.deeper }}`,
			data:    map[string]any{"parameters": map[string]any{"scalar": "x"}},
			wantOut: []string{"parameters.scalar.deeper"},
		},
		{
			name:    "a present leaf keeps its own value",
			tmpl:    `{{ .parameters.filled }}`,
			data:    map[string]any{"parameters": map[string]any{"filled": "kept"}},
			wantOut: []string{"parameters.absent"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			SeedAbsentLeafKeys(tc.data, CollectRootRefs(mustParseForRoots(t, tc.tmpl)), nil)
			for _, path := range tc.wantAt {
				got, present := lookupPath(tc.data, path)
				if !present {
					t.Errorf("%s: nothing supplied at %s", tc.name, path)
					continue
				}
				if got != "" {
					t.Errorf("%s: %s = %#v, want the empty string", tc.name, path, got)
				}
			}
			for _, path := range tc.wantOut {
				if _, present := lookupPath(tc.data, path); present {
					t.Errorf("%s: %s was supplied for, and must not have been", tc.name, path)
				}
			}
		})
	}
	// The present-leaf case above only asserts an unrelated key stayed absent; pin
	// the value itself so an overwrite cannot pass.
	data := map[string]any{"parameters": map[string]any{"filled": "kept"}}
	SeedAbsentLeafKeys(data, CollectRootRefs(mustParseForRoots(t, `{{ .parameters.filled }}`)), nil)
	if got, _ := lookupPath(data, "parameters.filled"); got != "kept" {
		t.Errorf("a filled leaf must keep its value, got %#v", got)
	}
}

// Supplying a leaf makes its parent non-empty, so a guard over the PARENT flips
// from falsy to truthy once the template reads an absent key beneath it. It is a
// specified consequence rather than an accident, and it is pinned here because the
// only way to avoid it is to stop supplying the leaf — which is the emitted
// placeholder text the seeding exists to remove.
func TestSeedAbsentLeafKeys_SuppliedLeafMakesItsParentTruthy(t *testing.T) {
	t.Parallel()
	guardOnly := map[string]any{"parameters": map[string]any{}}
	SeedAbsentLeafKeys(guardOnly, CollectRootRefs(mustParseForRoots(t, `{{ if .parameters }}yes{{ end }}`)), nil)
	if got := len(guardOnly["parameters"].(map[string]any)); got != 0 {
		t.Fatalf("a guard alone reads no key, so nothing may be supplied; parent holds %d key(s)", got)
	}
	guardAndRead := map[string]any{"parameters": map[string]any{}}
	SeedAbsentLeafKeys(guardAndRead, CollectRootRefs(mustParseForRoots(t, `{{ if .parameters }}yes{{ end }}{{ .parameters.absent }}`)), nil)
	if got := len(guardAndRead["parameters"].(map[string]any)); got != 1 {
		t.Fatalf("reading an absent key must supply it, making the parent non-empty; parent holds %d key(s)", got)
	}
	// The leaf itself stays falsy, which is what keeps a guard written on the FIELD
	// behaving as it did over the unset value.
	if v, _ := lookupPath(guardAndRead, "parameters.absent"); v != "" {
		t.Errorf("supplied leaf = %#v, want the empty string so a guard over it stays falsy", v)
	}
}

// The data a surface hands over is the data written into, and a non-map is
// declined rather than panicking.
func TestSeedAbsentLeafKeys_NonMapDataIsDeclined(t *testing.T) {
	t.Parallel()
	refs := CollectRootRefs(mustParseForRoots(t, `{{ .parameters.missing }}`))
	SeedAbsentLeafKeys("not a map", refs, nil)
	SeedAbsentLeafKeys(nil, refs, nil)
	SeedAbsentLeafKeys(struct{ Parameters map[string]any }{}, refs, nil)
}

// One path written through $ and through the dot is one reference: the walk keeps
// one entry for it, quoting the spelling the template wrote first, and the
// iteration flag merges across the two spellings exactly as it merges across two
// occurrences of one spelling.
func TestCollectRootRefs_BothAnchorsAreOneReference(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		tmpl     string
		wantPath string
		iterated bool
	}{
		{"dot first, ranged through $", `{{ .values.user }}{{ range $.values.user }}x{{ end }}`, ".values.user", true},
		{"$ first, ranged through $", `{{ $.values.user }}{{ range $.values.user }}x{{ end }}`, "$.values.user", true},
		{"ranged through $ first, read through the dot", `{{ range $.values.user }}x{{ end }}{{ .values.user }}`, "$.values.user", true},
		{"ranged through the dot, read through $", `{{ range .values.user }}x{{ end }}{{ $.values.user }}`, ".values.user", true},
		{"read both ways, never ranged", `{{ $.values.a }}{{ .values.a }}`, "$.values.a", false},
		{"inside a rebound body, through $ and an outer alias", `{{ $v := .values }}{{ range .infra.list }}{{ $.values.a }}{{ end }}{{ $v.a }}`, "$.values.a", false},
	}
	for _, tc := range cases {
		var matching []RootRef
		for _, ref := range CollectRootRefs(mustParseForRoots(t, tc.tmpl)) {
			if strings.Join(ref.Segs, ".") == strings.TrimPrefix(strings.TrimPrefix(tc.wantPath, "$"), ".") {
				matching = append(matching, ref)
			}
		}
		if len(matching) != 1 {
			t.Errorf("%s: %q recorded %d entries for one path, want exactly 1: %v",
				tc.name, tc.tmpl, len(matching), refPaths(matching))
			continue
		}
		if matching[0].Path != tc.wantPath {
			t.Errorf("%s: the entry quotes %q, want the first-written spelling %q", tc.name, matching[0].Path, tc.wantPath)
		}
		if matching[0].Iterated != tc.iterated {
			t.Errorf("%s: Iterated = %v, want %v", tc.name, matching[0].Iterated, tc.iterated)
		}
	}
}

// A field chained off a parenthesised single reference names that reference's path
// extended by the field, on the same terms as the direct spelling — so a check over
// the direct spelling sees the parenthesised one too. A function applied inside the
// parentheses still leaves the chained field unnamed.
func TestCollectRootRefs_ParenthesisedReferenceNamesItsPath(t *testing.T) {
	t.Parallel()
	cases := []struct {
		tmpl string
		want []string
	}{
		{`{{ (.locals).absent }}`, []string{".locals.absent"}},
		{`{{ ($.locals).absent }}`, []string{"$.locals.absent"}},
		{`{{ ((.locals)).absent }}`, []string{".locals.absent"}},
		{`{{ (.values).a.b }}`, []string{".values.a.b"}},
		{`{{ (.).locals.v }}`, []string{".locals.v"}},
		{`{{ with .metadata }}{{ (.).release_name }}{{ end }}`, []string{".metadata", ".metadata.release_name"}},
		{`{{ $m := .metadata }}{{ ($m).release_name }}`, []string{".metadata", ".metadata.release_name"}},
		{`{{ range (.values).list }}{{ . }}{{ end }}`, []string{".values.list"}},
		{`{{ (default dict .values.tls).ca }}`, []string{".values.tls"}},
		{`{{ range .values.list }}{{ (.).name }}{{ end }}`, []string{".values.list"}},
	}
	for _, tc := range cases {
		got := refPaths(CollectRootRefs(mustParseForRoots(t, tc.tmpl)))
		if strings.Join(got, ",") != strings.Join(tc.want, ",") {
			t.Errorf("%q collected %v, want exactly %v", tc.tmpl, got, tc.want)
		}
	}
	refs := CollectRootRefs(mustParseForRoots(t, `{{ range (.values).list }}{{ . }}{{ end }}`))
	if len(refs) != 1 || !refs[0].Iterated {
		t.Errorf("a parenthesised reference ranged over must be one iterated entry, got %+v", refs)
	}
}

// The parenthesised spelling of a read of an undeclared local is classified exactly
// as the direct spelling is, so every surface refusing the direct one through this
// classification refuses the parenthesised one with the same fault and key.
func TestClassifyRootKeyReference_ParenthesisedRootReadsAsTheDirectSpelling(t *testing.T) {
	t.Parallel()
	declared := []string{"probe"}
	for _, text := range []string{
		`{{ .locals.absent }}`,
		`{{ (.locals).absent }}`,
		`{{ ($.locals).absent }}`,
	} {
		got := ClassifyRootKeyReference(text, "{{", "}}", LocalsContextRoot, declared, declared)
		if got.Fault != RootKeyUndeclared || got.Key != "absent" {
			t.Errorf("%s: classified %+v, want an undeclared-key fault on %q", text, got, "absent")
		}
	}
}

// A root read whole that the surface defines but that holds nothing — nil, or left
// out of the data because nothing was supplied for it — is supplied the empty string,
// as an absent leaf is. A root the surface does not define, a root holding a value,
// and a root the template iterates are left exactly as they were.
func TestSeedAbsentLeafKeys_DefinedRootHoldingNothingIsSupplied(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		tmpl    string
		data    map[string]any
		defined []string
		want    map[string]any // the root's value afterwards; a missing key means absent
	}{
		{"defined root left out of the data", `{{ .workload }}`, map[string]any{}, []string{"workload"},
			map[string]any{"workload": ""}},
		{"defined root left out, read through $", `{{ $.workload }}`, map[string]any{}, []string{"workload"},
			map[string]any{"workload": ""}},
		{"root the data holds as nil", `{{ .workload }}`, map[string]any{"workload": nil}, nil,
			map[string]any{"workload": ""}},
		{"root the surface does not define", `{{ .undeclared }}`, map[string]any{}, []string{"workload"},
			map[string]any{}},
		{"root holding a value keeps it", `{{ .workload }}`, map[string]any{"workload": "kept"}, []string{"workload"},
			map[string]any{"workload": "kept"}},
		{"root the template iterates", `{{ range .features }}{{ . }}{{ end }}`, map[string]any{}, []string{"features"},
			map[string]any{}},
		{"root a guard reads", `{{ if .workload }}y{{ end }}`, map[string]any{}, []string{"workload"},
			map[string]any{"workload": ""}},
	}
	for _, tc := range cases {
		SeedAbsentLeafKeys(tc.data, CollectRootRefs(mustParseForRoots(t, tc.tmpl)), tc.defined)
		if len(tc.data) != len(tc.want) {
			t.Errorf("%s: data holds %v, want %v", tc.name, tc.data, tc.want)
			continue
		}
		for key, want := range tc.want {
			if got, present := tc.data[key]; !present || got != want {
				t.Errorf("%s: %s = %#v (present %v), want %#v", tc.name, key, got, present, want)
			}
		}
	}
}

// The outcomes the walk and the seeding exist for, read off the template engine
// itself under the absent-key policy the value surfaces run with: a defined root
// holding nothing prints nothing, a path ranged in one spelling and read in the
// other iterates as nothing instead of failing on a string, and a guarded read of a
// present value prints the value as it always did.
func TestSeedAbsentLeafKeys_RenderedOutcomes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		tmpl    string
		data    map[string]any
		defined []string
		want    string
	}{
		{"a defined root holding nothing prints nothing", `[{{ .workload }}]`,
			map[string]any{}, []string{"workload"}, "[]"},
		{"a nil root prints nothing", `[{{ .workload }}]`,
			map[string]any{"workload": nil}, nil, "[]"},
		{"ranged through $ and read through the dot iterates as nothing",
			`{{ if false }}{{ .values.user }}{{ end }}{{ range $.values.user }}x{{ end }}`,
			map[string]any{"values": map[string]any{}}, []string{"values"}, ""},
		{"ranged through the dot and read through $ iterates as nothing",
			`{{ range .values.user }}x{{ end }}{{ if false }}{{ $.values.user }}{{ end }}`,
			map[string]any{"values": map[string]any{}}, []string{"values"}, ""},
		{"a present value prints as it did", `{{ .values.user }}{{ range $.values.list }}-{{ . }}{{ end }}`,
			map[string]any{"values": map[string]any{"user": "u", "list": []any{"a"}}}, []string{"values"}, "u-a"},
		{"a guard over a defined root holding nothing stays falsy", `{{ if .workload }}y{{ else }}n{{ end }}{{ .workload }}`,
			map[string]any{}, []string{"workload"}, "n"},
	}
	for _, tc := range cases {
		tpl, err := template.New(tc.name).Option("missingkey=zero").Parse(tc.tmpl)
		if err != nil {
			t.Fatalf("%s: parse: %v", tc.name, err)
		}
		SeedAbsentLeafKeys(tc.data, CollectRootRefs(tpl.Tree.Root), tc.defined)
		var out bytes.Buffer
		if err := tpl.Execute(&out, tc.data); err != nil {
			t.Errorf("%s: execution failed: %v", tc.name, err)
			continue
		}
		if out.String() != tc.want {
			t.Errorf("%s: rendered %q, want %q", tc.name, out.String(), tc.want)
		}
	}
}

func lookupPath(data map[string]any, path string) (any, bool) {
	segs := strings.Split(path, ".")
	cur := data
	for _, seg := range segs[:len(segs)-1] {
		next, ok := cur[seg].(map[string]any)
		if !ok {
			return nil, false
		}
		cur = next
	}
	v, ok := cur[segs[len(segs)-1]]
	return v, ok
}
