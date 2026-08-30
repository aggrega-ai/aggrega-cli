// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Component-identity refusal — build surface. Drives an Aggregate naming a
// component whose identity cannot be stamped on what it emits through the REAL
// build pipeline, asserting the halt by sentinel.
//
// An ordinary component's identity reaches its output unmodified, so the target's
// rules for such a name cannot be absorbed by a derivation: either the identity
// satisfies them or the build refuses it. These rows are that refusal, driven
// end to end rather than over the predicate alone — a declared rule that is never
// invoked passes every test written against the predicate it calls.
//
// The workspace, its writer and its positive control are shared with the
// negative-authoring corpus beside this file. Each row renames the one component
// on BOTH authoring surfaces, so the document stays internally consistent and the
// halt is attributable to the identity rather than to a component the deployment
// no longer places.
package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// renameCorpusComponent rewrites the base workspace's single component from `api`
// to the supplied identity, on the Aggregate that declares it and on the Release
// that places it.
//
// Both surfaces are rewritten because a rename on one alone authors a component
// the deployment does not place, which halts for a different reason entirely —
// and a row failing on that would read exactly like a row failing on its own
// fault.
func renameCorpusComponent(wf *workspaceFiles, identity string) {
	wf.aggregate = strings.Replace(wf.aggregate, "\n    api:\n", "\n    "+identity+":\n", 1)
	wf.release = strings.Replace(wf.release, "\n        api:\n", "\n        "+identity+":\n", 1)
}

// TestComponentIdentityRefusal_HaltsOnAnIdentityThatCannotBeStamped is the
// invocation-and-content proof for both shapes the refusal covers.
//
// The positive control is its own row rather than an assumption: an identity the
// rules admit must still build, or every refusing row below would pass on a rule
// that refuses everything.
func TestComponentIdentityRefusal_HaltsOnAnIdentityThatCannotBeStamped(t *testing.T) {
	t.Parallel()

	rows := []struct {
		name string
		// identity is what the component is renamed to on both surfaces.
		identity string
		// wantHalt is false for the rows that must still build.
		wantHalt bool
		// wantMessage is a fragment of the refusal naming the rule the identity
		// failed, so a row cannot pass on the wrong arm of the refusal.
		wantMessage string
	}{
		{
			name:     "an ordinary identity builds",
			identity: "api",
			wantHalt: false,
		},
		{
			// A dot is deliberately absent here even though a stamped name admits
			// one: the authoring surface refuses it at the envelope, because it is
			// the separator a module-contributed identity is assembled with. So the
			// punctuation an author can actually write is the hyphen and the
			// underscore, and those are what this row proves still build.
			name:     "an identity carrying punctuation inside it builds",
			identity: "main_db-1",
			wantHalt: false,
		},
		{
			name:     "the reserved prefix without the join is an ordinary name",
			identity: "bfr",
			wantHalt: false,
		},
		{
			name:        "an identity opening as a composed one does is refused",
			identity:    "bfr--api",
			wantHalt:    true,
			wantMessage: `which must not open with the reserved "bfr--"`,
		},
		{
			name:        "an identity opening on a character a stamped name may not open on is refused",
			identity:    "_api",
			wantHalt:    true,
			wantMessage: "begin and end with a letter or a digit",
		},
		{
			name:        "an identity closing on a character a stamped name may not close on is refused",
			identity:    "api-",
			wantHalt:    true,
			wantMessage: "begin and end with a letter or a digit",
		},
		{
			name:        "an identity longer than a stamped name may be is refused",
			identity:    strings.Repeat("a", 64),
			wantHalt:    true,
			wantMessage: "be at most 63 characters",
		},
	}

	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			wf := baseWorkspaceFiles()
			renameCorpusComponent(&wf, row.identity)
			_, err := pipelinetest.BuildE(t, writeMinimalWorkspace(t, wf))

			if !row.wantHalt {
				if err != nil {
					t.Fatalf("identity %q must build; got: %v", row.identity, err)
				}
				return
			}

			if err == nil {
				t.Fatalf("identity %q built clean; it would be stamped on every resource the "+
					"component emits", row.identity)
			}
			if !errors.Is(err, analyzer.ErrUnusableComponentIdentity()) {
				t.Fatalf("identity %q halted on something other than the identity refusal: %v",
					row.identity, err)
			}

			// The per-violation finding is what a reader sees, not the wrapper's
			// summary: the summary counts violations and the finding is where the
			// identity, the rule and the position live. Asserting against the
			// wrapper's own text would assert against a sentence that names none of
			// them.
			var multi *resolver.MultiResolverError
			if !errors.As(err, &multi) {
				t.Fatalf("the halt must carry its violations; got %T", err)
			}
			found := 0
			for i, violation := range multi.Violations {
				if !errors.Is(violation.Err, analyzer.ErrUnusableComponentIdentity()) {
					continue
				}
				found++
				rendered := violation.Error()

				// The finding names the identity and the rule its replacement has to
				// satisfy. Asserting only the sentinel would pass a refusal that told
				// the author nothing they could act on.
				if !strings.Contains(rendered, row.identity) {
					t.Errorf("violation %d does not name the offending identity %q: %s", i, row.identity, rendered)
				}
				if !strings.Contains(rendered, row.wantMessage) {
					t.Errorf("violation %d does not name the rule %q the identity failed: %s",
						i, row.wantMessage, rendered)
				}

				// The fault is a name the developer wrote in the application they
				// authored, so the category has to send them there. Reporting it as a
				// catalog fault would point them at a file holding nothing they can
				// change.
				if converted := violation.ToCompilerError(); converted.Category != cerrors.DeveloperError {
					t.Errorf("violation %d category = %v, want %v", i, converted.Category, cerrors.DeveloperError)
				}
				// The locator positions the finding on the key the author typed,
				// which is what lets an editor place it.
				if !strings.HasSuffix(violation.Field, row.identity) {
					t.Errorf("violation %d locator = %q, want it to name the authored key %q",
						i, violation.Field, row.identity)
				}
			}
			if found != 1 {
				t.Errorf("violations carrying the identity sentinel = %d, want 1: %+v", found, multi.Violations)
			}
		})
	}
}
