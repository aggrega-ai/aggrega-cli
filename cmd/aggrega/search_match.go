// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// searchFilter is the set of predicates a local index search applies to each
// record. Each field narrows one facet; an empty field imposes no constraint on
// its facet. Query is free text matched as a substring; Kinds and Capabilities
// are match-any sets within their facet; Selector is a label selector; PluginType
// is an exact match.
type searchFilter struct {
	Query        string
	Kinds        []string
	Selector     string
	Capabilities []string
	PluginType   string
}

// filterRecords returns the records that satisfy every clause of f, in the input
// slice's order. The match contract is AND across clauses and OR within a
// set-valued clause: a record matches when it satisfies the free-text query AND
// the kind set AND the label selector AND the capability set AND the plugin type
// — where the kind and capability sets each match when the record satisfies any
// one of their members. An empty clause imposes no constraint, so an empty filter
// returns every record.
//
// The free-text query is a case-insensitive substring tested against the
// record's name and its description; a record matches when either contains the
// query. The kind clause is case-insensitive against the canonical kind
// vocabulary: the flag grammar is lowercase (component, feature, recipe,
// featurerecipe, workload, plugin) while a published record stores its canonical
// capitalized kind (Component, FeatureRecipe, Plugin, ...), so the membership
// test folds case to bridge the two. The plugin-type clause is a case-sensitive
// exact match. The selector is evaluated against the record's reconstructed
// catalog labels; a record with no labels cannot satisfy a non-empty selector,
// and a malformed selector is a caller error that propagates rather than
// silently matching nothing.
//
// The input order is preserved and no map is iterated in the result path, so the
// output is byte-stable across runs of the same input.
func filterRecords(records []registryindex.Record, f searchFilter) ([]registryindex.Record, error) {
	query := strings.ToLower(f.Query)
	var out []registryindex.Record
	for _, rec := range records {
		if query != "" {
			name := strings.ToLower(rec.Name)
			desc := strings.ToLower(rec.Description)
			if !strings.Contains(name, query) && !strings.Contains(desc, query) {
				continue
			}
		}
		if len(f.Kinds) > 0 && !anyKind(f.Kinds, rec.Kind) {
			continue
		}
		if f.Selector != "" {
			var labels types.OrderedMap[string]
			if rec.Labels != nil {
				labels = *rec.Labels
			}
			ok, err := selector.MatchString(f.Selector, labels)
			if err != nil {
				return nil, fmt.Errorf("search selector: %w", err)
			}
			if !ok {
				continue
			}
		}
		if len(f.Capabilities) > 0 && !anyCapability(f.Capabilities, rec.Capabilities) {
			continue
		}
		if f.PluginType != "" && rec.PluginType != f.PluginType {
			continue
		}
		out = append(out, rec)
	}
	return out, nil
}

// anyKind reports whether the record's kind matches any of the wanted kinds,
// case-insensitively — the OR-within-the-set semantics of the kind clause,
// folding the lowercase flag grammar onto the canonical capitalized kind a
// published record stores.
func anyKind(wanted []string, have string) bool {
	for _, w := range wanted {
		if strings.EqualFold(w, have) {
			return true
		}
	}
	return false
}

// anyCapability reports whether the record's capabilities include at least one of
// the wanted capabilities — the OR-within-the-set semantics of the capability
// clause.
func anyCapability(wanted, have []string) bool {
	for _, w := range wanted {
		if slices.Contains(have, w) {
			return true
		}
	}
	return false
}
