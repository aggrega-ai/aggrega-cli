// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/constraint"
	"github.com/aggrega-ai/aggrega-cli/internal/gotmpl"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ResolveAttachmentInject delivers authored attachment-level inject into the
// node's delivery pipe.
//
// An attachment-level inject value is a provides-key reference resolved
// against the attachment's OWN resolved provides — its FeatureRecipe's provides
// evaluated against the attachment's values/infra/locals — delivered into the
// host component's .context.inject under the local delivery key. This is
// distinct from uses[].inject (a named dependency target's provides, resolved
// in Pass 2b) and from component.inject (the host's own provides / literals,
// resolved in Pass 2a).
//
// The pipe already carries the Pass 2a (self) and Pass 2b (uses) delivery keys
// when this runs, so a delivery key already present is a duplicate across
// developer-authored sources. Delivery does not decide that: it records what
// every surface asked the pipe for, attributed, and the rule declared over that
// record refuses the duplicate. What is recorded is the deliveries the pipe
// already held when this ran, then each attachment's own claims in the order
// they were made — so the first claim under a key is the one that holds it, and a
// claim whose key is already held delivers nothing rather than displacing it. The
// FeatureRecipe injectMapping overlay in Phase A comes later and is merged over
// this pipe; its deliveries join the same record once the mappings render, where a
// mapping rewriting a key the component's own block or one of its connections
// delivered is interception, and one writing a key another attachment delivered
// is refused by the rule over that record. Attachments are processed in IR
// (author) order for deterministic emission.
//
// The attributed record is returned rather than written from here, because the
// caller is what holds the render's retention and what invokes the rule: this
// function's whole subject is one node's pipe, and a node absent from the
// returned record would be indistinguishable from one that delivered nothing.
//
// onSensitiveByValue, when non-nil, is called once for each delivery key that
// actually received a value the attachment's schema declares sensitive. This is
// the by-value channel by construction — the resolved scalar is copied into the
// pipe as a literal — so reporting from the delivery site is what keeps the
// advisory tied to a delivery that really happened. The callback receives the
// attachment instance, the delivery mechanism and the delivery key; it never
// sees the value.
//
// hostRenderedName is the name the node's own provider reported for it, which each
// attachment's provides read as .context.rendered_name; empty where none was
// reported. It is passed rather than looked up so the caller's one answer is the
// one every attachment surface reads.
//
// An attachment whose FeatureRecipe renders once for its boundary resolves its
// inject block against the one evaluation of its provides that boundary made,
// read from boundaryFR through the cascade lookup from the node's boundary path —
// the value [ResolveAttachmentProvides] reads too — and is never evaluated here.
func ResolveAttachmentInject(
	inject *types.InjectPipe,
	graph *types.IRGraph,
	nodeID, hostRenderedName string,
	recipeLookup RecipeLookup,
	onSensitiveByValue func(attachment, mechanism, deliveryKey string),
	declared func(featureType string) resolver.SensitiveLeaves,
	boundaryFR *BoundaryFRState,
) ([]constraint.InjectDelivery, error) {
	// The deliveries already standing in the pipe are read FIRST, before any
	// attachment delivers into it. They are the component's own and its
	// connections', which this function cannot tell apart and does not need to:
	// what the record has to say is that they are not an attachment's.
	deliveries := pipeDeliveries(*inject)
	if recipeLookup.FeatureRecipe == nil {
		return deliveries, nil
	}
	entries, ok := graph.Spec.FeatureValuesMapping.Get(nodeID)
	if !ok {
		inject.All = buildInjectAll(*inject)
		return deliveries, nil
	}
	var consumerBoundaryPath string
	if node, found := graph.Spec.Nodes.Get(nodeID); found {
		consumerBoundaryPath = node.BoundaryPath
	}
	for instanceName, entry := range entries.All() {
		if entry.Inject.Len() == 0 {
			continue
		}
		fr, found := recipeLookup.FeatureRecipe(instanceName, entry.Type)
		if !found {
			continue
		}
		made, boundary, _ := boundaryScopeAttachmentEvaluation(fr, boundaryFR, consumerBoundaryPath, instanceName)
		provided := made.Provides
		if !boundary {
			var err error
			provided, err = evalAttachmentFRProvides(fr, instanceName, nodeID, hostRenderedName, entry, declaredLeavesOf(declared, entry.Type))
			if err != nil {
				return deliveries, err
			}
		}
		deliveries = append(deliveries, deliverAttachmentInjectKeys(
			inject, instanceName, entry.Inject, provided, entry.SensitiveInject, onSensitiveByValue)...)
	}
	inject.All = buildInjectAll(*inject)
	return deliveries, nil
}

