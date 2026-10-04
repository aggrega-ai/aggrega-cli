// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// FeatureRecipeRef pairs an attachment instance name with its resolved
// FeatureRecipe.
//
// A ref participates in Phase A by having its FeatureRecipe's injectMapping
// evaluated against an attachment-scoped context whose .values/.infra come from
// the matching entry in ctx.Features. One lookup serves every attachment
// family, so a credential FeatureRecipe's injectMapping reads the attachment's
// own infra exactly as any other attachment's does.
type FeatureRecipeRef struct {
	InstanceName  string
	FeatureRecipe *types.FeatureRecipe
}

// InjectMappingDelivery records what one attachment's FeatureRecipe injectMapping
// put on the host node's delivery pipe: the attachment it was resolved for, the
// mapping's authored text, and the delivery keys the RENDERED overlay produced.
//
// It carries the authored text and the rendered keys TOGETHER because neither
// answers the question alone. A mapping's delivery key is itself a template, so
// only the rendered overlay names the key; what the mapping READ is only in the
// authored text, and no inspection of a rendered value could recover it. A caller
// deciding whether a delivered key carries material a declaration governs needs
// both halves of the same evaluation, and pairing them here is what stops the two
// from being fetched separately and mismatched.
//
// Reporting is deliberately a return value rather than an optional collector: a
// route that delivers without reporting is the shape every withholding gap on this
// pipe has had, and a value the caller must receive cannot be forgotten silently.
type InjectMappingDelivery struct {
	InstanceName string
	Mapping      string
	Keys         redact.DeliveryKeys

	// Mechanisms pairs each mechanism the host component's own injection block
	// declares with whether this mapping's rendered output carries a top-level
	// key for it, and with the FeatureRecipe the mapping was read from.
	//
	// It travels with the keys rather than beside them for the reason the keys
	// travel with the authored text: both halves come out of one evaluation, and
	// the mechanisms a rendered output names are knowable only from that output.
	//
	// It is empty where the output named no mechanism — a flat delivery
	// dictionary, or an output that parsed as nothing — because such an output
	// makes no claim about the component's declarations to read against.
	Mechanisms []constraint.InjectMappingMechanism

	// Deliveries are the deliveries this mapping made on the host node's pipe,
	// each attributed to the attachment and marked as made through its mapping,
	// in the order the merge applied them: the three named mechanisms and then
	// every other mechanism in the order the output wrote it.
	//
	// They are the keys above, read in order and attributed, and they come from
	// the same overlay the merge applied, so what the record says this mapping
	// delivered and what the pipe received cannot disagree. Every one of them
	// arrived: the merge sets each key the output carries, an empty value
	// included, so a key is absent from this list only where the output did not
	// carry it. The order is what a rule asking which delivery came first reads,
	// which an unordered key set cannot answer.
	Deliveries []constraint.InjectDelivery
}

