// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Tests for the delivery route a FeatureRecipe's own injectMapping opens.
//
// This route is the one a slot declaring no delivery of its own travels: the
// attachment carries no inject mapping, so the reading that walks that mapping has
// nothing to walk, and the delivered value reached the display in the clear. Every
// assertion here is PAIRED with a control of the same shape whose leaf is not
// declared, because a marking that fired on everything would satisfy each positive
// assertion on its own.
//
// The reference reading itself is the shared one, whose per-root and per-depth
// matrix lives beside it; what these tests own is the attribution — which delivery
// keys of which attachment a reading result applies to.

package renderer

import (
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// secretValueLeaves is what the credential Feature declares: the secret itself is
// declared, and the variable name it is delivered under deliberately is not — a
// name is a locator for the material, not the material.
func secretValueLeaves() resolver.SensitiveLeaves {
	return resolver.SensitiveLeaves{Values: map[string]bool{"origin": true}}
}

func leavesFor(per map[string]resolver.SensitiveLeaves) func(string) resolver.SensitiveLeaves {
	return func(instance string) resolver.SensitiveLeaves { return per[instance] }
}

// TestInjectMappingWithheld_MarksTheRenderedKeyTheAuthoredTextCannotName is the
// load-bearing case, and the constraint it turns on is why this route needs a
// reading of its own.
//
// The shipped credential mapping is
//
//	env:
//	  {{ .values.key }}: "{{ .values.origin }}"
//
// so the DELIVERY KEY IS ITSELF A TEMPLATE: the authored text names neither the key
// nor the value, and MARIADB_ROOT_PASSWORD exists only after the mapping has been
// evaluated. Attribution therefore has to run over the rendered overlay, and a
// design that tried to match authored keys against declarations would mark nothing
// at all on the shape the standard library actually ships.
func TestInjectMappingWithheld_MarksTheRenderedKeyTheAuthoredTextCannotName(t *testing.T) {
	t.Parallel()
	fr := dotenvSecretFR()
	delivered := []InjectMappingDelivery{{
		InstanceName: "root-password",
		Mapping:      fr.Spec.InjectMapping,
		Keys:         redact.DeliveryKeys{"env": {"MARIADB_ROOT_PASSWORD": true}},
	}}

	marked := injectMappingWithheld(delivered, leavesFor(map[string]resolver.SensitiveLeaves{
		"root-password": secretValueLeaves(),
	}))

	if !marked["env"]["MARIADB_ROOT_PASSWORD"] {
		t.Errorf("the rendered delivery key must be withheld; got %v", marked)
	}
	if marked["env"]["SOME_OTHER_KEY"] {
		t.Errorf("a key the overlay did not produce must stay absent; got %v", marked)
	}
	if marked["file"]["MARIADB_ROOT_PASSWORD"] {
		t.Errorf("a marking must not cross into another mechanism; got %v", marked)
	}
}

// TestInjectMappingWithheld_ControlsPerReason is the negative half, run per REASON
// the answer can be no rather than on one representative — a clause dropped from
// one guard leaves every other case passing.
func TestInjectMappingWithheld_ControlsPerReason(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		mapping string
		leaves  resolver.SensitiveLeaves
	}{
		{
			name:    "the Feature declares nothing sensitive",
			mapping: "env:\n  {{ .values.key }}: \"{{ .values.origin }}\"\n",
			leaves:  resolver.SensitiveLeaves{},
		},
		{
			name:    "the mapping reads only leaves nothing declares",
			mapping: "env:\n  ZZ_LOCATOR: \"{{ .values.key }}\"\n",
			leaves:  secretValueLeaves(),
		},
		{
			name:    "the mapping reads no pillar at all",
			mapping: "env:\n  ZZ_FIXED: \"literal\"\n",
			leaves:  secretValueLeaves(),
		},
		{
			name:    "the declaration is on the other pillar",
			mapping: "env:\n  ZZ_TUNING: \"{{ .infra.origin }}\"\n",
			leaves:  secretValueLeaves(),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			marked := injectMappingWithheld(
				[]InjectMappingDelivery{{
					InstanceName: "credentials",
					Mapping:      tc.mapping,
					Keys:         redact.DeliveryKeys{"env": {"ZZ_KEY": true}},
				}},
				leavesFor(map[string]resolver.SensitiveLeaves{"credentials": tc.leaves}))
			if len(marked) != 0 {
				t.Errorf("nothing must be withheld here; got %v", marked)
			}
		})
	}
}

