// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// rawDiagnostics drives one aggrega/validate request over a tier4 fixture and
// returns the diagnostic entries as the raw JSON objects the client receives.
//
// The entries are left un-decoded on purpose. A typed decode cannot tell an
// omitted key from a key carrying the empty string, and the difference between
// those two is exactly what the absence assertions below are about.
// tier4Workspace copies the named files of a tier4 fixture into a fresh
// temporary workspace and returns its root.
//
// The caller chooses which files to copy, and withholding one is a legitimate
// way to select the failure under test: a resolution that cannot find a sibling
// document fails at a different stage from one that finds every document and
// cannot resolve a reference. A short list is therefore a choice, and the test
// making it says which stage it is after.
func tier4Workspace(t *testing.T, fixture string, files []string) string {
	t.Helper()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", fixture)
	for _, rel := range files {
		data, err := os.ReadFile(filepath.Join(fixtureDir, rel))
		if err != nil {
			t.Fatalf("read fixture %s: %v", rel, err)
		}
		dst := filepath.Join(tmp, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(dst, data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", rel, err)
		}
	}
	return tmp
}

func rawDiagnostics(t *testing.T, fixture string, files []string) []map[string]json.RawMessage {
	t.Helper()
	tmp := tier4Workspace(t, fixture, files)

	srv := newTestServer(t, tmp, nil, nil)
	if catalogDir := filepath.Join(tmp, "catalog"); dirExists(catalogDir) {
		srv.wsConfig.Workspace.CatalogDirs = []string{catalogDir}
		srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{catalogDir}, nil)
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
		Release: "release.yaml",
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("unexpected envelope error: %+v", resp.Error)
	}
	var envelope struct {
		Diagnostics []map[string]json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(resp.Result, &envelope); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(envelope.Diagnostics) == 0 {
		t.Fatalf("fixture %s produced no diagnostics", fixture)
	}
	return envelope.Diagnostics
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// codeOnWire reports the value of the entry's code key and whether the key is
// present at all, keeping the two questions apart.
func codeOnWire(t *testing.T, entry map[string]json.RawMessage) (string, bool) {
	t.Helper()
	raw, present := entry["code"]
	if !present {
		return "", false
	}
	var code string
	if err := json.Unmarshal(raw, &code); err != nil {
		t.Fatalf("code is not a JSON string: %s", raw)
	}
	return code, true
}

func messageOnWire(t *testing.T, entry map[string]json.RawMessage) string {
	t.Helper()
	var message string
	if err := json.Unmarshal(entry["message"], &message); err != nil {
		t.Fatalf("message is not a JSON string: %s", entry["message"])
	}
	return message
}

// findCodeOnWire returns the first entry reporting want, or fails naming every
// code the fixture did emit — so a regression says which condition arrived
// instead of only that the expected one did not.
func findCodeOnWire(t *testing.T, entries []map[string]json.RawMessage, want string) map[string]json.RawMessage {
	t.Helper()
	var saw []string
	for _, entry := range entries {
		code, present := codeOnWire(t, entry)
		if present && code == want {
			return entry
		}
		if present {
			saw = append(saw, code)
		} else {
			saw = append(saw, "(absent)")
		}
	}
	t.Fatalf("no diagnostic reported code %q; codes on the wire were %v", want, saw)
	return nil
}

// TestDiagnosticCodeReachesTheWireAsAField pins the contract a client outside Go
// binds to: the condition's code arrives as its own key, and the message is not
// where a client has to look for it.
//
// Both halves matter. A code folded into the message would satisfy a reading of
// "the code reaches the client" while leaving the client parsing prose to
// recover it, which is the whole thing the key exists to remove — so the second
// assertion is what makes the first one mean something.
func TestDiagnosticCodeReachesTheWireAsAField(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		fixture string
		files   []string
		want    string
	}{
		{
			fixture: "parameter-type-mismatch",
			files:   []string{"release.yaml", "manifest.yaml", "aggregate.yaml"},
			want:    "parameter-contract-violation",
		},
		{
			fixture: "missing-required-parameter",
			files:   []string{"release.yaml", "manifest.yaml", "aggregate.yaml"},
			want:    "parameter-missing",
		},
	} {
		tc := tc
		t.Run(tc.fixture, func(t *testing.T) {
			t.Parallel()
			entries := rawDiagnostics(t, tc.fixture, tc.files)
			entry := findCodeOnWire(t, entries, tc.want)
			if message := messageOnWire(t, entry); strings.Contains(message, tc.want) {
				t.Errorf("code is embedded in the message text %q; it must travel as its own key and nowhere else", message)
			}
		})
	}
}

// TestDiagnosticCodeOnEveryReportingClass covers each class of daemon diagnostic
// that names a registered condition, driven end to end so a class that stops
// reporting its code fails here rather than going unnoticed.
//
// The classes reach the wire through different mappings — a resolver-class
// finding, a rule over the finished graph, and a resolution-completeness
// finding — and each mapping reads the condition from a different place, so one
// class passing says nothing about the others.
func TestDiagnosticCodeOnEveryReportingClass(t *testing.T) {
	t.Parallel()
	slotFixture := []string{
		"release.yaml", "manifest.yaml", "aggregate.yaml", ".aggrega-config.yaml",
		"catalog/components/slotted-db.yaml", "catalog/features/pw.yaml",
	}
	for _, tc := range []struct {
		name    string
		fixture string
		files   []string
		want    string
	}{
		{
			name:    "unfilled attachment slot",
			fixture: "required-attachment-slot",
			files:   slotFixture,
			want:    "required-slot-unfilled",
		},
		{
			name:    "no recipe resolved",
			fixture: "required-attachment-slot",
			files:   slotFixture,
			want:    "no-recipe-resolved",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			findCodeOnWire(t, rawDiagnostics(t, tc.fixture, tc.files), tc.want)
		})
	}
}

