// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/registryops"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The index build itself lives in the shared operations layer, which every
// surface reaches. What stays here is the cobra wiring and the terminal
// concerns: the flags, and the stream the walk's running commentary is written
// to. The names below resolve to that one implementation so this package's
// existing spellings — and the fixtures that drive them — address the shared
// layer rather than a second copy of it.
type (
	registryIndexSource = registryops.IndexSource
	// RegistryIndex is one host's assembled index, as the writer takes it.
	RegistryIndex = registryops.HostIndex
)

// newRegistryUpdateCmd builds `registry update [<name|host>]`, which walks each
// configured registry's repositories, reads each repository's latest-version
// manifest annotations, and writes a deterministic local searchable index a
// later offline search reads.
func newRegistryUpdateCmd() *cobra.Command {
	var upgrade bool
	c := &cobra.Command{
		Use:   "update [<name|host>]",
		Short: "Build the local searchable index of configured registries",
		Long: `Walk each configured registry's repositories, read the latest published version
of each, and write a deterministic local searchable index to the artifact cache.

With no argument, every configured registry is indexed. With a single registry
argument (a host or a friendly alias), only that registry is indexed.

This is the one registry command that reaches the network. The index it writes
lands in this workspace's own artifact cache, so refreshing it changes nothing
another project on this machine resolves through. Credentials are read from a
supplied or ambient source and never asked for: a registry that needs one you do
not have fails with a message naming what it wanted.

For each repository the highest strict-SemVer version is selected (pre-release
versions are skipped) and its catalog facets — kind, description, category,
labels, and, for plugins, socket type and capabilities — are recorded from the
published manifest. A repository with no conforming version is skipped; a
repository that cannot be read is skipped with a note, so one unreadable
repository does not abandon the whole index.

The per-repository records are byte-identical across two runs against the same
registry state; the freshness timestamp is written to a sibling file so it never
disturbs the records.

A refresh revalidates each repository conditionally: it re-resolves the selected
version's manifest digest and reuses the cached record untouched when the tag set
and digest are unchanged, so an unchanged registry costs a round of cheap digest
resolves rather than a full re-read of every repository's manifest and referrers.
Use --upgrade to ignore the cached validators and re-read every repository — this
picks up a change a conditional refresh skips, such as a deprecation marker added
to an existing version whose digest did not change.

Each published version's deprecation markers are read as the walk goes, and the ones
whose own signature verifies are recorded into the index. Every marker met is
reported, whether or not it was recorded, and each registry reports how many markers
its index now records — including when that number is zero. Where signature
verification is disabled for a registry, the walk reports that its markers were not
examined rather than a count, so a registry that published nothing is never confused
with one this run had no basis to judge.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return registryUpdateRun(registryUpdateConfig{
				ws:      rw,
				Args:    args,
				Upgrade: upgrade,
				Out:     cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().BoolVar(&upgrade, "upgrade", false, "Re-read every repository from the registry, ignoring what is already cached (picks up changes a conditional refresh would skip)")
	return c
}

// registryUpdateConfig is the input contract for `aggrega registry update`,
// populated by the cobra command from its optional argument and flags.
type registryUpdateConfig struct {
	// Args carries the optional single registry token (host or alias). Empty
	// selects every configured registry.
	Args []string
	// Upgrade forces a full re-read of every repository, ignoring the cached
	// revalidation validators. It is the escape from the conditional refresh's one
	// bound: a deprecation added to an existing version whose tag set and manifest
	// digest are unchanged is invisible to digest-compare, and a full re-read picks
	// it up.
	Upgrade bool

	ws  *workspaceconfig.ResolvedWorkspace
	Out io.Writer
	// now supplies the freshness timestamp written to each host's sibling file.
	// Nil selects the live clock; tests inject a frozen time for determinism.
	now func() time.Time
}

// registryUpdateRun builds a local index for each selected registry.
//
// The walk itself comes from the shared layer, so this command and every other
// surface run one index build rather than each performing its own. What is
// decided here is where the running commentary goes: a person is watching this
// stream, so the walk's notices are written to it as they happen rather than
// collected and rendered at the end — a network walk over several registries is
// exactly the case where a silent wait is the wrong answer.
func registryUpdateRun(cfg registryUpdateConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return fmt.Errorf("workspaceconfig: %w", err)
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	var token string
	if len(cfg.Args) == 1 {
		token = cfg.Args[0]
	}
	_, err = registryops.Update(rw, registryops.UpdateOptions{
		Registry: token,
		Upgrade:  cfg.Upgrade,
		Now:      cfg.now,
	}, out)
	return err
}
