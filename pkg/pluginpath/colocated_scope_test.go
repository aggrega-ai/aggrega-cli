// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginpath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/readscope"
)

// TestTheColocatedTierReadsTheArtifactOnlyFromInsideItsRoot drives the cascade's
// co-located tier over an artifact beside its entity in each shape a link can
// give it, one fault per row beside controls.
//
// Every row seeds the content cache with different bytes under the same
// reference, so a row that refuses proves the refusal stopped the cascade rather
// than missed and let the next tier answer: a miss would return the cached bytes.
func TestTheColocatedTierReadsTheArtifactOnlyFromInsideItsRoot(t *testing.T) {
	const ref = "governance/probe"
	leaf := PluginLeaf(ref)
	cases := []struct {
		name string
		// layout lays out the files and returns where the tier reads.
		layout func(t *testing.T, ws, outside string) ColocatedDir
		// want is the bytes the cascade must return; empty means it must refuse.
		want string
		// tier is the tier the bytes must come from.
		tier Tier
		// link is the link the refusal must name, relative to ws.
		link string
	}{
		{
			name: "control: a plain file beside the entity is read",
			layout: func(t *testing.T, ws, outside string) ColocatedDir {
				put(t, filepath.Join(ws, "catalog", "p", leaf), "beside")
				return ColocatedDir{Dir: filepath.Join(ws, "catalog", "p"), Root: ws}
			},
			want: "beside", tier: TierColocated,
		},
		{
			name: "control: a link to a guest elsewhere inside the root is read",
			layout: func(t *testing.T, ws, outside string) ColocatedDir {
				put(t, filepath.Join(ws, "guests", "g.wasm"), "inside")
				link(t, filepath.Join(ws, "guests", "g.wasm"), filepath.Join(ws, "catalog", "p", leaf))
				return ColocatedDir{Dir: filepath.Join(ws, "catalog", "p"), Root: ws}
			},
			want: "inside", tier: TierColocated,
		},
		{
			name: "a link to a guest outside the root is not read, and the cascade halts rather than falling through",
			layout: func(t *testing.T, ws, outside string) ColocatedDir {
				put(t, filepath.Join(outside, "g.wasm"), "outside")
				link(t, filepath.Join(outside, "g.wasm"), filepath.Join(ws, "catalog", "p", leaf))
				return ColocatedDir{Dir: filepath.Join(ws, "catalog", "p"), Root: ws}
			},
			link: filepath.Join("catalog", "p", leaf),
		},
		{
			name: "a guest beneath a directory link leading out of the root is not read",
			layout: func(t *testing.T, ws, outside string) ColocatedDir {
				put(t, filepath.Join(outside, "p", leaf), "outside")
				link(t, filepath.Join(outside, "p"), filepath.Join(ws, "catalog", "p"))
				return ColocatedDir{Dir: filepath.Join(ws, "catalog", "p"), Root: ws}
			},
			link: filepath.Join("catalog", "p"),
		},
		{
			name: "control: a link leading nowhere carries nothing to read and is a clean miss",
			layout: func(t *testing.T, ws, outside string) ColocatedDir {
				link(t, filepath.Join(outside, "absent.wasm"), filepath.Join(ws, "catalog", "p", leaf))
				return ColocatedDir{Dir: filepath.Join(ws, "catalog", "p"), Root: ws}
			},
			want: "cached", tier: TierCache,
		},
		{
			name: "control: a directory listed outside the workspace is a root of its own, and a link elsewhere inside it is read",
			layout: func(t *testing.T, ws, outside string) ColocatedDir {
				listed := filepath.Join(outside, "listed")
				put(t, filepath.Join(listed, "guests", "g.wasm"), "listed")
				link(t, filepath.Join(listed, "guests", "g.wasm"), filepath.Join(listed, "p", leaf))
				return ColocatedDir{Dir: filepath.Join(listed, "p"), Root: listed}
			},
			want: "listed", tier: TierColocated,
		},
		{
			name: "control: with no root nothing is held",
			layout: func(t *testing.T, ws, outside string) ColocatedDir {
				put(t, filepath.Join(outside, "g.wasm"), "outside")
				link(t, filepath.Join(outside, "g.wasm"), filepath.Join(ws, "catalog", "p", leaf))
				return ColocatedDir{Dir: filepath.Join(ws, "catalog", "p")}
			},
			want: "outside", tier: TierColocated,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ws, outside := t.TempDir(), t.TempDir()
			at := tc.layout(t, ws, outside)
			if err := oci.WriteCached(ws, ".aggrega/cache", oci.PluginArtifactKind(), ref, "", []byte("cached")); err != nil {
				t.Fatalf("seed cache: %v", err)
			}
			r := NewResolver(ResolutionContext{
				ColocatedDirs: map[string]ColocatedDir{ref: at},
				WorkspaceRoot: ws,
				CacheDir:      ".aggrega/cache",
			}, PinPostureNone, PinMaps{})

			got, err := r.Resolve(ref)
			if tc.want != "" {
				if err != nil {
					t.Fatalf("the cascade must resolve; got %v", err)
				}
				if string(got.Bytes) != tc.want || got.Tier != tc.tier {
					t.Fatalf("resolved %q from %s; want %q from %s", got.Bytes, got.Tier, tc.want, tc.tier)
				}
				return
			}
			if !errors.Is(err, readscope.ErrReadLeadsOutOfWorkspace()) {
				t.Fatalf("want the read-leads-out-of-workspace halt; got bytes %q, err %v", got.Bytes, err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0321" {
				t.Errorf("short code = %q, want E0321", code)
			}
			for _, want := range []string{
				"the link " + filepath.Join(ws, tc.link) + " leads to ",
				"which must stay inside " + ws,
				"the built .wasm beside a Plugin entity",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the refusal must carry %q:\n%v", want, err)
				}
			}

			// The pull's question about an entity authored in a catalog directory asks
			// the same cascade, so it is refused with the same halt rather than with the
			// artifact-not-found answer a miss would draw.
			_, aerr := r.ResolveCatalogArtifact("governance/probe:1.0.0", ref)
			if !errors.Is(aerr, readscope.ErrReadLeadsOutOfWorkspace()) || errors.Is(aerr, ErrPluginArtifactNotFound()) {
				t.Errorf("the lock-time question must return the read hold as it is; got %v", aerr)
			}
		})
	}
}

func put(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func link(t *testing.T, target, at string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(at), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Symlink(target, at); err != nil {
		t.Skipf("symbolic links are not available here: %v", err)
	}
}
