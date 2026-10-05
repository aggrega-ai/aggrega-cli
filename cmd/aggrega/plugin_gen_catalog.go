// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
)

// genCatalogOptions carries the parsed gen-catalog flags.
type genCatalogOptions struct {
	fromEmbedded bool
	pluginPath   string
	outputDir    string
	overlayPath  string
	dryRun       bool
}

// newPluginGenCatalogCmd builds the `plugin gen-catalog` subcommand. It emits a
// deterministic, idempotent Plugin entity for each selected guest by merging the
// machine-derived contract fields from the guest's describe() self-report with
// an authored per-plugin overlay (identity, runtime limits, provider block) and
// the guest's describe() self-report version. Re-running over the same inputs
// produces byte-identical output.
func newPluginGenCatalogCmd() *cobra.Command {
	opts := genCatalogOptions{}
	c := &cobra.Command{
		Use:   "gen-catalog",
		Short: "Generate Plugin catalog entities from guest describe() plus an authored overlay",
		Long: `Generate one Plugin entity per selected Wasm guest.

Each entity's contract fields — pluginType, abiVersion, capabilities, exports,
and contract — are derived from the guest's describe() self-report, so the
entity can never promise a capability the binary does not provide. The entity's
identity (metadata.name), runtime limits, and provider block come from an authored
per-plugin overlay; the entity version is the guest's describe() self-report
version. The plugin-level description, when the guest reports one, is emitted as
metadata.description and omitted cleanly when it does not.

Two selection modes:

  --from-embedded   generate an entity for every guest embedded in this binary.
                    The overlay path is a directory; each overlay is matched to
                    a guest by its metadata.name identity.
  --plugin <f.wasm> generate a single entity for one non-embedded guest. The
                    overlay path is a single overlay file.

The output is written under <output-dir>/<pluginType>/<name>/<version>/plugin.yaml
and carries a generated-output banner. The leading pluginType segment keeps two
same-named plugins in different sockets apart, since a bare plugin name is unique
only within its own socket. Regenerating over unchanged inputs yields
byte-identical files.

A generated entity is a catalog document you own and go on editing, so --dry-run
does everything gen-catalog does except write: it hands the content back, creating
and modifying nothing, and the answer names the file each document is for. A
caller that keeps its own document model — an editor, for instance — puts the
content there, where it is visible before it is saved and can be amended or
undone. Every other behaviour is the write's — the same guests are read and the
same overlays matched — so a selection gen-catalog would refuse is refused under
--dry-run too. Without the flag the entities are written, because a caller at a
terminal has nowhere better to put them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The workspace is resolved so the output directory can be placed against
			// it: one inside the workspace holds what is written beneath it to the
			// workspace, and one outside it — named here, on the command line — is
			// honoured where it was named and holds what is written beneath it to
			// itself.
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			// The generation copies every byte it emits to the writer handed in
			// below, so this command has the whole output already. Both halves of
			// the result are a second form of those same bytes — the notices are
			// that text, and the documents are that text decoded for a surface
			// that answers with values instead of prose — so projecting either
			// would print in full what the terminal has just been shown.
			// projection-omits GenCatalogResult: the generation already wrote every byte of it to this command's own writer, so both halves are a second form of output the terminal has in full
			_, err = pluginops.GenCatalog(cmd.Context(), pluginops.GenCatalogOptions{
				FromEmbedded:  opts.fromEmbedded,
				PluginPath:    opts.pluginPath,
				OutputDir:     opts.outputDir,
				OverlayPath:   opts.overlayPath,
				DryRun:        opts.dryRun,
				WorkspaceRoot: rw.Root(),
			}, cmd.OutOrStdout())
			return err
		},
	}
	c.Flags().BoolVar(&opts.fromEmbedded, "from-embedded", false, "generate an entity for every guest embedded in this binary")
	c.Flags().StringVar(&opts.pluginPath, "plugin", "", "generate a single entity for the .wasm guest at this path")
	c.Flags().StringVar(&opts.outputDir, "output-dir", "", "directory the generated entities are written under (required)")
	c.Flags().StringVar(&opts.overlayPath, "overlay", "", "authored overlay directory (--from-embedded) or file (--plugin) (required)")
	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Do everything except write: hand back the generated entities as content, naming the file each is for, and create or modify nothing. Every other behaviour is the write's, so a selection the write would refuse is refused here too. Without it the entities are written under --output-dir.")
	return c
}
