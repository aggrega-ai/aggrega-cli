// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The daemon's by-name and by-Release inspection legs.
//
// The oracle for the Release leg is a direct call to the shared inspection the
// graph request runs, over the same arguments, so these prove the method
// surfaces that computation rather than agreeing with a second copy of it.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// inspectByNameWorkspace builds a workspace whose catalog holds one entity of
// several kinds, plus a Component that extends a base — so a resolution can be
// told apart from a re-reading of one file — and one workspace document whose
// kind the catalog does not address by identity, which is the third outcome's
// fixture.
func inspectByNameWorkspace(t *testing.T) (ws string, srv *Server) {
	t.Helper()
	ws = t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")

	writeFixtureFile(t, filepath.Join(catalogDir, "components", "base.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/base
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      inherited_field: { type: string, required: false }
`)
	// An extending Component declares the inheritance marker and authors no
	// pillar content of its own, so its own document says where the pillars come
	// from and never what they are. That is what makes the merge observable
	// below: the field the resolved definition carries appears in no line of
	// this file.
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "svc.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "2.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  extends: demo/base
  schema: inherited
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "features", "metrics.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Feature
metadata:
  name: demo/metrics
  version: "1.2.3"
spec:
  schema:
    values:
      port: { type: integer, required: false }
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-server-synthesized
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/svc
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-server:latest
`)
	// A Manifest is a workspace document with a metadata.name that no catalog
	// accessor addresses by identity. Asking for it by name therefore resolves
	// nothing while the workspace plainly declares it — the third outcome.
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

	var in, out bytes.Buffer
	srv = newTestServer(t, ws, &in, &out)
	srv.wsConfig.Workspace.CatalogDirs = []string{catalogDir}
	srv.lazyCatalog = catalog.NewLazyLookup(ws, "", []string{catalogDir}, nil)
	return ws, srv
}

// callEntityDefinition drives one aggrega/entityDefinition request through the
// production dispatch and serializer path and returns the decoded response.
func callEntityDefinition(t *testing.T, srv *Server, params map[string]any) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	in.Write(framedRequest(1, "aggrega/entityDefinition", params))
	return runOneRequest(t, srv, &in, &out)
}

// decodeEntityDefinition decodes a successful response's result payload.
func decodeEntityDefinition(t *testing.T, resp jsonRPCResponse) entityDefinitionResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("aggrega/entityDefinition returned an error envelope: %+v", resp.Error)
	}
	var got entityDefinitionResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode result: %v (body: %s)", err, resp.Result)
	}
	return got
}

// TestEntityDefinitionResolvesANameWithNoCursorToStartFrom is the invocation
// proof for the by-name leg: a name alone, with no file open and no position,
// answers with the entity that name resolves to.
//
// It runs over several kinds rather than one because the resolution ladder tries
// them in order, and a ladder exercised at a single rung would leave every other
// rung asserted by nothing.
func TestEntityDefinitionResolvesANameWithNoCursorToStartFrom(t *testing.T) {
	_, srv := inspectByNameWorkspace(t)

	for _, tc := range []struct {
		name        string
		kind        string
		wantName    string
		wantVersion string
	}{
		{"demo/svc", "Component", "demo/svc", "2.0.0"},
		{"demo/metrics", "Feature", "demo/metrics", "1.2.3"},
		{"demo-server-synthesized", "Recipe", "demo-server-synthesized", "1.0.0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeEntityDefinition(t, callEntityDefinition(t, srv, map[string]any{"name": tc.name}))
			if got.Kind != tc.kind {
				t.Errorf("%q resolved to kind %q, want %q", tc.name, got.Kind, tc.kind)
			}
			if got.Name != tc.wantName {
				t.Errorf("%q reported identity %q, want %q", tc.name, got.Name, tc.wantName)
			}
			if got.Version != tc.wantVersion {
				t.Errorf("%q reported version %q, want %q", tc.name, got.Version, tc.wantVersion)
			}
			if strings.TrimSpace(got.Definition) == "" {
				t.Fatalf("%q answered with no definition", tc.name)
			}
			var body map[string]any
			if err := yaml.Unmarshal([]byte(got.Definition), &body); err != nil {
				t.Fatalf("the definition is not a document: %v", err)
			}
			if body["spec"] == nil {
				t.Errorf("the definition carries no spec: %s", got.Definition)
			}
			if body["kind"] != tc.kind {
				t.Errorf("the definition's own kind is %v, not %q", body["kind"], tc.kind)
			}
			// The pillar is carried through resolution as a parsed document node,
			// and re-encoding one into a foreign encoding emits the parser's own
			// fields in place of the content. These are the node's field names;
			// none of them is anything an author wrote.
			for _, leak := range []string{"HeadComment", "FootComment", "\nAnchor", "!!str"} {
				if strings.Contains(got.Definition, leak) {
					t.Errorf("the definition exposes the parser's node fields (%q): %s", leak, got.Definition)
				}
			}
		})
	}
}

