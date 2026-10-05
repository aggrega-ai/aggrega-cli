// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// Three things about who files a refusal on a document, and how often: a refusal
// joining several findings is published finding by finding, each where it points;
// a fault a build reports on a document is not reported a second time by that
// document's own validation; and a validation that stops at the document's parse or
// envelope answers with every layer standing on the document, not its own alone.
//
// Expectations are read off the fixture — the token each fault authors, the line
// each key sits on — and off the conditions' own registrations, never off the code
// under test.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/topology"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
)

// The fixture's documents, by role, relative to the workspace renderDialWorkspace lays.
const (
	fixtureRelease   = "app-release.yaml"
	fixtureAggregate = "app-agg.yaml"
	fixtureManifest  = "app-manifest.yaml"
	fixtureRecipe    = "catalog/recipes/server.yaml"
	fixtureComponent = "catalog/components/svc.yaml"
)

// declareModelsSocket gives the fixture's Component a socket an entry can be wired
// into, so an entry naming a target nothing answers to is refused for its target.
func declareModelsSocket(t *testing.T, ws string) {
	t.Helper()
	rewriteFixture(t, filepath.Join(ws, fixtureComponent), "spec:\n",
		"spec:\n"+
			"  usesSchema:\n"+
			"    models:\n"+
			"      selector: \"component.aggrega.ai/type=server\"\n")
}

// releaseEntries writes the given entries under the Release's one component and
// returns the Release's bytes.
func releaseEntries(t *testing.T, ws string, entries ...string) string {
	t.Helper()
	declareModelsSocket(t, ws)
	var b strings.Builder
	b.WriteString("      components:\n        app:\n          uses:\n            models:\n")
	for _, e := range entries {
		b.WriteString("              " + e + ":\n                target: no-" + e + "\n")
	}
	return rewriteFixture(t, filepath.Join(ws, fixtureRelease), "      components:\n        app: {}\n", b.String())
}

// joinedFinding is one finding a joined refusal must be published as.
type joinedFinding struct {
	document, key, token string
}

