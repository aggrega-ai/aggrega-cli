// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
)

// importParams is what aggrega/import takes.
//
// source names the file to convert, and outputDir where the drafted files go.
//
// 🔴 The two are judged by different rules, and the split is read/write. source
// is READ, and it is a file the person driving the client selected — a
// description exported by another tool, which sits wherever they saved it — so an
// absolute path naming a file outside the workspace is accepted and read.
// outputDir is WRITTEN to and is resolved inside the workspace this session
// opened, so the drafts land in a tree the session was opened over and no request
// can name a destination of its own choosing.
//
// outputDir is required and is the caller's to name: this surface chooses no
// destination on the caller's behalf, so an absent value is refused rather than
// resolved to one. A relative value is resolved against the workspace root rather
// than against a directory this process happens to be sitting in, so there is no
// "here" an absent value could mean.
//
// It is required under both deliveries, and a dry run does not excuse it. Under
// a dry run nothing is written, but each drafted file still names the path it is
// FOR, and that path is relative to this directory — so a dry run without it
// would answer with drafts belonging to nowhere.
//
// format names the source format explicitly. An absent value means the format is
// recognised by the signatures the importers in the workspace's catalog declare,
// which is the same default the command line carries — so a caller that names
// nothing gets detection rather than a refusal, and a caller converting a source
// no signature recognises, or signatures of more than one format recognise, names
// the format instead.
//
// dryRun does everything the plain request does except write: it answers with the
// drafted files as content instead of putting them on disk. Which of the two a
// caller wants is a property of the caller rather than of this surface — a client
// that keeps its own document model puts the drafts where the author can read them
// before anything reaches disk — so it is a request field. It defaults to writing,
// because the operation's product is a set of files and a surface that produces
// files owes them. It changes nothing else: the same importer runs over the same
// source and reports the same diagnostics, so a source the write would refuse is
// refused here too.
type importParams struct {
	Source    string `json:"source"`
	OutputDir string `json:"outputDir"`
	Format    string `json:"format"`
	DryRun    bool   `json:"dryRun"`
}

// importedFile is one drafted file on the wire: the path it is for, relative to
// the output directory, and its content.
//
// The content travels with its path rather than in a separate channel, because a
// caller that received content and a list of paths separately would have to
// correlate them, and an import producing several files gives it nothing to
// correlate on.
type importedFile struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// importDiagnostic is one importer diagnostic on the wire.
//
// 🔴 It is a projection rather than the socket's own diagnostic type, and
// deliberately so. That type spells one of its fields in snake_case, which is
// the plugin wire's convention and not this surface's — every field here is
// camelCase — so serializing it directly would answer with a key shaped unlike
// every other key a client reads.
//
// It is also not the daemon's published-diagnostic shape, which carries a
// document position and a source layer. An importer diagnostic is about a
// foreign source file rather than about a position in an authored document, so
// that shape would have to be filled with fabricated coordinates.
//
// code is present only where the importer assigned one, which is a fact about
// the diagnostic rather than a gap.
type importDiagnostic struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
}

// importResult is the aggrega/import response.
//
// files carries every drafted file the importer produced, with its content,
// whether or not it was written — so a reporting delivery answers with the same
// set a writing one puts on disk. written names the files that reached disk, and
// is empty under a reporting delivery: what was produced and what was written are
// different questions, and a caller that asked to be reported to is asking only
// the first.
//
// 🔴 halted is what a client branches on for the outcome, and it is NOT an error.
// An import that could not convert everything still drafts what it could, and
// its diagnostics are the operation's most useful output — so a partial import
// answers with the files it produced, the diagnostics explaining what it could
// not convert, and halted true. Reducing that to a failure would discard exactly
// what the caller needs in order to act.
//
// files, written and diagnostics are always arrays, empty rather than absent, so
// a client reads an import that produced nothing as an empty answer rather than
// as a missing field.
type importResult struct {
	Format      string             `json:"format"`
	OutputDir   string             `json:"outputDir"`
	Written     []string           `json:"written"`
	Files       []importedFile     `json:"files"`
	Diagnostics []importDiagnostic `json:"diagnostics"`
	Halted      bool               `json:"halted"`
}