// pipeDeliveries reads the deliveries a pipe carries, in the order the pipe holds
// them: the three named mechanisms and then the rest in the order the pipe
// accumulated them. It attributes nothing; a caller that knows who made the
// deliveries stamps them.
//
// It reads two pipes. Read from the node's own pipe before any attachment
// delivers, every entry it returns is one the component's own injection block or
// one of its connections made. Neither carries an attachment, and the record does
// not distinguish the two: a duplicate between an attachment and either of them
// is one condition with one remedy, and what says the other surface is not an
// attachment's is the empty attachment on its entry. Read from one attachment's
// rendered mapping overlay, it returns what that mapping delivers, in the order
// the merge applies it, because the merge visits the mechanisms in this same
// order — which is why one walk serves both.
//
// Every entry reports as delivered, because a pipe holds a key only where a value
// arrived under it — a reference that resolved to nothing set nothing — and the
// merge sets every key an overlay carries.
func pipeDeliveries(inject types.InjectPipe) []constraint.InjectDelivery {
	var out []constraint.InjectDelivery
	record := func(mechanism string, keys types.OrderedMap[string]) {
		for key := range keys.All() {
			out = append(out, constraint.InjectDelivery{
				Mechanism: mechanism, DeliveryKey: key, Delivered: true,
			})
		}
	}
	record("env", inject.Env)
	record("file", inject.File)
	record("arg", inject.Arg)
	for mechanism, keys := range inject.Additional.All() {
		record(mechanism, keys)
	}
	return out
}

// attachmentFRScope is the reference set the attachment FeatureRecipe path's
// templates are checked against — its own locals entries and then its provides.
// The universe is ProvidesContext, the type both stages are evaluated against: an
// attachment IS being resolved here, so .attachment_name names it and
// .context.instance_id names the host component the attachment is resolving for.
// One scope covers both stages because they render against one context shape; two
// would be free to disagree about what the path admits.
//
// context.rendered_name is the HOST node's answer on this surface, as instance_id
// is the host's identity. Whether it is admitted is a property of the host being
// rendered — admitted where the host's provider reported a name, refused where it
// reported none — so it is decided per host by [attachmentHostScope] rather than
// declared here.
var attachmentFRScope = templateScope{
	data: ProvidesContext{},
}

// attachmentFRRenderScope is the reference set an attachment FeatureRecipe's
// RENDER templates are checked against — its values mapping, its workload body,
// each of its artifacts entries, and each of its pipeline steps' input and gate.
// The universe is the full render context, because that is what those fields are
// evaluated against: an attachment's context is derived from its host node's, with
// the values and infra pillars replaced by the attachment's own and the attachment
// instance named. Deriving it from the host's context is also what puts the host's
// reported name at context.rendered_name on these fields.
//
// It declares no narrowing of its own and takes the same per-host one the
// locals-and-provides scope above takes, through [attachmentHostScope], for the same
// reason: a rule that admitted the reference on some of an attachment's surfaces and
// refused it on others for one host would be an inconsistency rather than a coverage
// bound. Two scopes rather than one is a consequence of the two surfaces being
// evaluated against two different context TYPES — the locals and provides pass
// renders against the provides context, these fields against the whole render
// context — and the narrowing they share is derived at one site so neither can drift
// from the other.
var attachmentFRRenderScope = templateScope{
	data: &types.TemplateContext{},
}

