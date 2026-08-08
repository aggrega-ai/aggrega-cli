// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/metadata"
	"helm.sh/helm/v3/pkg/getter"
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
	got, err := a.Resolve(sourceresolver.SourceSpec{
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
	got, err := a.Resolve(sourceresolver.SourceSpec{
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
	_, err := a.Resolve(sourceresolver.SourceSpec{
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
	_, err := a.Resolve(sourceresolver.SourceSpec{
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
	_, err := a.Resolve(sourceresolver.SourceSpec{
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

	pg := &productionGetter{providers: newProvidersNoEnv()}
	got, err := pg.Get(srv.URL + "/chart.tgz")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(got.Bytes()) != string(body) {
		t.Errorf("body mismatch; got %d bytes", len(got.Bytes()))
	}
}
