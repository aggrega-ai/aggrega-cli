// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
	"github.com/aggrega-ai/aggrega-cli/pkg/recipepin"
)

// sharedRecipeURI is the provider-qualified identity the fixture's Manifest maps
// the Component type to, and so the identity each Release's lockfile records the
// shared Recipe under.
const sharedRecipeURI = "helm/chartuser:1.0.0"

// versionedChartTgz builds a minimal, deterministic Helm chart archive for
// chart pinchart at the given version, whose single template renders the version
// as a literal so the rendered artifact identifies which chart version produced
// it. Two versions therefore differ in their bytes and in what they render.
func versionedChartTgz(t *testing.T, version string) []byte {
	t.Helper()
	entries := [][2]string{
		{"pinchart/Chart.yaml", "apiVersion: v2\nname: pinchart\nversion: " + version + "\n"},
		{"pinchart/templates/cm.yaml", "apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: {{ .Release.Name }}-pin\ndata:\n  chartVersion: \"" + version + "\"\n"},
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e[0], Mode: 0o644, Size: int64(len(e[1])), Typeflag: tar.TypeReg, ModTime: time.Unix(0, 0).UTC(), Format: tar.FormatPAX}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write([]byte(e[1])); err != nil {
			t.Fatalf("tar body: %v", err)
		}
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

// serveMultiVersionHelmRepo serves a helm repository index carrying every named
// version of chart pinchart, each pointing at its own archive on the same server.
func serveMultiVersionHelmRepo(t *testing.T, versions ...string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	var entries strings.Builder
	entries.WriteString("apiVersion: v1\nentries:\n  pinchart:\n")
	for _, v := range versions {
		blob := versionedChartTgz(t, v)
		path := "/pinchart-" + v + ".tgz"
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/gzip")
			_, _ = w.Write(blob)
		})
		fmt.Fprintf(&entries, "  - name: pinchart\n    version: %s\n    urls:\n    - %s\n", v, srv.URL+path)
	}
	index := entries.String()
	mux.HandleFunc("/index.yaml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-yaml")
		_, _ = w.Write([]byte(index))
	})
	return srv
}

// sharedRecipeTwoReleasesFixture authors ONE workspace holding ONE catalog whose
// single Recipe templates its source version from the consuming component's
// infra, and TWO Releases that supply two different versions. The Recipe is
// referenced from the Manifest by its full provider-qualified URI so it lands as
// a pinned closure entity in each Release's lockfile, and its name does not
// collide with the Component's, so no shadowing rule suppresses the recorded
// digest. Returns the two release paths.
func sharedRecipeTwoReleasesFixture(t *testing.T, root, repoRootURL string) (string, string) {
	t.Helper()
	catalogRoot := filepath.Join(root, "catalog")
	writeFixture(t, filepath.Join(root, ".aggrega-config.yaml"), "workspace: {}\nauto_pull: false\n")
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
`)
	release := func(name, dist, chartVersion string) string {
		writeFixture(t, filepath.Join(root, name+".yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: %s
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./%s
      components:
        api:
          infra:
            chart_version: "%s"
`, name, dist, chartVersion))
		return filepath.Join(root, name+".yaml")
	}
	relA := release("release-a", "a", "0.6.0")
	relB := release("release-b", "b", "0.7.0")

	writeFixture(t, filepath.Join(root, "manifest.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Manifest
metadata:
  name: manifest
  version: "1.0.0"
spec:
  mappings:
    components:
      type:
        server: `+sharedRecipeURI+`
`)
	writeFixture(t, filepath.Join(catalogRoot, "components", "server.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Component
metadata:
  name: server
  version: "1.0.0"
  labels:
    component.aggrega.ai/type: "server"
spec:
  schema:
    values:
      port: { type: integer, default: 8080 }
`)
	writeHelmRendererPlugin(t, filepath.Join(catalogRoot, "providers"))
	writeFixture(t, filepath.Join(catalogRoot, "recipes", "chartuser.yaml"), fmt.Sprintf(`apiVersion: aggrega.ai/v1alpha1
kind: Recipe
metadata:
  name: chartuser
  version: "1.0.0"
spec:
  capability: helm
  implements: server
  source:
    kind: helm-https-chart
    url: %s
    chart: pinchart
    version: "{{ .infra.chart_version }}"
`, repoRootURL))
	return relA, relB
}

