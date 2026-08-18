// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/yamlpos"
)

// parseYAML returns the parsed yaml.Node for the supplied content.
// Helper for the position-walker unit tests so each test case stays
// focused on the assertion rather than the parse boilerplate.
func parseYAML(t *testing.T, content string) *yaml.Node {
	t.Helper()
	var node yaml.Node
	if err := yaml.Unmarshal([]byte(content), &node); err != nil {
		t.Fatalf("parse yaml: %v", err)
	}
	return &node
}

// lineIndexFor builds a UTF-16 LineIndex over content for the
// diagnostic position-recovery tests, matching the daemon's default
// position encoding.
func lineIndexFor(content string) yamlpos.LineIndex {
	return yamlpos.BuildLineIndexWithEncoding([]byte(content), yamlpos.PositionEncodingUTF16)
}

// The walker's own unit tests live with the walker, at
// pkg/yamlpos/fieldpath_test.go. What remains here exercises the daemon's
// use of it: that a finding's Field becomes a positioned diagnostic and
// degrades cleanly when it cannot.

func TestResolverViolationToDiagnostic_FieldMatchesYAML(t *testing.T) {
	t.Parallel()
	content := `spec:
  parameters:
    port:
      type: integer
    host:
      type: string
`
	doc := parseYAML(t, content)
	ve := &resolver.ResolverError{
		Entity:  "Aggregate",
		Field:   ".spec.parameters.host",
		Message: "parameter host: expected string, got integer",
		Err:     resolver.ErrParameterContractViolation(),
	}
	diag := resolverFindingToDiagnostic(ve, "error", doc, lineIndexFor(content))
	if diag.Line != 5 {
		t.Errorf("Line = %d; want 5 (host key line)", diag.Line)
	}
	// host key spans columns 5-8 (1-based, end exclusive past "host").
	if diag.Column != 5 || diag.EndColumn != 9 || diag.EndLine != 5 {
		t.Errorf("range = %d:%d-%d:%d; want 5:5-5:9", diag.Line, diag.Column, diag.EndLine, diag.EndColumn)
	}
	if diag.Severity != "error" {
		t.Errorf("Severity = %q; want %q", diag.Severity, "error")
	}
	if diag.Message != ve.Message {
		t.Errorf("Message = %q; want %q", diag.Message, ve.Message)
	}
}

func TestResolverViolationToDiagnostic_FieldMissingFallsBackToNeighborhood(t *testing.T) {
	t.Parallel()
	content := `spec:
  components: {}
`
	doc := parseYAML(t, content)
	ve := &resolver.ResolverError{
		Entity:  "Aggregate",
		Field:   ".spec.parameters.missing",
		Message: "parameter missing: required",
		Err:     resolver.ErrParameterMissing(),
	}
	// .spec matches but .parameters does not, so the walker falls back
	// to the deepest reached key (.spec at line 1, column 1).
	diag := resolverFindingToDiagnostic(ve, "error", doc, lineIndexFor(content))
	if diag.Line != 1 || diag.Column != 1 {
		t.Errorf("expected fall-back to .spec at 1:1; got %d:%d", diag.Line, diag.Column)
	}
}

// TestHandleValidateEmits1002WhenCascadeExhausted exercises the
// release-not-resolved emission path: when the disambiguation
// cascade attempted to find a Release and could not (candidates
// exist but none selected), handleValidate emits a 1002
// errReleaseNotResolved envelope rather than returning Tier-1
// diagnostics. The envelope's Message is the one-line cascade
// summary; the Data field carries the validated file path and the
// multi-line cascade trace.
func TestHandleValidateEmits1002WhenCascadeExhausted(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)
	// Synthesise the cascade-exhausted state: candidates found but
	// nothing selected (no marker, no config, multiple Release files).
	srv.cascadeAttempt = cascadeAttempt{
		ReleaseCandidatesFound: 2,
	}
	// activeRelease left nil — the cascade-exhausted branch fires.

	file := filepath.Join(tmp, "ambiguous.yaml")
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    file,
		Content: "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: ambiguous\n  version: \"1.0.0\"\nspec:\n  components: {}\n",
	}))
	resp := runOneRequest(t, srv, &in, &out)

	if resp.Error == nil {
		t.Fatalf("expected 1002 envelope; got no error")
	}
	if resp.Error.Code != errReleaseNotResolved {
		t.Errorf("Code = %d; want %d (errReleaseNotResolved)", resp.Error.Code, errReleaseNotResolved)
	}
	if resp.Error.Data == nil {
		t.Fatalf("expected non-nil Data carrying cascade_attempt + file")
	}
	if resp.Error.Data.File != file {
		t.Errorf("Data.File = %q; want %q", resp.Error.Data.File, file)
	}
	if resp.Error.Data.CascadeAttempt == "" {
		t.Errorf("Data.CascadeAttempt empty; want multi-line trace")
	}
}

