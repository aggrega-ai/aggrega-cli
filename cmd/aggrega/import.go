// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

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
	stdout bool
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
— the paths written, or the drafted files themselves under --stdout — so it can
be piped unchanged.

With --stdout, the drafted files are written to standard output — a header
line naming each file's path followed by its content — instead of to disk, so
the result can be reviewed or piped before anything is written.`,
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

			res, err := importops.Import(cmd.Context(), rw, importops.Options{
				Source:        content,
				SourceName:    path,
				Format:        format,
				OutputDir:     ".",
				Delivery:      authoredcontent.DeliveryFor(opts.stdout),
				ResolvePlugin: importPluginBytes,
				Sources:       sources,
				Settings:      settings,
			})
			if err != nil {
				return err
			}

			// The import's commentary — how the source was read, then what the
			// importer reported about it — goes to the error stream. Under
			// --stdout the standard output stream is the drafted files
			// themselves, so a commentary line there would corrupt what a caller
			// is piping; putting both on one stream is also what lets a reader
			// see which importer raised an unmapped-node warning.
			reportImportPreamble(cmd.ErrOrStderr(), res)
			reportImportDiagnostics(cmd.ErrOrStderr(), res.Diagnostics)

			if opts.stdout {
				if err := writeImportStdout(cmd.OutOrStdout(), res.Files); err != nil {
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
	c.Flags().BoolVar(&opts.stdout, "stdout", false, "Write the drafted files to standard output instead of to disk")
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

// writeImportStdout writes the drafted files to w as a deterministic
// concatenation: a "# <path>" header line per file, then the file's content,
// with a trailing newline so consecutive files stay separated. The files are
// emitted in the order the importer returned them.
func writeImportStdout(w io.Writer, files []abi.ScaffoldFile) error {
	for _, f := range files {
		if _, err := fmt.Fprintf(w, "# %s\n", f.Path); err != nil {
			return err
		}
		if _, err := io.WriteString(w, f.Content); err != nil {
			return err
		}
		if !strings.HasSuffix(f.Content, "\n") {
			if _, err := io.WriteString(w, "\n"); err != nil {
				return err
			}
		}
	}
	return nil
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
// the path headers under --stdout are both root-relative. It is therefore true
// under both deliveries, which is why the line frames the paths rather than
// claiming a destination: under --stdout nothing is written anywhere, and a
// line announcing a directory the run never touched would be worse than no line
// at all.
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
