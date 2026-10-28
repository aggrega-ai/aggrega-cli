// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"

	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// SelfInjectContext is the restricted template context for self-inject value:
// evaluation. Its Context block defines the node's own identity, labels and
// annotations and nothing else, so .context.inject and .context.uses name keys this
// surface's context does not define: the inject pipe is what this pass is
// constructing, and a template inside it cannot read from it. The refusal comes
// from the shape of the type, so no list anywhere names the two pillars it keeps
// out — a pillar added to or retired from the block moves what the surface admits
// with no edit at the check.
//
// Metadata is the Release and compilation identity the node's Recipe templates read
// at .metadata, from the same assembly. It is held by value, so the block is always
// present: a template reading one of its fields renders that field, or nothing where
// the build has no Release, and never stops on an absent block.
type SelfInjectContext struct {
	Values   types.OrderedMap[any]  `json:"values"`
	Infra    types.OrderedMap[any]  `json:"infra"`
	Context  SelfInjectContextBlock `json:"context"`
	Metadata types.TemplateMetadata `json:"metadata"`
}

// selfInjectMetadata is the .metadata block a node's self-inject value: templates
// read: the node's template metadata, or an empty block where there is none.
func selfInjectMetadata(graph *types.IRGraph, release *types.Release) types.TemplateMetadata {
	if m := nodeTemplateMetadata(graph, release); m != nil {
		return *m
	}
	return types.TemplateMetadata{}
}

// SelfInjectContextBlock holds the restricted context fields for self-inject templates.
type SelfInjectContextBlock struct {
	InstanceID  string                   `json:"instance_id"`
	Labels      types.OrderedMap[string] `json:"labels,omitempty,omitzero"`
	Annotations *types.AnnotationBuckets `json:"annotations,omitempty"`
}

// ExecutePass2a resolves the component-level injection block one node's author
// wrote into that node's injection pipe, before the pipe is rehydrated from what
// the node's dependencies provide.
//
// Each mechanism the block declares is a list of entries, and each entry
// resolves one delivery key:
//   - from: → resolved from the node's own provides
//   - value: → evaluated as a Go template with the restricted context
//
// The resolved keys are observable on the pipe it populates and are returned
// nowhere. Whether one of them is also delivered by a dependency connection is
// not this function's question: that is a rule over the finalised graph, asked of
// the block as authored rather than of the values resolution produced, and asked
// before the render begins.
func ExecutePass2a(
	inject *types.InjectPipe,
	selfInject *types.InjectBlock,
	nodeID string,
	ownProvides types.OrderedMap[string],
	siCtx SelfInjectContext,
	withheld redact.Keys,
) error {
	if selfInject == nil {
		return nil
	}

	// Process each mechanism
	if err := processSelfInjectMechanism(&inject.Env, "env", selfInject.Env, ownProvides, siCtx, withheld); err != nil {
		return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q self-inject", nodeID))
	}
	if err := processSelfInjectMechanism(&inject.File, "file", selfInject.File, ownProvides, siCtx, withheld); err != nil {
		return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q self-inject", nodeID))
	}
	if err := processSelfInjectMechanism(&inject.Arg, "arg", selfInject.Arg, ownProvides, siCtx, withheld); err != nil {
		return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q self-inject", nodeID))
	}

	// A mechanism the component names that is not one of the three typed ones
	// resolves through the same per-key resolver and lands under its own name.
	// The resolver is already mechanism-parametric, so the mechanism name and
	// the destination map are the only things that differ from the three calls
	// above.
	for _, mech := range customInjectMechanisms(selfInject.Additional) {
		raw, _ := selfInject.Additional.Get(mech)
		dest, ok := inject.Additional.Get(mech)
		if !ok {
			dest = types.OrderedMap[string]{}
		}
		if err := processSelfInjectMechanism(&dest, mech, raw, ownProvides, siCtx, withheld); err != nil {
			return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID}, fmt.Sprintf("node %q self-inject", nodeID))
		}
		inject.Additional.Set(mech, dest)
	}

	// Rebuild .all
	inject.All = buildInjectAll(*inject)

	return nil
}

