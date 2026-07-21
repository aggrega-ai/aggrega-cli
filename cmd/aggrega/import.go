// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parsersocket"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

var (
	importFormat string
	importStdout bool
)

// newImportCmd turns a foreign source description into draft Aggrega authoring
// files. It reads the source file, selects the importer for the source format,
// runs the importer, and writes the drafted files to disk — the operator's
// entry point to adopting existing infrastructure without hand-authoring the
// entities from scratch.
func newImportCmd() *cobra.Command {
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
top-level shape to recognise the format; if the shape is not recognised, import
stops and asks for --format rather than guessing. Detection reads only the
named file — no other files, no network.

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

			format, err := selectImportFormat(importFormat, path, content)
			if err != nil {
				return err
			}

			rw, err := workspaceFromContext(cmd.Context())
			if err != nil {
				return err
			}
			res, load, parserRes, parserLoad, err := configImporterSeam(rw)
			if err != nil {
				return err
			}

			// Consumer-scoped with-config for the importer serving this format:
			// only the matching plugins.importer attachment's inputs are threaded,
			// never another attachment's.
			sources, settings, err := importAttachmentInputs(rw, format)
			if err != nil {
				return err
			}

			return runImport(cmd.Context(), res, load, parserRes, parserLoad, content, importOptions{
				format:   format,
				stdout:   importStdout,
				workDir:  ".",
				out:      cmd.OutOrStdout(),
				errOut:   cmd.ErrOrStderr(),
				sources:  sources,
				settings: settings,
			})
		},
	}
	c.Flags().StringVar(&importFormat, "format", "", "Name the source format explicitly, overriding shape detection (for example langflow or flowise)")
	c.Flags().BoolVar(&importStdout, "stdout", false, "Write the drafted files to standard output instead of to disk")
	return c
}

// selectImportFormat resolves the source format for a run: an explicit format
// the operator named wins outright; otherwise the format is detected from the
// file's top-level shape. It mirrors the extension-vs-explicit precedence the
// parser socket uses, with content sniffing standing in for the extension
// fallback because the supported source formats share the .json extension.
func selectImportFormat(explicit, path string, content []byte) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	return detectFormat(path, content)
}

// detectFormat selects the source format from the file's top-level JSON shape
// when the operator did not name one. It disambiguates the two supported
// agent-definition formats, which both use the .json extension, by where the
// flow graph sits in the top-level object:
//
//   - langflow — the graph is nested under a top-level "data" object, so the
//     top-level "data.nodes" is an array.
//   - flowise — the graph is flat at the top level, so top-level "nodes" and
//     "edges" are both arrays and there is no "data.nodes".
//
// When neither shape matches — a different JSON shape, or content that is not
// JSON at all — detectFormat does not guess: it returns an error instructing
// the operator to pass --format. Detection reads only content; it makes no
// network or filesystem access, so it is deterministic and reproducible.
func detectFormat(path string, content []byte) (string, error) {
	var probe struct {
		Data struct {
			Nodes json.RawMessage `json:"nodes"`
		} `json:"data"`
		Nodes json.RawMessage `json:"nodes"`
		Edges json.RawMessage `json:"edges"`
	}
	if err := json.Unmarshal(content, &probe); err != nil {
		return "", undetectedFormatError(path)
	}

	// Langflow wins first: a graph nested under data.nodes is the disambiguator,
	// so a file carrying it is langflow regardless of any flat top-level keys.
	if isJSONArray(probe.Data.Nodes) {
		return "langflow", nil
	}
	// Flowise: a flat graph — nodes and edges arrays directly at the top level,
	// with no data.nodes wrapper (already excluded above).
	if isJSONArray(probe.Nodes) && isJSONArray(probe.Edges) {
		return "flowise", nil
	}
	return "", undetectedFormatError(path)
}

// undetectedFormatError is the loud, actionable diagnostic for a source whose
// shape detection cannot classify. It names the file and tells the operator to
// pass --format rather than leaving the failure to a guessed, wrong importer.
func undetectedFormatError(path string) error {
	return fmt.Errorf("cannot detect the source format of %q; pass --format to name it explicitly (for example --format langflow or --format flowise)", path)
}

// isJSONArray reports whether raw is a JSON array by its first non-whitespace
// byte. It intentionally rejects a JSON null (which json.Unmarshal into a slice
// would accept) and an absent field (empty raw), so only a genuine array value
// counts toward format detection.
func isJSONArray(raw json.RawMessage) bool {
	for _, b := range raw {
		switch b {
		case ' ', '\t', '\n', '\r':
			continue
		case '[':
			return true
		default:
			return false
		}
	}
	return false
}

// importOptions carries the resolved inputs for one import run: the selected
// format, whether to write to standard output rather than disk, the directory
// disk output is rooted at, the output and diagnostic sinks, and the
// consumer-scoped with-config the resolved importer reads. Threading the sinks
// and inputs in keeps the run testable without touching process globals.
//
// sources are the named source files read from the resolved importer
// attachment's with.sources; settings is that attachment's with.settings
// marshalled to the opaque options document. Both are nil when the attachment
// declares neither, so a settings-free run stays byte-identical to a bare import.
type importOptions struct {
	format   string
	stdout   bool
	workDir  string
	out      io.Writer
	errOut   io.Writer
	sources  []abi.ImportSource
	settings json.RawMessage
}

