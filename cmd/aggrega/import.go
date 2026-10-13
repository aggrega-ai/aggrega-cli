// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

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
}

// newImportCmd turns a foreign source description into draft Aggrega authoring
// files. It reads the source file, selects the importer for the source format,
// runs the importer, and writes the drafted files to disk — the operator's
// entry point to adopting existing infrastructure without hand-authoring the
// entities from scratch.
func newImportCmd() *cobra.Command {
	opts := &importFlags{}
	c := &cobra.Command{
		Use:   "import <file>",
		Short: "Turn a foreign source description into draft Aggrega authoring files",
		Long: `Turn a foreign source description into draft Aggrega authoring files.

The importer reads the source file, drafts Aggrega entities from it, and writes
the drafted files beneath the root of the workspace the run resolved, from
whichever folder the command is run in (creating parent directories as needed).
The translation is done by the importer plugin in the workspace's catalog that
advertises the source format; import selects it, runs it, and writes what it
produces. Its .wasm is read from the places a build reads a plugin from — beside
its Plugin entity in a catalog folder among them — and nothing is fetched.

A plugins.importer attachment attaches the importer its uri names by identity
and version — importer/<name>:<version>, as plugin add importer writes it. An
attached importer that advertises the source format is the one that runs, at
the version its uri names, whatever other importers advertise the format too.
Where more than one importer advertises the format and no single one of them is
attached, import stops and names the importers it could not choose between. An
attachment's with: sources and settings are handed to the importer it attaches
and to no other, so an importer no attachment names is handed none.

Pass --output-dir to write the drafts beneath another directory instead. A
relative path resolves against the directory you run the command in, as every
path you type does. A directory you name outside the workspace is used where you
named it, and what is written beneath it is held to it. Wherever the drafts go,
a link on the way to one that leads out of the workspace — or, beneath a
directory you named outside it, out of that directory — is refused, and nothing
is written.

The source format selects how the importer reads the file. Pass --format to
name it explicitly. When --format is omitted, import recognizes the format by
the signatures the importers in the workspace's catalog declare: each names the
syntax a source is read in, the file names it applies to and the paths its
parsed tree must or must not carry. The file is parsed by the parser the catalog
serves for a syntax, once for each syntax a signature applying to its name reads
it in, and the format of the one importer whose signature matches is the one
read. If no signature matches, import stops, asks
for --format and names the formats the catalog's importers advertise; if
signatures of more than one format match, it stops and names them. It does not
guess. Detection reads only the named file — no other source, no network.

Before the drafted output, import names on standard error the format it read
the source as and the directory the drafted paths are relative to, alongside
any diagnostics the importer reported. Standard output carries only the result
— the paths written, or the drafted files themselves under --dry-run — so it can
be piped unchanged.

--dry-run does everything import does except write: standard output carries one
document listing each drafted file's path and its content, so the result can be
reviewed, parsed or piped before anything reaches disk. Every other behaviour is
the write's — the same importer runs over the same source and reports the same
diagnostics — so what a dry run shows is what a write would put on disk, and a
source the write would refuse is refused here too.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			content, err := os.ReadFile(path)
			if err != nil {
				return fmt.Errorf("read source %q: %w", path, err)
			}

			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}

			// A destination named on the command line is a path typed here, so it
			// is resolved against the folder the command runs in, as every typed
			// path is, and handed on whole so the first line names where the drafts
			// are. With none named, none is handed: the operation's own default — the
			// root of the workspace the run resolved — is the command's, so the
			// command line and every other surface naming no destination land their
			// drafts in one place.
			outputDir := ""
			if opts.outputDir != "" {
				outputDir, err = filepath.Abs(opts.outputDir)
				if err != nil {
					return fmt.Errorf("resolve --output-dir %q against the directory the command runs in: %w",
						opts.outputDir, err)
				}
			}

			// The flag is turned into a delivery once, here, and every branch
			// below reads that value through its own predicate rather than
			// re-reading the flag. The flag and the delivery carry the same
			// answer today, and a second branch on the flag would be a second
			// spelling of one question — the spelling that stops moving the day a
			// third delivery is admitted.
			delivery := authoredcontent.DeliveryFor(opts.dryRun)
			res, err := importops.Import(cmd.Context(), rw, importops.Options{
				Source:        content,
				SourceName:    path,
				Format:        opts.format,
				OutputDir:     outputDir,
				Delivery:      delivery,
				ResolvePlugin: importPluginBytes,
			})
			if err != nil {
				return err
			}

			// The import's commentary — how the source was read, then what the
			// importer reported about it — goes to the error stream. Under
			// --dry-run the standard output stream holds one parseable document
			// and nothing else, so a commentary line there would corrupt what a
			// caller is parsing; putting both on one stream is also what lets a
			// reader see which importer raised an unmapped-node warning.
			reportImportPreamble(cmd.ErrOrStderr(), res)
			reportImportDiagnostics(cmd.ErrOrStderr(), res.Diagnostics)

			if delivery.Reports() {
				if err := reportImportedFiles(cmd.OutOrStdout(), res.Files); err != nil {
					return err
				}
			} else {
				for _, name := range res.Written {
					fmt.Fprintf(cmd.OutOrStdout(), "wrote %s\n", name)
				}
			}

			return res.Refusal()
		},
	}
	c.Flags().StringVar(&opts.format, "format", "", "Name the source format explicitly, overriding detection by the catalog's importer signatures (for example langflow or flowise)")
	c.Flags().BoolVar(&opts.dryRun, "dry-run", false, "Do everything except write: hand back the drafted files as content, naming the file each is for, and create or modify nothing. Every other behaviour is the write's, the importer run included. Without it the drafts are written to disk.")
	c.Flags().StringVar(&opts.outputDir, "output-dir", "", "Directory the drafted files are written under. A relative path resolves against the directory you run the command in. Without it the drafts are written beneath the root of the workspace the run resolved.")
	return c
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
// is recognised by the signatures the catalog's importers declare, and nothing
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
