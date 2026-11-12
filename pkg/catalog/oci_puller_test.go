// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package catalog

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
)

// TestSelectPullVersion pins the discovery step the three pull entrypoints
// share: an empty-floorVersion pull lists tags and selects the highest
// conformant version, surfacing a NotFound-wrapped error when none conforms.
// The error rows are the behavior delta — a "v"-prefixed or four-part
// "1.2.3.4" registry now yields "no version" where the previous dotted-numeric
// selector accepted "1.2.3.4" and rejected nothing four-part about it. Both the
// lint publish gate and this pull path now route through the same strict
// selection, so they can no longer disagree about what resolves.
func TestSelectPullVersion(t *testing.T) {
	t.Parallel()
	const ref = "example.test/component/widget"

	cases := []struct {
		name      string
		tags      []string
		want      string
		wantErr   bool
		errIsNotF bool
	}{
		{name: "highest stable selected", tags: []string{"1.0.0", "1.1.0", "0.9.0"}, want: "1.1.0"},
		{name: "pre-release skipped on the pull path", tags: []string{"1.0.0", "1.1.0-rc.1"}, want: "1.0.0"},
		{name: "v-prefix-only yields no-version error", tags: []string{"v1.0.0", "v2.0.0"}, wantErr: true, errIsNotF: true},
		{name: "four-part-only yields no-version error", tags: []string{"1.2.3.4"}, wantErr: true, errIsNotF: true},
		{name: "non-semver-only yields no-version error", tags: []string{"latest", "edge"}, wantErr: true, errIsNotF: true},
		{name: "empty tag list yields no-version error", tags: nil, wantErr: true, errIsNotF: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectPullVersion(ref, tc.tags)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("selectPullVersion(%v): want error; got version %q", tc.tags, got)
				}
				if tc.errIsNotF && !errors.Is(err, oci.ErrFetchNotFound()) {
					t.Errorf("selectPullVersion(%v): error %v; want wrapped ErrFetchNotFound", tc.tags, err)
				}
				if got != "" {
					t.Errorf("selectPullVersion(%v): want empty version on error; got %q", tc.tags, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("selectPullVersion(%v): unexpected error %v", tc.tags, err)
			}
			if got != tc.want {
				t.Errorf("selectPullVersion(%v): got %q; want %q", tc.tags, got, tc.want)
			}
		})
	}
}

// TestEntityReference_PluginGuard pins the Plugin reference guard: a bare
// plugin entity identity binds to <registry>/plugin/<socket>/<name>, while an
// already-qualified reference (a leading registry-host-shaped segment — a DNS
// host or a host:port) is refused with ErrQualifiedPluginReference rather than
// double-prefixed into an unfetchable location.
func TestEntityReference_PluginGuard(t *testing.T) {
	t.Parallel()
	const registry = "hub.aggrega.ai"

	t.Run("bare identity binds to the wasm location", func(t *testing.T) {
		got, err := entityReference(registry, "Plugin", "pipeline/patch-credential")
		if err != nil {
			t.Fatalf("bare plugin identity should bind cleanly; got error %v", err)
		}
		const want = registry + "/plugin/pipeline/patch-credential"
		if got != want {
			t.Errorf("entityReference = %q; want %q", got, want)
		}
	})

	for _, tc := range []struct {
		name     string
		identity string
	}{
		{"DNS-host-qualified", "host.tld/plugin/pipeline/patch-credential"},
		{"host:port-qualified", "127.0.0.1:5000/plugin/pipeline/patch-credential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := entityReference(registry, "Plugin", tc.identity)
			if !errors.Is(err, ErrQualifiedPluginReference()) {
				t.Fatalf("entityReference(%q) error = %v; want errors.Is(ErrQualifiedPluginReference)", tc.identity, err)
			}
		})
	}
}

// TestEntityReference_RecipeScopeGuard pins the recipe reference guard: a
// provider-scoped identity binds to <registry>/recipe/<provider>/<name>, while an
// unscoped identity (no "/") or one with an empty leading provider ("/name") is
// refused with ErrUnscopedRecipeReference rather than bound to a double-slash
// location that cannot resolve. The guard covers both Recipe and FeatureRecipe.
func TestEntityReference_RecipeScopeGuard(t *testing.T) {
	t.Parallel()
	const registry = "hub.aggrega.ai"

	for _, kind := range []string{"Recipe", "FeatureRecipe"} {
		kind := kind
		t.Run(kind+" provider-scoped binds", func(t *testing.T) {
			got, err := entityReference(registry, kind, "helm/adminer")
			if err != nil {
				t.Fatalf("provider-scoped %s should bind cleanly; got error %v", kind, err)
			}
			want := registry + "/" + OCILocation(kind, "helm", "adminer")
			if got != want {
				t.Errorf("entityReference = %q; want %q", got, want)
			}
			if strings.Contains(strings.TrimPrefix(got, registry+"/"), "//") {
				t.Errorf("bound location must not contain a double slash: %q", got)
			}
		})

		for _, tc := range []struct {
			name     string
			identity string
		}{
			{"unscoped no slash", "adminer-helm"},
			{"empty leading provider", "/adminer-helm"},
		} {
			t.Run(kind+" "+tc.name, func(t *testing.T) {
				_, err := entityReference(registry, kind, tc.identity)
				if !errors.Is(err, ErrUnscopedRecipeReference()) {
					t.Fatalf("entityReference(%s, %q) error = %v; want errors.Is(ErrUnscopedRecipeReference)", kind, tc.identity, err)
				}
			})
		}
	}
}
