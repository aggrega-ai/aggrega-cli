// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

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

// ExistingAggregateError reports an import refused because one of its drafts
// would be written over an Aggregate already on disk.
//
// 🔴 An Aggregate is the document an author goes on editing once it is drafted,
// so an import never writes over one unasked: the same command doing different
// things depending on what the destination holds would be a guess. Every other
// existing file a draft lands on is written over as before, so the refusal is
// bounded to the one document an author is expected to own. Bringing the
// Aggregate up to date with the source is what a sync does instead.
//
// Its message names the command line's flag, which is the remedy at a terminal;
// a protocol surface recognises the refusal by its type and words it with the
// field its caller sends instead.
type ExistingAggregateError struct {
	// Path is the drafted path that would be written over the Aggregate, exactly
	// as the importer returned it, relative to OutputDir.
	Path string
	// OutputDir is the directory the drafted paths are relative to.
	OutputDir string
}

// Error names the Aggregate and what the reader may do about it.
func (e *ExistingAggregateError) Error() string {
	return fmt.Sprintf("the import would write over the Aggregate %q beneath %q, which an author may have edited "+
		"since it was drafted, so no files were written; pass --sync to bring it up to date with the source, or "+
		"--output-dir to draft beneath another directory", e.Path, e.OutputDir)
}

// Options carries the resolved inputs for one import.
//
// SourcePath is where the source description is read from on this host, after the
// caller's own rule for which paths it may name: a file, whose bytes the importer
// is handed whole, or a directory, whose files the importer's declared file
// patterns select are handed to it as named sources. [Import] reads it, so no
// surface reads a source of its own and a file and a directory are told apart in
// one place. SourceName is what the source is called as the caller received it,
// used to name it in a refusal and, for a file, matched against the file patterns
// the importers' recognition signatures declare. Format names the source format
// explicitly; an empty Format means the format is recognised from the source by
// those signatures.
//
// OutputDir is the directory drafted files are written under by a writing
// delivery, and the directory every drafted path is relative to under either
// delivery. An empty OutputDir is the root of the workspace the operation is
// handed: an import that names no destination writes beneath the root of the
// workspace its run resolved, wherever the caller happens to be standing. That
// default is decided here and nowhere else, so a surface that names no
// destination hands none rather than stating the default of its own. A non-empty
// OutputDir is the caller's — a directory a person typed, or one a request named
// — and is made a directory path before any draft is placed beneath it: a
// relative one against the process's working directory, and on Windows a drive
// named bare as the current directory on that drive. A reporting delivery writes
// nothing, and the reported paths are still relative to it.
//
// Sync asks the import to bring what an earlier import drafted beneath the
// output directory up to date with the source, rather than writing the draft
// whole. Without it an import whose drafts would be written over an existing
// Aggregate is refused before anything is written or reported
// ([ExistingAggregateError]). With it, the import reads the record an import
// writes beside its drafts ([RecordFileName]) and judges every part three ways —
// see [Import].
//
// BeforeWrite, where set, is handed the result once every part is judged and
// before anything is written or removed, under both deliveries: a caller
// printing what the import found prints it before the write, so a write that
// fails part way leaves the reader knowing what it set out to do. It is not
// called for an import that is refused.
//
// Target names the provider capability — compose or helm, say, the word a
// Recipe's spec.capability binds to — the import drafts a Manifest for, beside
// the importer's drafts, mapping each drafted component type to the one Recipe of
// that capability the workspace's catalog offers for it, and pointing the drafted
// Release at it. An empty Target drafts no Manifest, and the import is then
// exactly the importer's drafts. A Target no Recipe of that catalog binds to is
// refused before the importer runs ([ErrImportTargetUnbound]). Every surface
// hands its target here as it received it, so the Manifest is composed in one
// place for all of them — see [Import]. Under Sync an empty Target is the target
// the record of the import being brought up to date names, where it names one.
//
// Upgrade applies to an import in a workspace that names no catalog folder. Which
// catalog snapshot such an import reads: with Upgrade, the newest; otherwise the
// one the record beside the drafts names, fetched by its digest when this machine
// does not hold it; otherwise the one the workspace last fetched as the newest;
// otherwise the newest. Under Upgrade the newest is fetched now, and the workspace
// reads it from then on. It is refused under the offline posture, since
// only the registry can say which snapshot is the newest. Where the workspace names
// a catalog folder no snapshot is read, and it changes nothing.
//
// There is no field for the importer's with-config. What an importer is handed
// is read by [Import] from the workspace's own plugins.importer attachments,
// once the importer is resolved, because which attachment's inputs apply
// depends on which importer runs — and a surface computing them ahead of the
// resolution would be answering that before it is known.
type Options struct {
	SourcePath    string
	SourceName    string
	Format        string
	Target        string
	OutputDir     string
	Sync          bool
	Upgrade       bool
	Delivery      authoredcontent.Delivery
	ResolvePlugin PluginBytesResolver
	BeforeWrite   func(Result)
}

