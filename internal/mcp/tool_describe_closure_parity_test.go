// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// What the Release description must carry, and why each half is asserted the way
// it is.
//
// A closure report is made of two populations: the references the resolution
// reached, and the findings decided about them. A report that drops either
// describes a Release as sound that a reader of the same facts describes as broken
// — silently, because no verdict moves and nothing in the answer says a class was
// withheld.
//
// The two halves need different instruments.
//
//   - The REFERENCE half is asserted against the closure's own DECLARED axis set,
//     over a closure with a member planted on every axis. Deriving the expectation
//     from the projection under test compares a value with itself: an axis the
//     projection stopped reading would leave the expectation too, and the
//     comparison would keep passing. Planting on every declared axis also makes an
//     axis added later covered on the day it lands, with nobody editing this file.
//   - The FINDING half cannot be planted, because a finding is decided by the
//     resolution rather than carried on the closure. So it is driven through the
//     real tool over a workspace authored to raise each class, with the expectation
//     read from the shared producers and a calibration per class so a fixture that
//     stopped raising one cannot leave that half iterating over nothing.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops/compileropstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// closureWithAMemberOnEveryDeclaredAxis builds a closure carrying one library on
// each declared axis, named after the axis it sits on.
//
// Every axis is populated on purpose: an axis left empty would be
// indistinguishable from an axis the projection dropped, so a projection short by
// one axis would pass. The names come from the declaration, so a renamed or added
// axis is planted without this helper being touched.
func closureWithAMemberOnEveryDeclaredAxis(t *testing.T) closure.Closure {
	t.Helper()
	axes := closure.LibraryAxes()
	if len(axes) == 0 {
		t.Fatal("the closure declares no library axis, so planting a member on each would plant nothing " +
			"and the assertion below would compare two empty listings")
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

// TestDescribeLibraryProjectionCoversEveryDeclaredAxis requires the reported
// listing to carry a member from every declared library axis, with the kind that
// axis fixes.
//
// The failure it pins is the silent one this file exists for. A listing that names
// the axes itself goes on reporting the set it was written against, and an axis
// added to the declaration afterwards is carried by nobody — the answer simply
// stops mentioning it, with nothing failing to say so.
func TestDescribeLibraryProjectionCoversEveryDeclaredAxis(t *testing.T) {
	t.Parallel()
	cl := closureWithAMemberOnEveryDeclaredAxis(t)

	got := describeLibraryRefs(cl)
	t.Logf("the listing carried %d member(s): %+v", len(got), got)

	for _, axis := range closure.LibraryAxes() {
		want := closureRef{URI: "planted/" + axis.Axis, Version: "1.0.0", Kind: axis.Kind}
		found := false
		for _, g := range got {
			if g == want {
				found = true
				break
			}
		}
		if !found {
			// Named rather than counted. Both sides short by one still agree on a
			// count, and the axis that went missing is the only actionable part.
			t.Errorf("the listing drops a declared library axis.\n  missing: %+v\n  carried: %+v", want, got)
		}
	}
	if len(got) != len(closure.LibraryAxes()) {
		t.Errorf("the listing carried %d member(s) for %d declared axes; got %+v",
			len(got), len(closure.LibraryAxes()), got)
	}
}

// describeClosureWorkspace writes a workspace whose closure resolves AND whose
// resolution reaches a shared document and raises both finding classes at once.
//
// The Manifest maps the component the Aggregate instantiates onto a Recipe that
// implements it — so the closure resolves — and carries a SECOND mapping onto a
// Recipe implementing an unrelated type, which is the mapping fault. Splitting the
// two keeps the fault from also breaking the resolution: a fixture whose only
// mapping was inconsistent would refuse before there was a description to report a
// finding beside. The second mapping's key is a real Component because the closure
// walk resolves every mapping key, so a key naming nothing refuses for a different
// reason entirely.
//
// The record of pinned versions carries a withdrawal marker, which is the other
// class. withMarker=false is the matched control: without it, a report that named a
// withdrawal unconditionally would satisfy the assertion while saying nothing true.
func describeClosureWorkspace(t *testing.T, withMarker bool) (root, catalogDir, releasePath string) {
	t.Helper()
	root = t.TempDir()
	catalogDir = filepath.Join(root, "catalog")

	writeCatalogFile(t, filepath.Join(catalogDir, "libraries", "base-types.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: SchemaLibrary
metadata:
  name: BaseTypes
  version: "1.0.0"
spec:
  schemas:
    PortSpec:
      port: { type: integer, default: 8080 }
`)
	const svcComponent = `apiVersion: aggrega.ai/v1beta1
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
	writeCatalogFile(t, filepath.Join(catalogDir, "components", "svc.yaml"), svcComponent)
	writeCatalogFile(t, filepath.Join(catalogDir, "components", "other.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	writeCatalogFile(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1beta1
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
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: demo-server:latest
`)
	writeCatalogFile(t, filepath.Join(catalogDir, "recipes", "unrelated.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Recipe
metadata:
  name: demo-unrelated
  version: "1.0.0"
spec:
  capability: helm
  implements: demo/something-else
  workload: |
    apiVersion: aggrega.ai/v1beta1
    kind: Workload
    replicas: 1
    containers:
      main:
        image: something-else:latest
`)
	writeCatalogFile(t, filepath.Join(root, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1beta1
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
	writeCatalogFile(t, filepath.Join(root, "app-agg.yaml"), `apiVersion: aggrega.ai/v1beta1
kind: Aggregate
metadata:
  name: app-agg
  version: "1.0.0"
spec:
  components:
    app:
      type: "demo/svc:1.0.0"
`)
	releasePath = filepath.Join(root, "app-release.yaml")
	writeCatalogFile(t, releasePath, `apiVersion: aggrega.ai/v1beta1
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

	// The record of pinned versions describes what this Release resolves to, because
	// the tool refuses one that does not — the description would name versions no
	// build of the Release uses. The withdrawal marker sits on the row of the
	// reference the Aggregate writes, and the comparison reads no marker.
	//
	// The view is this package's own resolution of the workspace, pinned to the
	// lockfile the shared fixture hands it.
	tw, err := newTestServer(t).resolveToolWorkspace(root, []string{catalogDir})
	if err != nil {
		t.Fatalf("resolve the fixture workspace: %v", err)
	}
	viewFor := func(lf *lockfile.Lockfile) *catalog.LazyLookup {
		pins, err := catalog.PinsFromLockfile(lf)
		if err != nil {
			t.Fatalf("pin the fixture view to its lockfile: %v", err)
		}
		return tw.newView(releasePath, pins)
	}
	compileropstest.WriteMatchingLockfile(t, viewFor, releasePath, tw.root, tw.cfg, func(lf *lockfile.Lockfile) {
		if !withMarker {
			return
		}
		row := compileropstest.EntityRow(t, lf, "demo/svc:1.0.0")
		row.Deprecation = &lockfile.DeprecationRecord{
			Severity: "warning",
			Message:  "superseded by demo/svc-v2",
		}
	})
	return root, catalogDir, releasePath
}

// describeReleaseFor invokes the real tool over the workspace and decodes the
// structured payload.
func describeReleaseFor(t *testing.T, root, catalogDir, releasePath string) describeReleasePayload {
	t.Helper()
	s := newTestServer(t)
	res := callTool(t, s, "describe", describeArgs{
		Release: releasePath, WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
	})
	if res.IsError {
		t.Fatalf("describe refused, so there is no report to assert about: %s", res.Content[0].Text)
	}
	var payload describeReleasePayload
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal describe payload: %v (body: %s)", err, res.StructuredContent)
	}
	return payload
}

// TestDescribeReleaseCarriesEverythingTheResolutionReachedAndDecided drives the
// real tool and requires the report to account for every shared document the
// resolution reached and every finding it decided, with each expectation read from
// the shared producer rather than written out here.
//
// A spelled-out expectation would prove only that this file and the projection were
// edited together. Asking the producers what they reached and decided, and requiring
// the report to carry all of it, is a claim about the channel instead of about a
// sentence.
func TestDescribeReleaseCarriesEverythingTheResolutionReachedAndDecided(t *testing.T) {
	t.Parallel()
	root, catalogDir, releasePath := describeClosureWorkspace(t, true)

	// The expectation side, from a SEPARATE resolution. Deriving both sides from one
	// call would compare a value with itself, which is the blindness that let a whole
	// class go unreported on one surface while another reported it.
	tw, twErr := newTestServer(t).resolveToolWorkspace(root, []string{catalogDir})
	if twErr != nil {
		t.Fatalf("resolve the oracle workspace: %v", twErr)
	}
	lf, lockPins, lockRefused := tw.loadReleaseLockfile(releasePath)
	if lockRefused != "" {
		t.Fatalf("read the oracle's lockfile: %s", lockRefused)
	}
	resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
		View:            tw.newView(releasePath, lockPins),
		ReleasePath:     releasePath,
		WorkspaceRoot:   tw.root,
		WorkspaceConfig: tw.cfg,
		Lockfile:        lf,
		Augment:         true,
	})
	if cerr != nil {
		t.Fatalf("the oracle resolution refused, so the shared producers reached and decided nothing and "+
			"this test would assert nothing: stage=%d %v", cerr.Stage, cerr)
	}

	var wantLibraries []closureRef
	for _, axis := range closure.LibraryAxes() {
		for _, l := range axis.Libraries(&resolved.Closure) {
			wantLibraries = append(wantLibraries, closureRef{URI: l.URI, Version: l.ResolvedVersion, Kind: axis.Kind})
		}
	}
	var wantMappings []string
	for _, f := range resolved.ManifestConsistencyFindings {
		wantMappings = append(wantMappings, f.Error())
	}
	report, trustErr := compilerops.ReleaseDeprecationReport(lf, releasePath)
	if trustErr != nil {
		t.Fatalf("read the oracle's withdrawal report: %v", trustErr)
	}
	var wantDeprecations []string
	for _, f := range report {
		wantDeprecations = append(wantDeprecations, f.Notice)
	}

	// Calibration, per class. Without it a fixture that stopped raising one class
	// would leave that part of the comparison iterating over nothing, and the test
	// would report success for a channel it never exercised.
	if len(wantLibraries) == 0 {
		t.Fatal("the resolution reached no shared document for this workspace, so the fixture no longer " +
			"exercises the library axis end to end")
	}
	if len(wantMappings) == 0 {
		t.Fatal("the resolution decided no mapping finding for this workspace, so the fixture no longer " +
			"raises that class")
	}
	if len(wantDeprecations) == 0 {
		t.Fatal("the shared report found no recorded withdrawal for this workspace, so the fixture no " +
			"longer raises that class")
	}

	got := describeReleaseFor(t, root, catalogDir, releasePath)
	t.Logf("the report carried entity_count=%d library_count=%d libraries=%+v mapping_diagnostics=%v deprecations=%v",
		got.EntityCount, got.LibraryCount, got.Libraries, got.MappingDiagnostics, got.Deprecations)

	for _, w := range wantLibraries {
		found := false
		for _, g := range got.Libraries {
			if g == w {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("a shared document the resolution reached reaches no field of the report:\n"+
				"  missing: %+v\n  carried: %+v", w, got.Libraries)
		}
	}
	if got.LibraryCount != len(got.Libraries) {
		t.Errorf("the reported count %d does not match the listing beside it (%d entries), so the two are "+
			"computed from different reads of the same resolution", got.LibraryCount, len(got.Libraries))
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
				// Containment rather than equality: the report is entitled to frame an
				// entry for the surface it answers on, and that framing is
				// presentation. What it is not entitled to do is drop the finding.
				if strings.Contains(g, w) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("the %s finding the shared producer decided reaches no field of the report:\n"+
					"  missing: %s\n  carried: %v", class.name, w, class.got)
			}
		}
	}

	// The description is still delivered. A finding that displaced it would leave a
	// caller asking what a Release depends on with one complaint in place of the
	// answer, which is what reporting instead of refusing exists to avoid.
	if got.EntityCount == 0 || len(got.Entities) == 0 {
		t.Error("the report carried findings and no entity listing, so a finding displaced the answer " +
			"the caller asked for")
	}
}

// TestDescribeReleaseCountsSurviveTheConciseFormat pins that the concise format
// keeps every count and every finding while dropping the listings.
//
// The counts are the only thing the concise format reports about what was
// reached, so a listing added to this report owes one: without it a caller
// choosing concise is told a Release draws on nothing rather than told how much
// it draws on.
func TestDescribeReleaseCountsSurviveTheConciseFormat(t *testing.T) {
	t.Parallel()
	root, catalogDir, releasePath := describeClosureWorkspace(t, true)

	detailed := describeReleaseFor(t, root, catalogDir, releasePath)
	if detailed.LibraryCount == 0 {
		t.Fatal("the detailed report reached no shared document, so there is no count for the concise " +
			"format to preserve and this test would assert nothing")
	}

	s := newTestServer(t)
	askedFormat := responseFormatConcise
	res := callTool(t, s, "describe", describeArgs{
		Release: releasePath, WorkspaceRoot: root, CatalogDirs: []string{catalogDir},
		ResponseFormat: &askedFormat,
	})
	if res.IsError {
		t.Fatalf("describe (concise) refused: %s", res.Content[0].Text)
	}
	var concise describeReleasePayload
	if err := json.Unmarshal(res.StructuredContent, &concise); err != nil {
		t.Fatalf("unmarshal concise payload: %v", err)
	}
	t.Logf("concise report: entity_count=%d library_count=%d entities=%d libraries=%d "+
		"mapping_diagnostics=%d deprecations=%d",
		concise.EntityCount, concise.LibraryCount, len(concise.Entities), len(concise.Libraries),
		len(concise.MappingDiagnostics), len(concise.Deprecations))

	if concise.LibraryCount != detailed.LibraryCount {
		t.Errorf("the concise format reports %d shared document(s); the detailed format reached %d",
			concise.LibraryCount, detailed.LibraryCount)
	}
	if concise.EntityCount != detailed.EntityCount {
		t.Errorf("the concise format reports %d entity(ies); the detailed format reached %d",
			concise.EntityCount, detailed.EntityCount)
	}
	if len(concise.Libraries) != 0 || len(concise.Entities) != 0 {
		t.Errorf("the concise format carried the listings it exists to omit: entities=%d libraries=%d",
			len(concise.Entities), len(concise.Libraries))
	}
	// Findings are not a listing and are reported at both formats: a caller choosing
	// the shorter answer is not choosing to be told less about what is wrong.
	if len(concise.MappingDiagnostics) == 0 || len(concise.Deprecations) == 0 {
		t.Errorf("the concise format withheld a finding class: mapping=%d deprecation=%d",
			len(concise.MappingDiagnostics), len(concise.Deprecations))
	}
}

// TestDescribeReleaseReportsNoWithdrawalWhereNoneIsRecorded is the matched control
// on the withdrawal channel alone. It keeps the assertion above from passing on a
// report that named a withdrawal unconditionally.
func TestDescribeReleaseReportsNoWithdrawalWhereNoneIsRecorded(t *testing.T) {
	t.Parallel()
	root, catalogDir, releasePath := describeClosureWorkspace(t, false)

	got := describeReleaseFor(t, root, catalogDir, releasePath)
	if len(got.Deprecations) != 0 {
		t.Errorf("a record carrying no withdrawal marker was reported with %v", got.Deprecations)
	}
	// The other classes are unaffected by the marker, so they must still be
	// reported — which is what makes this a control on one channel rather than on
	// the report as a whole.
	if len(got.MappingDiagnostics) == 0 || got.LibraryCount == 0 {
		t.Errorf("the control workspace reported mapping=%d libraries=%d, so it is not the same workspace "+
			"minus the marker and the comparison above is not controlled",
			len(got.MappingDiagnostics), got.LibraryCount)
	}
}
