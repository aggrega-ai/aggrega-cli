// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/closure"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/sourceresolver"
)

// What this file proves.
//
// A pull resolves the source a Recipe names, and when that source cannot be
// fetched the refusal a reader sees opens with what the pull was resolving, in
// the words the reader authored — a Recipe, a FeatureRecipe, its source — rather
// than a name the code gives the step that does it. The reader never wrote that
// name and no page teaches it, so a message carrying it tells them nothing they
// can act on.
//
// The run is the command's own entry point over a real workspace. The one thing
// replaced is the transport, through the seam the operation declares for it, and
// the replacement refuses exactly as a git adapter refuses a reference the
// repository does not carry: through the shared wrap every adapter reports with,
// under the not-found condition.

// refusingSourceResolver answers every source resolution the way an adapter
// answers a reference the remote does not hold.
type refusingSourceResolver struct{}

func (refusingSourceResolver) Resolve(spec closure.SourceSpec) (*closure.ResolvedSource, error) {
	return nil, sourceresolver.Wrap(sourceresolver.SourceSpec{
		Kind:    spec.Kind,
		URL:     spec.URL,
		Version: spec.Version,
		Ref:     spec.Ref,
	}, sourceresolver.ErrFetchNotFound(), errors.New("reference not found"))
}

// sourcePassPull drives a writing pull over the fixture, asking for the pin step
// as --pin-sources does: the fixture's Recipe carries an authored-identity git
// source, and a pull that records no pin beside it halts, which would stop the
// positive control below for a reason this file is not about.
func sourcePassPull(t *testing.T, root, releasePath, catalogRoot string, res closure.SourceResolver) error {
	t.Helper()
	pin := true
	return pullRun(context.Background(), pullConfig{
		ws:             resolveTempWorkspace(t, tempWorkspaceOptions{Root: root}),
		ReleasePath:    releasePath,
		CatalogDirs:    []string{catalogRoot},
		PinSources:     &pin,
		sourceResolver: res,
		DiagOut:        io.Discard,
		Out:            io.Discard,
	})
}

// TestAPullRefusalFromTheSourcePassSaysWhatWasBeingResolved holds a refusal
// raised while resolving a Recipe's source to three things at once: it carries
// the not-found condition the adapter raised, so a caller matching by identity
// keeps matching; it says what was being resolved in the reader's terms; and it
// carries no trace of the step's code name. The control row resolves the same
// workspace through a resolver that answers, so a fixture that could not pull at
// all would fail there rather than pass the refusal row for the wrong reason.
func TestAPullRefusalFromTheSourcePassSaysWhatWasBeingResolved(t *testing.T) {
	t.Parallel()
	const prefix = "resolving the source of each Recipe and FeatureRecipe the Release uses: "

	for _, tc := range []struct {
		name    string
		res     closure.SourceResolver
		refused bool
	}{
		{
			name: "a source the remote answers is pinned",
			res:  &movingReferenceResolver{commit: movedRefCommitBefore},
		},
		{
			name:    "a source the remote does not hold is refused in the reader's terms",
			res:     refusingSourceResolver{},
			refused: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			releasePath, catalogRoot := writeGitRefWorkspace(t, root)

			err := sourcePassPull(t, root, releasePath, catalogRoot, tc.res)
			if !tc.refused {
				if err != nil {
					t.Fatalf("the pull must succeed when the source resolves, or the refusal row proves nothing: %v", err)
				}
				pinned, _ := pinnedSourceVersion(t, filepath.Join(root, "release.lock"))
				if pinned != movedRefCommitBefore {
					t.Fatalf("the pull must pin the commit the source resolved to; pinned %q", pinned)
				}
				return
			}

			if err == nil {
				t.Fatal("the pull succeeded over a source the remote does not hold; it must refuse")
			}
			if !errors.Is(err, oci.ErrFetchNotFound()) {
				t.Fatalf("the refusal must carry the not-found condition the adapter raised; got: %v", err)
			}
			msg := err.Error()
			t.Logf("emitted diagnostic:\n%s", msg)
			// Each text assertion reports rather than stops, so a refusal that has lost
			// the reader's words and kept the code's name fails on both counts at once.
			if at := strings.Index(msg, prefix); at < 0 {
				t.Errorf("the refusal does not say what the pull was resolving; want %q in:\n%s", prefix, msg)
			} else if !strings.Contains(msg[at+len(prefix):], `"server-recipe:1.0.0"`) {
				t.Errorf("the refusal does not name, after saying what was being resolved, the Recipe whose source it was:\n%s", msg)
			}
			if strings.Contains(msg, "descent") {
				t.Errorf("the refusal still carries the code's name for the step, which no reader authored:\n%s", msg)
			}
		})
	}
}
