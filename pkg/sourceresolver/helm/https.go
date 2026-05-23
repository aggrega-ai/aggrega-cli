// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/cacheslot"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"
)

// KindHTTPS names the helm-https-chart source kind.
const KindHTTPS = "helm-https-chart"

// indexResolver is the seam between production index-file
// resolution (fetch index.yaml from the repo root, parse with
// helm's repo.LoadIndexFile, look up the chart+version) and the
// test-injected resolver that returns a fixed URL without network
// access.
type indexResolver func(repoURL, chart, version string) (directURL string, err error)

// httpsGetter abstracts helm.sh/helm/v3/pkg/getter.Getter so tests
// can inject a static-body implementation without spinning up
// helm's full provider machinery.
type httpsGetter interface {
	Get(href string, options ...getter.Option) (*tarGzBuffer, error)
}

// tarGzBuffer wraps the *bytes.Buffer the helm getter returns so the
// adapter sees a typed surface rather than poking at *bytes.Buffer
// directly. Production wires the helm getter's *bytes.Buffer into
// this shape via the adapter helpers.
type tarGzBuffer struct {
	data []byte
}

func (t *tarGzBuffer) Bytes() []byte { return t.data }

type httpsAdapter struct {
	resolveIndex indexResolver
	getter       httpsGetter
}

// NewHTTPSAdapter returns a helm-https-chart adapter wired to the
// production helm getter (programmatic cli.EnvSettings — no
// $KUBECONFIG / $HELM_* reads) and the production index resolver
// (downloads index.yaml from the repo root, parses with
// repo.LoadIndexFile, looks up the chart+version, returns the
// direct .tgz URL).
func NewHTTPSAdapter() sourceresolver.Adapter {
	g := &productionGetter{providers: newProvidersNoEnv()}
	return &httpsAdapter{
		resolveIndex: func(repoURL, chart, version string) (string, error) {
			return resolveFromIndexProduction(g, repoURL, chart, version)
		},
		getter: g,
	}
}

// Kind returns the helm-https-chart kind string.
func (a *httpsAdapter) Kind() string { return KindHTTPS }

// Resolve pulls the chart referenced by spec via HTTPS. Two URL
// shapes are accepted:
//
//   - **Direct .tgz URL** (spec.URL ends in `.tgz`): the adapter
//     fetches the URL directly. The lockfile records spec.URL
//     verbatim as the canonical URL.
//   - **Repo-root URL** (any other shape, with spec.Chart populated):
//     the adapter fetches `<repo-root>/index.yaml`, looks up
//     spec.Chart + spec.Version, extracts the direct .tgz URL, and
//     fetches it. The lockfile records the **resolved direct .tgz
//     URL** as the canonical URL, NOT the repo-root URL —
//     build-time digest verification reads from the resolved URL's
//     cache slot and NEVER re-fetches index.yaml.
//
// The upstream archive is pinned verbatim (no normalisation pass) —
// archive.Digest computes the SHA-256 over the upstream bytes, which
// matches the digest Helm conventionally distributes via
// `provenance.yaml` or `<chart>.tgz.prov`. Drift between consecutive
// pulls of the same version is caught at the next pull via
// ErrFetchIntegrity.
func (a *httpsAdapter) Resolve(spec sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	resolvedURL := spec.URL
	if !strings.HasSuffix(strings.ToLower(spec.URL), ".tgz") {
		if spec.Chart == "" {
			return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound,
				errors.New("repo-root URL requires spec.Chart"))
		}
		u, err := a.resolveIndex(spec.URL, spec.Chart, spec.Version)
		if err != nil {
			return nil, mapHTTPError(spec, err)
		}
		resolvedURL = u
	}

	buf, err := a.getter.Get(resolvedURL)
	if err != nil {
		return nil, mapHTTPError(spec, err)
	}
	body := buf.Bytes()
	digest := archive.Digest(body)
	slot, err := cacheslot.Slot(spec.WorkspaceRoot, spec.CacheDir, KindHTTPS, resolvedURL, spec.Version, digest)
	if err != nil {
		return nil, fmt.Errorf("helm-https-chart: %w", err)
	}
	if err := cacheslot.WriteBlobAndMetadata(slot, body,
		KindHTTPS, resolvedURL, spec.Version, digest, MediatypeHelmChart); err != nil {
		return nil, fmt.Errorf("helm-https-chart: %w", err)
	}

	return &sourceresolver.ResolvedSource{
		Kind:           KindHTTPS,
		URL:            resolvedURL,
		PinnedVersion:  spec.Version,
		Digest:         digest,
		Mediatype:      MediatypeHelmChart,
		CachePath:      slot + "/blob.tgz",
		OriginalSource: spec.OriginalSource,
	}, nil
}

