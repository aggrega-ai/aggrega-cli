// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// unpinnedEntityRegistry is an in-process registry holding one entity, the
// Component the stale-slot fixture's Aggregate names, at version 1.0.0. It
// answers a fetch that names no version with that one, as a registry answers one
// with the highest version it holds, so a pull with no lockfile reaches it.
type unpinnedEntityRegistry struct{}

func (unpinnedEntityRegistry) Pull(context.Context, string, string) (*oci.PullResult, error) {
	return nil, oci.ErrFetchNotFound()
}

func (unpinnedEntityRegistry) PullEntity(_ context.Context, identity, kind, _ string) (*oci.PullResult, string, error) {
	if identity != staleSlotComponentURI || kind != "Component" {
		return nil, "", oci.ErrFetchNotFound()
	}
	body := []byte(staleSlotPinnedBody)
	manifest, manifestDigest, err := ociManifestArtifact(oci.MediaTypeComponent, body)
	if err != nil {
		return nil, "", err
	}
	return &oci.PullResult{MediaType: oci.MediaTypeComponent, Bytes: body, Digest: manifestDigest, ManifestBytes: manifest}, "1.0.0", nil
}

// TestAnEntityCacheThatCannotBeWrittenIsSaidAsWhatWasBeingDone drives a pull whose
// only source for a Component is a registry, and whose entity cache cannot take
// the copy the registry served: a regular file stands where the copy's directory
// goes. Something in the way, rather than a permission, is what makes the write
// fail, so the test holds for the superuser and on every platform.
//
// What the reader is shown is one line saying which entity was being resolved,
// what was being done, with which path, and the operating system's reason:
//
//	resolve entity "core/datastore/mariadb": create the cache directory <slot>: <reason>
//
// It is asserted by equality, so a label naming a package, a second "resolve", the
// name of the call that failed, or the path said twice each fail it. The reason is
// read off the error the system returned, because its words are the platform's.
func TestAnEntityCacheThatCannotBeWrittenIsSaidAsWhatWasBeingDone(t *testing.T) {
	t.Parallel()
	f := writeStaleSlotFixture(t, staleSlotOptions{
		mappingKey:  staleSlotComponentURI,
		config:      "default_registry: hub.aggrega.ai\n" + fakeRegistryTrust,
		leaveStderr: true,
	})
	// The registry is the only source: no catalog directory holds the Component.
	if err := os.Remove(f.componentPath); err != nil {
		t.Fatalf("remove the catalog directory's copy: %v", err)
	}
	ws := resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root})
	cacheDir := ws.Config().ResolvedArtifactCacheDir()
	// A regular file stands where the copy's directory goes. The Aggregate and the
	// Manifest write the reference bare, and the copy is keyed by it at the version
	// it resolved to — the slot every reader of its pin addresses.
	slot := mustCachePath(f.root, cacheDir, staleSlotEntry, "1.0.0")
	writeFixture(t, slot, "in the way\n")

	var diag, out bytes.Buffer
	err := pullRun(context.Background(), pullConfig{
		ws:          ws,
		ReleasePath: f.releasePath,
		CatalogDirs: f.catalogDirs,
		DiagOut:     &diag,
		Out:         &out,
		puller:      unpinnedEntityRegistry{},
	})
	if err == nil {
		t.Fatalf("the pull completed although its entity cache could not take the fetched copy:\n%s%s", diag.String(), out.String())
	}
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("the failure no longer carries the operating system's path error: %v", err)
	}
	if pathErr.Path != slot {
		t.Fatalf("the write failed at %s, which is not the slot the fixture blocked (%s): %v", pathErr.Path, slot, err)
	}

	printed := formatCompilerError(err)
	want := fmt.Sprintf("resolve entity %q: create the cache directory %s: %s", staleSlotComponentURI, pathErr.Path, pathErr.Err.Error())
	if first, _, _ := strings.Cut(printed, "\n"); first != want {
		t.Errorf("the pull printed\n  %s\nwant\n  %s", first, want)
	}
	for _, label := range []string{"resolve: ", "catalog: ", "oci: ", "tier-3"} {
		if strings.Contains(printed, label) {
			t.Errorf("the diagnostic carries %q, a label the reader cannot act on:\n%s", label, printed)
		}
	}
}
