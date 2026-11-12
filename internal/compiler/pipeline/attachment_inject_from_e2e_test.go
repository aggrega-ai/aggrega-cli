// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof that an attachment's inject entry whose `from:` names a key the
// attachment's FeatureRecipe does not publish is refused rather than built and
// dropped — unless the FeatureRecipe reads the attachment's entries itself.
//
// The cases share the fixture the literal cases build: its FeatureRecipe
// token-compose publishes one key, name, and reads no entry; directive-token-compose
// publishes name as well and reads the attachment's entries at .inject, rendering
// them into a config of its own.

package pipeline_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// TestAnAttachmentFromNamingNoPublishedKeyIsRefused builds an attachment whose
// entry reads `nmae` from a FeatureRecipe that publishes `name` and reads no entry
// itself. The build halts under the condition a `from:` naming no published key
// raises on every surface, positioned at the attachment's own entry and naming the
// key, the entry, the FeatureRecipe and the key it does publish.
func TestAnAttachmentFromNamingNoPublishedKeyIsRefused(t *testing.T) {
	t.Parallel()

	artifacts, err := buildAttachmentLiteral(t, "unresolved-from")
	if err == nil {
		t.Fatalf("the build succeeded over a from: naming no key the FeatureRecipe publishes.\n"+
			"INVARIANT: an attachment's entry names a key its FeatureRecipe publishes, or one it reads itself.\n"+
			"CONSEQUENCE: the component starts without the variable and the build reports success.\n"+
			"delivered: %v", renderedPipe(artifacts, "TOKEN_NAME="))
	}
	// The identity, not the message: a message comparison keeps compiling after the
	// condition is re-registered and silently stops matching.
	if !errors.Is(err, resolver.ErrInvalidInjectKey()) {
		t.Fatalf("the build halted on a different condition than the one naming an unpublished from-key: %v", err)
	}
	if code, ok := cerrors.ShortCodeOf(err); !ok || code != "E0156" {
		t.Errorf("the halt's short code is %q (found %v); want E0156", code, ok)
	}
	ce := diagnosticCarrying(err, "invalid inject key")
	if ce == nil {
		t.Fatalf("the halt carries no diagnostic stating the condition: %v", err)
	}
	if want := ".spec.components.api.features.token"; ce.Source().Locator != want {
		t.Errorf("the finding is positioned at %q; want the attachment's own entry %q", ce.Source().Locator, want)
	}
	text := ce.Message()
	for _, want := range []string{
		`"nmae"`, `in "TOKEN_NAME"`, `mechanism "env"`, `FeatureRecipe "token-compose"`, `publishes "name"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the finding does not say %s; got: %s", want, text)
		}
	}
}

// TestAnAttachmentEntryHeldAndUnpublishedIsReportedForEachReason writes one entry
// that fails twice: its key is one the component's own block already delivers, and
// it reads a key the attachment's FeatureRecipe does not publish. Each reason has
// its own remedy, so the one halt carries both findings, each under its own
// condition.
func TestAnAttachmentEntryHeldAndUnpublishedIsReportedForEachReason(t *testing.T) {
	t.Parallel()

	_, err := buildAttachmentLiteral(t, "held-unresolved")
	if err == nil {
		t.Fatal("the build succeeded over an entry whose key is held and whose from: names no published key")
	}
	for _, condition := range []error{renderer.ErrAttachmentInjectKeyCollision(), resolver.ErrInvalidInjectKey()} {
		if !errors.Is(err, condition) {
			t.Errorf("the halt does not carry %v; got: %v", condition, err)
		}
	}
	// A halt joining several findings reports its envelope at the outermost frame
	// and each finding beneath it, so the findings are read where they sit.
	findings := strings.Join(diagnosticMessages(err), "\n")
	for _, want := range []string{
		`duplicate delivery key across attachment-inject and another inject source ("TOKEN_NAME"`,
		`("nmae", in "TOKEN_NAME", mechanism "env", FeatureRecipe "token-compose", publishes "name")`,
	} {
		if !strings.Contains(findings, want) {
			t.Errorf("the halt does not carry the finding %s; got:\n%s", want, findings)
		}
	}
}

// diagnosticMessages is the message of every compiler diagnostic a halt carries,
// walking each wrapped error and each joined one.
func diagnosticMessages(err error) []string {
	var out []string
	for _, ce := range diagnostics(err) {
		if ce.Message() != "" {
			out = append(out, ce.Message())
		}
	}
	return out
}

// diagnosticCarrying is the first compiler diagnostic a halt carries whose message
// contains text, or nil where none does. It is read by its message rather than
// as the outermost diagnostic, because a frame wrapping the finding is a
// diagnostic too and carries no locator of its own.
func diagnosticCarrying(err error, text string) *cerrors.CompilerError {
	for _, ce := range diagnostics(err) {
		if strings.Contains(ce.Message(), text) {
			return ce
		}
	}
	return nil
}

// diagnostics is every compiler diagnostic a halt carries, outermost first,
// walking each wrapped error and each joined one.
func diagnostics(err error) []*cerrors.CompilerError {
	var out []*cerrors.CompilerError
	var walk func(error)
	walk = func(e error) {
		if e == nil {
			return
		}
		if ce, ok := e.(*cerrors.CompilerError); ok {
			out = append(out, ce)
		}
		switch wrapped := e.(type) {
		case interface{ Unwrap() []error }:
			for _, inner := range wrapped.Unwrap() {
				walk(inner)
			}
		case interface{ Unwrap() error }:
			walk(wrapped.Unwrap())
		}
	}
	walk(err)
	return out
}

// TestAnAttachmentFromItsFeatureRecipeReadsItselfStillBuilds is the case the
// refusal must not reach: the entry reads `api_token`, which the FeatureRecipe does
// not publish, but the FeatureRecipe reads the attachment's entries at .inject and
// renders them into a config of its own. The entry is consumed rather than
// dropped, so the build renders — with the entry in the FeatureRecipe's output and
// nothing delivered into the component's own pipe.
func TestAnAttachmentFromItsFeatureRecipeReadsItselfStillBuilds(t *testing.T) {
	t.Parallel()

	artifacts, err := buildAttachmentLiteral(t, "consumed")
	if err != nil {
		t.Fatalf("the build halted on an entry its FeatureRecipe reads itself: %v", err)
	}
	if len(artifacts) == 0 {
		t.Fatal("the build produced no artifact, so the case proves nothing about what it rendered")
	}
	if !renderedPipe(artifacts, `{"OPENAI_API_KEY":"api_token"}`) {
		t.Error("the FeatureRecipe did not render the entry it reads at .inject")
	}
	if renderedPipe(artifacts, "OPENAI_API_KEY=") {
		t.Error("the entry reached the component's own pipe, which delivers only published keys")
	}
}

// TestTheUnpublishedKeyRefusalReadsTrueOnAConnection asserts the condition's
// registered wording on a surface that raised it before an attachment's own block
// did: a connection reading a key the component it points at does not publish. The
// wording names no particular source, because the condition is raised from three,
// and each finding names its source in its detail.
func TestTheUnpublishedKeyRefusalReadsTrueOnAConnection(t *testing.T) {
	t.Parallel()

	_, err := buildDeliveryOrigin(t, "component-authored")
	if err == nil {
		t.Fatal("the build succeeded over a connection reading a key its target does not publish")
	}
	if !errors.Is(err, resolver.ErrInvalidInjectKey()) {
		t.Fatalf("the build halted on a different condition: %v", err)
	}
	refusal := refusalText(err)
	if want := "invalid inject key: the from-key must name a provides key its source publishes"; !strings.Contains(refusal, want) {
		t.Errorf("the refusal does not open with the registered wording %q; got: %s", want, refusal)
	}
	if strings.Contains(refusal, "of the target component type") {
		t.Errorf("the refusal still names the target component type as the only source; got: %s", refusal)
	}
	if !strings.Contains(refusal, "core/db") {
		t.Errorf("the refusal does not name the component type it resolved against; got: %s", refusal)
	}
}
