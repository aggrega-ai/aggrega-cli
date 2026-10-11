// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

// The daemon's cache status lists the documentation-bundle cache where the shared
// inventory puts it: among the classes every project on the machine shares.
//
// Not parallel: it points the session's cache roots at its own fixture through
// the package-level seam, for the reason the held-open tests beside it give.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestTheDaemonsCacheStatusListsTheDocsBundleClassAsMachineWide seeds one
// documentation bundle in a machine-wide cache root of the test's own and reads
// the daemon's cache status through the real dispatch: the class is listed with
// machine reach, carries the bundle, and is named among the machine-wide classes.
func TestTheDaemonsCacheStatusListsTheDocsBundleClassAsMachineWide(t *testing.T) {
	const ref, version = "reg.example.com/docs/aggrega-docs", "1.0.0"
	root := t.TempDir()
	store := memory.New()
	if _, err := oci.Push(context.Background(), store, version, oci.MediaTypeDocs, []byte("not read by the inventory"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if _, err := docscache.ResolveAndCache(context.Background(), store, ref, version, root, nil); err != nil {
		t.Fatalf("seed the bundle: %v", err)
	}
	wsRoot := t.TempDir()
	addressCacheRootsAt(t, cacheops.Locations{
		WorkspaceRoot:  wsRoot,
		CacheDir:       heldOpenCacheDir,
		CompileDir:     filepath.Join(t.TempDir(), "wasm-compile"),
		DescribeDir:    filepath.Join(t.TempDir(), "plugin-describe"),
		DocsBundlesDir: docscache.DocsBundlesRoot(root),
	})
	srv := newTestServer(t, wsRoot, nil, nil)

	answer := cacheAnswerOnTheWire(t, srv, "aggrega/cacheStatus", map[string]any{})

	var found bool
	for _, class := range arrayOnTheWire(t, answer, "classes") {
		if stringOnTheWire(t, class, "name") != cacheops.DocsBundleClassName {
			continue
		}
		found = true
		if reach := stringOnTheWire(t, class, "reach"); reach != string(cacheops.ReachMachine) {
			t.Errorf("the docs-bundle class reports reach %q, want %q", reach, cacheops.ReachMachine)
		}
		elements := arrayOnTheWire(t, class, "elements")
		if len(elements) != 1 || stringOnTheWire(t, elements[0], "label") != ref+"@"+version {
			t.Errorf("the docs-bundle class lists %v, want the one seeded bundle", elements)
		}
	}
	if !found {
		t.Fatalf("the daemon's cache status lists no %q class: %s", cacheops.DocsBundleClassName, answer["classes"])
	}

	var scope struct {
		MachineWideClasses []string `json:"machineWideClasses"`
	}
	raw, present := answer["scope"]
	if !present {
		t.Fatalf("the answer carries no scope key: %v", answer)
	}
	if err := json.Unmarshal(raw, &scope); err != nil {
		t.Fatalf("decode scope: %v (%s)", err, raw)
	}
	machineWide := scope.MachineWideClasses
	var named bool
	for _, name := range machineWide {
		named = named || name == cacheops.DocsBundleClassName
	}
	if !named {
		t.Errorf("the machine-wide classes %v do not name the docs-bundle cache", machineWide)
	}
}
