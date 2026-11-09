// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// closureFixtureFile is one Release written into a fixture directory. A slice
// rather than a map, so the files are written in a fixed order and a failure
// reports them in that order.
type closureFixtureFile struct{ name, content string }

const (
	// closureChainHead extends a sibling that extends it back, so the chain
	// closes on itself.
	closureChainHead = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: closure-child
  environment: dev
  version: "1.0.0"
spec:
  extends: ./base.yaml
`
	closureChainBase = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: closure-base
  environment: dev
  version: "1.0.0"
spec:
  extends: ./child.yaml
`
	// closureMissingParentHead names a file that is not beside it.
	closureMissingParentHead = `apiVersion: aggrega.ai/v1beta1
kind: Release
metadata:
  name: closure-child
  environment: dev
  version: "1.0.0"
spec:
  extends: ./does-not-exist.yaml
`
)

// TestResolveClosure_FollowsTheReleaseExtendsChain holds the property that
// separates a walk which compiles from a walk a build runs: the Release's own
// extends chain is followed from the shared closure resolution every front end
// drives, not only from a direct call to the chain function.
//
// It is driven through that entry point deliberately, because the failure it
// guards against is the call being taken back out. A test that only called the
// chain function would keep passing with nothing wired, and every front end would
// then resolve a Release whose chain no one ever looks at — which reports no
// error at all, only inheritance quietly not happening.
//
// 🔴 The stage is asserted alongside the condition, and the pair is the whole
// assertion. A closure resolution that has stopped following the chain does not
// stop failing on these fixtures: an inheriting Release names no Manifest of its
// own, so resolution falls through to the sibling lookup and fails one stage
// later on a pointer that arrived empty. Only the stage tells "the chain was
// judged" apart from "the chain was skipped and something downstream noticed".
//
// It lives in the external test package because the shared closure layer imports
// the resolver: an in-package test importing it back would not compile.
func TestResolveClosure_FollowsTheReleaseExtendsChain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		files []closureFixtureFile
		// want is the condition the chain fault must report, matched through the
		// exported accessor rather than against a registered code: a code compared
		// as a string keeps compiling after the condition is registered under a
		// different one, and silently stops matching.
		want error
	}{
		{
			name: "a chain that closes on itself",
			files: []closureFixtureFile{
				{"child.yaml", closureChainHead},
				{"base.yaml", closureChainBase},
			},
			want: validator.ErrCyclicExtension(),
		},
		{
			name: "a parent that cannot be resolved",
			files: []closureFixtureFile{
				{"child.yaml", closureMissingParentHead},
			},
			want: validator.ErrInheritanceMerge(),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			for _, f := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f.name), []byte(f.content), 0o644); err != nil {
					t.Fatalf("test setup: write %s: %v", f.name, err)
				}
			}
			// The catalog view is real but empty, and on these fixtures it is never
			// consulted: the chain is followed before either sibling is looked for,
			// so resolution returns before anything reaches the catalog. It is
			// supplied rather than left nil so that a closure resolution which has
			// stopped following the chain fails the assertions below instead of
			// failing on an absent view, which would say nothing about the wiring.
			view := catalog.NewLazyLookup(dir, filepath.Join(dir, "cache"), nil, nil).ForRequest(nil)

			_, cerr := compilerops.ResolveClosure(context.Background(), compilerops.ClosureInputs{
				View:            view,
				ReleasePath:     filepath.Join(dir, "child.yaml"),
				WorkspaceRoot:   dir,
				WorkspaceConfig: workspaceconfig.Default(),
			})
			if cerr == nil {
				t.Fatal("resolving the closure of a Release whose extends chain is at fault must halt")
			}
			if cerr.Stage != compilerops.ClosureStageResolveReleaseExtends {
				t.Errorf("stage = %d; want the stage that follows the Release's own extends chain (%d). "+
					"A later stage is what an unfollowed chain looks like: the failure then names a "+
					"sibling pointer that arrived empty rather than the chain that failed to fill it: %v",
					cerr.Stage, compilerops.ClosureStageResolveReleaseExtends, cerr)
			}
			if !errors.Is(cerr, tc.want) {
				t.Errorf("closure failure = %v; want the %v condition", cerr, tc.want)
			}
		})
	}
}
