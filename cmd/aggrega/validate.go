// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/validateops"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"

	"github.com/spf13/cobra"
)

// newValidateCmd builds the `validate` verb: CORRECTNESS checking across three
// symmetric scopes. It complements `build` (which needs a complete Aggregate +
// Manifest + Release closure and emits artifacts) and `lint` (which checks
// publishing conventions):
//
//   - No argument: whole-catalog correctness — every catalog entity is loaded
//     and its schema-pillar structure, library resolution, extends chains, and
//     provider input-kind capabilities are checked.
//   - --release <path>: release-scoped correctness — the entities a Release's
//     dependency closure reaches are validated (closure-scoped pillars, extends,
//     and pre-render authored-value conformance).
//   - <entity>: single-entity correctness — one catalog entity file is checked
//     against its kind's schema and then against the checks that kind carries.
//     A required value the file leaves empty is reported first, every one at
//     once, as a placeholder still owed. A Component or Feature has its pillar
//     structure checked; a Workload, FeatureRecipe or Plugin has its shape
//     checked; and a Recipe is driven through the real compilation pipeline
//     around a synthesized minimal Release so an author sees the isolated
//     rendered output before wiring the entity into any Aggregate. Kinds outside
//     that set are refused here, naming the scope that reaches them instead.
//
// Output is deterministic and the command writes nothing to disk.
func newValidateCmd() *cobra.Command {
	var catalogDirs []string
	var releasePath string
	var pluginPath string
	var pluginType string
	var responseFormat string
	var noUnsupportedFieldWarnings bool
	var irGraph bool
	c := &cobra.Command{
		Use:   "validate [entity-path]",
		Short: "Check correctness across the catalog, a Release, or one entity",
		Long: "Check CORRECTNESS across three symmetric scopes.\n\n" +
			"With no argument, validate checks the WHOLE catalog: schema-pillar\n" +
			"structure, cross-document library resolution, extends chains, and provider\n" +
			"input-kind capabilities, over every catalog entity.\n\n" +
			"With --release, validate checks the entities a Release's dependency closure\n" +
			"reaches: closure-scoped pillars, extends, and pre-render authored-value\n" +
			"conformance — the same correctness a build applies, scoped to that Release.\n\n" +
			"With an entity path, validate checks one entity file. The kinds it accepts\n" +
			"as a single-entity target are Component, Feature, Recipe, FeatureRecipe,\n" +
			"Workload and Plugin. A value the kind requires that the file leaves empty is\n" +
			"reported first, as a placeholder still owed: every one is named at once, with\n" +
			"what it takes, read from the kind's own schema. That reading is this scope's\n" +
			"alone; a whole-catalog run, --release and a build do not apply it. Each kind\n" +
			"then has its schema checked, then the checks its own kind carries: pillar\n" +
			"structure for a Component or Feature; shape for a Workload, FeatureRecipe or\n" +
			"Plugin; and for a Recipe, shape plus an end-to-end compile against a minimal\n" +
			"generated harness, so its resolution, its guards and a render of its output\n" +
			"are all exercised. Where that Recipe's source is one that has to be fetched,\n" +
			"the render is reported as not attempted instead: validating one entity on its\n" +
			"own reads no lockfile, reads and writes no cache and makes no network request,\n" +
			"so the chart or module it names is never retrieved and there is nothing to\n" +
			"render from. Any other kind is refused here today, and the refusal names the\n" +
			"scope that checks it instead: a whole-catalog run reaches an Aggregate and the\n" +
			"library kinds when they sit in a catalog directory, and --release reaches a\n" +
			"Release, the Manifest it names and the Aggregate it deploys. The command\n" +
			"writes nothing to disk.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := ""
			if len(args) > 0 {
				path = args[0]
			}
			// The graph is a Release's, and --release is the one scope that
			// resolves one: the whole-catalog and single-entity scopes drive a
			// synthesized harness rather than a Release, and the plugin scope
			// inspects one artifact. Refusing here keeps the flag from rendering a
			// verdict and silently dropping the document that was asked for.
			if irGraph && releasePath == "" {
				return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
					fmt.Sprintf("--%s applies to the --release scope, the one scope that resolves a Release to a graph; pass --release <path-to-release.yaml>", flagIRGraph),
					nil, nil)
			}
			// --type declares the plugin type for a binary-only .wasm check and is
			// meaningful only alongside --plugin; reject it on its own so it never
			// silently no-ops on a catalog-, release-, or entity-scoped run.
			if pluginType != "" && pluginPath == "" {
				return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
					"--type applies only with --plugin; pass --plugin <path-to-.wasm> to validate a binary", nil, nil)
			}
			if pluginPath != "" {
				// Binary/entity plugin validation is a distinct scope: it checks a
				// .wasm against its own describe() self-report (and, with a Plugin
				// entity argument, against the entity's declared surface). It selects
				// neither the whole-catalog nor the release closure.
				if releasePath != "" {
					return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
						"--plugin is mutually exclusive with --release; validate a .wasm binary or a Release closure, not both", nil, nil)
				}
				return validatePluginBinary(cmd.Context(), pluginPath, pluginType, path, cmd.OutOrStdout())
			}
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			if releasePath != "" {
				// Release-closure mode is mutually exclusive with a positional
				// entity target — they select different closures.
				if path != "" {
					return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
						"--release is mutually exclusive with an entity argument; pass one or the other", nil, nil)
				}
				return validateRelease(cmd.Context(), rw, releasePath, catalogDirs, compilerops.ParseVerbosity(responseFormat), irGraph, os.Stderr, cmd.OutOrStdout())
			}
			if path == "" {
				// No entity argument → whole-catalog correctness.
				return validateCatalogWideFromWorkspace(rw, catalogDirs)
			}
			return dispatchValidate(cmd.Context(), rw, path, catalogDirs, noUnsupportedFieldWarnings, cmd.OutOrStdout())
		},
	}
	c.Flags().StringArrayVar(&catalogDirs, "catalog", nil,
		helpCatalogDir)
	c.Flags().StringVar(&releasePath, "release", "", "Path to a Release YAML to validate over its resolved dependency closure (mutually exclusive with an entity argument)")
	c.Flags().StringVar(&pluginPath, "plugin", "", "Path to a raw .wasm to validate against its describe() self-report — the pre-publish authoring check. Alone it needs --type; with a Plugin entity argument it cross-checks the binary against the entity's declared type, capability claim, command mirror, and host-function record.")
	c.Flags().StringVar(&pluginType, "type", "", "Plugin type for a binary-only --plugin check, verified against the binary's self-reported type. Only meaningful with --plugin; when a Plugin entity supplies the type, omit it (a disagreeing value halts).")
	// No back-quoted token in the usage text: pflag reads the first one as the
	// flag's value placeholder and strips its quotes, so a quoted value name here
	// would render "--response-format detailed" as the placeholder and leave any
	// later quoted name showing its literal quotes. The two accepted values are
	// named in prose instead, and the placeholder stays the type.
	c.Flags().StringVar(&responseFormat, "response-format", compilerops.VerbosityDetailed, "Output volume for the --release scope: detailed (default) reports every diagnostic plus advisory lines; concise reports the diagnostic count and the first diagnostic only. The verdict and exit code are identical for both.")
	c.Flags().BoolVar(&noUnsupportedFieldWarnings, flagNoUnsupportedFieldWarnings, false, helpNoUnsupportedFieldWarnings+". Applies to the entity scope, the one scope that renders")
	// --response-format sits next to this one and answers a different question:
	// that flag sets how much of this command's OWN report is written, and this
	// one asks for a second document beside it. Saying so in the help is what
	// keeps a reader from reading them as two spellings of one knob.
	c.Flags().BoolVar(&irGraph, flagIRGraph, false, helpIRGraph+". Applies to the --release scope, the one scope that resolves a Release to a graph. It selects a DOCUMENT, where --response-format sets how much of this command's own report is written; neither affects the verdict or the exit code")
	return c
}

