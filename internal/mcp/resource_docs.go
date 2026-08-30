// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"errors"
	"strings"
	"time"

	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/egress"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/pullverify"
	"github.com/aggrega-ai/aggrega-cli/pkg/registryhost"
)

const (
	// schemeLangref addresses a language-reference page inside the
	// documentation bundle; schemeAuthoring addresses an authoring-guide topic.
	schemeLangref   = "langref"
	schemeAuthoring = "authoring"

	// langrefPrefix and authoringPrefix are the bundle-internal directories the
	// two schemes read from. A langref:// URI names a page beneath the
	// language-reference tree; an authoring:// URI names a topic beneath the
	// authoring tree.
	langrefPrefix   = "language-reference/"
	authoringPrefix = "authoring/"

	// docsBundleName is the artifact name of the documentation bundle within
	// the reserved docs registry namespace. The default reference is
	// <default_registry>/docs/<docsBundleName>.
	docsBundleName = "aggrega-docs"

	// docsNamespace is the reserved registry namespace standalone data
	// artifacts publish under; a documentation bundle lives at
	// <registry>/<docsNamespace>/<docsBundleName>.
	docsNamespace = "docs"
)

// docsResourceTemplates returns the templated resources the server serves: the
// language-reference and authoring-guide families, each a URI template that
// expands into a family of readable pages. They are templates rather than
// concrete resources/list entries because their content is parameterised by
// the page path or topic name.
func docsResourceTemplates() []resourceTemplate {
	return []resourceTemplate{
		{
			URITemplate: "langref://{path}",
			Name:        "language-reference",
			Description: "A language-reference page, served inline from the local documentation bundle. The path names a page within the reference (for example langref://entities/component).",
			MimeType:    "text/markdown",
		},
		{
			URITemplate: "authoring://{topic}",
			Name:        "authoring-guide",
			Description: "An authoring-guide topic, served inline from the local documentation bundle. The topic names a guide (for example authoring://getting-started).",
			MimeType:    "text/markdown",
		},
	}
}

// docsAcquireTimeout bounds the one acquisition attempt a session makes. The
// egress transport carries no deadline of its own, so without a bound an
// unreachable registry would hold the handshake open for as long as the
// operating system's connect timeout allows. It is generous enough for a cold
// pull of a documentation bundle over a slow link and short enough that a dead
// registry costs a pause rather than a session.
const docsAcquireTimeout = 30 * time.Second

// docsTargetFor resolves the OCI target the documentation bundle is pulled from
// and the pull-time signature check to run against it.
//
// It is deliberately the narrowest seam that removes the registry: everything
// after it — the cache-presence probe, the pull, the media-type check, the
// verification, the cache write and the read — is the production path, so a test
// substituting a target still exercises all of it. A seam one level higher, over
// the whole acquisition, would take the cache write out of the tested path and
// leave the trigger proven against nothing.
//
// Nil selects the production resolution below.
type docsTargetFor func(tw *toolWorkspace, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error)

// ensureDocsBundle runs the first-use acquisition of the documentation bundle:
// once per session, at the handshake, and never on a resource read.
//
// Siting it at the handshake rather than on the read is the whole of the offline
// contract. ReadPage takes no target and cannot reach the network; folding a
// fetch into the read would put a registry round-trip behind every documentation
// request and make a served page's availability depend on connectivity at the
// moment it is asked for. Acquiring once, ahead of any read, keeps the read
// offline and the answer warm.
//
// It never fails the handshake. An offline workspace, a registry that does not
// serve the bundle, and a signature that does not verify each leave the cache
// exactly as it was; the reason goes to the log stream and the documentation
// resources answer resource-not-found, which is a poorer answer than a page and
// a far better one than a refused session.
func (s *server) ensureDocsBundle() {
	s.docsBundleOnce.Do(func() {
		tw, err := s.resolveToolWorkspace("", nil)
		if err != nil {
			s.logf("mcp: documentation bundle not acquired: resolve workspace: %v", err)
			return
		}
		ref, version := s.docsBundleCoords(tw)
		if docscache.Cached(tw.root, tw.cacheDir, ref, version) {
			return
		}
		targetFor := s.docsTarget
		if targetFor == nil {
			targetFor = docsRegistryTarget
		}
		warn := func(notice string) { s.logf("mcp: %s", notice) }
		target, verify, err := targetFor(tw, ref, version, warn)
		if err != nil {
			s.logf("mcp: documentation bundle %s@%s not acquired: %v", ref, version, err)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), docsAcquireTimeout)
		defer cancel()
		if _, err := docscache.ResolveAndCache(ctx, target, ref, version, tw.root, tw.cacheDir, verify); err != nil {
			s.logf("mcp: documentation bundle %s@%s not acquired: %v", ref, version, err)
			return
		}
		s.logf("mcp: documentation bundle %s@%s acquired", ref, version)
	})
}