// TestHandleValidateNoReleaseScaffoldReturnsTier1 exercises the
// backward-compatibility path: workspaces without any Release files
// (single-Aggregate scaffolds, catalog-only repositories, scratch
// directories) return Tier-1 diagnostics only — no 1002, no spurious
// toasts. The cascade-attempted-but-found-nothing state has
// ReleaseCandidatesFound == 0 and the handler skips the 1002
// branch.
func TestHandleValidateNoReleaseScaffoldReturnsTier1(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)
	// Cascade-attempted-but-empty state: no candidates → no 1002.
	srv.cascadeAttempt = cascadeAttempt{
		ReleaseCandidatesFound: 0,
	}

	file := filepath.Join(tmp, "scaffold.yaml")
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    file,
		Content: "apiVersion: aggrega.ai/v1alpha1\nkind: Aggregate\nmetadata:\n  name: scaffold\n  version: \"1.0.0\"\nspec:\n  components: {}\n",
	}))
	resp := runOneRequest(t, srv, &in, &out)

	if resp.Error != nil {
		t.Fatalf("expected no error for no-Release scaffold; got %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("expected zero diagnostics for well-formed scaffold; got %d", len(result.Diagnostics))
	}
}

// TestEmitReleaseNotResolvedEnvelopeShape verifies the helper's
// envelope construction: Code is 1002, Message is the cascade
// summary, Data carries both the file and the multi-line trace.
func TestEmitReleaseNotResolvedEnvelopeShape(t *testing.T) {
	t.Parallel()
	srv := &Server{
		cascadeAttempt: cascadeAttempt{
			ReleaseCandidatesFound: 3,
		},
	}
	env := srv.emitReleaseNotResolvedEnvelope("/ws/aggregate.yaml")
	if env == nil {
		t.Fatalf("expected non-nil envelope")
	}
	if env.Code != errReleaseNotResolved {
		t.Errorf("Code = %d; want %d", env.Code, errReleaseNotResolved)
	}
	if env.Message == "" {
		t.Errorf("Message empty; want cascade summary")
	}
	if env.Data == nil || env.Data.File != "/ws/aggregate.yaml" {
		t.Errorf("Data.File missing or wrong: %+v", env.Data)
	}
	if env.Data.CascadeAttempt == "" {
		t.Errorf("Data.CascadeAttempt empty; want multi-line trace")
	}
}

// runTier4FixtureCase encapsulates the end-to-end Tier-A test
// pattern: copy a fixture into a tempdir, simulate cascade resolution
// by directly setting Server state, drive the validate request, and
// assert a diagnostic lands at the expected key line. Used across
// every Tier-A class fixture so each case stays focused on its
// expected-position assertion.
type runTier4FixtureCase struct {
	name        string
	fixtureName string // directory under testdata/tier4/
	wantLine    int    // expected Diagnostic line for the violation's target key
	wantMessage string // substring expected in at least one Diagnostic.Message
}

func runTier4ValidationFixture(t *testing.T, tc runTier4FixtureCase) {
	t.Helper()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", tc.fixtureName)
	for _, name := range []string{"release.yaml", "manifest.yaml", "aggregate.yaml"} {
		src := filepath.Join(fixtureDir, name)
		dst := filepath.Join(tmp, name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	releasePath := filepath.Join(tmp, "release.yaml")
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}

	srv := newTestServer(t, tmp, nil, nil)
	srv.activeRelease = rel
	srv.activeReleasePath = releasePath
	srv.cascadeAttempt = cascadeAttempt{
		AuthoringReleaseConfigured: true,
		ReleaseCandidatesFound:     1,
		ResolvedStep:               1,
	}

	aggPath := filepath.Join(tmp, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    aggPath,
		Content: string(aggBytes),
	}))
	resp := runOneRequest(t, srv, &in, &out)

	if resp.Error != nil {
		t.Fatalf("unexpected envelope error: %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Diagnostics) == 0 {
		t.Fatalf("expected at least one Tier-A diagnostic; got zero")
	}

	var sawTargetLine, sawTargetMessage bool
	for _, d := range result.Diagnostics {
		if d.Line == tc.wantLine {
			sawTargetLine = true
			if d.Severity != "error" {
				t.Errorf("diagnostic at line %d has Severity = %q; want %q",
					tc.wantLine, d.Severity, "error")
			}
		}
		if tc.wantMessage != "" && containsSubstring(d.Message, tc.wantMessage) {
			sawTargetMessage = true
		}
	}
	if !sawTargetLine {
		var lines []int
		for _, d := range result.Diagnostics {
			lines = append(lines, d.Line)
		}
		t.Errorf("no diagnostic at expected line %d; got lines %v messages %q",
			tc.wantLine, lines, diagMessages(result.Diagnostics))
	}
	if tc.wantMessage != "" && !sawTargetMessage {
		t.Errorf("no diagnostic message contained %q; got messages %q",
			tc.wantMessage, diagMessages(result.Diagnostics))
	}
}

