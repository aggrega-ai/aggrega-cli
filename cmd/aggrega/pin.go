// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-cli/internal/dryrunreport"
	"github.com/aggrega-ai/aggrega-cli/internal/entitypin"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// newPinCmd builds the `pin` command: the entity author's own step, which records
// in the entity.lock beside an entity the content pins for the inputs it reads
// from outside its own document.
func newPinCmd() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:   "pin <entity-path>",
		Short: "Record the content pins for an entity's external inputs in the entity.lock beside it",
		Long: `Record, in the entity.lock beside a Recipe or FeatureRecipe, the content pin for every input
the entity reads from outside its own document: a remote source: whose coordinates you wrote out
in full, a file an artifacts entry imports with $import, and each file of a local source:.

Pinning is the entity author's step. Run it on an entity before it is consumed — before you
publish it, and after every change to one of its inputs — and commit the entity.lock it writes
beside the entity. A pull over an entity whose lock does not record these pins halts and names
this command, and so does a push of it; so does one whose local source has had a file added,
changed or removed since it was pinned.

No Release is needed. A source whose coordinates carry no template names one artifact for every
Release that uses the entity, so it is resolved and pinned here, with nothing supplied. A remote
source whose coordinates are filled in from a template is not pinned here: which artifact it names is
decided by each Release, and each identity it resolves to is pinned in that Release's lockfile.
A source that names a directory beside the entity is not fetched: each file it contributes to what
a publication of the entity carries is digested as it stands and pinned by its path, so a build
refuses a file added, changed or removed after the pin. A local source written as a template
contributes every file in the entity's directory, so each of those is pinned.

Which files a source directory contributes is decided here, once, and recorded. The step reads the
ignore rules the plugin serving the entity's capability declares — this workspace's provider binding
for that capability, or else the plugin built into aggrega — together with the ignore files that
plugin names inside the source, such as a chart's .helmignore, and the .aggregaignore beside the
entity, in gitignore syntax, whose lines come last and so have the final say. It records the plugin
and the composed rules in the entity.lock, and every publication, pull, build and render of the
entity applies the recorded rules and no others, so a changed binding or a newer binary moves
nothing until you pin again. A symbolic link among the files is refused rather than followed.

The step writes beside the entity, so the entity needs a writable home: the document you name is
read from its own directory and the lock lands next to it. The entity file itself is never
written to. Re-running over unchanged inputs leaves the lock byte-identical; an entity that reads
nothing a lock records has its lock removed. A Component, a Feature or any other kind that reads
no such input is reported as having nothing to pin, and nothing beside it is touched.

A directory holds one entity.lock, so the step refuses a Recipe or FeatureRecipe that shares its
directory with any other entity, naming the entity in the way, whether or not either has anything
to pin. Every kind has a directory of its own except Aggregate, Release and Manifest, which may sit
side by side; a push holds the entity it publishes to the same rule.

aggrega pull --pin-sources and aggrega push --pin-sources run this same step on your behalf, and
write the same bytes.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			rw, err := workspaceForSubject(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return pinRun(cmd.Context(), cmd.OutOrStdout(), rw, args[0], dryRun)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "Resolve the entity's inputs and print the entity.lock the step would write instead of writing it: its path and the content it would hold go to stdout, and nothing on disk moves. A lock the step would leave byte-identical is not printed. The resolution runs exactly as it would otherwise, so the content cache still fills")
	return c
}

// helpPullPinSources and helpPushPinSources are the two commands' --pin-sources
// help. The flag is one concept on both commands — run the author's own pin step
// on their behalf — so the two sentences say the same thing about the step, the
// writable home and the precedence, and differ only in which entities the step
// runs over and which setting the flag overrides.
//
// "Writable home" is the catalog's meaning of local: an entity whose document a
// catalog directory holds. It is never the reference-text meaning a source's own
// spelling carries, and the help says which so a reader does not take one for the
// other.
const (
	helpPullPinSources = "Run the entity author's pin step over the Recipes and FeatureRecipes this Release builds, " +
		"recording in the entity.lock beside each the pin for every input it reads — a remote source: written out in " +
		"full, a file an artifacts entry imports, and each file of a local source — instead of halting on an entity whose lock does not record " +
		"them. The same step as aggrega pin, writing the same bytes. It writes beside the entity, so it runs only " +
		"where the entity has a writable home: a document in one of your catalog directories. An entity resolved " +
		"from the cache has none and is never pinned. Overrides the workspace's pin_sources_at_pull in both " +
		"directions: --pin-sources pins for this run and --pin-sources=false halts for this run, whatever the " +
		"setting says"
	helpPushPinSources = "Run the entity author's pin step on the entity before bundling it, so the entity.lock the " +
		"step writes beside it is part of what is published, instead of refusing an entity whose lock does not record " +
		"its pins. The same step as aggrega pin, writing the same bytes. It " +
		"writes beside the entity, so it runs only where the entity has a writable home: a document in a directory " +
		"it was read from — which a push always has, since it publishes the entity document you name. Overrides the " +
		"workspace's pin_sources_at_push in both directions: --pin-sources pins for this run and " +
		"--pin-sources=false does not, whatever the setting says"
)

// explicitBool reads a boolean flag as the answer an invocation gave, or nil
// where it gave none.
//
// A flag that overrides a workspace setting in both directions has three
// answers — pin, do not pin, and "the setting decides" — and a plain boolean
// carries two of them. Reading whether the flag was passed is what supplies the
// third, so an invocation that said nothing defers to the workspace while
// --pin-sources=false still overrides a workspace configured to pin.
func explicitBool(cmd *cobra.Command, name string, value bool) *bool {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	v := value
	return &v
}

// pinRun runs the pin step over one entity and renders the command line's own
// account of what it did.
//
// The step itself lives in the pin step's own package, which every surface that
// pins reaches; this translates the command's arguments into that step's options
// and turns its result into the lines a terminal expects.
func pinRun(ctx context.Context, out io.Writer, rw *workspaceconfig.ResolvedWorkspace, entityPath string, dryRun bool) error {
	// The terminal is shown the step's own sentence about what it did, the one a
	// publication that pins prints too, so the result's facts reach it through
	// that sentence rather than field by field here.
	//
	// projection-omits internal/entitypin.Result.Pinnable: the summary printed below opens "Nothing to pin" where it is false, which is the whole of what it tells a terminal reader
	// projection-omits internal/entitypin.Result.Kind: the summary printed below names the entity's kind on every outcome
	// projection-omits internal/entitypin.Result.Name: the summary printed below names a pinned entity by the name its outcome records, and an entity with nothing to pin is named by the document the invocation gave
	// projection-omits internal/entitypin.Result.EntityPath: the invocation named the entity document itself, and the summary printed below names the lock written beside it, or that document where there was nothing to pin
	// projection-omits internal/entitypin.Result.Outcome: the summary printed below states the outcome — whether the lock moved or was removed, the lock's path, and how many pins of each class it holds
	res, err := entitypin.Pin(ctx, entitypin.Options{
		Workspace:  rw,
		EntityPath: entityPath,
		DryRun:     dryRun,
	})
	if err != nil {
		return fmt.Errorf("aggrega pin: %w", err)
	}
	if dryRun && res.Report != nil {
		dryrunreport.Write(out, []dryrunreport.File{*res.Report})
	}
	fmt.Fprintln(out, res.Summary(dryRun))
	return nil
}
