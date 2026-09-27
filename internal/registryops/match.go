// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package registryops

import (
	"errors"
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/registryindex"
	"github.com/aggrega-ai/aggrega-cli/pkg/selector"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// errNoIndexAnywhere reports that the configured host set is non-empty and not
// one of them has a local index, so there is nothing to search.
//
// It is distinguished from an empty result because the two mean different things:
// an empty result is a completed search that matched nothing, and this is a search
// that could not be performed. Presenting the second as the first is how a caller
// concludes an entity does not exist when nobody has built the index.
//
// It is a package-local error value and not a registered halting condition, which
// is what every refusal this search reports is: none of them is built into a
// diagnostic envelope, so none carries a short code a reader could quote, and the
// named-registry refusal beside it — the nearer and more specific of the two —
// carries no identity either. A value declared here under the condition naming
// convention would promise a reader an identifier no surface prints. It stays a
// declared value rather than an inline one so a caller separates this refusal
// from an empty result by identity instead of by its message text.
var errNoIndexAnywhere = errors.New("no local index found for any configured registry; run 'aggrega registry update'")

// IndexNotBuiltError reports that a specifically named registry has no local
// index. It names the host, because a caller that asked for one registry is owed
// which one.
type IndexNotBuiltError struct {
	Host string
}

func (e *IndexNotBuiltError) Error() string {
	return "registry " + e.Host + " has no local index; run 'aggrega registry update'"
}

// SelectorError reports a malformed label selector. It is a caller's own input
// error rather than a failure of the search, which is why it carries its own type
// — a surface phrases the two differently, and telling them apart from the message
// text would mean matching on prose.
type SelectorError struct {
	Err error
}

func (e *SelectorError) Error() string { return "search selector: " + e.Err.Error() }

func (e *SelectorError) Unwrap() error { return e.Err }

// filterRecords returns the records that satisfy every clause of q, in the input
// slice's order. The match contract is AND across clauses and OR within a
// set-valued clause: a record matches when it satisfies the free-text query AND
// the kind set AND the label selector AND the capability set AND the plugin type
// — where the kind and capability sets each match when the record satisfies any
// one of their members. An empty clause imposes no constraint, so an empty query
// returns every record.
//
// The free-text query is a case-insensitive substring tested against the record's
// name and its description; a record matches when either contains the query. The
// kind clause is case-insensitive against the canonical kind vocabulary: the flag
// grammar is lowercase (component, feature, recipe, featurerecipe, workload,
// plugin) while a published record stores its canonical capitalized kind
// (Component, FeatureRecipe, Plugin, ...), so the membership test folds case to
// bridge the two. The plugin-type clause is a case-sensitive exact match. The
// selector is evaluated against the record's reconstructed catalog labels; a
// record with no labels cannot satisfy a non-empty selector, and a malformed
// selector is a caller error that propagates rather than silently matching
// nothing.
//
// The selector is parsed once before any record is read, so a malformed one is
// reported whatever the records are — including when the index is empty or the
// other clauses leave no record for the selector to be evaluated against. A
// search answering "nothing matched" for a selector it could not read would be
// reporting a result for a question it never asked.
//
// The input order is preserved and no map is iterated in the result path, so the
// output is byte-stable across runs of the same input.
func filterRecords(records []registryindex.Record, q Query) ([]registryindex.Record, error) {
	if q.Selector != "" {
		if _, err := selector.Parse(q.Selector); err != nil {
			return nil, &SelectorError{Err: err}
		}
	}
	query := strings.ToLower(q.Text)
	var out []registryindex.Record
	for _, rec := range records {
		if query != "" {
			name := strings.ToLower(rec.Name)
			desc := strings.ToLower(rec.Description)
			if !strings.Contains(name, query) && !strings.Contains(desc, query) {
				continue
			}
		}
		if len(q.Kinds) > 0 && !anyKind(q.Kinds, rec.Kind) {
			continue
		}
		if q.Selector != "" {
			var labels types.OrderedMap[string]
			if rec.Labels != nil {
				labels = *rec.Labels
			}
			ok, err := selector.MatchString(q.Selector, labels)
			if err != nil {
				return nil, &SelectorError{Err: err}
			}
			if !ok {
				continue
			}
		}
		if len(q.Capabilities) > 0 && !anyCapability(q.Capabilities, rec.Capabilities) {
			continue
		}
		if q.PluginType != "" && rec.PluginType != q.PluginType {
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
