// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheCacheCommandsSayWhenTheRootTheyWereGivenIsUnusable holds the fallback a
// cache command takes when the workspace root it was given cannot be used: it
// keeps going on the machine-wide caches alone, and says so in one line naming
// the root and why, in the words the resolution itself refuses that root with —
// the ones every command that needs the workspace prints. A root that resolves
// says nothing, and a caller that passes no writer — the prune, which refuses
// without a workspace rather than acting on the machine-wide caches — gets the
// same fallback with no notice.
//
// The expected sentence is built from the resolution's own refusal of the same
// root rather than written out here, so the line is held to the words the
// resolution uses wherever they are composed.
func TestTheCacheCommandsSayWhenTheRootTheyWereGivenIsUnusable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	if err := os.WriteFile(file, []byte("not a directory\n"), 0o644); err != nil {
		t.Fatalf("write the file: %v", err)
	}
	usable := seedCapTreeRoot(t, "")

	contextFor := func(root string) context.Context {
		ctx := withWorkspaceResolver(context.Background(), root, globalConfigLookup("", frozenEnv(nil)))
		nameWorkspaceRootSource(ctx, "--"+workspaceRootFlagName)
		return ctx
	}

	for _, tc := range []struct {
		name string
		root string
		why  string
	}{
		{name: "a file", root: file, why: "which is not a directory"},
		{name: "a path that does not exist", root: filepath.Join(dir, "missing"), why: "which cannot be used"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, refusal := workspaceFromContext(contextFor(tc.root))
			if refusal == nil {
				t.Fatalf("the resolution accepted %s as a workspace root, so there is no fallback to report", tc.root)
			}
			if !strings.Contains(refusal.Error(), tc.why) {
				t.Fatalf("the resolution refused %s as %q; want it to say %q", tc.root, refusal, tc.why)
			}

			var errOut bytes.Buffer
			loc, err := resolveCacheLocations(contextFor(tc.root), &errOut)
			if err != nil {
				t.Fatalf("the cache commands stopped on an unusable root (%v); they fall back to the machine-wide caches", err)
			}
			if loc.WorkspaceRoot != "" {
				t.Errorf("the locations carry the workspace root %q; an unusable root resolves none", loc.WorkspaceRoot)
			}
			want := "note: " + refusal.Error() + "; only the machine-wide caches are acted on\n"
			if errOut.String() != want {
				t.Errorf("the notice is %q; want %q", errOut.String(), want)
			}

			// The prune passes no writer: the fallback is taken all the same, and
			// there is nowhere to write the notice to.
			if loc, err := resolveCacheLocations(contextFor(tc.root), nil); err != nil || loc.WorkspaceRoot != "" {
				t.Errorf("with no writer the resolution returned root %q and %v; want no root and no error",
					loc.WorkspaceRoot, err)
			}
		})
	}

	t.Run("a usable root", func(t *testing.T) {
		t.Parallel()
		var errOut bytes.Buffer
		loc, err := resolveCacheLocations(contextFor(usable), &errOut)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if loc.WorkspaceRoot == "" {
			t.Errorf("a usable root resolved no workspace")
		}
		if errOut.Len() != 0 {
			t.Errorf("a usable root produced a notice: %q", errOut.String())
		}
	})
}