// TestEntityDefinitionAnswersWithTheResolvedDefinitionNotTheAuthoredOne is what
// makes this a definition rather than a file read. The Component under test
// extends a base and declares the inheritance marker in place of any pillar
// content, so the field the answer carries appears nowhere in the document that
// declares the name.
//
// Without this the method could be satisfied by returning the bytes of whichever
// file declared the name, and every other assertion here would still pass.
func TestEntityDefinitionAnswersWithTheResolvedDefinitionNotTheAuthoredOne(t *testing.T) {
	ws, srv := inspectByNameWorkspace(t)

	authored, err := os.ReadFile(filepath.Join(ws, "catalog", "components", "svc.yaml"))
	if err != nil {
		t.Fatalf("read the authored document: %v", err)
	}
	if strings.Contains(string(authored), "inherited_field") {
		t.Fatal("precondition: the authored document already carries the inherited field, so the " +
			"merge would be unobservable")
	}

	got := decodeEntityDefinition(t, callEntityDefinition(t, srv, map[string]any{"name": "demo/svc"}))
	if !strings.Contains(got.Definition, "inherited_field") {
		t.Errorf("the definition carries no inherited field, so the extends chain was not resolved: %s",
			got.Definition)
	}
	if got.Name != "demo/svc" {
		t.Errorf("the merged definition lost the entity's own identity: %q", got.Name)
	}
}