// classifyAttachmentTemplateRenderedName decides whether one authored template
// field on a per-component attachment reads the name its host node's target gives
// the resources it renders for the host where the host's provider reported no such
// name, and returns the refusal where it does.
//
// It is the ONE home of that decision, so the rule declared over these fields and
// any later reader reach the same verdict. The refused reference is recovered from
// the collected root accesses rather than from the keystrokes, so a read through a
// with body or a variable bound to the identity pillar is refused exactly as the
// direct spelling is.
//
// It looks for that ONE key rather than running the scope's whole admission walk,
// and the narrowness is deliberate. These fields have never been checked against a
// reference set, so a full walk would begin refusing references that render today —
// a stale root resolving to an empty value is a defect, but it is a different
// defect with a different remedy, and turning it into a halt here would reject
// authoring on the strength of a rule nobody declared over this surface. The set
// the author is told about is still derived from the per-host scope rather than
// restated, so what is refused and what is reported cannot disagree.
//
// A template that does not parse yields no refusal: evaluation reports the parse
// error against the same text, so refusing it here as well would report one
// authoring fault twice.
func classifyAttachmentTemplateRenderedName(
	text, leftDelim, rightDelim, hostRenderedName string,
) *templateScopeViolation {
	if text == "" {
		return nil
	}
	tree, err := gotmpl.ParseTree(text, leftDelim, rightDelim)
	if err != nil {
		return nil
	}
	ref, reads := renderedNameRootRef(tree)
	if !reads {
		return nil
	}
	keys, gated := attachmentHostScope(attachmentFRRenderScope, hostRenderedName).admittedKeys(identityContextRoot)
	if !gated || slices.Contains(keys, renderedNameContextKey) {
		return nil
	}
	return &templateScopeViolation{Ref: ref, Admitted: keys, Level: scopeLevelKey}
}

// evalAttachmentFRProvides evaluates the attachment FeatureRecipe's provides
// against the attachment's own scope — the host component's .context.instance_id,
// the attachment's .attachment_name, its own values and infra, and the FR's own
// evaluated locals — mirroring the boundary-scope FR provides evaluation. The
// resolved provides are the source the authored inject's provides-key references
// resolve against.
//
// The two identities are distinct and both are carried: .context.instance_id is
// the host component, and the attachment travels separately on .attachment_name.
// hostRenderedName is the name the host's provider reported for the host, carried
// as .context.rendered_name on both stages and refused by their scope where it is
// empty ([attachmentHostScope]).
//
// The locals resolve first, through evaluateLocalsInOrder — the one home of the
// locals contract — so an entry may read an earlier sibling and a forward or
// unknown sibling reference halts. The provides then evaluate against the same
// scope carrying the fully resolved block, which is what lets a provides template
// read .locals and receive resolved values; a provides template naming a local the
// block does not declare halts through the same shared refusal.
//
// leaves is what the attachment's Feature declares sensitive. Both the locals and
// the provides render against the attachment's own values and infra, so a failure
// of either reports without a value that Feature declares, or a local computed
// from one.
func evalAttachmentFRProvides(fr *types.FeatureRecipe, instanceName, nodeID, hostRenderedName string, entry types.IRFeatureEntry, leaves resolver.SensitiveLeaves) (types.OrderedMap[string], error) {
	withheld := attachmentPillarKeys(leaves, derivedLocals(fr.Spec.Locals, leaves.Values, leaves.Infra))
	scope := attachmentHostScope(attachmentFRScope, hostRenderedName)
	resolvedLocals, err := evaluateLocalsInOrder(
		fr.Spec.Locals,
		scope,
		func(key string) localsRefSite {
			return localsRefSite{
				Where: fmt.Sprintf("node %q attachment %q locals[%q]", nodeID, instanceName, key),
				File:  fr.Metadata.Name,
				Key:   key,
			}
		},
		func(key string) string { return fmt.Sprintf("attach-inject-local[%s.%s]", instanceName, key) },
		func(resolved types.OrderedMap[string]) any {
			return ProvidesContext{
				Context:        ProvidesContextBlock{InstanceID: encodeNodeIdentity(nodeID), RenderedName: hostRenderedName},
				Values:         entry.Values,
				Infra:          entry.Infra,
				Locals:         resolved,
				AttachmentName: instanceName,
			}
		},
		withheld,
	)
	if err != nil {
		return types.OrderedMap[string]{}, err
	}
	if err := checkProvidesLocalsReferences(fr.Spec.Provides, resolvedLocals, func(key string) localsRefSite {
		return localsRefSite{
			Where: fmt.Sprintf("node %q attachment %q provides[%q]", nodeID, instanceName, key),
			File:  fr.Metadata.Name,
		}
	}); err != nil {
		return types.OrderedMap[string]{}, err
	}
	pCtx := ProvidesContext{
		Context:        ProvidesContextBlock{InstanceID: nodeID, RenderedName: hostRenderedName},
		Values:         entry.Values,
		Infra:          entry.Infra,
		Locals:         resolvedLocals,
		AttachmentName: instanceName,
	}
	provided := types.OrderedMap[string]{}
	for k, tmplText := range fr.Spec.Provides.All() {
		out, err := evalValueTemplate(fmt.Sprintf("attach-inject-provides[%s.%s]", instanceName, k), tmplText, scope.with(pCtx, withheld), "{{", "}}")
		if err != nil {
			return types.OrderedMap[string]{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("node %q attachment %q provides[%q]", nodeID, instanceName, k))
		}
		provided.Set(k, strings.TrimSpace(out))
	}
	return provided, nil
}

