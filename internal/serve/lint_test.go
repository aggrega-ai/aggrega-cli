// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The conventions fixtures. Both are Components, and they differ in exactly the
// thing the naming rule reads, so a finding over the first and silence over the
// second are attributable to that difference and to nothing else.
const (
	// conventionsBadComponent breaks the naming shape: a Component's name carries
	// the namespace/category/name form, and a bare word carries none of it.
	conventionsBadComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: BadName
  version: 1.0.0
  labels:
    component.aggrega.ai/type: server
spec:
  schema:
    values:
      type: object
`

	// conventionsCleanComponent is the positive control: an entity the rules pass,
	// in the directory its category mirrors. It has to be genuinely clean rather
	// than a quieter violation — a fixture that broke some other rule would report
	// findings and every no-over-fire assertion would be measuring the wrong thing.
	conventionsCleanComponent = `apiVersion: aggrega.ai/v1alpha1
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

	// conventionsAggregate is a document the conventions do not describe. An
	// Aggregate is authoring-side rather than published catalog content, so the
	// kind gate passes it over — which is what keeps an editor session quiet about
	// the documents an author spends most of their time in.
	conventionsAggregate = `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: conventions-app
  version: "1.0.0"
spec:
  components:
    backend:
      type: example/compute/api-server
`

	// conventionsForeignYAML is not an Aggrega document at all.
	conventionsForeignYAML = `replicaCount: 1
image:
  repository: nginx
  tag: latest
`
)

// conventionsWorkspace stands a workspace holding one Component under a catalog
// directory and returns the root plus the entity's workspace-relative path.
func conventionsWorkspace(t *testing.T, content string) (root, rel string) {
	t.Helper()
	root = t.TempDir()
	writeFixtureFile(t, filepath.Join(root, workspaceconfig.ConfigFilename),
		"workspace:\n  catalog_dirs:\n    - ./catalog\n")
	rel = filepath.Join("catalog", "components", "svc.yaml")
	writeFixtureFile(t, filepath.Join(root, rel), content)
	return root, rel
}

// conventionsServer builds a daemon over root with the publishing-conventions
// dial in the requested position.
//
// It resolves a real workspace rather than leaving the field nil, because the
// conventions layer refuses to run without one — resolving inside the handler
// would lint whatever directory the test process happens to sit in rather than
// the workspace the request named.
func conventionsServer(t *testing.T, root string, lintOnValidate bool) *Server {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace at %s: %v", root, err)
	}
	catalogDirs := rw.ResolveCatalogDirs("", nil)
	sess := newTestSession(t, root, &bytes.Buffer{}, &bytes.Buffer{})
	srv := newTestWorkspace(sess, root)
	srv.wsConfig = rw.Config()
	srv.resolvedWorkspace = rw
	srv.lintOnValidate = lintOnValidate
	srv.lazyCatalog = catalog.NewLazyLookup(root, rw.Config().ResolvedArtifactCacheDir(), catalogDirs, nil)
	return srv
}

// validateSavedFile drives one validation of the file at rel, sending the bytes
// on disk as the buffer, and returns the reply's composed diagnostics. It is the
// no-unsaved-edits case of validateBuffer below, which is the distinction the two
// names carry: this one asks what the author has saved, that one what they are
// currently typing.
func validateSavedFile(t *testing.T, srv *Server, root, rel, release string) []Diagnostic {
	t.Helper()
	return validateBuffer(t, srv, root, rel, release, mustRead(t, filepath.Join(root, rel)))
}

// validateBuffer drives one validation of rel carrying content as the editor
// buffer, and returns the reply's composed diagnostics. It fails the test on an
// error envelope, because every assertion below is about what a SUCCESSFUL
// validation carries.
func validateBuffer(t *testing.T, srv *Server, root, rel, release, content string) []Diagnostic {
	t.Helper()
	res, envelope := srv.handleValidate(context.Background(), validateParams{
		File:    filepath.Join(root, rel),
		Content: content,
		Release: release,
	})
	if envelope != nil {
		t.Fatalf("validate refused %s: [%d] %s", rel, envelope.Code, envelope.Message)
	}
	return res.Diagnostics
}

// conventionEntries returns the diagnostics the publishing-conventions layer
// contributed, read from the source key rather than from the message text.
func conventionEntries(diags []Diagnostic) []Diagnostic {
	var out []Diagnostic
	for _, d := range diags {
		if d.Source == DiagnosticSourceConventions {
			out = append(out, d)
		}
	}
	return out
}

