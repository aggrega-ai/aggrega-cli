// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"fmt"

	libschema "github.com/google/jsonschema-go/jsonschema"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// attachmentBlockKey is the authored keyword the slot shape is validated
// under. It leads every finding's field path, so a diagnostic reads
// ".spec.components.<name>.features.<attachment>.<key>" — the authoring site,
// not a synthetic wrapper path.
const attachmentBlockKey = "features"

// guardAttachmentSlotShape validates each component instance's authored
// attachment block against the shape its Component's featuresSchema projects.
// An attachment whose name binds a declared slot must match that slot's
// sub-schema; an attachment under any other name is a free attachment and is
// admitted untouched.
//
// The openness is the point, not a concession. The projected outer object
// declares no additionalProperties constraint, so wiring it here constrains
// declared fills without closing the block — the property that lets one
// catalog entity serve both a per-consumer fill and an attachment shared at a
// broader scope. A guard that rejected undeclared names would delete that
// capability, which is why the projection's two depths (open parent, closed
// slots) are load-bearing rather than incidental.
//
// What it catches, beyond what the Aggregate's own envelope schema already
// rejects: a delivery mechanism the slot's inject allowlist does not permit,
// and any mechanism at all on a Black-Box slot, whose empty allowlist reserves
// delivery to the Recipe. The mechanism NAMES are the slot's contract; the
// shape of a delivery entry is the entity schema's, and is not re-checked here.
//
// PRESENCE IS NOT THIS GUARD'S QUESTION. Whether a slot declared
// `required: true` ends up bound is asked of the node's attachment set after
// the whole cascade — an attachment contributed at a subsystem, boundary or
// Release scope binds the slot while leaving this authored block empty — so a
// presence check here would reject exactly the shared form the open object
// exists to permit. The projection emits no outer `required` list, so this
// guard cannot make that claim even by accident.
//
// Catalog-less paths (nil catalog.Component) skip entirely, as does a
// component whose type is absent from the catalog view but was not resolved as
// a catalog reference; a resolved-but-absent reference is a loud view
// inconsistency, classified here the way the sibling guards classify it.
// Findings are canonicalised by (Entity, Field) so the emitted set is
// byte-identical across runs of the same input.
func guardAttachmentSlotShape(na types.NormalizedAggregate, catalog CatalogLookup) []*ResolverError {
	agg := na.Aggregate()
	if agg == nil || catalog.Component == nil {
		return nil
	}
	entity := fmt.Sprintf("Aggregate %s", agg.Metadata.Name)

	// Memoise the compiled validator per instance TYPE STRING — many instances
	// share one type and construction resolves the whole schema eagerly. Keyed by
	// the string the caller looked up, not by the resolved entity's name, so two
	// instances pinning different versions of one entity name cannot share a
	// validator built from the other's slot set. Mirrors the compiled-schema
	// cache in the sibling conformance guard.
	type cached struct {
		v  jsonvalidator.Validator
		ok bool
	}
	validators := map[string]cached{}
	validatorFor := func(typeURI string, comp types.Component) (jsonvalidator.Validator, bool) {
		if c, seen := validators[typeURI]; seen {
			return c.v, c.ok
		}
		v, ok := slotShapeValidator(comp.Spec.FeaturesSchema)
		validators[typeURI] = cached{v: v, ok: ok}
		return v, ok
	}

	var errs []*ResolverError
	for _, p := range collectComponentInstances(na) {
		if p.inst.Features.Len() == 0 {
			continue
		}
		componentField := componentFieldPath(p.subsystemPath, p.name, p.synthesized)
		comp, found := catalog.Component(p.inst.Type)
		if !found {
			if verr := guardEntityViewInconsistency(entity, componentField, p.inst.Type); verr != nil {
				errs = append(errs, verr)
			}
			continue
		}
		if comp.Spec.FeaturesSchema.Len() == 0 {
			continue
		}
		v, ok := validatorFor(p.inst.Type, comp)
		if !ok {
			continue
		}
		errs = append(errs, attachmentShapeErrors(v, p.inst.Features, entity, componentField)...)
	}
	sortResolverErrors(errs)
	return errs
}

// slotShapeValidator compiles a validator for one entity's slot declarations,
// wrapping the projected schema one level deep under the authored keyword so
// findings' field paths lead with it. Returns ok=false when the entity declares
// no slots, or when the projection does not compile.
//
// A projection that cannot compile is an internal defect in the generator, not
// an authoring error, so it must not be reported against the author's document —
// but the skip is silent, which means such a defect disables this check rather
// than announcing itself. The projection's compilability is therefore pinned by
// a test beside the generator, which is where a regression can be caught before
// it reaches a build.
func slotShapeValidator(slots types.OrderedMap[types.AttachmentSlot]) (jsonvalidator.Validator, bool) {
	projected := validator.GenerateBaseAttachmentsJSONSchema(slots)
	if projected == nil {
		return nil, false
	}
	wrapper := &libschema.Schema{
		Type:       "object",
		Properties: map[string]*libschema.Schema{attachmentBlockKey: projected},
	}
	v, err := jsonvalidator.New(wrapper)
	if err != nil {
		return nil, false
	}
	return v, true
}

// attachmentShapeErrors validates one authored attachment block and maps the
// findings to ResolverErrors wrapping ErrAttachmentSlotShape.
//
// The block is round-tripped through JSON so the validator sees plain maps and
// slices rather than the typed OrderedMap; a block that does not marshal is not
// a shape violation and yields no findings. Only leaf-most findings are kept —
// the validator reports a failure at every level along a path — so one
// violation maps to one error while independent violations stay distinct.
func attachmentShapeErrors(
	v jsonvalidator.Validator,
	authored types.OrderedMap[types.FeatureInstance],
	entity, componentField string,
) []*ResolverError {
	raw, err := json.Marshal(authored)
	if err != nil {
		return nil
	}
	var block any
	if err := json.Unmarshal(raw, &block); err != nil {
		return nil
	}
	wrapped := map[string]any{attachmentBlockKey: block}

	// SkipPredicates: the projected schema carries no x-aggrega predicates, so
	// there is no parameter context to supply and nothing to evaluate.
	rpt, err := v.Validate(wrapped, &jsonvalidator.Context{Surround: wrapped, SkipPredicates: true})
	if err != nil || rpt == nil || len(rpt.Findings) == 0 {
		return nil
	}

	leaves := leafmostFindings(rpt.Findings)
	errs := make([]*ResolverError, 0, len(leaves))
	for _, f := range leaves {
		field := componentField
		if f.FieldPath != "" {
			field = componentField + "." + f.FieldPath
		}
		msg := f.Message
		if f.Keyword != "" {
			msg = fmt.Sprintf("%s (%s)", f.Message, f.Keyword)
		}
		// Every closed position in this projection is expressed as a
		// not-of-empty sub-schema, so within THIS schema a `not` failure means
		// exactly one thing: the key the field path names is not part of the
		// slot's declaration. The engine's own wording for that keyword names
		// the mechanism rather than the fault, and the field path already
		// carries the offending key, so say what the author has to change.
		if f.Keyword == "not" {
			msg = "this key is not part of the slot's declaration — remove it, or declare it on the slot in the entity's featuresSchema"
		}
		errs = append(errs, &ResolverError{
			Entity:  entity,
			Field:   field,
			Message: fmt.Sprintf("attachment does not conform to the declared slot shape: %s", msg),
			Err:     ErrAttachmentSlotShape,
		})
	}
	return errs
}
