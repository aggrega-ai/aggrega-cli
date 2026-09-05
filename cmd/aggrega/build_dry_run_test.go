// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

// "A dry run writes nothing" is the one promise this flag makes, so the proof is
// the filesystem rather than the flag's own report: every case here brackets a
// real dispatch with a byte-level snapshot of the workspace and compares the two.
//
// The snapshot machinery mirrors the assertion the preview computation carries one
// layer down, deliberately name-for-name so a reader recognises it as the same
// method rather than a second invention. It is a mirror rather than a shared
// helper because the original is an unexported test helper in another package,
// which no import can reach; unifying them would mean promoting it to
// non-test code in a package this work does not otherwise touch.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/buildops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/externalinputs"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// treeSnapshot records every entry under root by path, size and modification
// time. Size and modification time are both recorded because either alone misses
// a class: a rewrite with identical length moves only the time, and a file
// written twice within the filesystem's timestamp granularity moves only the
// size.
//
// A root that does not exist yet snapshots as empty rather than failing, so a
// case can bracket a directory the run under observation might create.
func treeSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	snap := map[string]string{}
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return snap
	}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		info, ierr := d.Info()
		if ierr != nil {
			return ierr
		}
		snap[rel] = fmt.Sprintf("dir=%v size=%d mtime=%d", d.IsDir(), info.Size(), info.ModTime().UnixNano())
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return snap
}