// diagSummary renders a diagnostic set for a failure message: what each entry
// says, which layer raised it and what code it carries, so a red names what was
// actually published instead of a count.
func diagSummary(diags []Diagnostic) string {
	if len(diags) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(diags))
	for _, d := range diags {
		parts = append(parts, "["+d.Source+"/"+d.Severity+"/"+d.Code+"] "+d.Message)
	}
	return strings.Join(parts, "\n  ")
}

// lintCoreReport runs the publishing-conventions rules over the entity at rel
// through the lint core directly, and returns the report they produced.
//
// It is the oracle for what the daemon's own layer must say. Both read one set of
// rules, so a finding the two surfaces word differently is a defect in the
// daemon's projection rather than a difference of opinion.
func lintCoreReport(t *testing.T, root, rel string) lintops.Report {
	t.Helper()
	rw, err := workspaceconfig.Resolve(root, root)
	if err != nil {
		t.Fatalf("resolve the workspace at %s: %v", root, err)
	}
	collect := &lintops.FindingCollector{}
	// The verdict is dropped: in this mode every finding is advisory, and what an
	// oracle for the text owes is the findings.
	_ = lintops.RunEntity(t.Context(), lintops.Config{
		EntityPath: filepath.Join(root, rel),
		Workspace:  rw,
		Out:        io.Discard,
		DiagOut:    io.Discard,
		Collect:    collect,
	})
	if !collect.Reached() {
		t.Fatal("the lint core never got as far as reporting, so it is an oracle for nothing")
	}
	return collect.Report()
}

// TestConventionsLayerFollowsItsDial drives BOTH positions of
// serve.lint_on_validate over one workspace and one entity, which is the whole
// of what makes the default a default: an off position nobody drove is an
// assumption rather than a ruling.
//
// The two legs are the same request against the same convention-breaking entity,
// so the only thing that differs between them is the dial — and each leg asserts
// the position it is about rather than merely counting entries, so a red names
// which position went wrong.
func TestConventionsLayerFollowsItsDial(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)

	t.Run("off, the default: no conventions finding reaches a client", func(t *testing.T) {
		srv := conventionsServer(t, root, false)
		if srv.lintOnValidate {
			t.Fatal("precondition: the dial under test is not off")
		}
		diags := validateSavedFile(t, srv, root, rel, "")
		if found := conventionEntries(diags); len(found) != 0 {
			t.Errorf("the dial is off and %d publishing-conventions finding(s) still reached the client:\n  %s",
				len(found), diagSummary(found))
		}
		// The store is empty too, not merely the reply: a client asking for this
		// file's cached diagnostics must be told the same thing.
		if found := conventionEntries(srv.snapshotDiagnostics(filepath.Join(root, rel))); len(found) != 0 {
			t.Errorf("the dial is off and the store holds %d conventions finding(s):\n  %s",
				len(found), diagSummary(found))
		}
	})

	t.Run("on: the findings arrive", func(t *testing.T) {
		srv := conventionsServer(t, root, true)
		diags := validateSavedFile(t, srv, root, rel, "")
		found := conventionEntries(diags)
		if len(found) == 0 {
			t.Fatalf("the dial is on and no publishing-conventions finding reached the client; "+
				"the reply carried:\n  %s", diagSummary(diags))
		}
		// The same answer is cached, so a client that asks again after the edit
		// settles is told the same thing rather than losing the layer.
		if cached := conventionEntries(srv.snapshotDiagnostics(filepath.Join(root, rel))); len(cached) != len(found) {
			t.Errorf("the reply carried %d conventions finding(s) and the store holds %d", len(found), len(cached))
		}
	})
}

