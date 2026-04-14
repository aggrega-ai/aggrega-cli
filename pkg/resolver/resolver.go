// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"os"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// ResolveConfig configures the Step 1 resolver pipeline.
type ResolveConfig struct {
	AggregatePath string // path to Aggregate YAML file
	ReleasePath   string // path to Release YAML file
	ManifestPath  string // path to Manifest YAML file
}

// ResolveResult is the output of the Step 1 resolver pipeline.
type ResolveResult struct {
	Aggregate     *types.Aggregate
	Release       *types.Release
	Manifest      *types.Manifest
	BoundaryInfra map[string]*BoundaryInfra // per-component boundary context
}

// Resolve executes the full Step 1 pipeline:
//  1. Load Release and Manifest
//  2. Load parametersFrom external files (if any)
//  3. Merge parameters (Release.parameters + external)
//  4. Read raw Aggregate YAML and interpolate parameters
//  5. Unmarshal interpolated Aggregate
//  6. Validate instance names
//  7. Wire boundary infra from Release
//  8. Purge parameters from memory
func Resolve(cfg ResolveConfig) (*ResolveResult, error) {
	// 1. Load Release
	release, err := LoadRelease(cfg.ReleasePath)
	if err != nil {
		return nil, err
	}

	// 1b. Load Manifest
	manifest, err := LoadManifest(cfg.ManifestPath)
	if err != nil {
		return nil, err
	}

	// 2. Load parametersFrom
	var externalParams map[string]interface{}
	if len(release.Spec.ParametersFrom) > 0 {
		externalParams, err = LoadParametersFrom(release.Spec.ParametersFrom)
		if err != nil {
			return nil, err
		}
	}

	// 3. Merge parameters
	params := MergeParameters(release.Spec.Parameters, externalParams)

	// 4. Read raw Aggregate YAML and interpolate
	rawAgg, err := os.ReadFile(cfg.AggregatePath)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   cfg.AggregatePath,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad,
		}
	}

	interpolated, err := InterpolateParameters(rawAgg, params)
	if err != nil {
		return nil, err
	}

	// 5. Unmarshal interpolated Aggregate
	var agg types.Aggregate
	if err := yaml.Unmarshal(interpolated, &agg); err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   cfg.AggregatePath,
			Message: fmt.Sprintf("failed to unmarshal interpolated YAML: %v", err),
			Err:     ErrYAMLLoad,
		}
	}

	// 6. Validate instance names
	nameErrs := ValidateInstanceNames(&agg)
	if len(nameErrs) > 0 {
		return nil, nameErrs[0] // return first violation
	}

	// 7. Wire boundary infra
	boundaryInfra := WireBoundaryInfra(release)

	// 8. Purge parameters
	PurgeParameters(&params)

	return &ResolveResult{
		Aggregate:     &agg,
		Release:       release,
		Manifest:      manifest,
		BoundaryInfra: boundaryInfra,
	}, nil
}
