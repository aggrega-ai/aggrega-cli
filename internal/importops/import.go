// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
	"github.com/aggrega-ai/aggrega-cli/pkg/writescope"
)

// EscapingPathError reports a scaffold path the importer returned that does not
// stay under the output root.
//
// 🔴 A scaffold path is supplied by the importer plugin, so it is untrusted
// input on every surface. Joining such a path onto an output root CLEANS a
// climbing segment rather than refusing it, so a returned "../elsewhere" lands
// outside the directory the caller named — which is why this is a refusal rather
// than a normalisation: silently rewriting the path to something contained would
// put a file somewhere the importer did not ask for and the caller cannot
// predict.
//
// Its message names no flag and no request field, so both surfaces report it
// verbatim; there is nothing here for either to re-word. It is a distinct type
// so a caller can branch on the condition without matching the sentence.
type EscapingPathError struct {
	// Path is the offending path exactly as the importer returned it.
	Path string
	// OutputDir is the root it was required to stay under.
	OutputDir string
}

// Error names the offending path and the root it left, so the reader can see
// which of the two to correct.
func (e *EscapingPathError) Error() string {
	return fmt.Sprintf("the importer returned the file path %q, which does not stay inside the output "+
		"directory %q; no files were written", e.Path, e.OutputDir)
}

// Offending returns the path the importer returned, so a surface reporting this
// refusal names the same path without parsing the message.
func (e *EscapingPathError) Offending() string { return e.Path }

// Options carries the resolved inputs for one import.
//
// Source is the source description's bytes and SourceName is what it is called,
// used only to name the file in a detection refusal. Format names the source
// format explicitly; an empty Format means the format is detected from Source.
//
// OutputDir is the directory drafted files are written under by a writing
// delivery, and the directory every drafted path is relative to under either
// delivery. An empty OutputDir is the root of the workspace the operation is
// handed: an import that names no destination writes beneath the root of the
// workspace its run resolved, wherever the caller happens to be standing. That
// default is decided here and nowhere else, so a surface that names no
// destination hands none rather than stating the default of its own. A non-empty
// OutputDir is the caller's — a directory a person typed, or one a request named
// — and is used as given, a relative one against the process's working
// directory. A reporting delivery writes nothing, and the reported paths are
// still relative to it.
//
// There is no field for the importer's with-config. What an importer is handed
// is read by [Import] from the workspace's own plugins.importer attachments,
// once the importer is resolved, because which attachment's inputs apply
// depends on which importer runs — and a surface computing them ahead of the
// resolution would be answering that before it is known.
type Options struct {
	Source        []byte
	SourceName    string
	Format        string
	OutputDir     string
	Delivery      authoredcontent.Delivery
	ResolvePlugin PluginBytesResolver
}

// Result is what one import produced.
//
// OutputDir is the directory the drafted paths are relative to: Options.OutputDir
// as the caller handed it, or the workspace root where it named none. It is read
// from here rather than from what the caller passed, so a surface reporting where
// the drafts went reports the directory the import actually used.
//
// Files is every drafted file the importer returned, in the importer's own
// order, carrying content whether or not it was written — so a reporting
// delivery answers with the same set a writing one puts on disk.
//
// Written is the output-root-relative path of each file actually written, in the
// order written, and is empty under a reporting delivery. It is separate from
// Files rather than a flag on each entry because "what was produced" and "what
// reached disk" are different questions, and a caller under a reporting delivery
// is asking only the first.
//
// Diagnostics is what the importer reported, in its own order. A halt-severity
// entry is carried here as data rather than returned as an error: an import that
// partially succeeds is exactly the case whose diagnostics are its most useful
// output, so reducing it to a failure would discard what the caller needs. Each
// surface decides its own outcome from [Result.Halted].
type Result struct {
	Format      string
	OutputDir   string
	Files       []abi.ScaffoldFile
	Written     []string
	Diagnostics []abi.Diagnostic
}

