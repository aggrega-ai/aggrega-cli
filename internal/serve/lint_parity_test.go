// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// The disposition of one field of the shared lint shapes on this surface.
//
// There are exactly two, and the distinction is what makes the tables below able
// to fail. A field is either carried under a named key, in which case the value
// the operation computed must appear there byte for byte, or it is transformed —
// in which case what the transformation is gets stated here and asserted
// separately, because a transformed value cannot be found by looking for the
// value that went in.
type dispositionKind int

const (
	// carried: the value appears under wireKey exactly as the operation computed it.
	carried dispositionKind = iota
	// transformed: the value reaches the surface changed, or reaches it somewhere
	// other than the entry. The reason is recorded and the transformation is
	// asserted by a named test rather than by the value walk.
	transformed
)

// disposition records what becomes of one field of a shared lint shape on this
// surface.
type disposition struct {
	kind dispositionKind
	// wireKey is the key the value appears under, for a carried field.
	wireKey string
	// why states what happens to a transformed field and where that is asserted.
	// It is required for a transformed field, so a field cannot be excused from
	// the value walk without a reason being written down.
	why string
}

// findingDispositions accounts for every field of [lintops.Finding] on the
// daemon's projection of it.
//
// 🔴 The table is held from BOTH ends by the census below: a field of the shared
// shape that this table does not name fails, and a row naming a field the shared
// shape no longer has fails too. That is what makes this an assertion about the
// shared result rather than about the projection — a fact added to the shared
// shape cannot reach this surface unnoticed, and cannot be silently dropped
// either, because the only way to add a row is to decide which of the two
// dispositions it has.
//
// Deriving the expectation from the projection instead is precisely what blinds
// this class: a walk over the keys the projection fills can only ever report that
// the projection fills the keys it fills.
var findingDispositions = map[string]disposition{
	"Severity": {
		kind: transformed,
		why: "translated from the plugin wire's severity vocabulary into this wire's, which is " +
			"asserted below by name",
	},
	"Message": {kind: carried, wireKey: "message"},
	"RuleID":  {kind: carried, wireKey: "code"},
	"ShortCode": {
		kind: transformed,
		why: "read on the entry off the condition the rule identity names, through the lookup that filled it, " +
			"together with the reference that explains it; asserted below by name",
	},
	"Code":           {kind: transformed, why: "collapsed into the same code key as RuleID, which wins; asserted below by name"},
	"Classification": {kind: carried, wireKey: "classification"},
	"Suggest":        {kind: carried, wireKey: "suggest"},
	"Location":       {kind: transformed, why: "projected field by field, accounted for by findingLocationDispositions"},
}

// findingLocationDispositions accounts for every field of
// [lintops.FindingLocation].
var findingLocationDispositions = map[string]disposition{
	"File": {
		kind: transformed,
		why: "carried beside the entries on the file group rather than on each entry, and spelled " +
			"relative to the workspace root; asserted below by name",
	},
	"Line":      {kind: carried, wireKey: "line"},
	"Column":    {kind: carried, wireKey: "column"},
	"EndLine":   {kind: carried, wireKey: "end_line"},
	"EndColumn": {kind: carried, wireKey: "end_column"},
	"KeyPath":   {kind: carried, wireKey: "keyPath"},
}

// summaryDispositions accounts for every count of [lintops.ReportSummary] on the
// `aggrega/lint` answer.
var summaryDispositions = map[string]disposition{
	"EntitiesChecked": {kind: carried, wireKey: "checked"},
	"Findings":        {kind: carried, wireKey: "findings"},
	"Errors":          {kind: carried, wireKey: "errors"},
	"Warnings":        {kind: carried, wireKey: "warnings"},
	"Infos":           {kind: carried, wireKey: "infos"},
}

