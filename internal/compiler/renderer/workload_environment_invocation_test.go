// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// The rule over the environment names a Workload's containers declare is invoked
// once per Workload each owner renders, and this file drives a real build over all
// three owners and reads what each value was invoked over.
//
// A rule invoked zero times raises nothing, which is what a rule that ran and
// approved also does, and every outcome proof over the same build stays green. So
// the invocation itself is asserted, by identity: which value, over which owner,
// for which field, positioned at which document.
//
// It drives the four-site fixture the artifact-pillar owner proofs use, with two
// changes made here rather than there, so the proofs reading that fixture are
// untouched: the host component's Recipe renders a Workload instead of a source,
// and every document is renamed under a prefix no other fixture in this package
// uses. The recorder replaces package-level values, so a render running in another
// test would be recorded too; filtering by that prefix makes a foreign render
// unable to change any assertion here.

// envOwnerPrefix opens every document name the fixture below renders, and is what
// the recorder filters by.
const envOwnerPrefix = "env-owner-"

// The three declared values this file records, named so a record says which was
// invoked rather than only how often.
const (
	envRuleNodesOwn         = "node's own Recipe"
	envRuleNodeAttachment   = "attachment on a node"
	envRuleBoundaryAttached = "boundary-scope attachment"
)

// envInvocation is one invocation of one of the three values, in invocation order.
type envInvocation struct {
	// rule names which of the three values was invoked.
	rule string

	// owner is the node id for the two node-rooted values and the boundary's scope
	// path for the boundary-rooted one.
	owner string

	// attachment is empty for the value declared over a node's own Recipe.
	attachment string

	// field is the template field the value was invoked over.
	field string

	// file is the document the subject is positioned at.
	file string
}

// recordWorkloadEnvironmentInvocations replaces all three declared values with
// wrappers that record every subject the production path invokes them over, and
// restores the declarations when the test ends.
//
// Each wrapper DELEGATES rather than substituting a verdict, so the build under
// observation behaves exactly as it does unobserved.
//
// The caller must not run in parallel: it replaces package-level declarations.
func recordWorkloadEnvironmentInvocations(t *testing.T) func() []envInvocation {
	t.Helper()

	var (
		mu   sync.Mutex
		seen []envInvocation
	)
	add := func(r envInvocation) {
		mu.Lock()
		seen = append(seen, r)
		mu.Unlock()
	}

	own := WorkloadEnvironmentVariableNamedOnce
	onNode := AttachmentWorkloadEnvironmentVariableNamedOnce
	onBoundary := BoundaryAttachmentWorkloadEnvironmentVariableNamedOnce

	WorkloadEnvironmentVariableNamedOnce = constraint.Check[
		constraint.HydratedView, constraint.NodeTemplateFieldSubject,
	]{
		Sentinel: own.Sentinel,
		Severity: own.Severity,
		Run: func(v constraint.HydratedView, s constraint.NodeTemplateFieldSubject) []constraint.Objection {
			add(envInvocation{
				rule: envRuleNodesOwn, owner: s.NodeID(), field: s.TemplateField(), file: s.SourceFile(),
			})
			return own.Run(v, s)
		},
	}
	AttachmentWorkloadEnvironmentVariableNamedOnce = constraint.Check[
		constraint.HydratedView, constraint.NodeAttachmentTemplateFieldSubject,
	]{
		Sentinel: onNode.Sentinel,
		Severity: onNode.Severity,
		Run: func(v constraint.HydratedView, s constraint.NodeAttachmentTemplateFieldSubject) []constraint.Objection {
			add(envInvocation{
				rule: envRuleNodeAttachment, owner: s.NodeID(), attachment: s.AttachmentName(),
				field: s.TemplateField(), file: s.SourceFile(),
			})
			return onNode.Run(v, s)
		},
	}
	BoundaryAttachmentWorkloadEnvironmentVariableNamedOnce = constraint.Check[
		constraint.HydratedView, constraint.BoundaryAttachmentTemplateFieldSubject,
	]{
		Sentinel: onBoundary.Sentinel,
		Severity: onBoundary.Severity,
		Run: func(v constraint.HydratedView, s constraint.BoundaryAttachmentTemplateFieldSubject) []constraint.Objection {
			add(envInvocation{
				rule: envRuleBoundaryAttached, owner: s.BoundaryPath(), attachment: s.AttachmentName(),
				field: s.TemplateField(), file: s.SourceFile(),
			})
			return onBoundary.Run(v, s)
		},
	}
	t.Cleanup(func() {
		WorkloadEnvironmentVariableNamedOnce = own
		AttachmentWorkloadEnvironmentVariableNamedOnce = onNode
		BoundaryAttachmentWorkloadEnvironmentVariableNamedOnce = onBoundary
	})

	return func() []envInvocation {
		mu.Lock()
		defer mu.Unlock()
		out := make([]envInvocation, 0, len(seen))
		for _, r := range seen {
			if strings.HasPrefix(r.file, envOwnerPrefix) {
				out = append(out, r)
			}
		}
		return out
	}
}