// TestInjectMappingWithheld_ReadsThroughADefensiveWrapper pins the direction of the
// reference reading on the idiom an author most often writes for an optional key.
// The wrapper's trailing expression hangs off the wrapper's result rather than off
// the context root, so a reading that attributed only root-anchored references
// would answer "reads nothing declared" for a mapping that plainly reads the
// credential. The control is the same wrapper over a leaf nothing declares.
func TestInjectMappingWithheld_ReadsThroughADefensiveWrapper(t *testing.T) {
	t.Parallel()
	per := map[string]resolver.SensitiveLeaves{"credentials": secretValueLeaves()}
	withheld := injectMappingWithheld([]InjectMappingDelivery{{
		InstanceName: "credentials",
		Mapping:      "env:\n  ZZ_SECRET: \"{{ default \"\" .values.origin }}\"\n",
		Keys:         redact.DeliveryKeys{"env": {"ZZ_SECRET": true}},
	}}, leavesFor(per))
	if !withheld["env"]["ZZ_SECRET"] {
		t.Errorf("a wrapped read of the declared leaf must still withhold; got %v", withheld)
	}

	control := injectMappingWithheld([]InjectMappingDelivery{{
		InstanceName: "credentials",
		Mapping:      "env:\n  ZZ_LOCATOR: \"{{ default \"\" .values.key }}\"\n",
		Keys:         redact.DeliveryKeys{"env": {"ZZ_LOCATOR": true}},
	}}, leavesFor(per))
	if len(control) != 0 {
		t.Errorf("the same wrapper over an undeclared leaf must withhold nothing; got %v", control)
	}
}

// TestInjectMappingWithheld_CoversEveryKeyOfTheMappingThatRead is the executable
// record of a stated design cost, not an accident of implementation.
//
// Attribution is per ATTACHMENT rather than per key, because a mapping's keys and
// values are both templates and nothing in the rendered overlay links a key back to
// the part of the text that produced it. So a mapping that reads the credential
// anywhere withholds every key it delivered — including a public locator delivered
// alongside. That over-collects, which is the fail-safe direction; the alternative
// that would attribute per key was weighed and refused, and the reason is in the
// decision record.
//
// The two keys are deliberately named so their sorted order is the reverse of their
// authored order, so a marking that silently keyed off position rather than name
// would show up here.
func TestInjectMappingWithheld_CoversEveryKeyOfTheMappingThatRead(t *testing.T) {
	t.Parallel()
	marked := injectMappingWithheld([]InjectMappingDelivery{{
		InstanceName: "credentials",
		Mapping:      "env:\n  ZZ_SECRET: \"{{ .values.origin }}\"\n  AA_LOCATOR: \"{{ .values.key }}\"\n",
		Keys:         redact.DeliveryKeys{"env": {"ZZ_SECRET": true, "AA_LOCATOR": true}},
	}}, leavesFor(map[string]resolver.SensitiveLeaves{"credentials": secretValueLeaves()}))

	for _, key := range []string{"ZZ_SECRET", "AA_LOCATOR"} {
		if !marked["env"][key] {
			t.Errorf("every key the mapping delivered must be withheld, including %q; got %v", key, marked)
		}
	}
}

// TestInjectMappingWithheld_IsPerAttachment pins the scope of one reading result:
// two attachments deliver into the SAME pipe, and only the one whose mapping reads a
// declared leaf has its keys withheld. Without this the route would be a per-node
// switch, which would blank a second credential's public locator on the strength of
// the first credential's declaration.
func TestInjectMappingWithheld_IsPerAttachment(t *testing.T) {
	t.Parallel()
	marked := injectMappingWithheld([]InjectMappingDelivery{
		{
			InstanceName: "root-password",
			Mapping:      "env:\n  {{ .values.key }}: \"{{ .values.origin }}\"\n",
			Keys:         redact.DeliveryKeys{"env": {"MARIADB_ROOT_PASSWORD": true}},
		},
		{
			InstanceName: "tuning",
			Mapping:      "env:\n  AA_POOL: \"{{ .values.size }}\"\n",
			Keys:         redact.DeliveryKeys{"env": {"AA_POOL": true}},
		},
	}, leavesFor(map[string]resolver.SensitiveLeaves{
		"root-password": secretValueLeaves(),
		"tuning":        {},
	}))

	if !marked["env"]["MARIADB_ROOT_PASSWORD"] {
		t.Errorf("the credential attachment's key must be withheld; got %v", marked)
	}
	if marked["env"]["AA_POOL"] {
		t.Errorf("another attachment's key must not be withheld by this one's declaration; got %v", marked)
	}
}

