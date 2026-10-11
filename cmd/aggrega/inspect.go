// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/ir"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compilerops"
	"github.com/aggrega-ai/aggrega-cli/internal/redact"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

func newInspectCmd() *cobra.Command {
	// Flag storage is per-command-instance, not package-global, so constructing
	// the command concurrently (parallel tests calling newInspectCmd) never
	// races on a shared binding target. The RunE closure captures these locals,
	// resolves them into a single inspectOptions value, and threads that struct
	// onward; the locals never reach a per-kind renderer.
	var (
		catalogDirs  []string
		releaseFlag  string
		listFlag     bool
		detailFlag   bool
		rawFlag      bool
		resolvedFlag bool
		colorFlag    string
		noColorFlag  bool
		outputFlag   string
		pluginFlag   string

		showImplementsFlag bool
		flatFlag           bool
		unredactedFlag     bool
		irGraphFlag        bool
		closureFlag        bool
	)
	c := &cobra.Command{
		Use:   "inspect [identifier]",
		Short: "Inspect a catalog entity by name, an entity file by path, or a published entity by its registry reference",
		Long: `Render a readable view of any Aggrega entity — a catalog entity by name, an
authored entity file by path, an entity published in a registry, or a
fully-resolved Release.

It answers "what does this entity actually resolve to?": which entities extend
which bases, which Recipe maps to each type, which Features attach to each node,
and where each Feature's contributions came from.

A single argument routes by its shape. A path — anything starting with ./, ../
or /, or ending in .yaml or .yml — is read as a file and inspected by its own
kind: header. A registry reference — oci://<host>/<repository>:<tag>, or
@<digest> in place of :<tag> — is read from the registry that publishes it.
Anything else is looked up by name in the catalog search stack: the
workspace's own, or, with --release, the one the workspace configuration
gives that Release, so a name several catalog variants declare, one per
Release, resolves to the variant that Release builds with.

A registry reference shows the entity exactly as it was published: unpacked
from the archive its kind is published as where it is one, and rendered by the
same view a file of that kind gets. Reading it reaches the network, using the
credentials this machine holds for that registry, and an offline workspace
refuses it. The fetched bytes are checked against the digest the artifact's
own manifest records for them, which establishes that they are the bytes that
manifest names and nothing more: no signature attached to the artifact is
verified, no withdrawal recorded against it is reported, and nothing is
written — no cache entry and no lockfile.

Three verbosity levels apply to every kind: --list for identifiers only, the
default for a structured overview, and --detail for full field bodies. --list
and --detail are mutually exclusive. Resolution depth has its own pair, --raw
for the entity exactly as authored and --resolved for the kind's full depth,
also mutually exclusive.

Note that --raw reproduces the file as written, so a value the author declared
sensitive appears as authored. That is what as-authored means; handle such
output as you would the entity file itself. --output yaml reproduces the file
the same way for every kind, a Release included.

A Release answers two further questions, and each has its own flag because
neither answer contains the other. --ir-graph asks for the IR graph a Release
resolves to — what the Release looks like, as JSON on stdout in addition to
whatever else this command produces. --closure asks for the Release's dependency
closure — every catalog entity it reaches and the version each resolved to,
which is what was pulled in to build the Release. An entity can be reached
without appearing as a node, and a node does not carry the version its type
resolved to, so holding one of the two is not holding the other.

--ir-graph is orthogonal to --output, which picks the format of the command's
own view. Values an author declared sensitive are withheld from the graph, as
they are from the readable layouts. Pass --unredacted to print them instead.`,
		Example: `  # A catalog entity by name
  aggrega inspect core/compute/api-server

  # An authored file by path, identifiers only
  aggrega inspect ./catalog/components/api-server.yaml --list

  # An entity as it was published, read straight from its registry
  aggrega inspect oci://hub.aggrega.ai/component/core/compute/api-server:1.0.0

  # A Release resolved end to end, with full field bodies
  aggrega inspect ./releases/production.yaml --detail

  # A Release exactly as authored, with no resolution
  aggrega inspect ./releases/production.yaml --raw

  # The IR graph that Release resolves to, as JSON
  aggrega inspect ./releases/production.yaml --ir-graph

  # What that Release depends on, and at which resolved versions
  aggrega inspect ./releases/production.yaml --closure`,
		// Arg-count contract enforced before RunE (so genuine misuse surfaces the
		// Usage block): exactly one identifier in the normal path; zero when
		// --plugin names a raw .wasm to derive host functions from.
		Args: func(cmd *cobra.Command, args []string) error {
			if cmd.Flags().Changed("plugin") {
				if len(args) > 0 {
					return fmt.Errorf("inspect --plugin <path-to-.wasm> takes no identifier argument")
				}
				return nil
			}
			return cobra.ExactArgs(1)(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			out := cmd.OutOrStdout()
			// --plugin <path> is the pre-publish authoring loop: derive the host
			// functions from a raw .wasm not yet published or stamped, via the same
			// derivation primitive the record-sync tooling writes with and the loader
			// registers from. It takes no identifier argument.
			if pluginFlag != "" {
				if irGraphFlag {
					return refuseIRGraph("a raw plugin artifact")
				}
				if closureFlag {
					return refuseClosure("a raw plugin artifact")
				}
				if releaseFlag != "" {
					return refuseReleaseTerms("a raw plugin artifact")
				}
				return inspectRawPlugin(cmd.Context(), out, pluginFlag)
			}
			// A name looked up in a Release's terms is about that Release; anything
			// else is about the identifier itself, which names a file when it is one.
			subject := releaseFlag
			if subject == "" {
				subject = args[0]
			}
			rw, err := workspaceForSubject(cmd.Context(), subject)
			if err != nil {
				return err
			}

			color := colorFlag
			if noColorFlag {
				color = "never"
			}
			colorMode, err := parseColorMode(color)
			if err != nil {
				return err
			}
			output, err := parseOutputFormat(outputFlag)
			if err != nil {
				return err
			}

			// Color precedence: an explicit per-command --color=always|never (or
			// the --no-color alias mapped to "never" above) is authoritative and
			// wins over the process-global decision. --color=auto (the default)
			// defers to the process-global color-off signal — set by the global
			// --no-color / --plain flags or NO_COLOR — and, failing that, to
			// whether the sink is a terminal. The global signal is the single
			// source for the NO_COLOR read; inspect does not read it separately.
			noColorSet := globalDisplay.colorOff
			opts := inspectOptions{
				level:          resolveLevel(listFlag, detailFlag),
				mode:           resolveMode(rawFlag, resolvedFlag),
				output:         output,
				color:          colorMode,
				out:            out,
				colorize:       newColorizer(colorMode, out, noColorSet),
				showImplements: showImplementsFlag,
				flat:           flatFlag,
				unredacted:     unredactedFlag,
				irGraph:        irGraphFlag,
				closure:        closureFlag,
				release:        releaseFlag,
			}
			return dispatchInspect(cmd.Context(), rw, args[0], catalogDirs, opts)
		},
	}
	c.Flags().StringArrayVar(&catalogDirs, "catalog", nil, helpCatalogDir)
	c.Flags().StringVar(&releaseFlag, "release", "", "Look a catalog name up in the terms of this Release file: through the catalog directories the workspace configuration gives that Release, with --catalog prepended. Applies to a name; a file or a registry reference is inspected as it stands and refuses it. "+helpReleaseResolution)
	c.Flags().BoolVar(&listFlag, "list", false, "Render identifiers only.")
	c.Flags().BoolVar(&detailFlag, "detail", false, "Render full field bodies.")
	c.Flags().BoolVar(&rawFlag, "raw", false, "Render the entity as authored, without resolution.")
	c.Flags().BoolVar(&resolvedFlag, "resolved", false, "Render the entity fully resolved.")
	c.Flags().StringVar(&colorFlag, "color", "auto", "Colorize output: auto|always|never.")
	c.Flags().BoolVar(&noColorFlag, "no-color", false, "Disable colorized output (alias for --color=never).")
	c.Flags().StringVarP(&outputFlag, "output", "o", "text", "Output format: text|yaml.")
	c.Flags().BoolVar(&showImplementsFlag, "show-implements", false, "For a Recipe or FeatureRecipe, resolve spec.implements against the catalog and render the bound Component or Feature.")
	c.Flags().BoolVar(&flatFlag, "flat", false, "Render a tree-shaped entity (Aggregate, Release) as a flat list, each node prefixed with its full dotted path. No effect on tabular kinds.")
	c.Flags().BoolVar(&unredactedFlag, flagUnredacted, false, helpUnredacted)
	c.Flags().BoolVar(&irGraphFlag, flagIRGraph, false, helpIRGraph+". Applies to a Release, the one kind that resolves to a graph; any other kind refuses the request rather than silently dropping it")
	c.Flags().BoolVar(&closureFlag, flagClosure, false, helpClosure+".")
	c.Flags().StringVar(&pluginFlag, "plugin", "", "Filesystem path to a raw .wasm artifact; derives and prints the host functions it imports, before `aggrega plugin sync` records them in a Plugin entity. Takes no identifier argument.")
	c.MarkFlagsMutuallyExclusive("list", "detail")
	c.MarkFlagsMutuallyExclusive("raw", "resolved")
	c.MarkFlagsMutuallyExclusive("color", "no-color")
	// The dependency closure is one of the documents this command can produce
	// about a Release, and each of these flags asks for a different one: the
	// entity as authored, the entity fully resolved, the compiled graph, or a
	// format for the command's own view. Asking for two documents in one
	// invocation is refused rather than silently answered with one of them.
	c.MarkFlagsMutuallyExclusive(flagClosure, "raw")
	c.MarkFlagsMutuallyExclusive(flagClosure, "resolved")
	c.MarkFlagsMutuallyExclusive(flagClosure, "output")
	c.MarkFlagsMutuallyExclusive(flagClosure, flagIRGraph)
	return c
}

// dispatchInspect routes the inspect command by the input form:
//
//   - Path-shaped argument (starts with ./, ../, /, or ends with .yaml /
//     .yml) → load the file, peek at kind:, dispatch to the appropriate
//     inspector branch (Component → schema; Release → per-component
//     Feature listings with provenance brackets; an embedded-only kind or
//     an unknown kind → an error saying which).
//   - Registry reference (starts with oci://) → the entity published at that
//     coordinate, read through the shared published-entity reading and
//     rendered by the same per-kind inspector a file of its kind gets.
//   - Anything else → treated as a catalog entity name, resolved by identity
//     across every entity kind (Component, Plugin, Feature, Recipe,
//     FeatureRecipe, Aggregate, SchemaLibrary, MappingLibrary, WorkloadLibrary,
//     ArtifactLibrary) and rendered by the matched kind's inspector.
//
// catalogDirs supplies the repeatable --catalog flag directories for both the
// catalog-name branch and the Release branch's pipeline; each load site resolves
// them against the workspace catalog_dirs into the first-win search stack.
//
// ctx is the run's own and is carried to every per-kind renderer, because two of
// them resolve a dependency closure and one builds a graph — work that leaves the
// process and that a reader must be able to stop.
func dispatchInspect(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, identifier string, catalogDirs []string, opts inspectOptions) error {
	// --release chooses the stack a name resolves through. A registry reference
	// is read from its registry and a file is read as it stands, so neither has a
	// stack to choose, and the flag is refused rather than silently doing nothing.
	if opts.release != "" {
		if compilerops.IsRegistryReference(identifier) {
			return refuseReleaseTerms("a registry reference")
		}
		if isPathInput(identifier) {
			return refuseReleaseTerms("a file")
		}
	}
	if compilerops.IsRegistryReference(identifier) {
		// No kind a registry publishes is a Release, so both Release-only
		// documents are refused before anything is fetched rather than after a
		// round trip that could only end the same way.
		if opts.irGraph {
			return refuseIRGraph("a published entity")
		}
		if opts.closure {
			return refuseClosure("a published entity")
		}
		return inspectPublishedEntity(ctx, rw, identifier, catalogDirs, opts)
	}

	if isPathInput(identifier) {
		return inspectPath(ctx, rw, identifier, catalogDirs, opts)
	}

	// A bare identifier resolves against the catalog, and no catalog kind is a
	// Release: a Release names an Aggregate and a Manifest beside it, so it is
	// reached as a file. Refusing here rather than rendering the entity and
	// dropping the request is what keeps either flag from quietly doing nothing.
	if opts.irGraph {
		return refuseIRGraph("a catalog entity")
	}
	if opts.closure {
		return refuseClosure("a catalog entity")
	}
	return inspectCatalogEntity(ctx, rw, identifier, catalogDirs, opts)
}

// inspectPublishedEntity reads the entity a registry reference publishes and
// renders it through the per-kind inspector a file of that kind is rendered
// through.
//
// The reading is the shared one every surface showing a published entity runs, so
// this command, the daemon and the tool protocol read the same bytes from one
// coordinate the same way — including how they refuse. What it hands back is the
// entity document with any archive already unpacked, which is exactly the input
// every per-kind inspector is written against: they read the bytes they are
// handed as a document, and this is the one place those bytes come from a
// registry rather than a file.
//
// The reference stands in for the file path. The inspectors use that value only
// to name the subject in their own diagnostics, never to read from disk, so a
// refusal from one of them names the coordinate a person typed.
//
// Under the readable layouts what was read follows the entity: the coordinate,
// whether its bytes were an archive the entity was unpacked from, the media type
// the manifest declares, and that manifest's digest. Those are facts about the
// artifact rather than about the entity, so the view itself cannot show them.
// Under --output yaml nothing follows the document, so a consumer parsing it
// receives the document and nothing else.
func inspectPublishedEntity(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, reference string, catalogDirs []string, opts inspectOptions) error {
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	// projection-omits internal/compilerops.PublishedEntity.Name: the kind's own view is rendered from the document this is read off, and names the entity in the identity block every kind's view shares, exactly as it does for a file of that kind
	// projection-omits internal/compilerops.PublishedEntity.Version: the same view names the version in that identity block, read from the same document
	// projection-omits internal/compilerops.PublishedEntity.Description: the same view prints the description beneath that identity block, read from the same document
	entity, err := compilerops.ReadPublishedEntity(ctx, rw.Config(), reference)
	if err != nil {
		return err
	}
	render, ok := kindRenderers[entity.Kind]
	if !ok {
		// Unreachable while every publishable kind has an inspector, and stated
		// rather than assumed: a kind added to the publishable set without one
		// would otherwise print nothing here.
		return fmt.Errorf("%s publishes a %s, which inspect has no view for", reference, entity.Kind)
	}
	if err := render(ctx, rw, reference, entity.Document, catalogDirs, opts); err != nil {
		return err
	}
	if opts.output != outputYAML {
		fmt.Fprintf(opts.out, "\nPublished at: %s\nPublished as: %s\nMedia type: %s\nManifest digest: %s\n",
			entity.Reference, publishedShape(entity.Archive), entity.MediaType, entity.Digest)
	}
	return nil
}

// publishedShape says, in a reader's words, how a coordinate held the entity
// shown: as an archive the document was unpacked from, or as the document itself.
func publishedShape(archive bool) string {
	if archive {
		return "an archive, which the entity was unpacked from"
	}
	return "the entity document"
}

// refuseReleaseOnly reports a request for a document only a Release resolves,
// made of a subject that resolves none, naming what does resolve one.
//
// It is one function rather than a sentence at each branch, so every place that
// can meet such a request answers it identically. The refusal is loud because the
// alternative is a flag that renders the subject's own view and silently omits
// the document the caller asked for, which reads to that caller exactly like a
// Release whose document is empty.
func refuseReleaseOnly(flag, subject, document string) error {
	return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
		fmt.Sprintf("--%s applies to a Release, and %s resolves to no %s; point inspect at a Release file to read one",
			flag, subject, document),
		nil, nil)
}

