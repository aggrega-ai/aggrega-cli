// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/pluginops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// newPluginAddCmd builds the `plugin add` command: it finds a plugin of the named
// type in the configured registry, by its name first, resolves the query to one
// plugin identity, and either writes the attachment into the workspace config file — a
// `plugins.<type>` list entry (governance / linter / importer) or a
// `plugins.providers` capability binding (provider) — or, for the cli type,
// installs the plugin as a top-level aggrega command.
func newPluginAddCmd() *cobra.Command {
	var capability string
	var role string
	var global bool
	var workspace bool
	var acceptCaps bool
	var assumeYes bool
	var dryRun bool
	c := &cobra.Command{
		Use:   "add <type> <query>",
		Short: "Find a plugin of the named type in the registry and add it",
		Long: `Find a plugin of the named type in the configured registry and add it to your
configuration.

Examples:
  aggrega plugin add cli moo                          # add the moo command
  aggrega plugin add cli moo:0.2.4                    # a specific version
  aggrega plugin add governance rego --role render-verdict

Arguments:
  <type>   The plugin socket to add for. One of: provider, governance,
           linter, importer, or cli. The governance type requires --role.
  <query>  The plugin's name, optionally followed by :<version> to add that
           version rather than the latest published one. The name is looked
           up directly first. Where no plugin of the chosen type has that name,
           it is matched as a case-insensitive substring against the plugin
           names of that type, which reads the registry's whole catalog and
           must resolve to exactly one plugin — otherwise the command lists the
           candidates so you can narrow it. A query naming a version is never
           matched that way. The provider type takes no version.

What add writes depends on the type:

  - A socket type (governance, linter, importer) pins the version the query
    names, or else the latest published version, and adds the bare plugin
    identity (<type>/<name>:<version>) to the plugins.<type> list. The latest
    is never taken from pre-release versions, so a plugin that publishes only
    those is added by naming one. A governance entry also records its --role,
    one of ` + pluginops.GovernanceRolesHint() + `.
    An importer the plugins.importer list already names keeps its one entry,
    moved to the pinned version with its place and what it carries under with.
  - The provider type adds a plugins.providers entry naming the plugin by its
    bare identity (provider/<name>) with no version; --capability names the
    capability the binding serves, preserving existing settings. The version is
    the one the Recipe pins, or else the highest available, and the lockfile
    records the one a pull chose.
  - The cli type fetches the plugin from the registry, verifies it, and caches
    its bytes, then pins it into the plugins.cli list as a top-level aggrega
    command — no separate pull step. Its resolved reference and the content
    digest of the verified bytes are recorded so the command runs those exact
    bytes, resolving from the local cache on later runs with no further network
    access. A cli plugin defaults to the global config, so its command is
    available in every workspace; --workspace pins it to this project instead.
    It needs no workspace: run outside one, its bytes are cached in the
    machine-wide cache rather than in the folder you ran it in. Its declared
    capabilities are confirmed before installing, and --accept-capabilities or
    --yes skips that prompt for non-interactive use.

A provider or socket attachment is written to a workspace's own configuration
unless --global is passed, so outside a workspace it is refused before the
registry is reached; so is anything else that can be refused without it.

A provider or socket attachment is configuration you go on editing, so --dry-run
does everything the plain command does except write: it hands back the
configuration the write would have produced, creating and modifying nothing. The
answer names the file that content is for — which matters here, because a
--global attachment targets the config shared across every workspace. A caller
that keeps its own document model puts the content there, where it is visible
before it is saved and can be amended or undone. The registry is still searched,
and for a socket type the version still resolved, since the returned document
carries the identity a write would have written.

Under --dry-run standard output carries that content and nothing else, so it stays
parseable; a version substitution is announced on standard error instead. The cli
type does not accept --dry-run: what it records is the content digest of the bytes
it verified, which is a record the machine maintains rather than content you
edit.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return pluginAddRun(cmd.Context(), pluginAddConfig{
				Socket:     args[0],
				Query:      args[1],
				Capability: capability,
				Role:       role,
				Global:     global,
				Workspace:  workspace,
				AcceptCaps: acceptCaps,
				AssumeYes:  assumeYes,
				DryRun:     dryRun,
				ws:         rw,
				In:         cmd.InOrStdin(),
				Out:        cmd.OutOrStdout(),
				ErrOut:     cmd.ErrOrStderr(),
			})
		},
	}
	c.Flags().StringVar(&capability, "capability", "", "Capability the provider binding serves (required for, and only valid with, the provider type)")
	c.Flags().StringVar(&role, "role", "", "Governance attachment role: "+pluginops.GovernanceRolesHint()+" (required for, and only valid with, the governance type)")
	c.Flags().BoolVar(&global, "global", false, "Write the attachment to the global config shared across every workspace.")
	c.Flags().BoolVar(&workspace, "workspace", false, "Write the attachment to this project's workspace config. A provider or socket attachment defaults here; a cli plugin defaults to the global config.")
	c.Flags().BoolVar(&acceptCaps, "accept-capabilities", false, "Accept a cli plugin's declared capabilities without the interactive prompt (cli type only)")
	c.Flags().BoolVar(&assumeYes, "yes", false, "Skip the cli capability-confirmation prompt for non-interactive use (cli type only)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Do everything except write: hand back the resulting configuration as content, naming the file it is for, and create or modify nothing. Without it the attachment is written to the resolved config file. Not accepted with the cli type.")
	return c
}

// pluginAddConfig is the input contract for `aggrega plugin add`, populated by
// the cobra command from its arguments and flags.
type pluginAddConfig struct {
	Socket     string
	Query      string
	Capability string
	Role       string

	// Global and Workspace are the operator's write-target overrides. A plugin
	// attachment is build-scoped, so it defaults to this project's workspace
	// config; --global forces the global file and --workspace forces the
	// workspace file. Setting both is a usage error.
	Global    bool
	Workspace bool

	// AcceptCaps and AssumeYes gate the cli type's capability-confirmation
	// prompt. They are consulted only when the type is cli; on any other type a
	// set flag is a usage error, mirroring the provider-only --capability guard.
	AcceptCaps bool
	AssumeYes  bool

	// DryRun asks for the resulting configuration as content rather than as a
	// write. A provider or socket attachment is configuration its owner goes on
	// editing, so a caller holding a document model takes the content and decides
	// where it lands. It is refused on the cli type, whose product is a record the
	// machine maintains.
	DryRun bool

	ws *workspaceconfig.ResolvedWorkspace
	In io.Reader
	// Out carries the answer, and ErrOut whatever the answer cannot convey.
	//
	// The pair exists because under DryRun the answer on Out is one parseable
	// document and nothing else, so the version-substitution notice the operation
	// announces would break the document's shape rather than merely sit beside it.
	// It goes to ErrOut, which leaves that shape alone — and it goes there rather
	// than nowhere, because a writer of nil leaves version selection unscreened,
	// which would skip a withdrawn version silently.
	Out    interface{ Write([]byte) (int, error) }
	ErrOut interface{ Write([]byte) (int, error) }
}

// pluginAddRun runs the addition and renders the command line's own account of
// what it did.
//
// The operation itself lives in the plugin operations layer, which every surface
// reaches; this translates the command's flags into that layer's options and turns
// the result into the sentence a terminal expects. Which field the result filled
// is what says what landed — a bare version-pinned identity for a config socket, a
// bare identity with no version for a provider binding, a pinned reference for a
// command extension — so the sentence is chosen from the answer rather than
// re-derived from the arguments.
//
// Every sentence carries how far the write reached, because the file a mutation
// lands in is not something a reader should have to recognise from its path.
//
// A version substitution is stated the same way — from the conclusion on the
// result rather than from prose the operation wrote to a stream handed to it. Both
// surfaces are handed the same conclusion, so a reader here and a caller answering
// with values learn the same fact, and the one that renders it does so at the
// moment it chooses on the writer its own answer occupies. The wording itself is
// not composed here: it has one home, shared with the screen that reaches the
// conclusion, so a substitution reads the same however a reader met it.
//
// A version the query named that turns out to be retracted is pinned all the same,
// and the result line says so from the result's own value (withdrawnPinClause),
// after the notice that gave the reason as the version was resolved.
//
// The command-extension sentence is the short one, and deliberately: the installer
// this command supplies has already named the reference it pinned, the commands it
// registered and the file it wrote, so restating those would announce one install
// twice. What the installer cannot report is the reach, which the operation
// computes and only its result carries — so that is what is added here.
//
// Under a reporting delivery no sentence is rendered at all: the answer is the one
// parseable document the operation composed, addressed to the file the write would
// have gone to. That path comes off the result's own write-target resolution, so
// the content is addressed to the file the write would have used rather than to a
// second resolution taken here.
func pluginAddRun(ctx context.Context, cfg pluginAddConfig) error {
	// The request is mapped onto a delivery through the one mapper every surface
	// routes through, and every branch below reads that delivery through its
	// predicate — so this surface expresses the choice nowhere in its own terms.
	delivery := authoredcontent.DeliveryFor(cfg.DryRun)
	// A reporting delivery leaves Out holding one document, so the operation's
	// prose is redirected rather than dropped: a nil writer would leave version
	// selection unscreened.
	notices := cfg.Out
	if delivery.Reports() {
		notices = cfg.ErrOut
	}
	// The substitution is taken as values and stated below, so the operation is
	// asked to announce it nowhere — which leaves its account of the markers it
	// read still reaching the writer above, since that account is a different fact
	// and discarding both would take it too.
	//
	// The redirect is conditional on there BEING a writer. That condition is the
	// safety property rather than a tidiness one, and removing it reintroduces
	// exactly the failure the screen exists to prevent.
	// Selection is screened only where
	// a substitution can be announced, so handing the operation a discarding writer
	// with nothing here to render onto would screen the selection and then swallow
	// the explanation — the silent substitution the screen exists to prevent. With
	// nowhere to state it, this leaves that writer unset and selection unscreened,
	// which is the same posture the operation takes on its own.
	var retractions io.Writer
	if notices != nil {
		retractions = io.Discard
	}
	res, err := pluginops.Add(ctx, cfg.ws, pluginops.AddOptions{
		Socket:             cfg.Socket,
		Query:              cfg.Query,
		Capability:         cfg.Capability,
		Role:               cfg.Role,
		Global:             cfg.Global,
		Workspace:          cfg.Workspace,
		AcceptCapabilities: cfg.AcceptCaps,
		AssumeYes:          cfg.AssumeYes,
		Delivery:           delivery,
		InstallCLIPlugin:   installCLIPlugin,
		In:                 cfg.In,
		Notices:            notices,
		RetractionNotices:  retractions,
	})
	if err != nil {
		return err
	}
	// The substitution the addition concluded, stated from the value it concluded
	// rather than read off a stream: it goes where the rest of this command's
	// commentary goes, which under a reporting delivery is the writer that leaves
	// the document's shape alone.
	pluginops.WriteRetractionNotice(notices, res.Retraction)
	if delivery.Reports() {
		if cfg.Out == nil {
			return nil
		}
		return authoredcontent.Report(cfg.Out, []authoredcontent.Document{{
			Path:    res.ConfigPath,
			Content: string(res.Content),
		}})
	}
	switch {
	case res.URI != "":
		fmt.Fprintf(cfg.Out, "Bound capability %q to %s in %s (%s)\n",
			res.Capability, res.URI, res.ConfigPath, pluginWriteReach(res.ConfigIsMachineWide))
	case res.Identity != "":
		fmt.Fprintf(cfg.Out, "Added %s as a %s plugin in %s (%s)%s\n",
			res.Identity, res.Socket, res.ConfigPath, pluginWriteReach(res.ConfigIsMachineWide), withdrawnPinClause(res.Withdrawn))
	case res.Reference != "":
		fmt.Fprintf(cfg.Out, "Pinned %s (%s)%s\n", res.Reference, pluginWriteReach(res.ConfigIsMachineWide), withdrawnPinClause(res.Withdrawn))
	}
	return nil
}

// withdrawnPinClause is what a result line adds when the version the query named
// is retracted, and the empty string otherwise.
//
// It is read off the result, the same value every other surface answers with, so
// the line reporting what was pinned says the outcome in the terms the other
// surfaces state it in. The withdrawal notice printed while the version was
// resolved stays as it is: it alone carries the reason recorded against the
// version and where that record was read, which the value does not. A provider
// binding pins no version, so its line never carries the clause.
func withdrawnPinClause(withdrawn bool) string {
	if !withdrawn {
		return ""
	}
	return "; the version named is retracted and was pinned because it was named"
}

// installCLIPlugin is the command-extension installer the operations layer is
// handed.
//
// It exists as a named adapter rather than a closure at the call site because it
// is supplied from two places — this command, and the daemon's wiring — and a
// closure written twice is the same function written twice. The install itself
// stays in this package because its collision guard reads the freshly built
// command tree, which is what authoritatively decides the built-in names and
// cannot be enumerated from outside package main without becoming a second copy
// that drifts from the tree.
func installCLIPlugin(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, req pluginops.CLIInstallRequest) error {
	return pluginInstallRun(ctx, rw, req.Reference, req.Global, req.Workspace, req.AcceptCapabilities, req.AssumeYes, req.In, req.Out)
}
