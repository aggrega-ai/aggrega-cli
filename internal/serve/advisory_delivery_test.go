// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// The daemon's half of the one advisory channel.
//
// Two properties are proved here and they pull in opposite directions, which is
// why both are needed. An advisory that names an authoring key must reach the
// editor ON that key — losing the position is the regression a single channel
// invites, because the shape it replaced recovered the position and a naive
// collapse flattens it. And an advisory that names no key must still ARRIVE,
// placed at the head of the file — dropping it is the opposite regression, and a
// test that only checked positioning would pass on a surface that silently
// discarded every unpositioned entry.

// nearMissWorkspace copies the slot fixture into a temp workspace, replaces the
// Aggregate with the variant authoring an attachment one edit from a declared
// slot, and returns a server wired to it plus the Aggregate's path and bytes.
func nearMissWorkspace(t *testing.T) (*Server, string, []byte) {
	t.Helper()
	tmp := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	fixtureDir := filepath.Join(cwd, "testdata", "tier4", "required-attachment-slot")

	for _, rel := range []string{
		"release.yaml", "manifest.yaml", ".aggrega-config.yaml",
		"catalog/components/slotted-db.yaml",
		"catalog/features/pw.yaml",
	} {
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

	// The semantic pass reads the Aggregate from disk, so the variant is written
	// to the Aggregate's own path rather than passed only as buffer content.
	variant, err := os.ReadFile(filepath.Join(fixtureDir, "aggregate-nearmiss.yaml"))
	if err != nil {
		t.Fatalf("read near-miss variant: %v", err)
	}
	aggPath := filepath.Join(tmp, "aggregate.yaml")
	if err := os.WriteFile(aggPath, variant, 0o644); err != nil {
		t.Fatalf("write aggregate: %v", err)
	}

	srv := newTestServer(t, tmp, nil, nil)
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(tmp, "catalog")}
	srv.lazyCatalog = catalog.NewLazyLookup(tmp, "", []string{filepath.Join(tmp, "catalog")}, nil)
	return srv, aggPath, variant
}

// TestValidateCarriesAnAdvisoryOnItsAuthoringKey drives aggrega/validate over a
// document authoring an attachment one edit from a declared slot, and pins the
// whole crossing: the advisory reaches the editor, at the warning tier, ON the
// offending key.
//
// The position is the assertion that matters. An advisory the editor places at
// the head of the file is an advisory the author cannot navigate to, and the
// finding carries a path into the document rather than a resolved line — so the
// position exists only if the surface holding the parsed file walks that path.
// This is the proof that it does.
func TestValidateCarriesAnAdvisoryOnItsAuthoringKey(t *testing.T) {
	t.Parallel()
	srv, aggPath, aggBytes := nearMissWorkspace(t)

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
	var result validateResult
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		t.Fatalf("unmarshal result: %v", err)
	}

	// Found by the near miss the author has to act on rather than by a registered
	// sentence, because the pair of spellings is what makes the finding usable and
	// a sentence alone would read identically for every near miss in every
	// document.
	var advisory *Diagnostic
	for i := range result.Diagnostics {
		if strings.Contains(result.Diagnostics[i].Message, "user_passwrod") &&
			strings.Contains(result.Diagnostics[i].Message, "user_password") {
			advisory = &result.Diagnostics[i]
			break
		}
	}
	if advisory == nil {
		t.Fatalf("no diagnostic names both the authored spelling and the declared one; got %v",
			diagMessages(result.Diagnostics))
	}

	if advisory.Severity != editorSeverityWarning {
		t.Errorf("advisory severity = %q; want %q — a near miss must never publish as an error, or it "+
			"refuses authoring the compiler deliberately permits", advisory.Severity, editorSeverityWarning)
	}

	// Positioned on the misspelled key, not at the head of the file. The line is
	// read out of the fixture rather than written here, so the assertion survives
	// an edit that moves the key and fails when the walk stops working.
	wantLine := fixtureKeyLine(t, string(aggBytes), "user_passwrod:")
	if advisory.Line != wantLine {
		t.Errorf("advisory at line %d; want the `user_passwrod:` key at line %d. An advisory the editor "+
			"cannot point at is not usable in the editor (diagnostics: %v)",
			advisory.Line, wantLine, diagMessages(result.Diagnostics))
	}
	if advisory.Line == 1 && advisory.Column == 1 {
		t.Error("advisory landed at the head of the file; the finding carries a path into the document, so " +
			"the position was available and was lost")
	}
	// The span covers the offending token, which is what makes the editor
	// highlight the word rather than a point.
	if advisory.EndColumn <= advisory.Column {
		t.Errorf("advisory span = %d:%d-%d:%d; want an end column past its start so the offending token is "+
			"highlighted", advisory.Line, advisory.Column, advisory.EndLine, advisory.EndColumn)
	}
}