// TestConventionsFindingCarriesItsRuleIdentityAndPosition is the parity
// assertion this surface owes the other two: a caller reads which rule objected
// and where from named keys, never by parsing prose.
//
// Both halves are asserted and the second is what makes the first mean
// something. A rule identity folded into the message would satisfy a reading of
// "the client can tell which rule fired" while leaving the client matching text
// to recover it, which is the whole reason the key exists.
//
// The message is compared against the rule's own text as the command line
// carries it, so the two surfaces cannot word one finding differently.
func TestConventionsFindingCarriesItsRuleIdentityAndPosition(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)
	srv := conventionsServer(t, root, true)

	found := conventionEntries(validateSavedFile(t, srv, root, rel, ""))
	if len(found) == 0 {
		t.Fatal("no conventions finding was published, so there is nothing to read keys off")
	}

	var named *Diagnostic
	var codes []string
	for i := range found {
		codes = append(codes, found[i].Code)
		if found[i].Code == "name-shape" {
			named = &found[i]
		}
	}
	if named == nil {
		t.Fatalf("no entry names the naming-shape rule; the codes published were %v", codes)
	}

	// The position. Every finding locates at least the file it concerns, and the
	// entry positions itself inside that file — at the head of it when the rule
	// pinned nothing, which is a position a client can render either way.
	if named.Line < 1 || named.Column < 1 {
		t.Errorf("the entry positions itself at %d:%d; both are 1-based and neither may be zero",
			named.Line, named.Column)
	}

	// Advisory, on the editor wire's own vocabulary.
	if named.Severity != editorSeverityWarning {
		t.Errorf("Severity = %q; a conventions finding published while authoring is advisory", named.Severity)
	}

	// The rule identity travels in the key and not inside the prose.
	if strings.Contains(named.Message, "name-shape") {
		t.Errorf("the rule identity is embedded in the message %q; the key is the only place it travels",
			named.Message)
	}

	// The same finding, as the lint core produced it, is the oracle for the text.
	report := lintCoreReport(t, root, rel)
	var want string
	for _, f := range report.Findings {
		if f.RuleID == "name-shape" {
			want = f.Message
		}
	}
	if want == "" {
		t.Fatal("the lint core produced no naming-shape finding, so there is no oracle to compare against")
	}
	if named.Message != want {
		t.Errorf("the daemon and the lint core word one finding differently\n daemon: %q\n core:   %q",
			named.Message, want)
	}
}

// TestConventionsLayerDoesNotOverFire is the negative half. A clean entity
// publishes nothing, and a document the conventions do not describe publishes
// nothing either — with the dial ON in every leg, so silence is attributable to
// the subject rather than to the dial.
func TestConventionsLayerDoesNotOverFire(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		file    string
		content string
	}{
		{"a clean catalog entity", filepath.Join("catalog", "components", "svc.yaml"), conventionsCleanComponent},
		{"an Aggregate, which the conventions do not describe", "app.yaml", conventionsAggregate},
		{"foreign YAML", filepath.Join("charts", "values.yaml"), conventionsForeignYAML},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeFixtureFile(t, filepath.Join(root, workspaceconfig.ConfigFilename),
				"workspace:\n  catalog_dirs:\n    - ./catalog\n")
			writeFixtureFile(t, filepath.Join(root, tc.file), tc.content)

			srv := conventionsServer(t, root, true)
			diags := validateSavedFile(t, srv, root, tc.file, "")
			if found := conventionEntries(diags); len(found) != 0 {
				t.Errorf("%d conventions finding(s) were published over %s:\n  %s",
					len(found), tc.name, diagSummary(found))
			}
		})
	}
}

// TestConventionsLayerReadsTheBufferNotTheFile pins what makes the layer a
// per-edit answer rather than a per-save one, in both directions.
//
// One direction on its own proves nothing. A corrected buffer over a broken file
// could go quiet because the buffer was read OR because nothing ran at all, and a
// broken buffer over a clean file could report because the buffer was read OR
// because the layer reports indiscriminately. Together they can only both hold if
// the bytes the request carried are the bytes the rules were given.
func TestConventionsLayerReadsTheBufferNotTheFile(t *testing.T) {
	t.Parallel()

	t.Run("a corrected buffer over a broken file goes quiet", func(t *testing.T) {
		t.Parallel()
		root, rel := conventionsWorkspace(t, conventionsBadComponent)
		srv := conventionsServer(t, root, true)
		diags := validateBuffer(t, srv, root, rel, "", conventionsCleanComponent)
		if found := conventionEntries(diags); len(found) != 0 {
			t.Errorf("the buffer is clean and the file on disk is not, and %d finding(s) were published:\n  %s",
				len(found), diagSummary(found))
		}
	})

	t.Run("a broken buffer over a clean file reports", func(t *testing.T) {
		t.Parallel()
		root, rel := conventionsWorkspace(t, conventionsCleanComponent)
		srv := conventionsServer(t, root, true)
		diags := validateBuffer(t, srv, root, rel, "", conventionsBadComponent)
		if found := conventionEntries(diags); len(found) == 0 {
			t.Errorf("the buffer breaks a convention and the file on disk does not, and nothing was "+
				"published; the reply carried:\n  %s", diagSummary(diags))
		}
	})
}

