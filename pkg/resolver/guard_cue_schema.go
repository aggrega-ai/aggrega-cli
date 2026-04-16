// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"sort"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/cognitio/aggrega-cli/pkg/validator"
)

// guardCueSchema validates each component instance's .values (from the Aggregate)
// and .infra (from the Release boundary) against the catalog Component's cueSchema
// using the "values" and "infra" pillars respectively. Missing pillars are no-ops.
//
// Walks Aggregate components and recursively through Subsystems. Emits one
// ResolverError per validation failure; raw CUE messages are translated by the
// validator package before reaching the caller.
func guardCueSchema(agg *types.Aggregate, catalog CatalogLookup, boundaryInfra map[string]*BoundaryInfra) []*ResolverError {
	if agg == nil || catalog.Component == nil {
		return nil
	}

	var errs []*ResolverError

	// Flat walk: collect (instanceName, componentInstance) pairs, sorted.
	instances := collectComponentInstances(agg)
	for _, pair := range instances {
		comp, ok := catalog.Component(pair.inst.Type)
		if !ok {
			continue // unknown-type errors are the concern of a different guard
		}

		// Build the unified schema: generated baseUsesCueSchema &
		// generated baseSecretsCueSchema & user cueSchema. The generated bases
		// enforce socket-name / inject-mechanism / secret-slot constraints on
		// .uses and .secrets even when the user schema omits them.
		schema := validator.BuildUnifiedSchemaFull(comp.Spec.CueSchema, comp.Spec.UsesSchema, comp.Spec.SecretsSchema)
		if schema == "" {
			continue
		}

		entity := fmt.Sprintf("Component %q (instance %q, type %q)",
			comp.Metadata.Name, pair.name, pair.inst.Type)

		// Values pillar — data from the Aggregate component instance.
		valuesErrs := validator.ValidatePillar(schema, "values", pair.inst.Values, entity)
		for _, ve := range valuesErrs {
			errs = append(errs, translateValidationError(ve, pair.name))
		}

		// Infra pillar — data from the Release boundary for this instance.
		var infra map[string]interface{}
		if bi, ok := boundaryInfra[pair.name]; ok && bi != nil {
			infra = bi.Infra
		}
		infraErrs := validator.ValidatePillar(schema, "infra", infra, entity)
		for _, ve := range infraErrs {
			errs = append(errs, translateValidationError(ve, pair.name))
		}

		// Uses pillar — data from the Aggregate component instance. Validates
		// socket names exist in usesSchema and inject mechanisms are allowlisted.
		// Short-circuit when both the schema and the data are empty to avoid
		// spurious validation over unstructured map[string]interface{} conversion.
		if len(comp.Spec.UsesSchema) > 0 || len(pair.inst.Uses) > 0 {
			usesData := normalizeUsesForCUE(pair.inst.Uses)
			usesErrs := validator.ValidatePillar(schema, "uses", usesData, entity)
			for _, ve := range usesErrs {
				errs = append(errs, translateValidationError(ve, pair.name))
			}
		}

		// Secrets pillar — validates slot names declared in secretsSchema and
		// inject mechanism allowlisting (Black-Box slots reject any inject).
		// Required slots fail validation when absent. Run whenever either side
		// has content; close() on the generated pillar enforces the contract.
		if len(comp.Spec.SecretsSchema) > 0 || len(pair.inst.Secrets) > 0 {
			secretsData := normalizeSecretsForCUE(pair.inst.Secrets)
			secretErrs := validator.ValidatePillar(schema, "secrets", secretsData, entity)
			for _, ve := range secretErrs {
				errs = append(errs, translateValidationError(ve, pair.name))
			}
		}
	}

	return errs
}

