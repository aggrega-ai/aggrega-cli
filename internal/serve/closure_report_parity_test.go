// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// What the daemon's closure answer must carry, and why each half is asserted the
// way it is.
//
// A closure answer is made of two populations: the references the resolution
// reached, and the findings decided about them. A projection that drops either
// reports a Release as sound that a reader of the same facts reports as broken —
// and it does so silently: no verdict moves, no code changes, nothing appears on
// screen to say a class was withheld.
//
// The two halves need different instruments, and conflating them is what leaves a
// gap.
//
//   - The REFERENCE half is asserted against the closure's own DECLARED axis set,
//     over a closure with a member planted on every axis. Deriving the expectation
//     from the projection under test compares a value with itself: an axis the
//     projection stopped reading would leave the expectation too, and the
//     comparison would keep passing. Planting on every declared axis also makes an
//     axis added later covered on the day it lands, with nobody editing this file.
//   - The FINDING half cannot be planted, because a finding is decided by the
//     resolution rather than carried on the closure. So it is driven end to end
//     over a workspace authored to raise each class, with the expectation read from
//     the shared producers — the resolution's own mapping verdict and the shared
//     withdrawal report — and a calibration per class so a fixture that stopped
//     raising one cannot leave that half iterating over nothing.

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// declaredLibraryListing is the library listing the closure's declared axes hold,
// derived from the declaration rather than from any projection of it.
//
// It is the expectation side for every assertion about the library half, here and
// in the Release-closure test beside it. Written as a walk of the declared axis
// set, it says what the answer OWES; a walk of the projection would only say what
// the projection currently does.
func declaredLibraryListing(cl closure.Closure) []ClosureEntity {
	var out []ClosureEntity
	for _, axis := range closure.LibraryAxes() {
		for _, l := range axis.Libraries(&cl) {
			out = append(out, ClosureEntity{URI: l.URI, Version: l.ResolvedVersion, Kind: axis.Kind})
		}
	}
	return out
}

// closureWithAMemberOnEveryDeclaredAxis builds a closure carrying one library on
// each declared axis, named after the axis it sits on.
//
// Every axis is populated on purpose: an axis left empty would be indistinguishable
// from an axis the projection dropped, so a projection short by one axis would pass.
// The names come from the declaration, so a renamed or added axis is planted without
// this helper being touched.
func closureWithAMemberOnEveryDeclaredAxis(t *testing.T) closure.Closure {
	t.Helper()
	axes := closure.LibraryAxes()
	if len(axes) == 0 {
		t.Fatal("the closure declares no library axis, so planting a member on each would plant nothing " +
			"and every assertion below would compare two empty listings")
	}
	var cl closure.Closure
	for _, axis := range axes {
		axis.SetLibraries(&cl, []closure.ResolvedLibrary{{
			URI:             "planted/" + axis.Axis,
			ResolvedVersion: "1.0.0",
		}})
	}
	return cl
}

// TestClosureLibraryProjectionCoversEveryDeclaredAxis requires the wire projection
// to carry a member from every declared library axis.
//
// The failure it pins is the silent one this whole file exists for. A projection
// that names the axes itself goes on reporting the set it was written against, and
// an axis added to the declaration afterwards is carried by nobody — the answer
// simply stops mentioning it, with nothing failing to say so.
func TestClosureLibraryProjectionCoversEveryDeclaredAxis(t *testing.T) {
	t.Parallel()
	cl := closureWithAMemberOnEveryDeclaredAxis(t)

	got := closureLibraries(cl, stateRows)
	want := declaredLibraryListing(cl)

	t.Logf("declared axes hold %d member(s); the projection carried %d: %+v", len(want), len(got), got)

	// Named rather than counted. Both sides short by one still agree on a count,
	// and the axis that went missing is the only actionable part of the failure.
	for _, w := range want {
		found := false
		for _, g := range got {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the projection drops a declared library axis.\n  missing: %+v\n  carried: %+v", w, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("the projection carried %d member(s) for %d declared; got %+v",
			len(got), len(want), got)
	}
}

