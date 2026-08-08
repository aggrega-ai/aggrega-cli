// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"errors"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
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
// a body that would escape the bundle root, so those surface as a clean
// resource-not-found rather than a malformed lookup. A body with no file
// extension gains a ".md" suffix, so both langref://entities/component and
// langref://entities/component.md resolve to the same markdown page.
func bundlePageForURI(uri string) (string, bool) {
	scheme, body, ok := splitScheme(uri)
	if !ok {
		return "", false
	}
	body = strings.Trim(body, "/")
	if body == "" || strings.Contains(body, "..") {
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
