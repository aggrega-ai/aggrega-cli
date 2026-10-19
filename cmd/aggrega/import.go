// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// importFlags carries the `import` command's flag state. It is constructed per
// command so two invocations in one process cannot observe each other's flag
// values.
type importFlags struct {
	format    string
	dryRun    bool
	outputDir string
	sync      bool
}

// newImportCmd turns a foreign source description into draft Aggrega authoring
// files. It hands the source — a file, or a directory — to the import operation,
// which reads it, selects the importer for the source format, runs the importer,
// and writes the drafted files to disk — the operator's entry point to adopting
// existing infrastructure without hand-authoring the entities from scratch.
func newImportCmd() *cobra.Command {
	opts := &importFlags{}
	c := &cobra.Command{
		Use:   "import <file or directory>",
		Short: "Turn a foreign source description into draft Aggrega authoring files",
		Long: `Turn a foreign source description into draft Aggrega authoring files.

The importer reads the source, drafts Aggrega entities from it, and writes the
drafted files beneath the root of the workspace the run resolved, from whichever
folder the command is run in (creating parent directories as needed). The
translation is done by the importer plugin that advertises the source format;
import selects it, runs it, and writes what it produces. An importer in the
workspace's catalog has its .wasm read from the places a build reads a plugin
from — beside its Plugin entity in a catalog folder among them.

The compiler's core importers serve their formats in every workspace, with no
catalog entry. The first import that needs one this machine does not hold
fetches it from the workspace's default registry, holds it to the module the
compiler's release publishes, verifies its signature under that registry's
trust policy and the host functions it uses, and keeps it in the workspace's
content cache, so every later import reads it from there and reaches no
network. Nothing else is written for it: no lockfile, and no record of trusted
plugins. A module that does not verify is refused, and nothing is kept. Where
the module cannot be fetched — the workspace is offline, its registry cannot be
reached, or the registry does not hold it — import stops, naming the importer
and its version and what puts its module on the machine: running the import
once with the network available, where that would fetch it, or placing the
module beside a Plugin entity of that importer at that version in a catalog
folder.

The source is a file or a directory. A file is handed to the importer whole. A
directory is read into named sources: exactly the files the importer's declared
file patterns match, at any depth, each named by its path inside the directory
written with forward slashes, in sorted order, and no other file of the
directory is opened. A link inside the directory is never followed out of it:
the directory is held to itself wherever it lies, inside the workspace or
outside it, so a link leading out of it — to a directory, or to a file the
importer's patterns match (any importer's, when the format is recognized) — is
refused before anything is read. A link to a directory is never entered, and a
link to a file no pattern matches is passed over unread. An importer that
declares no file patterns reads one file and refuses a directory, and a
directory holding no file its patterns match is refused too. The files the
importer's attachment names under with: sources reach it beside the directory's
files, under their own base names, and a name the two share is refused rather
than one hiding the other.

A plugins.importer attachment attaches the importer its uri names by identity
and version — importer/<name>:<version>, as plugin add importer writes it. An
attached importer that advertises the source format is the one that runs, at
the version its uri names, whatever other importers advertise the format too.
Where more than one importer advertises the format and no single one of them is
attached, import stops and names the importers it could not choose between. The
core importers are among them: a workspace whose catalog holds an importer of
another name for a format a core importer serves attaches the one it wants.
Within one name the highest version runs, and an importer the catalog holds at
the name and version of a core importer runs in its place. An attachment's
with: sources and settings are handed to the importer it attaches and to no
other, so an importer no attachment names is handed none.

Pass --output-dir to write the drafts beneath another directory instead. A
relative path resolves against the directory you run the command in, as every
path you type does, and a drive named bare on Windows, such as D:, is the
current directory on that drive. The directory is resolved before any draft is
placed beneath it, so a draft whose path opens with a separator lands beneath
it rather than at the top of the drive. A directory you name outside
the workspace is used where you named it, and what is written beneath it is
held to it. Wherever the drafts go, a link on the way to one that leads out of
the workspace — or, beneath a directory you named outside it, out of that
directory — is refused, and nothing is written.

Beside its drafts, every import that drafts anything writes .aggrega-import.json,
the record of what it wrote: each drafted file and, in a drafted Aggregate, each
key at every depth, with a digest of the text written there. A key's text is its
line, its value's lines where its value is not a mapping of keys, and the comment
lines written directly above it at its own indentation. The record is JSON, so
nothing that reads the workspace's YAML documents reads it; keep it beside the
drafts for as long as you mean to sync them.

An import never writes over an Aggregate already at one of its drafted paths:
an Aggregate is the document an author goes on editing once it is drafted, so
import stops, naming it and --sync, and writes nothing — under --dry-run too.
Every other file already at a drafted path is written over, and a file an
earlier import drafted that this one does not draft is left where it is.

--sync brings what an earlier import drafted up to date with the source instead,
changing only what the import wrote. It judges every part three ways — what the
record says the import last wrote there, what is on disk, and what the source
drafts now:

  - a part the source left as the import last wrote it stays exactly as it is
    on disk, whether or not you edited it;
  - a part the source changed is written anew, where it is on disk as the
    import last wrote it;
  - a part the source dropped is removed, where it is as the import last wrote
    it and holds nothing you added — a drafted file is deleted whole;
  - a part the source gained is added beside the parts the draft names around
    it.

Everything else stays byte for byte: a key you added anywhere — a parameter, a
Feature, under a drafted Component instance too, another Component — a comment
not written directly above a drafted key, and every file the record does not
list. Before anything is written, standard error names each part that drifted,
one line each:

  added|removed|changed <key> in <file>
  added|removed|changed <file>

Where the sync would change or remove a part you edited, add a part where you
already wrote one, or remove a part holding something you added, import stops,
naming each such part and why, and writes nothing; reconcile them by hand and
sync again. A comment you write directly above a drafted key edits that key.
An Aggregate at a drafted path that no record lists stops the sync too, saying
so, as does a record that cannot be read. Where the importer reports a
diagnostic that stops the import, a sync writes nothing. A sync into a directory
holding no record and nothing at its drafted paths writes the whole draft.

With --sync, --dry-run writes nothing and names the same drift, or stops on the
same conflicts, and the exit status says which: 0 when the drafts are in step
with the source, 2 when they drifted, and 1 on an error or when the sync is
refused. Standard output carries the files the sync would write, with the
content it would give them.

On Windows, a draft whose path holds a part Windows cannot hold as written — a
name it reserves for a device, such as NUL or aux, with or without an extension
and in any letter case, or a part ending in a full stop or a space — is refused
naming the draft, and nothing is written. On every other platform such a draft
is written as it is named.

The source format selects how the importer reads the source. Pass --format to
name it explicitly. When --format is omitted, import recognizes the format by
the signatures the importers declare — the workspace catalog's and the core
importers' alike: each names the syntax a source is read in, the file names it
applies to and the paths its parsed tree must or must not carry. A file is
parsed once for each syntax a signature applying to its name reads it in, and
the format of the one importer whose signature matches is the one read. The
core importers' signatures read a file as JSON, and those of the Langflow,
Flowise and n8n importers, and the MCP importer's for a registry server
description, name no file, so recognizing a file imported on its own reads it
as JSON too. The MCP importer recognizes a client's configuration by its name —
.mcp.json, claude_desktop_config.json, .vscode/mcp.json or .cursor/mcp.json —
so such a file under another name, or one holding comments, is imported naming
--format mcp. A directory is recognized the
same way over the files it holds: each signature is tried against the files
its file names match, and a signature that names no file applies to none of
them. If no signature matches, import stops, asks for --format and names the
formats the importers advertise; if signatures of more than one format match,
it stops and names them. It does not guess. Recognition reads nothing but the
source — of a directory, only the files a signature's file names match — and
reaches no network.

Recognition and the importer read the source through the parser the
workspace's catalog serves for its syntax, and, for a syntax the catalog
serves no parser for, through the parser the compiler itself carries for it —
JSON among them — so a workspace whose catalog holds no parser still
recognizes and imports a source the core importers read. Where the catalog
serves a parser for a syntax, the compiler's own plays no part in reading it.
The compiler's parsers are part of the binary, so nothing is fetched for one.
A syntax neither serves stops the import, naming the syntax no parser serves,
and a source its parser refuses stops the import with the parser's reason.

Before the drafted output, import names on standard error the format it read
the source as and the directory the drafted paths are relative to, then any
diagnostics the importer reported, then one line for each source node the
importer mapped, in the importer's order:

  mapped <node id> (<node type>) to <target> as a <mode>, <tier> at <score>

naming the catalog Component or draft it chose for the node, whether it is
referenced or drafted, the match tier, and the score of the best candidate it
weighed. A node the importer weighed no candidate for has no score, and its line
ends at the tier; a node it made no choice for reads "made no choice for <node
id> (<node type>)"; then, under --sync, the drift lines. Standard output
carries only the result — the paths written, and under --sync the paths
removed, or the drafted files themselves under --dry-run — so it can be piped
unchanged.

--dry-run does everything import does except write: standard output carries one
document listing each drafted file's path and its content, so the result can be
reviewed, parsed or piped before anything reaches disk. Every other behaviour is
the write's — the same importer runs over the same source and reports the same
diagnostics — so what a dry run shows is what a write would put on disk, and a
source the write would refuse is refused here too.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// The source is a path typed here, and it is handed on as typed: the
			// operation reads it, a file or a directory, so this surface reads
			// nothing of its own.
			path := args[0]

			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}

			// A destination named on the command line is a path typed here, and it
			// is handed on as typed: the operation resolves a directory it is
			// named against the folder the command runs in, as every typed path is,
			// in the one place every surface's named directory is made a directory
			// path, and the first line names the directory that resolution gave. With
			// none named, none is handed: the operation's own default — the root of
			// the workspace the run resolved — is the command's, so the command line
			// and every other surface naming no destination land their drafts in one
			// place.
			outputDir := opts.outputDir

			// The flag is turned into a delivery once, here, and every branch
			// below reads that value through its own predicate rather than
			// re-reading the flag. The flag and the delivery carry the same
			// answer today, and a second branch on the flag would be a second
			// spelling of one question — the spelling that stops moving the day a
			// third delivery is admitted.
			delivery := authoredcontent.DeliveryFor(opts.dryRun)
			res, err := importops.Import(cmd.Context(), rw, importops.Options{
				SourcePath:    path,
				SourceName:    path,
				Format:        opts.format,
				OutputDir:     outputDir,
				Sync:          opts.sync,
				Delivery:      delivery,
				ResolvePlugin: importPluginBytes,
				// The import's commentary — how the source was read, then what the
				// importer reported about it, then how it mapped each node, then
				// what drifted — goes to the error stream, and it goes there once
				// everything is judged and before anything is written. Under
				// --dry-run the standard output stream holds one parseable document
				// and nothing else, so a commentary line there would corrupt what a
				// caller is parsing; putting all of it on one stream is also what
				// lets a reader see which importer raised an unmapped-node warning
				// and what it chose for that node.
				BeforeWrite: func(res importops.Result) {
					reportImportPreamble(cmd.ErrOrStderr(), res)
					reportImportDiagnostics(cmd.ErrOrStderr(), res.Diagnostics)
					reportImportDecisions(cmd.ErrOrStderr(), res.Decisions)
					reportImportDrift(cmd.ErrOrStderr(), res.Drift)
				},
			})
			if err != nil {
				return err
			}

			if delivery.Reports() {
				if err := reportImportedFiles(cmd.OutOrStdout(), res.Files); err != nil {
					return err
				}
			} else {
				for _, name := range res.Written {
					fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", name)
				}
				for _, name := range res.Removed {
					fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", name)
				}
			}

			if halt := res.Refusal(); halt != nil {
				return halt
			}
			// Under --sync --dry-run the exit status says whether the drafts drifted,
			// so a check can fail on drift without reading the lines above. The
			// lines already say which parts, so the status carries no message of
			// its own.
			if opts.sync && delivery.Reports() && len(res.Drift) > 0 {
				return exitCodeError{code: importDriftExitStatus}
			}
			return nil
		},
	}
	c.Flags().StringVar(&opts.format, "format", "", "Name the source format explicitly, overriding detection by the importers' signatures (for example langflow, flowise, n8n or mcp)")
	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Do everything except write: hand back the drafted files as content, naming the file each is for, and create or modify nothing. Every other behaviour is the write's, the importer run included. Without it the drafts are written to disk.")
	c.Flags().StringVar(&opts.outputDir, "output-dir", "", "Directory the drafted files are written under. A relative path resolves against the directory you run the command in, and a drive named bare on Windows, such as D:, is the current directory on that drive. Without it the drafts are written beneath the root of the workspace the run resolved.")
	c.Flags().BoolVar(&opts.sync, "sync", false, "Bring what an earlier import drafted up to date with the source, changing only what the import wrote, as the record beside the drafts lists it, and naming each part that drifted. With --dry-run, exit 0 when in step, 2 when drifted, 1 on an error or a refusal.")
	return c
}

// importDriftExitStatus is the exit status of `import --sync --dry-run` when the
// drafts drifted from the source: 0 is in step, and 1 stays every error and
// refusal, so a check reading only the status tells the three apart.
const importDriftExitStatus = 2

// reportImportDrift writes one line per part a sync brings up to date, in the
// order the draft names them. The sentence is the shared one
// ([importops.DriftLine]), so the assistant's import tool words the same drift
// the same way. An import that is not a sync, and a sync in step, add no line.
func reportImportDrift(w io.Writer, drift []importops.Drift) {
	for _, d := range drift {
		fmt.Fprintln(w, importops.DriftLine(d))
	}
}

// importPluginBytes resolves an importer or parser plugin URI to its module
// bytes through the shared plugin cascade.
//
// It is an adapter rather than a second resolution: the cascade it binds is the
// one the install path, the plugin listing and the runtime plugin runner all
// build from, so a URI one path accepts every other path resolves identically.
// It exists because that cascade's inputs are derived here, in the command
// package, and the operations layer takes the resolution as a seam rather than
// rebuilding it.
//
// The one input it adds is colocated, the directories beside the Plugin entities
// of the catalog view the import seam loaded to select the importer. With them
// the cascade reads a module laid down beside its entity in a catalog directory,
// as a build does, so an importer authored there runs without being published.
// They arrive from that seam rather than being derived here, because only the
// view that selected the plugin knows where its entity sits. The install path,
// the plugin listing and the runtime plugin runner hand none, so what they
// resolve is unchanged.
func importPluginBytes(rw *workspaceconfig.ResolvedWorkspace, uri string,
	colocated map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error) {
	in := workspacePluginResolutionInputs(rw)
	in.colocatedDirs = colocated
	return resolvePluginURI(uri, in)
}

// reportImportedFiles hands the drafted files back as content instead of
// writing them, in the reported form every capability that reports authored
// content answers with.
//
// The form is the shared one rather than a framing written here, and the
// difference is what makes the output parseable rather than a matter of taste.
// A header-line-per-file concatenation cannot be read back: a drafted file
// whose own first line begins with the header marker is indistinguishable from
// the start of the next file, so there is no parse
// that recovers the set, and a caller piping the stream has to invent an
// escaping convention the producer never applied. The shared form is one YAML
// document carrying a documents list, so a caller reads it with the parser it
// already has and content beginning with a comment or a document marker needs
// no escaping at all.
//
// Each entry carries the path the importer returned, relative to the output
// root the commentary line names — the same path the writing delivery lands the
// file at, so what is reported and what would be written address one file.
//
// The files are reported in the order the importer returned them, which the
// shared writer preserves, so the same source reports the same bytes.
func reportImportedFiles(w io.Writer, files []abi.ScaffoldFile) error {
	docs := make([]authoredcontent.Document, 0, len(files))
	for _, f := range files {
		docs = append(docs, authoredcontent.Document{Path: f.Path, Content: f.Content})
	}
	return authoredcontent.Report(w, docs)
}

// reportImportPreamble writes the one line that says how to read everything the
// import prints after it: the source format it ran for, and the directory the
// drafted paths are relative to.
//
// The format is what says which importer read the file, and it is the one fact
// the drafted output does not already carry — with --format omitted the format
// is recognised by the signatures the importers declare, and nothing
// else says which format they recognised. It is named on every run rather than only on a recognised
// one: conditioning the line on how the format was arrived at would make the
// output depend on the route to the fact rather than on the fact, and would put
// a second reading of "was this named explicitly" at a surface whose job is to
// report what the import did.
//
// The output root is named because every drafted path is relative to it and
// carries no anchor of its own — the wrote lines under a writing delivery and
// the reported paths under --dry-run are both root-relative. It is therefore
// true under both deliveries, which is why the line frames the paths rather
// than claiming a destination: under --dry-run nothing is written anywhere, and
// a line announcing a directory the run never touched would be worse than no
// line at all.
//
// The root is a full path — the workspace root where the run named no
// destination, and the --output-dir it named otherwise — and it is printed as
// the command line prints every location it reports, as written rather than
// quoted, so a Windows path reads with its own separators rather than each one
// doubled.
//
// Both are read from the result rather than from the values passed to the call,
// so the line names what the import actually ran with and the two cannot come
// to disagree.
func reportImportPreamble(w io.Writer, res importops.Result) {
	fmt.Fprintf(w, "read as the %s format, with drafted paths relative to %s\n", res.Format, res.OutputDir)
}

// reportImportDiagnostics writes each importer diagnostic to w as a
// "severity: message" line, in the order the importer returned them, so an
// unresolved node or an ambiguous mapping is surfaced loudly rather than
// silently dropped.
func reportImportDiagnostics(w io.Writer, diags []abi.Diagnostic) {
	for _, d := range diags {
		fmt.Fprintf(w, "%s: %s\n", d.Severity, d.Message)
	}
}

// reportImportDecisions writes one line per mapping decision the importer
// returned, in its order, so an author sees what each source node was mapped to
// and how sure the importer was — where the import guessed — without opening the
// drafts. The sentence is the shared one ([importops.DecisionLine]), so the
// assistant's import tool words the same decision the same way. An importer that
// reports no decisions adds no line.
func reportImportDecisions(w io.Writer, decisions []abi.ImportDecision) {
	for _, d := range decisions {
		fmt.Fprintln(w, importops.DecisionLine(d))
	}
}
