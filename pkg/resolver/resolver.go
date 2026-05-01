// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"os"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// ResolveConfig configures the Step 1 resolver pipeline. All three paths are
// required; relative paths resolve against the working directory of the
// caller. The typical caller (cmd/aggrega/build.go) discovers AggregatePath
// and ManifestPath from a pre-loaded Release.
type ResolveConfig struct {
	AggregatePath string // path to Aggregate YAML file
	ReleasePath   string // path to Release YAML file
	ManifestPath  string // path to Manifest YAML file
}

// ResolveResult carries the artifacts produced by Step 1. The result is
// passed by pointer through topology, IR, and renderer; mutations after
// Resolve returns are not expected.
//
// Once Resolve returns, the parameter map used during interpolation has
// been actively zeroed and nilled (see PurgeParameters). Parameter values
// do not appear in any field of ResolveResult — they survive only in the
// already-interpolated YAML bytes that produced Aggregate.
type ResolveResult struct {
	Aggregate     *types.Aggregate
	Release       *types.Release
	Manifest      *types.Manifest
	BoundaryInfra map[string]*BoundaryInfra // per-component boundary context
}

// Resolve executes the Step 1 pipeline against the configured paths and
// returns the resolved artifacts. The pipeline is:
//
//  1. Load Release.
//  2. Load Manifest.
//  3. Load parametersFrom external files (deep-merge in declared order;
//     later files override earlier).
//  4. Merge Release.parameters over the external map (Release wins per key).
//  5. Read raw Aggregate YAML bytes.
//  6. Interpolate the merged parameter map into the raw bytes via Go
//     template + Sprig (missingkey=zero, so undefined references render
//     as empty rather than failing).
//  7. Unmarshal the interpolated bytes into a typed Aggregate.
//  8. Validate user-authored instance names against the dot-free pattern.
//  9. Wire per-component boundary infra from the Release boundary tree.
//  10. Actively zero-and-nil the parameter map before returning.
//
// Error semantics:
//
//   - Any I/O, unmarshal, or template error short-circuits the pipeline
//     and is returned wrapped in a ResolverError with the source entity
//     and path attached.
//   - Instance-name validation returns only the first violation — the name
//     pattern is a hard gate, and a single illegal name implies the
//     Aggregate cannot be processed further.
//   - Semantic guards (uses sockets, inject keys, requiredFeatures, etc.)
//     are NOT run by Resolve. Callers invoke RunGuards separately after
//     Resolve to obtain the catalog-aware checks; RunGuards collects all
//     violations rather than short-circuiting.
//
// Steps must run in this order: name validation operates on the resolved
// YAML, so it must follow interpolation and unmarshal; boundary infra
// wiring depends on the Release; the parameter purge must run after every
// step that consumes parameters.
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
