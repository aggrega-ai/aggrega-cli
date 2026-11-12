// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

//go:build integration

package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/lockfile"
)

// requestLog records the path of every request an in-process registry serves, so a
// test can say what a command asked a registry for.
type requestLog struct {
	mu    sync.Mutex
	paths []string
}

func (l *requestLog) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.mu.Lock()
		l.paths = append(l.paths, r.Method+" "+r.URL.Path)
		l.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

// since returns the requests recorded after the first mark requests.
func (l *requestLog) since(mark int) []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.paths[mark:]...)
}

func (l *requestLog) len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.paths)
}

// TestIntegration_CatalogAuthoredTransformAttachmentNeverReachesARegistry drives a
// governance plugin authored in a catalog directory, with its .wasm beside it and
// attached at a transform role, against a registry that publishes the same identity
// with different bytes.
//
// An artifact of an entity authored in a catalog directory is read from the machine
// and never fetched, so the pull asks the registry nothing about that identity and
// pins the bytes beside the entity. The registry's different bytes make each half
// observable: a pull that fetched would record their digest rather than the local
// one, and the build — which reads the module beside the entity first and holds it
// to the pin — would then refuse the local bytes as not matching. The build here
// passes its drift check and runs the local guest.
func TestIntegration_CatalogAuthoredTransformAttachmentNeverReachesARegistry(t *testing.T) {
	local := wasmtest.EnsureFixtureBuilt(t, "mutator-proof-go.wasm")
	published := wasmtest.EnsureFixtureBuilt(t, "governance-multirole-go.wasm")
	guest, err := os.ReadFile(local)
	if err != nil {
		t.Fatalf("read the local guest: %v", err)
	}

	var log requestLog
	host := startRegistry(t, log.wrap)
	publishedDigest := publishSocketPluginEntity(t, host, catalogTransformArtifact, "1.0.0", published, "governance", "mutate")
	if publishedDigest == "" {
		t.Fatal("the registry-side publish reported no digest")
	}
	mark := log.len()
	if mark == 0 {
		t.Fatal("the request log recorded nothing for the publish, so it cannot show what the pull asks for")
	}

	root, releasePath, _ := catalogTransformAttachmentWorkspace(t, guest,
		"default_registry: \""+host+"\"\noffline: false\n")
	t.Chdir(root)
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root})

	if err := pullRun(context.Background(), pullConfig{ws: ws, ReleasePath: releasePath}); err != nil {
		t.Fatalf("pull: %v", err)
	}
	// The repository the plugin is published under is the one a fetch of its
	// artifact reads; nothing about the attachment may be asked of it.
	pluginRepository := "/v2/plugin/" + catalogTransformArtifact + "/"
	for _, req := range log.since(mark) {
		if strings.Contains(req, pluginRepository) {
			t.Errorf("the pull asked the registry for the catalog-authored attachment's plugin: %s", req)
		}
	}
	lf, _, err := lockfile.LoadLockfile(lockfile.PathFor(releasePath))
	if err != nil {
		t.Fatalf("load the written lockfile: %v", err)
	}
	requireCatalogTransformPin(t, lf, guest)

	if err := dispatchBuild(context.Background(), ws, releasePath, nil, false, io.Discard, io.Discard); err != nil {
		t.Fatalf("the build over the lockfile the pull wrote: %v", err)
	}
	if found, tree := renderedTreeHolds(t, filepath.Join(root, "dist"), catalogTransformProofLabel); !found {
		t.Errorf("the rendered output does not carry the label %q the local guest injects:\n%s", catalogTransformProofLabel, tree)
	}
}
