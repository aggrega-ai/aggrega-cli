// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package resolver

import (
	"fmt"
	"os"

	"github.com/cognitio/aggrega-cli/pkg/types"
	"gopkg.in/yaml.v3"
)

// The three Load* functions in this file are raw YAML readers: each reads a
// path, unmarshals into the typed entity, and wraps any failure as a
// ResolverError with the entity kind and source path attached. They do not
// validate against the JSON Schema envelope — schema validation is handled
// in pkg/validator against the entity's authored Schema content composed
// with the usesSchema / secretsSchema bridge synthesis.
//
// LoadRelease and LoadManifest are used directly: cmd/aggrega/build.go
// pre-loads the Release to discover the Aggregate and Manifest paths, and
// Resolve loads the Manifest as part of its pipeline. LoadAggregate exists
// for symmetry but is not used by Resolve, which reads raw Aggregate bytes
// for parameter interpolation and unmarshals them only after interpolation.

// LoadAggregate reads and unmarshals an Aggregate YAML file.
func LoadAggregate(path string) (*types.Aggregate, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	var agg types.Aggregate
	if err := yaml.Unmarshal(data, &agg); err != nil {
		return nil, &ResolverError{
			Entity:  "Aggregate",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	return &agg, nil
}

// LoadRelease reads and unmarshals a Release YAML file.
func LoadRelease(path string) (*types.Release, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	var rel types.Release
	if err := yaml.Unmarshal(data, &rel); err != nil {
		return nil, &ResolverError{
			Entity:  "Release",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	return &rel, nil
}

// LoadManifest reads and unmarshals a Manifest YAML file.
func LoadManifest(path string) (*types.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ResolverError{
			Entity:  "Manifest",
			Field:   path,
			Message: fmt.Sprintf("failed to read file: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	var man types.Manifest
	if err := yaml.Unmarshal(data, &man); err != nil {
		return nil, &ResolverError{
			Entity:  "Manifest",
			Field:   path,
			Message: fmt.Sprintf("failed to unmarshal YAML: %v", err),
			Err:     ErrYAMLLoad,
		}
	}
	return &man, nil
}
