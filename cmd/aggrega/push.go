// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
	"oras.land/oras-go/v2"

	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

var (
	pushEntityPath string
	pushRegistry   string
	pushDryRun     bool
)

var pushCmd = &cobra.Command{
	Use:   "push <entity-path>",
	Short: "Bundle and publish a catalog entity to an OCI registry",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		path := pushEntityPath
		if path == "" && len(args) > 0 {
			path = args[0]
		}
		return pushRun(pushConfig{
			EntityPath: path,
			Registry:   pushRegistry,
			DryRun:     pushDryRun,
		})
	},
}

func init() {
	pushCmd.Flags().StringVar(&pushEntityPath, "entity", "", "Path to the entity YAML to publish (alternatively pass as the positional arg)")
	pushCmd.Flags().StringVar(&pushRegistry, "registry", "", "Destination registry host (overrides the workspace's default_registry)")
	pushCmd.Flags().BoolVar(&pushDryRun, "dry-run", false, "Compute the bundle and print the proposed <reference>:<tag>; skip the network call")
	rootCmd.AddCommand(pushCmd)
}

// pushConfig is the input contract for the push workflow. The cobra
// command populates it from the flag-bound globals at invocation
// time; pushRun reads ONLY from cfg (no package-level globals
// referenced inside the function body) so test bodies stay
// race-free under t.Parallel. Same shape as pullRun(pullConfig) /
// dispatchBuild(...).
type pushConfig struct {
	EntityPath string
	Registry   string
	DryRun     bool

	// PushTarget overrides the production remote.Repository
	// construction. Tests inject an in-memory oras.Target via this
	// field; nil in production. When nil and DryRun is false,
	// pushRun constructs the target via oci.NewRemoteRepository.
	PushTarget oras.Target
}

// pushEntityHeader is the minimal YAML envelope pushRun parses out
// of the entity file: kind, metadata.name, metadata.version. The
// shape mirrors the per-kind envelope pkg/oci/bundle.go uses for
// validateLibraryPerVersionSubdir; duplicated here because the push
// verb needs the same fields for every kind, not just libraries.
type pushEntityHeader struct {
	Kind     string `yaml:"kind"`
	Metadata struct {
		Name    string `yaml:"name"`
		Version string `yaml:"version"`
	} `yaml:"metadata"`
}

// pushRun bundles the entity at cfg.EntityPath and publishes it.
// Reads workspace config; parses the entity envelope (kind,
// metadata.name, metadata.version); bundles via pkg/oci.Bundle (which
// handles per-kind dispatch including library per-version subdir
// enforcement and Recipe / FeatureRecipe tar.gz directory walking);
// constructs the OCI repository reference; pushes via pkg/oci.Push.
//
// Returns wrapped errors on workspace-config validation failure,
// entity-file read / parse failure, bundle failure (including
// ErrUnknownEntityKind for non-publishable kinds and
// ErrLibraryVersionMismatch for libraries whose metadata.version
// disagrees with their per-version subdirectory), and push failure.
//
// When cfg.DryRun is true, pushRun prints the proposed
// <reference>:<tag> + mediatype + bundle byte count and returns
// without constructing a remote target. Useful for CI authoring and
// for verifying the reference-shape construction without binding to
// a registry.
func pushRun(cfg pushConfig) error {
	if cfg.EntityPath == "" {
		return fmt.Errorf("aggrega push: entity path required (pass as positional arg or via --entity)")
	}

	wsCfg, err := workspaceconfig.Load(workspaceConfigPath())
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}

	registry := cfg.Registry
	if registry == "" {
		registry = wsCfg.Workspace.DefaultRegistry
	}
	if registry == "" {
		return fmt.Errorf("aggrega push: no destination registry; set --registry or workspace.default_registry")
	}

	data, err := os.ReadFile(cfg.EntityPath)
	if err != nil {
		return fmt.Errorf("aggrega push: read %s: %w", cfg.EntityPath, err)
	}
	var header pushEntityHeader
	if err := yaml.Unmarshal(data, &header); err != nil {
		return fmt.Errorf("aggrega push: parse %s: %w", cfg.EntityPath, err)
	}
	if header.Kind == "" {
		return fmt.Errorf("aggrega push: %s: missing kind field", cfg.EntityPath)
	}
	if header.Metadata.Name == "" {
		return fmt.Errorf("aggrega push: %s: missing metadata.name", cfg.EntityPath)
	}
	if header.Metadata.Version == "" {
		return fmt.Errorf("aggrega push: %s: missing metadata.version", cfg.EntityPath)
	}

	bundleBytes, mediaType, err := oci.Bundle(header.Kind, cfg.EntityPath)
	if err != nil {
		return fmt.Errorf("aggrega push: bundle %s: %w", cfg.EntityPath, err)
	}

	reference := buildPushReference(registry, header.Kind, header.Metadata.Name)
	tag := header.Metadata.Version

	if cfg.DryRun {
		fmt.Printf("aggrega push (dry-run): %s:%s\n  kind: %s\n  mediatype: %s\n  size: %d bytes\n",
			reference, tag, header.Kind, mediaType, len(bundleBytes))
		return nil
	}

	target := cfg.PushTarget
	if target == nil {
		repo, err := oci.NewRemoteRepository(reference)
		if err != nil {
			return fmt.Errorf("aggrega push: %s: %w", reference, err)
		}
		target = repo
	}

	digest, err := oci.Push(context.Background(), target, tag, mediaType, bundleBytes)
	if err != nil {
		return fmt.Errorf("aggrega push: push %s:%s: %w", reference, tag, err)
	}

	fmt.Printf("Push complete: %s:%s (%s)\n", reference, tag, digest)
	return nil
}

// buildPushReference composes the canonical OCI repository reference
// for a catalog entity: <registry>/<kindLower>/<metadata.name>. The
// kind segment is lowercase per the mediatype convention
// (application/vnd.aggrega.<kindLower>.v1...); metadata.name may
// itself contain "/" separators (e.g., "core/server",
// "core/compute/api-server") and is preserved verbatim.
//
// The push-side construction here MUST agree with the consume-side
// catalog.SplitURIForOCI which parses URIs back into their
// (reference, tag) pair at the closure walker's Tier-3 dispatch.
// TestPushReferenceRoundTripsThroughSplitURIForOCI pins the
// symmetry across all seven publishable catalog kinds.
func buildPushReference(registry, kind, metadataName string) string {
	return registry + "/" + strings.ToLower(kind) + "/" + metadataName
}
