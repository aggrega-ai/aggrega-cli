// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"helm.sh/helm/v4/pkg/cli"
	"helm.sh/helm/v4/pkg/getter"
)

// fakeGetter is a static-body httpsGetter used in tests.
type fakeGetter struct {
	body []byte
	err  error
}

func (f *fakeGetter) Get(href string, options ...getter.Option) (*tarGzBuffer, error) {
	if f.err != nil {
		return nil, f.err
	}
	return &tarGzBuffer{data: f.body}, nil
}

func TestHTTPSResolveDirectTGZ(t *testing.T) {
	t.Parallel()
	body := []byte("fake-helm-chart-tgz-content\n")
	a := &httpsAdapter{
		getter: &fakeGetter{body: body},
	}

	root := t.TempDir()
	got, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          KindHTTPS,
		URL:           "https://charts.example.com/mariadb-25.0.8.tgz",
		Version:       "25.0.8",
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.URL != "https://charts.example.com/mariadb-25.0.8.tgz" {
		t.Errorf("URL = %q; want direct .tgz preserved", got.URL)
	}
	// A direct .tgz resolves to itself: no chart segment and no separate
	// provenance, so its slot and metadata stay identical to the pre-chart shape.
	if got.Chart != "" {
		t.Errorf("Chart should be empty for a direct .tgz; got %q", got.Chart)
	}
	if got.ResolvedURL != "" {
		t.Errorf("ResolvedURL should be empty for a direct .tgz; got %q", got.ResolvedURL)
	}
	if _, err := os.Stat(got.CachePath); err != nil {
		t.Errorf("cache blob missing: %v", err)
	}
	m, err := metadata.Read(filepath.Join(filepath.Dir(got.CachePath), "metadata.yaml"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if m.Mediatype != MediatypeHelmChart {
		t.Errorf("metadata mediatype = %q; want %s", m.Mediatype, MediatypeHelmChart)
	}
	if m.ResolvedURL != "" {
		t.Errorf("metadata resolved_url should be omitted for a direct .tgz; got %q", m.ResolvedURL)
	}
}

func TestHTTPSResolveRepoRootViaIndex(t *testing.T) {
	t.Parallel()
	body := []byte("fake-helm-chart-tgz-content\n")
	a := &httpsAdapter{
		resolveIndex: func(repoURL, chart, version string, _ ...getter.Option) (string, error) {
			if chart != "mariadb" || version != "25.0.8" {
				t.Errorf("resolveIndex called with %s@%s", chart, version)
			}
			return repoURL + "/charts/mariadb-25.0.8.tgz", nil
		},
		getter: &fakeGetter{body: body},
	}

	root := t.TempDir()
	got, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          KindHTTPS,
		URL:           "https://charts.bitnami.com/bitnami",
		Chart:         "mariadb",
		Version:       "25.0.8",
		WorkspaceRoot: root,
		CacheDir:      ".aggrega/cache",
	})
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// Identity is the authored repo-root URL plus the chart name — NOT the
	// resolved .tgz. Build reconstructs this same authored identity offline, so
	// the cache/lockfile lookup hits.
	if got.URL != "https://charts.bitnami.com/bitnami" {
		t.Errorf("URL should be the authored repo-root identity; got %q", got.URL)
	}
	if got.Chart != "mariadb" {
		t.Errorf("Chart should carry the chart name; got %q", got.Chart)
	}
	// The resolved .tgz is provenance only.
	if got.ResolvedURL != "https://charts.bitnami.com/bitnami/charts/mariadb-25.0.8.tgz" {
		t.Errorf("ResolvedURL should be the resolved direct .tgz provenance; got %q", got.ResolvedURL)
	}
	// The cache metadata records the authored URL as identity and the resolved
	// .tgz as provenance.
	m, err := metadata.Read(filepath.Join(filepath.Dir(got.CachePath), "metadata.yaml"))
	if err != nil {
		t.Fatalf("read metadata: %v", err)
	}
	if m.URL != "https://charts.bitnami.com/bitnami" {
		t.Errorf("metadata URL should be authored identity; got %q", m.URL)
	}
	if m.ResolvedURL != "https://charts.bitnami.com/bitnami/charts/mariadb-25.0.8.tgz" {
		t.Errorf("metadata resolved_url should be the resolved .tgz; got %q", m.ResolvedURL)
	}
}

