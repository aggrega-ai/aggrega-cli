// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// TestRenderDiagnosticsLeadWithTheirConditionsSentence pins the composition every
// render-stage diagnostic in this package follows: the condition's registered
// sentence opens the message, the occurrence's own values follow it, and the
// sentence is stated exactly ONCE.
//
// The occurrence count is the half worth having. A message that opens with the
// right sentence and also embeds it further along reads as one diagnostic saying
// the same thing twice, and asserting only the prefix cannot see that — so both
// halves are asserted together for every constructor.
//
// The constructors here are the reachable ones: each is a pure function of its
// arguments, so a table exercises the real composition rather than a re-implemented
// copy of it. The conditions raised inline inside larger render passes are covered
// by those passes' own tests.
func TestRenderDiagnosticsLeadWithTheirConditionsSentence(t *testing.T) {
	site := localsRefSite{Where: `Recipe "web" provides["host"]`, File: "web", Key: "host"}
	violation := &templateScopeViolation{
		Ref:      gotmpl.RootRef{Segs: []string{"nope"}, Path: ".nope.here"},
		Admitted: []string{"values", "infra"},
	}
	keyViolation := &templateScopeViolation{
		Ref:      gotmpl.RootRef{Segs: []string{"locals"}, Path: ".locals.missing"},
		Admitted: []string{"declared"},
		Level:    scopeLevelKey,
	}

	cases := []struct {
		name      string
		err       error
		condition error
	}{
		{"locals forward reference", localsForwardRefErr(site, ".locals.later", "later", []string{"earlier"}), ErrLocalsForwardReference()},
		{"locals forward self-reference", localsForwardRefErr(site, ".locals.host", "host", nil), ErrLocalsForwardReference()},
		{"locals unknown reference", localsUnknownRefErr(site, ".locals.typo", "typo", []string{"host"}), ErrLocalsUnknownReference()},
		{"locals unknown reference, no locals declared", localsUnknownRefErr(site, ".locals.typo", "typo", nil), ErrLocalsUnknownReference()},
		{"pipeline patch target not structured", pipelineNotStructuredErr("web", "svc.yaml"), ErrPipelinePatchTargetNotStructured()},
		{"pipeline patch pre-assembly", pipelinePreAssemblyErr("web", "svc.yaml", "boundary"), ErrPipelinePatchPreAssembly()},
		{"pipeline host scope unavailable", pipelineHostScopeErr("web", "extra.yaml"), ErrPipelineHostScopeUnavailable()},
		{"template unknown root", templateUnknownRootErr(cerrors.CatalogError, "provides[\"host\"]", violation), ErrTemplateUnknownRoot()},
		{"template unknown key under a root", templateUnknownRootErr(cerrors.CatalogError, "provides[\"host\"]", keyViolation), ErrTemplateUnknownRoot()},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sentence, ok := cerrors.MessageOf(tc.condition)
			if !ok || sentence == "" {
				t.Fatalf("condition carries no registered sentence")
			}
			var compiled *cerrors.CompilerError
			if !asCompilerError(tc.err, &compiled) {
				t.Fatalf("diagnostic is not a CompilerError: %T", tc.err)
			}
			if !strings.HasPrefix(compiled.Message(), sentence) {
				t.Errorf("message does not lead with its registered sentence\n  sentence: %q\n  message : %q", sentence, compiled.Message())
			}
			if n := strings.Count(compiled.Message(), sentence); n != 1 {
				t.Errorf("message states the registered sentence %d times; want exactly 1\n  message: %q", n, compiled.Message())
			}
		})
	}
}

func asCompilerError(err error, out **cerrors.CompilerError) bool {
	ce, ok := err.(*cerrors.CompilerError)
	if ok {
		*out = ce
	}
	return ok
}