// TestPublishedAdvisoriesRecoverAKeyPathAndFallBackWithoutOne pins both
// directions of the build path's positioning in one place, because they are one
// decision and separating them lets either drift.
//
// The entry carrying a path into the document is walked to its key. The entry
// carrying none — every finding about the shape of the deployed topology, whose
// coordinates are graph coordinates and not document ones — still arrives, at the
// head of the file. A surface that dropped it would lose the render's entire
// advisory class while this file's other assertions all passed.
func TestPublishedAdvisoriesRecoverAKeyPathAndFallBackWithoutOne(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	const file = "/w/aggregate.yaml"
	content := []byte("spec:\n  components:\n    db:\n      features:\n        pw: {}\n")

	s.publishRenderDiagnostics(ctx, file, []renderer.Diagnostic{
		{
			Severity: abi.SeverityWarn,
			Message:  "an advisory that named an authoring key",
			Location: &renderer.DiagnosticLocation{KeyPath: ".spec.components.db.features.pw"},
		},
		{
			Severity: abi.SeverityWarn,
			Message:  "an advisory that named a graph coordinate",
			Location: &renderer.DiagnosticLocation{Instance: "db", Kind: "helm"},
		},
		{Severity: abi.SeverityWarn, Message: "an advisory that named nothing at all"},
	}, nil, content, nil)
	flush()

	got := s.snapshotDiagnostics(file)
	if len(got) != 3 {
		t.Fatalf("published %d diagnostics; want 3 — an unpositioned advisory must still arrive", len(got))
	}

	// The keyed entry lands on its key.
	if got[0].Line != 5 {
		t.Errorf("keyed advisory at line %d; want 5 (the `pw:` key)", got[0].Line)
	}
	if got[0].EndColumn <= got[0].Column {
		t.Errorf("keyed advisory span = %d:%d-%d:%d; want the key token spanned",
			got[0].Line, got[0].Column, got[0].EndLine, got[0].EndColumn)
	}

	// The graph-coordinate entry falls back to the head of the file AND keeps its
	// attribution in the text, which is where a reader finds which node it is
	// about now that the position cannot say.
	if got[1].Line != 1 || got[1].Column != 1 {
		t.Errorf("graph-coordinate advisory at %d:%d; want the head of the file, since mapping a node back "+
			"to an authoring position is a capability this surface does not have", got[1].Line, got[1].Column)
	}
	if !strings.Contains(got[1].Message, "db") {
		t.Errorf("graph-coordinate advisory message = %q; want it to name the node, which is the only place "+
			"that coordinate survives once the position is the file head", got[1].Message)
	}

	// The entry naming nothing arrives too.
	if got[2].Line != 1 || got[2].Column != 1 {
		t.Errorf("unpositioned advisory at %d:%d; want the head of the file", got[2].Line, got[2].Column)
	}
	if got[2].Message != "an advisory that named nothing at all" {
		t.Errorf("unpositioned advisory message = %q; want it carried verbatim", got[2].Message)
	}
}

