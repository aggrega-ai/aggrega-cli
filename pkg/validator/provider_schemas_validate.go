// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package validator

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// ValidatePluginProviderSchemas enforces the load-time structural
// contract on a Plugin's spec.provider.workloadSchema field. When a
// provider-rendering Plugin declares a spec.provider block with a non-empty
// workloadSchema, the field is a Go string carrying JSON Schema 2020-12;
// the validator compiles a non-empty body via pkg/compactform and surfaces
// any compile error wrapped in a CATALOG_ERROR-categorised CompilerError
// citing the source file. A Plugin with no spec.provider block, or an empty
// workloadSchema, passes without inspection — the block is optional and
// non-synthesizing provider plugins omit the schema.
//
// The function is invoked from pkg/catalog/catalog.go's pillar validation once
// per loaded Plugin. Errors collect across plugins into the aggregate
// CatalogError that LoadAll returns, matching the collect-and-emit-all
// policy the Feature/Component validator pass uses. The body-compilation
// contract lives in validateProviderSchemaField.
func ValidatePluginProviderSchemas(
	pluginName, filePath string,
	spec *types.PluginSpec,
) []error {
	// The provider block is held by value, so a plugin that declares none carries
	// a zero block whose workloadSchema is empty — which the per-field validator
	// already passes without inspection. Only the spec pointer needs a guard.
	if spec == nil {
		return nil
	}
	var errs []error
	if e := validateProviderSchemaField("Plugin", "provider.workloadSchema", pluginName, filePath, spec.Provider.WorkloadSchema); e != nil {
		errs = append(errs, e)
	}
	return errs
}

// validateProviderSchemaField is the per-field validator for
// workloadSchema-shaped fields carrying JSON Schema 2020-12 (the Plugin's
// spec.provider.workloadSchema).
// Empty (or whitespace-only) bodies pass without inspection. Non-empty
// bodies parse as YAML and pass through pkg/compactform; any error from
// either stage wraps into a CatalogError citing the entity kind, the
// entity name, the field name, the source file, and the underlying
// diagnostic. The cause is interpolated into the message rather than only
// wrapped for errors.Is, because it carries the whole of what the author
// must act on — which anchor closes a cycle, which line does not parse —
// and a reader of the rendered message is the only consumer that can act
// on it.
func validateProviderSchemaField(entityKind, fieldName, entityName, filePath, body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var bodyNode yaml.Node
	if err := yaml.Unmarshal([]byte(body), &bodyNode); err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: filePath},
			fmt.Sprintf("%s %q: spec.%s body is not valid YAML: %s. "+
				"Authored as JSON Schema 2020-12; "+
				"vocabulary/compact-form-authoring.md describes the authoring surface.",
				entityKind, entityName, fieldName, err.Error()))
	}
	if _, err := compactform.Compile(&bodyNode, compactform.WithConsumerFile(filePath)); err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: filePath},
			fmt.Sprintf("%s %q: spec.%s body failed to compile to JSON Schema 2020-12: %s. "+
				"See vocabulary/compact-form-authoring.md for the authoring surface.",
				entityKind, entityName, fieldName, err.Error()))
	}
	return nil
}