// ExecutePhaseA runs Phase A (Pre-Render Interception) for one node.
// It iterates FeatureRecipes in the provided order (5-Tier Onion Model from ir.FeatureOrder),
// evaluates each injectMapping template, and deep-merges the output over ctx.Context.Inject.
// For each FeatureRecipe, .values and .infra are swapped to the attachment's own
// values/infra, and .attachment_name names the attachment instance being resolved.
// .context.instance_id remains the host component throughout, so an injectMapping
// reads the same two denotations an attachment's locals do.
//
// A FeatureRecipe's own .locals resolve against that attachment-scoped view
// before its injectMapping runs, through the same evaluator the attachment's
// render path uses, so an attachment's locals resolve identically whichever
// surface reads them. The resolved entries merge over the base Recipe's
// evaluated locals, the FeatureRecipe winning on a key collision. A local
// whose template fails halts the build; it does not reach the delivery pipe as
// unevaluated text.
//
// The authored-mechanism set is collected once from ctx.InjectRaw — the
// immutable Developer intent populated by BuildInjectPipe — so every
// attachment's mapping is read against the full Component- or
// Feature-attachment authoring. ctx.InjectRaw must be either nil or a
// *types.InjectBlock per the field's contract; any other concrete type
// indicates a contract violation upstream and produces a loud halt rather than
// silently reading against an empty set.
//
// It returns one InjectMappingDelivery per attachment whose mapping produced an
// overlay, in the order the attachments were resolved. That report is what lets a
// caller decide which of the delivered keys carry material a declaration governs:
// a mapping's delivery key is itself a template, so the key is knowable only from
// the rendered overlay while what the mapping READ is knowable only from its
// authored text, and this is the one point where both are in scope.
//
// Each entry also carries the pairing between the mechanisms the host component
// declared and the mechanisms that attachment's rendered output named. The
// verdict on that pairing is not reached here: a mechanism the output leaves
// unnamed is a delivery this attachment silently drops, and the rule declared
// over the recorded pairing is what refuses it. Reaching the verdict here would
// put a check inside a phase, where it could be neither placed nor positioned by
// the framework that computes both.
func ExecutePhaseA(ctx *types.TemplateContext, featureRecipes []FeatureRecipeRef) ([]InjectMappingDelivery, error) {
	var authored []string
	if ctx.InjectRaw != nil {
		rawBlock, ok := ctx.InjectRaw.(*types.InjectBlock)
		if !ok {
			return nil, cerrors.Wrap(
				fmt.Errorf("internal error while collecting the inject mechanisms an entity authored: the authored inject block arrived in an unexpected shape (%T)", ctx.InjectRaw),
				cerrors.TranslationError,
				cerrors.SourceLocation{},
				"phase A: authored-mechanism collection",
			)
		}
		if rawBlock != nil {
			if rawBlock.Env != nil {
				authored = append(authored, "env")
			}
			if rawBlock.File != nil {
				authored = append(authored, "file")
			}
			if rawBlock.Arg != nil {
				authored = append(authored, "arg")
			}
			for k := range rawBlock.Additional.All() {
				authored = append(authored, k)
			}
		}
	}

	var delivered []InjectMappingDelivery
	for _, ref := range featureRecipes {
		if ref.FeatureRecipe == nil {
			continue
		}
		tmplText := ref.FeatureRecipe.Spec.InjectMapping
		if tmplText == "" {
			continue
		}

		// Build feature-scoped context: name the attachment instance being
		// resolved, swap .values/.infra to the feature's own, resolve the
		// FeatureRecipe's OWN locals against that scope, and merge the resolved
		// entries over the base Recipe's evaluated locals (FR wins on collision).
		// Both sides of the merge are resolved values; what injectMapping reads
		// through .locals is never authored template text.
		//
		// The instance name is set unconditionally: the attachment being resolved
		// is known from the ref, whether or not it carries values or infra of its
		// own. Only the values and infra pillars depend on that lookup, so they
		// fall back to the host node's when the attachment carries neither.
		vals, inf := ctx.Values, ctx.Infra
		if attachment, ok := ctx.AttachmentPillars(ref.InstanceName); ok {
			vals, inf = attachment.Values, attachment.Infra
		}
		featureCtx := attachmentFRContext(ctx, ref.InstanceName, vals, inf)
		frLocals, err := EvaluateFeatureRecipeLocals(
			ref.FeatureRecipe, ref.InstanceName, ctx.Context.InstanceID,
			featureCtx.Values, featureCtx.Infra,
		)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError,
				cerrors.SourceLocation{File: ref.FeatureRecipe.Metadata.Name},
				fmt.Sprintf("feature %q (recipe %q) locals", ref.InstanceName, ref.FeatureRecipe.Metadata.Name))
		}
		featureCtx.Locals = mergeFeatureLocals(ctx.Locals, frLocals)

		// Evaluate injectMapping with the feature-scoped context
		output, err := EvalTemplate(
			fmt.Sprintf("injectMapping[%s/%s]", ref.InstanceName, ref.FeatureRecipe.Metadata.Name),
			tmplText,
			&featureCtx,
		)
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError,
				cerrors.SourceLocation{File: ref.FeatureRecipe.Metadata.Name},
				fmt.Sprintf("feature %q (recipe %q) injectMapping", ref.InstanceName, ref.FeatureRecipe.Metadata.Name))
		}

		// Parse the output and deep-merge over the inject pipe
		overlay, mapped := ParseInjectMappingOutput(output)

		if overlay != nil {
			DeepMergeInjectStructured(&ctx.Context.Inject, overlay)
			// Reported from the same overlay the merge applies, so what the report
			// names and what the pipe received cannot disagree.
			delivered = append(delivered, InjectMappingDelivery{
				InstanceName: ref.InstanceName,
				Mapping:      tmplText,
				Keys:         overlayDeliveryKeys(overlay),
				Mechanisms: authoredMechanismsMapped(
					authored, mapped, ref.FeatureRecipe.Metadata.Name),
				Deliveries: overlayDeliveries(overlay, ref.InstanceName),
			})
		}
	}

	return delivered, nil
}

