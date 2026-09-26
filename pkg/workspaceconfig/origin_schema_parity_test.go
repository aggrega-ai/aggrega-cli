// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	schemas "github.com/aggrega-ai/aggrega-schemas"
)

// originCoverageLocation names the file the attributed rows' exemptions are
// written in, so a finding sends a reader to the one place the residue lives
// rather than to whichever check happened to report it.
const originCoverageLocation = "pkg/workspaceconfig/origin_schema_parity_test.go"

// originSchemaRow is one setting the configuration schema declares, at the
// granularity the origin report attributes.
//
// path is the setting's dotted name, spelled as a config file writes it. list
// reports whether the setting is a sequence, which is the axis the report
// attributes differently: a scalar is attributed per value, a list as a whole.
type originSchemaRow struct {
	path string
	list bool
}

// originSchemaPropertyFreeKeywords are the schema keywords the walk below does
// not act on and admits anyway.
//
// Membership turns on one property: a keyword belongs here only if it cannot
// carry a subschema. Each is either an annotation a reader sees or a constraint
// on a scalar, so none of them can hide a declared setting. Every keyword that
// can carry a subschema is either acted on by the walk or refused by it, which
// is what makes the walk's reach a decision rather than an accident.
//
// Admitting a new annotation is a one-line edit with a visible reason. That
// asymmetry is deliberate: a keyword wrongly refused fails loudly and costs a
// line, while a keyword wrongly walked past narrows the coverage check in
// silence and nothing reports it.
var originSchemaPropertyFreeKeywords = map[string]bool{
	"$comment":         true,
	"$id":              true,
	"$schema":          true,
	"const":            true,
	"default":          true,
	"deprecated":       true,
	"description":      true,
	"enum":             true,
	"examples":         true,
	"exclusiveMaximum": true,
	"exclusiveMinimum": true,
	"format":           true,
	"maxItems":         true,
	"maxLength":        true,
	"maximum":          true,
	"minItems":         true,
	"minLength":        true,
	"minimum":          true,
	"multipleOf":       true,
	"pattern":          true,
	"readOnly":         true,
	"required":         true,
	"title":            true,
	"type":             true,
	"uniqueItems":      true,
	"writeOnly":        true,
}

