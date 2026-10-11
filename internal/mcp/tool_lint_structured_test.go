// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The lint tool's structured result. The sibling parity file proves this
// adapter's two writer streams equal a direct core call; these tests prove the
// other half — that the tool hands back the findings as DATA, each carrying the
// rule that produced it and where it points, rather than only as the text those
// streams spell out.
//
// The oracle is the same one the parity file uses: a direct lintops call over the
// same resolved inputs, with its own collector. Comparing reports rather than
// prose is what makes "the same findings, one field vocabulary" an assertion
// instead of a claim.

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// lintStructuredCleanComponent is an entity the conventions rules pass: a
// Component whose metadata.name carries the three-segment form the name-shape
// rule documents, in the directory that form mirrors.
//
// It is the positive control for the no-over-fire assertion, and it has to be a
// genuinely clean entity rather than a quieter violation — a fixture that merely
// broke a different rule would report findings and the assertion would then be
// measuring the wrong thing.
const lintStructuredCleanComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: example/compute/api-server
  version: 1.0.0
  labels:
    component.aggrega.ai/category: compute
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`

// lintStructuredWorkspace stands a workspace holding one entity at rel and
// returns its root.
func lintStructuredWorkspace(t *testing.T, rel, content string) string {
	t.Helper()
	root := t.TempDir()
	writeLintParityFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), "workspace: {}\n")
	writeLintParityFixture(t, filepath.Join(root, rel), content)
	return root
}

// lintReportFromTool invokes the lint tool over entity in the workspace at root
// and returns the tool result together with its decoded structured payload. A
// result carrying no structured payload decodes to the zero report, and the
// caller distinguishes the two by reading res.StructuredContent itself.
func lintReportFromTool(t *testing.T, root, entity string) (*callToolResult, lintops.Report) {
	t.Helper()
	s := newTestServer(t)
	s.launchRoot = root
	res := callTool(t, s, "lint", lintArgs{Entity: entity})
	var report lintops.Report
	if len(res.StructuredContent) > 0 {
		if err := json.Unmarshal(res.StructuredContent, &report); err != nil {
			t.Fatalf("unmarshal the lint tool's structured payload: %v\npayload: %s", err, res.StructuredContent)
		}
	}
	return res, report
}

// lintReportFromCore is the oracle: the lint core run directly over the same
// entity, with its own collector, so the comparison is against what the
// operation produced rather than against a second expectation written by hand.
func lintReportFromCore(t *testing.T, root, entity string) lintops.Report {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	collect := &lintops.FindingCollector{}
	var out, diag bytes.Buffer
	if rerr := lintops.RunEntity(t.Context(), lintops.Config{
		EntityPath:     filepath.Join(root, entity),
		PolicyResolver: func(_ string, ws bool) bool { return ws },
		Workspace:      rw,
		Out:            &out,
		DiagOut:        &diag,
		Collect:        collect,
	}); rerr != nil {
		t.Fatalf("core oracle refused the entity: %v\n%s", rerr, diag.String())
	}
	if !collect.Reached() {
		t.Fatal("the core oracle never reached reporting, so there is nothing to compare against")
	}
	return collect.Report()
}

// TestLintToolCarriesEachFindingsRuleIdentityAndPosition is the assertion the
// tool surface could not satisfy while it re-spliced its two output streams into
// one text blob: a caller reads which rule objected and which file the objection
// is about from named keys, never by parsing prose.
//
// Both halves are asserted, and the second is what makes the first mean
// something. A rule identity folded into the message would satisfy a reading of
// "the caller can tell which rule fired" while leaving the caller matching text
// to recover it, which is the whole reason the key exists.
func TestLintToolCarriesEachFindingsRuleIdentityAndPosition(t *testing.T) {
	t.Parallel()
	const entity = "component.yaml"
	root := lintStructuredWorkspace(t, entity, lintParityBadComponent)

	res, report := lintReportFromTool(t, root, entity)

	// Default mode is advisory, so a convention-breaking entity is reported and
	// the call still succeeds. This is the same statement the severity counts
	// below make, asserted at the level a client branches on.
	if res.IsError {
		t.Fatalf("a conventions finding failed the call; default mode is advisory: %s", res.Content[0].Text)
	}
	if len(res.StructuredContent) == 0 {
		t.Fatal("the tool returned no structured payload, so no finding could carry a rule identity or a position")
	}
	if len(report.Findings) == 0 {
		t.Fatal("precondition: the fixture produced no finding, so the assertions below would be vacuous")
	}

	var found *lintops.Finding
	var sawRules []string
	for i := range report.Findings {
		sawRules = append(sawRules, report.Findings[i].RuleID)
		if report.Findings[i].RuleID == "name-shape" {
			found = &report.Findings[i]
		}
	}
	if found == nil {
		t.Fatalf("no finding names the naming-shape rule; the rule identities on the wire were %v", sawRules)
	}

	// The position. Every finding locates at least the file it concerns, whether
	// or not the producing rule pinned a line, so the file is what this asserts.
	switch {
	case found.Location == nil:
		t.Error("the finding carries no location, so a caller cannot tell which file it is about")
	case found.Location.File == "":
		t.Error("the finding's location names no file")
	case filepath.Base(found.Location.File) != entity:
		t.Errorf("the finding points at %q; want the entity that was linted (%q)",
			found.Location.File, entity)
	}

	// Severity travels as its own key and the run does not halt.
	if found.Severity != "warn" {
		t.Errorf("Severity = %q; want the advisory severity", found.Severity)
	}
	if report.Summary.Errors != 0 {
		t.Errorf("Summary.Errors = %d; a default-mode conventions run reports none", report.Summary.Errors)
	}
	if report.Summary.Warnings == 0 {
		t.Error("Summary.Warnings = 0 while a warning finding is in the list; the summary disagrees with what it summarizes")
	}

	// The rule identity is not recoverable from the message, which is what makes
	// the key load-bearing rather than decorative.
	if strings.Contains(found.Message, found.RuleID) {
		t.Errorf("the rule identity is embedded in the message %q; the key is the only place it travels", found.Message)
	}

	// The short code of the condition the rule names travels as a key of its own
	// on the structured answer, and is the code printed at the head of the text
	// answer's row for the same finding. It is read off the condition through its
	// exported accessor, never written here as a literal, and off the payload as a
	// client decodes it, so a renamed key fails here rather than round-tripping
	// through the report type.
	short, ok := cerrors.ShortCodeOf(lintops.ErrNameShape())
	if !ok || short == "" {
		t.Fatal("the naming-shape condition reports no short code, so there is nothing to look for")
	}
	if found.ShortCode != short {
		t.Errorf("ShortCode = %q; the condition the naming-shape rule names declares %q", found.ShortCode, short)
	}
	var wire struct {
		Findings []map[string]any `json:"findings"`
	}
	if err := json.Unmarshal(res.StructuredContent, &wire); err != nil {
		t.Fatalf("decode the structured payload: %v\npayload: %s", err, res.StructuredContent)
	}
	keyed := false
	for _, entry := range wire.Findings {
		if entry["ruleId"] == found.RuleID && entry["shortCode"] == short {
			keyed = true
		}
	}
	if !keyed {
		t.Errorf("no finding on the structured payload carries shortCode %q beside ruleId %q: %s",
			short, found.RuleID, res.StructuredContent)
	}
	if !strings.Contains(res.Content[0].Text, short+" "+found.RuleID+": ") {
		t.Errorf("the text answer carries no row printing the short code %q the structured answer carries "+
			"ahead of %q: %q", short, found.RuleID, res.Content[0].Text)
	}

	// The human rendering still carries the same finding, so the two renderings
	// describe one set of findings rather than two independently assembled ones.
	if !strings.Contains(res.Content[0].Text, "name-shape") {
		t.Errorf("the text block dropped the advisory the structured payload reports: %q", res.Content[0].Text)
	}

	// The oracle: byte-for-byte the report a direct core call produces.
	if oracle := lintReportFromCore(t, root, entity); !reflect.DeepEqual(report, oracle) {
		t.Errorf("the tool's report and the core's diverged\n tool:  %+v\n core:  %+v", report, oracle)
	}
}

// TestLintToolStructuredResultDoesNotOverFire is the other direction. A surface
// that reported a finding for every entity would satisfy the test above and be
// useless, so a conventions-clean entity has to come back clean — and come back
// with a payload, because "the rules ran and found nothing" is a different answer
// from "no rules ran".
func TestLintToolStructuredResultDoesNotOverFire(t *testing.T) {
	t.Parallel()
	const entity = "example/compute/api-server.yaml"
	root := lintStructuredWorkspace(t, entity, lintStructuredCleanComponent)

	res, report := lintReportFromTool(t, root, entity)

	if res.IsError {
		t.Fatalf("a clean entity failed the call: %s", res.Content[0].Text)
	}
	if len(res.StructuredContent) == 0 {
		t.Fatal("a clean run returned no payload; a run that reached reporting and found nothing must say so")
	}
	if len(report.Findings) != 0 {
		t.Errorf("a conventions-clean entity produced %d finding(s): %+v", len(report.Findings), report.Findings)
	}
	if report.Summary.Findings != 0 || report.Summary.Warnings != 0 || report.Summary.Errors != 0 {
		t.Errorf("the summary reports findings over a clean run: %+v", report.Summary)
	}
	if report.Summary.EntitiesChecked != 1 {
		t.Errorf("Summary.EntitiesChecked = %d; want 1, so a clean answer is distinguishable from an empty one",
			report.Summary.EntitiesChecked)
	}
}

// TestLintToolEmitsNoStructuredResultWhenNoRuleRan pins the branch that keeps an
// empty report from reading as a clean lint.
//
// An operation that fails before any rule executes has produced no findings and
// also established nothing, so it emits no payload at all. An empty report there
// would assert a clean entity over a lint that never happened — and the two are
// indistinguishable to a caller that only counts findings, which is why the
// branch exists and why the absence is asserted rather than assumed.
func TestLintToolEmitsNoStructuredResultWhenNoRuleRan(t *testing.T) {
	t.Parallel()
	// The workspace holds a real entity, so the miss below is the named path
	// failing to resolve rather than a workspace with nothing in it.
	root := lintStructuredWorkspace(t, "component.yaml", lintParityBadComponent)

	res, _ := lintReportFromTool(t, root, "no-such-entity.yaml")

	if !res.IsError {
		t.Fatalf("an unreadable entity was reported as a success: %s", res.Content[0].Text)
	}
	if len(res.StructuredContent) != 0 {
		t.Errorf("a run that executed no rule emitted a structured payload (%s); an empty report there "+
			"claims a clean lint over a lint that never happened", res.StructuredContent)
	}
}

// TestLintToolCollectorLeavesTheOutputStreamsAlone is the standing proof that
// collection is additive at THIS seam.
//
// The sibling parity file compares the adapter's streams against the core's over
// a nil collector's absence of effect; this asserts the property directly — the
// same run with and without a collector writes the same bytes — so a future
// change that made collection observable in the output fails here, naming the
// property, rather than surfacing as a puzzling parity failure.
func TestLintToolCollectorLeavesTheOutputStreamsAlone(t *testing.T) {
	t.Parallel()
	const entity = "component.yaml"
	root := lintStructuredWorkspace(t, entity, lintParityBadComponent)

	run := func(collect *lintops.FindingCollector) (string, string) {
		t.Helper()
		s := newTestServer(t)
		s.launchRoot = root
		var out, diag bytes.Buffer
		if err := s.lintInProcess(context.Background(),
			lintRequest{entity: entity, collect: collect}, &out, &diag); err != nil {
			t.Fatalf("lint refused the entity: %v\n%s", err, diag.String())
		}
		return out.String(), diag.String()
	}

	withoutOut, withoutDiag := run(nil)
	collect := &lintops.FindingCollector{}
	withOut, withDiag := run(collect)

	if withoutOut != withOut {
		t.Errorf("threading a collector changed the summary stream\n without: %q\n with:    %q", withoutOut, withOut)
	}
	if withoutDiag != withDiag {
		t.Errorf("threading a collector changed the diagnostic stream\n without: %q\n with:    %q", withoutDiag, withDiag)
	}
	if withDiag == "" {
		t.Fatal("the fixture produced no diagnostic output, so the comparison above compared two empty strings")
	}
	if !collect.Reached() {
		t.Error("the collector gathered nothing while the run reported findings; the seam is not threaded")
	}
}
