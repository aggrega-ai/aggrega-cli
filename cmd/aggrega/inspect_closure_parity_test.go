// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// What the closure listing must carry, and why each half is asserted the way it
// is.
//
// A closure listing is made of two populations: the references the resolution
// reached, and the findings decided about them. A listing that drops either
// describes a Release as sound that a reader of the same facts describes as broken
// — silently, because no exit code moves and nothing on screen says a class was
// withheld.
//
// The two halves need different instruments.
//
//   - The REFERENCE half is asserted against the closure's own DECLARED axis set,
//     over a closure with a member planted on every axis. Deriving the expectation
//     from the listing under test compares a value with itself: an axis the listing
//     stopped writing would leave the expectation too, and the comparison would
//     keep passing. Planting on every declared axis also makes an axis added later
//     covered on the day it lands, with nobody editing this file.
//   - The FINDING half cannot be planted, because a finding is decided by the
//     resolution rather than carried on the closure. So it is driven through the
//     real command over a workspace authored to raise each class, with the
//     expectation read from the shared producers and a calibration per class so a
//     fixture that stopped raising one cannot leave that half iterating over
//     nothing.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// closureWithAMemberOnEveryDeclaredAxis builds a closure carrying one library on
// each declared axis, named after the axis it sits on.
//
// Every axis is populated on purpose: an axis left empty would be
// indistinguishable from an axis the listing dropped, so a listing short by one
// axis would pass. The names come from the declaration, so a renamed or added axis
// is planted without this helper being touched.
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

// TestClosureListingCoversEveryDeclaredLibraryAxis requires the listing to write a
// section for every declared library axis, labelled with the kind that axis fixes
// and carrying its member.
//
// The failure it pins is the silent one this file exists for. A listing that names
// the axes itself goes on writing the set it was written against, and an axis added
// to the declaration afterwards is written by nobody — the listing simply stops
// mentioning it, with nothing failing to say so.
func TestClosureListingCoversEveryDeclaredLibraryAxis(t *testing.T) {
	t.Parallel()
	cl := closureWithAMemberOnEveryDeclaredAxis(t)

	var buf bytes.Buffer
	renderClosureLibraries(testInspectOptions(&buf), cl)
	got := buf.String()
	t.Logf("the listing wrote:\n%s", got)

	if got == "" {
		t.Fatal("the listing wrote nothing for a closure with a member on every declared axis, so the " +
			"assertions below would look for absences in an absence")
	}
	for _, axis := range closure.LibraryAxes() {
		// The section header carries the kind and the member count; the entry
		// carries the planted identity. Both are asserted, because a listing that
		// wrote the header and dropped the members reads exactly like a listing that
		// worked.
		if !strings.Contains(got, axis.Kind+" (1):") {
			t.Errorf("the listing writes no section for the declared %s axis; it wrote:\n%s", axis.Axis, got)
		}
		if !strings.Contains(got, "planted/"+axis.Axis) {
			t.Errorf("the listing writes no member for the declared %s axis; it wrote:\n%s", axis.Axis, got)
		}
	}
}

