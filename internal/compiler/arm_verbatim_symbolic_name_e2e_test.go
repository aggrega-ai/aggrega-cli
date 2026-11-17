// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that a resource symbolic name a template cannot hold halts
// the build instead of reaching the emitted bytes, and that a legal one is
// untouched.
//
// The fixture's Recipe writes the component's symbol value into the template as
// the resource's symbolic name without rewriting it, which is what a Recipe is
// free to do: the provider emits the name exactly as authored. The Aggregates
// differ only in the symbol values, so the name is the one variable between the
// control and each fault.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const armVerbatimDir = "testdata/arm-verbatim-symbolic-name/"

// TestArmVerbatimSymbolCollisionHaltsNamingBothNodes builds two datastores whose
// Recipe writes one symbolic name for both. A template holds one resource per
// symbolic name, so the build has to refuse rather than emit one resource where
// two components were declared.
//
// The refusal comes from the host comparing the names each guest declared, so
// the assertion is against that halt specifically. The message names both
// components and the symbol, because renaming one is the author's remedy.
func TestArmVerbatimSymbolCollisionHaltsNamingBothNodes(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, armInput(armVerbatimDir+"release-collision.yaml"))
	if err == nil {
		t.Fatal("two nodes declaring one symbolic name built cleanly; want a halt naming the collision")
	}
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("the build failed for a different reason than the name collision: %v", err)
	}
	var envelope *cerrors.CompilerError
	if !errors.As(err, &envelope) {
		t.Fatalf("the halt is not a categorised diagnostic: %v", err)
	}
	joined, isMulti := envelope.Unwrap().(interface{ Unwrap() []error })
	if !isMulti {
		t.Fatalf("the report wraps a single error rather than the set the rule raised: %T", envelope.Unwrap())
	}
	texts := make([]string, 0, len(joined.Unwrap()))
	for _, member := range joined.Unwrap() {
		texts = append(texts, member.Error())
	}
	whole := strings.Join(texts, "\n")
	for _, want := range []string{"alpha", "beta", "Shared"} {
		if !strings.Contains(whole, want) {
			t.Errorf("the halt does not name %q, so the author cannot tell which components to rename: %v", want, texts)
		}
	}
}

// TestArmSymbolOutsideAlphabetHalts builds a datastore whose symbolic name
// carries a hyphen. ARM admits letters, digits and underscores in a symbolic
// name, and the provider has no name of its own to substitute for an authored
// one, so refusing is the only thing left. The halt is asserted as the
// value-not-representable condition and names the pointer to fix.
func TestArmSymbolOutsideAlphabetHalts(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, armInput(armVerbatimDir+"release-outside-alphabet.yaml"))
	if err == nil {
		t.Fatalf("a symbolic name carrying a hyphen built cleanly and emitted %v", sortedKeys(result.Artifacts))
	}
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Fatalf("the build failed for a different reason than the symbolic name: %v", err)
	}
	msg := cerrors.CauseChain(err)
	t.Logf("halt: %s", msg)
	for _, want := range []string{"arm-provider:", "/resources/payments-db", "letters, digits and underscores"} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmLegalSymbolsAreEmittedUnchanged asserts the refusals do not over-fire
// and reads the emitted bytes rather than the build's exit alone: the symbols
// land in the template exactly as authored.
func TestArmLegalSymbolsAreEmittedUnchanged(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armInput(armVerbatimDir+"release-legal.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	got := sortedAnyKeys(armSection(t, template, "resources"))
	if want := []string{"Alpha", "Beta"}; !equalStringSlice(got, want) {
		t.Fatalf("the template declares resources %v, want exactly %v\n--- template ---\n%s", got, want, raw)
	}
}

// TestArmLegalSymbolsBuildIsDeterministic asserts the control build is
// byte-identical across repeated runs.
func TestArmLegalSymbolsBuildIsDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, armInput(armVerbatimDir+"release-legal.yaml"))
}