// TestEntityDefinitionTellsItsThreeOutcomesApart drives the whole outcome set in
// one place: a name that resolves, a name nothing declares, and a name a
// workspace document declares which the catalog resolves nothing for.
//
// The two refusals are asserted on the STRUCTURE of the envelope rather than on
// its wording, because that is what a client can branch on. A refusal for a name
// nothing declares carries no source document, since there is none; a refusal for
// a name that is written down names the document it is written in. A wording
// assertion would pass on two envelopes a client could not tell apart.
func TestEntityDefinitionTellsItsThreeOutcomesApart(t *testing.T) {
	_, srv := inspectByNameWorkspace(t)

	t.Run("resolved", func(t *testing.T) {
		resp := callEntityDefinition(t, srv, map[string]any{"name": "demo/svc"})
		if resp.Error != nil {
			t.Fatalf("a name that resolves was refused: %+v", resp.Error)
		}
		if len(resp.Result) == 0 {
			t.Fatal("a name that resolves carried no result")
		}
	})

	t.Run("not found", func(t *testing.T) {
		resp := callEntityDefinition(t, srv, map[string]any{"name": "demo/no-such-thing"})
		if resp.Error == nil {
			t.Fatalf("a name that resolves to nothing was answered rather than refused: %s", resp.Result)
		}
		if len(resp.Result) != 0 {
			t.Errorf("a refused request still carried a result: %s", resp.Result)
		}
		if resp.Error.Code != errEntityNotResolvable {
			t.Errorf("the refusal carries code %d, not the not-resolvable code %d",
				resp.Error.Code, errEntityNotResolvable)
		}
		if resp.Error.Data != nil && resp.Error.Data.File != "" {
			t.Errorf("the refusal names a source document for a name nothing declares: %+v", resp.Error.Data)
		}
		if !strings.Contains(resp.Error.Message, "demo/no-such-thing") {
			t.Errorf("the refusal does not name what it refused: %s", resp.Error.Message)
		}
	})

	t.Run("found but unresolvable", func(t *testing.T) {
		resp := callEntityDefinition(t, srv, map[string]any{"name": "app-manifest"})
		if resp.Error == nil {
			t.Fatalf("a declared name the catalog resolves nothing for was answered: %s", resp.Result)
		}
		if resp.Error.Code != errEntityNotResolvable {
			t.Errorf("the refusal carries code %d, not the not-resolvable code %d",
				resp.Error.Code, errEntityNotResolvable)
		}
		if resp.Error.Data == nil || resp.Error.Data.File == "" {
			t.Fatalf("the refusal names no source document, so a client cannot tell it from a name "+
				"nothing declares: %+v", resp.Error.Data)
		}
		if resp.Error.Data.File != "app-manifest.yaml" {
			t.Errorf("the refusal names %q, not the workspace-relative document that declares the name",
				resp.Error.Data.File)
		}
		if filepath.IsAbs(resp.Error.Data.File) {
			t.Errorf("the refusal discloses a host path: %q", resp.Error.Data.File)
		}
		if !strings.Contains(resp.Error.Message, "Manifest") {
			t.Errorf("the refusal does not say what the document declares it as: %s", resp.Error.Message)
		}
	})
}

// TestEntityDefinitionRefusesARequestNamingNoEntity pins the band: an empty name
// is a malformed request the client corrects, not a state of the workspace it
// would ask a person to repair.
func TestEntityDefinitionRefusesARequestNamingNoEntity(t *testing.T) {
	_, srv := inspectByNameWorkspace(t)

	for _, tc := range []struct {
		label  string
		params map[string]any
	}{
		{"no name key at all", map[string]any{}},
		{"an empty name", map[string]any{"name": ""}},
		{"a name that is only space", map[string]any{"name": "   "}},
	} {
		t.Run(tc.label, func(t *testing.T) {
			resp := callEntityDefinition(t, srv, tc.params)
			if resp.Error == nil {
				t.Fatalf("a request naming no entity was answered: %s", resp.Result)
			}
			if resp.Error.Code != jsonRPCInvalidParams {
				t.Errorf("the refusal carries code %d, not the invalid-params code %d",
					resp.Error.Code, jsonRPCInvalidParams)
			}
			if !strings.Contains(resp.Error.Message, "name must be the entity's identity") {
				t.Errorf("the refusal does not state the contract: %s", resp.Error.Message)
			}
		})
	}
}

// callReleaseClosure drives one aggrega/releaseClosure request through the
// production dispatch and serializer path against a workspace built for the
// Release-scoped inspection, and returns the decoded response.
func callReleaseClosure(t *testing.T, ws, release string) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	in.Write(framedRequest(1, "aggrega/releaseClosure", releaseClosureParams{Release: release}))
	return runOneRequest(t, srv, &in, &out)
}

// callReleaseClosurePastPushes is callReleaseClosure for a workspace whose
// lockfile is gone. That refusal is preceded by the withdrawal of any drift
// finding on the lockfile, which callReleaseClosure would decode as the answer.
func callReleaseClosurePastPushes(t *testing.T, ws, release string) jsonRPCResponse {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	in.Write(framedRequest(1, "aggrega/releaseClosure", releaseClosureParams{Release: release}))
	resp, _ := runOneRequestPastPushes(t, srv, &in, &out)
	return resp
}

