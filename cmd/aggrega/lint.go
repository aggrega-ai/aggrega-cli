// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/internal/serve"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// lintFlags carries the `lint` command's flag state. It is constructed per
// command so two invocations in one process cannot observe each other's flag
// values. Cobra populates it from the invocation; the RunE assembles it into a
// lintops.Config for the single-entity path and threads it into the release /
// whole-catalog cores.
type lintFlags struct {
	entityPath  string
	releasePath string
	prePublish  bool
	baseline    string
	pluginPath  string
	catalogDirs []string

	// Tri-state policy override flags. Every `allow_*` policy key in the
	// workspace config's lint.publishing_discipline block gets a paired CLI
	// flag, so the two surfaces stay in step as keys are added; the pairing
	// is what effectivePolicies resolves against, keyed on the flag's base
	// name. These fields exist as the flags' bind targets; the effective
	// value is decided by makeResolverFromCobra per cobra Flags().Changed()
	// state rather than read from here.
	allowDynamicRefs          bool
	noAllowDynamicRefs        bool
	allowIDOverrides          bool
	noAllowIDOverrides        bool
	allowDirectXAggregaExt    bool
	noAllowDirectXAggregaExt  bool
	allowAggregaAIPrefix      bool
	noAllowAggregaAIPrefix    bool
	allowNonstandardNames     bool
	noAllowNonstandardNames   bool
	allowCategoryMismatches   bool
	noAllowCategoryMismatches bool

	allowUndeclaredRecipeProvides   bool
	noAllowUndeclaredRecipeProvides bool

	allowConnectionCredentialMismatches   bool
	noAllowConnectionCredentialMismatches bool

	output string
}

