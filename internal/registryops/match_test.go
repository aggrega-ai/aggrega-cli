// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package registryops

import (
	"errors"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// sampleRecords returns a fixed record set in URI-sorted order — the order the
// index writer produces — so the determinism assertion can compare the filtered
// output against a stable input order.
func sampleRecords() []registryindex.Record {
	datastore := types.NewOrderedMap[string]()
	// The label key is fixture data rather than a coupling: what these cases
	// exercise is selector matching, for which any key would serve equally.
	datastore.Set("component.aggrega.ai/category", "datastore")
	return []registryindex.Record{
		{
			URI:         "oci://h/component/postgres",
			Name:        "postgres",
			Kind:        "Component",
			Description: "Relational DATABASE server",
			Category:    "datastore",
			Labels:      datastore,
		},
		{
			URI:         "oci://h/component/redis",
			Name:        "redis",
			Kind:        "Component",
			Description: "In-memory cache",
		},
		{
			URI:          "oci://h/plugin/helm-render",
			Name:         "helm-render",
			Kind:         "Plugin",
			Description:  "Helm rendering plugin",
			PluginType:   "render",
			Capabilities: []string{"fs.read", "net.none"},
		},
		{
			URI:          "oci://h/plugin/policy-guard",
			Name:         "policy-guard",
			Kind:         "Plugin",
			Description:  "Policy validator",
			PluginType:   "policy",
			Capabilities: []string{"net.none"},
		},
	}
}

// names projects the record slice to its names in order, so assertions read as
// the expected match set without comparing whole structs.
func names(recs []registryindex.Record) []string {
	if len(recs) == 0 {
		return nil
	}
	out := make([]string, len(recs))
	for i, r := range recs {
		out[i] = r.Name
	}
	return out
}

func TestFilterRecords(t *testing.T) {
	cases := []struct {
		name   string
		filter Query
		want   []string
	}{
		{
			name:   "empty filter matches every record in input order",
			filter: Query{},
			want:   []string{"postgres", "redis", "helm-render", "policy-guard"},
		},
		{
			name:   "query matches description case-insensitively",
			filter: Query{Text: "database"},
			want:   []string{"postgres"},
		},
		{
			name:   "query matches name case-insensitively",
			filter: Query{Text: "REDIS"},
			want:   []string{"redis"},
		},
		{
			name:   "query matching neither name nor description excludes",
			filter: Query{Text: "nonesuch"},
			want:   nil,
		},
		{
			name:   "single kind",
			filter: Query{Kinds: []string{"Plugin"}},
			want:   []string{"helm-render", "policy-guard"},
		},
		{
			name:   "repeated kind ORs within the set",
			filter: Query{Kinds: []string{"Component", "Plugin"}},
			want:   []string{"postgres", "redis", "helm-render", "policy-guard"},
		},
		{
			name:   "kind is case-insensitive: lowercase flag matches capitalized record kind",
			filter: Query{Kinds: []string{"component"}},
			want:   []string{"postgres", "redis"},
		},
		{
			name:   "selector matches on the catalog category label",
			filter: Query{Selector: "component.aggrega.ai/category=datastore"},
			want:   []string{"postgres"},
		},
		{
			name:   "empty selector matches every record",
			filter: Query{Selector: ""},
			want:   []string{"postgres", "redis", "helm-render", "policy-guard"},
		},
		{
			name:   "single capability",
			filter: Query{Capabilities: []string{"fs.read"}},
			want:   []string{"helm-render"},
		},
		{
			name:   "repeated capability ORs within the set",
			filter: Query{Capabilities: []string{"fs.read", "net.none"}},
			want:   []string{"helm-render", "policy-guard"},
		},
		{
			name:   "plugin type exact match",
			filter: Query{PluginType: "render"},
			want:   []string{"helm-render"},
		},
		{
			name:   "clauses AND together",
			filter: Query{Kinds: []string{"Plugin"}, PluginType: "render"},
			want:   []string{"helm-render"},
		},
		{
			name:   "a record failing any one clause is excluded",
			filter: Query{Kinds: []string{"Component"}, PluginType: "render"},
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := filterRecords(sampleRecords(), tc.filter)
			if err != nil {
				t.Fatalf("filterRecords: unexpected error: %v", err)
			}
			if !reflect.DeepEqual(names(got), tc.want) {
				t.Errorf("filterRecords = %v; want %v", names(got), tc.want)
			}
		})
	}
}

// TestFilterRecordsKindCaseFold confirms the kind clause folds the lowercase flag
// grammar onto the canonical capitalized kind a published record stores, so a
// user typing --kind plugin or --kind featurerecipe matches records the publish
// path stamped as Plugin and FeatureRecipe.
func TestFilterRecordsKindCaseFold(t *testing.T) {
	recs := []registryindex.Record{
		{URI: "oci://h/plugin/helm", Name: "helm", Kind: "Plugin"},
		{URI: "oci://h/featurerecipe/tls", Name: "tls", Kind: "FeatureRecipe"},
		{URI: "oci://h/component/api", Name: "api", Kind: "Component"},
	}
	cases := []struct {
		flag string
		want []string
	}{
		{"plugin", []string{"helm"}},
		{"featurerecipe", []string{"tls"}},
		{"FeatureRecipe", []string{"tls"}},
	}
	for _, tc := range cases {
		got, err := filterRecords(recs, Query{Kinds: []string{tc.flag}})
		if err != nil {
			t.Fatalf("filterRecords(--kind %q): %v", tc.flag, err)
		}
		if !reflect.DeepEqual(names(got), tc.want) {
			t.Errorf("filterRecords(--kind %q) = %v; want %v", tc.flag, names(got), tc.want)
		}
	}
}