// Halted reports whether any diagnostic is of halt severity.
//
// It is a predicate here rather than a severity scan at each surface so the
// command line's exit outcome and a protocol answer's field cannot come to
// disagree about whether one import completed.
func (r Result) Halted() bool {
	for _, d := range r.Diagnostics {
		if d.Severity == abi.SeverityHalt {
			return true
		}
	}
	return false
}

// Import turns a foreign source description into draft Aggrega authoring files.
//
// It selects the source format, resolves the importer the catalog advertises for
// that format — the one a plugins.importer attachment names, where one names an
// advertiser — runs it with the with-config of the attachment naming it and of
// no other, and either writes the drafted files under the output directory —
// Options.OutputDir, or the root of the workspace rw where it names none — or
// returns them for the caller to place, according to Options.Delivery.
//
// The importer is resolved once, here, before it is run: the with-config it is
// handed depends on which importer the resolution chose, and the resolution
// reports that together with the importer, so the attachment that selected it and
// the attachment it is configured by are one answer rather than two.
//
// 🔴 The write is CONTAINED, and the containment is the reused predicate rather
// than a check written here. A scaffold path comes from the importer plugin, so
// every path is judged against [pathscope.IsLocalSlash] — the single home of
// "does this path stay inside the directory it is relative to", and the same
// predicate the rendered-artifact write is judged by. A path is a
// slash-separated logical name rather than a host path, which is the algebra
// that entry point owns: judging it as a host path would make a backslash a
// separator on one platform and an ordinary character on another, so the same
// importer output would be accepted on one machine and refused on the next.
//
// 🔴 Every path is judged BEFORE any file is written and before a reporting
// delivery answers, in one place (checkFiles), so a dry run refuses exactly the
// drafts a write refuses, and an importer returning one contained path and one
// escaping path writes neither. Validating as it went would leave the contained
// prefix on disk from a run that was refused, which is a partial result nobody
// asked for.
//
// That containment analysis is lexical and resolves no symbolic link, so every
// drafted path is also held, with its links resolved, to the root the output
// directory picks, in the same judgement.
//
// A halt-severity diagnostic is reported in the Result rather than returned as an
// error, and any files the importer produced alongside it are still written — an
// import the importer could not complete still drafts what it could. The returned
// error is reserved for a run that could not be performed: no workspace, no
// plugin resolver, a catalog that would not load, a guest that failed, an
// escaping path, or a write that failed.
func Import(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, opts Options) (Result, error) {
	if _, err := workspaceOrRefuse(rw); err != nil {
		return Result{}, err
	}
	if opts.ResolvePlugin == nil {
		return Result{}, fmt.Errorf("internal: no plugin resolver supplied — the caller must supply one and pass it in")
	}

	format, err := SelectFormat(opts.Format, opts.SourceName, opts.Source)
	if err != nil {
		return Result{}, err
	}

	seam, err := BuildSeam(rw, opts.ResolvePlugin)
	if err != nil {
		return Result{}, err
	}

	attachments := rw.Config().Plugins.Importer
	importer, at, resolveErr := seam.ImporterRes.ImporterPluginForFormat(format, attachmentURIs(attachments))
	var sources []abi.ImportSource
	var settings json.RawMessage
	if resolveErr == nil {
		if sources, settings, err = attachmentInputs(attachments, at); err != nil {
			return Result{}, err
		}
	}

	imported, err := importersocket.ImportArtifact(ctx, resolvedImporter{plugin: importer, err: resolveErr},
		seam.ImporterLoad, seam.ParserRes, seam.ParserLoad, format, opts.Source, sources, settings)
	if err != nil {
		return Result{}, err
	}

	outputDir := outputDirFor(rw, opts.OutputDir)
	res := Result{
		Format:      format,
		OutputDir:   outputDir,
		Files:       imported.Files,
		Diagnostics: imported.Diagnostics,
	}
	// Every drafted path is judged before anything is written and before anything
	// is reported, so a reporting delivery refuses exactly what the write refuses.
	if err := checkFiles(rw.Root(), outputDir, imported.Files); err != nil {
		return Result{}, err
	}
	if opts.Delivery.Reports() {
		return res, nil
	}

	written, err := writeFiles(outputDir, imported.Files)
	if err != nil {
		return Result{}, err
	}
	res.Written = written
	return res, nil
}

