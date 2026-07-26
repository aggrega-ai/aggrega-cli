// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// slotShapeCatalog returns a CatalogLookup whose one Component declares a
// Black-Box slot (empty inject allowlist, delivery reserved to the Recipe) and a
// transparent slot allowing env and file.
func slotShapeCatalog() CatalogLookup {
	slots := types.OrderedMap[types.AttachmentSlot]{}
	slots.Set("root_password", types.AttachmentSlot{Required: true, Inject: []string{}})
	slots.Set("user_password", types.AttachmentSlot{Inject: []string{"env", "file"}})
	comp := types.Component{}
	comp.Metadata.Name = "core/datastore/db"
	comp.Spec.FeaturesSchema = slots
	return CatalogLookup{
		Component: func(uri string) (types.Component, bool) {
			if strings.HasPrefix(uri, "core/datastore/db") {
				return comp, true
			}
			return types.Component{}, false
		},
	}
}

// slotShapeAggregate wraps one component instance carrying the supplied
// attachment block.
func slotShapeAggregate(features types.OrderedMap[types.FeatureInstance]) types.NormalizedAggregate {
	inst := types.ComponentInstance{Type: "core/datastore/db:1.0.0", Features: features}
	comps := types.OrderedMap[types.ComponentInstance]{}
	comps.Set("db", inst)
	agg := &types.Aggregate{}
	agg.Metadata.Name = "slot-shape"
	agg.Spec.Components = comps
	return types.Normalize(agg)
}

func fill(t string, injectEnv, injectArg bool, description string) types.FeatureInstance {
	fi := types.FeatureInstance{Type: t, Description: description}
	if injectEnv || injectArg {
		fi.Inject = &types.InjectBlock{}
		entry := []any{map[string]any{"in": "PW", "from": "value"}}
		if injectEnv {
			fi.Inject.Env = entry
		}
		if injectArg {
			fi.Inject.Arg = entry
		}
	}
	return fi
}

func TestGuardAttachmentSlotShape_BlackBoxSlotRejectsAuthoredDelivery(t *testing.T) {
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("root_password", fill("core/secret-value", true, false, ""))

	errs := guardAttachmentSlotShape(slotShapeAggregate(feats), slotShapeCatalog())
	if len(errs) == 0 {
		t.Fatal("a Black-Box slot reserves delivery to the Recipe; an authored inject block must be rejected")
	}
	if !errors.Is(errs[0].Err, ErrAttachmentSlotShape) {
		t.Errorf("sentinel = %v; want ErrAttachmentSlotShape", errs[0].Err)
	}
	if !strings.Contains(errs[0].Field, "features.root_password") {
		t.Errorf("field %q must locate the offending attachment", errs[0].Field)
	}
}

func TestGuardAttachmentSlotShape_MechanismOutsideAllowlistRejected(t *testing.T) {
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("user_password", fill("core/secret-value", false, true, ""))

	errs := guardAttachmentSlotShape(slotShapeAggregate(feats), slotShapeCatalog())
	if len(errs) == 0 {
		t.Fatal("`arg` is not in the slot's [env, file] allowlist; the fill must be rejected")
	}
	if !errors.Is(errs[0].Err, ErrAttachmentSlotShape) {
		t.Errorf("sentinel = %v; want ErrAttachmentSlotShape", errs[0].Err)
	}
}

func TestGuardAttachmentSlotShape_AllowlistedMechanismAccepted(t *testing.T) {
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("user_password", fill("core/secret-value", true, false, ""))

	if errs := guardAttachmentSlotShape(slotShapeAggregate(feats), slotShapeCatalog()); len(errs) != 0 {
		t.Fatalf("`env` is allowlisted; the fill must be accepted; got %v", errs[0])
	}
}

func TestGuardAttachmentSlotShape_DescriptionOnAFillIsLegal(t *testing.T) {
	// The permitted key set mirrors the attachment shape the entity schemas
	// admit. `description` is legal on any attachment, so it must stay legal on
	// a slot fill — a shape check narrower than the envelope would reject
	// authoring the product documents as valid.
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("root_password", fill("core/secret-value", false, false, "why this credential is here"))

	if errs := guardAttachmentSlotShape(slotShapeAggregate(feats), slotShapeCatalog()); len(errs) != 0 {
		t.Fatalf("a documented fill must be accepted; got %v", errs[0])
	}
}

func TestGuardAttachmentSlotShape_FreeAttachmentAdmitted(t *testing.T) {
	// The open outer object is what lets one entity serve both a per-consumer
	// fill and an attachment shared at a broader scope. A name no slot declares
	// is a free attachment and carries no slot constraint — including the inject
	// allowlist, which belongs to the slot and not to the block.
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("some_monitoring", fill("core/prometheus-exporter", false, true, ""))

	if errs := guardAttachmentSlotShape(slotShapeAggregate(feats), slotShapeCatalog()); len(errs) != 0 {
		t.Fatalf("a free attachment must be admitted untouched; got %v", errs[0])
	}
}

func TestGuardAttachmentSlotShape_UnfilledRequiredSlotIsSilentHere(t *testing.T) {
	// Presence is NOT this guard's question, structurally: an attachment
	// contributed at a broader scope binds the slot while leaving this authored
	// block empty, so a presence claim here would reject the shared form. The
	// projection emits no outer required list, and this pins that the guard
	// cannot regain the claim.
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("user_password", fill("core/secret-value", false, false, ""))

	for _, e := range guardAttachmentSlotShape(slotShapeAggregate(feats), slotShapeCatalog()) {
		if strings.Contains(e.Message, "root_password") {
			t.Fatalf("the shape guard reported the unfilled required slot %q: %v", "root_password", e)
		}
	}
}

func TestGuardAttachmentSlotShape_CatalogLessAndSlotLessSkip(t *testing.T) {
	feats := types.OrderedMap[types.FeatureInstance]{}
	feats.Set("root_password", fill("core/secret-value", true, false, ""))
	na := slotShapeAggregate(feats)

	if errs := guardAttachmentSlotShape(na, CatalogLookup{}); errs != nil {
		t.Errorf("a catalog-less path has no slot declarations to check; got %v", errs)
	}

	slotless := CatalogLookup{Component: func(string) (types.Component, bool) {
		return types.Component{}, true
	}}
	if errs := guardAttachmentSlotShape(na, slotless); errs != nil {
		t.Errorf("an entity declaring no slots constrains nothing; got %v", errs)
	}
}
