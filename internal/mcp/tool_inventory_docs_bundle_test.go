// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/cacheops"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestTheCacheInventoryListsTheDocsBundleClassAsMachineWide seeds one
// documentation bundle in a machine-wide cache root of the test's own and calls
// the cache-status tool: the class is listed with machine reach, carries the
// bundle, and is named among the machine-wide classes — so a caller reading reach
// before proposing a reclamation is told a clear of it reaches every project on
// the machine.
func TestTheCacheInventoryListsTheDocsBundleClassAsMachineWide(t *testing.T) {
	t.Parallel()
	const ref, version = "reg.example.com/docs/aggrega-docs", "1.0.0"
	root := t.TempDir()
	store := memory.New()
	if _, err := oci.Push(context.Background(), store, version, oci.MediaTypeDocs, []byte("not read by the inventory"), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if _, err := docscache.ResolveAndCache(context.Background(), store, ref, version, root, nil); err != nil {
		t.Fatalf("seed the bundle: %v", err)
	}
	machine := t.TempDir()
	s := &server{
		out:      io.Discard,
		logw:     io.Discard,
		startDir: t.TempDir(),
		cacheLocations: func(workspaceRoot, cacheDir string) cacheops.Locations {
			return cacheops.Locations{
				WorkspaceRoot:  workspaceRoot,
				CacheDir:       cacheDir,
				CompileDir:     filepath.Join(machine, "wasm-compile"),
				DescribeDir:    filepath.Join(machine, "plugin-describe"),
				DocsBundlesDir: docscache.DocsBundlesRoot(root),
			}
		},
	}

	res := callTool(t, s, "cache-status", map[string]any{})
	if res.IsError {
		t.Fatalf("cache-status reported an error: %s", res.Content[0].Text)
	}
	var payload struct {
		Classes []struct {
			Name     string `json:"name"`
			Reach    string `json:"reach"`
			Elements []struct {
				Label string `json:"label"`
			} `json:"elements"`
		} `json:"classes"`
		MachineWideClasses []string `json:"machine_wide_classes"`
	}
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("decode the payload: %v (%s)", err, res.StructuredContent)
	}

	var found bool
	for _, class := range payload.Classes {
		if class.Name != cacheops.DocsBundleClassName {
			continue
		}
		found = true
		if class.Reach != string(cacheops.ReachMachine) {
			t.Errorf("the docs-bundle class reports reach %q, want %q", class.Reach, cacheops.ReachMachine)
		}
		if len(class.Elements) != 1 || class.Elements[0].Label != ref+"@"+version {
			t.Errorf("the docs-bundle class lists %+v, want the one seeded bundle", class.Elements)
		}
	}
	if !found {
		t.Fatalf("the cache inventory lists no %q class: %s", cacheops.DocsBundleClassName, res.StructuredContent)
	}
	var named bool
	for _, name := range payload.MachineWideClasses {
		named = named || name == cacheops.DocsBundleClassName
	}
	if !named {
		t.Errorf("machine_wide_classes %v does not name the docs-bundle cache", payload.MachineWideClasses)
	}
}