// envOwnerProvider is the fixture's provider, synthesizing a Workload into a file
// named for its own capability so no two sites' synthesized files share a name.
type envOwnerProvider struct {
	stubArtifactsEmitter
	capability string
}

func (p *envOwnerProvider) SynthesizeWorkload(
	_ context.Context, _ *types.TemplateContext, _ *types.Workload, _ NodeRenderContext,
) ([]EmittedFile, error) {
	return []EmittedFile{{
		Name:   envOwnerPrefix + "synth-" + p.capability + ".yaml",
		Origin: EmitOriginWorkload,
		Body:   []byte("kind: Synthesized\n"),
	}}, nil
}

// envOwnerFactory resolves each capability the four-site fixture declares to a
// provider carrying that capability's own delimiter pair.
func envOwnerFactory() EngineFactory {
	return func(capability string) (Provider, error) {
		pair, ok := ownerDelimiters[capability]
		if !ok {
			return nil, errors.New("no provider for that capability")
		}
		p := &envOwnerProvider{capability: capability}
		p.left, p.right = pair[0], pair[1]
		return p, nil
	}
}

// envOwnerLookup binds the four-site fixture with the host component's Recipe
// rendering a Workload instead of a source, and every document renamed under
// envOwnerPrefix. The two node-carried attachments keep their pillars, so one of
// them ("mon") renders a Workload and the other ("logs") renders none; the
// boundary-scope attachment renders one.
func envOwnerLookup() RecipeLookup {
	base := ownerLookup()
	return RecipeLookup{
		Recipe: func(instanceName, componentType string) (*types.Recipe, bool) {
			r, ok := base.Recipe(instanceName, componentType)
			if !ok {
				return nil, false
			}
			r.Metadata.Name = envOwnerPrefix + "host"
			r.Spec.Source = nil
			r.Spec.Workload = types.WorkloadTemplate{Inline: ownerWorkload}
			return r, true
		},
		FeatureRecipe: func(instanceName, featureType string) (*types.FeatureRecipe, bool) {
			fr, ok := base.FeatureRecipe(instanceName, featureType)
			if !ok {
				return nil, false
			}
			fr.Metadata.Name = envOwnerPrefix + fr.Metadata.Name
			return fr, true
		},
	}
}

// envInvocationsOf narrows a record to one of the three values, keeping
// invocation order.
func envInvocationsOf(got []envInvocation, rule string) []envInvocation {
	out := make([]envInvocation, 0, len(got))
	for _, r := range got {
		if r.rule == rule {
			out = append(out, r)
		}
	}
	return out
}