// decodeReleaseClosure decodes a successful response's result payload.
func decodeReleaseClosure(t *testing.T, resp jsonRPCResponse) releaseClosureResult {
	t.Helper()
	if resp.Error != nil {
		t.Fatalf("aggrega/releaseClosure returned an error envelope: %+v", resp.Error)
	}
	var got releaseClosureResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode result: %v (body: %s)", err, resp.Result)
	}
	return got
}

// TestReleaseClosureReportsWhatTheReleaseReaches is the invocation proof for the
// Release leg, and its oracle is the shared inspection itself: the answer is
// compared against the projection of the closure that computation resolved for
// the same Release, rather than against a listing written out here.
//
// A listing written here would prove only that this file and the resolution were
// edited together. Comparing against the shared computation proves the method
// reports what that computation reached.
func TestReleaseClosureReportsWhatTheReleaseReaches(t *testing.T) {
	ws, release := renderDialWorkspace(t)

	got := decodeReleaseClosure(t, callReleaseClosure(t, ws, release))

	var in, out bytes.Buffer
	oracleSrv := newPinIsolationServer(t, ws, &in, &out, 1)
	preview, errEnvelope := oracleSrv.previewRelease(oracleSrv.serverCtx, release, pipeline.Options{SkipRender: true})
	if errEnvelope != nil {
		t.Fatalf("the oracle inspection refused: %+v", errEnvelope)
	}

	if got.Release != release {
		t.Errorf("the answer reports Release %q for a request about %q", got.Release, release)
	}
	if got.Aggregate != preview.resolved.Aggregate.Metadata.Name {
		t.Errorf("the answer names Aggregate %q, the resolution reached %q",
			got.Aggregate, preview.resolved.Aggregate.Metadata.Name)
	}
	if got.Manifest != preview.resolved.Manifest.Metadata.Name {
		t.Errorf("the answer names Manifest %q, the resolution reached %q",
			got.Manifest, preview.resolved.Manifest.Metadata.Name)
	}
	if got.Environment != preview.resolved.Release.Metadata.Environment {
		t.Errorf("the answer names environment %q, the Release declares %q",
			got.Environment, preview.resolved.Release.Metadata.Environment)
	}

	// Logged so a failure here is readable against what was actually reached,
	// rather than against a listing the reader has to reconstruct.
	t.Logf("closure listing: entities=%+v libraries=%+v plugins=%+v",
		got.Entities, got.Libraries, got.Plugins)

	wantEntities := closureEntities(preview.resolved.Closure, stateRows)
	if len(wantEntities) == 0 {
		t.Fatal("precondition: the resolution reached no entity, so an equal listing would assert nothing")
	}
	if !sameClosureListing(got.Entities, wantEntities) {
		t.Errorf("the entity listing is not what the resolution reached.\n got: %+v\nwant: %+v",
			got.Entities, wantEntities)
	}
	// The library expectation is read off the closure's own DECLARED axis set
	// rather than off the projection under test. Deriving both sides from that
	// projection compares a value with itself: an axis the projection stopped
	// reading would be missing from the expectation too, and the comparison would
	// go on passing with nothing to say a whole class had stopped being reported.
	// That blindness is exactly how the same listing came to be complete on one
	// surface and short on another.
	wantLibraries := declaredLibraryListing(preview.resolved.Closure)
	if !sameClosureListing(got.Libraries, wantLibraries) {
		t.Errorf("the library listing is not what the declared axes hold.\n got: %+v\nwant: %+v",
			got.Libraries, wantLibraries)
	}
	if !sameClosureListing(got.Plugins, closurePlugins(preview.resolved.Closure, stateRows)) {
		t.Errorf("the guest listing is not what the resolution reached.\n got: %+v\nwant: %+v",
			got.Plugins, closurePlugins(preview.resolved.Closure, stateRows))
	}

	// The listing's stated job is which entities were reached AND at which
	// versions, so a listing carrying no resolved version anywhere would satisfy
	// every equality above while answering half the question.
	versioned := 0
	for _, e := range got.Entities {
		if e.URI == "" {
			t.Errorf("a closure entry carries no identity: %+v", e)
		}
		if e.Version != "" {
			versioned++
		}
	}
	if versioned == 0 {
		t.Error("no closure entry reports a resolved version, so the listing says what was reached but " +
			"not at which version")
	}
}