// TestFilterRecordsNilLabelsWithSelector confirms a record carrying no labels
// cannot satisfy a non-empty selector, and that the nil-Labels bridge to the
// selector's value-typed OrderedMap does not panic.
func TestFilterRecordsNilLabelsWithSelector(t *testing.T) {
	got, err := filterRecords(sampleRecords(), Query{
		Selector: "component.aggrega.ai/category=datastore",
	})
	if err != nil {
		t.Fatalf("filterRecords: unexpected error: %v", err)
	}
	for _, r := range got {
		if r.Name == "redis" {
			t.Fatalf("redis has no labels; must not satisfy a non-empty selector")
		}
	}
}

// TestFilterRecordsMalformedSelector confirms a malformed selector surfaces as an
// error rather than silently matching nothing — a bad selector is a user error.
func TestFilterRecordsMalformedSelector(t *testing.T) {
	_, err := filterRecords(sampleRecords(), Query{Selector: "component.aggrega.ai/category in ("})
	if err == nil {
		t.Fatal("filterRecords: want error for malformed selector, got nil")
	}
}

// TestFilterRecordsRefusesASelectorTheGrammarDoesNotDefine pins, one fault per
// row, that each shape the selector grammar refuses reaches the search as the
// search's own selector error wrapping the grammar's refusal — including when no
// record is left for the selector to be evaluated against, where a search that
// read the selector only per record would answer "nothing matched". The control
// row is a well-formed selector over the same inputs.
func TestFilterRecordsRefusesASelectorTheGrammarDoesNotDefine(t *testing.T) {
	cases := []struct {
		name    string
		records []registryindex.Record
		filter  Query
		refused bool
		want    []string
	}{
		{
			name:    "an absence test whose key carries a value",
			records: sampleRecords(),
			filter:  Query{Selector: "!component.aggrega.ai/category=datastore"},
			refused: true,
		},
		{
			name:    "separators alone",
			records: sampleRecords(),
			filter:  Query{Selector: " , , "},
			refused: true,
		},
		{
			name:    "a set written without parentheses",
			records: sampleRecords(),
			filter:  Query{Selector: "component.aggrega.ai/category in datastore"},
			refused: true,
		},
		{
			name:    "a malformed selector over an empty index",
			records: nil,
			filter:  Query{Selector: "!component.aggrega.ai/category=datastore"},
			refused: true,
		},
		{
			name:    "a malformed selector when the text query leaves no record",
			records: sampleRecords(),
			filter:  Query{Text: "nonesuch", Selector: "!component.aggrega.ai/category=datastore"},
			refused: true,
		},
		{
			name:    "control: the inequality the absence test was written in place of",
			records: sampleRecords(),
			filter:  Query{Kinds: []string{"Component"}, Selector: "component.aggrega.ai/category!=datastore"},
			want:    []string{"redis"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("filterRecords panicked: %v", r)
				}
			}()
			got, err := filterRecords(tc.records, tc.filter)
			if !tc.refused {
				if err != nil {
					t.Fatalf("filterRecords: unexpected error: %v", err)
				}
				if !reflect.DeepEqual(names(got), tc.want) {
					t.Errorf("filterRecords = %v; want %v", names(got), tc.want)
				}
				return
			}
			var searchErr *SelectorError
			if !errors.As(err, &searchErr) {
				t.Fatalf("filterRecords error = %v (%T), want a *SelectorError", err, err)
			}
			var syntax *selector.SyntaxError
			if !errors.As(err, &syntax) {
				t.Fatalf("filterRecords error = %v does not carry the grammar's refusal", err)
			}
			if got != nil {
				t.Errorf("filterRecords returned %v alongside its refusal", names(got))
			}
		})
	}
}

// TestFilterRecordsOrderDeterminism confirms the filtered output preserves the
// input slice order and is identical across two runs of the same input.
func TestFilterRecordsOrderDeterminism(t *testing.T) {
	in := sampleRecords()
	first, err := filterRecords(in, Query{})
	if err != nil {
		t.Fatalf("filterRecords: unexpected error: %v", err)
	}
	second, err := filterRecords(in, Query{})
	if err != nil {
		t.Fatalf("filterRecords: unexpected error: %v", err)
	}
	if !reflect.DeepEqual(names(first), names(second)) {
		t.Fatalf("filterRecords not deterministic: %v vs %v", names(first), names(second))
	}
	want := names(in)
	if !reflect.DeepEqual(names(first), want) {
		t.Errorf("filterRecords did not preserve input order: got %v; want %v", names(first), want)
	}
}