// originSchemaRows walks a configuration schema and returns every setting it
// declares, at the granularity the origin report attributes.
//
// That granularity is the report's own, and it is the whole reason this is not
// a plain recursive descent. A scalar leaf is one setting. An array is one
// setting too and is never descended, because the report attributes a list as a
// unit: a property inside an array's entries is carried by the array's own row
// rather than by a row of its own. A local definition reference is followed,
// because a definition's properties are as real to a reader asking where a
// value came from as a property written inline.
//
// A keyword the walk neither acts on nor recognizes as property-free is
// refused. The walk cannot report a setting it never reached, and nothing else
// would notice that it did not reach one, so the refusal is what keeps a
// schema that grows a composition keyword from quietly narrowing every check
// built on this.
//
// Rows come back in a deterministic order — lexical by property name within
// each object, depth first — so a finding built from them reads the same
// across runs.
func originSchemaRows(schema string) ([]originSchemaRow, error) {
	var root map[string]any
	if err := json.Unmarshal([]byte(schema), &root); err != nil {
		return nil, fmt.Errorf("unmarshal the configuration schema: %w", err)
	}
	defs, _ := root["$defs"].(map[string]any)

	var rows []originSchemaRow
	var walk func(node map[string]any, path string, seen map[string]bool) error

	// resolve follows a chain of local definition references, returning the
	// node the chain lands on. A reference that leaves the document, or one
	// that closes a cycle, is reported rather than followed: either would make
	// the walk's answer depend on how far it happened to get.
	resolve := func(node map[string]any, path string, seen map[string]bool) (map[string]any, map[string]bool, error) {
		for {
			raw, ok := node["$ref"]
			if !ok {
				return node, seen, nil
			}
			ref, ok := raw.(string)
			if !ok {
				return nil, nil, fmt.Errorf("the %q setting carries a reference that is not a string", path)
			}
			const localPrefix = "#/$defs/"
			if !strings.HasPrefix(ref, localPrefix) {
				return nil, nil, fmt.Errorf("the %q setting references %q, which is not a local definition; "+
					"the walk resolves local definitions only, so it cannot report what that reference declares", path, ref)
			}
			if seen[ref] {
				return nil, nil, fmt.Errorf("the %q setting closes a reference cycle at %q", path, ref)
			}
			target, ok := defs[strings.TrimPrefix(ref, localPrefix)].(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("the %q setting references %q, which the schema does not define", path, ref)
			}
			next := map[string]bool{ref: true}
			for k := range seen {
				next[k] = true
			}
			node, seen = target, next
		}
	}

	// describe names a node for a finding. The schema's root carries no path,
	// and a finding about it that said "the setting" would send a reader looking
	// for a property that does not exist.
	describe := func(path string) string {
		if path == "" {
			return "the schema root"
		}
		return fmt.Sprintf("the %q setting", path)
	}

	walk = func(node map[string]any, path string, seen map[string]bool) error {
		node, seen, err := resolve(node, path, seen)
		if err != nil {
			return err
		}

		// Keywords are examined in a fixed order so a schema carrying two the
		// walk refuses is refused with the same finding across runs.
		keywords := make([]string, 0, len(node))
		for keyword := range node {
			keywords = append(keywords, keyword)
		}
		sort.Strings(keywords)
		for _, keyword := range keywords {
			switch keyword {
			case "properties", "items", "$defs", "$ref":
				// Acted on below. Definitions are reached through a reference
				// rather than descended where they are declared, so a
				// definition block itself declares no setting.
			case "additionalProperties":
				// A closed object states that there is nothing further to find.
				// A subschema here, or an open object, admits keys the walk
				// cannot enumerate, so both are refused.
				if closed, ok := node[keyword].(bool); !ok || closed {
					return fmt.Errorf("%s admits additional properties by schema, so the walk "+
						"cannot enumerate what it declares", describe(path))
				}
			default:
				if !originSchemaPropertyFreeKeywords[keyword] {
					return fmt.Errorf("%s carries the %q keyword, which the walk does not act on "+
						"and cannot rule out as hiding a declared setting. Either teach the walk to descend it, "+
						"or add it to the property-free keywords in %s with the reason it cannot carry a subschema",
						describe(path), keyword, originCoverageLocation)
				}
			}
		}

		if _, ok := node["items"]; ok {
			rows = append(rows, originSchemaRow{path: path, list: true})
			return nil
		}
		props, ok := node["properties"].(map[string]any)
		if !ok || len(props) == 0 {
			rows = append(rows, originSchemaRow{path: path})
			return nil
		}
		names := make([]string, 0, len(props))
		for name := range props {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			child, ok := props[name].(map[string]any)
			if !ok {
				return fmt.Errorf("the %q property is not a schema object", name)
			}
			next := name
			if path != "" {
				next = path + "." + name
			}
			if err := walk(child, next, seen); err != nil {
				return err
			}
		}
		return nil
	}

	// The root is walked as a node like any other, so its own keywords are held
	// to the same guard. A composition keyword at the root would otherwise be
	// the one place the refusal above could not see.
	if err := walk(root, "", map[string]bool{}); err != nil {
		return nil, err
	}
	for _, r := range rows {
		if r.path == "" {
			return nil, fmt.Errorf("the configuration schema declares no properties at its root")
		}
	}
	return rows, nil
}

// originAppendCompanionReason is the reason every additive-companion exemption
// carries.
//
// It is named once rather than written per row. The rows differ in which
// setting they excuse and which row accounts for it, never in why, so a copy
// per row would be one sentence stored five times and the copies are what
// drift.
const originAppendCompanionReason = "an additive companion of a base list field, whose key sets that field, " +
	"so a layer contributing only through the companion is attributed the base field's row"

// originExemption declares one setting the configuration schema carries that
// the origin report gives no row of its own, and names the attributed row that
// accounts for it.
//
// carriedBy is required, and the check below verifies it names a live
// attributed row. That requirement is what separates this from a list of names
// to pass over: a skip list grows by anyone adding a name, while an exemption
// can only be written by someone who has established where the setting is in
// fact reported. So an exemption cannot excuse a setting into nowhere, and the
// report's coverage claim stays true — every setting the schema declares at
// this granularity either has its own row or is named as carried by one.
//
// There is deliberately no shape for a setting that is attributable by nothing
// at all. Such a setting would leave a reader asking where a value came from
// with no answer, which is the gap these checks close, so admitting one costs a
// change to this type rather than another row in a table.
type originExemption struct {
	path      string
	carriedBy string
	reason    string
}

