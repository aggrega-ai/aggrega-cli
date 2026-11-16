// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"

	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
)

// layFetchedSlot writes body into the cache slot the lockfile's pin for uri at
// version addresses, as a registry fetch of the published entity leaves it: the
// content, and the manifest the registry served for it. When withManifest is false
// only the content is written — the shape a fetch interrupted between its two
// writes, or a file placed there by hand, leaves behind.
func layFetchedSlot(t *testing.T, root, uri, version string, body []byte, withManifest bool) {
	t.Helper()
	layFetchedSlotOfKind(t, root, "Component", oci.MediaTypeComponent, uri, version, body, withManifest)
}

// layFetchedSlotOfKind is layFetchedSlot for an entity of any published kind,
// cached under kind and served under mediaType.
func layFetchedSlotOfKind(t *testing.T, root, kind, mediaType, uri, version string, body []byte, withManifest bool) {
	t.Helper()
	cacheDir := resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}).Config().ResolvedArtifactCacheDir()
	if err := oci.WriteCached(root, cacheDir, kind, uri+":"+version, version, body); err != nil {
		t.Fatalf("write the slot's content: %v", err)
	}
	if !withManifest {
		return
	}
	manifest, _, err := ociManifestArtifact(mediaType, body)
	if err != nil {
		t.Fatalf("the manifest a registry serves for the content: %v", err)
	}
	if err := oci.WriteCachedManifest(root, cacheDir, kind, uri+":"+version, version, manifest); err != nil {
		t.Fatalf("write the slot's manifest: %v", err)
	}
}

// TestARemovedCatalogEntityMeetsItsCacheSlotAlikeInPullVerifyAndBuild holds the
// three commands that read a Release's dependencies — the pull, the verifying pull
// and the build — to one answer when a Component authored in the catalog directory
// is removed after the pull, for each state the cache slot at its pinned version
// can be in. One fault per fixture: the removal, and the slot the row lays.
//
//   - No slot: the entity is gone and nothing stands in for it, so each refuses it
//     as a reference nothing resolves.
//   - The content without its manifest: not a cache entry, so it stands in for
//     nothing and each refuses exactly as with no slot — the same condition, and
//     never the internal-error halt a reader serving the content while another
//     refused it would reach.
//   - The content and its manifest, as a fetch of the published entity writes them:
//     the entity resolves to that published copy, whose bytes are the ones pinned,
//     and each completes.
//
// The workspace is offline, so a miss is answered by nothing but the cache.
func TestARemovedCatalogEntityMeetsItsCacheSlotAlikeInPullVerifyAndBuild(t *testing.T) {
	wasmtest.RequireEmbeddedEntities(t)
	t.Parallel()
	rows := []struct {
		name string
		// slot lays the row's cache slot; nil lays none.
		slot     func(t *testing.T, f refusalFixture, pulled []byte)
		resolves bool
	}{
		{name: "no slot at the pinned version", slot: nil, resolves: false},
		{name: "the content without its manifest at the pinned version", slot: func(t *testing.T, f refusalFixture, pulled []byte) {
			layFetchedSlot(t, f.root, refusalComponentURI, "1.0.0", pulled, false)
		}, resolves: false},
		{name: "the content and its manifest at the pinned version", slot: func(t *testing.T, f refusalFixture, pulled []byte) {
			layFetchedSlot(t, f.root, refusalComponentURI, "1.0.0", pulled, true)
		}, resolves: true},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			f := writeRefusalFixture(t)
			pulled, err := os.ReadFile(f.componentPath)
			if err != nil {
				t.Fatalf("read the Component: %v", err)
			}
			if row.slot != nil {
				row.slot(t, f, pulled)
			}
			if err := os.Remove(f.componentPath); err != nil {
				t.Fatalf("remove the Component: %v", err)
			}

			// The pull runs last because a completing pull may rewrite the lockfile;
			// neither the verify nor the build writes it.
			var verifyOut string
			commands := []struct {
				name string
				run  func() error
			}{
				{name: "pull --verify", run: func() error {
					out, err := f.verify(t)
					verifyOut = out
					return err
				}},
				{name: "build", run: func() error {
					var out, diag bytes.Buffer
					return dispatchBuildInRoot(t, resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}),
						f.releasePath, []string{f.catalogDir}, false, &diag, &out)
				}},
				{name: "pull", run: func() error {
					var out, diag bytes.Buffer
					return pullRun(context.Background(), pullConfig{
						ws:          resolveTempWorkspace(t, tempWorkspaceOptions{Root: f.root}),
						ReleasePath: f.releasePath,
						CatalogDirs: []string{f.catalogDir},
						Out:         &out,
						DiagOut:     &diag,
					})
				}},
			}
			for _, c := range commands {
				err := c.run()
				if row.resolves {
					if err != nil {
						t.Errorf("%s refused a removed entity whose published copy the cache holds complete at the pinned version: %v", c.name, err)
					}
					continue
				}
				if errors.Is(err, resolver.ErrGuardEntityViewInconsistency()) {
					t.Errorf("%s halted with the internal catalog-view disagreement rather than refusing the removed entity: %v", c.name, err)
					continue
				}
				if !errors.Is(err, closure.ErrUnresolvableURI()) {
					t.Errorf("%s: want the unresolvable-reference condition every command reports for the removed entity; got %v", c.name, err)
					continue
				}
				var unresolved *closure.UnresolvedEntityError
				if !errors.As(err, &unresolved) || unresolved.URI != refusalComponentURI {
					t.Errorf("%s: the refusal does not carry the removed reference %q: %v", c.name, refusalComponentURI, err)
				}
			}
			if row.resolves && !strings.Contains(verifyOut, "Verify: lockfile") {
				t.Errorf("the verify returned no error but printed no verdict: %q", verifyOut)
			}
		})
	}
}