// resolvedImporter hands the import socket the outcome of the resolution [Import]
// already made, so the importer is resolved once per import.
//
// The outcome is handed over whole — the importer, or the refusal — rather than
// the refusal being reported here, so a format no importer serves is reported
// exactly as the socket reports any resolution it is refused, in its words and
// with its own context, and not in a second spelling of them.
type resolvedImporter struct {
	plugin types.Plugin
	err    error
}

func (r resolvedImporter) ImporterPluginForFormat(string) (types.Plugin, error) {
	return r.plugin, r.err
}

// outputDirFor returns the directory an import's drafts are written under and
// relative to: the one the caller named, or, where it named none, the root of the
// workspace rw. It is the one statement of that default, so the command line, the
// assistant's import tool and any other caller naming no destination all land
// their drafts at the same root rather than wherever their process happens to be
// sitting.
func outputDirFor(rw *workspaceconfig.ResolvedWorkspace, named string) string {
	if named != "" {
		return named
	}
	return rw.Root()
}

// checkFiles judges every drafted file before any is written and before a
// reporting delivery answers. It is where an import's containment verdict is
// taken, for both deliveries, so a dry run and a write refuse the same drafts.
//
// Two judgements, the first over the whole set before the second begins. Each
// path must stay inside outputDir as a slash-separated logical name
// ([pathscope.IsLocalSlash]): one climbing out of it, through slashes or
// backslashes, an absolute one and an empty one are refused as an
// [EscapingPathError] naming the path and outputDir, whatever is on disk. Only
// then is each place a file would be written at held, with its links resolved:
// it must not lead, through a link, out of the root the output directory picks
// against the workspace at workspaceRoot — the workspace root where the output
// directory lies inside the workspace, and the output directory itself, a
// directory a person typed outside the workspace, say, where it lies outside it.
// The drafts are files the product writes on the caller's behalf, so a link
// standing in the output directory, or in a directory on the way to a draft,
// would otherwise turn an import into writing files elsewhere on the machine.
//
// The order means the link judgement only ever looks at places spelled beneath
// the output directory, and a draft carrying both faults is refused as the path
// that does not stay inside it.
func checkFiles(workspaceRoot, outputDir string, files []abi.ScaffoldFile) error {
	for _, f := range files {
		if !pathscope.IsLocalSlash(f.Path) {
			return &EscapingPathError{Path: f.Path, OutputDir: outputDir}
		}
	}
	root := writescope.RootFor(workspaceRoot, outputDir)
	for _, f := range files {
		if err := writescope.CheckWrite(root, filepath.Join(outputDir, filepath.FromSlash(f.Path))); err != nil {
			return err
		}
	}
	return nil
}

// writeFiles writes each drafted file under root at its own path, creating
// parent directories as needed, and returns the paths written in the order they
// were written.
//
// Every path has already passed checkFiles, over the whole set, before this is
// called — see [Import] for why the order matters and which predicate decides.
// Once a path has passed, joining it onto the root cannot leave the root, so the
// join below needs no second check.
func writeFiles(root string, files []abi.ScaffoldFile) ([]string, error) {
	written := make([]string, 0, len(files))
	for _, f := range files {
		dest := filepath.Join(root, filepath.FromSlash(f.Path))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return nil, fmt.Errorf("create directory for import file %q: %w", f.Path, err)
		}
		if err := os.WriteFile(dest, []byte(f.Content), 0o644); err != nil {
			return nil, fmt.Errorf("write import file %q: %w", f.Path, err)
		}
		written = append(written, f.Path)
	}
	return written, nil
}
