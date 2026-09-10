// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"

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
	format string
	report bool
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
the drafted files into the current directory (creating parent directories as
needed). The importer attached at the workspace's import socket does the
translation; import selects it, runs it, and writes what it produces.

The source format selects how the importer reads the file. Pass --format to
name it explicitly. When --format is omitted, import inspects the file's
top-level shape to recognize the format; if the shape is not recognized, import
stops and asks for --format rather than guessing. Detection reads only the
named file — no other files, no network.

Before the drafted output, import names on standard error the format it read
the source as and the directory the drafted paths are relative to, alongside
any diagnostics the importer reported. Standard output carries only the result
— the paths written, or the drafted files themselves under --report — so it can
be piped unchanged.

With --report, the drafted files are reported as content and nothing is
written: standard output carries one document listing each drafted file's path
and its content, so the result can be reviewed, parsed or piped before anything
reaches disk.`,
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

			// The format is selected before the attachment inputs because the
			// with-config is consumer-scoped to the importer serving THIS format:
			// only the matching plugins.importer attachment's inputs are threaded,
			// never another attachment's.
			format, err := importops.SelectFormat(opts.format, path, content)
			if err != nil {
				return err
			}
			sources, settings, err := importops.AttachmentInputs(rw, format)
			if err != nil {
				return err
			}

			// The flag is turned into a delivery once, here, and every branch
			// below reads that value through its own predicate rather than
			// re-reading the flag. The flag and the delivery carry the same
			// answer today, and a second branch on the flag would be a second
			// spelling of one question — the spelling that stops moving the day a
			// third delivery is admitted.
			delivery := authoredcontent.DeliveryFor(opts.report)
			res, err := importops.Import(cmd.Context(), rw, importops.Options{
				Source:        content,
				SourceName:    path,
				Format:        format,
				OutputDir:     ".",
				Delivery:      delivery,
				ResolvePlugin: importPluginBytes,
				Sources:       sources,
				Settings:      settings,
			})
			if err != nil {
				return err
			}

			// The import's commentary — how the source was read, then what the
			// importer reported about it — goes to the error stream. Under
			// --report the standard output stream holds one parseable document
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

			return importDiagnosticsError(res)
		},
	}
	c.Flags().StringVar(&opts.format, "format", "", "Name the source format explicitly, overriding shape detection (for example langflow or flowise)")
	c.Flags().BoolVar(&opts.report, "report", false, "Report the drafted files as content, naming the file each is for, and write nothing. Without it the drafts are written to disk.")
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
func importPluginBytes(rw *workspaceconfig.ResolvedWorkspace, uri string) (pluginpath.ResolvedPlugin, error) {
	return resolvePluginURI(uri, workspacePluginResolutionInputs(rw))
}

// reportImportedFiles hands the drafted files back as content instead of
// writing them, in the reported form every capability that reports authored
// content answers with.
//
// 🔴 The form is the shared one rather than a framing written here, and the
// difference is not cosmetic. A header-line-per-file concatenation cannot be
// read back: a drafted file whose own first line begins with the header marker
// is indistinguishable from the start of the next file, so there is no parse
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
// is recognised from the source's own shape, and nothing else says which shape
// was recognised. It is named on every run rather than only on a recognised
// one: conditioning the line on how the format was arrived at would make the
// output depend on the route to the fact rather than on the fact, and would put
// a second reading of "was this named explicitly" at a surface whose job is to
// report what the import did.
//
// The output root is named because every drafted path is relative to it and
// carries no anchor of its own — the wrote lines under a writing delivery and
// the reported paths under --report are both root-relative. It is therefore
// true under both deliveries, which is why the line frames the paths rather
// than claiming a destination: under --report nothing is written anywhere, and
// a line announcing a directory the run never touched would be worse than no
// line at all.
//
// Both are read from the result rather than from the values passed to the call,
// so the line names what the import actually ran with and the two cannot come
// to disagree.
func reportImportPreamble(w io.Writer, res importops.Result) {
	fmt.Fprintf(w, "read as the %s format, with drafted paths relative to %q\n", res.Format, res.OutputDir)
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

// importDiagnosticsError returns a non-nil error when the import raised a
// halt-severity diagnostic, so the command exits non-zero on an import the
// importer could not complete. The diagnostics themselves are already surfaced
// on stderr by reportImportDiagnostics; this only governs the exit outcome.
//
// The condition is read through the result's own predicate rather than by
// scanning severities here, so the command's exit outcome and a protocol
// answer's field cannot come to disagree about whether one import completed.
func importDiagnosticsError(res importops.Result) error {
	if res.Halted() {
		return errors.New("import raised one or more halt-severity diagnostics; see the messages above")
	}
	return nil
}
