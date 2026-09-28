// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/configops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// attachConfigCommands builds the `config` command family under parent: the
// grouping command, then each subcommand named by the operation it carries.
//
// It attaches the group before its children so that a child's invocation path is
// the one it will actually be invoked by when the registrar checks it against
// the operation table. Built detached and grafted on afterwards, a child's path
// would be missing everything above the group, and the check would be comparing
// a path no caller ever types.
func attachConfigCommands(parent *cobra.Command) {
	c := &cobra.Command{
		Use:   "config",
		Short: "Inspect and manage the workspace configuration",
		Long: `Inspect and manage the configuration Aggrega reads.

Most projects need two files.

One .aggrega-config.yaml at your project root, carrying a "workspace:" block —
that block is what marks the root — holds how this project builds: its catalog
directories, its provider bindings, which lint rules it relaxes. It is
committed, so a teammate who clones the project builds it the same way.

One global file at ~/.config/aggrega/config.yaml is shared across every
workspace on this machine. It holds what belongs to the machine rather than to
any one project: the registries you pull from and the signature trust policy
for each, how large the compiled-module store may grow, any command extension
you have installed. It loads automatically when it is there, and nothing
breaks when it is not.

Your project's file wins over the global one, an environment variable wins
over both, and a flag wins over everything.

Secrets are not configuration. A registry password or a signing identity token
is never a field in either file; it is supplied by environment variable or flag
at the moment it is needed.

Because the effective configuration is assembled rather than written anywhere,
"aggrega config show" is how you see what actually applies, and --origin is how
you find out which layer each setting came from. "aggrega config init" seeds a
working configuration, and "aggrega config capture" freezes the effective
build-critical settings into this workspace so a build does not depend on a
global file that is not committed.

Larger projects have more to work with. None of it is needed to start, and each
piece exists for the shape it solves:

  More than one file inside a project. Every .aggrega-config.yaml from the
  project root down to the directory you run in contributes, a nearer file
  overriding a farther one. Only a file carrying "workspace:" marks the root;
  one without it contributes settings without anchoring anything.

  Extending a list instead of replacing it. A nearer file's list replaces the
  farther one's. The companions catalog_dirs_append, governance_append,
  linter_append, importer_append and providers_append concatenate onto the
  resolved list at each layer instead of shadowing it.

  A file your pipeline owns. .aggrega-config-append-ci.yaml may sit beside the
  hand-authored file in any of those directories, so automation can overwrite
  it wholesale without touching what a person wrote. It is additive only: it
  removes nothing and overrides no scalar, and a scalar key written in it is
  ignored and named in a warning. It never marks a root.

  A relative path anchors to the file that declared it. A catalog directory, a
  local plugin path, or an entry under a plugin's with.sources resolves against
  the directory of the config file it was written in — not the project root,
  and not the directory you ran the command from. So the same "./catalog" names
  a different directory depending on which file carries it. Two settings sit
  outside this rule: artifact_cache_dir is interpreted relative to the project
  root wherever you run from, and AGGREGA_CATALOG_DIRS has no declaring file at
  all, so supply absolute paths in it.

  The other root markers. A "workspace:" block wins outright at whatever depth
  it sits. Failing one, the nearest .aggrega/ directory or aggregate*.yaml,
  release*.yaml or manifest*.yaml file anchors the root, in that order within a
  directory; failing everything, your working directory is used.
  --workspace-root skips discovery entirely.

  Naming the global file. --config <path> wins over $AGGREGA_CONFIG, which
  wins over the default location. Both name a file deliberately, so a path
  either gives that does not exist is a loud error rather than a silent
  fallback. Whichever file resolves is also the one a config-writing command
  writes when it writes globally.`,
		Example: `  # What applies right now
  aggrega config show

  # Which layer each setting came from
  aggrega config show --origin

  # Seed the global configuration with a registry and its trust policy
  aggrega config init

  # Seed this project's own configuration instead
  aggrega config init --workspace

  # Freeze the effective build-critical settings into this workspace
  aggrega config capture`,
	}
	mustAttachGroup(parent, c)
	mustAttach(c, newConfigInitCmd(), "config-init")
	mustAttach(c, newConfigCaptureCmd(), "config-capture")
	mustAttach(c, newConfigShowCmd(), "config-show")
}

