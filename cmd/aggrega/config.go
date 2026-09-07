// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
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

Configuration comes from a cascade rather than from one file: a global config,
then every .aggrega-config.yaml from the workspace root down, then environment
overrides. Later layers win over earlier ones. The workspace root is the nearest
ancestor directory whose .aggrega-config.yaml carries a workspace: block.

Because the effective configuration is assembled rather than written anywhere,
"aggrega config show" is how you see what actually applies, and --origin is how
you find out which layer each setting came from. "aggrega config init" seeds a
working configuration, and "aggrega config capture" freezes the effective
build-critical settings into this workspace so a build does not depend on a
global file that is not committed.`,
		Example: `  # What applies right now
  aggrega config show

  # Which layer each setting came from
  aggrega config show --origin

  # Seed a workspace configuration
  aggrega config init

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
a config file, an environment variable, or the built-in defaults. Scalar
settings are attributed per value; list and map settings are attributed as a
whole to the layer that last changed them.`,
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
	c.Flags().BoolVar(&origin, "origin", false, "Annotate each effective setting with the layer it came from (a config file, an environment variable, or the built-in defaults).")
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
		return fmt.Errorf("workspaceconfig: %w", err)
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
	var report bool

	c := &cobra.Command{
		Use:   "init",
		Short: "Seed a working configuration with a registry and its trust policy",
		Long: `Seed a working configuration by registering a registry and its signature trust
policy, so a fresh machine can pull and verify signed artifacts without editing
YAML by hand.

By default it registers the official hub (hub.aggrega.ai) with signatures
required and the official trusted identities. The registry's advertised trust
policy is fetched once from its own origin and pinned locally — trust-on-first-
use; when the registry is unreachable or advertises nothing, the built-in
official baseline is written instead, so init still produces a usable config
offline. Pass --registry to seed a different registry (for an air-gapped or
private hub).

The entry is written to the global config shared across every workspace by
default; pass --workspace to write it to this project's workspace config
instead. If the target config file already exists, init refuses rather than
overwrite it — pass --force to write anyway (the registry entry is merged into
the existing file, other settings preserved).

A seeded configuration is a document you own and go on editing, so --report hands
the content back instead of writing it: nothing is created or modified, and the
answer names the file the content is for — which matters here, because the target
is the machine-wide global config unless --workspace selects this project's own.
A caller that keeps its own document model puts the content there, where it is
visible before it is saved and can be amended or undone. --report needs no
--force: an existing file is never overwritten because nothing is written at all,
and the content reported is the merge a forced write would produce.

Under --report standard output carries that content and nothing else, so it stays
parseable. A registry that advertises no trust policy still produces its notice,
on standard error: read that stream too, or a configuration seeded with no trust
policy arrives without the advice to pin identities by hand.`,
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
			return configInitRun(configInitConfig{
				Registry:  registry,
				Force:     force,
				Global:    global,
				Workspace: workspace,
				Report:    report,
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
	c.Flags().BoolVar(&report, "report", false, "Report the seeded configuration as content, naming the file it is for, and write nothing. Without it the entry is written to the resolved config file.")
	return c
}

// configInitConfig is the input contract for `aggrega config init`.
type configInitConfig struct {
	// Registry is the host to seed with its trust policy. Empty is treated as
	// the official hub by the underlying add path's host resolution.
	Registry string
	// Force permits writing when the target config file already exists. Without
	// it, init refuses rather than touch an existing file.
	Force bool
	// Global and Workspace are the write-target overrides, mirroring
	// `registry add`. A registry is tool-scoped, so init defaults to the global
	// config; --workspace forces this project's workspace file. Setting both is a
	// usage error.
	Global    bool
	Workspace bool
	// Report asks for the seeded configuration as content rather than as a write.
	// A seeded configuration is a document its owner goes on editing, so a caller
	// holding a document model takes the content and decides where it lands.
	Report bool

	ws *workspaceconfig.ResolvedWorkspace
	// Out carries the answer, and ErrOut an advisory the answer cannot convey.
	//
	// The pair exists because under Report the answer on Out is one parseable
	// document and nothing else, so a trust-policy advisory the reported
	// configuration cannot express — that the registry advertised no policy and
	// identities must be pinned by hand — would be dropped rather than merely
	// moved. It goes to ErrOut, which leaves the document's shape alone.
	Out    io.Writer
	ErrOut io.Writer
	// trustClient transports the advertised-trust fetch. Nil selects a default
	// client; tests inject one so no live network is contacted.
	trustClient *http.Client
}

// configInitRun binds the command's flags to the seeding operation and renders
// what it did on the terminal's two streams. The seeding itself — the
// write-target resolution, the refuse-if-exists guard, and the registration the
// trust policy comes from — lives in internal/configops, so this surface and the
// daemon perform one seeding rather than each performing its own.
func configInitRun(cfg configInitConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}

	delivery := authoredcontent.DeliveryFor(cfg.Report)
	res, initErr := configops.Init(rw, configops.InitOptions{
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
		return fmt.Errorf("workspaceconfig: %w", err)
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
