// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
)

// lint command flags. Cobra populates these globals from the invocation; the
// RunE assembles them into a lintops.Config for the single-entity path and
// threads them into the release / whole-catalog cores.
var (
	lintEntityPath  string
	lintReleasePath string
	lintPrePublish  bool
	lintBaseline    string
	lintPluginPath  string
	lintCatalogDirs []string

	// Tri-state policy override flags. Both
	// `allow_*` (6) and `enforce_*` (2) families get paired CLI
	// flags. makeResolverFromCobra decides effective value per cobra
	// Flags().Changed() state.
	lintAllowDynamicRefs          bool
	lintNoAllowDynamicRefs        bool
	lintAllowIDOverrides          bool
	lintNoAllowIDOverrides        bool
	lintAllowDirectXAggregaExt    bool
	lintNoAllowDirectXAggregaExt  bool
	lintAllowAggregaAIPrefix      bool
	lintNoAllowAggregaAIPrefix    bool
	lintAllowNonstandardNames     bool
	lintNoAllowNonstandardNames   bool
	lintAllowCategoryMismatches   bool
	lintNoAllowCategoryMismatches bool
	lintEnforceImmutability       bool
	lintNoEnforceImmutability     bool
)

func newLintCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "lint [entity-path]",
		Short: "Check catalog publishing conventions across the catalog, a Release, or one entity",
		Long: `aggrega lint checks publishing CONVENTIONS — naming, category-directory
alignment, annotation prefixes, and the rest of the publishing-linter
discipline. It does not check correctness (schema, pillars, resolution,
render): that is aggrega validate.

Three invocation shapes, mirroring validate:

  aggrega lint                    conventions across every catalog entity
  aggrega lint --release <path>   conventions across a Release's dependency closure
  aggrega lint <entity>           publishing discipline for one entity

Without an entity argument, lint runs the structural conventions rules
over every catalog entity found under the catalog directories. With
--release it runs them over the entities that Release's closure reaches.
In both, findings are advisory (warning severity) and never halt.

With an entity path, lint runs the publishing-linter rule set against
that one entity. In default mode the rules run at warning severity over
local content without consulting any registry. With --pre-publish the
previous registry version is fetched and the schema-evolution
classification is compared against the proposed version bump; mismatches
halt. --pre-publish applies to the single-entity shape only. Per-workspace
policy flags from .aggrega-config.yaml's lint.publishing_discipline block
can be overridden per invocation via the --allow-*/--no-allow-* and
--enforce-*/--no-enforce-* flag pairs.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := lintEntityPath
			if path == "" && len(args) > 0 {
				path = args[0]
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			if lintReleasePath != "" {
				// Release-closure mode is mutually exclusive with a positional /
				// --entity target — they select different closures.
				if path != "" {
					return fmt.Errorf("OPS_ERROR: --release is mutually exclusive with an entity argument; pass one or the other")
				}
				// --pre-publish drives the single-entity registry-bump check; the
				// release-closure pass never consults it. Halt loudly rather than
				// silently ignore the flag.
				if lintPrePublish {
					return fmt.Errorf("OPS_ERROR: --pre-publish is mutually exclusive with --release; --pre-publish applies to a single entity, --release checks a Release closure")
				}
				return lintops.RunRelease(rw, lintReleasePath, lintCatalogDirs, cmd.OutOrStdout(), os.Stderr)
			}
			if path == "" {
				// No entity argument → whole-catalog conventions.
				return lintops.RunCatalogWide(rw, lintCatalogDirs, cmd.OutOrStdout(), os.Stderr)
			}
			if lintBaseline != "" && !lintPrePublish {
				// --baseline only shapes the pre-publish predecessor; forcing a
				// baseline without the check it feeds is a mistake, so halt loudly.
				return fmt.Errorf("OPS_ERROR: --baseline requires --pre-publish")
			}
			return lintops.RunEntity(lintops.Config{
				EntityPath:     path,
				PrePublish:     lintPrePublish,
				Baseline:       lintBaseline,
				PluginPath:     lintPluginPath,
				PolicyResolver: makeResolverFromCobra(cmd),
				Workspace:      rw,
				Out:            cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().StringArrayVar(&lintCatalogDirs, "catalog", nil,
		"Catalog directory; repeatable. Each --catalog is prepended to the workspace catalog_dirs search stack (first match wins). Used by the whole-catalog and --release conventions passes.")
	c.Flags().StringVar(&lintReleasePath, "release", "", "Path to a Release YAML to check conventions over its resolved dependency closure (mutually exclusive with an entity argument)")
	c.Flags().StringVar(&lintEntityPath, "entity", "", "Path to the entity YAML to lint (alternatively pass as the positional arg)")
	c.Flags().BoolVar(&lintPrePublish, "pre-publish", false, "Fetch the previous registry version and verify the proposed bump against the schema-evolution classification")
	c.Flags().StringVar(&lintBaseline, "baseline", "", "Diff the pre-publish evolution check against this specific already-published version instead of the auto-selected highest version below the one being published; must be a strict-SemVer tag present in the registry (requires --pre-publish)")
	c.Flags().StringVar(&lintPluginPath, "plugin", "", "Filesystem path to the publishing-linter .wasm artifact (overrides workspace plugins.linter[0].uri)")

	// Tri-state allow_* flags
	c.Flags().BoolVar(&lintAllowDynamicRefs, "allow-dynamic-refs", false, "Force allow_dynamic_refs=true for this invocation")
	c.Flags().BoolVar(&lintNoAllowDynamicRefs, "no-allow-dynamic-refs", false, "Force allow_dynamic_refs=false for this invocation")
	c.Flags().BoolVar(&lintAllowIDOverrides, "allow-id-overrides", false, "Force allow_id_overrides=true for this invocation")
	c.Flags().BoolVar(&lintNoAllowIDOverrides, "no-allow-id-overrides", false, "Force allow_id_overrides=false for this invocation")
	c.Flags().BoolVar(&lintAllowDirectXAggregaExt, "allow-direct-x-aggrega-extensions", false, "Force allow_direct_x_aggrega_extensions=true")
	c.Flags().BoolVar(&lintNoAllowDirectXAggregaExt, "no-allow-direct-x-aggrega-extensions", false, "Force allow_direct_x_aggrega_extensions=false")
	c.Flags().BoolVar(&lintAllowAggregaAIPrefix, "allow-aggrega-ai-prefix", false, "Force allow_aggrega_ai_prefix=true")
	c.Flags().BoolVar(&lintNoAllowAggregaAIPrefix, "no-allow-aggrega-ai-prefix", false, "Force allow_aggrega_ai_prefix=false")
	c.Flags().BoolVar(&lintAllowNonstandardNames, "allow-nonstandard-names", false, "Force allow_nonstandard_names=true")
	c.Flags().BoolVar(&lintNoAllowNonstandardNames, "no-allow-nonstandard-names", false, "Force allow_nonstandard_names=false")
	c.Flags().BoolVar(&lintAllowCategoryMismatches, "allow-category-mismatches", false, "Force allow_category_mismatches=true")
	c.Flags().BoolVar(&lintNoAllowCategoryMismatches, "no-allow-category-mismatches", false, "Force allow_category_mismatches=false")

	// Tri-state enforce_* flags
	c.Flags().BoolVar(&lintEnforceImmutability, "enforce-immutability", false, "Force enforce_immutability=true")
	c.Flags().BoolVar(&lintNoEnforceImmutability, "no-enforce-immutability", false, "Force enforce_immutability=false")

	return c
}

// makeResolverFromCobra returns a closure that decides effective policy values
// per cobra Flags().Changed() state. Precedence: explicit --no-X > explicit --X
// > workspace value.
func makeResolverFromCobra(cmd *cobra.Command) func(string, bool) bool {
	return func(flagBase string, wsValue bool) bool {
		if cmd.Flags().Changed("no-" + flagBase) {
			return false
		}
		if cmd.Flags().Changed(flagBase) {
			return true
		}
		return wsValue
	}
}