// newConfigShowCmd builds the `config show` command: it reports the
// configuration that applies right now — which files and environment overrides
// contributed and the effective merged result — with modes to dump a single
// layer's file verbatim or to annotate each setting with its origin.
func newConfigShowCmd() *cobra.Command {
	var global, workspace, origin bool

	c := &cobra.Command{
		Use:   "show",
		Short: "Show the effective configuration and where each setting came from",
		Long: `Show the configuration that applies right now.

By default it leads with the cascade of config files and environment overrides
that contributed, lowest precedence first, then prints the effective merged
configuration — the global config, the workspace-tree cascade, and any
environment override combined into what applies right now.

Pass --global or --workspace to print a single layer's own file bytes verbatim,
preserving its comments and formatting; when that layer's file does not exist it
says so rather than failing. The two are mutually exclusive.

Pass --origin to annotate each effective setting with the layer it came from —
a command-line flag, an environment variable, a config file, or the built-in
defaults, highest precedence first. A flag is named as the reader typed it, so
a setting a flag decided reports that flag as its source. A setting is
attributed to the highest-precedence layer that sets it, whatever value that
layer writes: a file restating the default is the file named, not the built-in
defaults. List and map settings are attributed as a whole. The signing
requirement is the one exception: any layer may turn it on and none can turn it
off, so it names the first layer that turns it on.`,
		Example: `  # What applies right now, with the contributing layers listed first
  aggrega config show

  # Where each setting came from
  aggrega config show --origin

  # This workspace's own config file, verbatim
  aggrega config show --workspace`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return configShowRun(configShowConfig{
				ws:        rw,
				lookup:    configLookupFromContext(cmd.Context()),
				Global:    global,
				Workspace: workspace,
				Origin:    origin,
				Out:       cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().BoolVar(&global, "global", false, "Print the global config file's own bytes verbatim, preserving its comments and formatting.")
	c.Flags().BoolVar(&workspace, "workspace", false, "Print this project's workspace config file's own bytes verbatim, preserving its comments and formatting.")
	c.Flags().BoolVar(&origin, "origin", false, "Annotate each effective setting with the layer it came from (a command-line flag, an environment variable, a config file, or the built-in defaults).")
	return c
}

// configShowConfig is the input contract for `aggrega config show`.
type configShowConfig struct {
	ws *workspaceconfig.ResolvedWorkspace
	// lookup carries the --config flag as the flag form of the global-config-path
	// override, so --global and the resolution seam resolve the same path the
	// workspace resolution used. Nil selects the live process environment.
	lookup func(string) string
	// Global and Workspace select a single-layer verbatim dump; Origin selects
	// the per-value provenance view. Global and Workspace are mutually exclusive,
	// and neither combines with Origin. With none set, the default effective view
	// applies.
	Global    bool
	Workspace bool
	Origin    bool
	Out       io.Writer
}

// configShowRun dispatches the show mode: a single-layer verbatim dump
// (--global / --workspace), the per-value provenance view (--origin), or the
// default cascade-plus-effective view.
func configShowRun(cfg configShowConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return err
	}

	switch {
	case cfg.Global && cfg.Workspace:
		return fmt.Errorf("aggrega config show: --global and --workspace cannot be combined; choose at most one")
	case cfg.Origin && (cfg.Global || cfg.Workspace):
		return fmt.Errorf("aggrega config show: --origin cannot be combined with --global or --workspace")
	case cfg.Global:
		path, ok := workspaceconfig.GlobalConfigPath(cfg.lookup)
		if !ok {
			fmt.Fprintln(cfg.Out, "No global config path can be resolved; set $AGGREGA_CONFIG or a home directory ($HOME / $XDG_CONFIG_HOME).")
			return nil
		}
		return configShowVerbatim(cfg.Out, path, "global")
	case cfg.Workspace:
		return configShowVerbatim(cfg.Out, filepath.Join(rw.Root(), workspaceconfig.ConfigFilename), "workspace")
	case cfg.Origin:
		return configShowOrigin(cfg.Out, rw)
	default:
		return configShowEffective(cfg.Out, rw)
	}
}

// configShowVerbatim writes a single layer's config file bytes unchanged, so its
// comments and formatting are preserved rather than lost to a re-marshal. A
// missing file is reported as an informative line, not an error.
func configShowVerbatim(out io.Writer, path, label string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "No %s config file exists at %s.\n", label, path)
			return nil
		}
		return fmt.Errorf("aggrega config show: read %s config %s: %w", label, path, err)
	}
	_, err = out.Write(data)
	return err
}