// containsSubstring is a small helper avoiding the strings import in
// every test. Equivalent to strings.Contains(s, sub).
func containsSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestRunTier4Validation_ParameterTypeMismatchPositionsAtAggregateKey
// exercises the full end-to-end Tier-A path against the
// parameter-type-mismatch fixture. The Release supplies
// spec.parameters.port as a string while the Aggregate's contract
// declares port as integer; the resolver emits a *ResolverError
// carrying Field == ".spec.parameters.port" and the walker lands
// the diagnostic at that key.
//
// Pins the architectural promise of Tier-A position recovery: a
// real pipeline diagnostic, surfaced through the wire shape, lands
// at the source position the user can navigate to.
func TestRunTier4Validation_ParameterTypeMismatchPositionsAtAggregateKey(t *testing.T) {
	t.Parallel()
	runTier4ValidationFixture(t, runTier4FixtureCase{
		name:        "parameter-type-mismatch",
		fixtureName: "parameter-type-mismatch",
		// `port:` key is on line 8 of the fixture aggregate.yaml.
		wantLine:    8,
		wantMessage: "port",
	})
}

// TestRunTier4Validation_MissingRequiredParameterPositionsAtAggregateKey
// exercises the ErrParameterMissing path: the Aggregate's contract
// declares spec.parameters.database_url as required; the Release
// supplies no parameters at all. The resolver emits a
// *ResolverError carrying Field == ".spec.parameters.database_url"
// and the walker lands the diagnostic at that key in the Aggregate.
func TestRunTier4Validation_MissingRequiredParameterPositionsAtAggregateKey(t *testing.T) {
	t.Parallel()
	runTier4ValidationFixture(t, runTier4FixtureCase{
		name:        "missing-required-parameter",
		fixtureName: "missing-required-parameter",
		// `database_url:` key is on line 8 of the fixture aggregate.yaml.
		wantLine:    8,
		wantMessage: "database_url",
	})
}

// TestAnalyzerErrorsToDiagnostics_FiltersCrossFileEntries exercises
// the OriginFile-based cross-file filter inside
// analyzerErrorsToDiagnostics. A synthetic AnalysisResult carries
// two entries: one whose OriginFile matches the validated file and
// one whose OriginFile names a different file. The helper drops
// the cross-file entry and emits a Diagnostic only for the match.
//
// Today no production analyzer path populates OriginFile — the
// daemon's runTier4Validation backfills the field with the
// validated file path for every entry, so all entries pass the
// filter (a no-op). This test exercises the filter LOGIC directly
// by supplying pre-populated OriginFile values that simulate the
// future case where an analyzer enhancement tracks per-error
// file origins. The filter is correctness defense against that
// enhancement landing — without this test, a future change that
// breaks the filter could ship undetected.
func TestAnalyzerErrorsToDiagnostics_FiltersCrossFileEntries(t *testing.T) {
	t.Parallel()
	const validatedFile = "/workspace/aggregate.yaml"
	const otherFile = "/workspace/catalog/components/server.yaml"

	errs := []analyzer.AnalysisError{
		{
			Kind:       analyzer.AnalysisErrorLocated,
			Message:    "selector violation: in-file error",
			OriginFile: validatedFile,
		},
		{
			Kind:       analyzer.AnalysisErrorLocated,
			Message:    "selector violation: cross-file error",
			OriginFile: otherFile,
		},
	}

	// Callers pre-render both OriginFile (via the catalog accessors)
	// and the validated file in the same coordinate system; the
	// helper just compares verbatim.
	diags := analyzerErrorsToDiagnostics(errs, validatedFile, nil, yamlpos.LineIndex{})

	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic after cross-file filter; got %d: %+v", len(diags), diags)
	}
	if !containsSubstring(diags[0].Message, "in-file error") {
		t.Errorf("expected in-file diagnostic to survive; got message %q", diags[0].Message)
	}
	if containsSubstring(diags[0].Message, "cross-file error") {
		t.Errorf("cross-file diagnostic leaked through filter; got message %q", diags[0].Message)
	}
}

