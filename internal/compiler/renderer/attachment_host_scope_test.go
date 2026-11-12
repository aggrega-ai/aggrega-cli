// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"errors"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// attachmentHostScopes is every scope an attachment surface is evaluated or judged
// against, keyed by the variable that holds it. Each takes its rendered-name
// narrowing per host from attachmentHostScope rather than declaring one.
func attachmentHostScopes() map[string]templateScope {
	return map[string]templateScope{
		"featureRecipeLocalsScope": featureRecipeLocalsScope,
		"attachmentFRScope":        attachmentFRScope,
		"attachmentFRRenderScope":  attachmentFRRenderScope,
	}
}

// TestAttachmentHostScope_NarrowsOnlyWhereTheHostReportsNoName pins the per-host
// narrowing on every attachment scope: the key is refused where the host's provider
// reported no name and admitted where it reported one, the derived narrowing names a
// key its context type defines, and deriving it leaves the declared scope as it was.
//
// The last is the property a shared narrowing map would break silently: a scope is
// handed out by value, a value copy shares the map, and a derivation writing into it
// would narrow the surface for every later host — including one whose provider does
// report a name — with nothing failing until that host is rendered.
func TestAttachmentHostScope_NarrowsOnlyWhereTheHostReportsNoName(t *testing.T) {
	t.Parallel()
	for name, scope := range attachmentHostScopes() {
		refused := attachmentHostScope(scope, "")
		keys, gated := refused.admittedKeys(identityContextRoot)
		if !gated {
			t.Fatalf("%s: .context is not a closed pillar here, so the narrowing below certifies nothing", name)
		}
		if slices.Contains(keys, renderedNameContextKey) {
			t.Errorf("%s admits .context.%s on a host whose provider reported no name; it admits %v",
				name, renderedNameContextKey, keys)
		}
		if !slices.Contains(keys, "instance_id") {
			t.Errorf("%s refuses the host's identity on a host reporting no name; it admits %v", name, keys)
		}
		if problems := refused.validate(); len(problems) > 0 {
			t.Errorf("%s: the per-host narrowing names something its context type does not define: %v", name, problems)
		}

		admitted, _ := attachmentHostScope(scope, "my-zukv").admittedKeys(identityContextRoot)
		if !slices.Contains(admitted, renderedNameContextKey) {
			t.Errorf("%s refuses .context.%s on a host whose provider reported a name; it admits %v",
				name, renderedNameContextKey, admitted)
		}
		declared, _ := scope.admittedKeys(identityContextRoot)
		if !slices.Contains(declared, renderedNameContextKey) {
			t.Errorf("%s: deriving the per-host narrowing changed the declared scope, which now admits %v",
				name, declared)
		}
	}
}

// TestTemplateScopeRefusingKey_LeavesTheScopeItNarrowsUntouched pins the copy the
// per-render narrowing makes, on a scope that already declares a narrowing of its
// own — the case where a derivation writing into the shared map would be visible.
// The attachment scopes declare none, so the test above cannot see the difference on
// them; this one can, and it is what holds the property for any scope that declares
// one.
func TestTemplateScopeRefusingKey_LeavesTheScopeItNarrowsUntouched(t *testing.T) {
	t.Parallel()
	before, _ := recipeLocalsScope.admittedKeys(identityContextRoot)
	if !slices.Contains(before, "instance_id") {
		t.Fatalf("the fixture scope does not admit the key the derivation narrows; it admits %v", before)
	}
	derived, _ := recipeLocalsScope.refusingKey(identityContextRoot, "instance_id").admittedKeys(identityContextRoot)
	if slices.Contains(derived, "instance_id") {
		t.Errorf("the derived scope still admits the key it was narrowed by; it admits %v", derived)
	}
	after, _ := recipeLocalsScope.admittedKeys(identityContextRoot)
	if !slices.Equal(before, after) {
		t.Errorf("deriving a narrowing changed the scope it was derived from: it admitted %v and now admits %v",
			before, after)
	}
}

// hostNameFR is a FeatureRecipe whose one local, one provides entry and source each
// read the host's rendered name.
func hostNameFR() *types.FeatureRecipe {
	locals := types.OrderedMap[string]{}
	locals.Set("host_name", "{{ .context.rendered_name }}")
	provides := types.OrderedMap[string]{}
	provides.Set("published", "{{ .context.rendered_name }}-{{ .locals.host_name }}")
	return &types.FeatureRecipe{
		Metadata: types.FeatureRecipeMetadata{Name: "host-name"},
		Spec:     types.FeatureRecipeSpec{Locals: locals, Provides: provides},
	}
}

// TestAttachmentSurfaces_ReadTheHostNameWhereItIsReported drives each surface an
// attachment evaluates once its host's answer is known — its locals on both of
// their paths, its provides, its source — with a host name and without one. With a
// name every surface reads exactly that name; without one every surface halts with
// the reference-not-admitted condition rather than rendering it away.
func TestAttachmentSurfaces_ReadTheHostNameWhereItIsReported(t *testing.T) {
	t.Parallel()
	const hostName = "my-zukv"

	surfaces := []struct {
		name string
		// eval returns what the surface read for the key, given the host's name.
		eval func(hostRenderedName string) (string, error)
		want string
	}{
		{
			name: "locals",
			eval: func(hostRenderedName string) (string, error) {
				got, err := EvaluateFeatureRecipeLocals(hostNameFR(), "creds", "my_kv", hostRenderedName,
					types.OrderedMap[any]{}, types.OrderedMap[any]{}, resolver.SensitiveLeaves{})
				v, _ := got.Get("host_name")
				return v, err
			},
			want: hostName,
		},
		{
			name: "provides, with the locals the provides pass resolves itself",
			eval: func(hostRenderedName string) (string, error) {
				got, err := evalAttachmentFRProvides(hostNameFR(), "creds", "my_kv", hostRenderedName,
					types.IRFeatureEntry{}, resolver.SensitiveLeaves{})
				v, _ := got.Get("published")
				return v, err
			},
			want: hostName + "-" + hostName,
		},
		{
			name: "source",
			eval: func(hostRenderedName string) (string, error) {
				got, err := EvaluateFeatureRecipeSource("./charts/{{ .context.rendered_name }}", "creds", "my_kv",
					hostRenderedName, types.OrderedMap[any]{}, types.OrderedMap[any]{}, types.OrderedMap[string]{}, redact.Keys{})
				s, _ := got.(string)
				return s, err
			},
			want: "./charts/" + hostName,
		},
	}
	for _, surface := range surfaces {
		t.Run(surface.name+" on a host reporting a name", func(t *testing.T) {
			t.Parallel()
			got, err := surface.eval(hostName)
			if err != nil {
				t.Fatalf("the surface refused its host's reported name: %v", err)
			}
			if got != surface.want {
				t.Errorf("the surface read %q, want %q", got, surface.want)
			}
		})
		t.Run(surface.name+" on a host reporting none", func(t *testing.T) {
			t.Parallel()
			got, err := surface.eval("")
			if err == nil {
				t.Fatalf("the surface rendered %q for a name its host's provider did not report", got)
			}
			if !errors.Is(err, ErrTemplateUnknownRoot()) {
				t.Errorf("the refusal does not carry %v: %v", ErrTemplateUnknownRoot(), err)
			}
		})
	}
}