// TestARefusalJoiningFindingsIsPublishedWhereEachFindingPoints drives the graph
// request over refusals the composition of the graph raises — one finding, two
// joined, and one written in the Aggregate — and holds the answer and the build
// layer to where each finding points: one entry per finding, each on its own
// document at its own key, in its own words and with its own codes.
func TestARefusalJoiningFindingsIsPublishedWhereEachFindingPoints(t *testing.T) {
	boundaryKey := func(alias string) string {
		return ".spec.boundaries.app.components.app.uses.models." + alias
	}
	for _, row := range []struct {
		name  string
		write func(t *testing.T, ws string) []joinedFinding
	}{
		{
			name:  "a Release that composes",
			write: func(t *testing.T, ws string) []joinedFinding { return nil },
		},
		{
			name: "a Release entry naming a target nothing answers to",
			write: func(t *testing.T, ws string) []joinedFinding {
				releaseEntries(t, ws, "local")
				return []joinedFinding{{fixtureRelease, boundaryKey("local"), "no-local"}}
			},
		},
		{
			name: "two such entries, joined into one refusal",
			write: func(t *testing.T, ws string) []joinedFinding {
				releaseEntries(t, ws, "local", "other")
				return []joinedFinding{
					{fixtureRelease, boundaryKey("local"), "no-local"},
					{fixtureRelease, boundaryKey("other"), "no-other"},
				}
			},
		},
		{
			name: "such an entry written in the Aggregate",
			write: func(t *testing.T, ws string) []joinedFinding {
				declareModelsSocket(t, ws)
				rewriteFixture(t, filepath.Join(ws, fixtureAggregate), "      type: \"demo/svc:1.0.0\"\n",
					"      type: \"demo/svc:1.0.0\"\n"+
						"      uses:\n"+
						"        models:\n"+
						"          local:\n"+
						"            target: no-local\n")
				return []joinedFinding{{fixtureAggregate, ".spec.components.app.uses.models.local", "no-local"}}
			},
		},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			ws, releaseRel := renderDialWorkspace(t)
			findings := row.write(t, ws)
			var in, out bytes.Buffer
			srv := newPinIsolationServer(t, ws, &in, &out, 1)

			answer, pushed := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel})
			if len(findings) == 0 {
				if answer.Error != nil {
					t.Fatalf("the control refused: code %d: %s", answer.Error.Code, answer.Error.Message)
				}
				return
			}
			if answer.Error == nil {
				t.Fatalf("the fault did not refuse; result: %s", answer.Result)
			}
			code, _ := cerrors.CodeOf(topology.ErrDanglingUsesTarget())
			shortCode, _ := cerrors.ShortCodeOf(topology.ErrDanglingUsesTarget())

			// The answer: every finding in its words, placed at the first finding.
			first := findings[0]
			firstDoc := filepath.Join(ws, first.document)
			for _, f := range findings {
				if !strings.Contains(answer.Error.Message, f.token) {
					t.Errorf("the answer drops the finding naming %q:\n%s", f.token, answer.Error.Message)
				}
			}
			content, err := os.ReadFile(firstDoc)
			if err != nil {
				t.Fatal(err)
			}
			wantLine, wantColumn := keyPosition(t, string(content), lastSegment(first.key)+":")
			if d := answer.Error.Data; d == nil || filepath.Clean(d.File) != firstDoc || d.Line != wantLine || d.Column != wantColumn {
				t.Errorf("the answer is placed at %+v; want %s at %d:%d", answer.Error.Data, firstDoc, wantLine, wantColumn)
			}

			// The build layer: one entry per finding, each at its own key.
			entries, published := lastPublicationFor(pushed, firstDoc)
			if !published {
				t.Fatalf("nothing was published on %s; published on: %v", firstDoc, publishedFiles(pushed))
			}
			var refusals []Diagnostic
			for _, d := range entries {
				if d.Severity == editorSeverityError {
					refusals = append(refusals, d)
				}
			}
			if len(refusals) != len(findings) {
				t.Fatalf("the layer carries %d error entries on %s; want one per finding (%d): %+v",
					len(refusals), firstDoc, len(findings), entries)
			}
			for i, f := range findings {
				got := refusals[i]
				line, column := keyPosition(t, string(content), lastSegment(f.key)+":")
				if got.Source != DiagnosticSourceBuild || got.KeyPath != f.key || got.Line != line || got.Column != column {
					t.Errorf("finding %d is published as %s %q at %d:%d; want %s %q at %d:%d",
						i, got.Source, got.KeyPath, got.Line, got.Column, DiagnosticSourceBuild, f.key, line, column)
				}
				if got.Code != code || got.ShortCode != shortCode {
					t.Errorf("finding %d names %q %q; the condition is %q %q", i, got.Code, got.ShortCode, code, shortCode)
				}
				if !strings.Contains(got.Message, f.token) {
					t.Errorf("finding %d is not in its own words (%q): %s", i, f.token, got.Message)
				}
				for j, other := range findings {
					if j != i && strings.Contains(got.Message, other.token) {
						t.Errorf("finding %d carries another finding's words (%q): %s", i, other.token, got.Message)
					}
				}
			}
			if aggregate := filepath.Join(ws, fixtureAggregate); aggregate != firstDoc {
				if entries, ok := lastPublicationFor(pushed, aggregate); ok {
					for _, d := range entries {
						if d.Severity == editorSeverityError {
							t.Errorf("a finding was filed on the Aggregate as well: %+v", d)
						}
					}
				}
			}
		})
	}
}

// lastSegment returns the last key of a dotted key path.
func lastSegment(key string) string {
	parts := strings.Split(key, ".")
	return parts[len(parts)-1]
}

// validateAnswer sends one validation of document's bytes on disk, or of buffer
// where it is not empty, in the terms of the fixture's Release, and returns the
// entries the answer carries.
func validateAnswer(t *testing.T, srv *Server, ws, document, buffer string) []Diagnostic {
	t.Helper()
	if buffer == "" {
		body, err := os.ReadFile(filepath.Join(ws, document))
		if err != nil {
			t.Fatal(err)
		}
		buffer = string(body)
	}
	answer, _ := driveRefusalRequest(t, srv, "aggrega/validate",
		validateParams{File: document, Content: buffer, Release: fixtureRelease})
	if answer.Error != nil {
		t.Fatalf("the validation of %s was refused: %s", document, answer.Error.Message)
	}
	var result validateResult
	if err := json.Unmarshal(answer.Result, &result); err != nil {
		t.Fatalf("decode the validation's answer: %v (%s)", err, answer.Result)
	}
	return result.Diagnostics
}

// naming returns the entries naming code, grouped by the layer that reported them.
func naming(entries []Diagnostic, code string) map[string][]Diagnostic {
	out := map[string][]Diagnostic{}
	for _, d := range entries {
		if d.Code == code {
			out[d.Source] = append(out[d.Source], d)
		}
	}
	return out
}