// newProvidersNoEnv constructs Helm getter providers without
// reading $KUBECONFIG / $HELM_*. cli.EnvSettings fields are
// populated programmatically (all empty); the adapter never reads
// ambient environment so source resolution is reproducible across
// developer machines and CI environments.
func newProvidersNoEnv() getter.Providers {
	s := &cli.EnvSettings{
		RegistryConfig:   "",
		RepositoryConfig: "",
		RepositoryCache:  "",
		Debug:            false,
	}
	return getter.All(s)
}

// productionGetter wraps the helm getter providers in the
// httpsGetter seam so the adapter has a typed surface to consume.
type productionGetter struct {
	providers getter.Providers
}

func (p *productionGetter) Get(href string, options ...getter.Option) (*tarGzBuffer, error) {
	u, err := url.Parse(href)
	if err != nil {
		return nil, fmt.Errorf("helm-https-chart: parse URL %q: %w", href, err)
	}
	g, err := p.providers.ByScheme(u.Scheme)
	if err != nil {
		return nil, fmt.Errorf("helm-https-chart: get provider for %q: %w", u.Scheme, err)
	}
	buf, err := g.Get(href, options...)
	if err != nil {
		return nil, err
	}
	return &tarGzBuffer{data: buf.Bytes()}, nil
}

// resolveFromIndexProduction fetches `<repoURL>/index.yaml`, parses
// it with repo.LoadIndexFile (after writing to a temp file —
// helm's parser is file-based, not byte-based), looks up the chart
// + version, and returns the first direct .tgz URL from the
// entry's URLs slice. If the URL is relative, it is joined against
// the repo root per Helm's index.yaml convention.
func resolveFromIndexProduction(g *productionGetter, repoURL, chart, version string) (string, error) {
	indexURL := strings.TrimRight(repoURL, "/") + "/index.yaml"
	buf, err := g.Get(indexURL)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp("", "helm-index-*.yaml")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(buf.Bytes()); err != nil {
		_ = tmp.Close()
		return "", err
	}
	_ = tmp.Close()

	idx, err := repo.LoadIndexFile(tmp.Name())
	if err != nil {
		return "", err
	}
	ver, err := idx.Get(chart, version)
	if err != nil {
		return "", err
	}
	if len(ver.URLs) == 0 {
		return "", fmt.Errorf("helm-https-chart: index entry %s@%s has no URLs", chart, version)
	}
	chartURL := ver.URLs[0]
	if !strings.HasPrefix(chartURL, "http://") && !strings.HasPrefix(chartURL, "https://") {
		base := strings.TrimRight(repoURL, "/")
		chartURL = base + "/" + strings.TrimLeft(chartURL, "/")
	}
	return chartURL, nil
}

// mapHTTPError maps a raw helm getter / index-parse error to a
// sourceresolver-wrapped sentinel. Helm getter's HTTPGetter wraps
// HTTP-status errors with "failed to fetch ... : <status>"; we
// match against the textual fragments because the wrapped error
// chain doesn't expose typed status codes.
func mapHTTPError(spec sourceresolver.SourceSpec, err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "404"):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound, err)
	case strings.Contains(msg, "401"), strings.Contains(msg, "403"):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchUnauthorized, err)
	case strings.Contains(msg, "no such host"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "i/o timeout"):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
	default:
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable, err)
	}
}

// helpful — keeps the filepath import healthy when build-tagged
// future evolution introduces a chart-name-derived cache filename.
var _ = filepath.Join

func init() {
	sourceresolver.Register(NewHTTPSAdapter())
}