// originExemptions is the enumerated bound: every setting the configuration
// schema declares that carries no attributed row of its own.
//
// It is enumerated rather than derived from a rule. A rule — pass over
// anything whose name ends a certain way, pass over anything the resolver
// folds — grows silently as the schema grows, because a setting added tomorrow
// is excused by a decision nobody took about it. A named list cannot do that: a
// setting is excused only by being written here, so the check refuses every
// setting that is neither attributed nor named, and what is excused is readable
// as data instead of hidden inside a passing run.
var originExemptions = []originExemption{
	{"plugins.governance_append", "plugins.governance", originAppendCompanionReason},
	{"plugins.importer_append", "plugins.importer", originAppendCompanionReason},
	{"plugins.linter_append", "plugins.linter", originAppendCompanionReason},
	{"plugins.providers_append", "plugins.providers", originAppendCompanionReason},
	{"workspace.catalog_dirs_append", "workspace.catalog_dirs", originAppendCompanionReason},
}

// originCoverageFindings reports every way the settings a configuration schema
// declares and the rows the origin report attributes disagree.
//
// Both directions are asked, because each is blind to what the other catches. A
// setting with no row is a reader's question the report cannot answer; a row
// naming a setting the schema does not declare prints a key no config file can
// legally carry, and because such a key is only ever printed and never matched,
// nothing else would notice it. The exemptions are held from both ends too: one
// naming a setting the schema does not declare excuses nothing, and one naming
// a setting that does have a row contradicts it.
//
// Each finding names the setting and what to do about it, because the reader of
// a failure is choosing between attributing the setting and stating what
// accounts for it, and a finding that only reports a mismatch leaves them to
// work out which.
//
// Findings are ordered by the schema walk for the unaccounted class and
// lexically for the rest, so a failing check reports the same text across runs.
func originCoverageFindings(rows []originSchemaRow, fields []originField, exemptions []originExemption) []string {
	var findings []string

	attributed := make(map[string]originField, len(fields))
	for _, f := range fields {
		if _, dup := attributed[f.name]; dup {
			findings = append(findings, fmt.Sprintf(
				"the %q setting is attributed twice, so the report prints it twice; delete one row", f.name))
			continue
		}
		attributed[f.name] = f
	}

	excused := make(map[string]originExemption, len(exemptions))
	for _, e := range exemptions {
		if _, dup := excused[e.path]; dup {
			findings = append(findings, fmt.Sprintf(
				"the %q setting is excused twice in %s; delete one row", e.path, originCoverageLocation))
			continue
		}
		excused[e.path] = e
	}

	declared := make(map[string]originSchemaRow, len(rows))
	for _, r := range rows {
		declared[r.path] = r
	}

	for _, r := range rows {
		f, hasRow := attributed[r.path]
		e, isExcused := excused[r.path]
		switch {
		case hasRow && isExcused:
			findings = append(findings, fmt.Sprintf(
				"the %q setting has an attribution row and is also excused in %s. One of the two is wrong. "+
					"If the row stands, delete the exemption: a setting the report attributes is not carried by "+
					"another row, and the bound only shrinks.", r.path, originCoverageLocation))
		case hasRow:
			if f.scalar == r.list {
				kind, want := "a scalar", "a list"
				if r.list {
					kind, want = "a list", "a scalar"
				}
				findings = append(findings, fmt.Sprintf(
					"the schema declares the %q setting as %s and the report attributes it as %s. The two differ "+
						"in behaviour, not only in rendering: a list row is also set through the field's "+
						"additive companion, so a layer writing only the companion is attributed the row and "+
						"positioned at the companion's key.", r.path, kind, want))
			}
		case isExcused:
			if strings.TrimSpace(e.carriedBy) == "" {
				findings = append(findings, fmt.Sprintf(
					"the %q setting is excused in %s and names no row that carries it, so a reader asking where "+
						"its value came from has no answer. Name the attributed row that accounts for it.",
					r.path, originCoverageLocation))
				break
			}
			if _, ok := attributed[e.carriedBy]; !ok {
				findings = append(findings, fmt.Sprintf(
					"the %q setting is excused in %s as carried by %q, which the report does not attribute, so "+
						"the exemption points at nothing. Either attribute %q or name the row that does account "+
						"for %q.", r.path, originCoverageLocation, e.carriedBy, e.carriedBy, r.path))
			}
			if len(strings.TrimSpace(e.reason)) < 40 {
				findings = append(findings, fmt.Sprintf(
					"the %q setting is excused in %s with too little to check: %q", r.path, originCoverageLocation, e.reason))
			}
			if strings.HasSuffix(strings.TrimSpace(e.reason), ".") {
				findings = append(findings, fmt.Sprintf(
					"the %q setting's exemption reason reads as a sentence rather than a clause a finding embeds: %q",
					r.path, e.reason))
			}
		default:
			findings = append(findings, fmt.Sprintf(
				"the configuration schema declares the %q setting and the origin report accounts for it nowhere, "+
					"so a reader asking where its value came from is told nothing. Either give it a row in "+
					"originFields, or name it in the exemptions in %s with the attributed row that carries it.",
				r.path, originCoverageLocation))
		}
	}

	stale := make([]string, 0, len(fields))
	for name := range attributed {
		if _, ok := declared[name]; !ok {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	for _, name := range stale {
		findings = append(findings, fmt.Sprintf(
			"the origin report attributes %q, which the configuration schema declares nowhere at the granularity "+
				"the report attributes. Either the setting was renamed and the row is stale — update it — or the "+
				"row names a property inside a list, which that list's own row already carries.", name))
	}

	staleExcused := make([]string, 0, len(exemptions))
	for path := range excused {
		if _, ok := declared[path]; !ok {
			staleExcused = append(staleExcused, path)
		}
	}
	sort.Strings(staleExcused)
	for _, path := range staleExcused {
		findings = append(findings, fmt.Sprintf(
			"the exemptions in %s excuse %q, which the configuration schema declares nowhere. It excuses nothing "+
				"and cannot be repaired by whoever reads it, because there is no setting to attribute — delete the row.",
			originCoverageLocation, path))
	}

	return findings
}

// originCoverageReach states what the coverage check asserts and what it does
// not, from the schema and the exemptions at the moment of the call.
//
// It reports its own reach rather than only a verdict, because a check that
// goes quiet about what it did not assert is indistinguishable from one that
// asserted everything. The figures are derived here rather than written down,
// so none of them can rot into agreement with a number somebody recorded.
//
// The bound it names is the report's granularity inherited deliberately. A list
// is attributed as a unit, so a property inside a list's entries is carried by
// that list's row and gets no assertion of its own: a new property added to a
// registry entry or a plugin attachment arrives with no row and this check
// stays green. Reporting that is the difference between a bound and a hole
// nobody mentioned.
func originCoverageReach(rows []originSchemaRow, exemptions []originExemption) string {
	lists := 0
	for _, r := range rows {
		if r.list {
			lists++
		}
	}
	return fmt.Sprintf("the configuration schema declares %d settings at the granularity the origin report "+
		"attributes, %d of them lists; %d are excused in %s as carried by another row. This check asserts NOTHING "+
		"about a property inside a list's entries: each of those %d lists is attributed as a unit, so a property "+
		"added inside one carries no row of its own and nothing here reports it",
		len(rows), lists, len(exemptions), originCoverageLocation, lists)
}

// originSchemaRowsOrFail walks the embedded configuration schema, failing the
// test rather than returning an error.
func originSchemaRowsOrFail(t *testing.T) []originSchemaRow {
	t.Helper()
	rows, err := originSchemaRows(schemas.AggregaConfigV1alpha1)
	if err != nil {
		t.Fatalf("walk the configuration schema: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("the configuration schema yielded no settings, so every check built on this walk would pass over nothing")
	}
	return rows
}

// TestEverySchemaSettingIsAttributedOrExempt is the coverage guard: every
// setting the configuration schema declares either has an attribution row in
// the origin report or is named in the exemptions with the row that carries it,
// and no row or exemption names a setting the schema does not declare.
//
// It fails rather than reports. A passing Go package prints nothing on any
// channel, so a guard that only reported would be inert in a routine run
// whatever it found. The reach travels on two channels and neither substitutes
// for the other: folded into the failure text, which is what the reader of a
// red sees, and logged, which the framework shows under -v and whenever
// anything in this package fails.
func TestEverySchemaSettingIsAttributedOrExempt(t *testing.T) {
	t.Parallel()
	rows := originSchemaRowsOrFail(t)
	reach := originCoverageReach(rows, originExemptions)
	t.Log(reach)

	if findings := originCoverageFindings(rows, originFields(), originExemptions); len(findings) != 0 {
		t.Fatalf("%d of %d settings the configuration schema declares are not accounted for by the origin report:\n  %s\n%s",
			len(findings), len(rows), strings.Join(findings, "\n  "), reach)
	}
}

// TestTheSchemaWalkReachesTheSettingsTheCoverageGuardRestsOn pins that the walk
// above actually reaches the shapes the coverage guard depends on, so a passing
// guard cannot be a guard passing over a walk that found nothing interesting.
//
// Three shapes, each reachable only by a different part of the walk: a setting
// nested inside a plain object, a setting reachable only by following a local
// definition reference, and a list. A walk that silently stopped at any of them
// would leave the coverage guard green.
func TestTheSchemaWalkReachesTheSettingsTheCoverageGuardRestsOn(t *testing.T) {
	t.Parallel()
	rows := originSchemaRowsOrFail(t)
	byPath := map[string]originSchemaRow{}
	for _, r := range rows {
		byPath[r.path] = r
	}

	for _, want := range []struct {
		path string
		list bool
		why  string
	}{
		{"compiled_module_cache.max_bytes", false, "a scalar nested inside a plain object"},
		{"signing.fulcio_url", false, "a scalar reachable only through a local definition reference"},
		{"registries", true, "a list, which the walk records without descending"},
		{"workspace.catalog_dirs_append", true, "an additive companion, which the exemptions rest on"},
	} {
		got, ok := byPath[want.path]
		if !ok {
			t.Errorf("the walk did not reach %q (%s), so the coverage guard asserts nothing about it", want.path, want.why)
			continue
		}
		if got.list != want.list {
			t.Errorf("the walk recorded %q with list=%v; want %v (%s)", want.path, got.list, want.list, want.why)
		}
	}

	// The walk must not descend a list. A property inside one is carried by
	// that list's row, and a row of its own would claim per-entry provenance
	// the report does not track. Asserted against the list rows the walk itself
	// found, so the assertion cannot go quiet by the lists being renamed.
	lists := 0
	for _, outer := range rows {
		if !outer.list {
			continue
		}
		lists++
		for _, inner := range rows {
			if strings.HasPrefix(inner.path, outer.path+".") {
				t.Errorf("the walk recorded %q inside the %q list; a list is attributed as a unit",
					inner.path, outer.path)
			}
		}
	}
	if lists == 0 {
		t.Fatal("the walk found no list, so the no-descent assertion above proved nothing")
	}
}

// withExemption returns base with e replacing the entry for the same setting,
// or appended when base has none.
//
// Replacing rather than appending is what lets a defective exemption be driven
// against a setting that legitimately carries one: appending would report a
// duplicate row and the check would never reach the fault under test.
func withExemption(base []originExemption, e originExemption) []originExemption {
	out := make([]originExemption, 0, len(base)+1)
	replaced := false
	for _, b := range base {
		if b.path == e.path {
			out, replaced = append(out, e), true
			continue
		}
		out = append(out, b)
	}
	if !replaced {
		out = append(out, e)
	}
	return out
}

// TestOriginCoverageRefusesASettingWithNeitherARowNorAnExemption drives the
// class the guard exists for: a setting the schema declares that nothing
// accounts for.
//
// The setting is planted in the walk's output rather than in the schema, which
// is what makes the failure drivable at all — the embedded schema is a shared
// contract and a test cannot edit it. The guard reads the walk's rows, so a
// planted row reaches it by exactly the path a real new property would.
func TestOriginCoverageRefusesASettingWithNeitherARowNorAnExemption(t *testing.T) {
	t.Parallel()
	rows := append(originSchemaRowsOrFail(t), originSchemaRow{path: "build.planted_setting"})

	findings := originCoverageFindings(rows, originFields(), originExemptions)
	if len(findings) != 1 {
		t.Fatalf("want exactly the planted setting reported; got %d findings:\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
	// Logged so the refusal this guard rests on is readable from a passing run
	// rather than only from a failing one.
	t.Logf("the planted setting is refused with: %s", findings[0])
	if !strings.Contains(findings[0], `"build.planted_setting"`) {
		t.Errorf("the finding does not name the planted setting: %q", findings[0])
	}
	if !strings.Contains(findings[0], "originFields") || !strings.Contains(findings[0], originCoverageLocation) {
		t.Errorf("the finding does not tell a reader both ways to account for the setting: %q", findings[0])
	}
}

// TestOriginCoverageRefusesAStaleAttributedRow drives the direction a coverage
// walk cannot see: a row naming a setting the schema does not declare. Such a
// key is only ever printed, never matched, so nothing else in the suite
// notices it.
func TestOriginCoverageRefusesAStaleAttributedRow(t *testing.T) {
	t.Parallel()
	rows := originSchemaRowsOrFail(t)
	fields := append(originFields(), originField{
		name:    "build.renamed_away",
		scalar:  true,
		display: func(Config) string { return "" },
	})

	findings := originCoverageFindings(rows, fields, originExemptions)
	if len(findings) != 1 || !strings.Contains(findings[0], `"build.renamed_away"`) {
		t.Fatalf("want the stale row reported once and named; got %d findings:\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
}

// TestOriginCoverageHoldsTheExemptionsFromBothEnds drives every way an
// exemption can be wrong: naming a setting the schema does not declare, naming
// a setting that already has a row, carrying no row that accounts for it,
// naming a row the report does not attribute, and carrying a placeholder
// reason.
//
// Each row is driven on its own, because the findings are produced in sequence
// and a fixture carrying two faults would keep passing once one of them was
// reported by a different clause.
func TestOriginCoverageHoldsTheExemptionsFromBothEnds(t *testing.T) {
	t.Parallel()
	rows := originSchemaRowsOrFail(t)
	fields := originFields()

	for _, tc := range []struct {
		name    string
		exempt  originExemption
		wantSub string
	}{
		{
			name:    "a setting the schema does not declare",
			exempt:  originExemption{"build.no_such_setting", "build.yaml_indent", originAppendCompanionReason},
			wantSub: "declares nowhere",
		},
		{
			name:    "a setting that already has a row",
			exempt:  originExemption{"build.yaml_indent", "offline", originAppendCompanionReason},
			wantSub: "has an attribution row and is also excused",
		},
		{
			name:    "no row that accounts for it",
			exempt:  originExemption{"workspace.catalog_dirs_append", "", originAppendCompanionReason},
			wantSub: "names no row that carries it",
		},
		{
			name:    "a row the report does not attribute",
			exempt:  originExemption{"workspace.catalog_dirs_append", "build.not_attributed", originAppendCompanionReason},
			wantSub: "points at nothing",
		},
		{
			name:    "a placeholder reason",
			exempt:  originExemption{"workspace.catalog_dirs_append", "workspace.catalog_dirs", "TODO"},
			wantSub: "too little to check",
		},
		{
			name:    "a reason reading as a sentence",
			exempt:  originExemption{"workspace.catalog_dirs_append", "workspace.catalog_dirs", originAppendCompanionReason + "."},
			wantSub: "reads as a sentence rather than a clause",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findings := originCoverageFindings(rows, fields, withExemption(originExemptions, tc.exempt))
			joined := strings.Join(findings, "\n  ")
			if len(findings) == 0 {
				t.Fatalf("the exemption %+v was accepted; want it refused", tc.exempt)
			}
			if !strings.Contains(joined, tc.wantSub) {
				t.Errorf("no finding reports %q; got:\n  %s", tc.wantSub, joined)
			}
		})
	}
}

// TestOriginCoverageRefusesAKindDisagreement pins that the scalar-or-list axis
// is checked and not only the name. The two differ in behaviour rather than in
// rendering alone, so a row bound to the wrong kind resolves the wrong key
// positions and is attributed by the wrong layers.
func TestOriginCoverageRefusesAKindDisagreement(t *testing.T) {
	t.Parallel()
	rows := originSchemaRowsOrFail(t)

	fields := make([]originField, 0, len(originFields()))
	flipped := false
	for _, f := range originFields() {
		if f.name == "registries" {
			f.scalar = true
			flipped = true
		}
		fields = append(fields, f)
	}
	if !flipped {
		t.Fatal("precondition: no registries row to flip, so this test would prove nothing")
	}

	findings := originCoverageFindings(rows, fields, originExemptions)
	if len(findings) != 1 || !strings.Contains(findings[0], `"registries"`) {
		t.Fatalf("want the kind disagreement reported once and named; got %d findings:\n  %s",
			len(findings), strings.Join(findings, "\n  "))
	}
	if !strings.Contains(findings[0], "additive companion") {
		t.Errorf("the finding does not say what the kind decides: %q", findings[0])
	}
}

// TestTheSchemaWalkRefusesWhatItCannotEnumerate pins the walk's own guard. A
// keyword that can carry a subschema, and an object left open, each hide
// settings the walk would otherwise pass over in silence — leaving every check
// built on it green over a narrower surface than it claims.
func TestTheSchemaWalkRefusesWhatItCannotEnumerate(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		schema  string
		wantSub string
	}{
		{
			name:    "a composition keyword",
			schema:  `{"properties":{"build":{"type":"object","oneOf":[{"properties":{"a":{"type":"string"}}}]}}}`,
			wantSub: `"oneOf" keyword`,
		},
		{
			name:    "a pattern-keyed property map",
			schema:  `{"properties":{"build":{"type":"object","patternProperties":{"^x":{"type":"string"}}}}}`,
			wantSub: `"patternProperties" keyword`,
		},
		{
			name:    "an object left open by schema",
			schema:  `{"properties":{"build":{"type":"object","additionalProperties":{"type":"string"}}}}`,
			wantSub: "admits additional properties by schema",
		},
		{
			name:    "an object left open by default",
			schema:  `{"properties":{"build":{"type":"object","additionalProperties":true}}}`,
			wantSub: "admits additional properties by schema",
		},
		{
			name:    "a reference the schema does not define",
			schema:  `{"properties":{"build":{"$ref":"#/$defs/Missing"}},"$defs":{}}`,
			wantSub: "which the schema does not define",
		},
		{
			name:    "a reference that leaves the document",
			schema:  `{"properties":{"build":{"$ref":"other.json#/$defs/Thing"}}}`,
			wantSub: "not a local definition",
		},
		{
			name:    "a reference cycle",
			schema:  `{"properties":{"build":{"$ref":"#/$defs/A"}},"$defs":{"A":{"$ref":"#/$defs/B"},"B":{"$ref":"#/$defs/A"}}}`,
			wantSub: "closes a reference cycle",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rows, err := originSchemaRows(tc.schema)
			if err == nil {
				t.Fatalf("the walk accepted a schema it cannot enumerate and returned %d rows", len(rows))
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Errorf("the refusal does not report %q: %v", tc.wantSub, err)
			}
		})
	}
}

// TestTheSchemaWalkAcceptsAClosedObject is the positive control for the guard
// above: a refusal that fired on every input would prove nothing about the
// inputs it was meant to refuse.
func TestTheSchemaWalkAcceptsAClosedObject(t *testing.T) {
	t.Parallel()
	const schema = `{"properties":{"build":{"type":"object","additionalProperties":false,` +
		`"description":"d","properties":{"yaml_indent":{"type":"integer","default":2,"minimum":0}}}}}`

	rows, err := originSchemaRows(schema)
	if err != nil {
		t.Fatalf("the walk refused a closed object it should enumerate: %v", err)
	}
	if len(rows) != 1 || rows[0].path != "build.yaml_indent" || rows[0].list {
		t.Errorf("walk = %+v; want one scalar setting build.yaml_indent", rows)
	}
}