// normalizeSecretsForCUE converts Aggregate `secrets` entries (map[string]SecretInstance)
// into a map[string]interface{} shape CUE can unify with the generated secrets
// pillar. Each SecretInstance's Inject (*InjectBlock) is normalized to a plain
// map[string]map[string]string so CUE sees `inject.env.KEY = "value"`-style
// entries matching the generated pillar's shape.
func normalizeSecretsForCUE(secrets map[string]types.SecretInstance) map[string]interface{} {
	if len(secrets) == 0 {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(secrets))
	for name, sec := range secrets {
		entry := map[string]interface{}{}
		if sec.Type != "" {
			entry["type"] = sec.Type
		}
		if len(sec.Values) > 0 {
			entry["values"] = sec.Values
		}
		if len(sec.Uses) > 0 {
			entry["uses"] = sec.Uses
		}
		if sec.Inject != nil {
			inj := map[string]interface{}{}
			if env := normalizeInjectMechanism(sec.Inject.Env); env != nil {
				inj["env"] = env
			}
			if file := normalizeInjectMechanism(sec.Inject.File); file != nil {
				inj["file"] = file
			}
			if arg := normalizeInjectMechanism(sec.Inject.Arg); arg != nil {
				inj["arg"] = arg
			}
			for mech, raw := range sec.Inject.Additional {
				if m := normalizeInjectMechanism(raw); m != nil {
					inj[mech] = m
				}
			}
			if len(inj) > 0 {
				entry["inject"] = inj
			}
		}
		out[name] = entry
	}
	return out
}

// normalizeInjectMechanism coerces the polymorphic InjectBlock mechanism field
// (map[string]interface{} from YAML) into map[string]string for CUE unification.
// Returns nil on unrecognized shapes so the caller can skip the mechanism entirely.
func normalizeInjectMechanism(raw interface{}) map[string]string {
	switch v := raw.(type) {
	case nil:
		return nil
	case map[string]string:
		if len(v) == 0 {
			return nil
		}
		return v
	case map[string]interface{}:
		if len(v) == 0 {
			return nil
		}
		out := make(map[string]string, len(v))
		for k, val := range v {
			if s, ok := val.(string); ok {
				out[k] = s
			}
		}
		return out
	default:
		return nil
	}
}

// normalizeUsesForCUE converts the polymorphic `uses` block (map[string]interface{})
// into a form acceptable to CUE unification via JSON. The Aggregate uses block can
// carry both the compact list form (`socket: [target1, target2]`) and the verbose
// form (`socket: {alias: {target: ..., inject: {...}}}`). The generated base schema
// expects the verbose form, so compact-style lists are skipped at this layer (an
// unknown socket would still fire in the data shape; list normalization is a
// separate concern handled by Step 2).
func normalizeUsesForCUE(uses map[string]interface{}) map[string]interface{} {
	if uses == nil {
		return map[string]interface{}{}
	}
	out := make(map[string]interface{}, len(uses))
	for socket, raw := range uses {
		// Only forward the verbose form — compact arrays don't match the
		// generated `{ [string]: { target: string } }` shape.
		if m, ok := raw.(map[string]interface{}); ok {
			out[socket] = m
		}
	}
	return out
}

// instancePair pairs a developer-chosen instance name with the authored instance.
type instancePair struct {
	name string
	inst types.ComponentInstance
}

// collectComponentInstances flattens Aggregate.Spec.Components plus all
// ComponentInstances reachable through Subsystems (recursive). Sorted by name
// for deterministic error ordering.
func collectComponentInstances(agg *types.Aggregate) []instancePair {
	var out []instancePair
	for name, inst := range agg.Spec.Components {
		out = append(out, instancePair{name: name, inst: inst})
	}
	collectFromSubsystems(agg.Spec.Subsystems, &out)
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

func collectFromSubsystems(subs map[string]types.Subsystem, out *[]instancePair) {
	for _, sub := range subs {
		for name, inst := range sub.Components {
			*out = append(*out, instancePair{name: name, inst: inst})
		}
		collectFromSubsystems(sub.Subsystems, out)
	}
}

// translateValidationError wraps a validator.ValidationError into a ResolverError,
// prefixing the field path with the component instance name so authors can locate
// the offending block directly in the Aggregate (e.g. ".components.api.values.port").
func translateValidationError(ve *validator.ValidationError, instanceName string) *ResolverError {
	field := ".components." + instanceName + ve.FieldPath
	msg := ve.Constraint
	if ve.Suggestion != "" {
		msg = msg + ". " + ve.Suggestion
	}
	return &ResolverError{
		Entity:  ve.Entity,
		Field:   field,
		Message: msg,
		Err:     ErrSchemaValidation,
	}
}