// refuseReleaseTerms reports --release given with a subject that is read as it
// stands rather than looked up through a catalog stack, naming the subject and
// what --release applies to. It is loud for the reason refuseReleaseOnly is: a
// flag that changed nothing reads to the person who typed it exactly like one
// that was honoured.
func refuseReleaseTerms(subject string) error {
	return cerrors.Unidentified(cerrors.OpsError, cerrors.SourceLocation{},
		fmt.Sprintf("--release applies to a catalog name, and %s is read as it stands rather than looked up "+
			"through the catalog directories a Release reads; pass a catalog name with --release, or drop --release",
			subject),
		nil, nil)
}

// refuseIRGraph reports a request for the IR graph made of a subject that
// resolves to none.
func refuseIRGraph(subject string) error {
	return refuseReleaseOnly(flagIRGraph, subject, "graph")
}

// refuseClosure reports a request for a dependency closure made of a subject
// that resolves none.
func refuseClosure(subject string) error {
	return refuseReleaseOnly(flagClosure, subject, "dependency closure")
}

// isPathInput returns true when the identifier names a filesystem path
// rather than a catalog entity name. Path heuristics: a leading
// directory marker (./, ../, /) or a YAML extension on the trailing
// segment.
//
// The shape test is deliberately not routed through one of the shared
// path discriminators. Each of those answers a question about a
// different authored surface, and the divergence is the point:
//
//   - resolver.IsRelativePathRef, the discriminator for a Release's
//     aggregate: and manifest: pointers, recognises ./ and ../ only.
//     It is false for a bare foo.yaml, so routing through it would
//     send `inspect foo.yaml` to the catalog branch and report that no
//     catalog entity matched — for a file in the working directory.
//   - The plugin-URI discriminator adds a leading / and is false for a
//     bare foo.yaml too, so it loses the same input.
//   - The classifier behind a Recipe's source: treats any token
//     carrying no remote scheme as local, so it is true for
//     core/devtool/adminer and for core/helm-renderer:0.4.1. Routing
//     through it would send every catalog identifier to the file
//     branch, where the read fails on something that was never a path.
//
// Probing the filesystem for the argument is worse than any of them,
// because it fails quietly rather than loudly. Where a file happens to
// sit at a path spelling the identifier, the probe takes the file
// branch, which renders the entity as authored, while the catalog
// branch resolves extends chains and carries the deprecation advisory.
// Both print an entity, so the downgrade leaves no error to read.
//
// The argument is a person's typed input and nothing records what they
// meant, so a shape test on it is the whole of what is available.
func isPathInput(identifier string) bool {
	if strings.HasPrefix(identifier, "./") || strings.HasPrefix(identifier, "../") || strings.HasPrefix(identifier, "/") {
		return true
	}
	if strings.HasSuffix(identifier, ".yaml") || strings.HasSuffix(identifier, ".yml") {
		return true
	}
	return false
}