// TestAnalyzerErrorsToDiagnostics_BackfillsEmptyOriginFile pins the
// behavior the production daemon path relies on today: analyzer
// errors whose OriginFile is empty get backfilled with the validated
// file path and pass the filter. This matches today's analyzer (which
// doesn't track OriginFile) and ensures the filter is a no-op until
// a future enhancement populates the field.
func TestAnalyzerErrorsToDiagnostics_BackfillsEmptyOriginFile(t *testing.T) {
	t.Parallel()
	const validatedFile = "/workspace/aggregate.yaml"

	errs := []analyzer.AnalysisError{
		{
			Kind:    analyzer.AnalysisErrorLocated,
			Message: "selector violation: no OriginFile set by analyzer",
			// OriginFile intentionally empty — matches today's
			// analyzer behavior.
		},
	}

	diags := analyzerErrorsToDiagnostics(errs, validatedFile, nil, yamlpos.LineIndex{})

	if len(diags) != 1 {
		t.Fatalf("expected 1 diagnostic for empty-OriginFile entry; got %d", len(diags))
	}
	// Confirm the backfill happened so the next emission with the
	// same slice would see the field populated.
	if errs[0].OriginFile != validatedFile {
		t.Errorf("OriginFile was not backfilled; got %q want %q",
			errs[0].OriginFile, validatedFile)
	}
}

// TestRunTier4Validation_DanglingExtendsFallsBackToTierD pins the
// dangling-extends emission shape against the current code path.
//
// When a catalog Component declares spec.extends against a
// non-existent parent URI and an Aggregate references that broken
// Component, the daemon's runTier4Validation produces a Tier-D
// fall-back diagnostic (file:1:1 with "Dependencies: " prefix) rather
// than a sub-file position-mapped diagnostic.
//
// The path empirically traced: the validate request enters
// runTier4Validation; the first call site is
// s.resolveReleaseClosure(s.activeReleasePath). resolveReleaseClosure
// internally calls s.resolveRecipeCatalog(); the catalog loads
// successfully but cat.ResolveAllExtends() returns at least one
// *validator.ValidationError; resolveRecipeCatalog wraps the slice
// as fmt.Errorf("catalog extends: N resolution error(s)");
// resolveReleaseClosure re-wraps as *jsonRPCError{Code:
// errEntityNotResolvable, Message: <wrapped string>} and returns
// that envelope to runTier4Validation; runTier4Validation's
// resolveReleaseClosure-envelope branch emits Diagnostic{Line:1,
// Column:1, Message: "Dependencies: " + envelope.Message}. The
// downstream resolveRecipeCatalog branch (which would prefix
// "Dependencies: catalog load: ...") is never reached on this path
// because resolveReleaseClosure already short-circuited the
// failure. The absence of the "catalog load:" substring is
// asserted below to pin which fall-back branch fires — without
// it, both Tier-D branches in runTier4Validation produce messages
// matching "Dependencies: " and "extends", and the test would not
// disambiguate.
//
// Position recovery for the per-Component ValidationError is not
// available at the daemon's pipeline boundary: the lazy-lookup
// path observed at the catalog layer logs the first
// ValidationError to stderr and continues with the unmerged
// Component (a behavior documented in the catalog package's
// contract but not asserted by this test). The structured
// per-Component position therefore never reaches a daemon-
// consumable shape. Promoting dangling-extends to Tier-A would
// require either propagating []*validator.ValidationError through
// the lazy lookup's return shape or instrumenting the daemon to
// re-scan the catalog post-failure to recover per-Component
// locators; both are out of scope for the current change.
func TestRunTier4Validation_DanglingExtendsFallsBackToTierD(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "dangling-extends")

	for _, name := range []string{"release.yaml", "manifest.yaml", "aggregate.yaml", ".aggrega-config.yaml"} {
		src := filepath.Join(fixtureDir, name)
		dst := filepath.Join(tmp, name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}
	catalogSrc := filepath.Join(fixtureDir, "catalog", "components", "broken-server.yaml")
	catalogDstDir := filepath.Join(tmp, "catalog", "components")
	if err := os.MkdirAll(catalogDstDir, 0o755); err != nil {
		t.Fatalf("mkdir catalog: %v", err)
	}
	data, err := os.ReadFile(catalogSrc)
	if err != nil {
		t.Fatalf("read catalog fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(catalogDstDir, "broken-server.yaml"), data, 0o644); err != nil {
		t.Fatalf("write catalog fixture: %v", err)
	}

	releasePath := filepath.Join(tmp, "release.yaml")
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}

	srv := newTestServer(t, tmp, nil, nil)
	srv.activeRelease = rel
	srv.activeReleasePath = releasePath
	srv.cascadeAttempt = cascadeAttempt{
		AuthoringReleaseConfigured: true,
		ReleaseCandidatesFound:     1,
		ResolvedStep:               1,
	}
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(tmp, "catalog")}
	// The lazy catalog captures catalogDirs at construction; newTestServer built it
	// with nil dirs, so rewire it to the test's catalog dir now that it is set.
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{filepath.Join(tmp, "catalog")}, nil)

	aggPath := filepath.Join(tmp, "aggregate.yaml")
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    aggPath,
		Content: string(aggBytes),
	}))
	resp := runOneRequest(t, srv, &in, &out)

	if resp.Error != nil {
		t.Fatalf("unexpected envelope error (validate should succeed with Tier-D diagnostic in result): %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("expected exactly 1 Tier-D diagnostic; got %d (%+v)", len(result.Diagnostics), result.Diagnostics)
	}
	d := result.Diagnostics[0]
	if d.Line != 1 || d.Column != 1 {
		t.Errorf("Tier-D fall-back position = %d:%d; want 1:1", d.Line, d.Column)
	}
	if d.Severity != "error" {
		t.Errorf("Severity = %q; want %q", d.Severity, "error")
	}
	if !containsSubstring(d.Message, "Dependencies: ") {
		t.Errorf("Message should carry Tier-D prefix; got %q", d.Message)
	}
	if !containsSubstring(d.Message, "extends") {
		t.Errorf("Message should mention extends; got %q", d.Message)
	}
	// Pin which Tier-D branch fires. runTier4Validation has two
	// Tier-D fall-back paths producing similar messages: the
	// resolveReleaseClosure-envelope path ("Dependencies: <envelope.Message>")
	// and the resolveRecipeCatalog-error path ("Dependencies: catalog load: <err>").
	// The dangling-extends class flows through the resolveReleaseClosure-
	// envelope path because resolveReleaseClosure internally invokes
	// resolveRecipeCatalog and short-circuits the catalog-load failure;
	// the outer runTier4Validation never reaches its own catalog-load
	// branch. The "catalog load:" substring would only appear if that
	// outer branch fired — its absence pins the emission site. If this
	// assertion ever fails because "catalog load:" appears, the
	// emission site has moved and the test's godoc trace needs to be
	// revised to match.
	if containsSubstring(d.Message, "catalog load:") {
		t.Errorf("expected resolveReleaseClosure-envelope Tier-D branch (absence of \"catalog load:\" substring); got %q", d.Message)
	}
}