// censusFields holds a disposition table against the shared shape it accounts
// for, in both directions.
func censusFields(t *testing.T, shape any, table map[string]disposition, name string) {
	t.Helper()

	typ := reflect.TypeOf(shape)
	declared := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		declared[typ.Field(i).Name] = true
	}

	var unaccounted []string
	for field := range declared {
		if _, ok := table[field]; !ok {
			unaccounted = append(unaccounted, field)
		}
	}
	sort.Strings(unaccounted)
	if len(unaccounted) > 0 {
		t.Fatalf("%s carries %s, which the daemon's disposition table does not account for. A fact the "+
			"lint operation computes is owed on every surface that can carry structure, so decide whether "+
			"this surface carries it under a key or transforms it — and if it transforms it, say what the "+
			"transformation is.", name, strings.Join(unaccounted, " and "))
	}

	var stale []string
	for field := range table {
		if !declared[field] {
			stale = append(stale, field)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Fatalf("the daemon's disposition table names %s, which %s no longer carries — a row for a field "+
			"that does not exist is a claim about nothing; delete it", strings.Join(stale, " and "), name)
	}

	for field, d := range table {
		if d.kind == carried && d.wireKey == "" {
			t.Errorf("%s.%s is recorded as carried under no key", name, field)
		}
		if d.kind == transformed && strings.TrimSpace(d.why) == "" {
			t.Errorf("%s.%s is excused from the value walk with no reason recorded", name, field)
		}
	}
}

// populateDistinctly fills every string and int field of v with a value found
// nowhere else, so a value walk over the projection is looking for something only
// that field could have put there.
//
// It populates by REFLECTION rather than from a hand-written literal on purpose:
// a field added to the shared shape acquires a distinctive value without anyone
// remembering to give it one, so the only remaining act is naming its disposition
// — which the census above already forces.
func populateDistinctly(v reflect.Value, seed *int) {
	for i := 0; i < v.NumField(); i++ {
		f := v.Field(i)
		if !f.CanSet() {
			continue
		}
		switch f.Kind() {
		case reflect.String:
			*seed++
			f.SetString(fmt.Sprintf("shared-fact-%d", *seed))
		case reflect.Int:
			*seed++
			f.SetInt(int64(100 + *seed))
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Pointer:
			f.Set(reflect.New(f.Type().Elem()))
			if f.Type().Elem().Kind() == reflect.Struct {
				populateDistinctly(f.Elem(), seed)
			}
		}
	}
}

// distinctlyPopulatedFinding returns a finding carrying a value found nowhere
// else in every field, with the one field whose vocabulary is closed set to a
// declared value.
func distinctlyPopulatedFinding() lintops.Finding {
	var f lintops.Finding
	seed := 0
	populateDistinctly(reflect.ValueOf(&f).Elem(), &seed)
	// Severity is the one closed vocabulary here: an undeclared value maps to the
	// editor's error tier by design, which would make the translation assertion
	// below pass over a value no rule can produce.
	f.Severity = abi.SeverityWarn
	return f
}

// wireFields marshals a diagnostic and decodes it back as keys, which is the only
// reading that sees what a client sees: an omitempty key that is never filled is
// absent from this map rather than present and zero.
func wireFields(t *testing.T, d Diagnostic) map[string]any {
	t.Helper()
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal the projected diagnostic: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("decode the projected diagnostic: %v", err)
	}
	return fields
}

// TestTheDaemonCarriesEveryFactALintFindingComputes is the parity assertion for
// the per-finding facts, and its expectation comes from the shared shape rather
// than from the daemon's projection of it.
//
// It reads every field the lint operation declares on a finding, requires each to
// be accounted for, and then requires each accounted-as-carried field's value to
// appear on the wire under the key the table names. So a projection that fills
// four keys out of seven fails naming the ones it dropped, and a fact added to
// the shared shape fails at the census before any value is compared.
//
// The finding is synthetic, and that is what the assertion needs rather than a
// concession. A projection is a total function over the shared shape: a fixture
// can only ever exercise the fields whatever rules happen to be shipped populate
// today, so a rule that starts filling a field tomorrow would meet a projection
// nothing had ever asserted over. The end-to-end half — that the daemon's answer
// and the lint operation's own report agree over a real run — is
// TestTheDaemonAndTheLintOperationAgreeOverARealRun below.
func TestTheDaemonCarriesEveryFactALintFindingComputes(t *testing.T) {
	t.Parallel()

	censusFields(t, lintops.Finding{}, findingDispositions, "lintops.Finding")
	censusFields(t, lintops.FindingLocation{}, findingLocationDispositions, "lintops.FindingLocation")

	finding := distinctlyPopulatedFinding()
	projected := conventionDiagnostic(finding)
	fields := wireFields(t, projected)
	// The projected entry is logged so a reader of a failure sees the payload the
	// walk below is reading, rather than only the field it objected to.
	if raw, err := json.Marshal(projected); err == nil {
		t.Logf("the wire entry a fully-populated finding projects to: %s", raw)
	}

	shape := reflect.ValueOf(finding)
	walk := func(v reflect.Value, table map[string]disposition, name string) {
		typ := v.Type()
		for i := 0; i < typ.NumField(); i++ {
			d, ok := table[typ.Field(i).Name]
			if !ok || d.kind != carried {
				continue
			}
			want := v.Field(i).Interface()
			got, present := fields[d.wireKey]
			if !present {
				t.Errorf("%s.%s = %v is computed by the lint operation and reaches no key on this "+
					"surface; the table says it is carried as %q. A finding whose remedy, verdict or "+
					"coordinate is dropped is a finding the reader has to diagnose twice.",
					name, typ.Field(i).Name, want, d.wireKey)
				continue
			}
			// The decode turns every number into a float64, so compare through the
			// text rather than through the interface values.
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("%s.%s = %v reaches key %q as %v", name, typ.Field(i).Name, want, d.wireKey, got)
			}
		}
	}
	walk(shape, findingDispositions, "lintops.Finding")
	walk(shape.FieldByName("Location").Elem(), findingLocationDispositions, "lintops.FindingLocation")
}

