// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// providerBindingHelperEligible reports whether pull may prompt the operator to
// disambiguate an ambiguously-served capability interactively. The helper is
// opt-out and auto-on: it runs only when stderr is a terminal AND stdin is
// interactive AND the pull-progress surface is not silenced (--quiet). A
// non-interactive pull (CI, a pipe) or a --quiet pull never prompts — the
// ambiguity halts loudly instead, so the pull never hangs waiting on input.
//
// A dry run is excluded for a different reason than the other three, and it is
// the reason the exclusion is not optional: answering the prompt WRITES the
// chosen binding into the workspace configuration, which is a file the run has
// just promised to leave alone. Left unattached, the ambiguity halts naming what
// to supply — the same loud outcome every caller with nobody watching gets, and
// a better one than a preview that edited a config file on its way past.
func providerBindingHelperEligible(cfg pullConfig) bool {
	return fileTerminal(os.Stderr) && isTerminal() && !cfg.diag.quiet && !cfg.DryRun
}

// runProviderBindingHelper prompts the operator to choose which provider plugin
// renders an ambiguously-served target, writes the choice into the workspace's
// `.aggrega-config.yaml` `plugins.providers` list, and updates the in-memory
// config so the immediately-following re-resolution adopts it. candidates is the
// sorted candidate set the ambiguity was raised over; rw is the resolution the
// answer is recorded against; out is the prompt sink (the pull's diagnostic
// stream). It never resolves the ambiguity silently: an unreadable choice or a
// write failure returns an error that halts the pull.
func runProviderBindingHelper(target string, candidates []types.Plugin, rw *workspaceconfig.ResolvedWorkspace, wsCfg *workspaceconfig.Config, out io.Writer) error {
	if len(candidates) < 2 {
		// The helper is only reached on a genuine ambiguity; a degenerate set is a
		// programming error, surfaced rather than silently guessed.
		return fmt.Errorf("provider binding helper: target %q has %d candidates; expected more than one", target, len(candidates))
	}
	fmt.Fprintf(out, "Capability %q is served by more than one provider plugin. Choose which renders it:\n", target)
	for i, p := range candidates {
		fmt.Fprintf(out, "  [%d] %s  (%s)\n", i+1, p.Metadata.Name, p.Metadata.Version)
	}
	fmt.Fprintf(out, "Enter a number [1-%d]: ", len(candidates))

	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil && err != io.EOF {
		return fmt.Errorf("provider binding helper: read choice: %w", err)
	}
	choice, cerr := strconv.Atoi(strings.TrimSpace(line))
	if cerr != nil || choice < 1 || choice > len(candidates) {
		return fmt.Errorf("provider binding helper: %q is not a valid choice in [1-%d]", strings.TrimSpace(line), len(candidates))
	}
	chosen := candidates[choice-1]

	// The interactive helper writes a build-scoped provider binding; it surfaces
	// no write-target flags, so it always resolves to the workspace default. The
	// destination is still resolved off the resolution rather than assembled from
	// a root, so the binding lands in the config file this run read and the site
	// stays correct if the operation's default file ever changes.
	rw, err = workspaceOrResolve(rw)
	if err != nil {
		return fmt.Errorf("provider binding helper: %w", err)
	}
	configPath, err := rw.WriteDestination(workspaceconfig.OpProviderBinding, false, false)
	if err != nil {
		return err
	}
	// The binding names the chosen plugin by its identity, <pluginType>/<name> —
	// the coordinate capability resolution compares a binding against — and not by
	// its bare name, which no binding is matched on.
	identity := chosen.Coordinate()
	if err := workspaceconfig.UpsertProviderBinding(configPath, target, identity); err != nil {
		return fmt.Errorf("provider binding helper: write %s: %w", configPath, err)
	}
	wsCfg.Plugins.Providers = workspaceconfig.UpsertProviderRef(wsCfg.Plugins.Providers, target, identity)
	fmt.Fprintf(out, "Recorded plugins.providers entry for %q = %s in %s\n", target, identity, configPath)
	return nil
}