// renderedChartVersion reads back the chart version the rendered artifacts under
// dist carry, so the assertion reads the produced bytes rather than the command's
// exit status.
func renderedChartVersion(t *testing.T, dist string) string {
	t.Helper()
	var found []string
	snapshot := snapshotDist(t, dist)
	for _, body := range snapshot {
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "chartVersion:") {
				v := strings.Trim(strings.TrimSpace(strings.TrimPrefix(trimmed, "chartVersion:")), `"`)
				found = append(found, v)
			}
		}
	}
	if len(found) == 0 {
		for name, body := range snapshot {
			t.Logf("dist file %s:\n%s", name, body)
		}
		t.Fatalf("no rendered artifact under %s carries a chartVersion key", dist)
	}
	for _, v := range found[1:] {
		if v != found[0] {
			t.Fatalf("rendered artifacts under %s disagree on chartVersion: %v", dist, found)
		}
	}
	return found[0]
}

// renderedChartVersionsByName reads every rendered object under dist that carries
// a chartVersion and maps its metadata.name to that version, so two consumers'
// renders can be told apart by the object each one produced.
func renderedChartVersionsByName(t *testing.T, dist string) map[string]string {
	t.Helper()
	out := map[string]string{}
	snapshot := snapshotDist(t, dist)
	for file, body := range snapshot {
		dec := yaml.NewDecoder(strings.NewReader(body))
		for {
			var doc struct {
				Metadata struct {
					Name string `yaml:"name"`
				} `yaml:"metadata"`
				Data struct {
					ChartVersion string `yaml:"chartVersion"`
				} `yaml:"data"`
			}
			err := dec.Decode(&doc)
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatalf("decode rendered %s: %v", file, err)
			}
			if doc.Data.ChartVersion == "" {
				continue
			}
			if prior, seen := out[doc.Metadata.Name]; seen && prior != doc.Data.ChartVersion {
				t.Fatalf("two rendered objects named %q carry chart versions %s and %s", doc.Metadata.Name, prior, doc.Data.ChartVersion)
			}
			out[doc.Metadata.Name] = doc.Data.ChartVersion
		}
	}
	if len(out) == 0 {
		for name, body := range snapshot {
			t.Logf("dist file %s:\n%s", name, body)
		}
		t.Fatalf("no rendered object under %s carries a chartVersion key", dist)
	}
	return out
}

// pullSharedRecipeRelease resolves one Release of the shared-recipe fixture the
// way `aggrega pull --release` does. pinSources is the --pin-sources answer: nil
// where the flag is not passed, so the workspace's own setting decides.
func pullSharedRecipeRelease(t *testing.T, root, catalogDir, releasePath string, pinSources *bool) error {
	t.Helper()
	return pullRun(context.Background(), pullConfig{
		ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath: releasePath,
		CatalogDirs: []string{catalogDir},
		PinSources:  pinSources,
		DiagOut:     io.Discard,
		Out:         io.Discard,
		diag:        buildDiagOptions{quiet: true},
	})
}

// buildSharedRecipeRelease builds one Release of the shared-recipe fixture the
// way `aggrega build --release` does, against the lockfile already on disk.
func buildSharedRecipeRelease(t *testing.T, root, catalogDir, releasePath string) error {
	t.Helper()
	return dispatchBuild(context.Background(), resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		releasePath, []string{catalogDir}, false, io.Discard, io.Discard)
}

// requireAuthoredRecipe reports the Recipe file holding anything other than the
// bytes its author wrote, and any entity lock written beside it, naming the
// command after which it was seen. A templated source's identity is known only
// once a Release supplies its infra, so it is recorded in that Release's lockfile
// and never beside the entity; the comparison is byte for byte because every
// Release's lockfile pins the Recipe's content digest, and any byte a command
// moved would move that digest under the other Release.
func requireAuthoredRecipe(t *testing.T, recipePath string, authored []byte, after string) {
	t.Helper()
	if got := mustReadFile(t, recipePath); !bytes.Equal(got, authored) {
		t.Errorf("after %s the Recipe file no longer holds the bytes its author wrote:\n--- authored\n%s--- on disk\n%s", after, authored, got)
	}
	if _, present, err := recipepin.ReadLock(recipePath); err != nil || present {
		t.Errorf("after %s an entity lock sits beside the Recipe (present=%v, err=%v); nothing about a templated source is recorded there", after, present, err)
	}
}

