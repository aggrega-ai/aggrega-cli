// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/gencatalog"
)

// genCatalogOptions carries the parsed gen-catalog flags.
type genCatalogOptions struct {
	fromEmbedded bool
	pluginPath   string
	outputDir    string
	overlayPath  string
	report       bool
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

A generated entity is a catalog document you own and go on editing, so --report
hands the content back instead of writing it: nothing is created or modified, and
the answer names the file each document is for. A caller that keeps its own
document model — an editor, for instance — puts the content there, where it is
visible before it is saved and can be amended or undone. Without the flag the
entities are written, because a caller at a terminal has nowhere better to put
them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return gencatalog.Run(cmd.Context(), gencatalog.Options{
				FromEmbedded: opts.fromEmbedded,
				PluginPath:   opts.pluginPath,
				OutputDir:    opts.outputDir,
				OverlayPath:  opts.overlayPath,
				Delivery:     authoredcontent.DeliveryFor(opts.report),
			}, cmd.OutOrStdout())
		},
	}
	c.Flags().BoolVar(&opts.fromEmbedded, "from-embedded", false, "generate an entity for every guest embedded in this binary")
	c.Flags().StringVar(&opts.pluginPath, "plugin", "", "generate a single entity for the .wasm guest at this path")
	c.Flags().StringVar(&opts.outputDir, "output-dir", "", "directory the generated entities are written under (required)")
	c.Flags().StringVar(&opts.overlayPath, "overlay", "", "authored overlay directory (--from-embedded) or file (--plugin) (required)")
	c.Flags().BoolVar(&opts.report, "report", false, "Report the generated entities as content, naming the file each is for, and write nothing. Without it the entities are written under --output-dir.")
	return c
}
