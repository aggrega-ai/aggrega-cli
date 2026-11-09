// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	libschema "github.com/google/jsonschema-go/jsonschema"

	schemas "github.com/aggrega-ai/aggrega-schemas"

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
// Exported so the shape of an attachment fill is validated through one
// implementation wherever this projection is asked: over the block a component
// that exists only after sub-aggregate flattening authored, and over one fill
// contributed from a broader scope against the slot it lands on. A component the
// root Aggregate declares is not asked it: its authored block answers to the
// attachment envelope, which fixes the keys a fill may carry, and to the
// delivery-mechanism allowlist rule, which fixes the mechanisms its inject block
// may name. The projection refuses no fill those two admit and admits no
// delivery the allowlist rule refuses, and the envelope binds a module's
// document as it binds the root's, so a fill draws one verdict whichever
// instrument measures it.
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
	return attachmentShapeErrors(s.v, authored, entity, "", func(path string) string {
		if path == "" {
			return componentField
		}
		return componentField + "." + path
	})
}

// ValidateAt is [AttachmentSlotShape.Validate] for a caller that positions each
// finding through the locator assembly rather than by a prefix: locate turns the
// path a finding landed on — leading with the attachment block's own key, empty
// for the block as a whole — into its locator, and file names the document that
// locator is a path inside.
func (s AttachmentSlotShape) ValidateAt(
	authored types.OrderedMap[types.FeatureInstance],
	entity, file string,
	locate func(path string) string,
) []*ResolverError {
	if s.v == nil || authored.Len() == 0 {
		return nil
	}
	return attachmentShapeErrors(s.v, authored, entity, file, locate)
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
	if len(attachmentShapeErrors(s.v, one, entity, "", func(string) string { return "" })) == 0 {
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
	entity, file string,
	locate func(path string) string,
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
		field := locate(f.FieldPath)
		msg := f.Message
		if f.Keyword != "" {
			msg = fmt.Sprintf("%s (%s)", f.Message, f.Keyword)
		}
		// Each slot, and the inject block of a slot declaring an allowlist, is a
		// closed position in this projection: its additionalProperties is a
		// not-of-empty sub-schema. The engine recognises that closed form and
		// reports every key the position does not declare under the keyword
		// additionalProperties, one finding at that key's own path, and the
		// projection carries no other additionalProperties. The inject block of
		// a slot whose allowlist is empty admits only the empty block instead,
		// through maxProperties, which the projection carries nowhere else and
		// which the engine reports at the inject key's own path. So within THIS
		// schema both keywords mean one thing: what the key the field path names
		// carries is not part of the slot's declaration — for maxProperties, a
		// delivery block on a slot that permits no delivery — and removing that
		// key is an edit the schema accepts. The engine's own wording names the
		// key, or for maxProperties only a count, and no edit, so say what the
		// author may change.
		if f.Keyword == "additionalProperties" || f.Keyword == "maxProperties" {
			msg = slotDeclarationRemedy(f.FieldPath, authored)
		}
		errs = append(errs, &ResolverError{
			Entity:  entity,
			Field:   field,
			File:    file,
			Message: fmt.Sprintf("attachment does not conform to the declared slot shape: %s", msg),
			Err:     ErrAttachmentSlotShape(),
		})
	}
	return errs
}

// slotDeclarationRemedy words the finding for a key, at path, that the slot's
// declaration does not hold, offering only an edit the Component schema accepts.
//
// The edit on offer depends on where the key sits. A key inside an attachment's
// inject block is a delivery mechanism, and a slot's inject allowlist can name
// only the mechanisms the schema enumerates: a mechanism inside that set may be
// added to the allowlist, while one outside it cannot be declared on any slot,
// so the message names the set and asks for the key's removal. Every other key
// sits at a position the slot's shape fixes, which no declaration on the slot
// widens, so removal is the one edit.
func slotDeclarationRemedy(path string, authored types.OrderedMap[types.FeatureInstance]) string {
	const fault = "this key is not part of the slot's declaration"
	mechanism, isMechanism := injectMechanismAt(path, authored)
	if !isMechanism {
		return fault + " — remove it"
	}
	declarable := slotInjectMechanisms()
	if len(declarable) == 0 {
		return fault + " — remove it"
	}
	if slices.Contains(declarable, mechanism) {
		return fault + " — remove it, or add it to the slot's inject allowlist in the entity's featuresSchema"
	}
	return fault + " — remove it: a slot's inject allowlist in the entity's featuresSchema accepts only " +
		joinAlternatives(declarable)
}

// injectMechanismAt reports the mechanism key path names when path addresses a
// key inside one authored attachment's inject block. The attachment names are
// matched whole, in author order, rather than by splitting path on its
// separator, because an attachment name may itself contain the separator.
func injectMechanismAt(path string, authored types.OrderedMap[types.FeatureInstance]) (string, bool) {
	for name := range authored.All() {
		prefix := attachmentBlockKey + "." + name + ".inject."
		if mechanism, ok := strings.CutPrefix(path, prefix); ok && mechanism != "" {
			return mechanism, true
		}
	}
	return "", false
}

// slotInjectMechanisms is the closed set of delivery mechanisms a slot's inject
// allowlist may name, in the order the Component schema lists them.
//
// It is read from the Component schema the binary embeds — the same document a
// catalog Component is validated against when it loads — so the mechanisms a
// diagnostic invites an author to declare are exactly the ones that validation
// then accepts. Empty when that schema no longer carries the set at the
// position read here, in which case a diagnostic offers removal alone.
var slotInjectMechanisms = sync.OnceValue(func() []string {
	var doc struct {
		Properties struct {
			Spec struct {
				Properties struct {
					FeaturesSchema struct {
						AdditionalProperties struct {
							Properties struct {
								Inject struct {
									Items struct {
										Enum []string `json:"enum"`
									} `json:"items"`
								} `json:"inject"`
							} `json:"properties"`
						} `json:"additionalProperties"`
					} `json:"featuresSchema"`
				} `json:"properties"`
			} `json:"spec"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(schemas.ComponentV1beta1), &doc); err != nil {
		return nil
	}
	return doc.Properties.Spec.Properties.FeaturesSchema.AdditionalProperties.Properties.Inject.Items.Enum
})

// joinAlternatives renders names as an English list: "a", "a and b",
// "a, b and c".
func joinAlternatives(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