// runImport is the testable inner path: it invokes the import socket with the
// injected importer resolver and loader plus the parser resolver and loader the
// importer draws on to obtain its parsed source tree, surfaces the importer's
// diagnostics, and writes the drafted files either to standard output or to
// disk. It returns an error
// when the importer raised an error-severity diagnostic, so a mapping the
// importer could not complete surfaces as a non-zero exit even though any
// partial files it produced are still written.
func runImport(ctx context.Context, res importersocket.ImporterResolver, load importersocket.ImporterLoader, parserRes parsersocket.ParserResolver, parserLoad parsersocket.ParserLoader, source []byte, opts importOptions) error {
	result, err := importersocket.ImportArtifact(ctx, res, load, parserRes, parserLoad, opts.format, source, opts.sources, opts.settings)
	if err != nil {
		return err
	}

	reportImportDiagnostics(opts.errOut, result.Diagnostics)

	if opts.stdout {
		if err := writeImportStdout(opts.out, result.Files); err != nil {
			return err
		}
	} else {
		if err := writeImportFiles(opts.workDir, result.Files); err != nil {
			return err
		}
		for _, f := range result.Files {
			fmt.Fprintf(opts.out, "wrote %s\n", f.Path)
		}
	}

	return importDiagnosticsError(result.Diagnostics)
}

// writeImportFiles writes each drafted file to disk at its path relative to
// root, creating parent directories as needed. Paths are interpreted as
// forward-slash-delimited (the wire form) and joined onto root.
func writeImportFiles(root string, files []abi.ScaffoldFile) error {
	for _, f := range files {
		dest := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create directory for import file %q: %w", f.Path, err)
		}
		if err := os.WriteFile(dest, []byte(f.Content), 0o644); err != nil {
			return fmt.Errorf("write import file %q: %w", f.Path, err)
		}
	}
	return nil
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

// reportImportDiagnostics writes each importer diagnostic to w as a
// "severity: message" line, in the order the importer returned them, so an
// unresolved node or an ambiguous mapping is surfaced loudly rather than
// silently dropped.
func reportImportDiagnostics(w io.Writer, diags []abi.Diagnostic) {
	for _, d := range diags {
		fmt.Fprintf(w, "%s: %s\n", d.Severity, d.Message)
	}
}

// importDiagnosticsError returns a non-nil error when any diagnostic is of
// error severity, so the command exits non-zero on an import the importer
// could not complete. The diagnostics themselves are already surfaced on
// stderr by reportImportDiagnostics; this only governs the exit outcome.
func importDiagnosticsError(diags []abi.Diagnostic) error {
	for _, d := range diags {
		if d.Severity == "error" {
			return errors.New("import raised one or more error-severity diagnostics; see the messages above")
		}
	}
	return nil
}

// configImporterSeam builds the real resolver and loader for an import run.
// Resolution is capability-select — the natural `aggrega import` resolution: the
// importer is chosen by the format it advertises (import:<format>) among the
// catalog's importer plugins, so a workspace attaching more than one importer
// resolves the right one per format rather than pinning a single one. The
// resolved plugin's bytes come from the shared plugin cascade (embedded,
// colocated, local, or cached), the same byte-resolution path every
// workspace-level socket plugin uses. The plugins.importer attachment surface
// still carries each importer's with-config, threaded consumer-scoped by
// importAttachmentInputs; the catalog decides which importer runs.
//
// A plugins.importer attachment's function field is currently inert: an importer
// has a single import export, so there is no alternate entry to dispatch to. The
// field is treated like the provider socket's — carried on the shared attachment
// shape but not wired to anything — until an importer surface with more than one
// entry exists.
//
// It also builds the parser seam the importer draws on to obtain its parsed
// source tree. The parser is selected by the same format capability from the
// same catalog view (the catalog resolves the parser advertising the format),
// and its bytes come from the same plugin cascade, so one loader closure serves
// both sockets — a parser plugin loads exactly like any other. Parser and
// importer resolution share the single catalog view and its embedded-resolution
// posture; no separate enable is needed for the parser side.
func configImporterSeam(rw *workspaceconfig.ResolvedWorkspace) (importersocket.ImporterResolver, importersocket.ImporterLoader, parsersocket.ParserResolver, parsersocket.ParserLoader, error) {
	if rw == nil {
		return nil, nil, nil, nil, errors.New("no workspace configuration resolved; run import from within a workspace that attaches an importer")
	}
	wsCfg := rw.Config()
	dirs := workspaceconfig.ResolveCatalogDirs(nil, wsCfg, rw.Root())
	cat, err := catalog.LoadAllWithCacheDir(rw.Root(), wsCfg.ResolvedArtifactCacheDir(), dirs...)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("load catalog for import resolution: %w", err)
	}
	cat.UseEmbeddedProviderResolution()

	in := workspacePluginResolutionInputs(rw)
	res := capabilitySelectImporter{cat: cat}
	load := func(p types.Plugin) (wasm.PluginSpec, error) {
		resolved, rerr := resolvePluginURI(p.ArtifactReference(), in)
		if rerr != nil {
			return wasm.PluginSpec{}, rerr
		}
		return resolver.PluginSpec(p.Coordinate(), resolved), nil
	}
	return res, load, cat, load, nil
}

