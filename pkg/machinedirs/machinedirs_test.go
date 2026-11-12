// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package machinedirs

import (
	"path/filepath"
	"testing"
)

// frozen answers lookups from vars alone, so each row states a whole machine
// without touching the environment of the process running the test.
func frozen(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// TestGlobalConfigFileFollowsEachPlatformsConvention drives the location rule
// for the global configuration file over a frozen environment per platform.
// The Windows rows carry HOME and the XDG variables beside APPDATA, so a rule
// that read them there would answer differently; the other rows carry APPDATA,
// so a rule that read it there would too.
func TestGlobalConfigFileFollowsEachPlatformsConvention(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		platform Platform
		env      map[string]string
		want     string
		resolves bool
	}{
		{
			name:     "windows reads APPDATA and nothing else",
			platform: Windows,
			env:      map[string]string{"APPDATA": "/roaming", "HOME": "/home/u", "XDG_CONFIG_HOME": "/xdg", "USERPROFILE": "/profile"},
			want:     filepath.Join("/roaming", "aggrega", "config.yaml"),
			resolves: true,
		},
		{
			name:     "windows without APPDATA resolves nothing",
			platform: Windows,
			env:      map[string]string{"HOME": "/home/u", "XDG_CONFIG_HOME": "/xdg", "USERPROFILE": "/profile"},
		},
		{
			name:     "linux reads XDG_CONFIG_HOME first",
			platform: Platform("linux"),
			env:      map[string]string{"XDG_CONFIG_HOME": "/xdg", "HOME": "/home/u", "APPDATA": "/roaming"},
			want:     filepath.Join("/xdg", "aggrega", "config.yaml"),
			resolves: true,
		},
		{
			name:     "linux falls back to HOME's .config",
			platform: Platform("linux"),
			env:      map[string]string{"HOME": "/home/u", "APPDATA": "/roaming"},
			want:     filepath.Join("/home/u", ".config", "aggrega", "config.yaml"),
			resolves: true,
		},
		{
			name:     "macos keeps the same rule as linux",
			platform: Darwin,
			env:      map[string]string{"HOME": "/Users/u", "APPDATA": "/roaming"},
			want:     filepath.Join("/Users/u", ".config", "aggrega", "config.yaml"),
			resolves: true,
		},
		{
			name:     "linux with neither variable resolves nothing",
			platform: Platform("linux"),
			env:      map[string]string{"APPDATA": "/roaming"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := GlobalConfigFile(tc.platform, frozen(tc.env))
			if ok != tc.resolves || got != tc.want {
				t.Errorf("GlobalConfigFile(%s) = %q, %v; want %q, %v", tc.platform, got, ok, tc.want, tc.resolves)
			}
		})
	}
}

// TestCacheRootFollowsEachPlatformsConvention drives the machine-wide cache
// root over a frozen environment per platform, and the sibling root a set of
// caches kept apart takes beside it.
func TestCacheRootFollowsEachPlatformsConvention(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		platform    Platform
		env         map[string]string
		want        string
		wantSibling string
		wantErr     string
	}{
		{
			name:        "windows keeps its caches in a directory of their own under LOCALAPPDATA",
			platform:    Windows,
			env:         map[string]string{"LOCALAPPDATA": "/local", "APPDATA": "/roaming", "HOME": "/home/u", "XDG_CACHE_HOME": "/xdgcache"},
			want:        filepath.Join("/local", "aggrega", "cache"),
			wantSibling: filepath.Join("/local", "aggrega-test", "cache"),
		},
		{
			name:     "windows without LOCALAPPDATA keeps no cache root",
			platform: Windows,
			env:      map[string]string{"APPDATA": "/roaming", "HOME": "/home/u", "XDG_CACHE_HOME": "/xdgcache"},
			wantErr:  "LOCALAPPDATA is not set",
		},
		{
			name:        "linux reads XDG_CACHE_HOME first",
			platform:    Platform("linux"),
			env:         map[string]string{"XDG_CACHE_HOME": "/xdgcache", "HOME": "/home/u", "LOCALAPPDATA": "/local"},
			want:        filepath.Join("/xdgcache", "aggrega"),
			wantSibling: filepath.Join("/xdgcache", "aggrega-test"),
		},
		{
			name:        "linux falls back to HOME's .cache",
			platform:    Platform("linux"),
			env:         map[string]string{"HOME": "/home/u", "LOCALAPPDATA": "/local"},
			want:        filepath.Join("/home/u", ".cache", "aggrega"),
			wantSibling: filepath.Join("/home/u", ".cache", "aggrega-test"),
		},
		{
			name:     "linux refuses a relative XDG_CACHE_HOME",
			platform: Platform("linux"),
			env:      map[string]string{"XDG_CACHE_HOME": "relative", "HOME": "/home/u"},
			wantErr:  "XDG_CACHE_HOME is not an absolute path",
		},
		{
			name:        "macos keeps its caches under Library/Caches",
			platform:    Darwin,
			env:         map[string]string{"HOME": "/Users/u", "XDG_CACHE_HOME": "/xdgcache", "LOCALAPPDATA": "/local"},
			want:        filepath.Join("/Users/u", "Library", "Caches", "aggrega"),
			wantSibling: filepath.Join("/Users/u", "Library", "Caches", "aggrega-test"),
		},
		{
			name:     "linux with neither variable keeps no cache root",
			platform: Platform("linux"),
			env:      map[string]string{"LOCALAPPDATA": "/local"},
			wantErr:  "neither XDG_CACHE_HOME nor HOME is set",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := CacheRoot(tc.platform, frozen(tc.env))
			sibling, siblingErr := SiblingCacheRoot(tc.platform, frozen(tc.env), "-test")
			if tc.wantErr != "" {
				if err == nil || err.Error() != tc.wantErr {
					t.Fatalf("CacheRoot(%s) = %q, %v; want the error %q", tc.platform, got, err, tc.wantErr)
				}
				if siblingErr == nil || siblingErr.Error() != tc.wantErr {
					t.Fatalf("SiblingCacheRoot(%s) = %q, %v; want the error %q", tc.platform, sibling, siblingErr, tc.wantErr)
				}
				return
			}
			if err != nil || filepath.Clean(got) != tc.want {
				t.Errorf("CacheRoot(%s) = %q, %v; want %q", tc.platform, got, err, tc.want)
			}
			if siblingErr != nil || filepath.Clean(sibling) != tc.wantSibling {
				t.Errorf("SiblingCacheRoot(%s) = %q, %v; want %q", tc.platform, sibling, siblingErr, tc.wantSibling)
			}
		})
	}
}