// lockedChartVersions returns, sorted, the versions of chart pinchart the
// Release's own lockfile records as resolved from the shared Recipe's source. A
// row with no digest, or one that does not name the shared Recipe as the entity
// that referenced it, fails the test.
func lockedChartVersions(t *testing.T, releasePath string) []string {
	t.Helper()
	path := lockfile.PathFor(releasePath)
	lf, _, err := lockfile.LoadLockfile(path)
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	var versions []string
	for _, sa := range lf.SourceArtifacts {
		if sa.Chart != "pinchart" {
			continue
		}
		if sa.Digest == "" {
			t.Errorf("%s records chart pinchart %s with no digest", path, sa.PinnedVersion)
		}
		if !slices.ContainsFunc(sa.ReferencedBy, func(r lockfile.SourceArtifactReference) bool {
			return r.Entity == "recipe" && r.URI == sharedRecipeURI && r.Field == "spec.source"
		}) {
			t.Errorf("%s records chart pinchart %s without naming %s's spec.source as its consumer: %+v", path, sa.PinnedVersion, sharedRecipeURI, sa.ReferencedBy)
		}
		versions = append(versions, sa.PinnedVersion)
	}
	sort.Strings(versions)
	return versions
}

// TestTwoReleasesShareOneRecipeWithoutRewritingIt proves that two Releases over
// one catalog coexist when they resolve one Recipe's templated source version to
// two different chart versions. Pull each, then build each, with no pull in
// between: all four commands succeed, in either order, and each build renders the
// chart version its own Release asked for — read out of the rendered artifact
// rather than taken from the command's result.
//
// What makes that hold is that no command writes the author's Recipe. Each
// Release's lockfile records the source identity that Release resolved, and pins
// the Recipe's content digest besides, so a command that moved a byte of the
// Recipe would leave the other Release's lockfile out of date and its next build
// would halt. The Recipe file is therefore compared byte for byte with what its
// author wrote after every one of the four commands. The sequence runs with the
// pin step at the workspace default and switched on, because the pin step is the
// one writer a pull runs beside an entity.
func TestTwoReleasesShareOneRecipeWithoutRewritingIt(t *testing.T) {
	orders := []struct{ name, first, second string }{
		{"A then B", "a", "b"},
		{"B then A", "b", "a"},
	}
	pinModes := []struct {
		name       string
		pinSources *bool
	}{
		{"workspace default", nil},
		{"pin step on", boolPtr(true)},
	}
	for _, order := range orders {
		for _, mode := range pinModes {
			t.Run(order.name+", "+mode.name, func(t *testing.T) {
				srv := serveMultiVersionHelmRepo(t, "0.6.0", "0.7.0")
				root := t.TempDir()
				t.Chdir(root)
				relA, relB := sharedRecipeTwoReleasesFixture(t, root, srv.URL)
				catalogDir := filepath.Join(root, "catalog")
				recipePath := filepath.Join(catalogDir, "recipes", "chartuser.yaml")
				release := map[string]string{"a": relA, "b": relB}
				wantVersion := map[string]string{"a": "0.6.0", "b": "0.7.0"}
				authored := mustReadFile(t, recipePath)

				for _, which := range []string{order.first, order.second} {
					step := "pull " + which
					if err := pullSharedRecipeRelease(t, root, catalogDir, release[which], mode.pinSources); err != nil {
						t.Fatalf("%s: %v", step, err)
					}
					requireAuthoredRecipe(t, recipePath, authored, step)
				}
				for _, which := range []string{order.first, order.second} {
					step := "build " + which
					err := buildSharedRecipeRelease(t, root, catalogDir, release[which])
					requireAuthoredRecipe(t, recipePath, authored, step)
					if err != nil {
						t.Errorf("%s after pull %s, pull %s: %v", step, order.first, order.second, err)
						continue
					}
					if got := renderedChartVersion(t, filepath.Join(root, "dist", which)); got != wantVersion[which] {
						t.Errorf("Release %s rendered chart version %q, want %s", which, got, wantVersion[which])
					}
				}
				for _, which := range []string{"a", "b"} {
					if got, want := lockedChartVersions(t, release[which]), []string{wantVersion[which]}; !slices.Equal(got, want) {
						t.Errorf("the lockfile of Release %s records chart versions %v, want %v", which, got, want)
					}
				}
			})
		}
	}
}

