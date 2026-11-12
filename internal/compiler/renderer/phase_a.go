// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer

import (
	"fmt"
	"reflect"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// injectMappingScope is the reference set a FeatureRecipe's injectMapping is
// checked against, whatever the recipe's execution scope. The universe is
// types.TemplateContext — the type the mapping is evaluated against, once per
// consuming component, on that component's context with the attachment's own
// values, infra and locals and the attachment instance named — so the roots the
// check admits are derived from that type rather than enumerated here.
//
// The surface declares one narrowing:
//
//   - boundary — the view a boundary-scope FeatureRecipe's other templates read is
//     populated only where the recipe renders once for its boundary. A mapping runs
//     per consumer, where nothing populates it, so a read of it is refused rather
//     than resolved to nothing.
//
// It is the ONE declaration of what a mapping may read, and both sites that judge a
// mapping read it: the evaluation below, and the rule over a boundary-scope
// FeatureRecipe's inline template fields, which checks a mapping before anything is
// evaluated. A second list kept beside either would be free to admit what the other
// refuses, and a mapping would then be refused a read its context holds, or admitted
// one its context never sets.
var injectMappingScope = templateScope{
	data:    &types.TemplateContext{},
	exclude: []string{"boundary"},
}

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

	// boundaryLocals, for a FeatureRecipe that renders once for its boundary, is
	// the locals that boundary's one evaluation resolved — the value Phase A hands
	// the injectMapping, in place of an evaluation for this consumer. Empty for
	// every other FeatureRecipe, whose locals Phase A evaluates per consumer.
	boundaryLocals types.OrderedMap[string]
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

	// Undelivered names the top-level keys this mapping's rendered output wrote
	// deliveries under that the merge does not apply, each beside the
	// FeatureRecipe the mapping was read from.
	//
	// It travels with the mechanisms for the reason they do: both are read off
	// the one rendered output, and the merged pipe cannot answer either. The phase
	// decides nothing about it; the rule declared over the recorded keys refuses
	// them.
	Undelivered []constraint.InjectMappingUndeliveredKey

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
// That holds for a FeatureRecipe rendering once for a boundary as much as for one
// rendering per component: a mapping runs once per consuming component either way,
// on that component's context. So every mapping is checked against the one scope
// [injectMappingScope] declares, and a read of .boundary — which no consuming
// component's context carries — halts with ErrTemplateUnknownRoot naming the roots
// a mapping may read.
//
// A FeatureRecipe's own .locals resolve against that attachment-scoped view
// before its injectMapping runs, through the same evaluator the attachment's
// render path uses, so an attachment's locals resolve identically whichever
// surface reads them. The resolved entries merge over the base Recipe's
// evaluated locals, the FeatureRecipe winning on a key collision. A local
// whose template fails halts the build; it does not reach the delivery pipe as
// unevaluated text. A FeatureRecipe rendering once for its boundary is the one
// exception to where its locals come from, not to the merge: they were evaluated
// once at boundary scope, where .boundary is set, and the ref carries that
// evaluation's resolved locals, so this evaluates none of them per consumer.
//
// The authored-mechanism set is collected once from ctx.InjectRaw — the
// immutable Developer intent populated by BuildInjectPipe — so every
// attachment's mapping is read against the full Component- or
// Feature-attachment authoring. ctx.InjectRaw must be either nil or a
// *types.InjectBlock per the field's contract; any other concrete type
// indicates a contract violation upstream and produces a loud halt rather than
// silently reading against an empty set.
//
// A mapping whose output does not parse halts with
// ErrInjectMappingOutputNotParsable, naming the attachment and the FeatureRecipe,
// rather than delivering nothing: none of the output's keys could be read, so the
// consumer would otherwise render as though the attachment delivered nothing at
// all. An output that is empty, or parses as a dictionary delivering nothing,
// delivers nothing and is not a failure.
//
// An output that parses and writes deliveries under a key the merge does not
// apply — the all view, which is rebuilt from the mechanisms rather than merged —
// is reported on its entry rather than halted here, for the reason the pairing
// below is: the rule declared over the recorded keys is what refuses it.
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
//
// withheld names what an author declared sensitive in the host's context and in
// each attachment's own pillars, so a failure evaluating an attachment's locals or
// its mapping reports without such a value.
func ExecutePhaseA(ctx *types.TemplateContext, featureRecipes []FeatureRecipeRef, withheld hostWithheld) ([]InjectMappingDelivery, error) {
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
		attachment, pillarsFound := ctx.AttachmentPillars(ref.InstanceName)
		if pillarsFound {
			vals, inf = attachment.Values, attachment.Infra
		}
		featureCtx := attachmentFRContext(ctx, ref.InstanceName, vals, inf)
		frLocals := ref.boundaryLocals
		if ref.FeatureRecipe.Spec.ExecutionScope != executionScopeBoundary {
			var err error
			frLocals, err = EvaluateFeatureRecipeLocals(
				ref.FeatureRecipe, ref.InstanceName, ctx.Context.InstanceID, ctx.Context.RenderedName,
				featureCtx.Values, featureCtx.Infra,
				withheld.leaves(ref.InstanceName, pillarsFound),
			)
			if err != nil {
				return nil, cerrors.Wrap(err, cerrors.CatalogError,
					cerrors.SourceLocation{File: ref.FeatureRecipe.Metadata.Name},
					fmt.Sprintf("feature %q (recipe %q) locals", ref.InstanceName, ref.FeatureRecipe.Metadata.Name))
			}
		}
		featureCtx.Locals = mergeFeatureLocals(ctx.Locals, frLocals)

		eval := valueTemplate(
			fmt.Sprintf("injectMapping[%s/%s]", ref.InstanceName, ref.FeatureRecipe.Metadata.Name),
			tmplText,
			injectMappingScope.with(
				&featureCtx,
				withheld.forAttachment(ref.InstanceName, pillarsFound, ref.FeatureRecipe.Spec.Locals),
			),
			"{{", "}}",
		)
		output, err := eval.run()
		if err != nil {
			return nil, cerrors.Wrap(err, cerrors.CatalogError,
				cerrors.SourceLocation{File: ref.FeatureRecipe.Metadata.Name},
				fmt.Sprintf("feature %q (recipe %q) injectMapping", ref.InstanceName, ref.FeatureRecipe.Metadata.Name))
		}

		// Parse the output and deep-merge over the inject pipe. An output that
		// does not parse halts: none of its keys would be delivered, and the
		// consumer would render as though the attachment delivered nothing. The
		// reason is withheld on the terms every template output's is, since the
		// value that broke the output is most often the one declared sensitive.
		// The frame names the attachment and the FeatureRecipe the way every other
		// failure of this mapping is named, with the condition and the reason
		// beneath it.
		overlay, mapped, parseErr := ParseInjectMappingOutput(output)
		if parseErr != nil {
			reason := withholdDecodeFailure(eval, parseErr, reflect.TypeOf(types.InjectPipe{}))
			return nil, cerrors.Wrap(
				fmt.Errorf("%w: %w", ErrInjectMappingOutputNotParsable(), reason),
				cerrors.CatalogError,
				cerrors.SourceLocation{File: ref.FeatureRecipe.Metadata.Name},
				fmt.Sprintf("feature %q (recipe %q) injectMapping output", ref.InstanceName, ref.FeatureRecipe.Metadata.Name))
		}

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
				Undelivered: undeliveredInjectMappingKeys(overlay, ref.FeatureRecipe.Metadata.Name),
				Deliveries:  overlayDeliveries(overlay, ref.InstanceName),
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
// state a flat delivery dictionary and an output parsing as nothing both reach. Returning
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
