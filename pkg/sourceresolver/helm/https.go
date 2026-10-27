// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package helm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourcekind"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/cacheslot"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/internal/archive"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver/netrc"
	"helm.sh/helm/v4/pkg/getter"
	repo "helm.sh/helm/v4/pkg/repo/v1"
)

// KindHTTPS names the helm-https-chart source kind, whose one declaration is
// [sourcekind.HelmHTTPSChart].
const KindHTTPS = sourcekind.HelmHTTPSChart

// indexResolver is the seam between production index-file
// resolution (fetch index.yaml from the repo root, parse with
// helm's repo.LoadIndexFile, look up the chart+version) and the
// test-injected resolver that returns a fixed URL without network
// access. optionsFor returns the getter options for a fetch of one URL,
// so the index fetch carries the credential for its own host.
type indexResolver func(repoURL, chart, version string, optionsFor func(href string) []getter.Option) (directURL string, err error)

// httpsGetter abstracts Helm's getter.Getter so tests can inject a
// static-body implementation without spinning up helm's full provider
// machinery.
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

// NewHTTPSAdapter returns a helm-https-chart adapter wired to Helm's
// built-in getters (see builtInProviders — no plugin is discovered, and
// no $KUBECONFIG / $HELM_* is read) and the production index resolver
// (downloads index.yaml from the repo root, parses with
// repo.LoadIndexFile, looks up the chart+version, returns the
// direct .tgz URL).
func NewHTTPSAdapter() sourceresolver.Adapter {
	g := &productionGetter{providers: builtInProviders()}
	return &httpsAdapter{
		resolveIndex: func(repoURL, chart, version string, optionsFor func(href string) []getter.Option) (string, error) {
			return resolveFromIndexProduction(g, repoURL, chart, version, optionsFor)
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
//     fetches the URL directly. The authored URL is the resolved URL,
//     so the identity URL is spec.URL and no separate provenance is
//     recorded.
//   - **Repo-root URL** (any other shape, with spec.Chart populated):
//     the adapter fetches `<repo-root>/index.yaml`, looks up
//     spec.Chart + spec.Version, extracts the direct .tgz URL, and
//     fetches it from there.
//
// The **authored** URL (spec.URL) plus spec.Chart is the artifact
// identity in every case — it is what the cache slot, the lockfile
// entry, and the recipe pin are keyed on, and what build reconstructs
// offline. The concrete .tgz location a repo-root form resolves to is
// recorded as provenance only (ResolvedSource.ResolvedURL and the cache
// metadata's resolved_url), never as identity — so build-time digest
// verification reads the authored-URL cache slot and NEVER re-fetches
// index.yaml. Keying identity on the authored URL also keeps two charts
// served from one repo root at the same version distinct (they share a
// URL but differ in Chart).
//
// The upstream archive is pinned verbatim (no normalisation pass) —
// archive.Digest computes the SHA-256 over the upstream bytes, which
// matches the digest Helm conventionally distributes via
// `provenance.yaml` or `<chart>.tgz.prov`. Drift between consecutive
// pulls of the same version is caught at the next pull via
// ErrFetchIntegrity.
//
// Each fetch carries the credential the netrc file spec.Netrc names holds
// for that fetch's own host — the index.yaml lookup and the chart download
// are asked separately, since an index can place a chart on another host —
// and a URL carrying a user and password of its own sends those instead.
// The file is read first, and one that cannot be read or does not parse
// halts the resolution before any request.
//
// Both fetches this method can make — the index.yaml lookup and the
// chart download — run through Helm's getter, which accepts no context
// of its own. The caller's ctx therefore reaches them underneath, by
// way of the transport they are given: see transportEndingWith. Ending
// ctx closes the connection beneath whichever fetch is in flight, so
// the transfer stops on the wire rather than continuing behind a caller
// that has stopped waiting.
func (a *httpsAdapter) Resolve(ctx context.Context, spec sourceresolver.SourceSpec) (*sourceresolver.ResolvedSource, error) {
	credentials, err := spec.Netrc.Load()
	if err != nil {
		return nil, err
	}

	// Production routes every fetch — the chart .tgz and any index.yaml lookup —
	// through the network-egress gate's transport, so an offline workspace is
	// refused before any dial and helm never builds its own ungated transport.
	// A test-injected getter carries its own static body and never touches the
	// network, so the gate applies only to the production getter.
	var httpOpts []getter.Option
	if _, isProd := a.getter.(*productionGetter); isProd {
		transport, ok := egressTransport(spec.Client)
		if !ok {
			return nil, egress.ErrOfflineEgress()
		}
		// One option carries the stop to both fetches, because this slice is what
		// the index lookup and the chart download are each handed.
		stoppable := transportEndingWith(ctx, transport)
		defer stoppable.CloseIdleConnections()
		httpOpts = append(httpOpts, getter.WithTransport(stoppable))
	}
	optionsFor := func(href string) []getter.Option {
		return withCredentialFor(href, credentials, httpOpts)
	}

	fetchURL := spec.URL
	// resolvedURL is provenance: empty unless the authored URL differs from the
	// concrete .tgz the index resolves to (the repo-root form), keeping the
	// direct-.tgz form's recorded metadata free of a redundant pointer.
	var resolvedURL string
	if !strings.HasSuffix(strings.ToLower(spec.URL), ".tgz") {
		if spec.Chart == "" {
			return nil, sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound(),
				errors.New("repo-root URL requires spec.Chart"))
		}
		u, err := a.resolveIndex(spec.URL, spec.Chart, spec.Version, optionsFor)
		if err != nil {
			return nil, mapHTTPError(spec, err)
		}
		fetchURL = u
		resolvedURL = u
	}

	buf, err := a.getter.Get(fetchURL, optionsFor(fetchURL)...)
	if err != nil {
		return nil, mapHTTPError(spec, err)
	}
	body := buf.Bytes()
	digest := archive.Digest(body)
	slot, err := cacheslot.Slot(spec.WorkspaceRoot, spec.CacheDir, KindHTTPS, spec.URL, spec.Chart, "", spec.Version, digest)
	if err != nil {
		return nil, fmt.Errorf("helm-https-chart: %w", err)
	}
	// An HTTPS-distributed chart has no OCI manifest; the archive is verified
	// against the recorded content digest alone. The slot and metadata are keyed
	// on the authored URL (spec.URL); the resolved .tgz is recorded as provenance.
	if err := cacheslot.WriteBlobAndMetadata(spec.WorkspaceRoot, spec.CacheDir, slot, body,
		KindHTTPS, spec.URL, spec.Version, digest, MediatypeHelmChart, resolvedURL, nil); err != nil {
		return nil, fmt.Errorf("helm-https-chart: %w", err)
	}

	return &sourceresolver.ResolvedSource{
		Kind:           KindHTTPS,
		URL:            spec.URL,
		Chart:          spec.Chart,
		PinnedVersion:  spec.Version,
		Digest:         digest,
		ResolvedURL:    resolvedURL,
		Mediatype:      MediatypeHelmChart,
		CachePath:      slot + "/blob.tgz",
		OriginalSource: spec.OriginalSource,
	}, nil
}

// withCredentialFor returns opts plus, where the netrc file holds a credential for
// href's host, that credential scoped to href.
//
// It is asked once per fetch, for the URL that fetch requests, because the two
// fetches of a repository root can reach two hosts: an index may place the chart
// archive anywhere. Helm's getter sends basic credentials only to the scheme and
// host of the URL they are scoped to, so scoping them to href itself is what
// gives each request its own host's credential and no other's.
func withCredentialFor(href string, credentials *netrc.File, opts []getter.Option) []getter.Option {
	cred, ok := credentials.ForURL(href)
	if !ok {
		return opts
	}
	out := make([]getter.Option, 0, len(opts)+2)
	out = append(out, opts...)
	return append(out, getter.WithURL(href), getter.WithBasicAuth(cred.Login, cred.Password))
}

// builtInProviders returns Helm's built-in getters — http and https in one
// provider, oci in the other — and nothing else.
//
// It never discovers a getter plugin. Helm's plugin-aware constructor reads
// its plugins from a directory, and with no directory configured it reads the
// process's working directory instead, so a build run from a checked-out
// repository would let that repository's contents register download handlers
// of their own. A helm-https-chart source is fetched over http or https, which
// the built-in getters serve, so no plugin is consulted.
//
// The built-in getters read no $KUBECONFIG / $HELM_* either, so source
// resolution is reproducible across developer machines and CI environments.
func builtInProviders() getter.Providers {
	return getter.Getters()
}

// transportEndingWith returns a copy of base whose connections belong to ctx: it
// dials under ctx, and it closes a connection already established when ctx ends.
//
// This is where a stop reaches an HTTPS chart fetch, and it has to be here
// because Helm's getter takes no context — it builds its own request and hands it
// to whatever transport it was given, so there is no argument to thread and no
// call to wrap. Closing the connection underneath is what actually ends the
// transfer: the read in progress fails and the getter returns because its fetch
// broke, not because anyone stopped waiting for it. Watching for the stop beside
// the fetch and returning early would look the same to the caller while leaving
// the download running, which is the opposite of what this is for.
//
// The copy is per-resolution, since it carries that resolution's context, and it
// keeps a connection pool of its own rather than sharing the gate's — so the
// caller closes its idle connections once the fetch is done. Copying is also what
// keeps the gate's own shared transport untouched: it is process-wide, and
// giving it a context would hand one resolution's lifetime to every other
// caller of it.
func transportEndingWith(ctx context.Context, base *http.Transport) *http.Transport {
	t := base.Clone()
	dial := t.DialContext
	if dial == nil {
		dial = (&net.Dialer{}).DialContext
	}
	t.DialContext = func(_ context.Context, network, addr string) (net.Conn, error) {
		// Dialling under ctx rather than the request's own background context is
		// what stops a fetch whose caller withdrew before the connection came up.
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		return &connEndingWithContext{
			Conn:    conn,
			release: context.AfterFunc(ctx, func() { _ = conn.Close() }),
		}, nil
	}
	return t
}

// connEndingWithContext closes when the context it was dialled under ends, so a
// transfer already in flight fails instead of running on unobserved. An ordinary
// close releases the registration too, so a connection on a long-lived context
// leaves nothing behind it.
type connEndingWithContext struct {
	net.Conn
	release func() bool
}

func (c *connEndingWithContext) Close() error {
	c.release()
	return c.Conn.Close()
}

// egressTransport extracts the concrete *http.Transport the network-egress gate
// constructs, for injection into the helm getter via getter.WithTransport (which
// takes a *http.Transport, not a RoundTripper). A nil client is the gate's
// offline signal; a client whose transport is not a *http.Transport reports not
// ok so the caller refuses rather than let the getter build its own transport.
func egressTransport(client *http.Client) (*http.Transport, bool) {
	if client == nil {
		return nil, false
	}
	t, ok := client.Transport.(*http.Transport)
	return t, ok
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
func resolveFromIndexProduction(g *productionGetter, repoURL, chart, version string, optionsFor func(href string) []getter.Option) (string, error) {
	indexURL := strings.TrimRight(repoURL, "/") + "/index.yaml"
	buf, err := g.Get(indexURL, optionsFor(indexURL)...)
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
// HTTP-status errors with "failed to fetch <url> : <status>"; we
// match against the textual fragments because the wrapped error
// chain doesn't expose typed status codes. The status is read from
// after the URL, so a URL whose port or path holds the digits of a
// status is not mistaken for that status.
func mapHTTPError(spec sourceresolver.SourceSpec, err error) error {
	msg := err.Error()
	status := msg
	if i := strings.LastIndex(msg, " : "); i >= 0 {
		status = msg[i+len(" : "):]
	}
	switch {
	case strings.Contains(status, "404"):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNotFound(), err)
	case strings.Contains(status, "401"), strings.Contains(status, "403"):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchUnauthorized(), err)
	case strings.Contains(msg, "no such host"),
		strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "i/o timeout"):
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(), err)
	default:
		return sourceresolver.Wrap(spec, sourceresolver.ErrFetchNetworkUnreachable(), err)
	}
}

// helpful — keeps the filepath import healthy when build-tagged
// future evolution introduces a chart-name-derived cache filename.
var _ = filepath.Join

func init() {
	sourceresolver.Register(NewHTTPSAdapter())
}