// sameClosureListing reports whether two listings carry the same entries in the
// same order. Order is part of the comparison: the resolution's order is a
// function of the Release's content, and a projection free to reorder it would
// be a second ordering nothing holds to the first.
func sameClosureListing(got, want []ClosureEntity) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestReleaseClosureComputesNoSecondClosure is the proof that this method joins
// the daemon's one Release inspection rather than walking a closure beside it.
//
// It proves it from a precondition the method does not implement. The shared
// inspection refuses a Release with no lockfile, before it resolves anything,
// and the refusal names the lockfile it looked for. A handler that reached a
// closure by any other route would have no such gate to inherit — every other
// route resolves against whatever the catalog holds and would answer here — so
// a refusal in exactly that shape is only available to a caller that went
// through the shared computation.
//
// Paired with the equality above, the two cover both halves: the values the
// method reports are that computation's, and the gates it is subject to are that
// computation's too. Neither is a count of walks, and neither claims to be: what
// they establish is that there is no second route producing this answer.
func TestReleaseClosureComputesNoSecondClosure(t *testing.T) {
	ws, release := renderDialWorkspace(t)

	// The positive control: with the lockfile in place the request is answered,
	// so the refusal below is attributable to its absence and not to a fixture
	// that never worked.
	if resp := callReleaseClosure(t, ws, release); resp.Error != nil {
		t.Fatalf("precondition: the prepared workspace was refused: %+v", resp.Error)
	}

	lockfilePath := lockfile.PathFor(filepath.Join(ws, release))
	if _, err := os.Stat(lockfilePath); err != nil {
		t.Fatalf("precondition: no lockfile at %s to remove: %v", lockfilePath, err)
	}
	if err := os.Remove(lockfilePath); err != nil {
		t.Fatalf("remove the lockfile: %v", err)
	}

	resp := callReleaseClosurePastPushes(t, ws, release)
	if resp.Error == nil {
		t.Fatalf("a Release with no lockfile was answered, so the closure came from somewhere other "+
			"than the shared inspection: %s", resp.Result)
	}
	if resp.Error.Code != errLockfileAbsent {
		t.Fatalf("the refusal carries code %d, not the lockfile-absent code %d — the shared "+
			"inspection's own gate did not run", resp.Error.Code, errLockfileAbsent)
	}
	if resp.Error.Data == nil || resp.Error.Data.Lockfile == "" {
		t.Errorf("the refusal names no lockfile, so it is not the shared inspection's: %+v", resp.Error.Data)
	}
	if resp.Error.Data != nil && resp.Error.Data.Release != release {
		t.Errorf("the refusal names Release %q for a request about %q", resp.Error.Data.Release, release)
	}
}

