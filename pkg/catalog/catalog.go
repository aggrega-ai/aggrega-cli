// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package catalog

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cognitio/aggrega-cli/internal/compiler/renderer"
	"github.com/cognitio/aggrega-cli/internal/compiler/topology"
	"github.com/cognitio/aggrega-cli/pkg/resolver"
	"github.com/cognitio/aggrega-cli/pkg/types"
	"github.com/cognitio/aggrega-cli/pkg/validator"
	"gopkg.in/yaml.v3"
)

// Catalog holds all loaded entities indexed for lookup.
type Catalog struct {
	Components     map[string]types.Component
	Features       map[string]types.Feature
	Recipes        map[string]types.Recipe        // keyed by spec.implements
	FeatureRecipes map[string]types.FeatureRecipe // keyed by spec.implements
	Aggregates     map[string]types.Aggregate     // keyed by metadata.name
	Providers      map[string]types.Provider      // keyed by metadata.name (e.g. "helm", "helm-chart")
}

// Load scans a directory recursively for .yaml files and indexes all entities.
// Delegates to LoadAll so single- and multi-directory loads share the same code path.
func Load(dir string) (*Catalog, error) {
	return LoadAll(dir)
}

// LoadAll scans one or more directories recursively and merges all entities into a single Catalog.
// Later directories override earlier ones on key collisions (last write wins).
// This supports layered catalogs like `base/` + `recipes/<variant>/` where the variant's
// Recipe by `spec.implements` replaces the base entry (if any).
func LoadAll(dirs ...string) (*Catalog, error) {
	c := &Catalog{
		Components:     make(map[string]types.Component),
		Features:       make(map[string]types.Feature),
		Recipes:        make(map[string]types.Recipe),
		FeatureRecipes: make(map[string]types.FeatureRecipe),
		Aggregates:     make(map[string]types.Aggregate),
		Providers:      make(map[string]types.Provider),
	}
	for _, dir := range dirs {
		if err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || (!strings.HasSuffix(path, ".yaml") && !strings.HasSuffix(path, ".yml")) {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read %q: %w", path, err)
			}
			return c.indexFile(data, path)
		}); err != nil {
			return nil, fmt.Errorf("catalog load %q: %w", dir, err)
		}
	}
	return c, nil
}

// entityHeader is used to peek at kind before full unmarshal.
type entityHeader struct {
	Kind string `yaml:"kind"`
}

func (c *Catalog) indexFile(data []byte, path string) error {
	var header entityHeader
	if err := yaml.Unmarshal(data, &header); err != nil {
		return nil // skip non-entity YAML files silently
	}

	switch header.Kind {
	case "Component":
		var e types.Component
		if err := yaml.Unmarshal(data, &e); err != nil {
			return fmt.Errorf("unmarshal Component %q: %w", path, err)
		}
		c.Components[e.Metadata.Name] = e
	case "Feature":
		var e types.Feature
		if err := yaml.Unmarshal(data, &e); err != nil {
			return fmt.Errorf("unmarshal Feature %q: %w", path, err)
		}
		c.Features[e.Metadata.Name] = e
	case "Recipe":
		var e types.Recipe
		if err := yaml.Unmarshal(data, &e); err != nil {
			return fmt.Errorf("unmarshal Recipe %q: %w", path, err)
		}
		// Resolve relative string sources against the Recipe file's directory so
		// co-located charts stay bundled with their Recipe (on disk today, OCI
		// artifact tomorrow). Absolute paths and non-string sources pass through.
		if s, ok := e.Spec.Source.(string); ok && s != "" && !filepath.IsAbs(s) {
			abs, err := filepath.Abs(filepath.Join(filepath.Dir(path), s))
			if err != nil {
				return fmt.Errorf("resolve Recipe %q source %q: %w", path, s, err)
			}
			e.Spec.Source = abs
		}
		c.Recipes[e.Spec.Implements] = e
	case "FeatureRecipe":
		var e types.FeatureRecipe
		if err := yaml.Unmarshal(data, &e); err != nil {
			return fmt.Errorf("unmarshal FeatureRecipe %q: %w", path, err)
		}
		c.FeatureRecipes[e.Spec.Implements] = e
	case "Aggregate":
		var e types.Aggregate
		if err := yaml.Unmarshal(data, &e); err != nil {
			return fmt.Errorf("unmarshal Aggregate %q: %w", path, err)
		}
		c.Aggregates[e.Metadata.Name] = e
	case "Provider":
		var e types.Provider
		if err := yaml.Unmarshal(data, &e); err != nil {
			return fmt.Errorf("unmarshal Provider %q: %w", path, err)
		}
		c.Providers[e.Metadata.Name] = e
	}
	// Unknown kinds silently skipped (Manifest, Release handled separately)
	return nil
}