// kindRenderer renders one entity kind authored as a standalone file. Each
// per-kind inspector is a kindRenderer registered in the kindRenderers table;
// adding a kind is a single table entry. The renderer reads the already-loaded
// bytes, the catalog search stack, and the resolved inspectOptions (which
// carry the level, mode, output format, color decision, and the output sink),
// and writes to opts.out.
//
// The first parameter is the run's context. Most kinds render from bytes already
// in hand and never consult it; the Release kind resolves a dependency closure or
// builds a graph, which is work that leaves the process. It is on the shared type
// rather than on those renderers alone so the dispatch has one shape to call, and
// so a kind that later reaches outside the process has the context already there.
type kindRenderer func(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, path string, data []byte, catalogDirs []string, opts inspectOptions) error

// kindRenderers maps an entity kind to its file-path inspector. inspectPath
// dispatches through this table; a kind absent from it is refused there, as
// embedded-only where it exists only inside another entity and as unknown
// otherwise.
var kindRenderers = map[string]kindRenderer{
	"Component":       renderComponentFile,
	"Feature":         renderFeatureFile,
	"Recipe":          renderRecipeFile,
	"FeatureRecipe":   renderFeatureRecipeFile,
	"Plugin":          renderPluginFile,
	"MappingLibrary":  renderMappingLibraryFile,
	"WorkloadLibrary": renderWorkloadLibraryFile,
	"ArtifactLibrary": renderArtifactLibraryFile,
	"SchemaLibrary":   renderSchemaLibraryFile,
	"Manifest":        renderManifestFile,
	"Aggregate":       renderAggregateFile,
	"Release":         renderReleaseFile,
}

// nonStandalone names the kinds that exist only embedded under another entity
// and so cannot be inspected as standalone files (a hard error).
var nonStandalone = map[string]bool{
	"Workload": true,
}

// inspectPath reads a YAML file, peeks at apiVersion / kind, and dispatches to
// the per-kind renderer registered for that kind. A kind that is structurally
// embedded-only (Workload) is a hard error naming the entity it embeds under;
// an unrecognized kind reports the unknown kind.
func inspectPath(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, path string, catalogDirs []string, opts inspectOptions) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %q: %w", path, err)
	}
	var preamble struct {
		APIVersion string `yaml:"apiVersion"`
		Kind       string `yaml:"kind"`
	}
	if err := yaml.Unmarshal(data, &preamble); err != nil {
		return fmt.Errorf("parse YAML preamble in %q: %w", path, err)
	}
	if preamble.Kind == "" {
		return fmt.Errorf("couldn't determine entity kind from %q; expected apiVersion/kind preamble", path)
	}

	if opts.irGraph && preamble.Kind != "Release" {
		return refuseIRGraph("a " + preamble.Kind)
	}
	if opts.closure && preamble.Kind != "Release" {
		return refuseClosure("a " + preamble.Kind)
	}

	if render, ok := kindRenderers[preamble.Kind]; ok {
		return render(ctx, rw, path, data, catalogDirs, opts)
	}
	if nonStandalone[preamble.Kind] {
		return fmt.Errorf("kind %s is not a standalone inspectable entity; %s is embedded in Recipes — inspect a Recipe to see its %s", preamble.Kind, preamble.Kind, preamble.Kind)
	}
	return fmt.Errorf("unknown entity kind %q in %q", preamble.Kind, path)
}

