// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// newConfigCmd builds the `config` parent command grouping the
// configuration-management subcommands.
func newConfigCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "config",
		Short: "Inspect and manage the workspace configuration",
	}
	c.AddCommand(newConfigCaptureCmd())
	return c
}

// newConfigCaptureCmd builds the `config capture` command: it freezes the
// effective build-critical configuration into this project's workspace config
// file so the workspace builds identically without relying on the machine's
// global configuration.
func newConfigCaptureCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "capture",
		Short: "Freeze the effective build-critical configuration into this workspace",
		Long: `Write the effective build-critical configuration into this project's workspace
config file, so the workspace builds identically without relying on the
machine's global configuration.

The build-critical subset is the configuration a build participates in: the
provider bindings, the mutator/policy/linter/importer plugin socket
attachments, the plugin sidecar directory, and the catalog directories. Each is
written at its effective value — the value that resolves right now with the
global configuration and the workspace tree merged together — so a binding that
today comes only from the global config is materialized into the workspace and
travels with the project.

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
				ws:  rw,
				Out: cmd.OutOrStdout(),
			})
		},
	}
	return c
}

// configCaptureConfig is the input contract for `aggrega config capture`.
type configCaptureConfig struct {
	ws  *workspaceconfig.ResolvedWorkspace
	Out io.Writer
}

// configCaptureRun writes the effective build-critical configuration subset into
// the resolved workspace's config file and reports what it captured and where.
// The destination is always the workspace config file — capture's whole purpose
// is to make the workspace self-contained, so it never targets the global config.
func configCaptureRun(cfg configCaptureConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	dest := filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)
	effective := rw.Config()

	if err := workspaceconfig.CaptureBuildCritical(dest, effective); err != nil {
		return fmt.Errorf("aggrega config capture: write %s: %w", dest, err)
	}
	reportCapture(cfg.Out, effective, dest)
	return nil
}

// reportCapture prints the build-critical fields captured into dest, in a fixed
// field order so the output is deterministic. A field with no effective value is
// omitted. When nothing was build-critical, it says so rather than printing an
// empty capture.
func reportCapture(out io.Writer, cfg workspaceconfig.Config, dest string) {
	if out == nil {
		return
	}
	wrote := false
	section := func(header string, lines []string) {
		if len(lines) == 0 {
			return
		}
		if !wrote {
			fmt.Fprintf(out, "Captured build-critical configuration into %s:\n", dest)
			wrote = true
		}
		fmt.Fprintf(out, "  %s:\n", header)
		for _, l := range lines {
			fmt.Fprintf(out, "    %s\n", l)
		}
	}

	section("providers", providerLines(cfg.Plugins.Providers))
	section("mutator", socketLines(cfg.Plugins.Mutator))
	section("policy", socketLines(cfg.Plugins.Policy))
	section("linter", socketLines(cfg.Plugins.Linter))
	section("importer", socketLines(cfg.Plugins.Importer))
	if cfg.PluginSidecarDir != "" {
		section("plugin_sidecar_dir", []string{cfg.PluginSidecarDir})
	}
	section("catalog_dirs", cfg.Workspace.CatalogDirs)

	if !wrote {
		fmt.Fprintf(out, "No build-critical configuration to capture into %s.\n", dest)
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
