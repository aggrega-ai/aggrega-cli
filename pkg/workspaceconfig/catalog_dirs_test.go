// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package workspaceconfig_test

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

func cfgWithCatalogDirs(dirs ...string) workspaceconfig.Config {
	c := workspaceconfig.Default()
	c.Workspace.CatalogDirs = dirs
	return c
}

// TestResolveCatalogDirs pins the single home for catalog-dir precedence: flag
// dirs prepend to config dirs (first-win), with the fallback applied only when
// neither is set. Flag dirs (which carry no declaring file) are resolved to
// absolute against the working directory here; configured dirs pass through
// unchanged because the cascade already resolved them against their declaring
// file at load (this unit test sets them directly, simulating that).
func TestResolveCatalogDirs(t *testing.T) {
	t.Parallel()
	ab := func(p string) string { a, _ := filepath.Abs(p); return a }
	cases := []struct {
		name     string
		flag     []string
		cfg      workspaceconfig.Config
		fallback string
		want     []string
	}{
		{"neither set -> fallback", nil, workspaceconfig.Default(), ".", []string{"."}},
		{"neither set -> wsRoot fallback", nil, workspaceconfig.Default(), "/ws", []string{"/ws"}},
		{"flag only (resolved to absolute)", []string{"./a"}, workspaceconfig.Default(), ".", []string{ab("./a")}},
		{"config only (passes through, already absolute at load)", nil, cfgWithCatalogDirs("/std"), ".", []string{"/std"}},
		{"flag prepends config (first-win order)", []string{"./local"}, cfgWithCatalogDirs("/std"), ".", []string{ab("./local"), "/std"}},
		{"multiple flags keep order, then config", []string{"./a", "./b"}, cfgWithCatalogDirs("/std1", "/std2"), ".", []string{ab("./a"), ab("./b"), "/std1", "/std2"}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := workspaceconfig.ResolveCatalogDirs(tc.flag, tc.cfg, tc.fallback)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ResolveCatalogDirs(%v, cfg=%v, %q) = %v; want %v", tc.flag, tc.cfg.Workspace.CatalogDirs, tc.fallback, got, tc.want)
			}
		})
	}
}