// configShowEffective leads with the ordered cascade of contributing config
// files and environment overrides, then prints the effective merged config as
// deterministic YAML.
func configShowEffective(out io.Writer, rw *workspaceconfig.ResolvedWorkspace) error {
	files := rw.ContributingFiles()
	if len(files) == 0 {
		fmt.Fprintln(out, "Configuration loaded from: built-in defaults only (no config files or environment overrides).")
	} else {
		fmt.Fprintln(out, "Configuration loaded from (lowest precedence first):")
		for i, f := range files {
			fmt.Fprintf(out, "  %d. %s\n", i+1, contributingLabel(f))
		}
	}

	fmt.Fprintln(out)
	fmt.Fprintln(out, "Effective configuration:")
	data, err := yaml.Marshal(rw.Config())
	if err != nil {
		return fmt.Errorf("aggrega config show: marshal effective config: %w", err)
	}
	_, err = out.Write(data)
	return err
}

// contributingLabel renders one contributing-layer entry for the loaded-from
// list: an environment-variable pseudo-entry (a leading "$") reads as such; a
// file path is shown as-is.
func contributingLabel(entry string) string {
	if strings.HasPrefix(entry, "$") {
		return "environment variable " + entry[1:]
	}
	return entry
}

// configShowOrigin prints each effective setting with its value and the layer it
// came from, aligned in columns and in a fixed setting order for deterministic
// output.
func configShowOrigin(out io.Writer, rw *workspaceconfig.ResolvedWorkspace) error {
	origins, err := rw.ValueOrigins()
	if err != nil {
		return fmt.Errorf("aggrega config show: %w", err)
	}
	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SETTING\tVALUE\tSOURCE")
	for _, o := range origins {
		fmt.Fprintf(w, "%s\t%s\t%s\n", o.Field, o.Value, o.Source)
	}
	return w.Flush()
}

