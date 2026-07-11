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
)

// seedDocsWorkspace builds a fixture documentation bundle, resolves it into a
// fresh workspace's local cache, and returns a server pointed at that workspace
// and bundle. The server's resource reads then serve from the warm cache.
func seedDocsWorkspace(t *testing.T) *server {
	t.Helper()
	ws := t.TempDir()
	cacheDir := workspaceconfig.Default().ResolvedArtifactCacheDir()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range []struct{ name, body string }{
		{"authoring/getting-started.md", authoringBody},
		{"language-reference/entities/component.md", langrefBody},
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

	store := memory.New()
	if _, err := oci.Push(context.Background(), store, docsTestVersion, oci.MediaTypeDocs, buf.Bytes()); err != nil {
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
