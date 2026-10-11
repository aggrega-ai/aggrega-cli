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
//     provider input-kind capabilities are checked; once for each Release, over
//     that Release's directories, where the workspace gives its Releases
//     directories of their own.
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
//     With --release beside it, the same check runs in that Release's terms:
//     the entity's references resolve through the catalog directories the
//     workspace configuration gives that Release, and no closure is validated.
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
	var unredacted bool
	c := &cobra.Command{
		Use:   "validate [entity-path]",
		Short: "Check correctness across the catalog, a Release, or one entity",
		Long: "Check CORRECTNESS across three symmetric scopes.\n\n" +
			"With no argument, validate checks the WHOLE catalog: schema-pillar\n" +
			"structure, cross-document library resolution, extends chains, and provider\n" +
			"input-kind capabilities, over every catalog entity. Where the workspace\n" +
			"configuration gives its Releases catalog directories of their own under\n" +
			"workspace.releases, it checks the catalog once for each Release, over the\n" +
			"directories that Release reads, and reports each pass under a line naming\n" +
			"its Releases and those directories; Releases reading the same directories\n" +
			"share one pass.\n\n" +
			"With --release, validate checks the entities a Release's dependency closure\n" +
			"reaches: closure-scoped pillars, extends, and pre-render authored-value\n" +
			"conformance — the same correctness a build applies, scoped to that Release.\n\n" +
			"With an entity path, validate checks one entity file. The kinds it accepts\n" +
			"as a single-entity target are Component, Feature, Recipe, FeatureRecipe,\n" +
			"Workload and Plugin. A value the kind requires that the file leaves empty is\n" +
			"reported first, as a placeholder still owed: every one is named at once, with\n" +
			"what it takes, read from the kind's own schema. That reading is this scope's\n" +
			"alone; a whole-catalog run, a Release's closure and a build do not apply it.\n" +
			"Each kind then has its schema checked, then the checks its own kind carries:\n" +
			"pillar structure for a Component or Feature; shape for a Workload,\n" +
			"FeatureRecipe or Plugin; and for a Recipe, shape plus an end-to-end compile\n" +
			"against a minimal generated harness, so its resolution, its guards and a\n" +
			"render of its output are all exercised. Where that Recipe's source is one\n" +
			"that has to be fetched, the render is reported as not attempted instead:\n" +
			"validating one entity on its own reads no lockfile, reads and writes no cache\n" +
			"and makes no network request, so the chart or module it names is never\n" +
			"retrieved and there is nothing to render from. A render that stops while the\n" +
			"Recipe's templates read what only a connection or an attached Feature\n" +
			"supplies — anything under .context.inject, .context.uses or .features — is\n" +
			"reported as not attempted too: the generated harness wires no connection and\n" +
			"attaches no Feature, so that render cannot be judged there, and a fault of\n" +
			"the Recipe's own beside such a read is set aside with it. A template that\n" +
			"does not parse, a reference the render refuses and a Recipe assertion that\n" +
			"calls fail still fail. Any other kind is refused here\n" +
			"today, and the refusal names the scope that checks it instead: a whole-catalog\n" +
			"run reaches an Aggregate and the library kinds when they sit in a catalog\n" +
			"directory, and --release reaches a Release, the Manifest it names and the\n" +
			"Aggregate it deploys.\n\n" +
			"With an entity path and --release together, the entity is checked in that\n" +
			"Release's terms: its references — the Component a Recipe implements, a\n" +
			"library a pillar refers to — resolve through the catalog directories the\n" +
			"workspace configuration gives that Release rather than the workspace's own,\n" +
			"so a workspace keeping one catalog variant per Release checks an entity\n" +
			"against the variant that Release builds with. The checks are the entity\n" +
			"path's; no closure is validated.\n\n" +
			"The command writes nothing to disk.",
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
			// An entity beside --release is still the single-entity scope, checked
			// in that Release's terms, and it resolves no closure and so no graph.
			if irGraph && path != "" {
				return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
					fmt.Sprintf("--%s applies to a Release's dependency closure, and an entity argument checks that one entity in the Release's terms instead, resolving no graph; drop the entity argument to validate the Release's closure", flagIRGraph),
					nil, nil)
			}
			// The reveal reaches the graph and nothing else this command writes,
			// so without the graph it would change nothing and say nothing.
			if err := refuseUnpairedUnredacted(irGraph, unredacted); err != nil {
				return err
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
			// The run is about the Release where one is named — an entity beside it
			// is checked in its terms — and about the entity otherwise.
			subject := releasePath
			if subject == "" {
				subject = path
			}
			rw, err := workspaceForSubject(cmd.Context(), subject)
			if err != nil {
				return err
			}
			if releasePath != "" && path == "" {
				return validateRelease(cmd.Context(), rw, releasePath, catalogDirs, compilerops.ParseVerbosity(responseFormat), irGraph, unredacted, os.Stderr, cmd.OutOrStdout())
			}
			if path == "" {
				// No entity argument → whole-catalog correctness, one pass for each
				// Release where the workspace gives its Releases directories of their own.
				return validateCatalogWideFromWorkspace(rw, catalogDirs)
			}
			// An entity argument is the single-entity scope, with --release or
			// without it: beside an entity, --release names the Release whose terms
			// the entity is checked in rather than a closure to validate.
			return dispatchValidate(cmd.Context(), rw, path, releasePath, catalogDirs, noUnsupportedFieldWarnings, cmd.OutOrStdout())
		},
	}
	c.Flags().StringArrayVar(&catalogDirs, "catalog", nil,
		helpCatalogDir)
	c.Flags().StringVar(&releasePath, "release", "", "Path to a Release YAML. Alone, validate over its resolved dependency closure; with an entity argument, check that entity in the Release's terms, its references resolved through the catalog directories the workspace configuration gives that Release. "+helpReleaseResolution)
	c.Flags().StringVar(&pluginPath, "plugin", "", "Path to a raw .wasm to validate against its describe() self-report — the pre-publish authoring check. Alone it needs --type; with a Plugin entity argument it cross-checks the binary against the entity's declared type, capability claim, command mirror, and host-function record.")
	c.Flags().StringVar(&pluginType, "type", "", "Plugin type for a binary-only --plugin check, verified against the binary's self-reported type. Only meaningful with --plugin; when a Plugin entity supplies the type, omit it (a disagreeing value halts).")
	// No back-quoted token in the usage text: pflag reads the first one as the
	// flag's value placeholder and strips its quotes, so a quoted value name here
	// would render "--response-format detailed" as the placeholder and leave any
	// later quoted name showing its literal quotes. The two accepted values are
	// named in prose instead, and the placeholder stays the type.
	c.Flags().StringVar(&responseFormat, "response-format", compilerops.VerbosityDetailed, "Output volume for the --release scope, a Release's closure with no entity argument: detailed (default) reports every diagnostic plus advisory lines; concise reports the diagnostic count and the first diagnostic only. The verdict and exit code are identical for both.")
	c.Flags().BoolVar(&noUnsupportedFieldWarnings, flagNoUnsupportedFieldWarnings, false, helpNoUnsupportedFieldWarnings+". Applies to the entity scope, the one scope that renders")
	// --response-format sits next to this one and answers a different question:
	// that flag sets how much of this command's OWN report is written, and this
	// one asks for a second document beside it. Saying so in the help is what
	// keeps a reader from reading them as two spellings of one knob.
	c.Flags().BoolVar(&irGraph, flagIRGraph, false, helpIRGraph+". Applies to the --release scope, the one scope that resolves a Release to a graph. It selects a DOCUMENT, where --response-format sets how much of this command's own report is written; neither affects the verdict or the exit code")
	c.Flags().BoolVar(&unredacted, flagUnredacted, false, helpUnredacted+" Reaches the graph --"+flagIRGraph+" writes and nothing else, so it requires --"+flagIRGraph)
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
//
// release is the --release value as typed, or empty. It names the Release whose
// terms the entity is checked in, and selects nothing else: the checks are this
// scope's either way.
//
// A directory the workspace configuration lists for that stack where nothing
// exists is not in the stack the check is handed, so the check reads it as holding
// nothing and gives the verdict it gives with that directory not listed. The line
// naming it is the one `aggrega pull` and `aggrega build` print for it, composed by
// the resolver that left it out and written to stderr ahead of the check's own
// advisory notes, since the stack is decided before the check reads anything. It
// changes what a reader is told and never the verdict or the exit code.
func dispatchValidate(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, path, release string, catalogFlagDirs []string, suppressUnsupportedFieldWarnings bool, out io.Writer) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// With no --release the entity concerns no one Release and is checked over
	// the workspace's own stack; with one, over the stack the configuration gives
	// that Release, so its references resolve to what that Release builds with.
	// Asking for the Release's reach is what refuses a --release naming no file;
	// the reach itself is set aside, since the check reads no workspace document
	// beyond the entity and the catalog directories it is handed.
	if _, err := rw.ReleaseReach(release, catalogFlagDirs); err != nil {
		return err
	}
	dirs := rw.ResolveCatalogDirs(release, catalogFlagDirs)
	named := rw.NamedCatalogDirs(release, catalogFlagDirs)
	rw.NoteAbsentCatalogDirs(os.Stderr, release, catalogFlagDirs)

	res, verr := validateops.ValidateEntity(ctx, rw.Root(), cacheDir, dirs, named, path, suppressUnsupportedFieldWarnings)
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