// closureFindingsWorkspace writes a workspace whose closure resolves AND whose
// resolution raises both finding classes at once.
//
// The Manifest maps the component the Aggregate instantiates onto a Recipe that
// implements it — so the Release resolves — and carries a SECOND mapping onto a
// Recipe implementing an unrelated type, which is the mapping fault. Splitting the
// two is what keeps the fault from also breaking the resolution: a fixture whose
// only mapping was inconsistent would refuse before there was a listing to report a
// finding beside.
//
// The record of pinned versions carries a withdrawal marker, which is the other
// class. The two are independent faults on purpose — neither must displace the
// other, and neither must displace the listing.
//
// withMarker=false is the matched control. Without it, a surface that reported no
// withdrawal at all would leave the "listing survives" assertions green and the
// withdrawal assertion would be the only thing carrying the test.
func closureFindingsWorkspace(t *testing.T, withMarker bool) (ws, releaseRel string) {
	t.Helper()
	ws = t.TempDir()
	catalogDir := filepath.Join(ws, "catalog")

	// A shared document, and a Component drawing on it. The library axis is
	// otherwise empty in a minimal workspace, and an empty axis cannot show whether
	// it is reported.
	writeFixtureFile(t, filepath.Join(catalogDir, "libraries", "base-types.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseTypes
  version: "1.0.0"
spec:
  schemas:
    PortSpec:
      port: { type: integer, default: 8080 }
`)
	const svcComponent = `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/svc
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      ports:
        $ref: "BaseTypes:1.0.0#/schemas/PortSpec"
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
	// The second mapping's key. It has to be a real Component: the closure walk
	// resolves every mapping key, so a key naming nothing refuses the resolution
	// before the mapping verdict is reached — a different fault, and one that would
	// leave this fixture unable to raise the class it exists for.
	writeFixtureFile(t, filepath.Join(catalogDir, "components", "other.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: demo/other
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    infra:
      port: { type: integer, default: 8080 }
`)
	// The recipe the second mapping points at implements a type nothing here
	// extends, which is what the mapping verdict reports.
	writeFixtureFile(t, filepath.Join(catalogDir, "recipes", "unrelated.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: demo-unrelated
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/something-else
  workload: |
    apiVersion: aggrega.ai/v1alpha1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: something-else:latest
`)
	writeFixtureFile(t, filepath.Join(catalogDir, "plugins", "helm-renderer.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: helm
  pluginType: provider
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
  provider:
    capabilities:
      - helm
  exports:
    - name: render_node
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
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
        demo/other: demo-unrelated:1.0.0
`)
	writeFixtureFile(t, filepath.Join(ws, "app-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/svc:1.0.0"
`)
	releaseRel = "app-release.yaml"
	relPath := filepath.Join(ws, releaseRel)
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
		Release:     "./" + releaseRel,
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities: []lockfile.Entity{{
			URI: "demo/svc:1.0.0", ResolvedVersion: "1.0.0", Digest: contentDigestHex([]byte(svcComponent)),
		}},
	}
	if withMarker {
		lf.Entities[0].Deprecation = &lockfile.DeprecationRecord{
			Severity: "warning",
			Message:  "superseded by demo/svc-v2",
		}
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(relPath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}

	writeFixtureFile(t, filepath.Join(ws, ".aggrega-config.yaml"), `workspace:
  catalog_dirs:
    - ./catalog
`)
	return ws, releaseRel
}

// closureAnswerFor drives one aggrega/releaseClosure request end to end and decodes
// the answer.
func closureAnswerFor(t *testing.T, ws, release string) releaseClosureResult {
	t.Helper()
	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(ws, "catalog")}
	in.Write(framedRequest(1, "aggrega/releaseClosure", releaseClosureParams{Release: release}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("aggrega/releaseClosure refused, so there is no answer to assert about: %+v", resp.Error)
	}
	var got releaseClosureResult
	if err := json.Unmarshal(resp.Result, &got); err != nil {
		t.Fatalf("decode result: %v (body: %s)", err, resp.Result)
	}
	return got
}

// TestReleaseClosureCarriesEveryFindingTheResolutionDecided drives the request over
// the wire and requires the answer to account for every finding of both classes,
// with each expectation read from the shared producer rather than written out here.
//
// A spelled-out expectation would prove only that this file and the projection were
// edited together. Asking the producers what they decided, and requiring the answer
// to carry all of it, is a claim about the channel instead of about a sentence — so
// a finding class the producers gain later is covered on the day it lands.
func TestReleaseClosureCarriesEveryFindingTheResolutionDecided(t *testing.T) {
	ws, release := closureFindingsWorkspace(t, true)

	// The expectation side, from a SEPARATE invocation. Deriving both sides from one
	// call would compare a value with itself, which is the blindness that let a whole
	// class go unreported on one surface while another reported it.
	var in, out bytes.Buffer
	oracle := newPinIsolationServer(t, ws, &in, &out, 1)
	oracle.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(ws, "catalog")}
	preview, errEnvelope := oracle.previewRelease(oracle.serverCtx, release, pipeline.Options{SkipRender: true})
	if errEnvelope != nil {
		t.Fatalf("the oracle inspection refused, so the shared producers decided nothing and this test "+
			"would assert nothing: %+v", errEnvelope)
	}

	var wantMappings []string
	for _, f := range preview.resolved.ManifestConsistencyFindings {
		wantMappings = append(wantMappings, f.Error())
	}
	report, trustErr := compilerops.ReleaseDeprecationReport(preview.lockfile, preview.releasePath)
	if trustErr != nil {
		t.Fatalf("read the oracle's withdrawal report: %v", trustErr)
	}
	var wantDeprecations []string
	for _, f := range report {
		wantDeprecations = append(wantDeprecations, f.Notice)
	}

	// Calibration, per class. Without it a fixture that stopped raising one class
	// would leave that half of the comparison iterating over nothing, and the test
	// would report success for a channel it never exercised.
	if len(wantMappings) == 0 {
		t.Fatal("the resolution decided no mapping finding for this workspace, so the fixture no longer " +
			"raises that class and this test cannot see whether the answer carries it")
	}
	if len(wantDeprecations) == 0 {
		t.Fatal("the shared report found no recorded withdrawal for this workspace, so the fixture no " +
			"longer raises that class and this test cannot see whether the answer carries it")
	}

	wantLibraries := declaredLibraryListing(preview.resolved.Closure)
	if len(wantLibraries) == 0 {
		t.Fatal("the resolution reached no shared document for this workspace, so the fixture no longer " +
			"exercises the library axis end to end and only the planted-member assertion covers it")
	}

	got := closureAnswerFor(t, ws, release)
	t.Logf("the answer carried entities=%+v libraries=%+v plugins=%+v mappingDiagnostics=%v deprecations=%v",
		got.Entities, got.Libraries, got.Plugins, got.MappingDiagnostics, got.Deprecations)

	// The library half, end to end over a real resolution rather than a planted
	// closure — so the two together cover both the projection and the wiring that
	// reaches it.
	for _, w := range wantLibraries {
		found := false
		for _, g := range got.Libraries {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("a shared document the resolution reached reaches no field of the answer:\n"+
				"  missing: %+v\n  carried: %+v", w, got.Libraries)
		}
	}

	for _, class := range []struct {
		name string
		want []string
		got  []string
	}{
		{"mapping", wantMappings, got.MappingDiagnostics},
		{"deprecation", wantDeprecations, got.Deprecations},
	} {
		for _, w := range class.want {
			found := false
			for _, g := range class.got {
				// Containment rather than equality: the answer is entitled to frame an
				// entry for the surface it is answering on, and that framing is
				// presentation. What it is not entitled to do is drop the finding.
				if strings.Contains(g, w) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("the %s finding the shared producer decided reaches no field of the answer:\n"+
					"  missing: %s\n  carried: %v", class.name, w, class.got)
			}
		}
	}

	// The listing is still delivered. A finding that displaced it would leave a
	// caller asking what a Release depends on with one complaint in place of the
	// answer, which is exactly what carrying findings instead of failing exists to
	// avoid.
	if len(got.Entities) == 0 {
		t.Error("the answer reported findings and no entity listing, so a finding displaced the answer " +
			"the caller asked for")
	}
}

// TestReleaseClosureReportsNoWithdrawalWhereNoneIsRecorded is the matched control.
// It keeps the assertion above from passing on an answer that reported a withdrawal
// unconditionally — which would satisfy containment while saying nothing true.
func TestReleaseClosureReportsNoWithdrawalWhereNoneIsRecorded(t *testing.T) {
	ws, release := closureFindingsWorkspace(t, false)

	got := closureAnswerFor(t, ws, release)
	if len(got.Deprecations) != 0 {
		t.Errorf("a record carrying no withdrawal marker was answered with %v", got.Deprecations)
	}
	// The other class is unaffected by the marker, so it must still be reported —
	// which is what makes this a control on the withdrawal channel alone rather than
	// on the answer as a whole.
	if len(got.MappingDiagnostics) == 0 {
		t.Error("the control workspace reported no mapping finding, so it is not the same workspace " +
			"minus the marker and the comparison above is not controlled")
	}
}

// TestReleaseClosureCollectionsAreArraysNotNull pins the shape contract the answer
// states: every collection is an array on the wire, empty rather than null, so a
// client iterates it without a null check.
//
// It reads the encoded body rather than a decoded value, because decoding maps a
// JSON null and an empty array onto the same Go slice and would collapse exactly
// the property under test.
func TestReleaseClosureCollectionsAreArraysNotNull(t *testing.T) {
	ws, release := closureFindingsWorkspace(t, false)

	var in, out bytes.Buffer
	srv := newPinIsolationServer(t, ws, &in, &out, 1)
	srv.wsConfig.Workspace.CatalogDirs = []string{filepath.Join(ws, "catalog")}
	in.Write(framedRequest(1, "aggrega/releaseClosure", releaseClosureParams{Release: release}))
	resp := runOneRequest(t, srv, &in, &out)
	if resp.Error != nil {
		t.Fatalf("aggrega/releaseClosure refused: %+v", resp.Error)
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(resp.Result, &raw); err != nil {
		t.Fatalf("decode result object: %v (body: %s)", err, resp.Result)
	}
	for _, field := range []string{"entities", "libraries", "plugins", "mappingDiagnostics", "deprecations"} {
		body, ok := raw[field]
		if !ok {
			t.Errorf("the answer carries no %q field at all", field)
			continue
		}
		if string(body) == "null" {
			t.Errorf("%q encoded as null; the response contract is an array, empty rather than null, so a "+
				"client iterates it without a null check", field)
		}
	}
}