// inspectCatalogEntity resolves a bare identifier against the catalog across
// every entity kind and renders the first match. It loads the catalog over the
// first-win search stack, resolves extends chains, then hands the identifier to
// the shared by-name resolution, which tries the kinds in a fixed precedence and
// returns the first identity match.
//
// The precedence lives with that resolution rather than here, so this command and
// every other surface that resolves a name answer with the same entity for the
// same ambiguous identifier. What stays here is the rendering: which view each
// matched kind gets, and the withdrawal advisory that follows it.
//
// A Component match keeps its dedicated render path: the merged schema +
// usesSchema view. Every other kind is
// handed to the same per-kind inspector the file-path branch uses (via the
// kindRenderers table), so a bare identifier and a standalone file of the same
// entity render identically. For the pure-unmarshal kinds the matched in-memory
// entity is re-marshaled to YAML for that dispatch; a bare non-Component
// identifier therefore shows the entity's canonical re-marshaled form under
// --raw / --output yaml, since catalog entities retain no single authored-file
// byte source uniformly across kinds. Aggregate is the exception: its inspector
// runs the topology pass over the entity read from its source file, so it
// receives the retained source path and authored bytes rather than a re-marshal.
//
// Every kind carries the same best-effort withdrawal advisory, read from the
// local registry index at the location that kind publishes to. It is reported
// after the entity itself, for every kind this function resolves, so a withdrawn
// artifact never presents as an ordinary one — the advisory follows from
// resolving, not from the kind. A missing or unreadable index shows nothing:
// inspect never fails on the advisory.
//
// No match in any kind is a clear not-found naming the identifier and stating it
// matched no catalog entity of any kind — the bare identifier is not assumed to
// be a Component.
func inspectCatalogEntity(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, identifier string, catalogDirs []string, opts inspectOptions) error {
	// Catalog entity details print without rendering any per-node IR, so no
	// workspace-relative path normalization is needed. The resolved workspace root
	// is still supplied, because it is the anchor the configured cache directory is
	// written against: with it, the walk steps over cached library.yaml files rather
	// than showing them as authored Tier-1 catalog no matter which directory
	// inspect is invoked from.
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// Strict load (inspect must fail on a broken catalog, unlike build's tolerant
	// Pass-1) over the first-win stack: --catalog dirs prepended to catalog_dirs.
	// A name with no --release concerns no one Release, so it resolves over the
	// workspace's own stack; with one, over the stack the configuration gives that
	// Release. Asking for the Release's reach is what refuses a --release naming
	// no file; the reach itself is set aside, since nothing here walks the
	// workspace's documents.
	if _, err := rw.ReleaseReach(opts.release, catalogDirs); err != nil {
		return err
	}
	dirs := rw.ResolveCatalogDirs(opts.release, catalogDirs)
	cat, err := catalog.LoadAllWithNamedDirs(wsRoot, cacheDir, rw.NamedCatalogDirs(opts.release, catalogDirs), dirs...)
	if err != nil {
		// A load that stopped on one document reports that document's refusal once:
		// the document and the fault it carries, in the words every other surface
		// reporting the same refusal uses. Rendered as the load raised it, the one
		// fault reads as a directory frame, a step frame and the constraint, each
		// nested under the last.
		if refused, ok := compilerops.RefusedCatalogDocument(err); ok {
			return compilerops.RefusedDocument(refused.Path, refused.Err)
		}
		return fmt.Errorf("catalog load: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		return fmt.Errorf("resolve extends: %s", extErrs[0].Error())
	}
	// A name that matches nothing is refused rather than rendered as an entity
	// with nothing in it, which is what an empty view of a mistyped name would
	// read as.
	//
	// The refusal carries the workspace's account of where it reads local
	// entities, because "matched nothing" has two causes a reader cannot tell
	// apart from the sentence alone: the name is wrong, or the document holding
	// it sits somewhere this workspace does not read. Only the second is fixed by
	// moving a file or naming a directory, and only the workspace can say which
	// directories were read.
	//
	// It is decided by the one predicate every surface carrying the account
	// reads. A strict load that got this far refused no document, so there is no
	// refusal record to hold the name against, and the account is given.
	//
	// With --release the account is the one the workspace gives for that Release,
	// which names its entry under workspace.releases rather than the workspace's
	// list, since that entry is what the lookup read.
	match, found := compilerops.ResolveEntity(catalogEntityView{cat: cat, lookup: cat.CatalogLookup()}, identifier)
	if !found {
		return fmt.Errorf("%q matched no catalog entity of any kind%s", identifier,
			compilerops.LocalCatalogAdvice(rw.LocalCatalogAdviceNote(opts.release), nil, identifier, ""))
	}
	// The identity the withdrawal advisory is looked up by is read from the
	// resolved entity, never from the typed identifier, which may carry a version
	// the published location has none.
	id := entityIdentity{kind: match.Kind, provider: match.Scope, name: match.Name}
	// What is rendered is titled by the identity the resolution settled on, not by
	// the string a person typed. The two differ whenever a bare name resolves to a
	// version, which is the ordinary case: titling the output with the typed string
	// leaves the reader unable to tell which version they are looking at, and it
	// reads differently from the same entity reached as a file, where every kind's
	// renderer already titles it name:version. A resolution that settled on no
	// identity falls back to the typed string, so the title never reads as an
	// entity with no name.
	resolvedIdentifier := identifierVersion(match.Name, match.Version)
	if match.Name == "" {
		resolvedIdentifier = identifier
	}

	switch match.Kind {
	case "Component":
		// Component keeps its own render path: the structured schema view. Its
		// withdrawal advisory is the shared one every other kind gets.
		//
		// The description is handed down beside the identity because that renderer
		// takes both from its caller, and here the caller is the resolution: the
		// entity it returned is the authority on what the name describes.
		comp, _ := match.Definition.(types.Component)
		if err := renderComponentStructured(comp, resolvedIdentifier, match.Description, opts); err != nil {
			return err
		}
		reportEntityWithdrawal(rw, id, opts)
		return nil
	case "Aggregate":
		// Aggregate is a resolved-instance kind: its inspector reads the entity
		// from a filesystem path to run the topology pass, unlike the
		// pure-unmarshal renderers that read the passed bytes. The resolved match
		// carries the retained source path and authored bytes, so it is dispatched
		// directly rather than through a re-marshal.
		//
		// The catalog records that path relative to the workspace root, because a
		// machine-stable provenance string is what the IR wants. The inspector
		// opens it as a filesystem path, which resolves against the process
		// working directory — so the relative form only finds the file when the
		// command is run from the workspace root, and fails from any subdirectory
		// of the same workspace. Anchoring it here, at the site that knows the
		// root the path is relative TO, is what makes a bare Aggregate identifier
		// resolve from anywhere in the workspace, as every other kind already
		// does.
		if err := kindRenderers["Aggregate"](ctx, rw, workspaceAnchoredPath(wsRoot, match.Source), match.Bytes, catalogDirs, opts); err != nil {
			return err
		}
		reportEntityWithdrawal(rw, id, opts)
		return nil
	default:
		return renderCatalogMatch(ctx, rw, id, resolvedIdentifier, match.Definition, catalogDirs, opts)
	}
}

// catalogEntityView adapts an eagerly-loaded catalog to the accessor surface the
// shared by-name resolution reads.
//
// It exists because this command loads its catalog strictly and up front, while
// the surfaces that hold a session resolve lazily, and the two backings expose
// their accessors under different names and shapes. The adapter is the whole of
// the difference: every method here forwards to this command's own accessor, so
// the resolution reaches exactly what this command reaches and the shared part is
// only the order the kinds are tried in.
type catalogEntityView struct {
	cat    *catalog.Catalog
	lookup resolver.CatalogLookup
}

func (v catalogEntityView) Component(uri string) (types.Component, bool) {
	return v.lookup.Component(uri)
}

func (v catalogEntityView) Feature(uri string) (types.Feature, bool) {
	return v.lookup.Feature(uri)
}

func (v catalogEntityView) PluginByURI(uri string) (types.Plugin, bool) {
	return v.cat.PluginByURI(uri)
}

func (v catalogEntityView) RecipeByURI(uri string) (types.Recipe, bool) {
	return v.cat.RecipeByURI(uri)
}

func (v catalogEntityView) FeatureRecipeByURI(uri string) (types.FeatureRecipe, bool) {
	return v.cat.FeatureRecipeByURI(uri)
}

func (v catalogEntityView) AggregateLookup() resolver.AggregateLookup {
	return v.cat.AggregateLookup()
}

func (v catalogEntityView) SchemaLibrary(uri string) (types.SchemaLibrary, bool) {
	lib, ok := v.cat.SchemaLibraries[uri]
	return lib, ok
}

func (v catalogEntityView) MappingLibrary(uri string) (types.MappingLibrary, bool) {
	lib, ok := v.cat.MappingLibraries[uri]
	return lib, ok
}

func (v catalogEntityView) WorkloadLibrary(uri string) (types.WorkloadLibrary, bool) {
	lib, ok := v.cat.WorkloadLibraries[uri]
	return lib, ok
}

func (v catalogEntityView) ArtifactLibrary(uri string) (types.ArtifactLibrary, bool) {
	lib, ok := v.cat.ArtifactLibraries[uri]
	return lib, ok
}

// LocalEntityPath reports no authored document, for any kind.
//
// The eager backing records a source path for the kinds its own load-time
// validation names files in, keyed for that purpose and not exposed, so there is
// no per-kind document index here to answer from. This command reports no such
// path on any of its surfaces either: the one kind whose document it does read —
// the Aggregate, whose inspector opens the file to run the topology pass — gets
// it from that kind's own accessor, so nothing here goes unanswered.
//
// It is a truthful negative rather than a stub. The resolution takes a reported
// path where there is one and leaves the field empty where there is not, which
// is the same answer any backing gives for an entity it holds no document for.
func (v catalogEntityView) LocalEntityPath(_, _, _, _ string) (string, bool) {
	return "", false
}

// workspaceAnchoredPath resolves a catalog-recorded source path — which the
// catalog stores relative to the workspace root — into a path a filesystem read
// resolves the same way from anywhere. An already-absolute path is returned
// unchanged, and so is one carried on an empty root, since neither has a root to
// be anchored against.
//
// It is the inverse of the relative rendering the catalog applies on the way in,
// and it exists because the two forms answer different questions: the relative one
// is a machine-stable provenance string, while a read needs a locatable path. A
// consumer that reads the provenance string as though it were the second gets a
// file-not-found whose cause is the working directory rather than the file.
func workspaceAnchoredPath(workspaceRoot, path string) string {
	if path == "" || workspaceRoot == "" || filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(workspaceRoot, path)
}

// entityIdentity is a resolved catalog entity's identity in the form the OCI
// location rule takes it: the kind, the scope segment (a Recipe's or
// FeatureRecipe's capability, a Plugin's pluginType — empty for every kind that
// carries none), and the bare identity name.
//
// It exists so the per-kind derivation is stated once per kind, at the branch that
// resolved the entity and therefore holds the typed value to read it from. The
// alternative — re-deriving the scope segment further down from the identifier a
// person typed — cannot work: a Recipe identifier is capability-scoped and
// version-pinnable, so re-parsing it would rebuild by hand what the resolver
// already decided, and would rebuild it differently for the kinds whose identifier
// shape and identity shape differ.
type entityIdentity struct {
	kind     string
	provider string
	name     string
}

// reportEntityWithdrawal writes the withdrawal advisory for a resolved entity, or
// nothing when no honored marker is recorded against it. It is best-effort by
// construction: the lookup degrades to reporting nothing on an absent or unreadable
// index, so inspect never fails on the advisory.
//
// It is the one place the catalog-identity branch reports a withdrawal, so every
// kind reports on identical grounds. A kind added to the resolution ladder without
// one of these calls would render its entity and stay silent about a recorded
// withdrawal — the exact silence this exists to remove — which is why the shared
// dispatch below carries the call rather than leaving it to each branch.
//
// The machine-readable emission carries no advisory. Under --output yaml the
// subject is a YAML document a consumer parses; a text block appended after it
// would break that parse, and folding the advisory into the document would assert
// a field the entity does not have. The advisory belongs to the text surface,
// where it is read by a person.
func reportEntityWithdrawal(rw *workspaceconfig.ResolvedWorkspace, id entityIdentity, opts inspectOptions) {
	if opts.output == outputYAML {
		return
	}
	if v, ok := lookupEntityDeprecation(rw, id.kind, id.provider, id.name); ok {
		renderDeprecationBlock(opts.out, v)
	}
}

// renderCatalogMatch re-marshals a catalog-resolved entity to YAML, dispatches it
// through the kind's file-path inspector (which re-unmarshals into the same typed
// struct), and reports any withdrawal recorded against the entity's published
// location. It is the bare-name analogue of inspectPath for the pure-unmarshal
// kinds: the render logic is shared with the file branch, so a bare identifier and
// a standalone file of the same entity render identically. The identifier stands
// in for the file path (used only in the inspectors' error strings and YAML
// round-trip diagnostics, never read from disk).
//
// The withdrawal report is here rather than at each branch because this is the one
// funnel every pure-unmarshal kind passes through: a kind reaches the advisory by
// resolving, not by remembering to ask for it.
func renderCatalogMatch(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, id entityIdentity, identifier string, entity any, catalogDirs []string, opts inspectOptions) error {
	data, err := yaml.Marshal(entity)
	if err != nil {
		return fmt.Errorf("marshal %s %q: %w", id.kind, identifier, err)
	}
	if err := kindRenderers[id.kind](ctx, rw, identifier, data, catalogDirs, opts); err != nil {
		return err
	}
	reportEntityWithdrawal(rw, id, opts)
	return nil
}

// inspectReleaseFile loads a Release plus its referenced Aggregate and
// Manifest, runs the build pipeline far enough to produce the IR
// (Resolve → ExpandAnnotations → RunGuards → Topology → IR), and
// prints per-node Feature attachments with provenance brackets:
//
//	Component: api  (node: aws-prod/eks-main/payments/api)
//	  Features:
//	    - core/rewrite-host:1.0.0    [explicit]
//	    - core/metrics-exporter:1.0.0    [annotation: feature.myco/enable-metrics from release.yaml:boundaries.prod.annotations.feature.myco/enable-metrics]
//
// The catalog directory comes from the --catalog flag (default `.`),
// mirroring `aggrega build`'s catalog discovery.
//
// Release is a resolved instance kind, and two independent choices shape what
// this writes:
//
//   - --raw and --output yaml each select the Release as authored, emitted
//     verbatim via the YAML round-trip, which is what every other kind's
//     --output yaml does too. Neither runs the resolution on its own account.
//   - default / --resolved: a boundary-organised text layout — each boundary
//     carries its output directory, sync-wave, and a two-way annotation split
//     (aggrega.ai/* provider-consumed vs. other pass-through); each component
//     under a boundary carries its Aggregate component type, its uses-edges
//     (cross-boundary edges marked), and its Feature attachments with the
//     preserved provenance brackets. --flat collapses the boundary tree to one
//     section per boundary keyed by its full path.
//   - --ir-graph asks, independently of both, for the IR graph this run
//     resolved, written first and in JSON. It is additive: whichever document
//     the choices above select is still written after it.
//
// The graph leads the stream deliberately. It is one JSON document followed by
// a newline, so a consumer decoding the stream reads it and stops, whatever the
// command writes afterwards; behind the command's own view it would have to be
// found first.
//
// This function threads opts.out as the output sink and reaches the graph
// through the compiler's one graph-construction entry, so the picture it draws
// cannot drift from what a build of the same Release would render.
func renderReleaseFile(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, releasePath string, data []byte, catalogDirs []string, opts inspectOptions) error {
	// The as-authored selection. --raw is the explicit one; --output yaml is the
	// same document reached through the format flag, which is what that flag
	// means for every other kind. Only a caller that also asked for the graph
	// pays for a resolution here.
	authored := opts.mode == modeForcedRaw || opts.output == outputYAML
	if authored && !opts.irGraph {
		return emitYAMLRoundTrip(opts, releasePath, data)
	}
	// The dependency closure is a different document about the same Release, and
	// it is resolved rather than rendered: nothing here builds a graph, loads a
	// rendering guest or produces an artifact. It is reached before the pipeline
	// below because that pipeline is the cost of the node view, which a caller
	// asking for the closure did not ask for.
	if opts.closure {
		return renderReleaseClosure(ctx, rw, releasePath, catalogDirs, opts)
	}
	// wsRoot is the resolved workspace root; the catalog renders its entity
	// source-file accessors relative to it so catalog-sourced IR records read
	// consistently across machines. The root Aggregate node's own source path
	// comes from the AggregatePath label passed to the resolve funnel below,
	// not from wsRoot.
	rw, err := workspaceOrResolve(rw)
	if err != nil {
		return err
	}
	wsRoot := rw.Root()
	wsCfg := rw.Config()
	cacheDir := wsCfg.ResolvedArtifactCacheDir()
	// Strict load over this Release's first-win stack (--catalog dirs prepended to
	// the directories the workspace configuration gives it).
	dirs := rw.ResolveCatalogDirs(releasePath, catalogDirs)
	named := rw.NamedCatalogDirs(releasePath, catalogDirs)
	cat, err := catalog.LoadAllWithNamedDirs(wsRoot, cacheDir, named, dirs...)
	if err != nil {
		return fmt.Errorf("catalog load: %w", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		return fmt.Errorf("resolve extends: %s", extErrs[0].Error())
	}

	releaseDir := filepath.Dir(releasePath)
	rel, err := resolver.LoadRelease(releasePath)
	if err != nil {
		return fmt.Errorf("load release: %w", err)
	}
	// The Manifest and the Aggregate are resolved by the resolvers every other
	// surface resolves a Release's references by — a relative path beside the
	// Release, or a name looked up beside it and then, for an Aggregate, in the
	// catalog — and held to the root they hold them to. A resolver of this
	// command's own would read a name as a file name and judge no path, and the
	// picture would then be of documents a build of the same Release never reads.
	manRes, err := resolver.ResolveManifest(wsRoot, releaseDir, releasePath, rel.Spec.Manifest)
	if err != nil {
		return fmt.Errorf("load manifest: %w", err)
	}
	manifest := manRes.Manifest
	aggRes, err := resolver.ResolveAggregate(wsRoot, releaseDir, releasePath, rel.Spec.Aggregate, cat.AggregateLookup(), nil)
	if err != nil {
		return fmt.Errorf("load aggregate: %w", err)
	}
	aggPath, aggBytes := aggRes.Path, aggRes.Bytes
	catalogLookup := cat.CatalogLookup()
	// A catalog directory walk enumerates authored entities and deliberately steps
	// over the artifact cache, so a plugin this workspace acquired from a
	// registry is reachable only through a cache-backed read. Without one the entity
	// front finds no such plugin at all and a transform-role guest halts here as
	// not-an-entity — while a build of the same documents resolves it and runs it.
	// That divergence is the one thing this command promises not to produce, so the
	// read is supplied rather than left absent.
	//
	// The view is built with no registry client, so this stays a local read: a slot
	// the cache does not hold is a clean miss reported as such, never a fetch.
	catalogLookup.PluginFromCache = catalog.NewLazyLookup(wsRoot, cacheDir, dirs, nil, named...).PluginFromCacheByURI
	// One lookup over this Release's Manifest, handed to the graph build whole:
	// the build reads the Recipes through it and records on each attachment the
	// FeatureRecipe it binds, so the layout prints the realization a build of the
	// same documents renders by reading the graph rather than asking again.
	recipeLookup := cat.RecipeLookupWithManifest(manifest)

	// Build the graph through the one entry that produces one, so what this
	// command draws is the graph a build would render rather than one assembled
	// here beside it. The products are read inside the scope, while the
	// parameters are live; nothing is held past it.
	//
	// The AggregatePath / ReleasePath labels carry the source locations the
	// annotation-expansion brackets read, and CompilerVersion is the stamp the
	// graph records — supplied on the inputs like every other build-identity
	// value, rather than handed to the graph build separately.
	return pipeline.BuildGraph(
		ctx,
		pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggBytes,
			// AggregatePath is the path the reference resolved to (drives the
			// annotation source-location brackets); RootAggregateFile is the
			// workspace-relative path stamped onto the root-tier node
			// (machine-stable). The two are separate because this command needs
			// different representations for them.
			AggregatePath:     aggPath,
			ReleasePath:       releasePath,
			RootAggregateFile: catalog.RelativeToWorkspace(wsRoot, aggPath),
			CompilerVersion:   provenanceCompilerVersion(),
			// The workspace's own configuration, live, plus the root and cache
			// directory a transform-role guest resolves through. A transform role
			// shapes the graph, so drawing the graph without it would draw a
			// composition no build of the same documents produces — which is the one
			// thing this command promises not to do.
			WorkspaceConfig: &wsCfg,
			WorkspaceRoot:   wsRoot,
			// A pinned claim over an empty set, and both halves are deliberate.
			// This command inspects a workspace whose plugins a recorded closure
			// IS meant to govern, so the claim is the true one and a pin-free
			// source would state the opposite. It holds none of those pins yet,
			// because this path reads the catalog without the record — so an
			// unrecorded-identity advisory is raised for every guest it resolves,
			// which is the honest report of that gap rather than a silence.
			PluginPins: pluginpath.RecordedPins(pluginpath.PinMaps{}),
		},
		catalogLookup,
		recipeLookup,
		cat.SubAggregateLoader(),
		func(gs *pipeline.GraphScope) error {
			if opts.irGraph {
				if err := writeIRGraph(opts.out, "inspect", gs.IRGraph, opts.unredacted); err != nil {
					return err
				}
			}
			if authored {
				return emitYAMLRoundTrip(opts, releasePath, data)
			}
			return renderReleaseResolved(opts, rel, releasePath, gs.IRGraph, gs.ResolveResult, catalogLookup)
		},
	)
}

// renderFeatureAttachment writes one Feature attachment to stdout.
// When the attachment's per-leaf provenance is uniform across both
// pillars, the function emits the per-attachment marker (the format
// established before per-leaf provenance shipped) so callers reading
// the common case see one compact line per attachment. When provenance
// diverges across leaves, the function expands into per-leaf lines —
// one bracket per leaf the attachment populates — so the rendered
// output surfaces the divergence that consumers would otherwise miss.
// declared carries what the attached Feature's schema declares sensitive; a
// leaf it covers prints the redaction placeholder in place of its value while
// keeping its key, its pillar prefix and its provenance bracket. A declaration
// covers the leaf it names and every leaf beneath it, so a value authored
// inside an object the schema declares sensitive is hidden too — that leaf IS
// named by the declaration the author wrote.
//
// Only a covered leaf is hidden: the per-attachment sensitivity roll-up says
// the attachment handles secret material, not which of its leaves do, and
// hiding all of them would print the placeholder over a referenceable
// credential's name and key — public locators an author left unmarked.
//
// Both layouts carry the attachment-level facts clause on the attachment's own
// line, so which connection an attachment was written on, which slot it fills
// and whether it handles declared secret material read the same whichever
// layout the provenance selects.
func renderFeatureAttachment(out io.Writer, instanceName string, entry types.IRFeatureEntry, declared resolver.SensitiveLeaves) {
	writeAttachment(out, "    ", instanceName, attachmentFactsBracket(entry), entry, declared)
}

// renderConnectionAttachment writes one attachment a connection carries,
// beneath the line naming that connection. The line has the shape every
// attachment's line has; its facts clause is the connection form, which leaves
// out what the connection's own line above it already says.
func renderConnectionAttachment(out io.Writer, instanceName string, entry types.IRFeatureEntry, declared resolver.SensitiveLeaves) {
	writeAttachment(out, "        ", instanceName, connectionAttachmentFactsBracket(entry), entry, declared)
}

// writeAttachment is the one layout of an attachment's line and, where the
// provenance is not uniform, of the leaves beneath it, at the indentation the
// caller places the attachment at. The two attachment views differ in where the
// line sits and which facts clause it carries, and in nothing else, so the
// layout choice, the redaction and the provenance brackets are written once.
func writeAttachment(out io.Writer, indent, instanceName, facts string, entry types.IRFeatureEntry, declared resolver.SensitiveLeaves) {
	// Formatted once, ahead of the layout branch: the clause describes the
	// attachment rather than a leaf, so both layouts print it in the same
	// position and neither can be edited into dropping it alone.
	if facts != "" {
		facts = "    " + facts
	}
	if featureProvenanceLayout(entry) {
		fmt.Fprintf(out, "%s- %s%s    %s\n", indent, featureLabel(instanceName, entry), facts, perAttachmentBracket(entry))
		return
	}
	fmt.Fprintf(out, "%s- %s%s\n", indent, featureLabel(instanceName, entry), facts)
	for _, leaf := range walkLeavesInOrder(entry) {
		// walkLeavesInOrder emits the pillar-prefixed coordinate, which is what
		// the declarations answer about, so the question is asked of them
		// directly rather than re-derived here. A declaration on an object
		// covers every leaf beneath it, so a value authored inside one prints
		// the placeholder exactly as a directly declared leaf does.
		if declared.Covers(leaf.path) {
			fmt.Fprintf(out, "%s    %s: %s    %s\n", indent, leaf.path, redact.Placeholder(), perLeafBracket(leaf.provenance))
			continue
		}
		fmt.Fprintf(out, "%s    %s: %v    %s\n", indent, leaf.path, leaf.value, perLeafBracket(leaf.provenance))
	}
}

// declaredFeatureLeaves reads which leaves an attached Feature's schema
// declares sensitive, through the same compile-and-read pair the IR's
// sensitivity derivation uses, so what inspect hides and what the graph marks
// come from one declaration. An absent Feature, or one declaring no schema,
// reports nothing sensitive rather than guessing.
//
// Asking for the unredacted view answers with the empty set, and that is where
// the affordance is applied rather than at each place a value is printed. A
// layout withholds a leaf because the set it was handed covers it, so emptying
// the set reveals every leaf in every layout at once — including a layout added
// later, which gets the behaviour without having to remember to ask for it.
func declaredFeatureLeaves(opts inspectOptions, lookup resolver.CatalogLookup, featureType string) resolver.SensitiveLeaves {
	if opts.unredacted {
		return resolver.SensitiveLeaves{}
	}
	compiled, ok := ir.CompileFeatureSchema(lookup, featureType)
	if !ok {
		return resolver.SensitiveLeaves{}
	}
	return resolver.DeclaredSensitiveLeaves(compiled)
}

// featureLabel formats the Feature URI line. The IR's Type field
// carries the typed URI (type:version) when resolved; the instance
// name supplements it when the two differ.
func featureLabel(instanceName string, entry types.IRFeatureEntry) string {
	label := entry.Type
	if entry.ResolvedVersion != "" && !strings.Contains(entry.Type, ":") {
		label = entry.Type + ":" + entry.ResolvedVersion
	}
	if instanceName != "" && instanceName != entry.Type {
		label = instanceName + " (" + label + ")"
	}
	return label
}

// attachmentFactsBracket formats the clause carrying the per-attachment facts the
// IR builder records about an attachment itself rather than about one of its
// leaves: the connection it was written on, the attachment slot it fills, and
// whether it handles material an author declared sensitive. Each is read from
// the entry; none is recomputed here, so what inspect prints and what the IR
// carries cannot disagree.
//
// The connection half leads, and appears only for an attachment written on one
// of the node's connections. It names the connection by the alias it is written
// under and the socket it is wired through, read from the entry's own connection
// field and never out of the attachment's qualified name: the name carries the
// alias but not the socket, and a reader joining the attachment to the edge it
// is presented on needs both. Its presence also says which declaration the slot
// half below names — the slot the connection's socket declares rather than one
// the host Component declares for its own attachments.
//
// The resolved Release view prints an attachment a connection carries beneath
// that connection's own line, with [connectionAttachmentFactsBracket] instead,
// so this clause's connection half is printed only for such an attachment whose
// connection the view shows no line for. The two clauses never describe one
// attachment, so the view states each attachment's connection once.
//
// The slot half distinguishes three states, not two:
//
//   - a slot name prints as "slot: <name>";
//   - [types.AttachmentBindingFree] prints as that bare word — the positive
//     finding that the resolved Component declares no slot of this name;
//   - an empty binding prints no clause at all. Empty is the absence of a
//     finding, because the host Component's declared slot set did not resolve,
//     and printing the free sentinel for it would report a finding the
//     compiler never made.
//
// The sensitivity half appears only for a marked attachment. Its absence
// records that no declaration marked the attachment, which is a narrower
// statement than the attachment being free of secret material.
//
// The realization half comes last: the FeatureRecipe the Manifest binds to the
// attachment, read from the entry's realization field, which the graph build
// records through the lookup the render binds by. An entry carrying none — the
// Manifest binds the attachment to no FeatureRecipe the catalog holds — prints
// no half rather than a realization nobody chose.
//
// Returns the empty string when the entry carries none of the facts, so an
// attachment of the node's own block, on a node whose Component does not
// resolve, whose declarations mark nothing and whose Manifest binds it to
// nothing, prints the line it printed before any fact existed.
func attachmentFactsBracket(entry types.IRFeatureEntry) string {
	var parts []string
	if c := entry.Connection; c != nil {
		parts = append(parts, "connection: "+c.Alias+" through socket "+c.Socket)
	}
	parts = append(parts, slotAndSensitivityHalves(entry.Binding, entry.Sensitive)...)
	return factsClause(append(parts, realizationHalf(entry)...))
}

// connectionAttachmentFactsBracket formats the facts clause of an attachment a
// connection carries, printed beneath that connection's own line.
//
// The line above it names the connection by its alias and its target, so the
// clause leaves the alias out and leads with the socket, which that line does
// not name — read, like the alias, from the entry's connection field. The slot,
// sensitivity and realization halves follow exactly as [attachmentFactsBracket]
// prints them.
func connectionAttachmentFactsBracket(entry types.IRFeatureEntry) string {
	var parts []string
	if c := entry.Connection; c != nil {
		parts = append(parts, "socket: "+c.Socket)
	}
	parts = append(parts, slotAndSensitivityHalves(entry.Binding, entry.Sensitive)...)
	return factsClause(append(parts, realizationHalf(entry)...))
}

// realizationHalf renders the realization half of an attachment's facts clause
// from the realization the entry carries, or nothing where it carries none. It
// is the one spelling of the half for both clauses, and it reads the entry
// rather than asking a lookup, so what inspect prints and what the graph
// carries cannot disagree.
func realizationHalf(entry types.IRFeatureEntry) []string {
	if entry.Realization == "" {
		return nil
	}
	return []string{"realization: " + entry.Realization}
}

// slotAndSensitivityHalves renders the slot half and the sensitivity half of an
// attachment's facts clause, in that order, from the binding and the roll-up the
// attachment carries. It is the one spelling of both halves for every view that
// prints them — the resolved Release's, whose values the graph carries, and the
// Aggregate's, whose values the same derivation produces without a graph — so a
// slot or a mark reads alike wherever an attachment is shown.
//
// The slot half has three states, not two: a slot name prints as "slot: <name>",
// [types.AttachmentBindingFree] prints as that bare word, and an empty binding —
// the absence of a finding — prints nothing. The sensitivity half prints only for
// a marked attachment.
func slotAndSensitivityHalves(binding string, sensitive bool) []string {
	var parts []string
	switch binding {
	case "":
	case types.AttachmentBindingFree:
		parts = append(parts, types.AttachmentBindingFree)
	default:
		parts = append(parts, "slot: "+binding)
	}
	if sensitive {
		parts = append(parts, "sensitive")
	}
	return parts
}

// factsClause brackets a facts clause's parts, or returns the empty string when
// there are none, so a line carrying no fact prints no empty brackets.
func factsClause(parts []string) string {
	if len(parts) == 0 {
		return ""
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// perAttachmentBracket formats the trailing [explicit] /
// [annotation: ...] suffix appended to each Feature listing when every
// populated leaf shares the same FieldProvenance shape. The function
// reads the top-level Origin / AnnotationKey / AnnotationSource fields
// the IR builder populates from the per-leaf provenance derivation.
func perAttachmentBracket(entry types.IRFeatureEntry) string {
	if entry.Origin == "annotation" {
		return fmt.Sprintf("[annotation: %s from %s]", entry.AnnotationKey, entry.AnnotationSource)
	}
	return "[explicit]"
}

// perLeafBracket formats the bracket suffix for one leaf when the
// attachment's per-leaf provenance is heterogeneous. The shape mirrors
// perAttachmentBracket but emits an additional "overrides annotation"
// clause when the leaf's explicit contribution suppressed an annotation
// contribution at the same leaf.
func perLeafBracket(prov types.FieldProvenance) string {
	if prov.Origin == "annotation" {
		return fmt.Sprintf("[annotation: %s from %s]", prov.AnnotationKey, prov.AnnotationSource)
	}
	if prov.OverriddenAnnotation != nil {
		return fmt.Sprintf("[explicit; overrides annotation: %s from %s]",
			prov.OverriddenAnnotation.AnnotationKey,
			prov.OverriddenAnnotation.AnnotationSource)
	}
	return "[explicit]"
}

// featureProvenanceLayout reports whether an IRFeatureEntry's per-leaf
// provenance is uniform across both pillars. The decision rule:
//
//   - Empty per-leaf maps → uniform (the attachment carries no leaves;
//     the top-level provenance is the only signal).
//   - Every populated leaf shares the same FieldProvenance shape
//     (same Origin, Side, AnnotationKey, AnnotationSource, no
//     OverriddenAnnotation at any leaf) → uniform.
//   - Otherwise → heterogeneous.
//
// Uniform attachments fall back to the per-attachment marker for
// compact output; heterogeneous attachments expand into per-leaf
// lines so consumers see each leaf's origin distinctly.
func featureProvenanceLayout(entry types.IRFeatureEntry) bool {
	var first types.FieldProvenance
	hasFirst := false
	check := func(m types.OrderedMap[types.FieldProvenance]) bool {
		for _, fp := range m.All() {
			if fp.OverriddenAnnotation != nil {
				return false
			}
			if !hasFirst {
				first = fp
				hasFirst = true
				continue
			}
			if fp.Origin != first.Origin ||
				fp.Side != first.Side ||
				fp.AnnotationKey != first.AnnotationKey ||
				fp.AnnotationSource != first.AnnotationSource {
				return false
			}
		}
		return true
	}
	if !check(entry.ValuesProvenance) {
		return false
	}
	return check(entry.InfraProvenance)
}

// leafRecord pairs a dotted-path leaf identifier with the leaf's scalar
// value and the FieldProvenance carried for that leaf. walkLeavesInOrder
// emits these in the order the inspect renderer prints them.
type leafRecord struct {
	path       string
	value      any
	provenance types.FieldProvenance
}

// walkLeavesInOrder returns the populated leaves of an IRFeatureEntry
// in render order — values pillar first (author order, flattened to
// dotted-path scalars from nested OrderedMap intermediates), then
// infra pillar (same discipline). Each leaf's path carries the pillar
// prefix (`values.` / `infra.`) followed by the dotted segment chain
// produced by types.FlattenLeaves walking the nested OrderedMap tree;
// the leaf's value is the primitive scalar at the terminal segment;
// the FieldProvenance lookup uses the flat-dotted-leaf path (the
// same key the IR builder records during the per-leaf merge). The
// IR builder and the inspect renderer share types.FlattenLeaves so
// the two sequences align by construction.
func walkLeavesInOrder(entry types.IRFeatureEntry) []leafRecord {
	out := make([]leafRecord, 0, entry.ValuesProvenance.Len()+entry.InfraProvenance.Len())
	for _, p := range types.FlattenLeaves(entry.Values) {
		prov, _ := entry.ValuesProvenance.Get(p.Path)
		out = append(out, leafRecord{path: types.PillarRootValues + p.Path, value: p.Value, provenance: prov})
	}
	for _, p := range types.FlattenLeaves(entry.Infra) {
		prov, _ := entry.InfraProvenance.Get(p.Path)
		out = append(out, leafRecord{path: types.PillarRootInfra + p.Path, value: p.Value, provenance: prov})
	}
	return out
}
