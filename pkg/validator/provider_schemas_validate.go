// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package validator

import (
	"fmt"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/compactform"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"go.yaml.in/yaml/v3"
)

// ValidateProviderSchemas enforces the load-time structural contract on a
// Provider's workloadSchema field. It is typed as a Go string carrying JSON
// Schema 2020-12; the validator parses a non-empty body via
// pkg/compactform.Compile and surfaces any compile error wrapped in a
// CATALOG_ERROR-categorised CompilerError citing the source file. An empty
// field passes without inspection — workloadSchema is optional (helm-chart,
// terraform, and cloudformation Providers omit it).
//
// The function is invoked from pkg/catalog/catalog.go's Pass 2 alongside
// ValidateSchemaPillars. Errors collect across providers into the aggregate
// CatalogError that LoadAll returns when any Pass-2 finding fires, matching
// the collect-and-emit-all policy chosen for the Feature/Component validator
// pass.
//
// The validator's coverage is intentionally narrower than the per-pillar
// Feature/Component check: the Provider schema is a flat JSON Schema body
// (not pillar-wrapped), so the shape check + pillar-key check from
// ValidateSchemaPillars do not apply here. The compactform.Compile
// gate alone catches malformed YAML, unknown compact-form keywords,
// and structural shape failures — sufficient at load time to keep
// non-JSON-Schema-2020-12 bodies out of catalogs.
//
// The wiring exists so today's catalogs reach the same load-time
// guardrail Components and Features enjoy.
func ValidateProviderSchemas(
	providerName, filePath string,
	spec *types.ProviderSpec,
) []error {
	if spec == nil {
		return nil
	}
	var errs []error
	if e := validateProviderSchemaField("workloadSchema", providerName, filePath, spec.WorkloadSchema); e != nil {
		errs = append(errs, e)
	}
	return errs
}

// validateProviderSchemaField is the shared per-field validator. Empty
// (or whitespace-only) bodies pass without inspection. Non-empty bodies
// parse as YAML and pass through pkg/compactform; any error from either
// stage wraps into a CatalogError citing the provider, the field name,
// and the source file.
func validateProviderSchemaField(fieldName, providerName, filePath, body string) error {
	if strings.TrimSpace(body) == "" {
		return nil
	}
	var bodyNode yaml.Node
	if err := yaml.Unmarshal([]byte(body), &bodyNode); err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: filePath},
			fmt.Sprintf("Provider %q: spec.%s body is not valid YAML. "+
				"Authored as JSON Schema 2020-12; "+
				"see vocabulary/compact-form-authoring.md.",
				providerName, fieldName))
	}
	if _, err := compactform.Compile(&bodyNode, compactform.WithConsumerFile(filePath)); err != nil {
		return cerrors.Wrap(err,
			cerrors.CatalogError,
			cerrors.SourceLocation{File: filePath},
			fmt.Sprintf("Provider %q: spec.%s body failed to compile to JSON Schema 2020-12. "+
				"See vocabulary/compact-form-authoring.md for the authoring surface.",
				providerName, fieldName))
	}
	return nil
}