// TestTheDaemonTransformsWhatItDoesNotCarryVerbatim asserts each transformation
// the table above excuses from the value walk, so an excused field is excused
// into an assertion rather than out of the gate.
func TestTheDaemonTransformsWhatItDoesNotCarryVerbatim(t *testing.T) {
	t.Parallel()

	t.Run("the severity is translated into this wire's vocabulary", func(t *testing.T) {
		t.Parallel()
		for plugin, editor := range map[string]string{
			abi.SeverityHalt: editorSeverityError,
			abi.SeverityWarn: editorSeverityWarning,
			abi.SeverityInfo: editorSeverityInfo,
		} {
			f := distinctlyPopulatedFinding()
			f.Severity = plugin
			if got := conventionDiagnostic(f).Severity; got != editor {
				t.Errorf("a %q finding is published at %q; want %q", plugin, got, editor)
			}
		}
	})

	t.Run("the rule identity wins the code key and the finding's own code is the fallback", func(t *testing.T) {
		t.Parallel()
		f := distinctlyPopulatedFinding()
		if got := conventionDiagnostic(f).Code; got != f.RuleID {
			t.Errorf("code = %q with both identifiers present; the rule identity is the answer (%q)", got, f.RuleID)
		}
		f.RuleID = ""
		if got := conventionDiagnostic(f).Code; got != f.Code {
			t.Errorf("code = %q with no rule identity; want the finding's own code %q", got, f.Code)
		}
	})

	t.Run("the short code is the one the finding carries, standing with its reference", func(t *testing.T) {
		t.Parallel()
		descriptive, ok := cerrors.CodeOf(lintops.ErrNameShape())
		if !ok {
			t.Fatal("the naming-shape condition reports no descriptive code, so no finding here names it")
		}
		for _, ruleID := range []string{descriptive, "rule-of-another-linter"} {
			f := lintops.FindingFrom(abi.Diagnostic{
				Severity: abi.SeverityWarn,
				Message:  "the rule's own sentence about the entity",
				RuleID:   ruleID,
			}, "component.yaml")
			// Non-vacuity: a registered rule's finding that carried no short code
			// would make the comparison below hold two empty strings against each
			// other.
			if ruleID == descriptive && f.ShortCode == "" {
				t.Fatalf("the lint operation filled no short code on a %s finding, so there is nothing to compare", ruleID)
			}
			d := conventionDiagnostic(f)
			if d.ShortCode != f.ShortCode {
				t.Errorf("[%s] the entry quotes the short code %q and the finding carries %q", ruleID, d.ShortCode, f.ShortCode)
			}
			if (d.CodeDescription != nil) != (d.ShortCode != "") {
				t.Errorf("[%s] the reference and the short code do not stand together: shortCode %q, "+
					"codeDescription %+v", ruleID, d.ShortCode, d.CodeDescription)
			}
		}
	})

	t.Run("the file is carried on the group, relative to the workspace root", func(t *testing.T) {
		t.Parallel()
		root, rel := conventionsWorkspace(t, conventionsBadComponent)
		srv := conventionsServer(t, root, false)
		f := distinctlyPopulatedFinding()
		f.Location.File = filepath.Join(root, rel)

		groups := srv.lintFindingsByFile([]lintops.Finding{f})
		if len(groups) != 1 {
			t.Fatalf("one finding grouped into %d file(s): %+v", len(groups), groups)
		}
		if groups[0].File != rel {
			t.Errorf("the group names %q; want the workspace-relative %q", groups[0].File, rel)
		}
	})
}

