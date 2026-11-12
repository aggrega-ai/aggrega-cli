// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that a selector the grammar refuses
// halts a build on every surface that evaluates one: a usesSchema socket, an
// attachment slot, and a Feature's appliesTo. Each malformed selector here is a
// shape that, read leniently, admits exactly the target it was written to
// exclude, so the halt is what stands between the author and a build that
// silently does the opposite of what they wrote. A control Release wires the
// same three surfaces with well-formed selectors and builds.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const selectorGrammarCatalog = "testdata/selector-grammar/catalog/base"

// TestSelectorGrammar_ARefusedSelectorHaltsTheBuildOnEverySurface drives one
// Release per surface, each carrying one malformed selector and nothing else
// wrong, and asserts the halt carries the rule's identity and names the
// selector, the socket, slot or Feature declaring it, and the requirement the
// grammar could not classify with the reason.
func TestSelectorGrammar_ARefusedSelectorHaltsTheBuildOnEverySurface(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		release  string
		sentinel error
		want     []string
	}{
		{
			name:     "a usesSchema socket selector",
			release:  "testdata/selector-grammar/release-socket.yaml",
			sentinel: analyzer.ErrSelectorViolation(),
			want: []string{
				"!tier=frontend", "backend", "which could not be evaluated",
				"requirement 1", "must be followed by a label key alone",
			},
		},
		{
			name:     "an attachment slot selector",
			release:  "testdata/selector-grammar/release-slot.yaml",
			sentinel: analyzer.ErrSlotSelectorUnsatisfied(),
			want: []string{
				"!tier=frontend", "audit", "which could not be evaluated",
				"requirement 1", "must be followed by a label key alone",
			},
		},
		{
			name:     "a Feature appliesTo",
			release:  "testdata/selector-grammar/release-appliesto.yaml",
			sentinel: analyzer.ErrSelectorViolation(),
			want: []string{
				"!tier=frontend", "core/not-frontend", "which could not be evaluated",
				"requirement 1", "must be followed by a label key alone",
			},
		},
		{
			name:     "a Feature appliesTo made of separators",
			release:  "testdata/selector-grammar/release-separators.yaml",
			sentinel: analyzer.ErrSelectorViolation(),
			want: []string{
				"core/separators", "which could not be evaluated", "requirement 1 is empty",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("the build panicked: %v", r)
				}
			}()
			_, err := pipelinetest.BuildE(t, pipelinetest.Input{
				ReleasePath:  tc.release,
				CatalogPaths: []string{selectorGrammarCatalog},
			})
			if err == nil {
				t.Fatal("the build succeeded; a selector the grammar refuses must halt it")
			}
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("the halt does not carry %v: %v", tc.sentinel, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("the halt does not name %q: %v", want, err)
				}
			}
		})
	}
}

// TestSelectorGrammar_WellFormedSelectorsOnTheSameSurfacesBuild is the control:
// the same three surfaces, each with a well-formed selector the wired target,
// the filling Feature and the attached-to component satisfy, build clean. It is
// what keeps each negative case failing on its selector rather than on the
// fixture around it.
func TestSelectorGrammar_WellFormedSelectorsOnTheSameSurfacesBuild(t *testing.T) {
	t.Parallel()
	if _, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  "testdata/selector-grammar/release-control.yaml",
		CatalogPaths: []string{selectorGrammarCatalog},
	}); err != nil {
		t.Fatalf("the control Release did not build: %v", err)
	}
}
