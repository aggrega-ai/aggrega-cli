// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// colocatedPluginYAML authors a governance Plugin entity named name.
func colocatedPluginYAML(name string) string {
	return "apiVersion: aggrega.ai/v1alpha1\nkind: Plugin\nmetadata:\n  name: " + name +
		"\n  version: \"1.0.0\"\n  pluginType: governance\nspec:\n  abiVersion: v1alpha1\n  exports:\n    - name: transform_annotations\n"
}

// TestTheColocatedMapCarriesTheRootTheEntitysCatalogDirectoryPicks holds both
// producers of the co-located map — the lazy view and the whole-catalog load — to
// the root the catalog directory that placed each Plugin entity picks: the
// workspace root for the conventional directory inside the workspace, and the
// directory itself for one listed outside it. It then hands the lazy view's map
// to the cascade, so the root is proved where it is used: inside the listed
// directory a .wasm linking elsewhere in it is read, and one linking out of it is
// refused.
func TestTheColocatedMapCarriesTheRootTheEntitysCatalogDirectoryPicks(t *testing.T) {
	ws, outside := t.TempDir(), t.TempDir()
	conventional := filepath.Join(ws, "catalog")
	listed := filepath.Join(outside, "listed")
	writeCatalogFile(t, conventional, filepath.Join("plugin", "governance", "inside", "1.0.0", "plugin.yaml"), colocatedPluginYAML("inside"))
	writeCatalogFile(t, listed, filepath.Join("plugin", "governance", "within", "1.0.0", "plugin.yaml"), colocatedPluginYAML("within"))
	writeCatalogFile(t, listed, filepath.Join("plugin", "governance", "beyond", "1.0.0", "plugin.yaml"), colocatedPluginYAML("beyond"))
	if err := os.WriteFile(filepath.Join(listed, "guest.wasm"), []byte("listed-guest"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(outside, "guest.wasm"), []byte("outside-guest"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	for at, target := range map[string]string{
		filepath.Join(listed, "plugin", "governance", "within", "1.0.0", pluginpath.PluginLeaf("governance/within")): filepath.Join(listed, "guest.wasm"),
		filepath.Join(listed, "plugin", "governance", "beyond", "1.0.0", pluginpath.PluginLeaf("governance/beyond")): filepath.Join(outside, "guest.wasm"),
	} {
		if err := os.Symlink(target, at); err != nil {
			t.Skipf("symbolic links are not available here: %v", err)
		}
	}
	named := []string{listed}
	stack := []string{listed, conventional}

	want := map[string]pluginpath.ColocatedDir{
		"governance/inside": {Dir: filepath.Join(conventional, "plugin", "governance", "inside", "1.0.0"), Root: ws},
		"governance/within": {Dir: filepath.Join(listed, "plugin", "governance", "within", "1.0.0"), Root: listed},
		"governance/beyond": {Dir: filepath.Join(listed, "plugin", "governance", "beyond", "1.0.0"), Root: listed},
	}
	lazy := NewLazyLookup(ws, "", stack, nil, named...).PluginColocatedDirs()
	eager, err := LoadAllWithNamedDirs(ws, "", named, stack...)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for producer, got := range map[string]map[string]pluginpath.ColocatedDir{"lazy view": lazy, "whole-catalog load": eager.PluginColocatedDirs()} {
		if len(got) != len(want) {
			t.Errorf("%s: %d entries %v; want %d", producer, len(got), got, len(want))
		}
		for ref, w := range want {
			if got[ref] != w {
				t.Errorf("%s: %s = %+v; want %+v", producer, ref, got[ref], w)
			}
		}
	}

	r := pluginpath.NewResolver(pluginpath.ResolutionContext{ColocatedDirs: lazy}, pluginpath.PinPostureNone, pluginpath.PinMaps{})
	if got, err := r.Resolve("governance/within"); err != nil || string(got.Bytes) != "listed-guest" {
		t.Errorf("a .wasm linking elsewhere inside the listed directory must be read; got %q, %v", got.Bytes, err)
	}
	if _, err := r.Resolve("governance/beyond"); !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
		t.Errorf("a .wasm linking out of the listed directory must be refused with the read hold; got %v", err)
	}
}