// TestSharedRecipeStalenessGuardStillRefuses is the counterweight to the test
// above. The staleness check exists to catch a catalog that genuinely moved under
// a lockfile, and two Releases sharing one Recipe must not be bought by making
// that check pass. Both Releases are pulled and built as a positive control, then
// the Recipe is edited in a way unrelated to source resolution — it gains an
// output label, which changes what every rendered object carries and touches no
// source coordinate — and each Release's next build must halt as out of date.
func TestSharedRecipeStalenessGuardStillRefuses(t *testing.T) {
	srv := serveMultiVersionHelmRepo(t, "0.6.0", "0.7.0")
	root := t.TempDir()
	t.Chdir(root)
	relA, relB := sharedRecipeTwoReleasesFixture(t, root, srv.URL)
	catalogDir := filepath.Join(root, "catalog")
	recipePath := filepath.Join(catalogDir, "recipes", "chartuser.yaml")
	release := map[string]string{"a": relA, "b": relB}

	for _, which := range []string{"a", "b"} {
		if err := pullSharedRecipeRelease(t, root, catalogDir, release[which], nil); err != nil {
			t.Fatalf("pull %s: %v", which, err)
		}
	}
	// Positive control: without the edit these same builds succeed, so each halt
	// below is attributable to the edit rather than to the fixture.
	for _, which := range []string{"a", "b"} {
		if err := buildSharedRecipeRelease(t, root, catalogDir, release[which]); err != nil {
			t.Fatalf("positive control: build %s after both pulls failed: %v", which, err)
		}
	}

	before := string(mustReadFile(t, recipePath))
	writeFixture(t, recipePath, strings.Replace(before,
		"  capability: helm\n",
		"  capability: helm\n  outputLabels:\n    example.com/tier: edge\n", 1))
	if after := string(mustReadFile(t, recipePath)); after == before {
		t.Fatalf("the unrelated-change edit did not change the recipe file; the assertion below would prove nothing:\n%s", before)
	}

	for _, which := range []string{"a", "b"} {
		err := buildSharedRecipeRelease(t, root, catalogDir, release[which])
		if err == nil {
			t.Errorf("build %s succeeded over a Recipe edited after its lockfile was written; the staleness check no longer refuses a catalog that moved", which)
			continue
		}
		if !errors.Is(err, lockfile.ErrLockfileDrift()) {
			t.Errorf("build %s halted with %v; want the lockfile staleness condition", which, err)
		}
	}
}

// TestOneReleaseTwoConsumersOneRecipe establishes where a templated source's
// identities are recorded when one Release resolves it more than once. A Recipe
// consumed by two component instances whose infra differs resolves to two source
// identities inside ONE resolution: the Release's own lockfile records one entry
// per identity, and the Recipe file is left exactly as its author wrote it. Each
// instance then renders the chart version its own infra asked for, read from the
// object it rendered.
func TestOneReleaseTwoConsumersOneRecipe(t *testing.T) {
	srv := serveMultiVersionHelmRepo(t, "0.6.0", "0.7.0")
	root := t.TempDir()
	t.Chdir(root)
	catalogDir := filepath.Join(root, "catalog")
	sharedRecipeTwoReleasesFixture(t, root, srv.URL)
	// Two components of the same type, so one Recipe binds to both.
	writeFixture(t, filepath.Join(root, "aggregate.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Aggregate
metadata:
  name: aggregate
  version: "1.0.0"
spec:
  components:
    api:
      type: server
      values:
        port: 8080
    api2:
      type: server
      values:
        port: 8081
`)
	writeFixture(t, filepath.Join(root, "release-both.yaml"), `apiVersion: aggrega.ai/v1alpha1
kind: Release
metadata:
  name: release-both
  environment: testing
  version: "1.0.0"
spec:
  aggregate: ./aggregate.yaml
  manifest: ./manifest.yaml
  boundaries:
    main:
      directory: ./both
      components:
        api:
          infra:
            chart_version: "0.6.0"
        api2:
          infra:
            chart_version: "0.7.0"
`)
	releasePath := filepath.Join(root, "release-both.yaml")
	recipePath := filepath.Join(catalogDir, "recipes", "chartuser.yaml")
	authored := mustReadFile(t, recipePath)

	if err := pullSharedRecipeRelease(t, root, catalogDir, releasePath, nil); err != nil {
		t.Fatalf("pull: %v", err)
	}
	requireAuthoredRecipe(t, recipePath, authored, "pull")
	if got, want := lockedChartVersions(t, releasePath), []string{"0.6.0", "0.7.0"}; !slices.Equal(got, want) {
		t.Errorf("the Release's lockfile records chart versions %v after one pull with two consuming instances, want %v", got, want)
	}

	if err := buildSharedRecipeRelease(t, root, catalogDir, releasePath); err != nil {
		t.Fatalf("build after one pull: %v", err)
	}
	requireAuthoredRecipe(t, recipePath, authored, "build")
	got := renderedChartVersionsByName(t, filepath.Join(root, "dist", "both"))
	if want := map[string]string{"api-pin": "0.6.0", "api2-pin": "0.7.0"}; !maps.Equal(got, want) {
		t.Errorf("rendered chart versions by object = %v, want %v", got, want)
	}
}