// TestReleaseClosureIsNotTheGraph pins the distinction the reference page states,
// because a client holding the graph can reasonably assume it already has this.
//
// The discriminating fact is an entity the Release reaches that is not a node on
// its graph. The graph says what the composition looks like; the closure says what
// was pulled in to build it. An entity can be in the second without being in the
// first, which is why one is not recoverable from the other.
func TestReleaseClosureIsNotTheGraph(t *testing.T) {
	ws, release := renderDialWorkspace(t)

	closureAnswer := decodeReleaseClosure(t, callReleaseClosure(t, ws, release))

	var reached []string
	for _, e := range closureAnswer.Entities {
		reached = append(reached, e.URI)
	}
	if len(reached) == 0 {
		t.Fatal("precondition: the closure listing is empty, so nothing can be contrasted against the graph")
	}

	graph := string(irGraphWithDial(t, ws, release, false))
	if graph == "" {
		t.Fatal("precondition: the graph request produced nothing to contrast against")
	}

	// The Recipe the Manifest maps the component type to is reached by the
	// Release and is not a node the graph keys.
	const recipeURI = "demo-server-synthesized"
	inClosure := false
	for _, uri := range reached {
		if strings.HasPrefix(uri, recipeURI) {
			inClosure = true
		}
	}
	if !inClosure {
		t.Fatalf("precondition: the fixture's Recipe is not in the closure listing, so the contrast "+
			"cannot be drawn: %v", reached)
	}

	var graphDoc struct {
		Spec struct {
			Nodes map[string]json.RawMessage `json:"nodes"`
		} `json:"spec"`
	}
	if err := json.Unmarshal([]byte(graph), &graphDoc); err != nil {
		t.Fatalf("decode the graph: %v", err)
	}
	if len(graphDoc.Spec.Nodes) == 0 {
		t.Fatalf("precondition: the graph carries no nodes, so an absence among them asserts nothing: %s", graph)
	}
	for id := range graphDoc.Spec.Nodes {
		if strings.Contains(id, recipeURI) {
			t.Fatalf("the fixture no longer discriminates: the Recipe is also a graph node (%s)", id)
		}
	}
}

// TestReleaseClosurePublishesNothing pins what the method deliberately does not
// do. Publishing replaces a whole diagnostic layer, so a request that published
// here would erase the layer the graph request wrote — and a client that asked
// for a listing would silently lose the drift warning it was already showing.
func TestReleaseClosurePublishesNothing(t *testing.T) {
	ws, release := renderDialWorkspace(t)

	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)

	// The graph request is what writes a layer. Drive it first so there is a
	// layer for a later publication to disturb.
	in.Write(framedRequest(1, "aggrega/irGraph", irGraphParams{Release: release}))
	srv.in = bufio.NewReader(&in)
	srv.out = &out
	body, err := readMessage(srv.in)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	var graphReq jsonRPCRequest
	if err := json.Unmarshal(body, &graphReq); err != nil {
		t.Fatalf("unmarshal request: %v", err)
	}
	driveHandle(t, srv, &graphReq, &out)

	before := srv.snapshotAllDiagnostics()
	if len(before) == 0 {
		t.Skip("the graph request wrote no diagnostic layer, so a later publication would have " +
			"nothing observable to disturb")
	}

	var in2, out2 bytes.Buffer
	in2.Write(framedRequest(2, "aggrega/releaseClosure", releaseClosureParams{Release: release}))
	if resp := runOneRequest(t, srv, &in2, &out2); resp.Error != nil {
		t.Fatalf("aggrega/releaseClosure refused: %+v", resp.Error)
	}

	after := srv.snapshotAllDiagnostics()
	if len(after) != len(before) {
		t.Fatalf("the listing changed the diagnostic layers: %d before, %d after", len(before), len(after))
	}
	for file, count := range before {
		if after[file] != count {
			t.Errorf("the listing changed %s from %d diagnostic(s) to %d", file, count, after[file])
		}
	}
}

// snapshotAllDiagnostics records how many diagnostics the daemon holds per file,
// for a test asserting that a request left them alone.
//
// It reads BOTH layers, keyed apart. The render layer is the one a listing
// request would be most likely to disturb — it is written by the same handler
// that computes an inspection — so a snapshot covering only the semantic layer
// would be blind to exactly the erasure this is checking for.
func (s *Server) snapshotAllDiagnostics() map[string]int {
	s.diagMu.Lock()
	defer s.diagMu.Unlock()
	out := map[string]int{}
	for file, diags := range s.diagnostics {
		out["semantic:"+file] = len(diags)
	}
	for file, diags := range s.renderDiags {
		out["render:"+file] = len(diags)
	}
	return out
}