// TestRunTier4Validation_CrossFileAnalyzerViolationsFilteredToValidatedFile
// pins the daemon's per-file diagnostic filter against a fixture that
// produces analyzer violations originating in two different Aggregate
// files. The fixture's root Aggregate attaches a Feature that fails
// appliesTo on a root-tier Component; the same fixture's sub-Aggregate
// (imported via spec.aggregates) attaches the same Feature on a
// sub-tier Component, producing a second appliesTo violation with a
// different OriginFile.
//
// Two-validation falsifiability pattern.
//
//  1. Drive the daemon's aggrega/validate against the root Aggregate
//     file. Capture the diagnostic set.
//  2. Independently run the same pipeline inputs through
//     pipeline.RunTier4Pipeline and inspect the analyzer's unfiltered
//     Errors slice. The slice must carry at least one entry whose
//     OriginFile names the root file AND at least one whose OriginFile
//     names a different file (the sub-aggregate's path).
//  3. Assert the daemon's response carries only the root-origin
//     violations — the sub-origin entries must NOT survive into the
//     daemon's response (filtered) yet MUST appear in the pipeline's
//     raw output (proving they were produced and not silently dropped
//     upstream).
//
// If the fixture stops producing cross-file violations (catalog or
// schema change), step 2 fails on disjointness. If the daemon's
// filter becomes a no-op, step 3's count assertion fails — the test
// cannot pass by luck.
//
// Both halves discriminate on something that differs per file, and neither is
// the message. The two violations report the same declaration failing on the
// same grounds, so they read identically by design; what separates them is the
// document each is about and the authoring key each names. Disjointness is
// therefore asserted on the locator, and survival on the count.
func TestRunTier4Validation_CrossFileAnalyzerViolationsFilteredToValidatedFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "cross-file-analyzer-violations")

	// Copy top-level fixture files.
	for _, name := range []string{"release.yaml", "manifest.yaml", "aggregate.yaml", ".aggrega-config.yaml"} {
		src := filepath.Join(fixtureDir, name)
		dst := filepath.Join(tmp, name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	// Mirror the catalog subtree (components/, features/, aggregates/) into the tempdir.
	type catalogEntry struct{ subdir, name string }
	catalogEntries := []catalogEntry{
		{"components", "web.yaml"},
		{"features", "premium.yaml"},
		{"aggregates", "sub-with-violation.yaml"},
	}
	for _, e := range catalogEntries {
		dstDir := filepath.Join(tmp, "catalog", e.subdir)
		if err := os.MkdirAll(dstDir, 0o755); err != nil {
			t.Fatalf("mkdir catalog/%s: %v", e.subdir, err)
		}
		data, err := os.ReadFile(filepath.Join(fixtureDir, "catalog", e.subdir, e.name))
		if err != nil {
			t.Fatalf("read catalog/%s/%s: %v", e.subdir, e.name, err)
		}
		if err := os.WriteFile(filepath.Join(dstDir, e.name), data, 0o644); err != nil {
			t.Fatalf("write catalog/%s/%s: %v", e.subdir, e.name, err)
		}
	}

	releasePath := filepath.Join(tmp, "release.yaml")
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}

	srv := newTestServer(t, tmp, nil, nil)
	srv.activeRelease = rel
	srv.activeReleasePath = releasePath
	srv.cascadeAttempt = cascadeAttempt{
		AuthoringReleaseConfigured: true,
		ReleaseCandidatesFound:     1,
		ResolvedStep:               1,
	}
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(tmp, "catalog")}
	// The lazy catalog captures catalogDirs at construction; newTestServer built it
	// with nil dirs, so rewire it to the test's catalog dir now that it is set.
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{filepath.Join(tmp, "catalog")}, nil)

	aggPath := filepath.Join(tmp, "aggregate.yaml")

	// Step 2: independently run the pipeline so the unfiltered analyzer
	// output is observable. The slice must carry violations from BOTH
	// the root Aggregate file AND the sub-aggregate file before the
	// per-file filter assertion is meaningful.
	view := srv.lazyCatalog.ForRequest(nil)
	resolved, env := srv.resolveReleaseClosure(view, releasePath, nil)
	if env != nil {
		t.Fatalf("resolveReleaseClosure envelope: %+v", env)
	}
	// Mirror the daemon's production conversion: feed the pipeline a
	// workspace-relative root aggregate path so the IR's per-node
	// SourceFile records sit in the same coordinate system as the
	// catalog accessors' sub-aggregate paths.
	relAggPath := catalog.RelativeToWorkspace(srv.workspaceRoot, aggPath)
	tier4, _ := pipeline.RunTier4Pipeline(
		context.Background(),
		pipeline.Inputs{
			Release:           resolved.Release,
			Manifest:          resolved.Manifest,
			AggregateRawBytes: resolved.AggBytes,
			AggregatePath:     relAggPath,
			WorkspaceConfig:   srv.wsConfig,
		},
		view.AsCatalogLookup(),
		view.RecipeLookupWithManifest(resolved.Manifest).Recipe,
		view.SubAggregateLoader(),
	)
	if tier4 == nil || tier4.Analysis == nil {
		t.Fatalf("expected non-nil Tier4Result.Analysis from pipeline; got %+v", tier4)
	}
	// The IR emits SourceFile in workspace-relative form so the wire
	// shape stays byte-identical across developer machines. Match
	// against the relative form here.
	const wantRootRel = "aggregate.yaml"
	const wantSubRel = "catalog/aggregates/sub-with-violation.yaml"
	var rootMessages, subMessages []string
	var rootLocators, subLocators []string
	for _, e := range tier4.Analysis.Errors {
		switch e.OriginFile {
		case wantRootRel:
			rootMessages = append(rootMessages, e.Message)
			rootLocators = append(rootLocators, e.Field)
		case wantSubRel:
			subMessages = append(subMessages, e.Message)
			subLocators = append(subLocators, e.Field)
		default:
			t.Errorf("unexpected OriginFile on analyzer violation: %q (at %q); want %q or %q",
				e.OriginFile, e.Field, wantRootRel, wantSubRel)
		}
	}
	if len(rootMessages) == 0 {
		t.Fatalf("expected ≥1 analyzer violation with OriginFile=%q (root); pipeline produced none. all errors: %+v",
			wantRootRel, tier4.Analysis.Errors)
	}
	if len(subMessages) == 0 {
		t.Fatalf("expected ≥1 analyzer violation with OriginFile=%q (sub-aggregate); pipeline produced none. all errors: %+v",
			wantSubRel, tier4.Analysis.Errors)
	}
	// Disjointness prevents a degenerate fixture where one violation is
	// counted twice, and it is asserted on the LOCATOR rather than on the
	// message. Which document a violation is about is carried by its origin
	// and its locator; the message carries the values the rule read, and two
	// components failing the same declaration on the same grounds read the
	// same by design. So a message comparison here would report a degenerate
	// fixture for a fixture that is doing exactly what it was built to do.
	for _, rl := range rootLocators {
		for _, sl := range subLocators {
			if rl == sl {
				t.Errorf("root-origin and sub-origin violations name the same authoring key; the fixture should produce per-file distinct violations.\n  root: %q\n  sub : %q", rl, sl)
			}
		}
	}

	// Step 3: drive the daemon's validate against the root Aggregate.
	// The response carries only the root-origin violation(s) — the
	// sub-origin entries are filtered.
	aggBytes, err := os.ReadFile(aggPath)
	if err != nil {
		t.Fatalf("read aggregate: %v", err)
	}
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    aggPath,
		Content: string(aggBytes),
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("unexpected envelope error: %+v", resp.Error)
	}
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Every root-origin message must appear.
	for _, rm := range rootMessages {
		found := false
		for _, d := range result.Diagnostics {
			if containsSubstring(d.Message, rm) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("root-origin violation message %q missing from daemon response; daemon diagnostics: %v",
				rm, diagMessages(result.Diagnostics))
		}
	}

	// The sub-origin entries must not survive, and that is asserted BY COUNT
	// rather than by absence of their text. The two files' violations read the
	// same, so a search for the sub-origin text finds the root-origin entry and
	// reports a leak that did not happen; counting how many diagnostics of this
	// class the response carries cannot be fooled that way. A filter turned into
	// a no-op raises the count by exactly the sub-origin entries, which is what
	// this fails on.
	class := rootMessages[0]
	carried := 0
	for _, d := range result.Diagnostics {
		if containsSubstring(d.Message, class) {
			carried++
		}
	}
	if want := countMatching(rootMessages, class); carried != want {
		t.Errorf("the response carries %d diagnostic(s) of the violation class %q; want %d, the root-origin entries alone.\n"+
			"the pipeline produced %d root-origin and %d sub-origin violation(s), so a per-file filter turned into a no-op reads as %d.\n"+
			"all daemon diagnostics: %v",
			carried, class, want, len(rootMessages), len(subMessages),
			want+countMatching(subMessages, class), diagMessages(result.Diagnostics))
	}
}

