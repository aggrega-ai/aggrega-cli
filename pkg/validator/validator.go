package validator

import (
	"github.com/cognitio/aggrega-cli/pkg/types"
)

// ValidateOption configures validation behavior.
type ValidateOption func(*validateConfig)

type validateConfig struct {
	catalogLookup func(uri string) (CatalogEntry, bool)
}

// WithCatalogLookup provides a function to resolve extends URIs to catalog entries.
func WithCatalogLookup(fn func(uri string) (CatalogEntry, bool)) ValidateOption {
	return func(c *validateConfig) {
		c.catalogLookup = fn
	}
}

// Validator performs CUE-based validation of Aggrega entities.
type Validator struct{}

// New returns a new Validator.
func New() *Validator {
	return &Validator{}
}

// ValidateComponentValues validates a component's values against its CUE schema,
// resolving extends and usesSchema as needed.
func (v *Validator) ValidateComponentValues(comp types.Component, values map[string]interface{}, opts ...ValidateOption) []*ValidationError {
	cfg := &validateConfig{}
	for _, o := range opts {
		o(cfg)
	}

	entity := "Component " + comp.Metadata.Name + ":" + comp.Metadata.Version
	var allErrs []*ValidationError

	// Resolve inheritance if extends is set
	if comp.Spec.Extends != "" && cfg.catalogLookup != nil {
		entry := CatalogEntry{
			URI:      comp.Metadata.Name + ":" + comp.Metadata.Version,
			Extends:  comp.Spec.Extends,
			Metadata: nil, // metadata merge not needed for values validation
			CUE:      comp.Spec.CueSchema,
		}
		result := ResolveInheritance(entry, cfg.catalogLookup)
		if len(result.Errors) > 0 {
			allErrs = append(allErrs, result.Errors...)
			return allErrs
		}
		// Use unified CUE for validation if inheritance produced one
		if result.UnifiedCUE != "" {
			errs := evaluateSchema(result.UnifiedCUE, values, entity)
			allErrs = append(allErrs, errs...)
		}
	} else if comp.Spec.CueSchema != "" {
		// Split Schema: unify baseUsesCueSchema with user cueSchema
		if len(comp.Spec.UsesSchema) > 0 {
			baseCUE := GenerateBaseUsesCueSchema(comp.Spec.UsesSchema)
			errs := UnifySchemas(baseCUE, comp.Spec.CueSchema, entity)
			allErrs = append(allErrs, errs...)
			if len(errs) > 0 {
				return allErrs
			}
		}

		// Evaluate schema against values
		errs := evaluateSchema(comp.Spec.CueSchema, values, entity)
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}

// ValidateFeatureValues validates a feature's values against its CUE schema.
func (v *Validator) ValidateFeatureValues(feat types.Feature, values map[string]interface{}, opts ...ValidateOption) []*ValidationError {
	cfg := &validateConfig{}
	for _, o := range opts {
		o(cfg)
	}

	entity := "Feature " + feat.Metadata.Name + ":" + feat.Metadata.Version
	var allErrs []*ValidationError

	// Resolve inheritance if extends is set
	if feat.Spec.Extends != "" && cfg.catalogLookup != nil {
		entry := CatalogEntry{
			URI:      feat.Metadata.Name + ":" + feat.Metadata.Version,
			Extends:  feat.Spec.Extends,
			Metadata: nil,
			CUE:      feat.Spec.CueSchema,
		}
		result := ResolveInheritance(entry, cfg.catalogLookup)
		if len(result.Errors) > 0 {
			allErrs = append(allErrs, result.Errors...)
			return allErrs
		}
		if result.UnifiedCUE != "" {
			errs := evaluateSchema(result.UnifiedCUE, values, entity)
			allErrs = append(allErrs, errs...)
		}
	} else if feat.Spec.CueSchema != "" {
		if len(feat.Spec.UsesSchema) > 0 {
			baseCUE := GenerateBaseUsesCueSchema(feat.Spec.UsesSchema)
			errs := UnifySchemas(baseCUE, feat.Spec.CueSchema, entity)
			allErrs = append(allErrs, errs...)
			if len(errs) > 0 {
				return allErrs
			}
		}
		errs := evaluateSchema(feat.Spec.CueSchema, values, entity)
		allErrs = append(allErrs, errs...)
	}

	return allErrs
}
