// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// End-to-end proof that a literal written in an attachment's own inject block is
// refused rather than built and dropped.
//
// An attachment's own block delivers into its component what the attachment's
// FeatureRecipe publishes, each entry naming a published key with `from:`. A
// `value:` entry there names nothing the attachment publishes, and a build that
// accepted it delivered nothing for it into the component. Each case below is
// built through the real pipeline.

package pipeline_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

const attachmentLiteralDir = "testdata/attachment-inject-literal/"

// buildAttachmentLiteral compiles one of this fixture's workspaces through the
// real pipeline and returns the artifacts and the error, without failing the test
// on either.
//
// The module case reads a second catalog holding the module's own Aggregate; it
// is kept out of every other case so a module the build does not import cannot
// take part in it.
func buildAttachmentLiteral(t *testing.T, variant string) (map[string][]byte, error) {
	t.Helper()
	catalogs := []string{attachmentLiteralDir + "catalog/base"}
	if variant == "module-literal" {
		catalogs = append(catalogs, attachmentLiteralDir+"catalog/module")
	}
	result, err := pipelinetest.BuildE(t, pipelinetest.Input{
		ReleasePath:  attachmentLiteralDir + "release-" + variant + ".yaml",
		CatalogPaths: catalogs,
	})
	if err != nil {
		return nil, err
	}
	return result.Artifacts, nil
}

// renderedPipe reports whether any artifact carries want, the text the fixture's
// Recipe renders for one entry of the component's env delivery pipe.
func renderedPipe(artifacts map[string][]byte, want string) bool {
	for _, body := range artifacts {
		if strings.Contains(string(body), want) {
			return true
		}
	}
	return false
}

// TestALiteralInAnAttachmentsOwnInjectBlockIsRefused writes one block — a
// reference to the key the attachment publishes, then a literal — on an
// attachment declared on the component, on one declared for a whole scope, and
// on one declared inside a module the Aggregate imports. Each halts under the
// condition naming a literal on an attachment, with one finding filed under the
// document that wrote the block, positioned at the attachment's own mechanism and
// naming the literal's entry alone: the reference beside it is read and passed,
// not refused with it.
//
// The module row is the one the other two cannot stand in for: a module's
// document is read only once the composition is flattened, so a rule asked of the
// Aggregate a build is handed does not reach it on its own.
func TestALiteralInAnAttachmentsOwnInjectBlockIsRefused(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name    string
		variant string
		entity  string
		locator string
	}{
		{"an attachment declared on the component", "literal",
			"Aggregate aggregate-literal", ".spec.components.api.features.token.inject.env"},
		{"an attachment declared scope-wide", "scope-literal",
			"Aggregate aggregate-scope-literal", ".spec.subsystems.tier.features.token.inject.env"},
		{"an attachment declared inside an imported module", "module-literal",
			"Aggregate token-module", ".spec.components.api.features.token.inject.env"},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			artifacts, err := buildAttachmentLiteral(t, row.variant)
			if err == nil {
				t.Fatalf("the build succeeded over a literal in an attachment's own inject block.\n"+
					"INVARIANT: an attachment's block delivers the keys its FeatureRecipe publishes; a literal there is refused.\n"+
					"CONSEQUENCE: the literal reaches no component and the build reports success.\n"+
					"delivered the literal: %v", renderedPipe(artifacts, "LITERAL_NAME="))
			}
			// The identity, not the message: a message comparison keeps compiling
			// after the condition is re-registered and silently stops matching.
			if !errors.Is(err, resolver.ErrAttachmentInjectValue()) {
				t.Fatalf("the build halted on a different condition than the one naming a literal on an attachment: %v", err)
			}
			// The refusal is raised over the authored documents — the Aggregate
			// while it is resolved, a module's document once the composition has
			// loaded it — so it arrives as a group of resolution findings rather
			// than as a finding of a later stage. The group carrying exactly one
			// finding is what says the reference beside the literal, and every
			// other rule, passed.
			finding := soleResolutionFinding(t, err)
			if !errors.Is(finding, resolver.ErrAttachmentInjectValue()) {
				t.Fatalf("the one finding carries a different condition: %v", finding)
			}
			if code, ok := cerrors.ShortCodeOf(finding); !ok || code != "E0372" {
				t.Errorf("the finding's short code is %q (found %v); want E0372", code, ok)
			}
			if finding.Entity != row.entity || finding.Field != row.locator {
				t.Errorf("the finding is filed under %q at %q; want %q at %q",
					finding.Entity, finding.Field, row.entity, row.locator)
			}
			text := finding.Error()
			for _, want := range []string{`"LITERAL_NAME"`, `"env"`, "the component's own inject block"} {
				if !strings.Contains(text, want) {
					t.Errorf("the finding does not say %s; got: %s", want, text)
				}
			}
			if strings.Contains(text, "TOKEN_NAME") {
				t.Errorf("the reference beside the literal was refused too; got: %s", text)
			}
		})
	}
}

// soleResolutionFinding returns the one finding a halt raised while the
// documents were resolved carries, failing when the halt carries no group of
// such findings or a group of any other size.
func soleResolutionFinding(t *testing.T, err error) *resolver.ResolverError {
	t.Helper()
	var group *resolver.MultiResolverError
	if !errors.As(err, &group) {
		t.Fatalf("the halt carries no group of resolution findings; got %T: %v", err, err)
	}
	if len(group.Violations) != 1 {
		t.Fatalf("the halt carries %d resolution finding(s), want exactly 1: %v", len(group.Violations), refusalText(err))
	}
	return group.Violations[0]
}

// TestAReferenceInAnAttachmentsOwnInjectBlockStillDelivers is the reference half
// of the case above, alone: the same block with the literal removed builds, and
// the value the attachment's FeatureRecipe publishes reaches the component under
// the entry's `in:`.
func TestAReferenceInAnAttachmentsOwnInjectBlockStillDelivers(t *testing.T) {
	t.Parallel()

	artifacts, err := buildAttachmentLiteral(t, "reference")
	if err != nil {
		t.Fatalf("the build halted on a reference to a key the attachment publishes: %v", err)
	}
	if len(artifacts) == 0 {
		t.Fatal("the build produced no artifact, so the case proves nothing about what it rendered")
	}
	if !renderedPipe(artifacts, "TOKEN_NAME=tok-secret") {
		t.Error("the reference in the attachment's block did not reach the component")
	}
}

// TestALiteralInTheComponentsOwnInjectBlockIsDelivered is the positive control
// for the remedy the refusal names: the literal moved to the component's own
// block builds and reaches the component, beside the attachment's reference.
func TestALiteralInTheComponentsOwnInjectBlockIsDelivered(t *testing.T) {
	t.Parallel()

	artifacts, err := buildAttachmentLiteral(t, "own-literal")
	if err != nil {
		t.Fatalf("the build halted on a literal in the component's own inject block: %v", err)
	}
	for _, want := range []string{"LITERAL_NAME=a-literal", "TOKEN_NAME=tok-secret"} {
		if !renderedPipe(artifacts, want) {
			t.Errorf("the component did not receive %q", want)
		}
	}
}
