// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

const (
	docsTestRef     = "reg.example.com/docs/aggrega-docs"
	docsTestVersion = "1.0.0"
	langrefBody     = "# Component\n\nA Component is a unit of compute.\n"
	authoringBody   = "# Getting started\n\nDeclare an Aggregate.\n"
	twoDotBody      = "# Draft\n\nA page whose own name carries two dots.\n"

	// twoDotPage is a bundle page whose file name carries two dots in the
	// middle, and twoDotURI addresses it. An ordinary page name may contain two
	// dots; only a name that climbs out of the tree it addresses is refused, and
	// this fixture is what makes that distinction observable end to end.
	twoDotPage = "language-reference/entities/component..draft.md"
	twoDotURI  = "langref://entities/component..draft.md"
)

// docsFixtureBundle builds the gzipped-tar documentation bundle every
// documentation fixture in this package uses. One builder, so a warm-cache
// fixture and a cold-workspace one can never disagree about what a bundle
// contains — which is what lets a test assert the same page bytes on both.
func docsFixtureBundle(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct{ name, body string }{
		{"authoring/getting-started.md", authoringBody},
		{"language-reference/entities/component.md", langrefBody},
		{twoDotPage, twoDotBody},
	} {
		body := []byte(e.body)
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatalf("tar header: %v", err)
		}
		if _, err := tw.Write(body); err != nil {
			t.Fatalf("tar write: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("tar close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

// seedDocsWorkspace builds a fixture documentation bundle, resolves it into a
// fresh workspace's local cache, and returns a server pointed at that workspace
// and bundle. The server's resource reads then serve from the warm cache.
func seedDocsWorkspace(t *testing.T) *server {
	t.Helper()
	ws := t.TempDir()
	cacheDir := workspaceconfig.Default().ResolvedArtifactCacheDir()

	store := memory.New()
	if _, err := oci.Push(context.Background(), store, docsTestVersion, oci.MediaTypeDocs, docsFixtureBundle(t), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if _, err := docscache.ResolveAndCache(context.Background(), store, docsTestRef, docsTestVersion, ws, cacheDir, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}

	return &server{
		out:               io.Discard,
		logw:              io.Discard,
		serverVersion:     docsTestVersion,
		startDir:          ws,
		docsBundleRef:     docsTestRef,
		docsBundleVersion: docsTestVersion,
	}
}

// readResource invokes resources/read for uri and returns the parsed result.
func readResource(t *testing.T, s *server, uri string) (readResourceResult, *rpcError) {
	t.Helper()
	params, err := json.Marshal(readResourceParams{URI: uri})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}
	res, rerr := s.handleResourcesRead(params)
	if rerr != nil {
		return readResourceResult{}, rerr
	}
	rr, ok := res.(readResourceResult)
	if !ok {
		t.Fatalf("resources/read returned %T, want readResourceResult", res)
	}
	return rr, nil
}

// TestServeLangrefInline proves langref:// serves a language-reference page as
// inline content from the warm cache with the markdown media type — with no
// network puller anywhere on the read path.
func TestServeLangrefInline(t *testing.T) {
	t.Parallel()
	s := seedDocsWorkspace(t)

	// Both the extension-less and the explicit-.md form resolve to the page.
	for _, uri := range []string{"langref://entities/component", "langref://entities/component.md"} {
		rr, rerr := readResource(t, s, uri)
		if rerr != nil {
			t.Fatalf("resources/read %s: %+v", uri, rerr)
		}
		if len(rr.Contents) != 1 {
			t.Fatalf("%s: got %d contents, want 1", uri, len(rr.Contents))
		}
		c := rr.Contents[0]
		if c.URI != uri {
			t.Errorf("%s: content URI = %q", uri, c.URI)
		}
		if c.MimeType != "text/markdown" {
			t.Errorf("%s: mimeType = %q, want text/markdown", uri, c.MimeType)
		}
		if c.Text != langrefBody {
			t.Errorf("%s: text = %q, want %q", uri, c.Text, langrefBody)
		}
	}
}

// TestServeAuthoringInline proves authoring:// serves an authoring topic inline
// from the warm cache.
func TestServeAuthoringInline(t *testing.T) {
	t.Parallel()
	s := seedDocsWorkspace(t)
	rr, rerr := readResource(t, s, "authoring://getting-started")
	if rerr != nil {
		t.Fatalf("resources/read: %+v", rerr)
	}
	if len(rr.Contents) != 1 || rr.Contents[0].Text != authoringBody {
		t.Fatalf("authoring content = %+v, want body %q", rr.Contents, authoringBody)
	}
	if rr.Contents[0].MimeType != "text/markdown" {
		t.Errorf("mimeType = %q, want text/markdown", rr.Contents[0].MimeType)
	}
}

// TestServeUnknownPageIsResourceNotFound proves a page absent from the bundle,
// an unrecognised scheme, and a traversal attempt all surface as a well-formed
// resource-not-found error rather than a fetch or a crash.
func TestServeUnknownPageIsResourceNotFound(t *testing.T) {
	t.Parallel()
	s := seedDocsWorkspace(t)
	for _, uri := range []string{
		"langref://entities/does-not-exist",
		"authoring://nope",
		"file:///etc/passwd",
		"langref://../../escape",
		"langref://",
	} {
		_, rerr := readResource(t, s, uri)
		if rerr == nil || rerr.Code != codeResourceNotFound {
			t.Errorf("resources/read %s error = %+v, want code %d", uri, rerr, codeResourceNotFound)
		}
	}
}

// TestServeMissingURIIsInvalidParams proves an empty uri is an invalid-params
// error, distinct from a resource-not-found.
func TestServeMissingURIIsInvalidParams(t *testing.T) {
	t.Parallel()
	s := seedDocsWorkspace(t)
	_, rerr := readResource(t, s, "")
	if rerr == nil || rerr.Code != codeInvalidParams {
		t.Fatalf("empty uri error = %+v, want code %d", rerr, codeInvalidParams)
	}
}

// TestServeColdCacheIsResourceNotFound proves a langref read against a
// workspace with no cached bundle reports resource-not-found (a loud cache
// miss) rather than reaching out to fetch it — the read path is offline-only.
func TestServeColdCacheIsResourceNotFound(t *testing.T) {
	t.Parallel()
	s := &server{
		out:               io.Discard,
		logw:              io.Discard,
		serverVersion:     docsTestVersion,
		startDir:          t.TempDir(),
		docsBundleRef:     docsTestRef,
		docsBundleVersion: docsTestVersion,
	}
	_, rerr := readResource(t, s, "langref://entities/component")
	if rerr == nil || rerr.Code != codeResourceNotFound {
		t.Fatalf("cold-cache read error = %+v, want code %d", rerr, codeResourceNotFound)
	}
}

// TestServeTwoDotPageNameResolves proves a page whose own file name carries two
// dots is reachable: the URI resolves and the page's bytes come back. The two
// dots sit inside a segment, so the name stays inside the tree its scheme
// addresses and the containment question answers yes.
func TestServeTwoDotPageNameResolves(t *testing.T) {
	t.Parallel()
	s := seedDocsWorkspace(t)
	rr, rerr := readResource(t, s, twoDotURI)
	if rerr != nil {
		t.Fatalf("resources/read %s: %+v", twoDotURI, rerr)
	}
	if len(rr.Contents) != 1 || rr.Contents[0].Text != twoDotBody {
		t.Fatalf("%s content = %+v, want body %q", twoDotURI, rr.Contents, twoDotBody)
	}
	if rr.Contents[0].MimeType != "text/markdown" {
		t.Errorf("%s: mimeType = %q, want text/markdown", twoDotURI, rr.Contents[0].MimeType)
	}
}

// TestServeInteriorClimbResolves proves a URI body that climbs and comes back
// down inside its own tree serves the page it names. The climb resolves before
// the bundle lookup, so "entities/../entities/component" is the same page as
// "entities/component" rather than a refusal.
func TestServeInteriorClimbResolves(t *testing.T) {
	t.Parallel()
	s := seedDocsWorkspace(t)
	const uri = "langref://entities/../entities/component"
	rr, rerr := readResource(t, s, uri)
	if rerr != nil {
		t.Fatalf("resources/read %s: %+v", uri, rerr)
	}
	if len(rr.Contents) != 1 || rr.Contents[0].Text != langrefBody {
		t.Fatalf("%s content = %+v, want body %q", uri, rr.Contents, langrefBody)
	}
}

// TestServeClimbOutOfSchemeTreeIsResourceNotFound proves the refusal fires on a
// body that climbs out of the tree its scheme addresses — including one that
// climbs into the OTHER scheme's tree and would otherwise serve a page that
// exists in the bundle. Each scheme addresses its own tree, so the read is
// refused rather than answered from the neighbouring one, and the refusal is
// asserted rather than inferred from an absent file.
func TestServeClimbOutOfSchemeTreeIsResourceNotFound(t *testing.T) {
	t.Parallel()
	s := seedDocsWorkspace(t)
	for _, uri := range []string{
		// Both of these name a page that IS in the fixture bundle.
		"langref://../authoring/getting-started",
		"authoring://../language-reference/entities/component",
		// A climb clean out of the bundle root as well.
		"langref://../../etc/passwd",
		"authoring://..",
	} {
		_, rerr := readResource(t, s, uri)
		if rerr == nil || rerr.Code != codeResourceNotFound {
			t.Errorf("resources/read %s error = %+v, want code %d", uri, rerr, codeResourceNotFound)
		}
	}
}

// TestBundlePageForURIRefusals proves the two refusals that are not containment
// still fire on their own terms: a body that names nothing, and a scheme the
// server does not serve.
func TestBundlePageForURIRefusals(t *testing.T) {
	t.Parallel()
	for _, uri := range []string{
		// An empty body, spelled three ways.
		"langref://",
		"authoring://",
		"langref://///",
		// Schemes the server does not serve, and a URI with no scheme at all.
		"bogus://entities/component",
		"file:///etc/passwd",
		"entities/component",
		"://entities/component",
	} {
		if page, ok := bundlePageForURI(uri); ok {
			t.Errorf("bundlePageForURI(%q) = %q, true; want refusal", uri, page)
		}
	}
}

// TestBundlePageForURIPagePaths pins the page path each accepted URI derives, so
// the derivation is readable at one glance and a change to it is visible.
func TestBundlePageForURIPagePaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ uri, want string }{
		{"langref://entities/component", "language-reference/entities/component.md"},
		{"langref://entities/component.md", "language-reference/entities/component.md"},
		{"authoring://getting-started", "authoring/getting-started.md"},
		{twoDotURI, twoDotPage},
		// A final segment carrying a dot is taken to carry its own extension, so
		// no ".md" is appended to it.
		{"langref://entities/foo..bar", "language-reference/entities/foo..bar"},
		{"langref://..config", "language-reference/..config"},
	} {
		got, ok := bundlePageForURI(tc.uri)
		if !ok {
			t.Errorf("bundlePageForURI(%q) refused; want %q", tc.uri, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("bundlePageForURI(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}