// diffSnapshots returns a human-readable description of every entry that was
// added, removed or modified between two snapshots, and the empty string when
// they are identical.
func diffSnapshots(before, after map[string]string) string {
	var lines []string
	for path, a := range after {
		b, existed := before[path]
		switch {
		case !existed:
			lines = append(lines, "added:    "+path)
		case a != b:
			lines = append(lines, "modified: "+path)
		}
	}
	for path := range before {
		if _, ok := after[path]; !ok {
			lines = append(lines, "removed:  "+path)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

// TestDryRunTreeComparisonDetectsAPlantedFile is the calibration for every
// assertion below, and it is what makes those assertions evidence rather than
// decoration: a comparison that cannot fail proves nothing about the run it
// brackets.
//
// It plants into a scratch directory rather than into a tree a real assertion
// watches, because a control that mutated the very tree under observation would
// be the defect it is meant to detect.
func TestDryRunTreeComparisonDetectsAPlantedFile(t *testing.T) {
	t.Parallel()
	scratch := t.TempDir()
	if err := os.WriteFile(filepath.Join(scratch, "already-here.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed scratch: %v", err)
	}

	before := treeSnapshot(t, scratch)
	if d := diffSnapshots(before, treeSnapshot(t, scratch)); d != "" {
		t.Fatalf("an unchanged tree must compare equal; got:\n%s", d)
	}

	if err := os.WriteFile(filepath.Join(scratch, "planted.txt"), []byte("y"), 0o644); err != nil {
		t.Fatalf("plant: %v", err)
	}
	if d := diffSnapshots(before, treeSnapshot(t, scratch)); !strings.Contains(d, "added:    planted.txt") {
		t.Fatalf("the comparison failed to see a planted file, so it cannot witness a write:\n%s", d)
	}

	// A rewrite in place is the other class, and it is the one a listing-only
	// comparison misses: the path set is identical and only the content moved.
	rewritten := treeSnapshot(t, scratch)
	if err := os.WriteFile(filepath.Join(scratch, "already-here.txt"), []byte("xx"), 0o644); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	if d := diffSnapshots(rewritten, treeSnapshot(t, scratch)); !strings.Contains(d, "modified: already-here.txt") {
		t.Fatalf("the comparison failed to see a rewritten file:\n%s", d)
	}
}

// dryRunFixture authors a buildable workspace whose single component renders
// through the embedded helm guest from a local chart source, and returns the
// Release path and the catalog directory.
//
// schemaRef, when non-empty, is written into the chart's values schema as a
// remote reference. That is what makes a render REQUEST an external input: the
// guest compiles the chart's schema through the host's fetch seam, so a remote
// reference in it is a render-time fetch rather than a resolution-time one.
func dryRunFixture(t *testing.T, root, schemaRef string) (releasePath, catalogDir string) {
	t.Helper()
	catalogDir = filepath.Join(root, "catalog")

	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: dry-run-aggregate
  version: "1.0.0"
spec:
  components:
    webapp:
      type: server
`)
	writeFixture(t, filepath.Join(root, "release.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: dry-run-release
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./dist/main
      components:
        webapp:
          infra:
            replicas: 1
`)
	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: dry-run-manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: server
`)
	writeFixture(t, filepath.Join(catalogDir, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values: {}
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogDir, "providers"))

	chartDir := filepath.Join(root, "charts", "webapp")
	writeFixture(t, filepath.Join(chartDir, "Chart.yaml"), "apiVersion: v2\nname: webapp\nversion: 0.1.0\n")
	writeFixture(t, filepath.Join(chartDir, "templates", "cm.yaml"), `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}
data:
  releaseName: {{ .Release.Name | quote }}
`)
	if schemaRef != "" {
		writeFixture(t, filepath.Join(chartDir, "values.schema.json"),
			fmt.Sprintf(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":%q}`, schemaRef))
	}

	writeFixture(t, filepath.Join(catalogDir, "recipes", "server.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: server
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source: %q
`, chartDir))

	return filepath.Join(root, "release.yaml"), catalogDir
}

// primeDryRunLockfile fills the Release's lockfile with an explicit pull, which
// is what a dry run needs in place: build is read-only by default, and a dry run
// declines to fill one.
func primeDryRunLockfile(t *testing.T, root, releasePath, catalogDir string) {
	t.Helper()
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	if err := pullRun(pullConfig{ReleasePath: releasePath, CatalogDirs: []string{catalogDir}, ws: ws}); err != nil {
		t.Fatalf("prime lockfile via pull: %v", err)
	}
}

// TestBuildDryRunWritesNothingThenTheSameBuildWritesTheArtifacts is the whole
// promise in one case, and the two halves have to sit together: the first alone
// is satisfied by a flag that broke the build, and the second alone says nothing
// about the flag at all.
//
// The dry run's report is asserted to carry each artifact's content, and the real
// build that follows is asserted to put those same bytes at exactly the paths the
// report named — so "the flag was accepted and did nothing" fails here, and so
// does a report whose paths are not the paths a build writes to.
func TestBuildDryRunWritesNothingThenTheSameBuildWritesTheArtifacts(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogDir := dryRunFixture(t, root, "")
	primeDryRunLockfile(t, root, releasePath, catalogDir)

	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	before := treeSnapshot(t, root)

	var out, diag bytes.Buffer
	if err := dispatchBuildWithDiag(ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildops.RunOptions{DryRun: true}, &diag, &out); err != nil {
		t.Fatalf("the dry run must complete the render: %v\ndiagnostics:\n%s", err, diag.String())
	}

	if d := diffSnapshots(before, treeSnapshot(t, root)); d != "" {
		t.Fatalf("the dry run wrote to the workspace; it promises it does not:\n%s", d)
	}

	report := out.String()
	if !strings.Contains(report, "nothing was written") {
		t.Errorf("the dry run's summary does not say nothing was written; got:\n%s", report)
	}
	// A report over zero artifacts would satisfy every assertion above while
	// proving the render never happened.
	headers := strings.Count(report, "==> ")
	if headers == 0 {
		t.Fatalf("the dry run reported no artifacts, so 'it wrote nothing' says nothing; got:\n%s", report)
	}
	if !strings.Contains(report, `releaseName: "webapp"`) {
		t.Errorf("the dry run's report does not carry the rendered content; got:\n%s", report)
	}

	// The same build without the flag: the artifacts appear.
	var buildOut, buildDiag bytes.Buffer
	if err := dispatchBuildWithDiag(ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildops.RunOptions{}, &buildDiag, &buildOut); err != nil {
		t.Fatalf("the same build without --dry-run must write its artifacts: %v\ndiagnostics:\n%s", err, buildDiag.String())
	}
	written := readTree(t, filepath.Join(root, "dist"))
	if len(written) == 0 {
		t.Fatal("the build without --dry-run wrote nothing, so the comparison above is not against a build that writes")
	}
	if len(written) != headers {
		t.Errorf("the dry run reported %d artifacts and the build wrote %d files; the report must describe the same set", headers, len(written))
	}
	// Every path the dry run named is a path the build actually wrote to. This is
	// the assertion a report computed from its own idea of the output layout
	// fails.
	for rel := range written {
		named := filepath.Join("dist", rel)
		if !strings.Contains(report, "==> "+named+" <==") {
			t.Errorf("the build wrote %s but the dry run's report never named it; report:\n%s", named, report)
		}
	}
}

// TestBuildDryRunRefusesTheFlagsThatWrite covers each pairing that cannot be
// honoured, one fault per row, and asserts the refusal names the flag the user
// typed rather than a flag it implies.
//
// The --upgrade row is why the check sits ahead of the implication that folds it
// into --auto-pull: a reader told their --auto-pull conflicts, when what they
// passed was --upgrade, has to work out the connection themselves.
//
// The --trace row is also the guard holding a coupling that has no runtime
// backstop: the trace write carries no dry-run condition of its own, so this row
// is what fails if the refusal is ever removed.
func TestBuildDryRunRefusesTheFlagsThatWrite(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogDir := dryRunFixture(t, root, "")
	primeDryRunLockfile(t, root, releasePath, catalogDir)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	tests := []struct {
		name    string
		trace   bool
		run     buildops.RunOptions
		wantAll []string
	}{
		{
			name:    "upgrade",
			run:     buildops.RunOptions{DryRun: true, Upgrade: true},
			wantAll: []string{"--dry-run conflicts with --upgrade", "rewrites the lockfile", "aggrega pull --upgrade --release"},
		},
		{
			name:    "auto-pull",
			run:     buildops.RunOptions{DryRun: true, AutoPull: true},
			wantAll: []string{"--dry-run conflicts with --auto-pull", "fills the lockfile", "aggrega pull --release"},
		},
		{
			name:    "trace",
			trace:   true,
			run:     buildops.RunOptions{DryRun: true},
			wantAll: []string{"--dry-run conflicts with --trace", ".trace/", "Drop --trace"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := treeSnapshot(t, root)
			var out, diag bytes.Buffer
			err := dispatchBuildWithDiag(ws, releasePath, []string{catalogDir}, tc.trace,
				buildDiagOptions{}, tc.run, &diag, &out)
			if err == nil {
				t.Fatal("the pairing must halt; the build completed")
			}
			for _, want := range tc.wantAll {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal does not carry %q; got: %v", want, err)
				}
			}
			// The refusal happens before any work, so nothing moved.
			if d := diffSnapshots(before, treeSnapshot(t, root)); d != "" {
				t.Errorf("the refused pairing still touched the workspace:\n%s", d)
			}
		})
	}
}

// TestBuildDryRunOverridesAWorkspaceConfiguredFill covers the leg no flag
// refusal reaches: the fill also runs from the workspace setting, so a dry run
// that only refused --auto-pull would still write a lockfile in a workspace
// configured to fill one.
//
// The outcome asserted is the honest one rather than a second refusal: the dry
// run overrides the setting for this invocation and the missing lockfile then
// halts with the same pull-first remediation the read-only default gives. A
// workspace that fills by default would otherwise have no way to preview a
// render at all.
func TestBuildDryRunOverridesAWorkspaceConfiguredFill(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)
	releasePath, catalogDir := dryRunFixture(t, root, "")

	// The config is authored before the workspace is resolved, so the helper
	// leaves it exactly as written.
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), fmt.Sprintf(`auto_pull: true
workspace:
  catalog_dirs:
    - %q
`, catalogDir))
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})
	if !ws.Config().AutoPull {
		t.Fatalf("the fixture must have the workspace fill switched on, or this case asserts nothing")
	}

	lockfilePath := lockfile.PathFor(releasePath)
	if _, err := os.Stat(lockfilePath); !os.IsNotExist(err) {
		t.Fatalf("the fixture must start with no lockfile; stat gave %v", err)
	}
	before := treeSnapshot(t, root)

	var out, diag bytes.Buffer
	err := dispatchBuildWithDiag(ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildops.RunOptions{DryRun: true}, &diag, &out)
	if err == nil {
		t.Fatal("a dry run with no lockfile must halt rather than fill one")
	}
	if !strings.Contains(err.Error(), "aggrega pull") {
		t.Errorf("the halt must carry the pull-first remediation; got: %v", err)
	}
	if _, statErr := os.Stat(lockfilePath); !os.IsNotExist(statErr) {
		t.Errorf("the dry run filled the lockfile at %s; the workspace setting must not reach it", lockfilePath)
	}
	if d := diffSnapshots(before, treeSnapshot(t, root)); d != "" {
		t.Errorf("the dry run wrote to the workspace while declining to fill:\n%s", d)
	}
}

// TestBuildDryRunDeclinesAnUnpinnedExternalReference is the first half of the
// frozen-input behaviour, driven through a render that genuinely requests an
// external input: the chart's values schema carries a remote reference, so the
// guest asks the host for it while compiling the schema.
//
// What is asserted is the whole chain a user meets: the refusal carries the
// frozen-closure condition, the diagnostic names the reference that was declined,
// the divergence note tells them what to run, and no blob landed in the store on
// the way to refusing.
func TestBuildDryRunDeclinesAnUnpinnedExternalReference(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	const unpinned = "https://unreachable.invalid/values-schema.json"
	releasePath, catalogDir := dryRunFixture(t, root, unpinned)
	primeDryRunLockfile(t, root, releasePath, catalogDir)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	storeRoot := externalinputs.StoreRoot(ws.Config().ResolvedArtifactCacheDir())
	storeBefore := treeSnapshot(t, storeRoot)

	var out, diag bytes.Buffer
	err := dispatchBuildWithDiag(ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildops.RunOptions{DryRun: true}, &diag, &out)
	if err == nil {
		t.Fatal("a dry run meeting an unpinned external reference must halt rather than fetch it")
	}
	// What the reader is told is asserted against the whole cause chain rather
	// than the registered condition, and that is a property of where the refusal
	// happens rather than a weaker assertion. The guest asks for the input, so
	// the refusal travels back out through the guest, which reports a failure of
	// its own; the condition's identity does not cross that boundary and only its
	// rendered text does. So the contract worth pinning here is the text a person
	// reads: the reference, and why it was refused.
	chain := cerrors.CauseChain(err)
	for _, want := range []string{unpinned, "is not pinned in the lockfile"} {
		if !strings.Contains(chain, want) {
			t.Errorf("the diagnostic does not carry %q; got:\n%s", want, chain)
		}
	}
	// The divergence signal: it is read off the refusal, not inferred by comparing
	// the lockfile to anything.
	note := diag.String()
	for _, want := range []string{
		"the lockfile does not pin every external reference this render requested",
		"- " + unpinned,
		"aggrega acquire --release",
	} {
		if !strings.Contains(note, want) {
			t.Errorf("the diagnostics do not carry %q; got:\n%s", want, note)
		}
	}
	if d := diffSnapshots(storeBefore, treeSnapshot(t, storeRoot)); d != "" {
		t.Errorf("the declined reference still put bytes in the input store:\n%s", d)
	}
}

// TestBuildDryRunServesAPinnedExternalReferenceFromTheStore is the other half,
// and without it the case above reads as "a dry run cannot render a chart with a
// remote reference at all", which is the opposite of the behaviour.
//
// The pin is established by the product's own acquisition rather than by hand-
// writing lockfile content, so what the dry run then consumes is exactly what a
// real build leaves behind. The server is closed before the dry run: a render
// that still succeeds can only have been served from the store, which is what
// makes "no network call" an observation rather than an assumption.
func TestBuildDryRunServesAPinnedExternalReferenceFromTheStore(t *testing.T) {
	root := t.TempDir()
	t.Chdir(root)

	schema := []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	var served int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served++
		_, _ = w.Write(schema)
	}))
	schemaURL := srv.URL + "/values-schema.json"

	releasePath, catalogDir := dryRunFixture(t, root, schemaURL)
	primeDryRunLockfile(t, root, releasePath, catalogDir)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	// A real build with acquisition on pins the reference and stores its bytes.
	var acqOut, acqDiag bytes.Buffer
	if err := dispatchBuildWithDiag(ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildops.RunOptions{}, &acqDiag, &acqOut); err != nil {
		srv.Close()
		t.Fatalf("the priming build must acquire and pin the reference: %v\ndiagnostics:\n%s", err, acqDiag.String())
	}
	if served == 0 {
		srv.Close()
		t.Fatal("the priming build never fetched the reference, so the fixture does not exercise an external input at all")
	}

	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		srv.Close()
		t.Fatalf("load lockfile after the priming build: %v", err)
	}
	var pinned bool
	for _, rec := range lf.ExternalInputs {
		if rec.URL == schemaURL {
			pinned = true
		}
	}
	if !pinned {
		srv.Close()
		t.Fatalf("the priming build did not pin %s; external inputs recorded: %+v", schemaURL, lf.ExternalInputs)
	}

	// From here the endpoint is gone: any fetch fails, so a render that completes
	// was served from the store.
	srv.Close()
	fetchesBeforeDryRun := served

	storeRoot := externalinputs.StoreRoot(ws.Config().ResolvedArtifactCacheDir())
	storeBefore := treeSnapshot(t, storeRoot)
	if len(storeBefore) == 0 {
		t.Fatal("the input store is empty, so 'the store is unchanged' says nothing")
	}
	lockfileBefore := treeSnapshot(t, filepath.Dir(lockfile.PathFor(releasePath)))

	var out, diag bytes.Buffer
	if err := dispatchBuildWithDiag(ws, releasePath, []string{catalogDir}, false,
		buildDiagOptions{}, buildops.RunOptions{DryRun: true}, &diag, &out); err != nil {
		t.Fatalf("a dry run must render a pinned reference from the store: %v\ndiagnostics:\n%s", err, diag.String())
	}
	if served != fetchesBeforeDryRun {
		t.Errorf("the dry run reached the network: fetch count moved from %d to %d", fetchesBeforeDryRun, served)
	}
	if d := diffSnapshots(storeBefore, treeSnapshot(t, storeRoot)); d != "" {
		t.Errorf("the dry run changed the input store; a pinned reference must be served without writing:\n%s", d)
	}
	if d := diffSnapshots(lockfileBefore, treeSnapshot(t, filepath.Dir(lockfile.PathFor(releasePath)))); d != "" {
		t.Errorf("the dry run rewrote the lockfile:\n%s", d)
	}
	if !strings.Contains(out.String(), "nothing was written") {
		t.Errorf("the dry run's summary does not say nothing was written; got:\n%s", out.String())
	}
}

// stubFetch is a fetch seam returning whatever the test declared, so the
// recorder can be driven over each outcome without a guest.
type stubFetch struct{ err error }

func (s stubFetch) Fetch(_ context.Context, _ string, _ string) ([]byte, error) {
	if s.err != nil {
		return nil, s.err
	}
	return []byte("served"), nil
}

// TestDeclineRecordingFetchRecordsOnlyTheFrozenClosureRefusal calibrates the
// divergence signal. A recorder that recorded every failure would report a
// divergence for an unreachable host or a corrupt store blob — neither of which
// is a divergence, because a real build fails on them identically — and a
// recorder that recorded nothing would leave the note permanently silent while
// every other assertion still passed.
//
// The refusal is wrapped before it is returned, because that is how it reaches
// the recorder in production: the service names the URL and the remedy around
// the condition it raises.
func TestDeclineRecordingFetchRecordsOnlyTheFrozenClosureRefusal(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		err        error
		wantRecord bool
	}{
		{
			name:       "frozen closure refusal is a divergence",
			err:        fmt.Errorf("externalinputs: %q is not pinned: %w", "https://example.com/a", externalinputs.ErrAcquisitionDisabled()),
			wantRecord: true,
		},
		{
			name: "an unreachable host is not",
			err:  errors.New("externalinputs: fetch \"https://example.com/a\": dial tcp: no such host"),
		},
		{
			name: "a corrupt store blob is not",
			err:  fmt.Errorf("externalinputs: store blob mismatch: %w", externalinputs.ErrStoreCorrupt()),
		},
		{
			name: "a served fetch is not",
			err:  nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := externalinputs.NewDeclineRecorder(stubFetch{err: tc.err})
			_, err := f.Fetch(context.Background(), "https://example.com/a", "test")
			if (err != nil) != (tc.err != nil) {
				t.Fatalf("the recorder must pass the outcome through unchanged; got err=%v", err)
			}
			got := f.Declined()
			if tc.wantRecord {
				if len(got) != 1 || got[0] != "https://example.com/a" {
					t.Errorf("Declined() = %v, want the one declined reference", got)
				}
				return
			}
			if len(got) != 0 {
				t.Errorf("Declined() = %v, want nothing recorded", got)
			}
		})
	}
}

// TestBuildDryRunFlagRegistered pins the flag onto the command with the default
// that keeps a plain build writing. It is the floor rather than the proof — the
// cases above are what show the flag changes behaviour.
func TestBuildDryRunFlagRegistered(t *testing.T) {
	t.Parallel()
	f := newBuildCmd().Flags().Lookup("dry-run")
	if f == nil {
		t.Fatal("build command missing the --dry-run flag")
	}
	if f.DefValue != "false" {
		t.Errorf("--dry-run default = %q, want false (a plain build writes)", f.DefValue)
	}
	for _, want := range []string{"--auto-pull", "--upgrade", "--trace", "unpinned"} {
		if !strings.Contains(f.Usage, want) {
			t.Errorf("the flag's help does not mention %q, which a reader needs before running it: %s", want, f.Usage)
		}
	}
}