// dispatchImport turns a foreign source description into draft Aggrega
// authoring files.
//
// 🔴 It WRITES, and the files it writes are files an author owns and goes on
// editing. It is admitted on the rule every writing method here runs under: a
// caller that sent this has asked for the drafts, exactly as a caller sending a
// build has asked for artifacts. What stays out of bounds is a write performed
// while validating or inspecting, where nobody asked, and this is not that.
//
// 🔴 The destination is the caller's to name and is nonetheless confined. The
// output directory resolves inside the workspace this session opened, and every
// path the importer itself returns is required to stay under that resolved output
// directory — a scaffold path comes from the plugin, so it is untrusted, and the
// operations layer refuses an escaping one before writing anything at all.
//
// The source is the other case and is deliberately not confined: it is a file the
// person driving the client selected, so an absolute path outside the workspace
// is accepted and read. Widening the read does not widen the write — the two
// resolve through different rules — so a request may convert a description saved
// anywhere while the drafts it produces still land inside the workspace.
//
// It reaches no registry and no network: the importer and the parser it draws on
// are resolved from the workspace's own catalog and their bytes come from the
// shared plugin cascade, which serves embedded, colocated, local and cached
// modules and fetches nothing. Nothing is prompted for.
func (s *Server) dispatchImport(ctx context.Context, req *jsonRPCRequest) error {
	var params importParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Source == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"import: source is required and must name the file to convert, either as an "+
				"absolute path or relative to the root of the workspace this session opened")
	}
	// outputDir is required under BOTH deliveries, and a non-writing one does not
	// excuse it. Under that delivery nothing is written, but each drafted file
	// still names the path it is FOR, and that path is relative to this directory
	// — so a dry run without it would answer with drafts belonging to nowhere.
	if params.OutputDir == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"import: outputDir is required and must name the directory the drafted files go in, "+
				"relative to the root of the workspace this session opened — the caller chooses "+
				"where the drafts land and this surface picks no destination (under dryRun nothing "+
				"is written, but the drafted paths are still relative to it)")
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"the daemon holds no resolved workspace to resolve an importer against")
	}
	if s.resolveImportPlugin == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"this daemon was started without a plugin resolver, so no importer module can be loaded")
	}

	sourcePath, err := s.selectedPath("source", params.Source)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "import: ", err)
	}

	outputDir, err := s.workspacePath("outputDir", params.OutputDir)
	if err != nil {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "import: ", err)
	}

	content, err := os.ReadFile(sourcePath)
	if err != nil {
		// The resolved path is named rather than the value as sent, because a
		// relative source resolves against the workspace root and a reader looking
		// for the file needs the location that was actually opened.
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "import: cannot read the source "+sourcePath+": ", err)
	}

	// The format the request named, or none, is handed on as it is: the import
	// settles it, recognising an unnamed one from the signatures of the importers
	// in the workspace's catalog.
	delivery := authoredcontent.DeliveryFor(params.DryRun)
	res, err := importops.Import(ctx, s.resolvedWorkspace, importops.Options{
		Source:        content,
		SourceName:    params.Source,
		Format:        params.Format,
		OutputDir:     outputDir,
		Delivery:      delivery,
		ResolvePlugin: s.resolveImportPlugin,
	})
	if ctx.Err() != nil {
		return nil
	}
	// A source whose format the import could not settle is refused naming the
	// field a caller here can send. The two refusals' own sentences name the
	// command line's flag, which is the right remedy at a terminal and
	// unreachable from a request, so each is recognised through its type rather
	// than by reading the sentence, and the formats it names are read off it.
	if undetected := (*importops.UndetectedFormatError)(nil); errors.As(err, &undetected) {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"import: the source format of "+undetected.Path()+" was not recognised from its shape by the "+
				"signatures the importers in the workspace's catalog declare; send format to name it explicitly ("+
				sessionFormatExamples(undetected.Formats())+")")
	}
	if ambiguous := (*importops.AmbiguousFormatError)(nil); errors.As(err, &ambiguous) {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "import: ",
			fmt.Errorf("%w (source %s, recognised as %s); send format naming the one to read it as",
				importops.ErrAmbiguousImportFormat(), ambiguous.Path(), strings.Join(ambiguous.Formats(), ", ")))
	}
	if err != nil {
		if escaping := (*importops.EscapingPathError)(nil); errors.As(err, &escaping) {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "import: ", escaping)
		}
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "import: ", err)
	}

	return s.writeResult(ctx, req.ID, importResult{
		Format:      res.Format,
		OutputDir:   res.OutputDir,
		Written:     importWrittenPaths(res, delivery),
		Files:       importedFiles(res.Files),
		Diagnostics: importDiagnostics(res.Diagnostics),
		Halted:      res.Halted(),
	})
}

// sessionFormatExamples names the formats a request may send in the field, or
// says the catalog advertises none — so a refusal never offers an example the
// workspace's catalog cannot serve.
func sessionFormatExamples(formats []string) string {
	if len(formats) == 0 {
		return "no importer in the workspace's catalog advertises a format to name"
	}
	return "for example " + strings.Join(formats, " or ")
}

// importWrittenPaths projects the written set, always as an array so a caller
// reads a reporting delivery as an empty list rather than a missing field.
func importWrittenPaths(res importops.Result, delivery authoredcontent.Delivery) []string {
	if delivery.Reports() || res.Written == nil {
		return []string{}
	}
	return res.Written
}

// importedFiles projects the drafted files onto the wire shape.
func importedFiles(files []abi.ScaffoldFile) []importedFile {
	out := make([]importedFile, 0, len(files))
	for _, f := range files {
		out = append(out, importedFile{Path: f.Path, Content: f.Content})
	}
	return out
}

// importDiagnostics projects the importer's diagnostics onto the wire shape.
func importDiagnostics(diags []abi.Diagnostic) []importDiagnostic {
	out := make([]importDiagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, importDiagnostic{Severity: d.Severity, Message: d.Message, Code: d.Code})
	}
	return out
}