// countMatching counts how many of the messages carry the given text.
func countMatching(messages []string, text string) int {
	n := 0
	for _, m := range messages {
		if containsSubstring(m, text) {
			n++
		}
	}
	return n
}

// diagMessages returns the messages of every Diagnostic in the
// slice, for inclusion in test failure output when the position
// assertion fails (gives the operator immediate visibility into
// what diagnostics WERE produced).
func diagMessages(ds []Diagnostic) []string {
	out := make([]string, 0, len(ds))
	for _, d := range ds {
		out = append(out, d.Message)
	}
	return out
}

// TestMultiResolverErrorRoundTripPreservesViolations is a smoke test
// confirming the pipeline-side extraction path: when
// runTier4Validation receives a MultiResolverError-wrapped result,
// the structured Violations slice round-trips into the Diagnostic
// slice via resolverFindingToDiagnostic. This catches the
// regression where the Violations slice would be lost at the
// pipeline boundary.
func TestMultiResolverErrorRoundTripPreservesViolations(t *testing.T) {
	t.Parallel()
	multi := &resolver.MultiResolverError{
		Entity:   "Aggregate",
		Field:    ".spec.parameters",
		Summary:  "2 parameter contract violations:\n  - .spec.parameters.port: ...\n  - .spec.parameters.host: ...",
		Sentinel: resolver.ErrParameterContractViolation(),
		Violations: []*resolver.ResolverError{
			{Entity: "Aggregate", Field: ".spec.parameters.port", Message: "port: expected integer", Err: resolver.ErrParameterContractViolation()},
			{Entity: "Aggregate", Field: ".spec.parameters.host", Message: "host: expected string", Err: resolver.ErrParameterContractViolation()},
		},
	}
	// Confirm errors.As recovers the MultiResolverError shape (this
	// is what RunTier4Pipeline does to populate ResolveDiagnostics).
	var got *resolver.MultiResolverError
	if !errors.As(multi, &got) {
		t.Fatalf("errors.As failed to extract *MultiResolverError")
	}
	if len(got.Violations) != 2 {
		t.Errorf("Violations length = %d; want 2", len(got.Violations))
	}

	// Walk each violation through resolverFindingToDiagnostic and
	// confirm the position recovery works against a real YAML doc.
	content := `spec:
  parameters:
    port:
      type: integer
    host:
      type: string
`
	doc := parseYAML(t, content)
	idx := lineIndexFor(content)
	diags := make([]Diagnostic, 0, len(got.Violations))
	for _, ve := range got.Violations {
		diags = append(diags, resolverFindingToDiagnostic(ve, "error", doc, idx))
	}
	if len(diags) != 2 {
		t.Fatalf("expected 2 diagnostics; got %d", len(diags))
	}
	// Diagnostics are positioned at port (line 3) and host (line 5).
	if diags[0].Line != 3 {
		t.Errorf("first diag Line = %d; want 3", diags[0].Line)
	}
	if diags[1].Line != 5 {
		t.Errorf("second diag Line = %d; want 5", diags[1].Line)
	}
}

