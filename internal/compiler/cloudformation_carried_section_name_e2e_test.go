// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// Package compiler_test — end-to-end proof that a name a CloudFormation template
// cannot hold halts the build when it is authored in a section the assembled
// stack carries verbatim, rather than reaching the emitted bytes.
//
// The sections the stack carries under the names their authors wrote hold logical
// ids on the same terms the resources do: letters and digits, unique within the
// template. Nothing between the author and the emitted bytes rewrites them, so a
// name outside that alphabet lands in a template the target will not accept —
// and the reference the fragment's own resource makes to it lands in one that
// cannot resolve.
//
// The fixture's Recipe names a template parameter after the component instance
// and references it from its one resource. Its resource logical id is a literal
// instead, so the fragment carries exactly one authored name that can fall
// outside the alphabet: what this pair isolates is the parameter's name and
// nothing else. Its two Aggregates differ only in what the component is called —
// one carries a hyphen, the other letters alone — so the name is the single
// variable and nothing about the Recipe, the Manifest or the boundary moves
// between them.

package compiler_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
)

const (
	cfnCarriedNameRelease      = "testdata/cloudformation-carried-section-name/release.yaml"
	cfnCarriedNameLegalRelease = "testdata/cloudformation-carried-section-name/release-legal.yaml"
	cfnCarriedNameCatalog      = "testdata/cloudformation-carried-section-name/catalog/base"

	// The component each Aggregate declares. The Recipe names its template
	// parameter after the instance, so the parameter's name is this plus a suffix.
	cfnCarriedNameIllegalComponent = "payments-db"
	cfnCarriedNameLegalComponent   = "paymentsdb"

	// The parameter name each build authors, written as the literal the emitted
	// template would carry.
	cfnCarriedNameIllegalParameter = "payments-dbRetention"
	cfnCarriedNameLegalParameter   = "paymentsdbRetention"

	// The stack Output name the provider derives for the legal build's one
	// provides entry. The component and the entry are each written into the
	// alphabet an Output name admits and joined by a sequence neither can carry;
	// the entry is authored `queue_url`, and its underscore is written as an
	// escape rather than dropped, so two entries differing only in punctuation
	// stay apart.
	cfnCarriedNameLegalOutput = "paymentsdbXXqueueXzuurl"
)

func cfnCarriedNameInput(release string) pipelinetest.Input {
	return pipelinetest.Input{
		ReleasePath:  release,
		CatalogPaths: []string{cfnCarriedNameCatalog},
	}
}

// TestCloudFormationCarriedSectionName_RefusesANameTheTemplateCannotHold asserts
// the build halts, and that the halt reaches the caller as an error naming what
// the author has to change.
//
// It asserts the halt's own subject rather than merely that the build failed: the
// fixture is otherwise valid, so a row satisfied by any error would keep passing
// once some unrelated gate rejected it first and would silently stop covering the
// alphabet. The SECTION is among the things asserted, because it is the word the
// author searches their own files for — a halt naming the name alone leaves them
// looking through a whole Recipe for where it was written.
//
// The error is asserted rather than a guest trap tolerated. This core runs inside
// a WebAssembly guest, so a panic there would reach the host through the runtime's
// error channel and would also fail a "the build failed" check — which is why
// that check is not the one made here.
//
// It asserts over the whole wrap chain, which is where the render layer's own
// summary keeps the cause. That is what the command layer walks to print the
// continuation beneath a failure, so a substring reachable here is a substring a
// reader sees. What this cannot assert from inside the compiler is the command
// layer's rendering of it: that formatter is declared in the command package,
// which nothing here can import.
func TestCloudFormationCarriedSectionName_RefusesANameTheTemplateCannotHold(t *testing.T) {
	t.Parallel()
	result, err := pipelinetest.BuildE(t, cfnCarriedNameInput(cfnCarriedNameRelease))
	if err == nil {
		keys := make([]string, 0, len(result.Artifacts))
		for k := range result.Artifacts {
			keys = append(keys, k)
		}
		t.Fatalf("the build emitted %v; a template parameter named %q must halt it rather than reach the emitted template",
			keys, cfnCarriedNameIllegalParameter)
	}
	msg := joinErrorChain(err)
	t.Logf("halt: %s", msg)
	for _, want := range []string{
		// The component, so the author knows whose Recipe wrote it.
		`"` + cfnCarriedNameIllegalComponent + `"`,
		// The section, which is the word to search for.
		"Parameters",
		// The name itself, and the character that cannot be in it.
		`"` + cfnCarriedNameIllegalParameter + `"`,
		`"-"`,
		// What the author may write instead.
		"letters and digits",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not name %s: %s", want, msg)
		}
	}
}

// TestCloudFormationCarriedSectionName_ALegalNameIsEmittedUnchanged asserts the
// refusal does not over-fire, and reads the emitted bytes rather than the build's
// exit alone: what matters is that the name lands in the template exactly as
// authored, since a check that halted nothing but quietly rewrote the name would
// pass a build-succeeded assertion.
//
// It also asserts the asymmetry in the same document. The parameter's name is the
// author's text verbatim; the Output name beside it is the provider's own
// derivation, which writes the separator its authored entry carried as an
// escape. That is why one surface needs a refusal and the other cannot: a
// derived name can always be written into the alphabet, an authored one can only
// be refused.
func TestCloudFormationCarriedSectionName_ALegalNameIsEmittedUnchanged(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, cfnCarriedNameInput(cfnCarriedNameLegalRelease))
	stack, raw := findCfnStack(t, result.Artifacts, "stack")

	params := cfnSectionKeys(stack, "Parameters")
	if len(params) != 1 || params[0] != cfnCarriedNameLegalParameter {
		t.Fatalf("the assembled stack declares parameters %v, want exactly [%s] — the name the Recipe authored, unchanged\n--- stack ---\n%s",
			params, cfnCarriedNameLegalParameter, raw)
	}

	outputIDs := cfnSectionKeys(stack, "Outputs")
	if len(outputIDs) != 1 || outputIDs[0] != cfnCarriedNameLegalOutput {
		t.Fatalf("the assembled stack declares outputs %v, want exactly [%s] — the name the provider derives\n--- stack ---\n%s",
			outputIDs, cfnCarriedNameLegalOutput, raw)
	}

	// The whole document lies inside the alphabet a logical id admits, asserted
	// over every section whose names are held to it rather than over the one
	// checked above, so a name the fixture gains later is covered without anyone
	// remembering to list it.
	for _, section := range []string{"Resources", "Outputs", "Parameters"} {
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

// TestCloudFormationCarriedSectionName_LegalBuildIsDeterministic asserts the
// control build is byte-identical across repeated runs, which is the standing
// output contract and the property a refusal added to the read path could
// plausibly have disturbed.
func TestCloudFormationCarriedSectionName_LegalBuildIsDeterministic(t *testing.T) {
	pipelinetest.AssertDeterministic(t, cfnCarriedNameInput(cfnCarriedNameLegalRelease))
}
