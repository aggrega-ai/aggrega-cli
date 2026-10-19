// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
)

// importParams is what aggrega/import takes.
//
// source names the file or the directory to convert, and outputDir where the
// drafted files go.
//
// 🔴 The two are judged by different rules, and the split is read/write. source
// is READ, and it is a file or a directory the person driving the client selected
// — a description exported by another tool, which sits wherever they saved it — so
// an absolute path naming one outside the workspace is accepted and read. A
// directory is read into the named sources its importer's file patterns select,
// held to itself wherever it lies, so no link inside it is followed out of it.
// outputDir is WRITTEN to and is resolved inside the workspace this session
// opened, so the drafts land in a tree the session was opened over and no request
// can name a destination outside it.
//
// outputDir is optional, as the command line's --output-dir is. An absent value
// hands the import no destination, and the import drafts beneath the root of the
// workspace this session opened — the default the operation states once for
// every surface, so this one states none of its own. A relative value is resolved
// against that root rather than against a directory this process happens to be
// sitting in. Under a dry run nothing is written, and each drafted path is still
// relative to the directory the answer names as outputDir, the one named or that
// root.
//
// format names the source format explicitly. An absent value means the format is
// recognised by the signatures the importers declare, the core importers' included,
// which is the same default the command line carries — so a caller that names
// nothing gets detection rather than a refusal, and a caller converting a source
// no signature recognises, or signatures of more than one format recognise, names
// the format instead.
//
// target is the command line's --target, carried as a field: the provider
// capability, such as compose or helm, the import drafts a Manifest for beside
// the importer's drafts and points the drafted Release at. It is handed to the
// import as sent, so the Manifest is the one every surface drafts for the same
// source; absent drafts none, and a capability no Recipe in the workspace's
// catalog binds to is refused before the importer runs, in the condition's own
// words, which name no flag.
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
//
// A request whose drafts would be written over an Aggregate already in outputDir
// is refused under both deliveries, naming the Aggregate and sync, and nothing is
// written.
//
// sync asks for what an earlier import drafted beneath outputDir to be brought up
// to date with the source, changing only what the import wrote as the record
// beside the drafts lists it, rather than the draft being written whole. It is
// the command line's --sync, carried as a field for the reason dryRun is one, and
// it composes with dryRun: the two together answer with what the sync would
// write and every part that would drift, and write nothing. A sync that would
// change or remove what an author wrote, or that no record answers for, is
// refused in this surface's words, naming this field.
type importParams struct {
	Source    string `json:"source"`
	OutputDir string `json:"outputDir"`
	Format    string `json:"format"`
	Target    string `json:"target"`
	DryRun    bool   `json:"dryRun"`
	Sync      bool   `json:"sync"`
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

// importDrift is one part a sync brought up to date with the source, or under
// dryRun would: how it drifted, the drafted file it is in, relative to the output
// directory, and the keys leading to it in a drafted Aggregate, empty where the
// part is the whole file.
//
// The keys are a list rather than one joined string, so a key holding a full
// stop is read back as the one key it is.
type importDrift struct {
	Change string   `json:"change"`
	File   string   `json:"file"`
	Key    []string `json:"key"`
}

// importResult is the aggrega/import response.
//
// files carries every file the import writes, with its content, whether or not
// it was written — so a reporting delivery answers with the same set a writing
// one puts on disk. For a request that is not a sync that is every drafted file
// the importer produced, followed by the record of what the import wrote, which
// every import that drafts anything writes beside its drafts; for a sync it is
// each drafted file the sync changes, with its new content, followed by the
// record where that changes too, and a sync in step answers an empty list.
// written names the files that reached disk, and is empty under a reporting
// delivery: what was produced and what was written are different questions, and
// a caller that asked to be reported to is asking only the first.
//
// drift is every part a sync brought up to date with the source, or under dryRun
// would, in the order the draft names them — a drafted file the sync deletes is
// one of them, removed with no keys. It is what the command line answers through
// its exit status under --sync --dry-run, carried here as data because this
// surface has none, and it is an empty list for a request that is not a sync, for
// a sync in step and for a sync whose importer could not finish, which writes
// nothing.
//
// 🔴 halted is what a client branches on for the outcome, and it is NOT an error.
// An import that could not convert everything still drafts what it could, and
// its diagnostics are the operation's most useful output — so a partial import
// answers with the files it produced, the diagnostics explaining what it could
// not convert, and halted true. Reducing that to a failure would discard exactly
// what the caller needs in order to act.
//
// decisions is the importer's mapping decision for each source node it mapped, in
// its order, under both deliveries and whether or not the import halted: what
// tells a client where the import guessed. Each entry is the importer's own
// decision type rather than a projection of it, unlike a diagnostic: that type's
// keys are already camelCase, so carrying it whole answers in this surface's
// register with every key the importer wrote, `componentName` included, and
// nothing renamed between the importer and the client. A candidate list, a
// choice, a rationale or a swap hint the importer did not write is absent from
// its entry, as it is on the importer's own answer.
//
// files, written, drift, diagnostics and decisions are always arrays, empty
// rather than absent, so a client reads an import that produced nothing as an
// empty answer rather than as a missing field.
type importResult struct {
	Format      string               `json:"format"`
	OutputDir   string               `json:"outputDir"`
	Written     []string             `json:"written"`
	Files       []importedFile       `json:"files"`
	Drift       []importDrift        `json:"drift"`
	Diagnostics []importDiagnostic   `json:"diagnostics"`
	Decisions   []abi.ImportDecision `json:"decisions"`
	Halted      bool                 `json:"halted"`
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
// 🔴 The destination is the caller's to name, or to leave to the operation's
// default, and is confined either way. A named output directory resolves inside
// the workspace this session opened, an absent one is that workspace's root, and
// every path the importer itself returns is required to stay under the output
// directory — a scaffold path comes from the plugin, so it is untrusted, and the
// operations layer refuses an escaping one before writing anything at all.
//
// The source is the other case and is deliberately not confined: it is a file or
// a directory the person driving the client selected, so an absolute path outside
// the workspace is accepted and read, and the import reads it — a directory held
// to itself. Widening the read does not widen the write — the two resolve through
// different rules — so a request may convert a description saved anywhere while
// the drafts it produces still land inside the workspace.
//
// The one registry it may reach is the session workspace's default registry, and
// only for a core importer of the compiler's release that the machine does not
// hold yet: the import fetches it there, verifies it as every fetched plugin is
// verified, and keeps it in the workspace's content cache, so later imports reach
// no network. Every other importer, and the parser it draws on, is resolved from
// the workspace's own catalog with its bytes from the shared plugin cascade —
// except a parser for a syntax the catalog serves none for, which is the one the
// binary carries. The acquisition and the parser resolution are the import
// operation's own, so this method acquires and parses exactly as the command and
// the assistant's tool do. Nothing is prompted for.
func (s *Server) dispatchImport(ctx context.Context, req *jsonRPCRequest) error {
	var params importParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "invalid params: ", err)
		}
	}
	if params.Source == "" {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"import: source is required and must name the file or the directory to convert, either as an "+
				"absolute path or relative to the root of the workspace this session opened")
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

	// An absent outputDir is handed on as none, and the import drafts beneath the
	// workspace root, which is its default rather than this surface's. The
	// resolver is asked only about a value that was sent: it refuses an empty one,
	// and an empty one here is a request leaving the destination to that default.
	outputDir := ""
	if params.OutputDir != "" {
		if outputDir, err = s.workspacePath("outputDir", params.OutputDir); err != nil {
			return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "import: ", err)
		}
	}

	// The format the request named, or none, is handed on as it is: the import
	// settles it, recognising an unnamed one from the signatures of the importers
	// it resolves among. The source is handed on as the path resolved, a
	// file or a directory, and the import reads it.
	delivery := authoredcontent.DeliveryFor(params.DryRun)
	res, err := importops.Import(ctx, s.resolvedWorkspace, importops.Options{
		SourcePath:    sourcePath,
		SourceName:    params.Source,
		Format:        params.Format,
		Target:        params.Target,
		OutputDir:     outputDir,
		Sync:          params.Sync,
		Delivery:      delivery,
		ResolvePlugin: s.resolveImportPlugin,
	})
	if ctx.Err() != nil {
		return nil
	}
	if readErr := (*importops.SourceReadError)(nil); errors.As(err, &readErr) {
		// The location read is named rather than the value as sent, because a
		// relative source resolves against the workspace root and a reader looking
		// for the file needs the location that was actually opened.
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "import: cannot read the source "+readErr.Path+": ",
			readErr.Err)
	}
	// A source whose format the import could not settle is refused naming the
	// field a caller here can send. The two refusals' own sentences name the
	// command line's flag, which is the right remedy at a terminal and
	// unreachable from a request, so each is recognised through its type rather
	// than by reading the sentence, and the formats it names are read off it.
	if undetected := (*importops.UndetectedFormatError)(nil); errors.As(err, &undetected) {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"import: the source format of "+undetected.Path()+" was not recognised from its shape by the "+
				"signatures the importers declare; send format to name it explicitly ("+
				sessionFormatExamples(undetected.Formats())+")")
	}
	if ambiguous := (*importops.AmbiguousFormatError)(nil); errors.As(err, &ambiguous) {
		return s.writeRefusal(ctx, req.ID, jsonRPCInvalidParams, "import: ",
			fmt.Errorf("%w (source %s, recognised as %s); send format naming the one to read it as",
				importops.ErrAmbiguousImportFormat(), ambiguous.Path(), strings.Join(ambiguous.Formats(), ", ")))
	}
	// An Aggregate already at a drafted path, a sync that would rewrite what an
	// author wrote, and a sync no record answers for are refused in this surface's
	// words, for the reason the format refusals above are: each refusal's own
	// sentence names the command line's flags.
	if existing := (*importops.ExistingAggregateError)(nil); errors.As(err, &existing) {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, sessionExistingAggregate(existing))
	}
	if conflicts := (*importops.SyncConflictError)(nil); errors.As(err, &conflicts) {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, sessionSyncConflict(conflicts))
	}
	if record := (*importops.ImportRecordError)(nil); errors.As(err, &record) {
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams, sessionImportRecord(record))
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
		Drift:       importDrifts(res.Drift),
		Diagnostics: importDiagnostics(res.Diagnostics),
		Decisions:   importDecisions(res.Decisions),
		Halted:      res.Halted(),
	})
}