// Result is what one import produced.
//
// OutputDir is the directory the drafted paths are relative to: Options.OutputDir
// made a directory path, or the workspace root where it named none. It is read
// from here rather than from what the caller passed, so a surface reporting where
// the drafts went reports the directory the import actually used.
//
// Files is every file the import writes, carrying content whether or not it was
// written — so a reporting delivery answers with the same set a writing one puts
// on disk. For an import that is not a sync, that is every drafted file the
// importer returned, in its own order, followed by the record of what the import
// wrote ([RecordFileName]) wherever the importer drafted anything. For a sync it
// is each drafted file whose content the sync changes, with the content the sync
// gives it, followed by the record where that changes too; a sync in step with
// its source writes nothing, and Files is empty.
//
// Written is the output-root-relative path of each file actually written, in the
// order written, and is empty under a reporting delivery. It is separate from
// Files rather than a flag on each entry because "what was produced" and "what
// reached disk" are different questions, and a caller under a reporting delivery
// is asking only the first.
//
// Drift is every part a sync brings up to date with the source, in the order the
// draft names them, and is empty for an import that is not a sync. It is the same
// under both deliveries: under a reporting delivery it is what a write would
// change.
//
// Removed is the output-root-relative path of each drafted file a sync deleted
// because the source no longer drafts it, in the order deleted, and is empty
// under a reporting delivery and for an import that is not a sync.
//
// Diagnostics is what the importer reported, in its own order. A halt-severity
// entry is carried here as data rather than returned as an error: an import that
// partially succeeds is exactly the case whose diagnostics are its most useful
// output, so reducing it to a failure would discard what the caller needs. Each
// surface decides its own outcome from [Result.Halted].
//
// Decisions is the importer's mapping decision for each source node it mapped,
// in the importer's own order and exactly as it returned them: the node, the
// catalog Components it weighed with their scores and tiers, what it chose, why,
// and how to swap the choice. It is what lets an author see where an import
// guessed, so every surface carries it in its answer, whatever the delivery and
// whether or not the import halted. It is empty for an importer that reports no
// decisions.
type Result struct {
	Format      string
	OutputDir   string
	Files       []abi.ScaffoldFile
	Written     []string
	Drift       []Drift
	Removed     []string
	Diagnostics []abi.Diagnostic
	Decisions   []abi.ImportDecision
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
// It reads the source at Options.SourcePath, loads the catalog view, selects the
// source format — the one named, or the one the signatures of that view's
// importers recognise the source as — resolves the importer the catalog
// advertises for that format — the one a plugins.importer attachment names, where
// one names an advertiser — runs it with the with-config of the attachment naming
// it and of no other, and either writes the drafted files under the output
// directory — Options.OutputDir, or the root of the workspace rw where it names
// none — or returns them for the caller to place, according to Options.Delivery.
//
// A file source is handed to the importer whole, as the primary bytes, exactly as
// it is read. A directory source is handed as named sources with the primary
// bytes empty: exactly the files the resolved importer's declared file patterns
// select — no other file of the directory is opened — each named by its
// slash-separated path inside the directory, in name order, together with the
// files the importer's attachment names in its with.sources under their base
// names, the whole list sorted by name. Before any file of the directory is read,
// every link the read would take something from — a link to a file a relevant
// pattern selects, and a link to a directory, which is never entered — is held to
// the directory, a root of its own wherever it lies, and one leading out of it is
// refused. An importer declaring no file pattern refuses a directory, and so does
// a directory holding no file its patterns select; a directory file and a
// with.sources file of one name refuse rather than one shadowing the other. The
// host caps nothing: a directory too large for the importer is refused by the
// importer's own runtime limits.
//
// The importer is resolved once, here, before it is run: the with-config it is
// handed depends on which importer the resolution chose, and the resolution
// reports that together with the importer, so the attachment that selected it and
// the attachment it is configured by are one answer rather than two.
//
// A core importer of the compiler's release that the machine does not hold — in
// the binary or in the workspace's content cache — is acquired by the import that
// first needs it, under ctx: fetched from the workspace's default registry, held
// to the digest the binary carries for it, verified as every fetched plugin is,
// and written to the content cache — and to nothing the workspace records, its
// lockfile and its record of trusted plugins included. Every later import reads
// it from there and reaches no registry. Where it can be neither read nor
// fetched, the import is refused, naming the importer, its version and what puts
// its module on the machine.
//
// The catalog view is the workspace's catalog folders. A workspace that names none
// reads the catalog its default registry publishes as one signed snapshot, laid out
// as a folder in the content cache and read through the same folder path. Which
// snapshot: with Options.Upgrade, the newest; otherwise the one the record beside
// the drafts names, fetched by its digest when this machine does not hold it;
// otherwise the one the workspace last fetched as the newest; otherwise the
// newest. A snapshot the content cache holds is read from there, held to its
// digest, and no registry is asked for it; one it does not hold is fetched, held
// to the manifest it came in and to its signature under the registry's trust
// policy, and kept. The record the import writes names the snapshot it read, by
// its manifest digest. Under the offline posture an import needing a
// fetch is refused, naming what to run. A registry that publishes no snapshot
// leaves the import with no catalog, and a warning among the diagnostics says so.
//
// 🔴 The write is CONTAINED, and the containment is the reused judgement rather
// than a check written here. A scaffold path comes from the importer plugin, so
// every path is placed by [pathscope.PlaceBeneath] — the one way a name somebody
// else chose becomes a path on disk, and the same judgement the rendered-artifact
// write is placed by. A path is a slash-separated logical name rather than a host
// path, which is the algebra that judgement owns: judging it as a host path would
// make a backslash a separator on one platform and an ordinary character on
// another, so whether the same importer output climbs out of the output directory
// would be answered one way on one machine and another way on the next. That
// answer is the same everywhere; what differs by machine is only that, where the
// process runs on Windows, a draft Windows cannot hold as written is refused too.
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
// plugin resolver, a source that could not be read ([*SourceReadError]), a
// catalog that would not load, a target no Recipe of that catalog binds to
// ([ErrImportTargetUnbound]), a source no importer recognises or more than one
// format's importers recognise, a link in a directory source leading out of it
// ([*DirectoryLinkError]), a directory refused for the importer's patterns or a
// name both a directory file and a with.sources file carry, a core importer of the
// compiler's release that is not on the machine and could not be fetched, or whose
// fetched module did not verify, a guest that failed,
// an escaping path, a draft Windows cannot hold where the import runs on Windows,
// a draft that would be written over an Aggregate already on disk
// ([*ExistingAggregateError]), a drafted path at the record's own place, a sync
// whose record is missing, does not list the Aggregate or cannot be read
// ([*ImportRecordError]), a sync that would change or remove what an author
// wrote ([*SyncConflictError]), or a write that failed.
//
// Given Options.Target, the import composes the draft set the importer returned
// into one that builds against that provider: beside the importer's drafts it
// drafts the Manifest the drafted Release needs and points the Release at it, in
// one composition every surface reaches only through here (see
// composeDraftSet). The drafted Manifest and the edited Release are drafts like
// any other: recorded, judged by the containment above and reported under a
// reporting delivery. A target no Recipe of the catalog binds to is refused once
// the catalog is loaded, before the source is recognised or the importer runs.
//
// Every import that drafts anything writes, beside its drafts, the record of
// what it wrote ([RecordFileName]): the target it was given, where it was given
// one, and each drafted file, and for a drafted Aggregate or Release each of its
// mapping entries at every depth, with a digest of the text written there; any
// other drafted file is recorded whole, under one digest. An entry's text is its
// key line, its value's lines for an entry whose value is not a mapping of
// entries, and the comment lines written directly above its key at the key's own
// column. A reporting delivery hands the record back among the files.
//
// Without Options.Sync, an Aggregate already at a drafted path refuses the import
// whatever the delivery, before anything is written or reported, so a dry run
// refuses it as the write does. Every other file already at a drafted path is
// written over, and a file an earlier import drafted that this one does not draft
// is left where it is.
//
// With Options.Sync, the record is read before the target is settled: a sync
// given no target drafts for the target the record names, and one given a target
// drafts for it and records it in place of the one recorded. The import then
// judges every part three ways — what the record says the import last wrote
// there, what is on disk, and what the source drafts now — and changes only what
// the import owns. A part the source left as the
// import last wrote it stays exactly as it is on disk, whether or not an author
// edited it. A part the source changed is written anew where it is on disk as
// the import last wrote it; a part the source dropped is removed where it is as
// the import last wrote it and holds nothing an author added — a drafted file
// deleted whole; a part the source gained is added beside the parts the draft
// names around it. Everything else on disk — a key an author added, a comment
// not written directly above an owned key, a file no record lists — is left byte
// for byte. A sync that would change or remove a part an author edited, add a
// part where an author already wrote one, or remove a part holding what an author
// added, is refused naming each, and nothing is written. An Aggregate on disk at
// a drafted path that no record lists refuses the sync. A sync whose importer
// reported a halt writes nothing. A sync into a directory holding no record and
// nothing at its drafted paths writes the whole draft, as a plain import does.
func Import(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, opts Options) (Result, error) {
	if _, err := workspaceOrRefuse(rw); err != nil {
		return Result{}, err
	}
	if opts.ResolvePlugin == nil {
		return Result{}, fmt.Errorf("internal: no plugin resolver supplied — the caller must supply one and pass it in")
	}

	// The source is found before the catalog is loaded, so a source that cannot be
	// read is refused as that, whatever the catalog holds. A directory is listed
	// here and none of its files is opened yet.
	src, err := readSource(opts.SourcePath, opts.SourceName)
	if err != nil {
		return Result{}, err
	}

	// The seam is built before the format is settled, because a format the caller
	// does not name is recognised from the signatures of the importers in the
	// catalog view the seam loads, through the parser pair it carries.
	seam, err := BuildSeam(rw, opts.ResolvePlugin)
	if err != nil {
		return Result{}, err
	}

	outputDir, err := outputDirFor(rw, opts.OutputDir)
	if err != nil {
		return Result{}, err
	}

	// A sync reads the record of the import it brings up to date before the target
	// is settled, because a sync given no target takes the one that import was
	// given: the drafts it judges were composed for that target, and composing them
	// for none would read as the source having dropped the drafted Manifest.
	target := opts.Target
	var rec *importRecord
	var recordOnDisk string
	if opts.Sync {
		if rec, recordOnDisk, err = readRecord(writescope.RootFor(rw.Root(), outputDir), outputDir); err != nil {
			return Result{}, err
		}
		if target == "" && rec != nil {
			target = rec.Target
		}
	}

	// A workspace naming no catalog folder reads the published catalog snapshot the
	// record beside the drafts names, where there is one: a sync's record, read
	// above, and otherwise the record of an earlier import into the same directory,
	// whatever this import does with it. A record a plain import cannot read pins
	// nothing — it is written over with the record this import writes — while a
	// sync's was refused above. A seam over catalog folders, and a harness standing
	// in for the seam, read no snapshot and no record for one.
	var snapshot catalogSnapshot
	if seam.readSnapshot != nil {
		read := catalogSnapshotRead{upgrade: opts.Upgrade}
		pinning := rec
		if !opts.Sync {
			pinning, _, _ = readRecord(writescope.RootFor(rw.Root(), outputDir), outputDir)
		}
		if pinning != nil {
			read.pinned = pinning.CatalogSnapshot
		}
		if seam, snapshot, err = seam.readSnapshot(ctx, read); err != nil {
			return Result{}, err
		}
	}

	// A target no Recipe of the catalog binds to is refused as soon as the catalog
	// is in hand, before the source is recognised or the importer runs, so nothing
	// is read through a parser, nothing is drafted and nothing is written. Where
	// the catalog is empty because the registry publishes no snapshot, the refusal
	// says that too.
	if err := checkTarget(target, target != opts.Target, seam.Recipes); err != nil {
		if snapshot.notice != "" {
			return Result{}, fmt.Errorf("%w; %s", err, snapshot.notice)
		}
		return Result{}, err
	}

	format, err := SelectFormat(ctx, opts.Format, src, seam)
	if err != nil {
		return Result{}, err
	}

	attachments := rw.Config().Plugins.Importer
	importer, at, resolveErr := seam.ImporterRes.ImporterPluginForFormat(format, attachmentURIs(attachments))
	var primary []byte
	var primaryName string
	var sources []abi.ImportSource
	var settings json.RawMessage
	if resolveErr == nil {
		// What the source hands the importer is decided by the importer resolved, so
		// a directory's files are selected only now; a resolution that was refused
		// reads nothing and is reported by the socket below.
		var read []abi.ImportSource
		if primary, read, err = src.inputsFor(importer, format); err != nil {
			return Result{}, err
		}
		primaryName = src.primaryName()
		var attached []attachedSource
		if attached, settings, err = attachmentInputs(attachments, at); err != nil {
			return Result{}, err
		}
		if sources, err = mergeSources(read, attached); err != nil {
			return Result{}, err
		}
	}

	imported, err := importersocket.ImportArtifact(ctx, resolvedImporter{plugin: importer, err: resolveErr},
		seam.importerLoader(ctx), seam.ParserRes, seam.parserLoader(ctx), format, primary, primaryName, sources, settings,
		seam.Catalog)
	if err != nil {
		return Result{}, err
	}

	// The draft set is composed from what the importer drafted, here and nowhere
	// else, before the record of it is written and before any path is judged — so
	// a drafted Manifest and an edited Release are recorded, contained and
	// reported exactly as the importer's own drafts are.
	draft, composed, err := composeDraftSet(target, seam.Recipes, imported.Files)
	if err != nil {
		return Result{}, err
	}
	diagnostics := imported.Diagnostics
	if snapshot.notice != "" {
		diagnostics = append([]abi.Diagnostic{warnDiagnostic(snapshot.notice)}, diagnostics...)
	}
	if len(composed) > 0 {
		diagnostics = append(append(make([]abi.Diagnostic, 0, len(diagnostics)+len(composed)), diagnostics...),
			composed...)
	}

	res := Result{
		Format:      format,
		OutputDir:   outputDir,
		Files:       draft,
		Diagnostics: diagnostics,
		Decisions:   imported.Decisions,
	}
	// The record of what the import writes is composed from the draft, and placed
	// after it, so it is judged with the drafted paths below.
	drafted := draftedFiles(draft)
	files := draft
	if len(drafted) > 0 {
		files = append(append(make([]abi.ScaffoldFile, 0, len(files)+1), files...),
			recordScaffold(target, snapshot.digest, drafted))
	}
	// Every drafted path is judged before anything is written and before anything
	// is reported, so a reporting delivery refuses exactly what the write refuses.
	if err := checkFiles(rw.Root(), outputDir, files); err != nil {
		return Result{}, err
	}
	if err := checkRecordPlace(outputDir, draft); err != nil {
		return Result{}, err
	}
	if opts.Sync {
		return syncImport(rw.Root(), outputDir, target, snapshot.digest, opts, res, drafted, rec, recordOnDisk)
	}
	// An Aggregate already at a drafted path is judged after the paths are, so it
	// is read only at a place already held inside the root, and before either
	// delivery answers, so a dry run refuses what the write refuses.
	if err := checkExistingAggregate(outputDir, draft); err != nil {
		return Result{}, err
	}
	res.Files = files
	if opts.BeforeWrite != nil {
		opts.BeforeWrite(res)
	}
	if opts.Delivery.Reports() {
		return res, nil
	}

	written, err := writeFiles(outputDir, files)
	if err != nil {
		return Result{}, err
	}
	res.Written = written
	return res, nil
}

// checkRecordPlace refuses a draft the importer placed where the import keeps
// its record: the record is written after the drafts, so such a draft would be
// written and then written over, and a later sync would read the importer's
// bytes as the record.
func checkRecordPlace(outputDir string, files []abi.ScaffoldFile) error {
	record, err := draftPlace(outputDir, RecordFileName)
	if err != nil {
		return err
	}
	for _, f := range files {
		place, err := draftPlace(outputDir, f.Path)
		if err != nil {
			return err
		}
		if place == record {
			return fmt.Errorf("the importer returned the file path %q, which is where an import keeps the record of "+
				"what it wrote (%s); no files were written", f.Path, RecordFileName)
		}
	}
	return nil
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
// relative to: the one the caller named, made a directory path against the
// process's working directory ([pathscope.DirectoryPath]), or, where it named none,
// the root of the workspace rw. It is the one statement of that default, so the
// command line, the assistant's import tool and any other caller naming no
// destination all land their drafts at the same root rather than wherever their
// process happens to be sitting.
//
// A named directory is made a directory path here, before any draft is placed
// beneath it, so the directory the result reports and the directory the drafts are
// written beneath are one: on Windows a drive named bare, such as "D:", is the
// current directory on that drive, and a draft named `\notes.txt` lands beneath it
// rather than at the top of the drive. A relative name resolves against the working
// directory as it always has, so on every other platform the drafts land where they
// did.
func outputDirFor(rw *workspaceconfig.ResolvedWorkspace, named string) (string, error) {
	if named != "" {
		return pathscope.DirectoryPath(named)
	}
	return rw.Root(), nil
}

// draftPlace returns the host path the drafted file at path is written at beneath
// outputDir, by the one way a name somebody else chose is placed on disk
// ([pathscope.PlaceBeneath]): a path that does not stay inside outputDir is refused
// as an [EscapingPathError], and, where the process runs on Windows, one holding a
// segment Windows cannot hold as written is refused with the halt
// pathscope.ErrNameWindowsCannotHold names.
func draftPlace(outputDir, path string) (string, error) {
	place, inside, err := pathscope.PlaceBeneath(outputDir, path)
	if err != nil {
		return "", err
	}
	if !inside {
		return "", &EscapingPathError{Path: path, OutputDir: outputDir}
	}
	return place, nil
}

// checkFiles judges every drafted file before any is written and before a
// reporting delivery answers. It is where an import's containment verdict is
// taken, for both deliveries, so a dry run and a write refuse the same drafts.
//
// Two judgements, the first over the whole set before the second begins. Each
// path must be one [draftPlace] places beneath outputDir: one climbing out of it as
// a slash-separated logical name, through slashes or backslashes, an absolute one
// and an empty one are refused as an [EscapingPathError] naming the path and
// outputDir, whatever is on disk, and on Windows one Windows cannot hold as written
// is refused naming the path and its segment. Only then is each place a file would
// be written at held, with its links resolved:
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
	places := make([]string, 0, len(files))
	for _, f := range files {
		place, err := draftPlace(outputDir, f.Path)
		if err != nil {
			return err
		}
		places = append(places, place)
	}
	root := writescope.RootFor(workspaceRoot, outputDir)
	for _, place := range places {
		if err := writescope.CheckWrite(root, place); err != nil {
			return err
		}
	}
	return nil
}

// checkExistingAggregate refuses an import one of whose drafts would be written
// over an Aggregate already on disk, as an [ExistingAggregateError] naming the
// first such draft in the importer's order.
//
// What is judged is the file already at the place: a regular file whose
// top-level kind is Aggregate. The draft's own content is not read, because what
// an import must not do is write over the document an author owns, whatever the
// importer drafted in its place. A file that does not read as YAML, or reads as
// another kind, is not an Aggregate, and a draft lands on it as before.
//
// Every path has already passed checkFiles, and each is placed by [draftPlace]
// again, so the place read is the place that was judged.
func checkExistingAggregate(outputDir string, files []abi.ScaffoldFile) error {
	for _, f := range files {
		place, err := draftPlace(outputDir, f.Path)
		if err != nil {
			return err
		}
		if isAggregateFile(place) {
			return &ExistingAggregateError{Path: f.Path, OutputDir: outputDir}
		}
	}
	return nil
}

// isAggregateFile reports whether a regular file at place reads as a document
// whose top-level kind is Aggregate. Anything it cannot read as one — no file, a
// directory, bytes that are not YAML — is not one.
func isAggregateFile(place string) bool {
	info, err := os.Stat(place)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	data, err := os.ReadFile(place)
	if err != nil {
		return false
	}
	return isAggregateBytes(data)
}

// writeFiles writes each drafted file under root at its own path, creating
// parent directories as needed, and returns the paths written in the order they
// were written.
//
// Every path has already passed checkFiles, over the whole set, before this is
// called — see [Import] for why the order matters and which predicate decides.
// Each is placed by [draftPlace] again, the one way a draft reaches a host path,
// so the place written is the place checkFiles judged.
func writeFiles(root string, files []abi.ScaffoldFile) ([]string, error) {
	written := make([]string, 0, len(files))
	for _, f := range files {
		dest, err := draftPlace(root, f.Path)
		if err != nil {
			return nil, err
		}
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