// advisoryGraphWorkspace builds the self-contained workspace the graph request
// resolves against, with one parameter declaration no reference reads spliced
// into the Aggregate so the run raises an advisory without failing.
//
// It mirrors the render-dial workspace rather than reusing it because the
// advisory has to be authored INTO the Aggregate, and that fixture writes its
// documents inline.
func advisoryGraphWorkspace(t *testing.T) (ws, releaseRel string) {
	t.Helper()
	ws = t.TempDir()

	catalogDir := filepath.Join(ws, "catalog")
	const svcComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    provides:
      host: { type: string, required: true }
`
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "svc.yaml"), svcComponent)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/svc
  provides:
    host: "{{ .context.instance_id }}"
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-server:latest
`)
	writeFixtureFile(t, filepath.Join(ws, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: app-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        demo/svc: demo-server-synthesized
`)
	// The unread declaration is the advisory this workspace exists to raise.
	writeFixtureFile(t, filepath.Join(ws, "app-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  parameters:
    unread_probe:
      type: string
  components:
    app:
      type: "demo/svc:1.0.0"
`)
	relPath := filepath.Join(ws, "app-release.yaml")
	writeFixtureFile(t, relPath, `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: app-release
  environment: production
  version: "1.0.0"
spec:
  aggregate: app-agg
  manifest: app-manifest
  boundaries:
    app:
      directory: ./dist/app
      components:
        app: {}
`)
	// The row records the component's own content digest, as a pull would: the
	// lockfile refuses a row without a well-formed one.
	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./app-release.yaml",
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities: []lockfile.Entity{{
			Kind: "component", URI: "demo/svc:1.0.0", ResolvedVersion: "1.0.0", Digest: contentDigestHex([]byte(svcComponent)),
		}},
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(relPath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	return ws, filepath.Base(relPath)
}

// TestGraphRequestPublishesTheAdvisoriesTheRunRaised is the proof for the second
// surface that structurally could not deliver these findings.
//
// The graph request runs the pre-render phases and returns a graph; the advisories
// those phases raise reached the process diagnostic stream and nothing a client
// can read. This drives the request over the wire and asserts the finding arrives
// in what the client is actually sent.
//
// It reads the emitted frames rather than the server's own map, because the map is
// an implementation detail and a client cannot see it: an entry set in the map and
// never pushed would satisfy an assertion over the map and deliver nothing.
func TestGraphRequestPublishesTheAdvisoriesTheRunRaised(t *testing.T) {
	t.Parallel()
	ws, releaseRel := advisoryGraphWorkspace(t)

	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: releaseRel}))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, &out)

	pushed := out.String()
	if !strings.Contains(pushed, "unread_probe") {
		t.Errorf("the graph request pushed nothing naming the advisory the run raised; a client driving the "+
			"compiler here receives no findings at all.\npushed:\n%s", pushed)
	}
	// The advisory must arrive at the warning tier. Published as an error it would
	// read as a reason the graph is wrong, which it is not.
	if !strings.Contains(pushed, `"severity":"warning"`) {
		t.Errorf("no pushed diagnostic carries the warning tier; an exit-code-neutral finding published as an "+
			"error misreports what it is.\npushed:\n%s", pushed)
	}
}

// TestGraphRequestPublishesNothingForACleanRelease is the calibration for the
// proof above: the same workspace without the unread declaration must push no
// such finding, or the assertion there could be passing on unrelated output.
func TestGraphRequestPublishesNothingForACleanRelease(t *testing.T) {
	t.Parallel()
	ws, releaseRel := renderDialWorkspace(t)

	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: releaseRel}))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var req jsonRPCRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &req, &out)

	if pushed := out.String(); strings.Contains(pushed, "unread_probe") {
		t.Errorf("a Release authoring no advisory pushed one; the fixture is not clean.\npushed:\n%s", pushed)
	}
}

// TestPublishedAdvisoriesFallBackWhenTheDocumentDoesNotParse is the calibration
// for the recovery above: an advisory must not be withheld because the document
// it points into could not be read.
//
// A buffer mid-edit does not parse, and that is the normal state of an editor
// session rather than an exceptional one. Withholding the findings until the
// document parses would make them disappear exactly while the author is typing.
func TestPublishedAdvisoriesFallBackWhenTheDocumentDoesNotParse(t *testing.T) {
	t.Parallel()
	s := layeredServer()
	var out bytes.Buffer
	ctx, flush := serializerContext(t, &out)
	const file = "/w/aggregate.yaml"

	s.publishRenderDiagnostics(ctx, file, []renderer.Diagnostic{{
		Severity: abi.SeverityWarn,
		Message:  "an advisory about a document that will not parse",
		Location: &renderer.DiagnosticLocation{KeyPath: ".spec.components.db"},
	}}, nil, []byte("spec:\n  components:\n   : : : not yaml\n"), nil)
	flush()

	got := s.snapshotDiagnostics(file)
	if len(got) != 1 {
		t.Fatalf("published %d diagnostics; want 1 — an unparseable document must not withhold the findings", len(got))
	}
	if got[0].Line != 1 || got[0].Column != 1 {
		t.Errorf("advisory at %d:%d; want the head of the file when no document could be walked",
			got[0].Line, got[0].Column)
	}
}
