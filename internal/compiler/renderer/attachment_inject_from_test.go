// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	stderrors "errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rule refusing an attachment entry whose `from:` names a key the attachment's
// FeatureRecipe does not publish reads the same record the rule over a held key
// reads, and is asked beside it. The rows below drive it the two ways the build
// does: through the framework's traversal over a recorded fact, and through the
// delivery that writes the record and then that traversal, so what the delivery
// records about why a claim went undelivered is what the rule is judged on.

// unresolved is one claim naming a key the attachment's FeatureRecipe does not
// publish, recorded as the delivery records it.
func unresolved(mechanism, key, from, attachment, featureRecipe string, published ...string) constraint.InjectDelivery {
	return constraint.InjectDelivery{
		Mechanism: mechanism, DeliveryKey: key, Attachment: attachment,
		From: from, Unresolved: true, FeatureRecipe: featureRecipe, Published: published,
	}
}

// TestAttachmentInjectFromRule walks the rule over the rows that make it
// discriminate, refusals and silences together. A rule refusing every unresolved
// claim would satisfy every refusal row and fail the one whose FeatureRecipe reads
// the entries itself; one refusing nothing fails the rest.
func TestAttachmentInjectFromRule(t *testing.T) {
	t.Parallel()

	consumed := unresolved("env", "OPENAI_API_KEY", "api_token", "creds", "eso-fetcher")
	consumed.DirectiveRead = true
	held := unresolved("env", "TOKEN", "nmae", "token", "token-fr", "name")

	for _, tc := range []struct {
		name        string
		attachments []string
		deliveries  []constraint.InjectDelivery
		// wantInvalid and wantHeld are one expected finding per entry under each
		// condition, each a '|'-separated set of substrings it has to carry.
		wantInvalid []string
		wantHeld    []string
	}{
		{
			name:        "REFUSAL: a key the FeatureRecipe does not publish, and nothing reads the entry",
			attachments: []string{"token"},
			deliveries:  []constraint.InjectDelivery{unresolved("env", "TOKEN", "nmae", "token", "token-fr", "name", "url")},
			wantInvalid: []string{`"nmae"|in "TOKEN"|mechanism "env"|FeatureRecipe "token-fr"|publishes "name"|publishes "url"`},
		},
		{
			name:        "REFUSAL: a FeatureRecipe publishing nothing ends the message after its name",
			attachments: []string{"token"},
			deliveries:  []constraint.InjectDelivery{unresolved("file", "/etc/x", "password", "token", "empty-fr")},
			wantInvalid: []string{`"password"|in "/etc/x"|mechanism "file"|FeatureRecipe "empty-fr")`},
		},
		{
			name:        "SILENT: the FeatureRecipe reads the attachment's entries itself",
			attachments: []string{"creds"},
			deliveries:  []constraint.InjectDelivery{consumed},
		},
		{
			name:        "SILENT: a claim whose key resolved",
			attachments: []string{"token"},
			deliveries:  []constraint.InjectDelivery{delivered("env", "TOKEN", "token")},
		},
		{
			name: "SILENT: an undelivered claim recorded with no reason of this rule's",
			// A claim left undelivered because its key was held, or one whose
			// FeatureRecipe's provides were never evaluated for the component, carries
			// no unresolved mark.
			attachments: []string{"token"},
			deliveries:  []constraint.InjectDelivery{claimed("env", "TOKEN", "token")},
		},
		{
			name:        "SILENT: another attachment's unresolved claim is not this attachment's",
			attachments: []string{"other"},
			deliveries:  []constraint.InjectDelivery{unresolved("env", "TOKEN", "nmae", "token", "token-fr", "name")},
		},
		{
			name:        "BOTH: a held key read from an unpublished one is reported for each reason",
			attachments: []string{"token"},
			deliveries:  []constraint.InjectDelivery{delivered("env", "TOKEN", ""), held},
			wantHeld:    []string{`"TOKEN"|mechanism "env"`},
			wantInvalid: []string{`"nmae"|in "TOKEN"|FeatureRecipe "token-fr"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			halt := injectDeliveryHalt(t, "api", tc.attachments, tc.deliveries)
			want := append(append([]string{}, tc.wantHeld...), tc.wantInvalid...)
			if len(want) == 0 {
				if halt != nil {
					t.Fatalf("must stay silent; raised %v", halt)
				}
				return
			}
			if halt == nil {
				t.Fatal("must refuse; stayed silent")
			}
			if len(tc.wantInvalid) > 0 && !stderrors.Is(halt, resolver.ErrInvalidInjectKey()) {
				t.Errorf("halt must carry the unpublished-key condition; got %v", halt)
			}
			if gotHeld := stderrors.Is(halt, ErrAttachmentInjectKeyCollision()); gotHeld != (len(tc.wantHeld) > 0) {
				t.Errorf("halt carries the held-key condition = %v, want %v; got %v", gotHeld, len(tc.wantHeld) > 0, halt)
			}
			got := injectDeliveryFindings(t, halt)
			if len(got) != len(want) {
				t.Fatalf("want %d finding(s), got %d: %q", len(want), len(got), got)
			}
			// Findings about a held key come first, then the others.
			for i, w := range want {
				for _, part := range strings.Split(w, "|") {
					if !strings.Contains(got[i], part) {
						t.Errorf("finding %d must carry %q; got %q", i, part, got[i])
					}
				}
			}
		})
	}
}

// TestAttachmentInjectFromRuleIsPositionedAtTheAttachment asserts the finding is
// positioned at the attachment's own entry inside the component's block, the block
// whose `from:` the author edits.
func TestAttachmentInjectFromRuleIsPositionedAtTheAttachment(t *testing.T) {
	t.Parallel()
	halt := injectDeliveryHalt(t, "api", []string{"token"}, []constraint.InjectDelivery{
		unresolved("env", "TOKEN", "nmae", "token", "token-fr", "name"),
	})
	findings := injectDeliveryFindings(t, halt)
	if len(findings) != 1 {
		t.Fatalf("want one finding, got %q", findings)
	}
	if !strings.HasPrefix(findings[0], "invalid inject key: the from-key must name a provides key its source publishes") {
		t.Errorf("the finding does not open with the registered wording; got %q", findings[0])
	}
	var ce *cerrors.CompilerError
	if !stderrors.As(halt, &ce) {
		t.Fatalf("halt is not a compiler diagnostic: %v", halt)
	}
	if ce.Category() != cerrors.DeveloperError {
		t.Errorf("category = %s, want %s", ce.Category(), cerrors.DeveloperError)
	}
	if want := ".spec.components.api.features.token"; ce.Source().Locator != want {
		t.Errorf("locator = %q, want %q", ce.Source().Locator, want)
	}
}

// fromFixture is one node, "api", in boundary "cluster", carrying one attachment,
// "token", whose inject block asks for TOKEN from the given key, bound to fr.
func fromFixture(fr *types.FeatureRecipe, from string) (*types.IRGraph, RecipeLookup) {
	nodes := types.OrderedMap[types.IRNode]{}
	nodes.Set("api", types.IRNode{Component: "core/api", BoundaryPath: "cluster"})
	inner := types.OrderedMap[types.IRFeatureEntry]{}
	inner.Set("token", types.IRFeatureEntry{Type: "core/token", Inject: injectEnvBlock("TOKEN", from)})
	fvm := types.OrderedMap[types.OrderedMap[types.IRFeatureEntry]]{}
	fvm.Set("api", inner)
	graph := &types.IRGraph{Spec: types.IRSpec{Nodes: nodes, FeatureValuesMapping: fvm}}
	lookup := RecipeLookup{FeatureRecipe: func(_, featureType string) (*types.FeatureRecipe, bool) {
		if featureType == "core/token" {
			return fr, true
		}
		return nil, false
	}}
	return graph, lookup
}

// tokenFR is a FeatureRecipe publishing one key, name, with whatever else the
// caller's edit adds.
func tokenFR(edit func(fr *types.FeatureRecipe)) *types.FeatureRecipe {
	provides := types.OrderedMap[string]{}
	provides.Set("name", "tok-secret")
	fr := &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "token-fr"},
		Spec:     types.FeatureRecipeSpec{Capability: "compose", Implements: "core/token", Provides: provides},
	}
	if edit != nil {
		edit(fr)
	}
	return fr
}

// deliverAndJudge runs the delivery the render runs for one node, with the reader
// the render installs, then the rules over what it recorded, and returns the pipe,
// the record and the halt.
func deliverAndJudge(t *testing.T, fr *types.FeatureRecipe, from string, state *BoundaryFRState) (types.InjectPipe, []constraint.InjectDelivery, error) {
	t.Helper()
	graph, lookup := fromFixture(fr, from)
	inject, _ := BuildInjectPipe(nil)
	deliveries, err := ResolveAttachmentInject(&inject, graph, "api", "", lookup, nil, nil, state,
		injectDirectiveReader(RecipeTemplateLibraries{}, nil))
	if err != nil {
		t.Fatalf("delivery itself must not fail: %v", err)
	}
	return inject, deliveries, injectDeliveryHalt(t, "api", []string{"token"}, deliveries)
}

// TestAnAttachmentFromNamingNoPublishedKeyIsRecordedAndRefused drives the delivery
// and the rule together over the cases the refusal turns on: a key the
// FeatureRecipe does not publish is refused where nothing reads the entry, and
// builds where the FeatureRecipe reads it — in a template producing its own
// resources, or only in a pipeline step's input — while a key it publishes still
// delivers.
func TestAnAttachmentFromNamingNoPublishedKeyIsRecordedAndRefused(t *testing.T) {
	t.Parallel()

	readsInArtifact := func(fr *types.FeatureRecipe) {
		fr.Spec.Artifacts.Set("directive.yaml", types.ArtifactEntry{Inline: `content: '{{ toJson .inject.env }}'`})
	}
	readsInStepWith := func(fr *types.FeatureRecipe) {
		fr.Spec.Pipeline = []types.PipelineStep{{
			ID: "injector", Plugin: "pipeline-step/injector:1.0.0", Function: "inject",
			With: "env_injections: {{ if .inject }}{{ toJson .inject.env }}{{ else }}{}{{ end }}\n",
		}}
	}
	readsThroughIndexOnly := func(fr *types.FeatureRecipe) {
		fr.Spec.Pipeline = []types.PipelineStep{{
			ID: "injector", Plugin: "pipeline-step/injector:1.0.0", Function: "inject",
			With: `env_injections: {{ toJson (index . "inject") }}`,
		}}
	}
	readsInInjectMappingOnly := func(fr *types.FeatureRecipe) {
		fr.Spec.InjectMapping = `{{ toJson .inject }}`
	}

	for _, tc := range []struct {
		name    string
		edit    func(fr *types.FeatureRecipe)
		from    string
		refused bool
	}{
		{name: "REFUSED: nothing reads the entry", from: "nmae", refused: true},
		{name: "BUILDS: a template producing the FeatureRecipe's own resources reads .inject", edit: readsInArtifact, from: "api_token"},
		{name: "BUILDS: only a pipeline step's input reads .inject", edit: readsInStepWith, from: "api_token"},
		{name: "REFUSED: the entries are reached only through index over the context", edit: readsThroughIndexOnly, from: "api_token", refused: true},
		{name: "REFUSED: only the injectMapping, evaluated against the component's context, names .inject", edit: readsInInjectMappingOnly, from: "api_token", refused: true},
		{name: "DELIVERS: a key the FeatureRecipe publishes", from: "name"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			pipe, deliveries, halt := deliverAndJudge(t, tokenFR(tc.edit), tc.from, nil)
			if tc.refused {
				if !stderrors.Is(halt, resolver.ErrInvalidInjectKey()) {
					t.Fatalf("want the unpublished-key condition; got %v", halt)
				}
				return
			}
			if halt != nil {
				t.Fatalf("must build; raised %v", halt)
			}
			got, delivered := pipe.Env.Get("TOKEN")
			if tc.from == "name" {
				if !delivered || got != "tok-secret" {
					t.Errorf("a published key must still deliver; env.TOKEN = %q (present %v)", got, delivered)
				}
				return
			}
			if delivered {
				t.Errorf("a key the FeatureRecipe does not publish must deliver nothing into the pipe; env.TOKEN = %q", got)
			}
			if len(deliveries) != 1 || !deliveries[0].Unresolved || !deliveries[0].DirectiveRead {
				t.Errorf("the claim must be recorded as naming an unpublished key its FeatureRecipe reads; got %+v", deliveries)
			}
		})
	}
}

// TestAnAttachmentWhoseProvidesWereNotEvaluatedIsNeverRefused covers the
// attachment whose FeatureRecipe renders once for its boundary. Where no boundary
// holding the component made that evaluation, there is nothing to read a key
// against, so no claim is marked and the build is not refused; where the boundary
// made it, a key it does not carry is refused like any other.
func TestAnAttachmentWhoseProvidesWereNotEvaluatedIsNeverRefused(t *testing.T) {
	t.Parallel()

	boundaryFR := tokenFR(func(fr *types.FeatureRecipe) { fr.Spec.ExecutionScope = executionScopeBoundary })

	evaluated := NewBoundaryFRState()
	provides := types.OrderedMap[string]{}
	provides.Set("name", "tok-secret")
	evaluated.Evaluations[boundaryFRKey{BoundaryPath: "cluster", FRInstance: "token"}] = boundaryFREvaluation{Provides: provides}

	for _, tc := range []struct {
		name    string
		state   *BoundaryFRState
		refused bool
	}{
		{name: "no boundary made the evaluation: not refused", state: nil},
		{name: "an empty boundary state: not refused", state: NewBoundaryFRState()},
		{name: "the boundary's evaluation lacks the key: refused", state: evaluated, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, deliveries, halt := deliverAndJudge(t, boundaryFR, "nmae", tc.state)
			if tc.refused {
				if !stderrors.Is(halt, resolver.ErrInvalidInjectKey()) {
					t.Fatalf("want the unpublished-key condition; got %v", halt)
				}
				return
			}
			if halt != nil {
				t.Fatalf("an attachment whose provides were not evaluated must not be refused; raised %v", halt)
			}
			if len(deliveries) != 1 || deliveries[0].Unresolved {
				t.Errorf("no claim may be marked unresolved without an evaluation; got %+v", deliveries)
			}
		})
	}
}

// TestFeatureRecipeTemplateReadsReachesEveryTemplateItsRenderEvaluates pins the set
// the read census walks on a FeatureRecipe: each template family evaluated against
// the attachment's own render context contributes its reads, a pipeline step's
// input and gate included, and the families evaluated against another context —
// its locals, provides, source and assertions, and the injectMapping evaluated
// against the component's context — contribute none.
func TestFeatureRecipeTemplateReadsReachesEveryTemplateItsRenderEvaluates(t *testing.T) {
	t.Parallel()

	fr := tokenFR(func(fr *types.FeatureRecipe) {
		fr.Spec.ValuesMapping = types.ValuesMapping{Inline: `a: {{ .values.vm }}`}
		fr.Spec.Workload = types.WorkloadTemplate{Inline: `b: {{ .values.wl }}`}
		fr.Spec.Artifacts.Set("x.yaml", types.ArtifactEntry{Inline: `c: [[ .values.art ]]`})
		fr.Spec.Omits = []types.OmitEntry{{When: `{{ .values.omit }}`, Kind: "Secret", Name: "n"}}
		fr.Spec.Pipeline = []types.PipelineStep{{
			ID: "s", Plugin: "p", Function: "f",
			With: `w: {{ .values.with }}`, When: `{{ .values.when }}`,
		}}
		fr.Spec.Locals = types.OrderedMap[string]{}
		fr.Spec.Locals.Set("l", `{{ .values.local }}`)
		fr.Spec.Provides.Set("p", `{{ .values.provided }}`)
		fr.Spec.Assertions = types.OrderedMap[string]{}
		fr.Spec.Assertions.Set("a", `{{ .values.asserted }}`)
		fr.Spec.Source = "{{ .values.source }}"
		fr.Spec.InjectMapping = `{{ .values.mapping }}`
	})

	reads, err := FeatureRecipeTemplateReads(fr, RecipeTemplateLibraries{}, "[[", "]]")
	if err != nil {
		t.Fatalf("FeatureRecipeTemplateReads: %v", err)
	}
	for _, want := range []string{
		".values.vm", ".values.wl", ".values.art", ".values.omit", ".values.with", ".values.when",
	} {
		if !slices.Contains(reads, want) {
			t.Errorf("the census misses %s; got %v", want, reads)
		}
	}
	for _, other := range []string{
		".values.local", ".values.provided", ".values.asserted", ".values.source", ".values.mapping",
	} {
		if slices.Contains(reads, other) {
			t.Errorf("the census counts %s, read by a template evaluated against another context; got %v", other, reads)
		}
	}
}

// TestInjectDirectiveReaderAnswersAnUnreadableFeatureRecipeAsReading pins the
// direction the reader errs in where the census cannot be taken: a template that
// does not parse is reported by the render that evaluates it, so the reader answers
// that the FeatureRecipe reads the entries rather than refusing them first under a
// condition that is not the fault.
func TestInjectDirectiveReaderAnswersAnUnreadableFeatureRecipeAsReading(t *testing.T) {
	t.Parallel()
	read := injectDirectiveReader(RecipeTemplateLibraries{}, nil)
	broken := tokenFR(func(fr *types.FeatureRecipe) {
		fr.Spec.Artifacts.Set("x.yaml", types.ArtifactEntry{Inline: `{{ .values.unclosed `})
	})
	if !read(broken) {
		t.Error("an unreadable FeatureRecipe must be answered as reading its entries")
	}
	if read(tokenFR(nil)) {
		t.Error("a FeatureRecipe whose templates read nothing must be answered as not reading its entries")
	}
	if read(nil) {
		t.Error("no FeatureRecipe reads nothing")
	}
}
