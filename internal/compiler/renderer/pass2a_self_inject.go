// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"iter"
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

	for mech, raw := range selfInjectMechanisms(selfInject) {
		// Every mechanism resolves through the same per-key resolver; the
		// destination is the pipe's own field for env, file and arg and the
		// mechanism's own entry for any other, and a failed resolution halts
		// before keep is called.
		dest, keep := injectPipeMechanism(inject, mech)
		if err := processSelfInjectMechanism(dest, mech, raw, ownProvides, siCtx, withheld); err != nil {
			return cerrors.Wrap(err, cerrors.DeveloperError, cerrors.SourceLocation{Instance: nodeID}, fmt.Sprintf("node %q self-inject", nodeID))
		}
		keep()
	}

	// Rebuild .all
	inject.All = buildInjectAll(*inject)

	return nil
}

// selfInjectMechanisms yields each mechanism a component's own inject block
// names, with its authored entries, in the order the delivery resolves them: env,
// file and arg, each only where the block names it, then every other mechanism in
// the order customInjectMechanisms gives. Within the render it is the one
// statement of that order and of that set: the delivery, the reading of which own value each entry
// delivers, and the reading of which delivered keys carry declared-sensitive
// material all walk a block through it, so each meets the mechanisms the delivery
// resolves, in one sequence. A nil block yields nothing.
func selfInjectMechanisms(block *types.InjectBlock) iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		if block == nil {
			return
		}
		for _, typed := range []struct {
			name string
			raw  any
		}{
			{"env", block.Env},
			{"file", block.File},
			{"arg", block.Arg},
		} {
			if typed.raw == nil {
				continue
			}
			if !yield(typed.name, typed.raw) {
				return
			}
		}
		for _, mech := range customInjectMechanisms(block.Additional) {
			raw, _ := block.Additional.Get(mech)
			if !yield(mech, raw) {
				return
			}
		}
	}
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

	entries := normalizeSelfInjectEntries(raw)

	keys := entries.Keys()
	sort.Strings(keys)

	for _, key := range keys {
		entry, _ := entries.Get(key)
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

// providesKey reports the own provides key the entry delivers, when it delivers
// one. It is the one statement of which half of an entry is read: a non-empty
// from: is read ahead of value:, so an entry carrying both — which schema
// validation and the rules asked while the documents are resolved refuse before
// any render, but which a block handed straight to the renderer can still carry —
// delivers its provides key, and its value: is read only when this reports none.
// Within the render, a reader of an entry asks here rather than testing the two
// fields itself, so the render's readings of an entry agree on which half counts.
func (e selfInjectEntry) providesKey() (string, bool) {
	return e.from, e.from != ""
}

// normalizeSelfInjectEntries reads one mechanism of a component-level injection
// block: the list of {in, from?, value?} entries the inject_delivery definition
// describes, keyed by each entry's in:, in the order the author wrote them. An
// entry whose in: does not decode is skipped, a mechanism that is not a list
// yields no entries, and an in: written twice keeps its first position and its
// last entry.
//
// Within the render it is the reader of a component's own entries, so no reading
// there can come to see an entry another skips. Both halves of an entry are kept
// as written; which one is read is [selfInjectEntry.providesKey]'s answer.
func normalizeSelfInjectEntries(raw any) types.OrderedMap[selfInjectEntry] {
	result := types.OrderedMap[selfInjectEntry]{}
	entries, ok := raw.([]any)
	if !ok {
		return result
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
		result.Set(in, outEntry)
	}
	return result
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
	if from, ok := entry.providesKey(); ok {
		val, ok := ownProvides.Get(from)
		if !ok {
			return "", cerrors.Unidentified(cerrors.DeveloperError, cerrors.SourceLocation{}, fmt.Sprintf("%s[%q]: from key %q not found in component provides", mechName, key, from), nil, nil)
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
