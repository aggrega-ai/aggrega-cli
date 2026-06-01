// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"fmt"
	"sort"
	"strings"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// ResolveAttachmentInject delivers authored attachment-level inject
// (feature.inject / secret.inject) into the node's delivery pipe.
//
// A feature/secret-level inject value is a provides-key reference resolved
// against the attachment's OWN resolved provides — its FeatureRecipe's provides
// evaluated against the attachment's values/infra/locals — delivered into the
// host component's .context.inject under the local delivery key. This is
// distinct from uses[].inject (a named dependency target's provides, resolved
// in Pass 2b) and from component.inject (the host's own provides / literals,
// resolved in Pass 2a).
//
// The pipe already carries the Pass 2a (self) and Pass 2b (uses) delivery keys
// when this runs, so a delivery key already present is a duplicate across
// developer-authored sources and halts with ErrAttachmentInjectKeyCollision. The
// FeatureRecipe injectMapping interception in Phase A is a later, last-wins
// overlay and is intentionally not a collision. Attachments are processed
// features-then-secrets in IR (author) order for deterministic emission.
func ResolveAttachmentInject(
	inject *types.InjectPipe,
	graph *types.IRGraph,
	nodeID string,
	recipeLookup RecipeLookup,
) error {
	if recipeLookup.FeatureRecipe == nil {
		return nil
	}
	deliver := func(entries types.OrderedMap[types.IRFeatureEntry]) error {
		for instanceName, entry := range entries.All() {
			if entry.Inject.Len() == 0 {
				continue
			}
			fr, found := recipeLookup.FeatureRecipe(entry.Type)
			if !found {
				continue
			}
			provided, err := evalAttachmentFRProvides(fr, instanceName, nodeID, entry)
			if err != nil {
				return err
			}
			if err := deliverAttachmentInjectKeys(inject, instanceName, entry.Inject, provided, nodeID); err != nil {
				return err
			}
		}
		return nil
	}
	if fvm, ok := graph.Spec.FeatureValuesMapping.Get(nodeID); ok {
		if err := deliver(fvm); err != nil {
			return err
		}
	}
	if secrets, ok := graph.Spec.Secrets.Get(nodeID); ok {
		if err := deliver(secrets); err != nil {
			return err
		}
	}
	inject.All = buildInjectAll(*inject)
	return nil
}

// evalAttachmentFRProvides evaluates the attachment FeatureRecipe's provides
// against the attachment's own scope (instance_id + values + infra + the FR's
// own evaluated locals), mirroring the boundary-scope FR provides evaluation.
// The resolved provides are the source the authored inject's provides-key
// references resolve against.
func evalAttachmentFRProvides(fr *types.FeatureRecipe, instanceName, nodeID string, entry types.IRFeatureEntry) (types.OrderedMap[string], error) {
	scope := ProvidesContext{
		Context: ProvidesContextBlock{InstanceID: nodeID},
		Values:  entry.Values,
		Infra:   entry.Infra,
		Locals:  types.OrderedMap[string]{},
	}
	for k, tmplText := range fr.Spec.Locals.All() {
		out, err := EvalTemplate(fmt.Sprintf("attach-inject-local[%s.%s]", instanceName, k), tmplText, scope)
		if err != nil {
			return types.OrderedMap[string]{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("node %q attachment %q locals[%q]", nodeID, instanceName, k))
		}
		scope.Locals.Set(k, strings.TrimSpace(out))
	}
	provided := types.OrderedMap[string]{}
	for k, tmplText := range fr.Spec.Provides.All() {
		out, err := EvalTemplate(fmt.Sprintf("attach-inject-provides[%s.%s]", instanceName, k), tmplText, scope)
		if err != nil {
			return types.OrderedMap[string]{}, cerrors.Wrap(err, cerrors.CatalogError, cerrors.SourceLocation{File: fr.Metadata.Name}, fmt.Sprintf("node %q attachment %q provides[%q]", nodeID, instanceName, k))
		}
		provided.Set(k, strings.TrimSpace(out))
	}
	return provided, nil
}

// deliverAttachmentInjectKeys resolves each delivery key's provides-key
// reference against the attachment's resolved provides and sets the resolved
// value into the pipe under the local delivery key. A delivery key already
// present in the pipe (from self-inject, uses-inject, or a prior attachment)
// halts with ErrAttachmentInjectKeyCollision. An unresolved provides-key (absent from the
// attachment's provides) delivers nothing (missingkey=zero semantics).
// Mechanisms are processed env, file, arg, then custom mechanisms in sorted
// order for deterministic emission.
func deliverAttachmentInjectKeys(inject *types.InjectPipe, instanceName string, entryInject types.OrderedMap[types.OrderedMap[string]], provided types.OrderedMap[string], nodeID string) error {
	set := func(dest *types.OrderedMap[string], mech, localKey, providesKey string) error {
		if _, exists := dest.Get(localKey); exists {
			return cerrors.Wrap(
				fmt.Errorf("%w: key %q in mechanism %q (attachment %q)", ErrAttachmentInjectKeyCollision, localKey, mech, instanceName),
				cerrors.DeveloperError, cerrors.SourceLocation{File: nodeID},
				fmt.Sprintf("node %q attachment %q inject delivery-key collision", nodeID, instanceName))
		}
		val, ok := provided.Get(providesKey)
		if !ok {
			return nil
		}
		dest.Set(localKey, val)
		return nil
	}
	for _, mt := range []struct {
		name string
		dest *types.OrderedMap[string]
	}{{"env", &inject.Env}, {"file", &inject.File}, {"arg", &inject.Arg}} {
		mechMap, ok := entryInject.Get(mt.name)
		if !ok {
			continue
		}
		for localKey, providesKey := range mechMap.All() {
			if err := set(mt.dest, mt.name, localKey, providesKey); err != nil {
				return err
			}
		}
	}
	var customMechs []string
	for mech := range entryInject.All() {
		if mech == "env" || mech == "file" || mech == "arg" {
			continue
		}
		customMechs = append(customMechs, mech)
	}
	sort.Strings(customMechs)
	for _, mech := range customMechs {
		mechMap, _ := entryInject.Get(mech)
		dst, ok := inject.Additional.Get(mech)
		if !ok {
			dst = types.OrderedMap[string]{}
		}
		for localKey, providesKey := range mechMap.All() {
			if err := set(&dst, mech, localKey, providesKey); err != nil {
				return err
			}
		}
		inject.Additional.Set(mech, dst)
	}
	return nil
}