// TestDiagnosticCodeKeyAbsentWhenNoConditionIsNamed pins the other half of the
// contract: where the failure names no registered condition the key is OMITTED,
// not sent carrying the empty string.
//
// A buffer no YAML parser will read is the class that has none to name — the
// parse failure comes from the YAML library and no condition of this
// toolchain's own stands behind it. An empty string on the wire would read as
// "the condition's code is the empty string", which is a claim, where an absent
// key is the honest statement that there is nothing to report.
func TestDiagnosticCodeKeyAbsentWhenNoConditionIsNamed(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/validate", validateParams{
		File:    filepath.Join(tmp, "aggregate.yaml"),
		Content: "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\n  bad: [unclosed\n",
	}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("unexpected envelope error: %+v", resp.Error)
	}
	var envelope struct {
		Diagnostics []map[string]json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal(resp.Result, &envelope); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}
	if len(envelope.Diagnostics) != 1 {
		t.Fatalf("expected one parse diagnostic; got %d", len(envelope.Diagnostics))
	}
	entry := envelope.Diagnostics[0]
	if code, present := codeOnWire(t, entry); present {
		t.Errorf("code key present (%q) on a failure naming no condition; it must be omitted", code)
	}
	// The entry is a real diagnostic, so the absence above is about the code and
	// not about an empty response.
	if message := messageOnWire(t, entry); !strings.Contains(message, "yaml:") {
		t.Errorf("expected the parser's own text; got %q", message)
	}
}

// TestDiagnosticCodeIsAnOutputAndLeavesMatchingAlone pins that reporting the
// code changed nothing about how a Go consumer recognises the condition.
//
// The mapping reads the code off the finding and the finding still matches its
// condition afterwards, which is the property that would break if reporting a
// code had been built by turning the string back into an identity — there is no
// such path, and this fails if one is ever introduced in its place.
func TestDiagnosticCodeIsAnOutputAndLeavesMatchingAlone(t *testing.T) {
	t.Parallel()
	content := `spec:
  parameters:
    port:
      type: integer
`
	finding := &resolver.ResolverError{
		Entity:  "Aggregate",
		Field:   ".spec.parameters.port",
		Message: "parameter port: expected integer, got string",
		Err:     resolver.ErrParameterContractViolation(),
	}
	diag := resolverFindingToDiagnostic(finding, "error", parseYAML(t, content), lineIndexFor(content))
	if diag.Code != "parameter-contract-violation" {
		t.Errorf("Code = %q; want the condition's registered code", diag.Code)
	}
	if !errors.Is(finding, resolver.ErrParameterContractViolation()) {
		t.Error("the finding no longer matches its condition; reporting the code must not touch matching")
	}
	if strings.Contains(diag.Message, diag.Code) {
		t.Errorf("Message %q carries the code; the key is the only place it travels", diag.Message)
	}
}