// onceFault is one fault both a document's own validation and a build report, the
// document whose validation is read, and the condition it names.
type onceFault struct {
	name      string
	write     func(t *testing.T, ws string) (document string)
	condition error
	dial      bool
	// fromBuild is the layer the one report must come from once a build has
	// refused: the build layer, or none at all where the refusal is an answer the
	// client files itself.
	fromBuild string
}

// onceFaults is the table the reports-once tests read, control first.
func onceFaults() []onceFault {
	return []onceFault{
		{
			name:  "a document no build refuses for",
			write: func(t *testing.T, ws string) string { return fixtureRelease },
		},
		{
			name: "a Release entry naming a target nothing answers to",
			write: func(t *testing.T, ws string) string {
				releaseEntries(t, ws, "local")
				return fixtureRelease
			},
			condition: topology.ErrDanglingUsesTarget(),
			fromBuild: DiagnosticSourceBuild,
		},
		{
			name: "a Recipe whose Workload names a slot its Component does not declare",
			write: func(t *testing.T, ws string) string {
				renderRefusalFaults()[1].write(t, ws)
				return fixtureRecipe
			},
			condition: validator.ErrWorkloadFeaturesSchema(),
			dial:      true,
			fromBuild: DiagnosticSourceBuild,
		},
		{
			name: "a Release whose dependencies do not resolve",
			write: func(t *testing.T, ws string) string {
				rewriteFixture(t, filepath.Join(ws, fixtureManifest), "demo/svc: demo-server-synthesized\n",
					"demo/svc: no-such-recipe:1.0.0\n")
				return fixtureRelease
			},
			condition: closure.ErrUnresolvableURI(),
		},
	}
}

// TestAFaultABuildReportsIsReportedOnceOnItsDocument holds a document's own
// validation to the fault a build reports there: before any build the validation
// reports it, and once a build has refused for it the document carries it once —
// the build's report, never the validation's beside it.
func TestAFaultABuildReportsIsReportedOnceOnItsDocument(t *testing.T) {
	for _, fault := range onceFaults() {
		fault := fault
		t.Run(fault.name, func(t *testing.T) {
			ws, releaseRel := renderDialWorkspace(t)
			document := fault.write(t, ws)
			var in, out bytes.Buffer
			srv := newPinIsolationServer(t, ws, &in, &out, 1)
			srv.buildOnInspect = fault.dial

			code := ""
			if fault.condition != nil {
				code, _ = cerrors.CodeOf(fault.condition)
			}
			before := validateAnswer(t, srv, ws, document, "")
			if fault.condition == nil {
				for _, d := range before {
					if d.Severity == editorSeverityError {
						t.Fatalf("the control's validation reports %+v", d)
					}
				}
			} else if got := naming(before, code); len(got[DiagnosticSourceSemantic]) != 1 || len(got) != 1 {
				t.Fatalf("before any build the document's own validation must report the fault once: %+v", before)
			}

			answer, pushed := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel})
			if fault.condition == nil {
				if answer.Error != nil {
					t.Fatalf("the control refused: %s", answer.Error.Message)
				}
				return
			}
			if answer.Error == nil {
				t.Fatalf("the fault did not refuse: %s", answer.Result)
			}

			after := validateAnswer(t, srv, ws, document, "")
			got := naming(after, code)
			if len(got[DiagnosticSourceSemantic]) != 0 {
				t.Errorf("the document's own validation reports again a fault a build reports: %+v", after)
			}
			switch fault.fromBuild {
			case DiagnosticSourceBuild:
				if len(got[DiagnosticSourceBuild]) != 1 {
					t.Errorf("the build's one report is not on the document: %+v", after)
				}
			default:
				if len(got) != 0 {
					t.Errorf("the refusal answered to the request is the one report; the document carries %+v", after)
				}
			}
			// What the client was last told about the document says the same.
			if entries, ok := lastPublicationFor(pushed, filepath.Join(ws, document)); ok {
				if n := len(naming(entries, code)[DiagnosticSourceSemantic]); n != 0 {
					t.Errorf("the publication on %s carries the validation's report beside the build's: %+v", document, entries)
				}
			}
		})
	}
}

