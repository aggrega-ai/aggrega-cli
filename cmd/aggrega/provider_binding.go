// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// providerBindingHelperEligible reports whether pull may prompt the operator to
// disambiguate an ambiguously-served render target interactively. The helper is
// opt-out and auto-on: it runs only when stderr is a terminal AND stdin is
// interactive AND the pull-progress surface is not silenced (--quiet). A
// non-interactive pull (CI, a pipe) or a --quiet pull never prompts — the
// ambiguity halts loudly instead, so the pull never hangs waiting on input.
func providerBindingHelperEligible(cfg pullConfig) bool {
	return fileTerminal(os.Stderr) && isTerminal() && !cfg.diag.quiet
}

// runProviderBindingHelper prompts the operator to choose which provider plugin
// renders an ambiguously-served target, writes the choice into the workspace's
// `.aggrega-config.yaml` `plugins.providers` list, and updates the in-memory
// config so the immediately-following re-resolution adopts it. candidates is the
// sorted candidate set the ambiguity was raised over; root is the workspace root
// the config file lives at; out is the prompt sink (the pull's diagnostic
// stream). It never resolves the ambiguity silently: an unreadable choice or a
// write failure returns an error that halts the pull.
func runProviderBindingHelper(target string, candidates []types.Plugin, root string, wsCfg *workspaceconfig.Config, out io.Writer) error {
	if len(candidates) < 2 {
		// The helper is only reached on a genuine ambiguity; a degenerate set is a
		// programming error, surfaced rather than silently guessed.
		return fmt.Errorf("provider binding helper: target %q has %d candidates; expected more than one", target, len(candidates))
	}
	fmt.Fprintf(out, "Render target %q is served by more than one provider plugin. Choose which renders it:\n", target)
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

	configPath := filepath.Join(root, workspaceconfig.ConfigFilename)
	if err := workspaceconfig.UpsertProviderBinding(configPath, target, chosen.Metadata.Name); err != nil {
		return fmt.Errorf("provider binding helper: write %s: %w", configPath, err)
	}
	wsCfg.Plugins.Providers = workspaceconfig.UpsertProviderRef(wsCfg.Plugins.Providers, target, chosen.Metadata.Name)
	fmt.Fprintf(out, "Recorded plugins.providers entry for %q = %s in %s\n", target, chosen.Metadata.Name, configPath)
	return nil
}