func TestHTTPSResolveRepoRootMissingChart(t *testing.T) {
	t.Parallel()
	a := &httpsAdapter{
		resolveIndex: func(repoURL, chart, version string, _ ...getter.Option) (string, error) { return "", nil },
	}
	_, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          KindHTTPS,
		URL:           "https://charts.example.com/repo",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if !errors.Is(err, sourceresolver.ErrFetchNotFound()) {
		t.Errorf("missing chart should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestHTTPSResolveGetterNotFound(t *testing.T) {
	t.Parallel()
	a := &httpsAdapter{
		getter: &fakeGetter{err: errors.New(`failed to fetch: 404 Not Found`)},
	}
	_, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          KindHTTPS,
		URL:           "https://charts.example.com/missing.tgz",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if !errors.Is(err, sourceresolver.ErrFetchNotFound()) {
		t.Errorf("404 should wrap ErrFetchNotFound; got %v", err)
	}
}

func TestHTTPSResolveGetterUnauthorized(t *testing.T) {
	t.Parallel()
	a := &httpsAdapter{
		getter: &fakeGetter{err: errors.New(`failed to fetch: 401 Unauthorized`)},
	}
	_, err := a.Resolve(context.Background(), sourceresolver.SourceSpec{
		Kind:          KindHTTPS,
		URL:           "https://charts.example.com/private.tgz",
		Version:       "1.0.0",
		WorkspaceRoot: t.TempDir(),
		CacheDir:      ".aggrega/cache",
	})
	if !errors.Is(err, sourceresolver.ErrFetchUnauthorized()) {
		t.Errorf("401 should wrap ErrFetchUnauthorized; got %v", err)
	}
}

func TestProductionGetterAgainstHTTPTest(t *testing.T) {
	t.Parallel()
	body := []byte("fake-helm-chart-tgz-content\n")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	pg := &productionGetter{providers: builtInProviders()}
	got, err := pg.Get(srv.URL + "/chart.tgz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Bytes()) != string(body) {
		t.Errorf("body mismatch; got %d bytes", len(got.Bytes()))
	}
}

// plantedGetterPluginScheme is the URL scheme the planted getter plugin claims.
// No built-in getter serves it, so a provider for it can only be the plugin.
const plantedGetterPluginScheme = "aggregaplanted"

// plantedGetterPlugin is a getter plugin in the form Helm loads one: a
// plugin.yaml one directory below the directory Helm scans, claiming
// plantedGetterPluginScheme and naming a command to run for it.
const plantedGetterPlugin = `name: "planted"
version: "0.1.0"
type: getter/v1
apiVersion: v1
runtime: subprocess
config:
  protocols:
    - "` + plantedGetterPluginScheme + `"
runtimeConfig:
  protocolCommands:
    - platformCommand:
        - command: "echo"
      protocols:
        - "` + plantedGetterPluginScheme + `"
`

// TestTheHTTPSAdapterRegistersNoGetterPluginFromTheWorkingDirectory holds that a
// getter plugin sitting under the directory the process runs from adds no
// download handler to the adapter, while the built-in http, https and oci
// handlers stay registered.
//
// The control comes first and is what makes the verdict mean something: in the
// same directory, Helm's plugin-aware constructor, handed settings naming no
// plugin directory, does register the planted plugin — so the fixture is a
// plugin Helm loads from exactly where it sits, and the adapter's refusal of it
// is not a fixture Helm would refuse anyway.
//
// It changes the process's working directory and environment, so it never runs
// in parallel. Loading a plugin makes Helm create a compilation cache under its
// cache home, so the control points Helm's homes at the test's own directories
// and leaves the developer's untouched.
func TestTheHTTPSAdapterRegistersNoGetterPluginFromTheWorkingDirectory(t *testing.T) {
	t.Setenv("HELM_CACHE_HOME", t.TempDir())
	t.Setenv("HELM_CONFIG_HOME", t.TempDir())
	t.Setenv("HELM_DATA_HOME", t.TempDir())
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "planted"), 0o755); err != nil {
		t.Fatalf("make the plugin directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "planted", "plugin.yaml"), []byte(plantedGetterPlugin), 0o644); err != nil {
		t.Fatalf("plant the plugin: %v", err)
	}
	t.Chdir(dir)

	discovered := getter.All(&cli.EnvSettings{})
	if _, err := discovered.ByScheme(plantedGetterPluginScheme); err != nil {
		t.Fatalf("control: Helm's plugin-aware constructor did not register the planted plugin from the "+
			"working directory, so this test cannot tell the adapter's refusal from a fixture Helm ignores: %v", err)
	}

	adapter, ok := NewHTTPSAdapter().(*httpsAdapter)
	if !ok {
		t.Fatalf("NewHTTPSAdapter returned %T, want *httpsAdapter", NewHTTPSAdapter())
	}
	production, ok := adapter.getter.(*productionGetter)
	if !ok {
		t.Fatalf("the adapter's getter is %T, want *productionGetter", adapter.getter)
	}

	if _, err := production.providers.ByScheme(plantedGetterPluginScheme); err == nil {
		t.Errorf("the adapter registered a download handler for %q from the planted plugin", plantedGetterPluginScheme)
	}
	if _, err := production.Get(plantedGetterPluginScheme + "://charts.example.com/chart.tgz"); err == nil {
		t.Errorf("the adapter fetched a %q URL; want a refusal naming the unsupported scheme", plantedGetterPluginScheme)
	}
	for _, scheme := range []string{"http", "https", "oci"} {
		if _, err := production.providers.ByScheme(scheme); err != nil {
			t.Errorf("the adapter has no built-in handler for %q: %v", scheme, err)
		}
	}
	if got, want := len(production.providers), len(getter.Getters()); got != want {
		t.Errorf("the adapter registered %d download providers, want Helm's %d built-in ones", got, want)
	}
}