// docsRegistryTarget is the production resolution. The workspace's egress gate
// supplies the transport and refuses outright when the workspace is offline; the
// shared OCI constructor supplies the authenticated repository, so credentials
// and the loopback plain-HTTP decision are read from the one place that owns
// them.
//
// The verifier it returns is the same pull-time signature check a catalog entity
// or a plugin gets, run against the workspace's own per-registry trust policy
// through pullverify. Returning none would have been simpler and would have made
// a documentation bundle the one artifact class that bypasses that policy.
func docsRegistryTarget(tw *toolWorkspace, ref, version string, warn func(string)) (oras.ReadOnlyTarget, docscache.VerifyAtPull, error) {
	httpClient, err := egress.HTTPClient(tw.cfg)
	if err != nil {
		return nil, nil, err
	}
	repo, err := oci.NewRemoteRepository(ref, httpClient)
	if err != nil {
		return nil, nil, err
	}
	verify := func(ctx context.Context, manifestDigest string) error {
		_, verr := pullverify.Verify(ctx, []pullverify.Entity{{
			URI:            ref,
			ManifestDigest: manifestDigest,
			Host:           registryhost.Of(ref),
		}}, pullverify.Options{
			Config: tw.cfg,
			Target: func(pullverify.Entity) (oras.ReadOnlyTarget, string, error) {
				return repo, version, nil
			},
			Warn: warn,
		})
		return verr
	}
	return repo, verify, nil
}

// readDocsResource serves a documentation resource URI as inline content read
// from the local documentation-bundle cache. It maps the URI scheme to the
// bundle-internal page path, resolves the workspace and the bundle coordinates,
// and reads the page offline — a cache miss or an absent page is reported as a
// well-formed resource-not-found error, an integrity or read failure as an
// internal error. The read never reaches the network.
func (s *server) readDocsResource(uri string) (any, *rpcError) {
	page, ok := bundlePageForURI(uri)
	if !ok {
		return nil, resourceNotFound(uri)
	}
	tw, err := s.resolveToolWorkspace("", nil)
	if err != nil {
		return nil, &rpcError{Code: codeInternalError, Message: "resolve workspace: " + err.Error()}
	}
	ref, version := s.docsBundleCoords(tw)
	data, mimeType, rerr := docscache.ReadPage(tw.root, tw.cacheDir, ref, version, page)
	if rerr != nil {
		if errors.Is(rerr, docscache.ErrFetchNotFound()) || errors.Is(rerr, docscache.ErrPageNotFound()) {
			return nil, resourceNotFound(uri)
		}
		return nil, &rpcError{Code: codeInternalError, Message: "read documentation resource: " + rerr.Error()}
	}
	return readResourceResult{
		Contents: []resourceContents{{URI: uri, MimeType: mimeType, Text: string(data)}},
	}, nil
}

// bundlePageForURI maps a documentation resource URI to its bundle-internal
// page path. It reports ok=false for an unrecognised scheme, an empty body, or
// a body that climbs out of the tree its scheme names, so those surface as a
// clean resource-not-found rather than a malformed lookup. A body with no file
// extension gains a ".md" suffix, so both langref://entities/component and
// langref://entities/component.md resolve to the same markdown page.
//
// Containment is pathscope's question, asked in slash arithmetic about the body
// rather than about the prefixed page path. A scheme addresses one tree of the
// bundle, and a body confined to its own tree is confined to the bundle as well,
// while the reverse does not hold: "../authoring/getting-started" under the
// language-reference prefix cleans to a page inside the bundle but outside the
// tree its scheme names. So a body carrying two dots is an ordinary page name
// and resolves — "entities/foo..bar", "..config", and an interior "a/../b" that
// resolves to "b" — and only a cleaned form that climbs above the tree is
// refused.
func bundlePageForURI(uri string) (string, bool) {
	scheme, body, ok := splitScheme(uri)
	if !ok {
		return "", false
	}
	body = strings.Trim(body, "/")
	// An empty body names no page at all, which is a separate refusal from
	// containment and keeps its own reason.
	if body == "" {
		return "", false
	}
	if !pathscope.IsLocalSlash(body) {
		return "", false
	}
	var prefix string
	switch scheme {
	case schemeLangref:
		prefix = langrefPrefix
	case schemeAuthoring:
		prefix = authoringPrefix
	default:
		return "", false
	}
	return prefix + ensureMarkdownExt(body), true
}

// splitScheme splits a "scheme://body" URI into its scheme and body. It reports
// ok=false when the "://" separator is absent, so a bare path or an opaque URI
// is treated as unrecognised.
func splitScheme(uri string) (scheme, body string, ok bool) {
	i := strings.Index(uri, "://")
	if i <= 0 {
		return "", "", false
	}
	return uri[:i], uri[i+len("://"):], true
}

// ensureMarkdownExt appends ".md" to a page body whose final segment carries no
// extension, so a bare topic or reference name resolves to its markdown page.
func ensureMarkdownExt(body string) string {
	last := body
	if i := strings.LastIndex(body, "/"); i >= 0 {
		last = body[i+1:]
	}
	if strings.Contains(last, ".") {
		return body
	}
	return body + ".md"
}

// docsBundleCoords returns the OCI reference and version of the documentation
// bundle the server reads pages from. Explicit server overrides win (a test
// pins a fixture bundle); otherwise the reference defaults to the workspace's
// registry under the reserved docs namespace and the version defaults to the
// server's own build identity.
func (s *server) docsBundleCoords(tw *toolWorkspace) (ref, version string) {
	ref = s.docsBundleRef
	if ref == "" {
		ref = tw.cfg.DefaultRegistry + "/" + docsNamespace + "/" + docsBundleName
	}
	version = s.docsBundleVersion
	if version == "" {
		version = s.serverVersion
	}
	return ref, version
}