// featureProvidesContract is the provides contract the Feature an attachment
// names declares — the keys its realizer must publish — together with the
// Feature's type, which a refusal names. It is read once per attachment, by
// [featureProvidesContractOf], and enforced where the attachment's provides are
// evaluated: per consumer for a FeatureRecipe rendering per component, once at
// the boundary attachment for one rendering once for its boundary. The zero
// value declares nothing.
type featureProvidesContract struct {
	keys    map[string]bool
	feature string
}

// featureProvidesContractOf reads the provides contract of the Feature named by
// featureType from the catalog. A lookup with no Feature reader, or a Feature the
// catalog does not hold, declares nothing.
func featureProvidesContractOf(catalogLookup resolver.CatalogLookup, featureType string) featureProvidesContract {
	if catalogLookup.Feature == nil {
		return featureProvidesContract{}
	}
	feat, found := catalogLookup.Feature(featureType)
	if !found {
		return featureProvidesContract{}
	}
	return featureProvidesContract{
		keys:    resolver.FeatureProvidesKeys(feat, catalogLookup.SchemaLibraries),
		feature: featureType,
	}
}

// enforce halts when produced omits a key the contract declares, through
// [enforceProvidesContract]. location is the halt's source location and
// contextPrefix locates the binding; realizer is the pre-quoted descriptor of the
// FeatureRecipe that produced the provides.
func (c featureProvidesContract) enforce(produced types.OrderedMap[string], location, contextPrefix, realizer string) error {
	if len(c.keys) == 0 {
		return nil
	}
	return enforceProvidesContract(c.keys, produced, location, contextPrefix, realizer, fmt.Sprintf("Feature %q", c.feature))
}