// sessionExistingAggregate words the refusal of a request whose drafts would be
// written over an Aggregate already on disk, naming the Aggregate where it sits
// and the fields a caller here can send instead.
func sessionExistingAggregate(e *importops.ExistingAggregateError) string {
	place := filepath.Join(e.OutputDir, filepath.FromSlash(e.Path))
	return "import: the draft " + e.Path + " would be written over the Aggregate at " + place + ", which an author " +
		"may have edited since it was drafted, so nothing was written; send sync to bring it up to date with the " +
		"source, or name another outputDir to draft beneath"
}

// sessionSyncConflict words the refusal of a sync that would change or remove
// what an author wrote, naming the field the caller sent rather than the command
// line's flag, and each conflicting part in the one sentence every surface prints
// for it ([importops.ConflictLine]), in the order the draft names them.
func sessionSyncConflict(e *importops.SyncConflictError) string {
	var b strings.Builder
	b.WriteString("import: sync was sent, and bringing the drafts beneath " + e.OutputDir + " up to date with the " +
		"source would change or remove what an author wrote, so nothing was written; reconcile each part below by " +
		"hand, or put back what the import wrote there, and send the request again:")
	for _, c := range e.Conflicts {
		b.WriteString("\n  ")
		b.WriteString(importops.ConflictLine(c))
	}
	return b.String()
}

