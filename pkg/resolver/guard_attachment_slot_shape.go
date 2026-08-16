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

// AttachmentSlotShape is a compiled validator for one Component's attachment-slot
// declarations. Construct it with [CompileAttachmentSlotShape] and apply it with
// [AttachmentSlotShape.Validate].
//
// It is a value, not a function, because construction resolves the whole
// projected schema eagerly while many component instances share one type — so a
// caller walking instances memoises the value per type string rather than
// recompiling. The compiled validator is immutable and safe to reuse across
// instances.
type AttachmentSlotShape struct {
	v jsonvalidator.Validator
}

// CompileAttachmentSlotShape compiles the shape comp's featuresSchema projects,
// wrapping it one level deep under the authored keyword so findings' field paths
// lead with it. Returns ok=false when the entity declares no slots, or when the
// projection does not compile.
//
// A projection that cannot compile is an internal defect in the generator, not
// an authoring error, so it must not be reported against the author's document —
// but the skip is silent, which means such a defect disables this check rather
// than announcing itself. The projection's compilability is therefore pinned by
// a test beside the generator, which is where a regression can be caught before
// it reaches a build.
//
// Exported so the shape of an authored attachment block is validated through one
// implementation wherever the block is found: on a component instance the
// Aggregate declares, and on a component that exists only after sub-aggregate
// flattening. The question is identical; only where the block is read from
// differs.
func CompileAttachmentSlotShape(comp types.Component) (AttachmentSlotShape, bool) {
	projected := validator.GenerateBaseAttachmentsJSONSchema(comp.Spec.FeaturesSchema)
	if projected == nil {
		return AttachmentSlotShape{}, false
	}
	wrapper := &libschema.Schema{
		Type:       "object",
		Properties: map[string]*libschema.Schema{attachmentBlockKey: projected},
	}
	v, err := jsonvalidator.New(wrapper)
	if err != nil {
		return AttachmentSlotShape{}, false
	}
	return AttachmentSlotShape{v: v}, true
}

// Validate checks one authored attachment block against the compiled slot shape,
// emitting one ErrAttachmentSlotShape per violation. entity and componentField
// attribute each finding; componentField leads the field locator so a diagnostic
// reads as the authoring site.
//
// PRESENCE IS NOT THIS VALIDATOR'S QUESTION — whether a slot declared
// `required: true` ends up bound is asked of a node's post-cascade attachment
// set, never of an authored block.
func (s AttachmentSlotShape) Validate(
	authored types.OrderedMap[types.FeatureInstance],
	entity, componentField string,
) []*ResolverError {
	if s.v == nil || authored.Len() == 0 {
		return nil
	}
	return attachmentShapeErrors(s.v, authored, entity, componentField)
}

// ValidateContributedFill checks one attachment contributed at a scope broader
// than the component it lands on — a subsystem-wide fill reaching every component
// beneath it — against the slot of the same name on THAT component, and returns a
// single violation when the contributed delivery is out of shape for it.
//
// The subject is the pair, not the block. One contributed fill can be legal on
// one component in the scope and out of shape on another, because what closes it
// is the slot the receiving Component declares; so the question cannot be asked
// of the authored block, which is well-formed and identical in both cases. It is
// asked once per (component, contributed fill) pair, which is why the caller is
// the site that holds a node's attachment set rather than the Aggregate walk.
//
// authoredLocator must name the scope the author declared the attachment at —
// build it with [ScopeAttachmentFieldPath]. The diagnostic names the component
// and the slot and points at that scope, and deliberately does NOT name the
// delivery block: the block is legal on its own terms, so naming it would send
// the author to correct authored content and invite them to damage it. What is
// wrong is the scope the attachment sits at relative to the slot it reached, so
// the message says to move it down instead.
//
// One violation per pair rather than one per offending mechanism. The author's
// edit is a single move whatever the block carries, and a per-mechanism list
// would enumerate parts of a block this diagnostic exists not to send them into.
//
// Returns nil when the shape is not compiled, when the contribution binds no
// declared slot (a free attachment is unconstrained), or when the contributed
// fill conforms.
func (s AttachmentSlotShape) ValidateContributedFill(
	instanceName string,
	contribution types.FeatureInstance,
	slotDeclared bool,
	entity, component, authoredLocator string,
) *ResolverError {
	if s.v == nil || !slotDeclared {
		return nil
	}
	// Reuse the compiled slot shape rather than re-deciding what a slot permits:
	// the authored-block guard and this one must agree by construction, so the
	// contribution is presented in exactly the shape that guard validates and the
	// findings are used only as a yes/no. Their field paths are discarded on
	// purpose — they lead into the delivery block.
	one := types.OrderedMap[types.FeatureInstance]{}
	one.Set(instanceName, contribution)
	if len(attachmentShapeErrors(s.v, one, entity, "")) == 0 {
		return nil
	}
	return &ResolverError{
		Entity: entity,
		Field:  authoredLocator,
		Message: fmt.Sprintf(
			"this attachment is declared at a scope that reaches component %q, whose attachment slot %q "+
				"does not accept the delivery it carries: move the attachment down to the components that "+
				"may receive it",
			component, instanceName),
		Err: ErrAttachmentSlotShape(),
	}
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
			Err:     ErrAttachmentSlotShape(),
		})
	}
	return errs
}
