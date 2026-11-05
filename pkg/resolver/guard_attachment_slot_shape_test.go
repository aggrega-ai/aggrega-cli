// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// TestAttachmentSlotShape_UndeclaredKeyRemedyIsOneTheSchemaAccepts pins the
// remedy the slot-shape halt offers for a key the slot's declaration does not
// hold. A slot's inject allowlist can name only the mechanisms the Component
// schema enumerates, so a mechanism outside that set must not be met with an
// invitation to declare it on the slot: following that advice produces a
// Component that fails validation when it loads. Each row carries one fault and
// asserts the whole message, so a row passes only on its own wording.
func TestAttachmentSlotShape_UndeclaredKeyRemedyIsOneTheSchemaAccepts(t *testing.T) {
	t.Parallel()

	declarable := slotInjectMechanisms()
	if len(declarable) == 0 {
		t.Fatal("the Component schema's slot inject set read empty; every mechanism remedy would fall back to removal alone")
	}
	if slices.Contains(declarable, "sidecar") || !slices.Contains(declarable, "file") {
		t.Fatalf("the rows below assume sidecar is outside the slot inject set and file inside it; the set reads %v", declarable)
	}

	comp := types.Component{Spec: types.ComponentSpec{FeaturesSchema: orderedSlots(
		"token", types.AttachmentSlot{Inject: []string{"env"}},
		"sealed", types.AttachmentSlot{Inject: []string{}},
	)}}
	shape, ok := CompileAttachmentSlotShape(comp)
	if !ok {
		t.Fatal("the slot shape must compile for a Component declaring slots")
	}

	const prefix = "attachment does not conform to the declared slot shape: this key is not part of the slot's declaration"
	for _, tc := range []struct {
		name       string
		attachment string
		inject     *types.InjectBlock
		wantField  string
		wantMsg    string
	}{
		{
			name:       "a mechanism no slot can declare is answered with removal and the set a slot accepts",
			attachment: "token",
			inject:     injectWith("sidecar"),
			wantField:  "web.features.token.inject.sidecar",
			wantMsg: prefix + " — remove it: a slot's inject allowlist in the entity's featuresSchema accepts only " +
				joinAlternatives(declarable),
		},
		{
			name:       "a mechanism a slot can declare may be added to the slot's allowlist",
			attachment: "token",
			inject:     &types.InjectBlock{File: []any{map[string]any{"in": "/run/token", "from": "value"}}},
			wantField:  "web.features.token.inject.file",
			wantMsg:    prefix + " — remove it, or add it to the slot's inject allowlist in the entity's featuresSchema",
		},
		{
			name:       "a delivery block on a slot whose allowlist is empty is answered with removal alone",
			attachment: "sealed",
			inject:     &types.InjectBlock{Env: []any{map[string]any{"in": "PW", "from": "value"}}},
			wantField:  "web.features.sealed.inject",
			wantMsg:    prefix + " — remove it",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			authored := types.OrderedMap[types.FeatureInstance]{}
			authored.Set(tc.attachment, types.FeatureInstance{Type: "acme/secret", Inject: tc.inject})

			errs := shape.Validate(authored, "app", "web")
			if len(errs) != 1 {
				t.Fatalf("want exactly one finding; got %d: %v", len(errs), errs)
			}
			got := errs[0]
			if !errors.Is(got.Err, ErrAttachmentSlotShape()) {
				t.Errorf("want ErrAttachmentSlotShape; got %v", got.Err)
			}
			if got.Field != tc.wantField {
				t.Errorf("field: want %q; got %q", tc.wantField, got.Field)
			}
			if got.Message != tc.wantMsg {
				t.Errorf("message:\nwant %q\n got %q", tc.wantMsg, got.Message)
			}
			if strings.Contains(got.Message, "declare it on the slot") {
				t.Errorf("the message must not invite declaring the key on the slot; got %q", got.Message)
			}
		})
	}
}

// TestAttachmentSlotShape_EmptyInjectBlockOnABlackBoxSlotConforms pins the
// empty block's verdict on a slot whose allowlist is empty, through both entry
// points the compiled shape offers: an authored block, and one fill contributed
// from a broader scope. An empty block dictates no delivery, so it says nothing
// the slot forbids, and the delivery-mechanism allowlist rule a root component's
// own block answers to admits it too. The mechanism-carrying rows are the
// counter-case that keeps the empty row from passing on a shape that admits
// everything.
func TestAttachmentSlotShape_EmptyInjectBlockOnABlackBoxSlotConforms(t *testing.T) {
	t.Parallel()

	comp := types.Component{Spec: types.ComponentSpec{FeaturesSchema: orderedSlots(
		"sealed", types.AttachmentSlot{Inject: []string{}},
	)}}
	shape, ok := CompileAttachmentSlotShape(comp)
	if !ok {
		t.Fatal("the slot shape must compile for a Component declaring slots")
	}

	for _, tc := range []struct {
		name    string
		inject  *types.InjectBlock
		conform bool
	}{
		{name: "an empty block conforms", inject: &types.InjectBlock{}, conform: true},
		{name: "no block conforms", inject: nil, conform: true},
		{name: "a core mechanism does not", inject: &types.InjectBlock{Env: []any{map[string]any{"in": "PW", "from": "value"}}}},
		{name: "a custom mechanism does not", inject: injectWith("sidecar")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fill := types.FeatureInstance{Type: "acme/secret", Inject: tc.inject}
			authored := types.OrderedMap[types.FeatureInstance]{}
			authored.Set("sealed", fill)

			errs := shape.Validate(authored, "app", "web")
			contributed := shape.ValidateContributedFill("sealed", fill, true, "app", "web", ".spec.subsystems.svc.features.sealed")
			if tc.conform {
				if len(errs) != 0 {
					t.Errorf("authored block: want no finding; got %v", errs)
				}
				if contributed != nil {
					t.Errorf("contributed fill: want no finding; got %v", contributed)
				}
				return
			}
			if len(errs) != 1 || errs[0].Field != "web.features.sealed.inject" {
				t.Errorf("authored block: want one finding at web.features.sealed.inject; got %v", errs)
			}
			if contributed == nil {
				t.Error("contributed fill: want a finding; got none")
			}
		})
	}
}

// TestJoinAlternatives pins the English list form the remedy names the
// declarable mechanisms in.
func TestJoinAlternatives(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		in   []string
		want string
	}{
		{in: nil, want: ""},
		{in: []string{"a"}, want: "a"},
		{in: []string{"a", "b"}, want: "a and b"},
		{in: []string{"a", "b", "c"}, want: "a, b and c"},
	} {
		if got := joinAlternatives(tc.in); got != tc.want {
			t.Errorf("joinAlternatives(%q): want %q; got %q", tc.in, tc.want, got)
		}
	}
}

func orderedSlots(pairs ...any) types.OrderedMap[types.AttachmentSlot] {
	out := types.OrderedMap[types.AttachmentSlot]{}
	for i := 0; i < len(pairs); i += 2 {
		out.Set(pairs[i].(string), pairs[i+1].(types.AttachmentSlot))
	}
	return out
}

func injectWith(mechanism string) *types.InjectBlock {
	b := &types.InjectBlock{}
	b.Additional.Set(mechanism, []any{map[string]any{"in": "PROBE", "from": "value"}})
	return b
}
