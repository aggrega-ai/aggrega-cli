// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that a CloudFormation resource
// logical id a template cannot hold halts the build instead of reaching the
// emitted bytes, and that a legal one is untouched.
//
// The fixture's Recipe writes the component instance's own name into the
// resource logical id without rewriting it, which is what a Recipe is free to
// do: the provider emits the id exactly as authored. Its two Aggregates differ
// only in what the component is named — one carries a hyphen and an underscore,
// the other letters alone — so the pair isolates the name as the single variable
// and nothing about the Recipe, the Manifest or the boundary moves between them.
//
// The legal build's emitted Outputs is what makes the asymmetry readable in one
// document rather than argued: the provider derives every Output name itself,
// writing the contributing component and the entry into the alphabet an Output
// name admits and joining them, so a separator in either arrives as an escape
// and the provider always has a legal name to emit. The resource logical id
// passes through as authored instead, so there is no name for the provider to
// derive and refusing is the only thing left — which is why it is the surface
// that needs a refusal.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const (
	cfnVerbatimIDRelease      = "testdata/cloudformation-verbatim-logical-id/release.yaml"
	cfnVerbatimIDLegalRelease = "testdata/cloudformation-verbatim-logical-id/release-legal.yaml"
	cfnVerbatimIDCatalog      = "testdata/cloudformation-verbatim-logical-id/catalog/base"

	// The component each Aggregate declares, which is also the resource logical
	// id its Recipe authors: the Recipe passes the name through untouched.
	cfnVerbatimIDIllegalComponent = "payments-db_primary"
	cfnVerbatimIDLegalComponent   = "paymentsdbprimary"

	// The stack Output name the provider derives for the legal build's one
	// provides entry, written as the literal the emitted template carries. The
	// component and the entry are each written into the alphabet an Output name
	// admits and joined by a sequence neither can carry; the entry is authored
	// `queue_url`, and its underscore is written as an escape rather than
	// dropped, so two entries differing only in punctuation stay apart.
	cfnVerbatimIDLegalOutput = "paymentsdbprimaryXXqueueXzuurl"
)

func cfnVerbatimIDInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  release,
		CatalogPaths: []string{cfnVerbatimIDCatalog},
	}
}

// TestCloudFormationVerbatimLogicalID_RefusesAnIDTheTemplateCannotHold asserts
// the build halts, and that the halt reaches the caller as an error naming what
// the author has to change.
//
// It asserts the halt's own subject rather than merely that the build failed:
// the fixture is otherwise valid, so a row satisfied by any error would keep
// passing once some unrelated gate rejected it first, and would silently stop
// covering the alphabet. Both characters are named, because the author has to
// fix both and a message naming one sends them back for a second build.
//
// The error is asserted rather than a guest trap tolerated. This core runs
// inside a WebAssembly guest, so a panic there would reach the host through the
// runtime's error channel and also fail a "the build failed" check — which is
// why that check is not the one made here.
//
// It asserts over the whole wrap chain, which is where the render layer's own
// summary keeps the cause. That is what the command layer walks to print the
// continuation beneath a failure, so a substring reachable here is a substring a
// reader sees. What this cannot assert from inside the compiler is the command
// layer's rendering of it: that formatter is declared in the command package,
// which nothing here can import.
func TestCloudFormationVerbatimLogicalID_RefusesAnIDTheTemplateCannotHold(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, cfnVerbatimIDInput(cfnVerbatimIDRelease))
	if err == nil {
		keys := make([]string, 0, len(result.Artifacts))
		for k := range result.Artifacts {
			keys = append(keys, k)
		}
		t.Fatalf("the build emitted %v; a resource logical id carrying a hyphen and an underscore must halt it rather than reach the emitted template", keys)
	}
	msg := joinErrorChain(err)
	t.Logf("halt: %s", msg)
	for _, want := range []string{
		// The component, so the author knows which name to go and look at.
		`"` + cfnVerbatimIDIllegalComponent + `"`,
		// Each character that cannot be there, and both of them.
		`"-"`,
		`"_"`,
		// What the author may write instead.
		"letters and digits",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not name %s: %s", want, msg)
		}
	}
}

// TestCloudFormationVerbatimLogicalID_ALegalIDIsEmittedUnchanged asserts the
// refusal does not over-fire, and reads the emitted bytes rather than the build's
// exit alone: what matters is that the id lands in the template exactly as
// authored, since a check that halted nothing but quietly rewrote the name would
// pass a build-succeeded assertion.
//
// It also asserts the asymmetry in the same document. The resource logical id is
// the author's text verbatim; the Output name is the provider's own derivation,
// which writes the separator its authored entry carried as an escape. That is
// why one surface needs a refusal and the other cannot: a derived name can
// always be written into the alphabet, an authored one can only be refused.
func TestCloudFormationVerbatimLogicalID_ALegalIDIsEmittedUnchanged(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnVerbatimIDInput(cfnVerbatimIDLegalRelease))
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	resourceIDs := cfnSectionKeys(stack, "Resources")
	if len(resourceIDs) != 1 || resourceIDs[0] != cfnVerbatimIDLegalComponent {
		t.Fatalf("the assembled stack declares resources %v, want exactly [%s] — the id the Recipe authored, unchanged\n--- stack ---\n%s",
			resourceIDs, cfnVerbatimIDLegalComponent, raw)
	}

	outputIDs := cfnSectionKeys(stack, "Outputs")
	if len(outputIDs) != 1 || outputIDs[0] != cfnVerbatimIDLegalOutput {
		t.Fatalf("the assembled stack declares outputs %v, want exactly [%s] — the name the provider derives\n--- stack ---\n%s",
			outputIDs, cfnVerbatimIDLegalOutput, raw)
	}

	// The whole document lies inside the alphabet a logical id admits, asserted
	// over every name the stack addresses something by rather than over the two
	// checked above, so a name the fixture gains later is covered without anyone
	// remembering to list it.
	for _, section := range []string{"Resources", "Outputs"} {
		for _, id := range cfnSectionKeys(stack, section) {
			for _, r := range id {
				alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
				if !alnum {
					t.Errorf("the emitted %s name %q carries %q — a logical id admits letters and digits only\n--- stack ---\n%s",
						section, id, r, raw)
				}
			}
		}
	}
}

// TestCloudFormationVerbatimLogicalID_LegalBuildIsDeterministic asserts the
// control build is byte-identical across repeated runs, which is the standing
// output contract and the property a refusal added to the read path could
// plausibly have disturbed.
func TestCloudFormationVerbatimLogicalID_LegalBuildIsDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, cfnVerbatimIDInput(cfnVerbatimIDLegalRelease))
}