// TestDiagnosticCodeFromRuleSentinel pins where the rule-over-the-graph class
// reads its condition from.
//
// A violation of this class reports its own message and does not unwrap to the
// condition it names, so a chain walk over the violation itself finds nothing —
// the mapping has to read the sentinel the violation carries. An edit that
// "simplified" this to a walk over the violation would leave the whole class
// codeless while every other class kept working.
func TestDiagnosticCodeFromRuleSentinel(t *testing.T) {
	t.Parallel()
	content := `spec:
  components:
    web:
      type: example/web
`
	violations := []analyzer.AnalysisError{{
		Kind:       analyzer.AnalysisErrorLocated,
		Message:    "selector violation",
		Field:      ".spec.components.web",
		OriginFile: "aggregate.yaml",
		Sentinel:   analyzer.ErrSelectorViolation(),
	}}
	diags := analyzerErrorsToDiagnostics(violations, "aggregate.yaml", parseYAML(t, content), lineIndexFor(content))
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic; got %d", len(diags))
	}
	if diags[0].Code != "selector-violation" {
		t.Errorf("Code = %q; want the sentinel's registered code", diags[0].Code)
	}
}

// TestDiagnosticCodeOnEnvelopeRejection pins the buffer-envelope class, whose
// findings all report one condition: the rejection is the condition, and what
// separates the findings beneath it is the key each one points at.
func TestDiagnosticCodeOnEnvelopeRejection(t *testing.T) {
	t.Parallel()
	srv := newTestServer(t, t.TempDir(), nil, nil)
	content := []byte(`apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: r
  environment: dev
  version: "1.0.0"
spec:
  aggregate: a
  manifest: m
  boundaries:
    main:
      directory: ./d
      components:
        api:
          values:
            port: 9999
`)
	diags, has := srv.envelopeDiagnosticsForBuffer("release.yaml", "", content, parseDoc(t, string(content)))
	if !has || len(diags) == 0 {
		t.Fatalf("expected envelope findings; has=%v diags=%d", has, len(diags))
	}
	for i, d := range diags {
		if d.Code != "entity-envelope-invalid" {
			t.Errorf("diagnostic %d Code = %q; want the envelope condition's registered code", i, d.Code)
		}
	}
}

// TestDiagnosticCodeOnLockfileDrift pins the drift class, which reaches the
// client on the pushed notification rather than on a validate response and is
// the one warning-severity class reporting a code.
func TestDiagnosticCodeOnLockfileDrift(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	srv := newTestServer(t, tmp, nil, nil)
	lockfilePath := filepath.Join(tmp, "release.lock")
	// A lockfile that PARSES and pins one entity, verified against a closure that
	// reaches none: the entity is pinned and no longer used, which is drift. A
	// malformed lockfile would take the load-failure branch instead and never
	// reach the drift report, so its digest is a well-formed content digest.
	pinned := "version: 1\n" +
		"release: r\n" +
		"generated_at: \"2026-01-01T00:00:00Z\"\n" +
		"entities:\n" +
		"  - kind: component\n" +
		"    uri: example/web\n" +
		"    resolved_version: 1.0.0\n" +
		"    digest: " + contentDigestHex([]byte("example/web")) + "\n"
	if err := os.WriteFile(lockfilePath, []byte(pinned), 0o644); err != nil {
		t.Fatalf("write lockfile: %v", err)
	}
	if lf := srv.verifyDriftAndUpdateCache(lockfilePath, "release.yaml", closure.Closure{}); lf == nil {
		t.Fatal("lockfile did not load; the drift branch was never reached")
	}
	diags := srv.snapshotDiagnostics(lockfilePath)
	if len(diags) != 1 {
		t.Fatalf("expected one drift diagnostic; got %d", len(diags))
	}
	if diags[0].Severity != "warning" {
		t.Errorf("Severity = %q; want warning", diags[0].Severity)
	}
	if diags[0].Code != "lockfile-drift" {
		t.Errorf("Code = %q; want the drift condition's registered code", diags[0].Code)
	}
}

// TestDiagnosticCodeOnCascadeFailure pins the class the daemon publishes on a
// Release document it found but could not load. It reaches the client on the
// pushed notification and is the class that anchors on a file other than the one
// being edited, so it is mapped at its own site rather than through the
// validation path the other classes share.
func TestDiagnosticCodeOnCascadeFailure(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	unloadable := filepath.Join(tmp, "release.yaml")
	// Found, parses as YAML, and rejected by its kind's schema: the shape that
	// makes the cascade record a load error rather than an absence.
	if err := os.WriteFile(unloadable, []byte("apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: r\nspec:\n  bogus_key: 1\n"), 0o644); err != nil {
		t.Fatalf("write release: %v", err)
	}
	_, loadErr := resolver.LoadRelease(unloadable)
	if loadErr == nil {
		t.Fatal("the fixture Release loaded; the cascade-failure branch needs a load error")
	}

	var out bytes.Buffer
	srv := newTestServer(t, tmp, nil, &out)
	attempt := cascadeAttempt{
		AuthoringReleaseConfigured: true,
		AuthoringReleasePath:       unloadable,
		AuthoringReleaseLoadError:  loadErr,
	}
	ctx, flush := serializerContext(t, &out)
	if published := srv.publishCascadeFailureDiagnostics(ctx, attempt); published != 1 {
		t.Fatalf("published %d candidates; want 1", published)
	}
	flush()

	diags := srv.snapshotDiagnostics(unloadable)
	if len(diags) != 1 {
		t.Fatalf("expected one diagnostic on the unloadable Release; got %d", len(diags))
	}
	if diags[0].Code != "entity-envelope-invalid" {
		t.Errorf("Code = %q; want the envelope condition's registered code", diags[0].Code)
	}
}