// ProviderLookup returns a provider by name (e.g. "helm", "helm-chart").
// Returns the zero value + false if not found.
func (c *Catalog) ProviderLookup(name string) (types.Provider, bool) {
	p, ok := c.Providers[name]
	return p, ok
}

// ResolveAllExtends walks every Component and Feature in the catalog and resolves
// its extends chain in-place: merged metadata, merged spec fields, unified CUE
// schema. Must be called before the catalog is consumed by guards or the renderer
// so downstream sees the fully-inherited entity. Deterministic via sorted-name
// iteration. Collects and returns every validation error — does not short-circuit,
// so one broken chain doesn't mask other problems.
func (c *Catalog) ResolveAllExtends() []*validator.ValidationError {
	var errs []*validator.ValidationError

	compLookup := func(uri string) (types.Component, bool) {
		comp, ok := c.Components[uri]
		return comp, ok
	}
	featLookup := func(uri string) (types.Feature, bool) {
		feat, ok := c.Features[uri]
		return feat, ok
	}

	compKeys := make([]string, 0, len(c.Components))
	for k := range c.Components {
		compKeys = append(compKeys, k)
	}
	sort.Strings(compKeys)
	for _, name := range compKeys {
		comp := c.Components[name]
		if comp.Spec.Extends == "" {
			continue
		}
		resolved, vErrs := validator.ResolveComponentExtends(comp, compLookup)
		if len(vErrs) > 0 {
			errs = append(errs, vErrs...)
			continue
		}
		c.Components[name] = resolved
	}

	featKeys := make([]string, 0, len(c.Features))
	for k := range c.Features {
		featKeys = append(featKeys, k)
	}
	sort.Strings(featKeys)
	for _, name := range featKeys {
		feat := c.Features[name]
		if feat.Spec.Extends == "" {
			continue
		}
		resolved, vErrs := validator.ResolveFeatureExtends(feat, featLookup)
		if len(vErrs) > 0 {
			errs = append(errs, vErrs...)
			continue
		}
		c.Features[name] = resolved
	}

	return errs
}

// CatalogLookup returns resolver-compatible lookup callbacks.
func (c *Catalog) CatalogLookup() resolver.CatalogLookup {
	return resolver.CatalogLookup{
		Component: func(typeURI string) (types.Component, bool) {
			comp, ok := c.Components[typeURI]
			return comp, ok
		},
		Feature: func(typeURI string) (types.Feature, bool) {
			feat, ok := c.Features[typeURI]
			return feat, ok
		},
	}
}

// RecipeLookup returns renderer-compatible lookup callbacks.
func (c *Catalog) RecipeLookup() renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(componentType string) (*types.Recipe, bool) {
			r, ok := c.Recipes[componentType]
			if !ok {
				return nil, false
			}
			return &r, true
		},
		FeatureRecipe: func(featureType string) (*types.FeatureRecipe, bool) {
			fr, ok := c.FeatureRecipes[featureType]
			if !ok {
				return nil, false
			}
			return &fr, true
		},
	}
}

// SubAggregateLoader returns a topology-compatible loader callback.
func (c *Catalog) SubAggregateLoader() topology.SubAggregateLoader {
	return func(typeName string) (*types.Aggregate, error) {
		agg, ok := c.Aggregates[typeName]
		if !ok {
			return nil, fmt.Errorf("sub-aggregate %q not found in catalog", typeName)
		}
		return &agg, nil
	}
}