func processSelfInjectMechanism(
	dest *types.OrderedMap[string],
	mechName string,
	raw any,
	ownProvides types.OrderedMap[string],
	siCtx SelfInjectContext,
	withheld redact.Keys,
) error {
	if raw == nil {
		return nil
	}

	entries, err := normalizeSelfInjectEntries(raw)
	if err != nil {
		return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, mechName)
	}

	keys := make([]string, 0, len(entries))
	for k := range entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		entry := entries[key]
		resolved, err := resolveSelfInjectEntry(key, entry, mechName, ownProvides, siCtx, withheld)
		if err != nil {
			return err
		}
		dest.Set(key, resolved)
	}

	return nil
}

// selfInjectEntry represents one normalized self-inject entry.
type selfInjectEntry struct {
	value string // value: template text
	from  string // from: provides key reference
}

// normalizeSelfInjectEntries reads one mechanism of a component-level injection
// block: the list of {in, from?, value?} entries the inject_delivery definition
// describes, keyed by each entry's in:. An entry whose in: does not decode is
// skipped, and a mechanism that is not a list yields no entries.
//
// Schema validation enforces that an entry carries exactly one of value: and
// from:, and the rules asked while the documents are resolved refuse an entry
// carrying both before any reader runs. Where both still reach this decode — a
// block handed straight to the renderer rather than through a build — both
// populate the entry, and resolveSelfInjectEntry reads from: ahead of value:.
func normalizeSelfInjectEntries(raw any) (map[string]selfInjectEntry, error) {
	result := map[string]selfInjectEntry{}
	entries, ok := raw.([]any)
	if !ok {
		return result, nil
	}
	for _, entry := range entries {
		in, value, from, hasValue, hasFrom, ok := types.DecodeVerboseInjectEntry(entry)
		if !ok {
			continue
		}
		outEntry := selfInjectEntry{}
		if hasValue {
			outEntry.value = value
		}
		if hasFrom {
			outEntry.from = from
		}
		result[in] = outEntry
	}
	return result, nil
}

func resolveSelfInjectEntry(
	key string,
	entry selfInjectEntry,
	mechName string,
	ownProvides types.OrderedMap[string],
	siCtx SelfInjectContext,
	withheld redact.Keys,
) (string, error) {
	// from: → resolve from own provides
	if entry.from != "" {
		val, ok := ownProvides.Get(entry.from)
		if !ok {
			return "", cerrors.Unidentified(cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("%s[%q]: from key %q not found in component provides", mechName, key, entry.from), nil, nil)
		}
		return val, nil
	}

	// value: → evaluate as Go template with restricted context
	if entry.value != "" {
		resolved, err := evalSelfInjectValue(
			fmt.Sprintf("self-inject.%s[%s]", mechName, key),
			entry.value,
			siCtx,
			withheld,
		)
		if err != nil {
			return "", cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("%s[%q] value: template", mechName, key))
		}
		return resolved, nil
	}

	return "", nil
}

// selfInjectScope is the reference set a component-level self-inject value:
// template is checked against: every root SelfInjectContext defines, with no
// narrowing declared. The two pillars this surface must keep out are kept out by the
// context block's own shape rather than by a narrowing, which is why there is
// nothing here to go stale.
var selfInjectScope = templateScope{data: SelfInjectContext{}}

// evalSelfInjectValue evaluates one component-level self-inject value: template.
// It is the value-template surface in every respect but one: every diagnostic it
// raises is DEVELOPER_ERROR, because a component-level inject: block is authored on
// the Aggregate. The reference rules, the wording, the sentinel, the missing-key
// policy and the reading of numbers are all the shared ones, because the
// evaluation is valueTemplate's with only the category and the failure wrap
// replaced.
//
// withheld names what the node's Component declares sensitive among the values and
// infra the template reads, so a failing template reports without such a value.
func evalSelfInjectValue(name, tmplText string, siCtx SelfInjectContext, withheld redact.Keys) (string, error) {
	e := valueTemplate(name, tmplText, selfInjectScope.with(siCtx, withheld), "", "")
	e.category = cerrors.DeveloperError
	e.failed = func(stage templateEvalStage, err error) error {
		return cerrors.Wrap(valueTemplateRunFailure(stage, err), cerrors.DeveloperError,
			cerrors.SourceLocation{File: name}, fmt.Sprintf("template %q %s", name, stage))
	}
	return e.run()
}