// sessionImportRecord words the refusal of a sync the record beside the drafts
// cannot answer for — no record lists the Aggregate it would bring up to date,
// or the record cannot be read — naming the field the caller sent rather than
// the command line's flag, and the record and the Aggregate where they sit.
func sessionImportRecord(e *importops.ImportRecordError) string {
	record := filepath.Join(e.OutputDir, filepath.FromSlash(e.Record))
	if e.Err != nil {
		return "import: sync was sent, and the record of what an import wrote, " + record + ", cannot be read: " +
			e.Err.Error() + "; nothing was written"
	}
	place := filepath.Join(e.OutputDir, filepath.FromSlash(e.Aggregate))
	return "import: sync was sent, and no record of what an import wrote lists the Aggregate at " + place + " (" +
		record + ", which every import writes beside its drafts), so its parts cannot be told from parts an author " +
		"wrote; nothing was written"
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

// importDrifts projects the parts a sync brought up to date onto the wire shape,
// in the operation's order, always as an array — and each part's keys as an
// array too, empty for a whole file — so a request that is not a sync, or a sync
// in step, answers an empty list rather than a missing field.
func importDrifts(drift []importops.Drift) []importDrift {
	out := make([]importDrift, 0, len(drift))
	for _, d := range drift {
		out = append(out, importDrift{Change: d.Change, File: d.File, Key: append([]string{}, d.Key...)})
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

// importDecisions carries the importer's decisions onto the wire in its order, as
// an array even when it reported none.
func importDecisions(decisions []abi.ImportDecision) []abi.ImportDecision {
	return append(make([]abi.ImportDecision, 0, len(decisions)), decisions...)
}