// enforceProvidesContract halts when a realizer's produced provides bag omits
// any key the abstract entity declares as its output contract. The contract is
// structural key-presence — subset-required, superset-permitted: every declared
// key must appear in produced, while extra produced keys are tolerated. Produced
// values are never inspected (they are opaque runtime pointers). Declared keys
// are checked in sorted order so the first missing key reported, and the halt,
// are byte-identical across runs. contextPrefix locates the binding (e.g.
// `node "x" attachment "y"`); realizer and abstract are the pre-quoted entity
// descriptors (e.g. `FeatureRecipe "z"`, `Feature "core/secret"`) the diagnostic
// names. nodeID sets the halt's source location.
//
// It reaches the Feature/FeatureRecipe pair, and that pair alone. The same
// semantic over the Component/Recipe pair is declared as
// [RecipePublishesEveryDeclaredProvidesKey] and invoked over the render's own
// presentation of what each node resolved, so the two halves of one condition are
// reached one way each rather than both through here. What keeps them one
// condition is the sentinel: both raise [ErrProvidesContractUnfulfilled], and the
// registered wording that names the condition is written once, on it.
//
// The attachment pair is not expressed the same way because it cannot be yet: the
// resolved-provides fact the declared rule's predicate reads is keyed by node, and
// an attachment's resolved provides are a separate surface with no fact of their
// own. So this stays until there is one, and the shape it would take is the shape
// beside it.
func enforceProvidesContract(declared map[string]bool, produced types.OrderedMap[string], nodeID, contextPrefix, realizer, abstract string) error {
	keys := make([]string, 0, len(declared))
	for k := range declared {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if _, has := produced.Get(key); !has {
			return cerrors.Wrap(ErrProvidesContractUnfulfilled(), cerrors.CatalogError, cerrors.SourceLocation{Instance: nodeID},
				cerrors.ComposeMessage(ErrProvidesContractUnfulfilled(), []string{
					contextPrefix,
					fmt.Sprintf("%s does not publish provides key %q declared by %s's schema.provides contract", realizer, key, abstract),
				}, ""))
		}
	}
	return nil
}