// TestOverlayDeliveryKeys_NamesWhatTheMergeApplies pins the census the attribution
// runs over. It has to name exactly the keys the merge puts on the pipe: a key it
// missed prints in the clear, and a key it invented withholds something that is not
// there. The union view is excluded because it is rebuilt from the typed mechanisms
// after the merge rather than merged from the overlay, so a typed marking already
// covers the same datum where the union displays it.
func TestOverlayDeliveryKeys_NamesWhatTheMergeApplies(t *testing.T) {
	t.Parallel()
	additional := types.OrderedMap[types.OrderedMap[string]]{}
	additional.Set("vault", omStrInject("TOKEN", "t0ken"))
	overlay := &types.InjectPipe{
		Env:        omStrInject("ZZ_ENV", "1"),
		File:       omStrInject("ZZ_FILE", "2"),
		Arg:        omStrInject("ZZ_ARG", "3"),
		All:        omStrInject("ZZ_UNION_ONLY", "4"),
		Additional: additional,
	}

	got := overlayDeliveryKeys(overlay)

	for mechanism, key := range map[string]string{
		"env":   "ZZ_ENV",
		"file":  "ZZ_FILE",
		"arg":   "ZZ_ARG",
		"vault": "TOKEN",
	} {
		if !got[mechanism][key] {
			t.Errorf("mechanism %q key %q must be named; got %v", mechanism, key, got)
		}
	}
	if _, present := got["all"]; present {
		t.Errorf("the union view is not merged from the overlay and must not be named; got %v", got)
	}
	if overlayDeliveryKeys(nil) != nil {
		t.Error("no overlay names no key")
	}
}

// TestPhaseA_ReportedDeliveryReachesTheNodeKeySet is the plumbing assertion, and it
// is the one that would have caught this family's third gap.
//
// It runs the real Phase A over the shipped credential FeatureRecipe shape, takes
// the report Phase A returns, reads it, and folds the result through the ONE
// assembler — so the chain from "the mapping rendered a key" to "the display
// withholds that key" is exercised end to end rather than at each end separately.
// The control is the same chain with the leaf undeclared.
func TestPhaseA_ReportedDeliveryReachesTheNodeKeySet(t *testing.T) {
	t.Parallel()
	assemble := func(leaves resolver.SensitiveLeaves) redact.Keys {
		ctx := componentInjectCtxWithAttachment("root-password",
			secretValues("MARIADB_ROOT_PASSWORD", "dev-root-pw-change-me"))
		delivered, err := ExecutePhaseA(ctx, []FeatureRecipeRef{
			{InstanceName: "root-password", FeatureRecipe: dotenvSecretFR()},
		}, hostWithheld{})

		if err != nil {
			t.Fatalf("ExecutePhaseA: %v", err)
		}
		if len(delivered) != 1 {
			t.Fatalf("Phase A must report the one mapping that delivered; got %d", len(delivered))
		}
		if !delivered[0].Keys["env"]["MARIADB_ROOT_PASSWORD"] {
			t.Fatalf("the report must name the rendered delivery key; got %v", delivered[0].Keys)
		}
		return redact.NodeKeys(nil, nil, types.OrderedMap[types.IRFeatureEntry]{}, redact.CrossNode{},
			injectMappingWithheld(delivered, leavesFor(map[string]resolver.SensitiveLeaves{"root-password": leaves})),
			nil, nil, redact.Derived{})
	}

	if !assemble(secretValueLeaves()).Delivery("env", "MARIADB_ROOT_PASSWORD") {
		t.Error("a declared credential delivered by the FeatureRecipe must be withheld on the node's set")
	}
	if !assemble(resolver.SensitiveLeaves{}).Empty() {
		t.Error("with nothing declared the same chain must produce an empty set, so a build declaring nothing pays nothing")
	}
}