// TestADocumentReportsItsFaultAgainOnceNoBuildReportsIt holds the other half: the
// omission lasts exactly as long as a build reports the fault. A dependency refusal
// is replaced by the next answer for the Release — here, a refusal for a lockfile
// that is gone — and forgotten when a client connects, since that client was
// answered no refusal; a build-layer refusal is replaced by the next run's layer —
// here, an inspection that does not render. After each, the document's own
// finding, still standing, is published and reported again.
func TestADocumentReportsItsFaultAgainOnceNoBuildReportsIt(t *testing.T) {
	faults := onceFaults()
	graphAgain := func(t *testing.T, srv *Server, releaseRel string) []publishDiagnosticsParams {
		_, pushed := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel})
		return pushed
	}
	for _, row := range []struct {
		name  string
		fault onceFault
		// stop ends every build report of the fault while the fault still stands,
		// and returns what was published while it did.
		stop func(t *testing.T, srv *Server, ws, releaseRel string) []publishDiagnosticsParams
	}{
		{
			name:  "the next answer for the Release refuses for something else",
			fault: faults[3],
			stop: func(t *testing.T, srv *Server, ws, releaseRel string) []publishDiagnosticsParams {
				if err := os.Remove(lockfile.PathFor(filepath.Join(ws, releaseRel))); err != nil {
					t.Fatal(err)
				}
				return graphAgain(t, srv, releaseRel)
			},
		},
		{
			name:  "a client connects, having been answered no refusal",
			fault: faults[3],
			stop: func(t *testing.T, srv *Server, ws, releaseRel string) []publishDiagnosticsParams {
				root := ws
				_, pushed := driveRefusalRequest(t, srv, "aggrega/initialize", initializeParams{WorkspaceRoot: &root})
				return pushed
			},
		},
		{
			name:  "the next inspection does not render",
			fault: faults[2],
			stop: func(t *testing.T, srv *Server, ws, releaseRel string) []publishDiagnosticsParams {
				srv.buildOnInspect = false
				return graphAgain(t, srv, releaseRel)
			},
		},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			ws, releaseRel := renderDialWorkspace(t)
			document := row.fault.write(t, ws)
			var in, out bytes.Buffer
			srv := newPinIsolationServer(t, ws, &in, &out, 1)
			srv.buildOnInspect = row.fault.dial
			code, _ := cerrors.CodeOf(row.fault.condition)

			validateAnswer(t, srv, ws, document, "")
			if answer, _ := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel}); answer.Error == nil {
				t.Fatal("precondition: the fault did not refuse")
			}
			if n := len(naming(srv.snapshotDiagnostics(filepath.Join(ws, document)), code)[DiagnosticSourceSemantic]); n != 0 {
				t.Fatalf("precondition: the document's own report was not omitted while a build reported the fault")
			}

			pushed := row.stop(t, srv, ws, releaseRel)

			entries, published := lastPublicationFor(pushed, filepath.Join(ws, document))
			if !published || len(naming(entries, code)[DiagnosticSourceSemantic]) != 1 {
				t.Errorf("once no build reports the fault, the document's own report is not published again on %s: %+v (published on %v)",
					document, entries, publishedFiles(pushed))
			}
			if got := naming(validateAnswer(t, srv, ws, document, ""), code); len(got[DiagnosticSourceSemantic]) != 1 {
				t.Errorf("once no build reports the fault, the document's own validation does not report it: %+v", got)
			}
		})
	}
}