// ResolveAttachmentProvides resolves every attachment in one bucket against its
// bound FeatureRecipe's `provides` ONCE — before the consumer renders — keyed by
// attachment instance name, and enforces the producer-fulfilment contract: the
// bound FeatureRecipe must publish every provides key the attachment's Feature
// declares in schema.provides; a missing key halts with
// ErrProvidesContractUnfulfilled (structural key-presence, never a judgement of
// the value).
//
// This is the one home for resolving an attachment's provides. Every attachment
// routes through it, so the published output a consumer reads at
// .features.<name>.provides — or at .context.uses.<alias>.features.<slot>.provides
// for an attachment one of its connections carries — the contract check, and the
// attachment FeatureRecipe's own .provides at Phase B.6 all derive from a single
// evaluation and cannot drift. An attachment with no bound FeatureRecipe is
// skipped; instances are walked in sorted order so the first-reported shortfall
// is deterministic.
//
// declared answers what each attachment's Feature declares sensitive, so a
// failure evaluating an attachment's provides reports without a value its Feature
// declares; nil declares nothing.
//
// hostRenderedName is the name the node's own provider reported for it — what an
// attachment's provides read as .context.rendered_name — and empty where none was
// reported, as for [ResolveAttachmentInject].
//
// An attachment whose FeatureRecipe renders once for its boundary is not evaluated
// here. Its provides were evaluated once, at boundary scope, before the node walk,
// and this reads that evaluation through the cascade lookup from consumerBoundaryPath
// — the boundary path of the node the attachments are resolved for — in boundaryFR,
// so every consumer the boundary holds reads the one value a uses edge carrying the
// attachment reads too. Its Feature's provides contract was enforced on that
// evaluation, at the boundary attachment, and is not enforced again per consumer. A
// nil boundaryFR holds no evaluation, and such an attachment then carries no
// provides.
func ResolveAttachmentProvides(attachments types.OrderedMap[types.IRFeatureEntry], nodeID, hostRenderedName string, recipeLookup RecipeLookup, catalogLookup resolver.CatalogLookup, declared func(featureType string) resolver.SensitiveLeaves, boundaryFR *BoundaryFRState, consumerBoundaryPath string) (map[string]types.OrderedMap[string], error) {
	if recipeLookup.FeatureRecipe == nil || attachments.Len() == 0 {
		return nil, nil
	}
	out := map[string]types.OrderedMap[string]{}
	instances := attachments.Keys()
	sort.Strings(instances)
	for _, instanceName := range instances {
		entry, _ := attachments.Get(instanceName)
		fr, found := recipeLookup.FeatureRecipe(instanceName, entry.Type)
		if !found || fr == nil {
			continue
		}
		if made, boundary, evaluated := boundaryScopeAttachmentEvaluation(fr, boundaryFR, consumerBoundaryPath, instanceName); boundary {
			if evaluated {
				out[instanceName] = made.Provides
			}
			continue
		}
		provided, err := evalAttachmentFRProvides(fr, instanceName, nodeID, hostRenderedName, entry, declaredLeavesOf(declared, entry.Type))
		if err != nil {
			return nil, err
		}
		// Producer-fulfilment: the bound FeatureRecipe must publish every provides
		// key the attachment's Feature declares as its output contract.
		if err := featureProvidesContractOf(catalogLookup, entry.Type).enforce(provided, nodeID,
			fmt.Sprintf("node %q attachment %q", nodeID, instanceName),
			fmt.Sprintf("FeatureRecipe %q", fr.Metadata.Name)); err != nil {
			return nil, err
		}
		out[instanceName] = provided
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// deliverAttachmentInjectKeys resolves each delivery key's provides-key
// reference against the attachment's resolved provides and sets the resolved
// value into the pipe under the local delivery key, returning one record per
// claim the attachment made, in the order the claims were made.
//
// A delivery key already present in the pipe (from self-inject, uses-inject, or a
// prior attachment) is a duplicate across developer-authored sources. This
// delivers nothing under such a key and records the claim as undelivered; the
// rule declared over the attributed record is what refuses it. Leaving the
// standing value in place rather than displacing it is what keeps the first claim
// under a key the one that holds it, which is the order the record's own contract
// promises a reader.
//
// An unresolved provides-key (absent from the attachment's provides) delivers
// nothing either (missingkey=zero semantics) and is recorded the same way — and
// the two cases have to be recorded alike, because a claim that put no value
// under a key leaves that key free for a later claim whichever reason it failed
// for. Mechanisms are processed env, file, arg, then custom mechanisms in sorted
// order for deterministic emission, which is the order the records come back in.
//
// sensitiveInject is the attachment's derived per-delivery-key marking, in the
// same mechanism-then-delivery-key shape as entryInject. It is read, never
// recomputed: whether a delivery key carries declared-sensitive material is
// decided once where the attachment is merged, so a second decision here could
// disagree with the first. onSensitiveByValue, when non-nil, is called for each
// marked key whose value the pipe actually received; a key whose referenced
// provides key resolved to nothing delivers no literal and reports nothing.
func deliverAttachmentInjectKeys(inject *types.InjectPipe, instanceName string, entryInject types.OrderedMap[types.OrderedMap[string]], provided types.OrderedMap[string], sensitiveInject types.OrderedMap[types.OrderedMap[string]], onSensitiveByValue func(attachment, mechanism, deliveryKey string)) []constraint.InjectDelivery {
	var claims []constraint.InjectDelivery
	reportSensitive := func(mech, localKey string) {
		if onSensitiveByValue == nil {
			return
		}
		marked, ok := sensitiveInject.Get(mech)
		if !ok {
			return
		}
		if _, isMarked := marked.Get(localKey); isMarked {
			onSensitiveByValue(instanceName, mech, localKey)
		}
	}
	set := func(dest *types.OrderedMap[string], mech, localKey, providesKey string) {
		claim := constraint.InjectDelivery{
			Mechanism: mech, DeliveryKey: localKey, Attachment: instanceName,
		}
		if _, exists := dest.Get(localKey); exists {
			claims = append(claims, claim)
			return
		}
		val, ok := provided.Get(providesKey)
		if !ok {
			claims = append(claims, claim)
			return
		}
		dest.Set(localKey, val)
		claim.Delivered = true
		claims = append(claims, claim)
		reportSensitive(mech, localKey)
	}
	for mech, mechMap := range deliveryMappingMechanisms(entryInject) {
		dest, keep := injectPipeMechanism(inject, mech)
		for localKey, providesKey := range mechMap.All() {
			set(dest, mech, localKey, providesKey)
		}
		keep()
	}
	return claims
}