func newLintCmd() *cobra.Command {
	opts := &lintFlags{}
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
over every catalog entity found under the catalog directories. Where the
workspace configuration gives its Releases catalog directories of their own
under workspace.releases, it runs them once for each Release, over the
directories that Release reads, and reports each pass under a line naming
its Releases and those directories; Releases reading the same directories
share one pass. With --release it runs them over the entities that
Release's closure reaches. In both, findings are advisory (warn severity)
and never halt.

With an entity path, lint runs the publishing-linter rule set against
that one entity. In default mode the rules run at warn severity over
local content without consulting any registry. With --pre-publish the
previous registry version is fetched and the schema-evolution
classification is compared against the proposed version bump; mismatches
halt. --pre-publish applies to the single-entity shape only. Per-workspace
policy flags from .aggrega-config.yaml's lint.publishing_discipline block
can be overridden per invocation via the --allow-*/--no-allow-* flag pairs.

All three shapes accept -o json, which replaces the human report on standard
output with one structured document: every finding with its severity, the rule
that produced it, its short code (the code printed at the head of its row in
the human report, which aggrega explain expands), its message, and the entity
file it concerns, plus counts by severity. A finding from a rule Aggrega does
not declare, one a linter of your own adds, carries no short code, as its row
prints none. A finding carries a line and column only when the rule that
produced it located the issue that precisely; otherwise the file alone locates
it. The message is the rule's own text, carried through unchanged, so the
structured and human renderings never word a finding differently. The exit
status is the same under either format.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			format, err := parseFormat("lint", opts.output, "text", "json")
			if err != nil {
				return err
			}
			structured := format == "json"
			path := opts.entityPath
			if path == "" && len(args) > 0 {
				path = args[0]
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}

			// On the structured surface stdout carries exactly one JSON document, so
			// the operation's own confirmation and summary lines are routed off it and
			// the report is written in their place. The findings report still reaches
			// stderr, where a text diagnostic alongside machine-readable stdout is
			// where a consumer expects it.
			var collect *lintops.FindingCollector
			opOut := cmd.OutOrStdout()
			if structured {
				collect = &lintops.FindingCollector{}
				opOut = io.Discard
			}
			// emit writes the structured report, when one was asked for and the run got
			// far enough to produce it, and returns the run's own verdict unchanged. A
			// run that failed before any rule executed emits no report: an empty one
			// would claim a clean lint over a lint that never happened.
			emit := func(runErr error) error {
				if !structured || !collect.Reached() {
					return runErr
				}
				if werr := writeStructured(cmd.OutOrStdout(), "lint", collect.Report()); werr != nil {
					return werr
				}
				return runErr
			}

			if opts.releasePath != "" {
				// Release-closure mode is mutually exclusive with a positional /
				// --entity target — they select different closures.
				if path != "" {
					return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
						"--release is mutually exclusive with an entity argument; pass one or the other", nil, nil)
				}
				// --pre-publish drives the single-entity registry-bump check; the
				// release-closure pass never consults it. Halt loudly rather than
				// silently ignore the flag.
				if opts.prePublish {
					return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
						"--pre-publish is mutually exclusive with --release; --pre-publish applies to a single entity, --release checks a Release closure", nil, nil)
				}
				return emit(lintops.RunRelease(cmd.Context(), rw, opts.releasePath, opts.catalogDirs, makeResolverFromCobra(cmd), opOut, os.Stderr, collect))
			}
			if path == "" {
				// No entity argument → whole-catalog conventions, one pass for each
				// Release where the workspace gives its Releases directories of their own.
				return emit(lintops.RunCatalogWide(cmd.Context(), rw, serve.WholeCatalogPasses(rw, opts.catalogDirs), opOut, os.Stderr, collect))
			}
			if opts.baseline != "" && !opts.prePublish {
				// --baseline only shapes the pre-publish predecessor; forcing a
				// baseline without the check it feeds is a mistake, so halt loudly.
				return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
					"--baseline requires --pre-publish", nil, nil)
			}
			return emit(lintops.RunEntity(cmd.Context(), lintops.Config{
				EntityPath:     path,
				PrePublish:     opts.prePublish,
				Baseline:       opts.baseline,
				PluginPath:     opts.pluginPath,
				PolicyResolver: makeResolverFromCobra(cmd),
				Workspace:      rw,
				Out:            opOut,
				Collect:        collect,
			}))
		},
	}
	c.Flags().StringArrayVar(&opts.catalogDirs, "catalog", nil,
		helpCatalogDir+" Used by the whole-catalog and --release conventions passes.")
	c.Flags().StringVar(&opts.releasePath, "release", "", "Path to a Release YAML to check conventions over its resolved dependency closure (mutually exclusive with an entity argument)")
	c.Flags().StringVar(&opts.entityPath, "entity", "", "Path to the entity YAML to lint (alternatively pass as the positional arg)")
	c.Flags().BoolVar(&opts.prePublish, "pre-publish", false, "Fetch the previous registry version and verify the proposed bump against the schema-evolution classification")
	c.Flags().StringVar(&opts.baseline, "baseline", "", "Diff the pre-publish evolution check against this specific already-published version instead of the auto-selected highest version below the one being published; must be a strict-SemVer tag present in the registry (requires --pre-publish)")
	c.Flags().StringVar(&opts.pluginPath, "plugin", "", "Filesystem path to the publishing-linter .wasm artifact (runs this artifact instead of the workspace plugins.linter attachments)")
	c.Flags().StringVarP(&opts.output, "output", "o", "text", "Output format: text|json.")

	// Tri-state allow_* flags
	c.Flags().BoolVar(&opts.allowDynamicRefs, "allow-dynamic-refs", false, "Force allow_dynamic_refs=true for this invocation")
	c.Flags().BoolVar(&opts.noAllowDynamicRefs, "no-allow-dynamic-refs", false, "Force allow_dynamic_refs=false for this invocation")
	c.Flags().BoolVar(&opts.allowIDOverrides, "allow-id-overrides", false, "Force allow_id_overrides=true for this invocation")
	c.Flags().BoolVar(&opts.noAllowIDOverrides, "no-allow-id-overrides", false, "Force allow_id_overrides=false for this invocation")
	c.Flags().BoolVar(&opts.allowDirectXAggregaExt, "allow-direct-x-aggrega-extensions", false, "Force allow_direct_x_aggrega_extensions=true")
	c.Flags().BoolVar(&opts.noAllowDirectXAggregaExt, "no-allow-direct-x-aggrega-extensions", false, "Force allow_direct_x_aggrega_extensions=false")
	c.Flags().BoolVar(&opts.allowAggregaAIPrefix, "allow-aggrega-ai-prefix", false, "Force allow_aggrega_ai_prefix=true")
	c.Flags().BoolVar(&opts.noAllowAggregaAIPrefix, "no-allow-aggrega-ai-prefix", false, "Force allow_aggrega_ai_prefix=false")
	c.Flags().BoolVar(&opts.allowNonstandardNames, "allow-nonstandard-names", false, "Force allow_nonstandard_names=true")
	c.Flags().BoolVar(&opts.noAllowNonstandardNames, "no-allow-nonstandard-names", false, "Force allow_nonstandard_names=false")
	c.Flags().BoolVar(&opts.allowCategoryMismatches, "allow-category-mismatches", false, "Force allow_category_mismatches=true")
	c.Flags().BoolVar(&opts.noAllowCategoryMismatches, "no-allow-category-mismatches", false, "Force allow_category_mismatches=false")
	c.Flags().BoolVar(&opts.allowUndeclaredRecipeProvides, "allow-undeclared-recipe-provides", false, "Force allow_undeclared_recipe_provides=true")
	c.Flags().BoolVar(&opts.noAllowUndeclaredRecipeProvides, "no-allow-undeclared-recipe-provides", false, "Force allow_undeclared_recipe_provides=false")
	c.Flags().BoolVar(&opts.allowConnectionCredentialMismatches, "allow-connection-credential-mismatches", false, "Force allow_connection_credential_mismatches=true")
	c.Flags().BoolVar(&opts.noAllowConnectionCredentialMismatches, "no-allow-connection-credential-mismatches", false, "Force allow_connection_credential_mismatches=false")

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
