// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"encoding/json"
	"fmt"
	"strings"

	libschema "github.com/google/jsonschema-go/jsonschema"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	jsonvalidator "github.com/aggrega-ai/aggrega-cli/pkg/validator/jsonschema"
)

// ProviderLookup resolves a provider by name (e.g. "helm", "helm-chart").
type ProviderLookup func(name string) (types.Provider, bool)

// isSourceless reports whether a Recipe is in generative (sourceless) mode.
// Source absent/nil → sourceless. Source non-empty string/object → sourced.
// Empty-string source is a fatal halt condition handled elsewhere (ErrEmptySource).
func isSourceless(recipe *types.Recipe) bool {
	if recipe == nil {
		return true
	}
	if recipe.Spec.Source == nil {
		return true
	}
	if s, ok := recipe.Spec.Source.(string); ok {
		return s == "" // empty string → treat as sourceless for validation purposes;
		// the dedicated helm provider will reject empty-string source with ErrEmptySource.
	}
	return false
}

// ValidateValuesAgainstBaseSchema validates a Recipe's resolved values
// dictionary against the bound Provider's baseSchema in sourceless
// (generative) mode. The provider's baseSchema is authored as JSON Schema
// 2020-12; this function parses it via
// pkg/compactform.Compile and runs the resulting schema against the
// supplied values dict using the pkg/validator/jsonschema adapter.
//
// Returns nil on success, when validation is skipped (provider missing,
// no ProviderLookup supplied, or baseSchema empty), or when the schema
// body compiles to an empty schema. Returns a CATALOG_ERROR-wrapped
// validation error on findings, with the consumer file path threaded
// through so operators locate the offending Recipe from the diagnostic.
//
// Sourceless-mode-only by construction — the caller (Phase B in the
// orchestrator) guards on isSourceless before invoking. Workload-mode
// validation is covered by ValidateWorkload + ValidateWorkloadSecrets;
// sourced-mode validation is covered by the upstream chart/module
// renderer accepting the values dict on its own terms.
func ValidateValuesAgainstBaseSchema(
	providerName string,
	providerLookup ProviderLookup,
	values map[string]any,
	consumerLabel string,
) error {
	if providerLookup == nil {
		return nil
	}
	p, found := providerLookup(providerName)
	if !found {
		return nil
	}
	if strings.TrimSpace(p.Spec.BaseSchema) == "" {
		return nil
	}

	var bodyNode yaml.Node
	if err := yaml.Unmarshal([]byte(p.Spec.BaseSchema), &bodyNode); err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: consumerLabel},
			fmt.Sprintf("provider %q baseSchema YAML parse", providerName))
	}

	schemaMap, err := compactform.Compile(&bodyNode)
	if err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: consumerLabel},
			fmt.Sprintf("provider %q baseSchema compactform compile", providerName))
	}
	if len(schemaMap) == 0 {
		return nil
	}

	schemaJSON, err := json.Marshal(schemaMap)
	if err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: consumerLabel},
			fmt.Sprintf("provider %q baseSchema marshal", providerName))
	}
	libSchema := &libschema.Schema{}
	if err := json.Unmarshal(schemaJSON, libSchema); err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: consumerLabel},
			fmt.Sprintf("provider %q baseSchema unmarshal", providerName))
	}
	v, err := jsonvalidator.New(libSchema)
	if err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: consumerLabel},
			fmt.Sprintf("provider %q baseSchema validator construction", providerName))
	}

	instance, err := normaliseInstance(values)
	if err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: consumerLabel},
			fmt.Sprintf("provider %q valuesMapping output JSON normalisation", providerName))
	}

	rpt, err := v.Validate(instance, nil)
	if err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: consumerLabel},
			fmt.Sprintf("provider %q baseSchema validate", providerName))
	}
	if rpt == nil || len(rpt.Findings) == 0 {
		return nil
	}

	parts := make([]string, 0, len(rpt.Findings))
	for _, f := range rpt.Findings {
		fieldPath := f.FieldPath
		if fieldPath == "" {
			fieldPath = "."
		}
		parts = append(parts, fmt.Sprintf("%s: %s", fieldPath, f.Message))
	}
	return cerrors.New(
		cerrors.CatalogError,
		cerrors.SourceLocation{File: consumerLabel},
		fmt.Sprintf("provider %q baseSchema validation rejected valuesMapping output:\n  - %s",
			providerName, strings.Join(parts, "\n  - ")),
		nil,
		nil,
	)
}

// normaliseInstance round-trips the values dict through JSON so the
// jsonschema adapter's walker sees the canonical map[string]any /
// []any / scalar shape regardless of what Go-typed structures the
// caller built. Mirrors the round-trip in ValidateWorkload.
func normaliseInstance(values map[string]any) (any, error) {
	if values == nil {
		return map[string]any{}, nil
	}
	raw, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