// closureFindingsWorkspace writes a workspace whose closure resolves AND whose
// resolution reaches a shared document and raises both finding classes at once.
//
// The Manifest maps the component the Aggregate instantiates onto a Recipe that
// implements it — so the deployment resolves — and carries a SECOND mapping onto a
// Recipe implementing an unrelated type, which is the mapping fault. Splitting the
// two keeps the fault from also breaking the resolution: a fixture whose only
// mapping was inconsistent would refuse before there was a listing to report a
// finding beside. The second mapping's key is a real Component because the closure
// walk resolves every mapping key, so a key naming nothing refuses for a different
// reason entirely.
//
// The record of pinned versions carries a withdrawal marker, which is the other
// class. withMarker=false is the matched control: without it, a listing that named
// a withdrawal unconditionally would satisfy the assertion while saying nothing
// true.
func closureFindingsWorkspace(t *testing.T, withMarker bool) (root, releasePath string) {
	t.Helper()
	root = t.TempDir()
	catalogDir := filepath.Join(root, "catalog")

	writeClosureFixture(t, filepath.Join(catalogDir, "libraries", "base-types.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: SchemaLibrary
metadata:
  name: BaseTypes
  version: "1.0.0"
spec:
  schemas:
    PortSpec:
      port: { type: integer, default: 8080 }
`)
	writeClosureFixture(t, filepath.Join(catalogDir, "components", "svc.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
`)
	writeClosureFixture(t, filepath.Join(catalogDir, "components", "other.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeClosureFixture(t, filepath.Join(catalogDir, "recipes", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeClosureFixture(t, filepath.Join(catalogDir, "recipes", "unrelated.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeClosureFixture(t, filepath.Join(root, "app-manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeClosureFixture(t, filepath.Join(root, "app-agg.yaml"), `apiVersion: aggrega.ai/v1alpha1
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
	writeClosureFixture(t, releasePath, `apiVersion: aggrega.ai/v1alpha1
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

	lf := &lockfile.Lockfile{
		Version:     1,
		Release:     "./app-release.yaml",
		GeneratedAt: "2026-06-03T10:00:00Z",
		Entities:    []lockfile.Entity{{URI: "demo/svc:1.0.0", ResolvedVersion: "1.0.0"}},
	}
	if withMarker {
		lf.Entities[0].Deprecation = &lockfile.DeprecationRecord{
			Severity: "warning",
			Message:  "superseded by demo/svc-v2",
		}
	}
	if err := lockfile.WriteLockfile(lockfile.PathFor(releasePath), lf); err != nil {
		t.Fatalf("WriteLockfile: %v", err)
	}
	// The catalog stack is declared here, in the workspace's own config, and named
	// nowhere else. Declared twice — once here and once as a command argument — the
	// same directory enters the search stack twice and every entity in it shadows
	// itself, which the resolution reports as a duplicate identity and which would
	// make this fixture describe a fault it does not have.
	writeClosureFixture(t, filepath.Join(root, workspaceconfig.ConfigFilename), `workspace:
  catalog_dirs:
    - ./catalog
`)
	return root, releasePath
}

// writeClosureFixture writes one fixture document, creating parent directories as
// needed and failing the test on any I/O error.
func writeClosureFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// closureListingFor drives the real command over the workspace and returns what it
// wrote.
func closureListingFor(t *testing.T, root, releasePath string) string {
	t.Helper()
	var buf bytes.Buffer
	opts := testInspectOptions(&buf)
	opts.closure = true
	opts.level = levelDefault
	err := dispatchInspect(context.Background(),
		resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath,
		nil,
		opts,
	)
	if err != nil {
		t.Fatalf("the closure selection refused, so there is no listing to assert about: %v", err)
	}
	return buf.String()
}

// sharedClosureFacts resolves the workspace's closure through the shared operation
// and returns what a listing of it owes: the shared documents it reached, the
// mapping findings it decided, and the withdrawals recorded against its pins.
//
// This is the expectation side, and it is a SEPARATE invocation on purpose. Deriving
// both sides from one call would compare a value with itself, which is the blindness
// that let a whole class go unlisted on one surface while another listed it.
func sharedClosureFacts(t *testing.T, root, releasePath string) (libraries, mappings, deprecations []string) {
	t.Helper()
	rw := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	wsCfg := rw.Config()
	dirs := rw.ResolveCatalogDirs(nil)
	view := catalog.NewLazyLookup(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs, nil)
	lf, _, lfErr := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if lfErr != nil {
		t.Fatalf("the fixture's record of pinned versions did not load, so the withdrawal half of this "+
			"test would assert nothing: %v", lfErr)
	}
	view.SetPins(catalog.PinsFromLockfile(lf))

	resolved, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
		View:            view,
		ReleasePath:     releasePath,
		WorkspaceRoot:   rw.Root(),
		WorkspaceConfig: wsCfg,
		Lockfile:        lf,
		Augment:         true,
	})
	if cerr != nil {
		t.Fatalf("the oracle resolution refused, so the shared producers reached and decided nothing and "+
			"this test would assert nothing: stage=%d %v", cerr.Stage, cerr)
	}

	for _, axis := range closure.LibraryAxes() {
		for _, l := range axis.Libraries(&resolved.Closure) {
			libraries = append(libraries, l.URI)
		}
	}
	for _, f := range resolved.ManifestConsistencyFindings {
		mappings = append(mappings, f.Error())
	}
	for _, f := range lf.DeprecationFindings() {
		deprecations = append(deprecations, f.Notice())
	}
	return libraries, mappings, deprecations
}

// TestClosureListingCarriesEverythingTheResolutionReachedAndDecided drives the real
// command and requires the listing to account for every shared document the
// resolution reached and every finding it decided.
//
// A spelled-out expectation would prove only that this file and the listing were
// edited together. Asking the producers what they reached and decided, and requiring
// the listing to carry all of it, is a claim about the channel instead of about a
// sentence.
func TestClosureListingCarriesEverythingTheResolutionReachedAndDecided(t *testing.T) {
	t.Parallel()
	root, releasePath := closureFindingsWorkspace(t, true)

	wantLibraries, wantMappings, wantDeprecations := sharedClosureFacts(t, root, releasePath)

	// Calibration, per class. Without it a fixture that stopped raising one class
	// would leave that part of the comparison iterating over nothing, and the test
	// would report success for a channel it never exercised.
	for _, class := range []struct {
		name string
		want []string
	}{
		{"shared document", wantLibraries},
		{"mapping finding", wantMappings},
		{"recorded withdrawal", wantDeprecations},
	} {
		if len(class.want) == 0 {
			t.Fatalf("the shared producers yielded no %s for this workspace, so the fixture no longer "+
				"exercises that class and this test cannot see whether the listing carries it", class.name)
		}
	}

	got := closureListingFor(t, root, releasePath)
	t.Logf("the command wrote:\n%s", got)

	for _, class := range []struct {
		name string
		want []string
	}{
		{"shared document", wantLibraries},
		{"mapping finding", wantMappings},
		{"recorded withdrawal", wantDeprecations},
	} {
		for _, w := range class.want {
			// Containment rather than equality: the listing is entitled to frame an
			// entry for the terminal it writes to — a section header, an indent, a
			// resolved version in brackets — and that framing is presentation. What it
			// is not entitled to do is drop the fact.
			if !strings.Contains(got, w) {
				t.Errorf("the %s the shared producer yielded reaches no line of the listing:\n"+
					"  missing: %s\n  wrote:\n%s", class.name, w, got)
			}
		}
	}

	// The listing is still delivered. A finding that displaced it would leave a
	// reader asking what a deployment depends on with one complaint in place of the
	// answer, which is what reporting instead of refusing exists to avoid.
	if !strings.Contains(got, "Entities (") {
		t.Errorf("the command wrote findings and no entity listing, so a finding displaced the answer the "+
			"reader asked for; it wrote:\n%s", got)
	}
}

// TestClosureListingWritesNoWithdrawalWhereNoneIsRecorded is the matched control on
// the withdrawal channel alone. It keeps the assertion above from passing on a
// listing that named a withdrawal unconditionally.
func TestClosureListingWritesNoWithdrawalWhereNoneIsRecorded(t *testing.T) {
	t.Parallel()
	root, releasePath := closureFindingsWorkspace(t, false)

	got := closureListingFor(t, root, releasePath)
	if strings.Contains(got, "Deprecations:") {
		t.Errorf("a record carrying no withdrawal marker was listed with a withdrawal section:\n%s", got)
	}
	// The other classes are unaffected by the marker, so they must still be
	// listed — which is what makes this a control on one channel rather than on the
	// listing as a whole.
	if !strings.Contains(got, "Mapping findings:") || !strings.Contains(got, "SchemaLibrary (") {
		t.Errorf("the control workspace listed no mapping finding or no shared document, so it is not the "+
			"same workspace minus the marker and the comparison above is not controlled; it wrote:\n%s", got)
	}
}