// TestTheDaemonCarriesEveryCountTheLintOperationComputes is the count half, and
// its expectation likewise comes from the shared summary type.
//
// It drives a real request over a workspace whose entity draws findings and
// compares the answer's counts against the summary the lint operation itself
// produced over the same workspace, count by count, with the census holding the
// comparison against the shared type from both ends. A count added to the shared
// summary therefore fails here rather than going unpublished.
func TestTheDaemonCarriesEveryCountTheLintOperationComputes(t *testing.T) {
	t.Parallel()

	censusFields(t, lintops.ReportSummary{}, summaryDispositions, "lintops.ReportSummary")

	root, rel := conventionsWorkspace(t, conventionsBadComponent)
	srv := conventionsServer(t, root, false)

	res := lintOverTheWire(t, srv, lintParams{Entity: rel})
	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatalf("marshal the lint answer: %v", err)
	}
	var answer map[string]any
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatalf("decode the lint answer: %v", err)
	}

	want := lintCoreReport(t, root, rel).Summary
	// Non-vacuity: a run producing no findings would make every severity count
	// zero on both sides, and the comparison would hold two empty answers against
	// each other.
	if want.Findings == 0 {
		t.Fatalf("the lint operation produced no findings over this fixture, so comparing counts proves nothing")
	}

	summary := reflect.ValueOf(want)
	typ := summary.Type()
	for i := 0; i < typ.NumField(); i++ {
		d, ok := summaryDispositions[typ.Field(i).Name]
		if !ok || d.kind != carried {
			continue
		}
		got, present := answer[d.wireKey]
		if !present {
			t.Errorf("the lint operation counts %s = %d and the answer carries no %q key",
				typ.Field(i).Name, summary.Field(i).Int(), d.wireKey)
			continue
		}
		if fmt.Sprint(got) != fmt.Sprint(summary.Field(i).Int()) {
			t.Errorf("%s: the answer says %v and the lint operation counted %d",
				d.wireKey, got, summary.Field(i).Int())
		}
	}
}

// TestTheDaemonAndTheLintOperationAgreeOverARealRun is the end-to-end half: the
// facts real rules populate are compared, finding for finding, between the
// daemon's answer and the operation's own report over one workspace.
//
// It complements rather than repeats the synthetic assertion. That one covers
// every field the shared shape declares and cannot be produced by any shipped
// rule; this one covers the path a client actually drives, and would catch a
// projection that is correct as a function and wired to the wrong findings.
func TestTheDaemonAndTheLintOperationAgreeOverARealRun(t *testing.T) {
	t.Parallel()

	root, rel := conventionsWorkspace(t, conventionsBadComponent)
	srv := conventionsServer(t, root, false)

	res := lintOverTheWire(t, srv, lintParams{Entity: rel})
	report := lintCoreReport(t, root, rel)
	if len(report.Findings) == 0 {
		t.Fatal("the lint operation produced no findings over this fixture, so there is nothing to compare")
	}

	published := map[string]Diagnostic{}
	for _, group := range res.Files {
		for _, d := range group.Diagnostics {
			published[d.Code+"\x00"+d.Message] = d
		}
	}
	for _, f := range report.Findings {
		identity := f.Identity()
		d, ok := published[identity+"\x00"+f.Message]
		if !ok {
			t.Errorf("the lint operation reported [%s] %s and the daemon's answer carries no such entry",
				identity, f.Message)
			continue
		}
		if d.Classification != f.Classification {
			t.Errorf("[%s] classification: the answer says %q and the operation computed %q",
				identity, d.Classification, f.Classification)
		}
		if d.Suggest != f.Suggest {
			t.Errorf("[%s] suggest: the answer says %q and the operation computed %q",
				identity, d.Suggest, f.Suggest)
		}
		wantKeyPath := ""
		if f.Location != nil {
			wantKeyPath = f.Location.KeyPath
		}
		if d.KeyPath != wantKeyPath {
			t.Errorf("[%s] keyPath: the answer says %q and the operation computed %q",
				identity, d.KeyPath, wantKeyPath)
		}
	}
}