// TestTheWorkloadEnvironmentRuleIsInvokedOncePerWorkloadEachOwnerRenders is the
// enumeration assertion for the production invocation on all three owners, over
// one build reaching all of them.
//
// It asserts what each value was invoked over, by identity: the owner key, the
// Workload field, and the document the subject is positioned at. Every key here is
// built from strings the render holds under other names, so a value invoked over
// the wrong owner yields a record of the right size, and only the named identities
// can tell the two apart. The node-carried attachment rendering no Workload is
// required to produce no invocation, and the boundary-scope attachment, which also
// appears in the node's merged attachment set, is required to be reached through
// its own value only.
//
// It does not run in parallel: it replaces package-level declarations.
func TestTheWorkloadEnvironmentRuleIsInvokedOncePerWorkloadEachOwnerRenders(t *testing.T) {
	recorded := recordWorkloadEnvironmentInvocations(t)

	res, err := RenderAll(context.Background(), ownerInput(), envOwnerLookup(), envOwnerFactory())
	if err != nil {
		t.Fatalf("the admitting build did not complete, so nothing below is attributable to the "+
			"invocation: %+v", err)
	}
	if res.HydratedView == nil {
		t.Fatal("the render published no mid-render view; every assertion below would be unreachable " +
			"for a reason unrelated to the invocation")
	}

	got := recorded()

	// ANTI-NEEDLE, per owner: a build rendering a Workload at all three must invoke
	// all three values.
	for _, rule := range []string{envRuleNodesOwn, envRuleNodeAttachment, envRuleBoundaryAttached} {
		if len(envInvocationsOf(got, rule)) == 0 {
			t.Fatalf("the value declared over the %s was invoked ZERO times over a build that renders "+
				"a Workload there; a rule invoked zero times is indistinguishable from one that approved", rule)
		}
	}

	wantOwn := []envInvocation{{
		rule: envRuleNodesOwn, owner: ownerNode, field: recipeFieldWorkload, file: envOwnerPrefix + "host",
	}}
	if diff := envInvocationsOf(got, envRuleNodesOwn); !slices.Equal(diff, wantOwn) {
		t.Errorf("the node's own value was invoked over %+v; want exactly %+v", diff, wantOwn)
	}

	wantOnNode := []envInvocation{{
		rule: envRuleNodeAttachment, owner: ownerNode, attachment: ownerMonInstance,
		field: recipeFieldWorkload, file: envOwnerPrefix + "mon-fr",
	}}
	if diff := envInvocationsOf(got, envRuleNodeAttachment); !slices.Equal(diff, wantOnNode) {
		t.Errorf("the node-carried attachment value was invoked over %+v; want exactly %+v — once for the "+
			"attachment rendering a Workload, never for the one rendering none, never for the boundary-scope one",
			diff, wantOnNode)
	}

	wantOnBoundary := []envInvocation{{
		rule: envRuleBoundaryAttached, owner: ownerBoundaryPath, attachment: ownerBoundaryInstance,
		field: recipeFieldWorkload, file: envOwnerPrefix + "bridge-fr",
	}}
	if diff := envInvocationsOf(got, envRuleBoundaryAttached); !slices.Equal(diff, wantOnBoundary) {
		t.Errorf("the boundary-scope attachment value was invoked over %+v; want exactly %+v",
			diff, wantOnBoundary)
	}

	// The facts each value read are the ones the render recorded under its own
	// owner: the three Workloads each declare one main container.
	view := res.HydratedView
	for _, owner := range []struct {
		name       string
		containers []constraint.ContainerEnvironmentNames
	}{
		{"the node's own", view.ParsedNodeWorkloadEnvironmentNames(ownerNode)},
		{"the node-carried mon", view.AttachmentParsedWorkloadEnvironmentNames(ownerNode, ownerMonInstance)},
		{"the boundary-scope", view.BoundaryAttachmentParsedWorkloadEnvironmentNames(ownerBoundaryPath, ownerBoundaryInstance)},
	} {
		if c := owner.containers; len(c) != 1 || c[0].Init || c[0].Name != "main" {
			t.Errorf("%s Workload's recorded containers are %+v; want the one main container", owner.name, c)
		}
	}
	if got := view.AttachmentParsedWorkloadEnvironmentNames(ownerNode, ownerLogsInstance); got != nil {
		t.Errorf("the attachment rendering no Workload has recorded containers %+v; want none", got)
	}
}
