// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
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
// manifest annotations and every version's manifest digest, verified signer and
// publication date, and writes a deterministic local searchable index a later
// offline search reads.
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

A read the registry refuses or drops in passing — a 429 or 5xx answer, a
connection reset or timed out, an answer cut short — is asked again a few times,
with a growing wait between attempts, and where the registry says how long to
wait (Retry-After) the wait is as long as it says. A repository with a read still
failing after that is not recorded from this run: it keeps the record the index
already holds for it, where that record was judged under the trust policy now in
force, and is otherwise left out of the index; either way it is named as a
repository that could not be read, as the walk goes and again in the closing
summary under its registry's row, with the kind of the read's last failure — the
status the registry answered, a timeout, a reset, an answer cut short — and the
read that failed. So a version is never recorded unsigned,
undated or without its deprecation marker because a read failed, with or without
--upgrade.

A registry whose repository listing repeats itself — naming a repository it has
already named, or answering two pages in a row with no repository while still
pointing to a next page — is not followed round again. That registry is reported
as not indexed, with a message naming it and saying its repository listing
repeated itself; the other registries are still indexed, and the command exits
non-zero.

A registry that is not indexed keeps the index it had before the run, unchanged:
nothing is written for a registry whose walk failed, whose listing repeated
itself, or that was being walked when the command was interrupted. Registries
indexed earlier in the same run keep what this run wrote for them. A registry's
new index is written aside and put in place whole, so a command stopped while
writing one leaves that registry with a complete index — the one it had before
the run, or the new one — or with none, never with a partial one.

Every strict-SemVer version a recorded repository publishes is listed too, each
with the manifest digest its tag resolves to — the address of the exact bytes
published under that version. Where the registry's trust policy pins a public key
or trusted identities, and verification is not switched off, each version's own
signatures are verified against that policy and the signer is recorded when one
verifies; a version with no signature, or none that verifies, is recorded with no
signer. Where several verify, the signer recorded is the one the transparency log
recorded earliest, whatever order the registry lists them in, so signing a version
again never changes its recorded signer.

Each version's publication date is recorded too, with its source. Where one of the
version's keyless signatures verified, the date is the time the transparency log
recorded that earliest signature, as the verified log entry attests it (source
'transparency-log') — so signing a version again never moves its date. Otherwise
it is the date the publication itself stated beside what it published (source
'publisher'), the earliest where several are stated: the publishing machine's own
clock, which nothing attests, read from any registry whatever its trust policy.
A version with neither — published without stating a date, and not signed
keyless in a way this policy verifies — is recorded with no date.

Only manifests, signature attachments and publication-date attachments are read:
no entity's content is fetched. 'aggrega search -o json' reports these under each
record's 'published' field.

The per-repository records are byte-identical across two runs against the same
registry state; the freshness timestamp is written to a sibling file so it never
disturbs the records.

A refresh reads again only what changed, and finds every change a full re-read
would. For each repository it revalidates the tag listing with a conditional
request, resolves every version's tag to its manifest digest, and revalidates every
version's attachments — its signatures, publication-date statements and deprecation
markers — with one conditional request, plus one for the signatures of a
deprecation marker it judged before. A version whose digest or attachments changed
is read again in full, and its signatures and markers are verified again exactly as
a full re-read verifies them; everything else is kept as recorded. A change to this
registry's trust policy reads every version again under the policy now in force, and
so does a repository whose entry in the index records none of these checks. An
unchanged registry therefore costs one conditional request per repository and two
small requests per version (three where a deprecation marker was judged), with
nothing downloaded. A version is read again on the next refresh where it carries a
deprecation marker that was not honored, or where this run got no way to
revalidate its attachments — which, on a registry that offers none, is every
version.

Several repositories are read at a time. Everything the command records and prints
comes out in repository order, so the index and the output are the same as reading
one repository at a time would produce.

Use --upgrade to read every repository in full, comparing nothing with the index
already written — to rebuild an index from the registry alone. It finds no change a
refresh without it would miss.