// validatePluginBinary runs the plugin scope core and renders its structured
// result: the header, per-check status lines, and advisory notices go to out;
// the first blocking error (if any) is returned so the exit code reflects it.
//
// projection-omits PluginBinaryResult.Notices: the advisory notice lines are
// already inside Diagnostics, which is the ordered report this prints in full.
// Notices repeats those same lines as bare strings for a caller that summarizes
// rather than prints, so the content is carried here and reading the repeat
// would print every notice twice.
func validatePluginBinary(ctx context.Context, pluginPath, declaredType, entityPath string, out io.Writer) error {
	res, err := validateops.ValidatePluginBinary(ctx, pluginPath, declaredType, entityPath, wasm.DescribeMaxMemoryPages)
	for _, line := range res.Diagnostics {
		fmt.Fprintln(out, line)
	}
	return err
}

// dispatchValidate resolves the workspace and catalog search stack, runs the
// single-entity scope core, and renders its structured result: the report lines
// and rendered artifacts go to out (stdout), advisory notes to stderr. It
// returns the core's first blocking error so a schema break halts loudly.
func dispatchValidate(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, path string, catalogFlagDirs []string, suppressUnsupportedFieldWarnings bool, out io.Writer) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// The single-entity scope names no Release — that is the --release scope's —
	// so it reads the workspace's own stack.
	dirs := rw.ResolveCatalogDirs("", catalogFlagDirs)

	res, verr := validateops.ValidateEntity(ctx, rw.Root(), cacheDir, dirs, path, suppressUnsupportedFieldWarnings)
	for _, line := range res.Diagnostics {
		fmt.Fprintln(out, line)
	}
	for _, a := range res.Artifacts {
		fmt.Fprintf(out, "\n--- %s ---\n", a.Key)
		out.Write(a.Content)
		if len(a.Content) > 0 && a.Content[len(a.Content)-1] != '\n' {
			fmt.Fprintln(out)
		}
	}
	for _, note := range res.Notes {
		fmt.Fprintln(os.Stderr, note)
	}
	return verr
}