// TestDiagnosticCodeOnClosureLevelFailure covers the closure-level class: a
// failure of the whole workspace resolution — a sibling document that cannot be
// loaded, a reference no catalog resolves — reported at the head of the edited
// file because it is about the workspace rather than about a position in that
// file.
//
// The class reaches the wire through an envelope no other class uses. The
// resolution hands its caller a rendered envelope, and rendering an error to
// text is one-way: the condition is reachable only through the failure the
// envelope carries beside that text. An edit that dropped the carried failure
// would leave this whole class reporting no code while every other class kept
// reporting one, and these cases are what fails then.
//
// Each case withholds a document the resolution needs, and which document is
// withheld selects the stage under test — the short file list is the choice, not
// an omission.
func TestDiagnosticCodeOnClosureLevelFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files []string
		want  string
	}{
		{
			name:  "manifest sibling absent",
			files: []string{"release.yaml", "aggregate.yaml"},
			want:  "document-not-loadable",
		},
		{
			name:  "component type no catalog holds",
			files: []string{"release.yaml", "manifest.yaml", "aggregate.yaml"},
			want:  "unresolvable-uri",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			entry := findCodeOnWire(t, rawDiagnostics(t, "dangling-extends", tc.files), tc.want)
			if message := messageOnWire(t, entry); strings.Contains(message, tc.want) {
				t.Errorf("code is embedded in the message text %q; it must travel as its own key and nowhere else", message)
			}
		})
	}
}

// TestDiagnosticCodeOnClosureExtendsFailure covers the closure-scoped extends
// gate, which the class above cannot reach: a catalog entity whose declared
// parent no catalog holds resolves far enough for the closure to be computed,
// so the failure arrives from the gate that walks the resolved closure rather
// than from the resolution itself.
func TestDiagnosticCodeOnClosureExtendsFailure(t *testing.T) {
	t.Parallel()
	entries := rawDiagnostics(t, "dangling-extends", []string{
		"release.yaml", "manifest.yaml", "aggregate.yaml", ".aggrega-config.yaml",
		"catalog/components/broken-server.yaml",
	})
	findCodeOnWire(t, entries, "extends-merge-failed")
}

// TestClosureEnvelopeCarriesTheFailureItRendered pins the envelope field that
// holds the failure, so a change that removes it fails naming that field rather
// than only leaving a key missing from a wire assertion.
//
// It also pins that carrying the failure left matching alone: the same value is
// still recognised by the condition it names, which is the property that would
// break if the code had been obtained by turning a string back into an identity
// instead of by walking the failure.
func TestClosureEnvelopeCarriesTheFailureItRendered(t *testing.T) {
	t.Parallel()
	tmp := tier4Workspace(t, "dangling-extends", []string{"release.yaml", "manifest.yaml", "aggregate.yaml"})
	srv := newTestServer(t, tmp, nil, nil)
	resolved, envelope := srv.resolveReleaseClosure(context.Background(), srv.lazyCatalog.ForRequest(nil), filepath.Join(tmp, "release.yaml"))
	if envelope == nil {
		t.Fatalf("the closure resolved (resolved=%v); the failure branch was never reached", resolved != nil)
	}
	if envelope.cause == nil {
		t.Fatal("the envelope carries no failure; its rendered text is one-way, so the condition behind it would be unrecoverable")
	}
	if code, ok := cerrors.CodeOf(envelope.cause); !ok || code != "unresolvable-uri" {
		t.Errorf("CodeOf(carried failure) = %q, %v; want the unresolvable-reference condition's registered code", code, ok)
	}
	if !errors.Is(envelope.cause, closure.ErrUnresolvableURI()) {
		t.Error("the carried failure no longer matches its condition; carrying it must not touch matching")
	}
	if strings.Contains(envelope.Message, "unresolvable-uri") {
		t.Errorf("the rendered text %q carries the code; the key is the only place it travels", envelope.Message)
	}
}