Each published version's deprecation markers are read as the walk goes, and the ones
whose own signature verifies are recorded into the index. Every marker read is
reported, whether or not it was recorded — a version kept as recorded is not read, so
its marker is not reported again — and each registry reports how many markers its
index now records, including when that number is zero. Where signature
verification is disabled for a registry, the walk reports that its markers were not
examined rather than a count, so a registry that published nothing is never confused
with one this run had no basis to judge.`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			return registryUpdateRun(cmd.Context(), registryUpdateConfig{
				ws:      rw,
				Args:    args,
				Upgrade: upgrade,
				Out:     cmd.OutOrStdout(),
			})
		},
	}
	c.Flags().BoolVar(&upgrade, "upgrade", false, "Read every repository in full, comparing nothing with the index already written (a refresh without it already re-reads whatever changed)")
	return c
}

// registryUpdateConfig is the input contract for `aggrega registry update`,
// populated by the cobra command from its optional argument and flags.
type registryUpdateConfig struct {
	// Args carries the optional single registry token (host or alias). Empty
	// selects every configured registry.
	Args []string
	// Upgrade reads every repository in full, comparing nothing with the index
	// already written. A refresh without it already re-reads everything that
	// changed, so Upgrade rebuilds an index from the registry alone rather than
	// picking up a change the refresh would miss.
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
//
// The walk's own result is then stated as a closing summary. The commentary is
// per-event and says each thing at the moment it happens, which on a walk over
// several registries leaves the outcome spread across the stream — and leaves
// parts of it out of the stream altogether.
func registryUpdateRun(ctx context.Context, cfg registryUpdateConfig) error {
	rw, err := workspaceOrResolve(cfg.ws)
	if err != nil {
		return err
	}
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}
	var token string
	if len(cfg.Args) == 1 {
		token = cfg.Args[0]
	}
	res, walkErr := registryops.Update(ctx, rw, registryops.UpdateOptions{
		Registry: token,
		Upgrade:  cfg.Upgrade,
		Now:      cfg.now,
	}, out)
	reportRegistryUpdate(out, res)
	return walkErr
}

// reportRegistryUpdate writes the closing summary of an index build: how many of
// the selected registries were indexed, what each indexed registry's walk found —
// how many entities it indexed, and each repository it could not read, named with
// its reason — which registries were not indexed, and the directory the index was
// written to.
//
// The running commentary carries none of them as an outcome. It names a
// repository as it skips it, at that moment and among everything else the walk
// says, but never says how many a registry could not read; it names a registry as it
// fails but never gathers them, so a reader watching a walk over several
// registries has to scroll back to find which are missing; and it never names the
// directory the index was written to, which is what says a refresh changed
// nothing another project on this machine resolves through. What is deliberately
// not restated is the reason a registry could not be indexed: the walk states
// that where it happens.
//
// Whether each registry was indexed is read from that registry's own record
// rather than from the walk's gathered list of failures, because the list is
// filled only on the path that walks every configured registry: a build asked
// for one registry by name records the failure on that registry and returns, so
// a count taken from the list would report the named registry as indexed.
//
// projection-omits UpdateResult.Failed: it gathers a subset of the registries
// whose own record already carries the error that stopped them, and it is empty
// on the path that indexes a single named registry, so the per-registry records
// are the only complete account of what was and was not indexed.
func reportRegistryUpdate(out io.Writer, res registryops.UpdateResult) {
	if res.NoRegistriesConfigured {
		// The walk says so as it starts, and there is no index to describe: the
		// index directory is resolved only once there is a registry to walk.
		return
	}
	if len(res.Hosts) == 0 {
		// Nothing was walked, so there is no outcome to summarize.
		return
	}

	var indexed, notIndexed []registryops.HostUpdate
	for _, host := range res.Hosts {
		if host.Err != "" {
			notIndexed = append(notIndexed, host)
			continue
		}
		indexed = append(indexed, host)
	}

	selected := countOfNoun(len(indexed)+len(notIndexed), "registry", "registries")
	if len(notIndexed) == 0 {
		fmt.Fprintf(out, "Indexed %s.\n", selected)
	} else {
		fmt.Fprintf(out, "Indexed %d of %s.\n", len(indexed), selected)
	}
	for _, host := range indexed {
		line := fmt.Sprintf("  %s: %s", host.Host, countOfNoun(host.Indexed, "entity", "entities"))
		if unread := len(host.Skipped); unread > 0 {
			line += fmt.Sprintf(", %s could not be read", countOfNoun(unread, "repository", "repositories"))
		}
		fmt.Fprintln(out, line)
		for _, skipped := range host.Skipped {
			fmt.Fprintf(out, "    %s: %s\n", skipped.Repo, skipped.Reason)
		}
	}
	if len(notIndexed) > 0 {
		names := make([]string, 0, len(notIndexed))
		for _, host := range notIndexed {
			names = append(names, host.Host)
		}
		fmt.Fprintf(out, "Not indexed: %s\n", strings.Join(names, ", "))
	}
	if len(indexed) > 0 && res.IndexRoot != "" {
		fmt.Fprintf(out, "Index written to %s\n", res.IndexRoot)
	}
}

// countOfNoun renders a count with the singular or plural form of its noun, so a
// summary line reads correctly for one registry as well as for several.
//
// It is deliberately local to this package. Several packages carry an equivalent
// of their own, none of them exported, and reaching for one of those would mean
// exporting a formatting detail from a package that has no other reason to
// publish it.
func countOfNoun(n int, singular, plural string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, singular)
	}
	return fmt.Sprintf("%d %s", n, plural)
}