// TestConventionsLayerClearsWhatWasFixed pins the refresh. A finding an author
// has repaired must stop being reported on the next validation rather than
// standing in the editor with nothing to clear it.
func TestConventionsLayerClearsWhatWasFixed(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)
	srv := conventionsServer(t, root, true)

	if found := conventionEntries(validateSavedFile(t, srv, root, rel, "")); len(found) == 0 {
		t.Fatal("precondition: the first validation published nothing, so there is nothing to clear")
	}
	diags := validateBuffer(t, srv, root, rel, "", conventionsCleanComponent)
	if found := conventionEntries(diags); len(found) != 0 {
		t.Errorf("the repaired buffer still carries %d conventions finding(s):\n  %s",
			len(found), diagSummary(found))
	}
	if found := conventionEntries(srv.snapshotDiagnostics(filepath.Join(root, rel))); len(found) != 0 {
		t.Errorf("the store still holds %d conventions finding(s) for a repaired document:\n  %s",
			len(found), diagSummary(found))
	}
}

// TestConventionsAndCorrectnessFindingsAreTellableApart is the assertion that a
// client never sees one problem reported twice.
//
// The fixture is the case where the two questions genuinely meet on one file: a
// catalog entity that breaks a naming convention, validated against a Release
// whose closure cannot resolve — which publishes a correctness finding on
// whatever file was validated. So one reply carries an entry from each layer, and
// what separates them is the source key.
//
// It also asserts no entry is duplicated within the reply, because composing
// three layers at one read is exactly where a file's findings could be appended
// twice and read as two problems.
func TestConventionsAndCorrectnessFindingsAreTellableApart(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)
	// A Release pointing at an Aggregate the workspace does not hold: the closure
	// resolution fails, which is the correctness finding published on the file the
	// request named whatever that file is.
	writeFixtureFile(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: conventions-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: no-such-aggregate
  manifest: no-such-manifest
  boundaries:
    app:
      directory: ./dist/app
      components: {}
`)

	srv := conventionsServer(t, root, true)
	diags := validateSavedFile(t, srv, root, rel, "release.yaml")

	var sawCorrectness, sawConventions bool
	for _, d := range diags {
		switch d.Source {
		case DiagnosticSourceSemantic:
			sawCorrectness = true
		case DiagnosticSourceConventions:
			sawConventions = true
		}
	}
	if !sawCorrectness {
		t.Errorf("the reply carries no correctness finding, so it cannot show the two apart:\n  %s",
			diagSummary(diags))
	}
	if !sawConventions {
		t.Errorf("the reply carries no conventions finding, so it cannot show the two apart:\n  %s",
			diagSummary(diags))
	}

	seen := map[string]int{}
	for _, d := range diags {
		seen[d.Source+"\x00"+d.Code+"\x00"+d.Message]++
	}
	for key, n := range seen {
		if n > 1 {
			parts := strings.Split(key, "\x00")
			t.Errorf("one finding appears %d times in a single reply: [%s/%s] %s", n, parts[0], parts[1], parts[2])
		}
	}
}

// TestConventionsFindingsStayAdvisory pins that a session acquires no halt it
// did not have. A workspace whose entity breaks a convention still validates,
// and its Release still resolves to a graph.
//
// The render half runs with the dial ON, which is the leg that could regress: the
// conventions layer runs on the validation path, and a run that failed the
// request rather than publishing a finding would take the whole session's
// verdict with it.
func TestConventionsFindingsStayAdvisory(t *testing.T) {
	t.Parallel()
	ws, releaseRel := renderDialWorkspace(t)
	componentRel := filepath.Join("catalog", "components", "svc.yaml")

	srv := conventionsServer(t, ws, true)
	// The validation succeeds: handleValidate's error envelope would have failed
	// the helper, and no entry rises above the advisory tier.
	diags := validateSavedFile(t, srv, ws, componentRel, "")
	found := conventionEntries(diags)
	if len(found) == 0 {
		t.Fatalf("precondition: this workspace's entity draws no conventions finding, so the assertions "+
			"below would prove nothing; the reply carried:\n  %s", diagSummary(diags))
	}
	for _, d := range found {
		if d.Severity == editorSeverityError {
			t.Errorf("a conventions finding was published at the halting tier: %s", diagSummary([]Diagnostic{d}))
		}
	}

	// And the graph still resolves, over the same workspace, with the dial on.
	graph := irGraphWithDials(t, ws, releaseRel, func(srv *Server) { srv.lintOnValidate = true })
	if len(graph) == 0 {
		t.Error("the Release produced no graph while the conventions dial was on")
	}
}

// lintOverTheWire drives one aggrega/lint request through the production
// dispatch and returns the decoded result. It fails the test on an error
// envelope.
func lintOverTheWire(t *testing.T, srv *Server, params lintParams) lintResult {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/lint", params))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("aggrega/lint refused the request: [%d] %s", resp.Error.Code, resp.Error.Message)
	}
	var res lintResult
	if err := json.Unmarshal(resp.Result, &res); err != nil {
		t.Fatalf("unmarshal the lint result: %v\nresult: %s", err, resp.Result)
	}
	return res
}

// lintRefusalOverTheWire drives one aggrega/lint request and returns the error
// envelope it was refused with. It fails the test on a successful reply.
func lintRefusalOverTheWire(t *testing.T, srv *Server, params lintParams) (int, string) {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/lint", params))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error == nil {
		t.Fatalf("the request was answered rather than refused: %s", resp.Result)
	}
	return resp.Error.Code, resp.Error.Message
}

// TestLintRequestAnswersWhateverTheDialSays pins that the dedicated request and
// the per-validation layer are one capability reached two ways rather than one
// gated behind the other.
//
// The dial is OFF in this test, which is the point: a caller sending the request
// has asked for the check by name, so a workspace that never asked for the
// per-edit layer still gets an answer. A request that went quiet with the dial
// off would make the capability unreachable in exactly the default configuration.
func TestLintRequestAnswersWhateverTheDialSays(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)
	srv := conventionsServer(t, root, false)

	res := lintOverTheWire(t, srv, lintParams{Entity: rel})

	if res.Halted {
		t.Error("the run reports a halting verdict; the local-content mode is advisory")
	}
	if res.Checked != 1 {
		t.Errorf("Checked = %d; one entity was named", res.Checked)
	}
	if len(res.Files) != 1 {
		t.Fatalf("the answer groups findings across %d file(s); want the one entity that was named: %+v",
			len(res.Files), res.Files)
	}
	if res.Files[0].File != rel {
		t.Errorf("the answer names %q; want the path the request sent (%q)", res.Files[0].File, rel)
	}
	if res.Entity != rel {
		t.Errorf("the answer repeats the subject as %q; want %q", res.Entity, rel)
	}

	var codes []string
	for _, d := range res.Files[0].Diagnostics {
		codes = append(codes, d.Code)
		if d.Source != DiagnosticSourceConventions {
			t.Errorf("an entry in a lint answer names the %q layer: %s", d.Source, diagSummary([]Diagnostic{d}))
		}
		if d.Line < 1 || d.Column < 1 {
			t.Errorf("an entry positions itself at %d:%d; both are 1-based", d.Line, d.Column)
		}
	}
	if !slices.Contains(codes, "name-shape") {
		t.Errorf("no entry names the naming-shape rule; the codes were %v", codes)
	}
}

// TestLintRequestPublishesNothing pins that a question about a subject does not
// rewrite the editor's problem list. The request answers about whatever it was
// asked; the layer a client renders is refreshed by validation and by nothing
// else, so a lint request over one file cannot replace what a validation
// published for another.
func TestLintRequestPublishesNothing(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)
	srv := conventionsServer(t, root, false)

	res := lintOverTheWire(t, srv, lintParams{Entity: rel})
	if len(res.Files) == 0 {
		t.Fatal("precondition: the request found nothing, so publishing nothing would prove nothing")
	}
	if diags := srv.snapshotDiagnostics(filepath.Join(root, rel)); len(diags) != 0 {
		t.Errorf("the lint request published %d diagnostic(s) into the session:\n  %s",
			len(diags), diagSummary(diags))
	}
}

// TestLintRequestOverTheWholeCatalog pins the third subject the capability
// covers. A request naming neither an entity nor a Release checks every catalog
// entity the configured directories hold, which is what the command line does
// with no argument — so this surface answers the same three questions rather than
// a subset of them under the same name.
func TestLintRequestOverTheWholeCatalog(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)
	writeFixtureFile(t, filepath.Join(root, "catalog", "components", "clean.yaml"), conventionsCleanComponent)
	srv := conventionsServer(t, root, false)

	res := lintOverTheWire(t, srv, lintParams{})

	if res.Entity != "" || res.Release != "" {
		t.Errorf("a whole-catalog answer repeats a subject it was not given: entity=%q release=%q",
			res.Entity, res.Release)
	}
	if res.Checked != 2 {
		t.Errorf("Checked = %d; the catalog holds two entities", res.Checked)
	}
	// The findings are attributed to the offending entity and to it alone, which
	// is what makes a whole-catalog answer actionable rather than a bare count.
	for _, f := range res.Files {
		if f.File != rel {
			t.Errorf("findings were attributed to %q; only %q breaks a convention", f.File, rel)
		}
	}
}

// TestLintAnswerSpellsEachFileByContainment pins the rule on the path this
// answer carries: relative to the workspace root for a document inside it, the
// absolute path for one outside, and never a climb above the root.
//
// A catalog directory need not sit inside the workspace root, and that is the
// case this exists for. The inside entity is the positive control — without it,
// an implementation reporting the absolute path for everything would satisfy the
// outside assertion and this would assert nothing about the rule.
//
// The climb is what it asserts against rather than merely emptiness, because the
// climb is non-empty: it spells one path two ways depending on where it is read
// from, and every other surface naming that same file names it absolutely — the
// rules' own message text inside this very answer included.
func TestLintAnswerSpellsEachFileByContainment(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	root := filepath.Join(base, "workspace")
	outsideDir := filepath.Join(base, "shared-catalog")

	writeFixtureFile(t, filepath.Join(root, workspaceconfig.ConfigFilename),
		"workspace:\n  catalog_dirs:\n    - ../shared-catalog\n    - ./catalog\n")
	insideRel := filepath.Join("catalog", "components", "svc.yaml")
	writeFixtureFile(t, filepath.Join(root, insideRel), conventionsBadComponent)
	outsideAbs := filepath.Join(outsideDir, "components", "svc-elsewhere.yaml")
	writeFixtureFile(t, outsideAbs, conventionsBadComponent)

	srv := conventionsServer(t, root, false)
	res := lintOverTheWire(t, srv, lintParams{})

	if res.Checked != 2 {
		t.Fatalf("Checked = %d; the two configured directories hold one entity each, so a different "+
			"number means this is not measuring what it claims", res.Checked)
	}
	named := map[string]bool{}
	for _, f := range res.Files {
		named[f.File] = true
		if strings.Contains(f.File, "..") {
			t.Errorf("the answer names %q, which climbs above the workspace root; a climb resolves "+
				"differently depending on where it is read from", f.File)
		}
	}
	if !named[insideRel] {
		t.Errorf("no entry names the inside entity at %q; the answer named %v", insideRel, keysOf(named))
	}
	if !named[filepath.Clean(outsideAbs)] {
		t.Errorf("no entry names the outside entity at its absolute path %q; the answer named %v",
			filepath.Clean(outsideAbs), keysOf(named))
	}
}

// keysOf reports a set's members, for a failure message that says what WAS named
// rather than only what was missing.
func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// TestLintRequestRefusesWhatItCannotAnswer drives each refusal, because a
// refusal nobody has seen fire is decoration. The first row is the positive
// control: a well-formed request must still be answered, or a handler that
// refused everything would satisfy every other row.
func TestLintRequestRefusesWhatItCannotAnswer(t *testing.T) {
	t.Parallel()
	root, rel := conventionsWorkspace(t, conventionsBadComponent)

	for _, tc := range []struct {
		name    string
		params  lintParams
		refuses string
	}{
		{"a well-formed request is answered", lintParams{Entity: rel}, ""},
		{
			"two subjects at once",
			lintParams{Entity: rel, Release: "release.yaml"},
			"name different subjects",
		},
		{
			"the pre-publication comparison with no entity to compare",
			lintParams{PrePublish: true},
			"requires entity",
		},
		{
			"an absolute entity path",
			lintParams{Entity: filepath.Join(root, rel)},
			"is an absolute path",
		},
		{
			"an entity path climbing out of the workspace",
			lintParams{Entity: filepath.Join("..", "elsewhere.yaml")},
			"resolves outside the workspace root",
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := conventionsServer(t, root, false)
			if tc.refuses == "" {
				lintOverTheWire(t, srv, tc.params)
				return
			}
			code, message := lintRefusalOverTheWire(t, srv, tc.params)
			if code != jsonRPCInvalidParams {
				t.Errorf("the refusal carries code %d; a request the client corrects is invalid-params", code)
			}
			if !strings.Contains(message, tc.refuses) {
				t.Errorf("the refusal does not name what was wrong: want %q in %q", tc.refuses, message)
			}
		})
	}
}