// TestAttachmentSlotNearMissPublishesAsEditorWarning pins the editor-visibility
// contract for the advisory class: an attachment-slot near miss must reach the
// language-server diagnostic stream at WARNING severity, positioned on the
// offending authoring key, and must not be published as an error. Terminal-only
// visibility is not sufficient — the author sees this in the editor or not at
// all.
func TestAttachmentSlotNearMissPublishesAsEditorWarning(t *testing.T) {
	t.Parallel()
	content := `spec:
  components:
    db:
      features:
        db_passwrod:
          type: core/referenced-secret
`
	doc := parseYAML(t, content)
	advisory := &resolver.ResolverError{
		Entity:  "Aggregate test-app",
		Field:   `.spec.components.db.features.db_passwrod`,
		Message: `attachment "db_passwrod" is not a declared slot, but the type declares the slot "db_password"`,
		Err:     resolver.ErrAttachmentSlotNearMiss(),
	}

	diag := resolverFindingToDiagnostic(advisory, "warning", doc, lineIndexFor(content))
	if diag.Severity != "warning" {
		t.Errorf("Severity = %q; want %q — an advisory must never publish as an error", diag.Severity, "warning")
	}
	// Positioned on the misspelled key (line 5), not at the head of the file.
	if diag.Line != 5 {
		t.Errorf("Line = %d; want 5 (the db_passwrod key line)", diag.Line)
	}
	if diag.Column != 9 || diag.EndLine != 5 || diag.EndColumn != 20 {
		t.Errorf("range = %d:%d-%d:%d; want 5:9-5:20 (spanning the key token)",
			diag.Line, diag.Column, diag.EndLine, diag.EndColumn)
	}
	if diag.Message != advisory.Message {
		t.Errorf("Message = %q; want %q", diag.Message, advisory.Message)
	}
}