// capabilitySelectImporter is the ImporterResolver backed by the catalog's
// capability resolution: it resolves the importer advertising import:<format>
// among the catalog's importer plugins, halting loudly on an unadvertised or
// ambiguous format. An importer is selected by the format it serves — the natural
// resolution for `aggrega import` — rather than by a single pinned attachment.
type capabilitySelectImporter struct {
	cat *catalog.Catalog
}

func (r capabilitySelectImporter) ImporterPluginForFormat(format string) (types.Plugin, error) {
	return r.cat.ImporterPluginForFormat(format)
}

// importAttachmentInputs resolves the consumer-scoped with-config for the
// importer serving format: it selects the plugins.importer attachment that
// applies to this format, reads that attachment's with.sources into named
// sources and marshals its with.settings, and returns only that attachment's
// inputs — never another attachment's. A workspace with no matching attachment,
// or a matching attachment declaring no with, threads nothing (nil, nil).
func importAttachmentInputs(rw *workspaceconfig.ResolvedWorkspace, format string) ([]abi.ImportSource, json.RawMessage, error) {
	if rw == nil {
		return nil, nil, nil
	}
	ref, ok := selectImporterAttachment(rw.Config().Plugins.Importer, format)
	if !ok {
		return nil, nil, nil
	}
	return importWithInputs(ref.With)
}

// selectImporterAttachment picks the plugins.importer attachment whose with-config
// applies to format: an attachment naming the import:<format> capability wins
// outright; otherwise a sole attachment applies (the single-importer shape, which
// names one importer without a per-format capability). More than one attachment
// with no capability match is ambiguous for this format, so none is selected and
// the importer runs with no with-config rather than guessing which attachment's
// inputs to thread — the resolution of WHICH importer runs is the catalog's job,
// this only decides whose with-config is threaded.
func selectImporterAttachment(refs []workspaceconfig.PluginRef, format string) (workspaceconfig.PluginRef, bool) {
	// The importer capability vocabulary ("import:<format>") is authored on the
	// attachment's capability field; it mirrors the catalog's importCapabilityPrefix.
	capability := "import:" + format
	for _, ref := range refs {
		if ref.Capability == capability {
			return ref, true
		}
	}
	if len(refs) == 1 {
		return refs[0], true
	}
	return workspaceconfig.PluginRef{}, false
}

// importWithInputs builds the consumer-scoped importer inputs from one
// attachment's with-config: with.sources read into base-name-keyed,
// name-sorted ImportSource entries (live files, modeled on the policy socket's
// source reading), and with.settings marshalled to a deterministic JSON document
// (Go's map-key-sorted encoding). Both are optional; a with declaring neither
// yields (nil, nil). It reads only the passed attachment's with, so one importer
// attachment's inputs never leak into another's invocation.
func importWithInputs(with *workspaceconfig.PluginRefWith) ([]abi.ImportSource, json.RawMessage, error) {
	if with == nil {
		return nil, nil, nil
	}
	sources, err := importSources(with.Sources)
	if err != nil {
		return nil, nil, err
	}
	settings, err := importSettings(with.Settings)
	if err != nil {
		return nil, nil, err
	}
	return sources, settings, nil
}

// importSources reads the attachment's declared source patterns into named
// import sources. Each pattern is glob-expanded; a pattern matching no file is a
// loud miss (a misconfigured attachment, not a silent empty import). Each matched
// file's bytes are carried under its base name (never an absolute host path — for
// portability and determinism), and the result is name-sorted so the wire is
// byte-identical across runs. Returns nil when the attachment declares no sources.
func importSources(patterns []string) ([]abi.ImportSource, error) {
	if len(patterns) == 0 {
		return nil, nil
	}
	var sources []abi.ImportSource
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return nil, fmt.Errorf("expand import source pattern %q: %w", pattern, err)
		}
		if len(matches) == 0 {
			return nil, fmt.Errorf("import source %q matched no files", pattern)
		}
		for _, m := range matches {
			content, rerr := os.ReadFile(m)
			if rerr != nil {
				return nil, fmt.Errorf("read import source %q: %w", m, rerr)
			}
			sources = append(sources, abi.ImportSource{Name: filepath.Base(m), Bytes: content})
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Name < sources[j].Name })
	return sources, nil
}

// importSettings marshals the attachment's free-form settings object to the
// opaque options document the importer reads. Go's encoding/json sorts map keys,
// so the document is byte-deterministic. Returns nil when the attachment declares
// no settings, which the host omits from the wire payload.
func importSettings(settings map[string]any) (json.RawMessage, error) {
	if len(settings) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("marshal import settings: %w", err)
	}
	return raw, nil
}
