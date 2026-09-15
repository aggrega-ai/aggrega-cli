// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//go:build integration

// The documentation-resource surface driven against a REAL HTTP OCI registry.
//
// Build-tagged `integration` so the fast unit run excludes it and the test-only
// go-containerregistry dependency stays out of the production binary's import
// graph — the same boundary the command layer's registry round-trip holds.
//
// What it adds over the untagged acquisition tests beside it is the one thing
// they substitute: the registry. Here the server resolves its own target through
// the egress gate and the shared OCI repository constructor, over real HTTP on
// loopback, from a reference derived from the workspace's own configured
// registry — so the path under test is the whole of the production one.
package mcp

import (
	"context"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"

	"github.com/aggrega-ai/aggrega-cli/internal/docsbundle"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestDocsResourceServedFromARealRegistry stands up an in-process OCI registry,
// publishes a documentation bundle to it, and drives a cold MCP session through
// the run's own acquisition and a resource read.
func TestDocsResourceServedFromARealRegistry(t *testing.T) {
	ctx := context.Background()

	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	// The reference the server will derive on its own: <registry>/docs/aggrega-docs.
	ref := host + "/" + docsbundle.Namespace + "/" + docsbundle.Name
	const version = "9.9.9-fixture"

	repo, err := oci.NewRemoteRepository(ref, srv.Client())
	if err != nil {
		t.Fatalf("NewRemoteRepository: %v", err)
	}
	if _, err := oci.Push(ctx, repo, version, oci.MediaTypeDocs, docsFixtureBundle(t), nil); err != nil {
		t.Fatalf("publish documentation bundle: %v", err)
	}

	// A workspace whose configured registry is the one just published to, and
	// whose cache is empty. Nothing pins the bundle reference on the server, so
	// docsBundleCoords derives it from this configuration.
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".aggrega-config.yaml"),
		[]byte("default_registry: "+host+"\nworkspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}

	var logged strings.Builder
	s := &server{
		out:               io.Discard,
		logw:              &logged,
		serverVersion:     version,
		startDir:          ws,
		docsBundleVersion: version,
	}

	if _, rerr := readResource(t, s, "langref://entities/component"); rerr == nil {
		t.Fatal("the workspace must start cold")
	}

	if resps := runServer(t, s); len(resps) != 0 {
		t.Fatalf("a run carrying no messages answered %d of them", len(resps))
	}
	if !strings.Contains(logged.String(), "acquired") {
		t.Fatalf("the run did not acquire the bundle; log was: %s", logged.String())
	}

	rr, rerr := readResource(t, s, "langref://entities/component")
	if rerr != nil {
		t.Fatalf("resources/read: %v", rerr)
	}
	if len(rr.Contents) != 1 {
		t.Fatalf("got %d content blocks, want 1", len(rr.Contents))
	}
	if got := rr.Contents[0].Text; got != langrefBody {
		t.Errorf("served page = %q; want %q", got, langrefBody)
	}
	if got := rr.Contents[0].MimeType; got != "text/markdown" {
		t.Errorf("media type = %q; want text/markdown", got)
	}
	t.Logf("served %s (%s) from %s@%s:\n%s",
		rr.Contents[0].URI, rr.Contents[0].MimeType, ref, version, rr.Contents[0].Text)
}

// TestDocsAcquisitionRefusedWhenTheWorkspaceIsOffline pins the egress gate on
// this path: an offline workspace never dials, and the refusal is the gate's own
// rather than a transport error from deep inside a connection attempt.
func TestDocsAcquisitionRefusedWhenTheWorkspaceIsOffline(t *testing.T) {
	srv := httptest.NewServer(registry.New())
	t.Cleanup(srv.Close)
	host := strings.TrimPrefix(srv.URL, "http://")

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, ".aggrega-config.yaml"),
		[]byte("default_registry: "+host+"\noffline: true\nworkspace: {}\n"), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}

	var logged strings.Builder
	s := &server{out: io.Discard, logw: &logged, serverVersion: "9.9.9-fixture", startDir: ws}
	runServer(t, s)
	if !strings.Contains(logged.String(), "offline") {
		t.Errorf("an offline workspace must report the egress refusal; log was: %s", logged.String())
	}
}