// TestTier4AdvisoriesMapToWarningsAlongsideErrors pins that the advisory slice
// on Tier4Result is a SEPARATE channel from ResolveDiagnostics: advisories map
// to warnings, violations to errors, and the presence of one does not suppress
// the other.
func TestTier4AdvisoriesMapToWarningsAlongsideErrors(t *testing.T) {
	t.Parallel()
	content := `spec:
  parameters:
    host:
      type: string
`
	doc := parseYAML(t, content)
	idx := lineIndexFor(content)

	violation := &resolver.ResolverError{
		Entity: "Aggregate", Field: ".spec.parameters.host",
		Message: "boom", Err: resolver.ErrParameterContractViolation(),
	}
	advisory := &resolver.ResolverError{
		Entity: "Aggregate", Field: ".spec.parameters.host",
		Message: "near miss", Err: resolver.ErrAttachmentSlotNearMiss(),
	}

	got := []Diagnostic{
		resolverFindingToDiagnostic(violation, "error", doc, idx),
		resolverFindingToDiagnostic(advisory, "warning", doc, idx),
	}
	if got[0].Severity != "error" || got[1].Severity != "warning" {
		t.Fatalf("severities = %q,%q; want error,warning", got[0].Severity, got[1].Severity)
	}
	// Both recover the same position through the one shared walker, so an
	// advisory is never demoted to a file-head diagnostic.
	if got[0].Line != got[1].Line || got[0].Column != got[1].Column {
		t.Errorf("advisory position %d:%d differs from violation %d:%d; both must use the shared walker",
			got[1].Line, got[1].Column, got[0].Line, got[0].Column)
	}
}