// authoredMechanismsMapped pairs each mechanism the host component's own
// injection block declares with whether one attachment's rendered injectMapping
// output named it, and stamps the FeatureRecipe the output was read from onto
// each pair.
//
// The authored order is kept, because it is the order the component wrote its
// mechanisms in and so the order an author reads them back in. The rendered
// names are searched rather than walked for the same reason the two sides are
// paired at all: the question is asked of the component's declarations, and a
// mechanism the output names that the component never declared is not this
// pairing's business — a mapping is free to write mechanisms of its own.
//
// It answers nothing at all when the output named no mechanism, which is the
// state a flat delivery dictionary and an unparsed output both reach. Returning
// a pairing there would say every declared mechanism went unnamed, and that
// reading turns the flat-output idiom into a refusal. The two states are told
// apart by the rendered names being absent rather than empty, which is what the
// parse reports.
func authoredMechanismsMapped(
	authored, mapped []string, recipe string,
) []constraint.InjectMappingMechanism {
	if len(mapped) == 0 || len(authored) == 0 {
		return nil
	}
	named := make(map[string]bool, len(mapped))
	for _, mechanism := range mapped {
		named[mechanism] = true
	}
	out := make([]constraint.InjectMappingMechanism, 0, len(authored))
	for _, mechanism := range authored {
		out = append(out, constraint.InjectMappingMechanism{
			Mechanism: mechanism,
			Recipe:    recipe,
			Mapped:    named[mechanism],
		})
	}
	return out
}

// overlayDeliveryKeys names the delivery keys one parsed injectMapping overlay
// puts on a node's pipe, by mechanism and then by key.
//
// It is read from [overlayDeliveries], so it walks exactly the mechanisms the
// merge applies — the three typed ones and any custom mechanism — and a key the
// report names is a key the pipe received. The union view is deliberately not
// walked: it is rebuilt from the typed mechanisms after the merge rather than
// merged from the overlay, so a marking on a typed mechanism already covers the
// same datum where the union displays it.
func overlayDeliveryKeys(overlay *types.InjectPipe) redact.DeliveryKeys {
	var out redact.DeliveryKeys
	for _, delivery := range overlayDeliveries(overlay, "") {
		if out == nil {
			out = redact.DeliveryKeys{}
		}
		if out[delivery.Mechanism] == nil {
			out[delivery.Mechanism] = map[string]bool{}
		}
		out[delivery.Mechanism][delivery.DeliveryKey] = true
	}
	return out
}

// overlayDeliveries reads the deliveries one parsed injectMapping overlay makes on
// a node's pipe, attributed to the attachment whose mapping produced it and
// marked as made through a mapping, in the order the merge applies them.
//
// The overlay is a pipe of its own, so it is read by the one walk every pipe is
// read by — the walk that records what the component's own block and its
// connections delivered — rather than by a second one that agrees with it today.
// That walk visits the mechanisms the merge applies, in the order it applies
// them, so the order recorded here is the order the pipe received the keys in.
// Every delivery is marked delivered, because the merge sets each key the overlay
// carries, an empty value included.
func overlayDeliveries(overlay *types.InjectPipe, attachment string) []constraint.InjectDelivery {
	if overlay == nil {
		return nil
	}
	deliveries := pipeDeliveries(*overlay)
	for i := range deliveries {
		deliveries[i].Attachment = attachment
		deliveries[i].ThroughMapping = true
	}
	return deliveries
}