// newConfigInitCmd builds the `config init` command: it seeds a working config
// by registering the official hub (or another registry) with its signature
// trust policy, so a fresh machine can pull and verify signed artifacts without
// hand-editing YAML.
func newConfigInitCmd() *cobra.Command {
	var registry string
	var force bool
	var global bool
	var workspace bool
	var dryRun bool

	c := &cobra.Command{
		Use:   "init",
		Short: "Seed a working configuration with a registry and its trust policy",
		Long: `Seed a working configuration by registering a registry and its signature trust
policy, so a fresh machine can pull and verify signed artifacts without editing
YAML by hand.

By default it registers the official hub (hub.aggrega.ai) with signatures
required and the official trusted identities. The registry's advertised trust
policy is fetched once from its own origin and pinned locally — trust-on-first-
use; when the hub is unreachable, advertises nothing, or the workspace is
offline, its built-in official baseline is written instead, so init still
produces a usable config offline. Pass --registry to seed a different registry
(for an air-gapped or private hub). Such a registry has no built-in baseline,
so seeding it while the workspace is offline is refused: init takes no
identities or key to pin by hand, so register it once while online, or add it
with "aggrega registry add --identity" or "--key".

The entry is written to the global config shared across every workspace by
default; pass --workspace to write it to this project's workspace config
instead. If the target config file already exists, init refuses rather than
overwrite it — pass --force to write anyway (the registry entry is merged into
the existing file, other settings preserved).

A seeded configuration is a document you own and go on editing, so --dry-run does
everything init does except write: it hands the content back, creating and
modifying nothing. The answer names the file the content is for — which matters
here, because the target is the global config unless --workspace selects this
project's own. A caller that keeps its own document model puts the content there,
where it is visible before it is saved and can be amended or undone. The registry
is still contacted for its advertised trust policy, because the policy in that
content is the policy a write would have pinned.

Because a dry run does everything else init does, it refuses an existing config
file exactly as a write refuses it, and for the same reason. So its exit code
predicts the write's: if the dry run refuses, init would have refused. Pass
--force alongside it to see the merge an overwriting write would produce.

Under --dry-run standard output carries that content and nothing else, so it stays
parseable. A registry seeded with no trust policy, because it advertises none,
still produces its notice, on standard error: read that stream too, or that
configuration arrives without the advice to pin identities by hand. A pinned
policy, whether advertised or the hub's built-in baseline, is in the content
itself and produces no notice.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// init writes the global config, so it resolves the workspace WITHOUT
			// the global layer: the explicit --config path it is about to create
			// need not pre-exist. The resolution still carries the same flag-aware
			// lookup every other command's does, because the write destination is
			// resolved off the resolution — so the file init creates is the file a
			// later read of this same invocation would consult.
			lookup := configLookupFromContext(cmd.Context())
			rw, err := workspaceconfig.ResolveWithoutGlobal("", workspaceRootFromContext(cmd.Context()), lookup)
			if err != nil {
				return err
			}
			return configInitRun(cmd.Context(), configInitConfig{
				Registry:  registry,
				Force:     force,
				Global:    global,
				Workspace: workspace,
				DryRun:    dryRun,
				ws:        rw,
				Out:       cmd.OutOrStdout(),
				ErrOut:    cmd.ErrOrStderr(),
			})
		},
	}
	c.Flags().StringVar(&registry, "registry", workspaceconfig.HubBaselineHost, "Registry host to seed with its trust policy. Defaults to the official hub.")
	c.Flags().BoolVar(&force, "force", false, "Overwrite an existing config file. Without it, init refuses when the target file already exists.")
	c.Flags().BoolVar(&global, "global", false, "Write the entry to the global config shared across every workspace. This is the default.")
	c.Flags().BoolVar(&workspace, "workspace", false, "Write the entry to this project's workspace config instead of the global config.")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Do everything except write: hand back the seeded configuration as content, naming the file it is for, and create or modify nothing. Every other behaviour is the write's, so an existing config file is refused exactly as a write refuses it — pass --force alongside it for the merge an overwriting write would produce.")
	return c
}

// configInitConfig is the input contract for `aggrega config init`.
type configInitConfig struct {
	// Registry is the host to seed with its trust policy. Empty is treated as
	// the official hub by the underlying add path's host resolution.
	Registry string
	// Force permits the seeding to proceed when the target config file already
	// exists. Without it, init refuses rather than touch an existing file, under
	// every delivery.
	Force bool
	// Global and Workspace are the write-target overrides, mirroring
	// `registry add`. A registry is tool-scoped, so init defaults to the global
	// config; --workspace forces this project's workspace file. Setting both is a
	// usage error.
	Global    bool
	Workspace bool
	// DryRun asks the seeding to do everything except write, handing back the
	// configuration as content. A seeded configuration is a document its owner
	// goes on editing, so a caller holding a document model takes the content and
	// decides where it lands. It changes nothing else: the existing-file refusal
	// and the trust fetch happen exactly as they do under a write.
	DryRun bool

	ws *workspaceconfig.ResolvedWorkspace
	// Out carries the answer, and ErrOut an advisory the answer cannot convey.
	//
	// The pair exists because under DryRun the answer on Out is one parseable
	// document and nothing else, so a trust-policy advisory the reported
	// configuration cannot express — that the registry advertised no policy and
	// identities must be pinned by hand — would be dropped rather than merely
	// moved. It goes to ErrOut, which leaves the document's shape alone.
	Out    io.Writer
	ErrOut io.Writer
	// trustClient transports the advertised-trust fetch. Nil takes the network-egress
	// gate's client in the registration; tests inject one so no live network is contacted.
	trustClient *http.Client
}

// configInitRun binds the command's flags to the seeding operation and renders
// what it did on the terminal's two streams. The seeding itself — the
// write-target resolution, the refuse-if-exists guard, and the registration the
// trust policy comes from — lives in internal/configops, so this surface and the
// daemon perform one seeding rather than each performing its own.
func configInitRun(ctx context.Context, cfg configInitConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return err
	}

	delivery := authoredcontent.DeliveryFor(cfg.DryRun)
	res, initErr := configops.Init(ctx, rw, configops.InitOptions{
		Registry:    cfg.Registry,
		Force:       cfg.Force,
		Global:      cfg.Global,
		Workspace:   cfg.Workspace,
		Delivery:    delivery,
		TrustClient: cfg.trustClient,
	})
	return writeRegistrationOutcome(cfg.Out, cfg.ErrOut, delivery, res.Registration, initErr)
}

// newConfigCaptureCmd builds the `config capture` command: it freezes the
// effective build-critical configuration into this project's workspace config
// file so the workspace builds identically without relying on the machine's
// global configuration.
func newConfigCaptureCmd() *cobra.Command {
	var allPlugins bool

	c := &cobra.Command{
		Use:   "capture",
		Short: "Freeze the effective build-critical configuration into this workspace",
		Long: `Write the effective build-critical configuration into this project's workspace
config file, so the workspace builds identically without relying on the
machine's global configuration.

The build-critical subset is the configuration a build participates in: a set of
plugin socket attachments and the catalog directories. By default the captured
sockets are only those that determine the
rendered output — the provider bindings and the transform-role governance attachments — since only those must
travel with the project for byte-identical output. Pass --all-plugins to also
freeze the verdict-role governance, linter, and importer sockets, which otherwise stay free to
vary by environment. Each field is written at its effective value — the value
that resolves right now with the global configuration and the workspace tree
merged together — so a binding that today comes only from the global config is
materialized into the workspace and travels with the project.

Machine-wide tooling settings, such as a registry's trust policy, are not
captured: they stay in the global config by their nature. Capture always writes
this project's workspace config file. Running it again on an unchanged
configuration rewrites the same file byte-for-byte.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return configCaptureRun(configCaptureConfig{
				ws:         rw,
				allPlugins: allPlugins,
				Out:        cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().BoolVar(&allPlugins, "all-plugins", false, "Capture all plugin sockets, including the verdict-role governance, linter, and importer gates. Without it, capture freezes only the sockets that determine the rendered output (the provider bindings and the transform-role governance attachments).")
	return c
}

// configCaptureConfig is the input contract for `aggrega config capture`.
type configCaptureConfig struct {
	ws *workspaceconfig.ResolvedWorkspace
	// allPlugins widens the captured socket set from the byte-determining sockets
	// (the provider bindings and the transform-role governance attachments) to every
	// plugin socket, adding the verdict-role governance and linter gates and the importer.
	allPlugins bool
	Out        io.Writer
}

// configCaptureRun binds the command's flags to the freezing operation and
// prints what it froze. The write and the captured set live in
// internal/configops, so this surface and the daemon freeze one subset rather
// than each deciding its own.
func configCaptureRun(cfg configCaptureConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return err
	}
	res, err := configops.Capture(rw, configops.CaptureOptions{AllPlugins: cfg.allPlugins})
	if err != nil {
		return err
	}
	reportCapture(cfg.Out, res)
	return nil
}

// reportCapture prints what a capture froze, in the order the operation reports
// it so the output is deterministic. When nothing was build-critical it says so
// rather than printing an empty capture.
//
// It renders and decides nothing. Which sockets were in scope, and which
// attachments within them, is the operation's answer and arrives on the result —
// so this cannot name a socket the write did not freeze, where a second
// enumeration here would have been able to.
func reportCapture(out io.Writer, res configops.CaptureResult) {
	if out == nil {
		return
	}
	wrote := false
	section := func(header string, lines []string) {
		if len(lines) == 0 {
			return
		}
		if !wrote {
			fmt.Fprintf(out, "Captured build-critical configuration into %s:\n", res.Path)
			wrote = true
		}
		fmt.Fprintf(out, "  %s:\n", header)
		for _, l := range lines {
			fmt.Fprintf(out, "    %s\n", l)
		}
	}

	// The provider socket binds a capability to a plugin while the others attach a
	// plugin to a list, so the two read differently to a person; which renderer a
	// socket takes is the only decision left here.
	for _, cs := range res.Sockets {
		if cs.Socket == workspaceconfig.SocketProvider {
			section(cs.Field, providerLines(cs.Refs))
			continue
		}
		section(cs.Field, socketLines(cs.Refs))
	}
	section("catalog_dirs", res.CatalogDirs)

	if !wrote {
		fmt.Fprintf(out, "No build-critical configuration to capture into %s.\n", res.Path)
	}
}

// providerLines renders each provider binding as "capability -> uri", falling
// back to whichever member is present so a binding is never rendered blank.
func providerLines(refs []workspaceconfig.PluginRef) []string {
	lines := make([]string, 0, len(refs))
	for _, r := range refs {
		switch {
		case r.Capability != "" && r.URI != "":
			lines = append(lines, r.Capability+" -> "+r.URI)
		case r.Capability != "":
			lines = append(lines, r.Capability)
		default:
			lines = append(lines, r.URI)
		}
	}
	return lines
}

// socketLines renders each socket attachment by its reference identity, adding a
// "(capability <name>)" note when the attachment resolves by capability.
func socketLines(refs []workspaceconfig.PluginRef) []string {
	lines := make([]string, 0, len(refs))
	for _, r := range refs {
		switch {
		case r.URI != "" && r.Capability != "":
			lines = append(lines, r.URI+" (capability "+r.Capability+")")
		case r.URI != "":
			lines = append(lines, r.URI)
		default:
			lines = append(lines, "capability "+r.Capability)
		}
	}
	return lines
}