// TestADependencyRefusalIsPublishedOnlyWhereItMovesWhatADocumentReports holds the
// record of a dependency refusal to publishing a document only where recording or
// clearing it changes what that document reports. The refusal stands in for the
// document's own finding and is never an entry of its own: it moves a Release whose
// own validation reported the fault, and nothing on a Release that reported nothing,
// where a publication would tell a client about a document nothing reported on,
// ahead of the answer the client is waiting for.
func TestADependencyRefusalIsPublishedOnlyWhereItMovesWhatADocumentReports(t *testing.T) {
	unresolvable := onceFaults()[3]
	code, _ := cerrors.CodeOf(unresolvable.condition)
	for _, row := range []struct {
		name string
		// validated runs the Release's own validation before the refusing request.
		validated bool
		// clear follows the refusing request with one refusing for something else,
		// which replaces the record.
		clear bool
		// published is whether the Release must be published: with the refusal's
		// finding left out where true, and never where false.
		published bool
	}{
		{
			name:      "the Release's own validation reported the fault, which the refusal leaves out",
			validated: true,
			published: true,
		},
		{
			name: "nothing on the Release reported the fault when the refusal is recorded",
		},
		{
			name:  "nothing on the Release reported the fault when the refusal is recorded or cleared",
			clear: true,
		},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			ws, releaseRel := renderDialWorkspace(t)
			document := unresolvable.write(t, ws)
			release := filepath.Join(ws, document)
			var in, out bytes.Buffer
			srv := newPinIsolationServer(t, ws, &in, &out, 1)

			if row.validated {
				if got := naming(validateAnswer(t, srv, ws, document, ""), code); len(got[DiagnosticSourceSemantic]) != 1 || len(got) != 1 {
					t.Fatalf("precondition: the Release's own validation does not report the fault once: %+v", got)
				}
			}
			answer, pushed := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel})
			if answer.Error == nil || answer.Error.Code != errEntityNotResolvable {
				t.Fatalf("precondition: the graph request did not refuse for a dependency: %+v (result %s)", answer.Error, answer.Result)
			}
			if row.clear {
				if err := os.Remove(lockfile.PathFor(filepath.Join(ws, releaseRel))); err != nil {
					t.Fatal(err)
				}
				again, pushedAgain := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel})
				if again.Error == nil || again.Error.Code != errLockfileAbsent {
					t.Fatalf("precondition: the next graph request did not refuse for the lockfile: %+v (result %s)", again.Error, again.Result)
				}
				pushed = append(pushed, pushedAgain...)
			}

			entries, published := lastPublicationFor(pushed, release)
			switch {
			case row.published && !published:
				t.Errorf("the refusal left the Release's own report out, and the Release was not published; published on %v",
					publishedFiles(pushed))
			case row.published:
				if got := naming(entries, code); len(got) != 0 {
					t.Errorf("the Release is published still reporting the fault the refusal stands for: %+v", entries)
				}
			case published:
				t.Errorf("the Release was published though nothing it reports moved: %+v", entries)
			}
		})
	}
}

// TestAValidationStoppedByItsDocumentKeepsTheBuildsEntries holds a validation that
// stops at its own document — the buffer does not parse, or its envelope is refused
// — to answering with every layer standing on the document: a client replaces each
// layer from the answer, and a validation that learned nothing about the build
// must not take the build's entry off the document.
func TestAValidationStoppedByItsDocumentKeepsTheBuildsEntries(t *testing.T) {
	for _, row := range []struct {
		name string
		// buffer turns the Recipe's bytes into the buffer validated, and returns it.
		buffer func(recipe string) string
		// own is a token of the validation's own finding, or "" for none.
		own string
	}{
		{name: "a buffer that validates", buffer: func(recipe string) string { return recipe }},
		{
			name: "a buffer that does not parse",
			buffer: func(string) string {
				return "apiVersion: aggrega.ai/v1alpha1\nkind: Recipe\nmetadata:\n  name: [unclosed\n"
			},
			own: "yaml:",
		},
		{
			name: "a buffer whose envelope is refused",
			buffer: func(recipe string) string {
				return strings.Replace(recipe, "  capability: helm\n", "  capability: helm\n  unknown_key: true\n", 1)
			},
			own: "unknown_key",
		},
	} {
		row := row
		t.Run(row.name, func(t *testing.T) {
			ws, releaseRel := renderDialWorkspace(t)
			renderRefusalFaults()[1].write(t, ws)
			var in, out bytes.Buffer
			srv := newPinIsolationServer(t, ws, &in, &out, 1)
			srv.buildOnInspect = true
			if answer, _ := driveRefusalRequest(t, srv, "aggrega/irGraph", irGraphParams{Release: releaseRel}); answer.Error == nil {
				t.Fatal("precondition: the fault did not refuse, so no build entry stands on the Recipe")
			}
			recipe, err := os.ReadFile(filepath.Join(ws, fixtureRecipe))
			if err != nil {
				t.Fatal(err)
			}
			buffer := row.buffer(string(recipe))
			if buffer == string(recipe) && row.own != "" {
				t.Fatal("the row's buffer is the file unchanged; its fault would not be written")
			}

			got := validateAnswer(t, srv, ws, fixtureRecipe, buffer)
			code, _ := cerrors.CodeOf(validator.ErrWorkloadFeaturesSchema())
			if len(naming(got, code)[DiagnosticSourceBuild]) != 1 {
				t.Errorf("the answer drops the build's entry standing on the Recipe: %+v", got)
			}
			if row.own != "" {
				found := false
				for _, d := range got {
					if d.Source == DiagnosticSourceSemantic && strings.Contains(d.Message, row.own) {
						found = true
					}
				}
				if !found {
					t.Errorf("the answer drops the validation's own finding (%q): %+v", row.own, got)
				}
			}
		})
	}
}
