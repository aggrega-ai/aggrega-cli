// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/artifactout"
	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/importersocket"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
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

// ExistingEntityError reports an import refused because one of its drafts
// would be written over an entity already on disk: a file at the drafted path
// whose top-level kind is Aggregate, Component, Feature, Recipe or FeatureRecipe.
//
// 🔴 An Aggregate is the document an author goes on editing once it is drafted,
// and a catalog entity is the document an author edits to fit their platform,
// so an import never writes over one unasked: the same command doing different
// things depending on what the destination holds would be a guess. Every other
// existing file a draft lands on is written over as before, so the refusal is
// bounded to the kinds an author is expected to own. Bringing them up to date
// with the source is what a sync does instead.
//
// Where another format's import into the same directory drafted the entity —
// its entry in the record beside the drafts lists the path — the refusal names
// that import, and offers drafting beneath another directory alone: a sync of
// this import's format would not bring that import's entity up to date.
//
// Its message names the command line's flag, which is the remedy at a terminal;
// a protocol surface recognises the refusal by its type and words it with the
// field its caller sends instead.
type ExistingEntityError struct {
	// Path is the drafted path that would be written over the entity, exactly
	// as the importer returned it, relative to OutputDir.
	Path string
	// OutputDir is the directory the drafted paths are relative to.
	OutputDir string
	// Kind is the kind the file already at Path declares.
	Kind entitykind.Kind
	// Format is the format of another import into OutputDir whose entry in the
	// record beside the drafts lists Path, and is empty where none does.
	Format string
}

// Error names the entity and what the reader may do about it.
func (e *ExistingEntityError) Error() string {
	if e.Format != "" {
		return fmt.Sprintf("the import would write over the %s %q beneath %q, which the %s import into that "+
			"directory drafted and the record of what imports wrote there (%s) lists for it, so no files were written; "+
			"pass --output-dir to draft beneath another directory", e.Kind, e.Path, e.OutputDir, e.Format,
			RecordFileName)
	}
	return fmt.Sprintf("the import would write over the %s %q beneath %q, which an author may have edited "+
		"since it was drafted, so no files were written; pass --sync to bring it up to date with the source, or "+
		"--output-dir to draft beneath another directory", e.Kind, e.Path, e.OutputDir)
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
// Aggregate, Component, Feature, Recipe or FeatureRecipe is refused before
// anything is written or reported ([ExistingEntityError]). With it, the import reads its entry in the record
// imports write beside their drafts ([RecordFileName]) and judges every part
// three ways — see [Import].
//
// BeforeWrite, where set, is handed the result once every part is judged and
// before anything is written or removed, under both deliveries: a caller
// printing what the import found prints it before the write, so a write that
// fails part way leaves the reader knowing what it set out to do. It is not
// called for an import that is refused.
//
// Target names the provider capability — compose or helm, say, the word a
// Recipe's spec.capability binds to — the import drafts a Manifest for, beside
// the importer's drafts, mapping each component type and feature type the drafts
// need to the Recipe or FeatureRecipe of that capability the workspace's catalog
// offers for it — the one there is, or among several the one named after the
// type — completing a Manifest the importer drafted itself, and pointing the
// drafted Release at it. An empty Target drafts no Manifest, and the import is then
// exactly the importer's drafts. A Target no Recipe of that catalog binds to is
// refused before the importer runs ([ErrImportTargetUnbound]). Every surface
// hands its target here as it received it, so the Manifest is composed in one
// place for all of them — see [Import]. Under Sync an empty Target is the target
// the entry of the import being brought up to date names, where it names one.
//
// Upgrade says which published catalog snapshot the import reads, behind the
// workspace's catalog folders where it names some and as its whole catalog where it
// names none. Which snapshot an import reads: with Upgrade, the newest; otherwise
// the one its entry in the record beside the drafts names, fetched by its digest
// when this machine does not hold it; otherwise the one the workspace last
// fetched as the newest; otherwise the newest. Under Upgrade the newest is fetched now, and the
// workspace reads it from then on. It is refused under the offline posture, with
// no default registry named and with the registry out of reach, since only the
// registry can say which snapshot is the newest. A workspace naming catalog
// folders otherwise imports over them alone where the snapshot cannot be read so,
// with a warning — except under Sync where its entry names a snapshot: that sync
// is refused, naming the snapshot, since the drafts it judges were drafted against
// that catalog.
//
// Answers is the answers file on this host ([AnswersFileName]) the values the
// drafts leave empty that no source can hold are read from and written to. Empty
// is the one the import's entry in the record beside the drafts names, where an
// earlier import into the same directory wrote one, and otherwise
// [DefaultAnswersPath] of the source. Every surface hands the path it was given
// here, resolved by its own rule for which paths it may name, so the file is read
// and written in one place for all of them.
//
// Ask, where set, puts each value the answers file leaves unanswered to a person,
// one at a time, and the answers are written into the drafts and into the answers
// file. Only a surface with a person at a terminal sets it; with it unset nothing is
// asked, and every value still owed is reported among the diagnostics and as
// [Result.OwedRefusal]. A reporting delivery asks nothing either, since it would
// keep no answer, and neither does a sync whose importer halted, since it writes
// nothing. An import refused for anything that does not turn on the answers — a
// drafted path, an Aggregate already on disk, the answers file itself — is refused
// before the first question; and each answer given reaches the answers file as
// soon as it is accepted, so a refusal judged on the answered drafts, or a write
// that fails, still leaves it there for the next run.
//
// Pull, where set, is how the surface pulls the dependencies of a Release, as
// `aggrega pull` does: an import that drafted a Release, writes, finished and owes
// nothing pulls that Release's dependencies through it once its files are written,
// so the drafts validate and build at once. Drafts still owing a value are not
// pulled: the pull would resolve a Release the reader has yet to finish, and the
// refusal naming what is owed is what they need. A workspace under the offline
// posture pulls nothing, and a diagnostic names the pull to run. It is a seam
// rather than a call because the pull is the dependency resolution every surface
// reaches through its own adapter, and the import reaches it the same way a
// build's automatic fill does. It returns the lines of its own account the
// surface did not show itself, each carried among the diagnostics as information,
// and its failure is the import's [Result.PullRefusal] rather than an error, so
// the result still says what was written.
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
	Answers       string
	Ask           Ask
	Pull          func(ctx context.Context, release string) ([]string, error)
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
//
// Owed is every value the drafts still leave empty that no source could supply
// and no answer did, in the order the drafts are written ([Owed]); each is also
// among the Diagnostics, as a warning, so a surface reporting diagnostics reports
// them. Answers is the answers file on this host they are read from and written
// in, empty where the drafts owe nothing, and AnswersWritten reports whether the
// import wrote it — an answers file already holding what it would be written with
// is not written again. [Result.PullRefusal] says why a pull the import made of
// the drafted Release's dependencies failed; a pull that succeeded is the
// lockfile beside the Release, and its own account is among the Diagnostics.
type Result struct {
	Format         string
	OutputDir      string
	Files          []abi.ScaffoldFile
	Written        []string
	Drift          []Drift
	Removed        []string
	Diagnostics    []abi.Diagnostic
	Decisions      []abi.ImportDecision
	Owed           []Owed
	Answers        string
	AnswersWritten bool

	// pulled is set where the import pulled the drafted Release's dependencies.
	pulled bool

	// answersHeld is set once the answers file holds every owed key, written by
	// this run or already as it would be written.
	answersHeld bool

	// reported is set under a reporting delivery, which writes nothing and so
	// refuses nothing for what the drafts owe.
	reported bool

	// pullFailed is the failure of the pull the import made, nil where it made
	// none or the pull succeeded.
	pullFailed *PullError
}

// PullRefusal returns the failure of the pull an import made of its drafted
// Release's dependencies once its files were written ([PullError]), and nil where
// it made none or the pull succeeded. The files are written either way, and the
// result names them, so a surface reports what reached disk beside the refusal.
func (r Result) PullRefusal() error {
	if r.pullFailed == nil {
		return nil
	}
	return r.pullFailed
}

// AnswersPath names the answers file as the paths written beside it are named:
// relative to the output directory, by the rule the record's pointer is written
// with, and empty where the drafts owe nothing.
func (r Result) AnswersPath() string {
	if r.Answers == "" {
		return ""
	}
	return filepath.FromSlash(answersPointer(r.OutputDir, r.Answers))
}

// OwedRefusal returns the refusal of a writing import whose drafts still owe
// values ([OwedValuesError]), naming how many and where they are written, and nil
// when they owe none. Every surface with nobody to ask reports its import's
// outcome through it, so the command line's exit status and a protocol answer
// cannot come to disagree about whether the drafts are complete.
//
// A reporting delivery is refused nothing for what its drafts owe: it writes
// nothing, the answers file included, and the owed values are among its
// diagnostics, so what it reports is what a write would leave owed.
func (r Result) OwedRefusal() error {
	if len(r.Owed) == 0 || r.reported {
		return nil
	}
	return &OwedValuesError{Owed: r.Owed, Answers: r.Answers, Written: r.answersHeld}
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
// it is read. A directory source is read without the workspace's build output
// folder — the folder a build of rw writes its rendered files to — wherever that
// folder lies beneath it: neither the recognition nor the importer reads a file
// inside it, so a directory that has been built is read as the project it holds
// (see [Source]). A directory source is handed as named sources with the primary
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
// The catalog view is the build's resolution order: the workspace's catalog
// folders first, then the catalog its default registry publishes as one signed
// snapshot, laid out as a folder in the content cache and read through the same
// folder path, last. The load is first-win by directory, so a folder's entity
// shadows the published one of its identity and every other published entity
// stays in view — for the projection the importer is handed, the importers, and
// the Recipes a target is checked against and its Manifest drafted from alike. A
// parser or an importer is resolved layer by layer: one the folders hold for a
// syntax or a format is chosen before any the published catalog holds, save a
// published importer a plugins.importer attachment names (see buildSeam).
// Which snapshot: with Options.Upgrade, the newest; otherwise the one the
// import's entry in the record beside the drafts names, fetched by its digest
// when this machine does not hold it; otherwise the one the workspace last
// fetched as the newest; otherwise the newest. A snapshot the content cache
// holds is read from there, held to its digest, and no registry is asked for it;
// one it does not hold is fetched, held to the manifest it came in and to its
// signature under the registry's trust policy, and kept. The entry the import writes names the snapshot it read, by
// its manifest digest; an import that read the folders alone because the
// snapshot its entry pins could not be read writes that pin back unchanged. In a
// workspace that names no catalog folder, an import
// needing a fetch under the offline posture is refused, naming what to run, and a
// registry that publishes no snapshot leaves the import with no catalog, which a
// warning among the diagnostics says. In a workspace that names catalog folders,
// a snapshot that cannot be read — offline with none on this machine, no default
// registry named, a registry out of reach or publishing none — leaves the import
// over the folders alone, and a warning among the diagnostics says why and what
// reads the snapshot; one that is read and does not verify refuses the import
// whatever the workspace names.
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
// 🔴 Every path is judged BEFORE any file is written, before a reporting delivery
// answers and before anyone is asked for an owed value, in one place (checkFiles),
// so a dry run refuses exactly the drafts a write refuses, and an importer
// returning one contained path and one escaping path writes neither. Validating as
// it went would leave the contained prefix on disk from a run that was refused,
// which is a partial result nobody asked for.
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
// a draft that would be written over an entity already on disk
// ([*ExistingEntityError]), a drafted path at the record's own place, a sync
// whose record is missing, whose entry does not list the entity, another
// format's entry lists it, whose entry naming no format lists none of its drafts,
// or whose record cannot be read ([*ImportRecordError]), a source not recognised
// before the entry of a record of several formats is chosen
// ([*UndetectedFormatError]), a sync that would change or remove what an author
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
// Every import that drafts anything writes, beside its drafts, its entry in the
// record of what imports wrote there ([RecordFileName]), which holds one entry
// per import format, sorted by format: the format, the target it was given, where
// it was given one, and each drafted file, and for a drafted Aggregate or Release
// each of its mapping entries at every depth, with a digest of the text written
// there; any other drafted file is recorded whole, under one digest. An entry's
// text is its key line, its value's lines for an entry whose value is not a
// mapping of entries, and the comment lines written directly above its key at
// the key's own column. The import replaces its format's entry, or adds it, and
// writes every other entry back as it read it, so imports of different formats
// into one directory each keep theirs. A record an older version wrote — one
// import's fields at its top level — reads as one entry naming no format. A
// record that cannot be read refuses a sync, and an import that is not a sync
// writes it anew, holding its own entry alone, with a warning that what it held
// for other formats' imports is lost. A reporting delivery hands the record back
// among the files.
//
// The entry an import reads is chosen by its format, before the published
// catalog snapshot is read, since the entry pins the snapshot: the format named;
// for a sync naming none over a record of one entry, that entry's; for an import
// naming none over a record of several, the format its source is recognised as
// by the importers of the workspace's catalog folders and the ones the binary
// carries, refused naming the record's formats where none recognises it
// (formatBeforeEntry); otherwise it is recognised over the whole view, the
// record's only entry pinning the snapshot. An import reads its format's entry.
// Where the record holds none, a sync reads the entry naming no format as its
// own, taking it over where it lists a path the sync drafts, and an import that
// is not a sync reads of it only the catalog snapshot it pins: its target and
// its answers file may be another import's.
//
// Without Options.Sync, an entity already at a drafted path refuses the import
// whatever the delivery, before anything is asked, written or reported, so a dry
// run refuses it as the write does, naming another format's import where that
// import's entry lists the path. Every other file already at a drafted path is
// written over, and a file an earlier import drafted that this one does not draft
// is left where it is. An entry naming no format is kept as it is, beside the
// import's own.
//
// With Options.Sync, the record is read before the target is settled: a sync
// given no target drafts for the target its entry names, and one given a target
// drafts for it and records it in place of the one recorded. A sync reading the
// entry naming no format takes it over — writes it back under its format — where
// it lists a path the sync drafts, and is refused otherwise, since it may be
// another import's and its files would read as dropped by the source. The import
// then judges every part three ways — what its entry says the import last wrote
// there, what is on disk, and what the source drafts now — and changes only what
// the import owns. A part the source left as the
// import last wrote it stays exactly as it is on disk, whether or not an author
// edited it. A part the source changed is written anew where it is on disk as
// the import last wrote it; a part the source dropped is removed where it is as
// the import last wrote it and holds nothing an author added — a drafted file
// deleted whole; a part the source gained is added beside the parts the draft
// names around it. Everything else on disk — a key an author added, a comment
// not written directly above an owned key, a file its entry does not list, every
// other format's entry and file — is left byte for byte. A sync that would change
// or remove a part an author edited, add a part where an author already wrote one,
// or remove a part holding what an author added, is refused naming each, and
// nothing is written. An entity on disk at a drafted path its entry does not list
// refuses the sync, naming another format's import where that import's entry
// lists it. A sync whose importer reported a halt writes nothing. A sync into a
// directory whose record holds no entry it reads, and nothing at its drafted
// paths, writes the whole draft, as a plain import does.
func Import(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, opts Options) (Result, error) {
	if _, err := workspaceOrRefuse(rw); err != nil {
		return Result{}, err
	}
	if opts.ResolvePlugin == nil {
		return Result{}, fmt.Errorf("internal: no plugin resolver supplied — the caller must supply one and pass it in")
	}

	// The source is found before the catalog is loaded, so a source that cannot be
	// read is refused as that, whatever the catalog holds. A directory is listed
	// here and none of its files is opened yet; the folder a build of this
	// workspace writes to is composed where the build composes it, so the folder
	// the listing leaves out is the one the build wrote.
	src, err := readSource(opts.SourcePath, opts.SourceName, artifactout.DefaultOutputDirIn(rw.Root()))
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

	// The record beside the drafts is read before the target and the catalog are
	// settled, because the entry of it this import reads names both: a sync given
	// no target takes the one that import was given — the drafts it judges were
	// composed for that target, and composing them for none would read as the
	// source having dropped the drafted Manifest — and every import reads the
	// catalog snapshot its entry pins. A record a sync cannot read refuses it; one
	// an import that is not a sync cannot read is written anew, holding its entry
	// alone, and a warning says so.
	found, recordOnDisk, recordErr := readRecord(writescope.RootFor(rw.Root(), outputDir), outputDir)
	if recordErr != nil && opts.Sync {
		return Result{}, recordErr
	}
	var record importRecord
	if found != nil {
		record = *found
	}
	format, err := formatBeforeEntry(ctx, opts, src, seam, record)
	if err != nil {
		return Result{}, err
	}
	rec := record.readBy(format)
	target := opts.Target
	if opts.Sync && target == "" && rec != nil {
		target = rec.Target
	}

	// The published catalog snapshot an import reads — behind the workspace's
	// catalog folders, or as its whole catalog where it names none — is the one its
	// entry in the record beside the drafts names, where it has one, whatever this
	// import does with the record. A harness standing in for the seam reads no
	// snapshot for one.
	var snapshot catalogSnapshot
	if seam.readSnapshot != nil {
		read := catalogSnapshotRead{upgrade: opts.Upgrade, sync: opts.Sync}
		if rec != nil {
			read.pinned = rec.CatalogSnapshot
		}
		if seam, snapshot, err = seam.readSnapshot(ctx, read); err != nil {
			return Result{}, err
		}
	}

	// A target no Recipe of the catalog binds to is refused as soon as the catalog
	// is in hand, before the source is recognised or the importer runs, so nothing
	// is read through a parser, nothing is drafted and nothing is written. Where
	// no snapshot was read behind the folders, or as the whole catalog, the
	// refusal says why too.
	if err := checkTarget(target, target != opts.Target, seam.Recipes); err != nil {
		if snapshot.notice != "" {
			return Result{}, fmt.Errorf("%w; %s", err, snapshot.notice)
		}
		return Result{}, err
	}

	if format == "" {
		if format, err = SelectFormat(ctx, "", src, seam); err != nil {
			return Result{}, err
		}
		// The format recognised now chooses the entry the import reads its answers
		// file pointer from and writes, where the record's only entry, which pinned
		// the snapshot, is another format's. A sync's is the entry it read already:
		// a record of one entry naming a format gave the sync that format.
		rec = record.readBy(format)
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

	// The drafts an earlier import of this format wrote are its own, not catalog
	// facts: where they lie in a catalog folder the view reads — drafts an importer
	// lays out under catalog/ beneath the workspace root — the importer would read its
	// own earlier Component as one the catalog holds, and judge the draft against
	// it. So the view it is handed leaves out every Component authored in a file
	// this import's entry lists, by place alone; every other Component stays in
	// view, one a catalog folder holds that no entry lists included.
	if own := ownEntry(opts.Sync, rec); own != nil && seam.projectWithout != nil && len(own.Files) > 0 {
		seam.Catalog = seam.projectWithout(listedDrafts(outputDir, *own))
	}

	imported, err := importersocket.ImportArtifact(ctx, resolvedImporter{plugin: importer, err: resolveErr},
		seam.importerLoader(ctx), seam.ParserRes, seam.parserLoader(ctx), format, primary, primaryName, sources, settings,
		seam.Catalog, target)
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

	// 🔴 Every refusal that does not turn on the answers is judged here, before
	// anyone is asked anything, so an import that would be refused asks nothing:
	// a person never types an answer into a run that then throws it away. The
	// answers change what a draft holds, never where it lands or what kind it is,
	// so what is judged here is what the answered drafts would be judged as.
	if err := checkDraftPlaces(rw.Root(), outputDir, draft, opts.Sync, record, format, rec); err != nil {
		return Result{}, err
	}

	// What the drafts still leave empty is read off the composed draft, answered
	// from the answers file and, where a person is asked, from them, and the
	// answers are written into the drafts — all before the record of them is
	// composed, so a re-run given the same answers drafts the same bytes and a
	// sync finds the drafts in step. Nobody is asked under a reporting delivery,
	// which keeps no answer, nor by a sync whose importer halted, which writes
	// nothing.
	ask := opts.Ask
	if opts.Delivery.Reports() || (opts.Sync && (Result{Diagnostics: diagnostics}).Halted()) {
		ask = nil
	}
	owing, err := answerDrafts(rw.Root(), outputDir, ownEntry(opts.Sync, rec), opts, ask, seam.Recipes, draft)
	if err != nil {
		return Result{}, err
	}
	draft = owing.files
	diagnostics = append(diagnostics, owing.diagnostics...)

	res := Result{
		Format:      format,
		OutputDir:   outputDir,
		Files:       draft,
		Diagnostics: diagnostics,
		Decisions:   imported.Decisions,
		Owed:        owing.remaining,
		reported:    opts.Delivery.Reports(),
		// An answers file written as the questions were answered was written by
		// this import, whether or not the end of it writes the file again.
		AnswersWritten: owing.kept,
	}
	if owing.path != "" {
		res.Answers = owing.path
	}
	// The record of what the import writes is composed from the draft, and placed
	// after it, at the place judged with the drafted paths above: the record read,
	// with this import's entry in it replaced or added and every other entry as it
	// was — save the entry naming no format a sync took over, which its entry
	// replaces.
	drafted := draftedFiles(draft)
	base := record
	if opts.Sync && rec != nil && rec.Format == "" {
		base = record.without("")
	}
	recordFile := recordScaffold(base, format, target, snapshot.recorded(), owing.pointer, drafted)
	files := draft
	if len(drafted) > 0 {
		files = append(append(make([]abi.ScaffoldFile, 0, len(files)+1), files...), recordFile)
		if recordErr != nil {
			res.Diagnostics = append(res.Diagnostics, warnDiagnostic(unreadableRecordNotice(outputDir, recordErr)))
		}
	}
	// The offline posture is known now, so a pull it will not let the import make
	// is named among the diagnostics a surface reports before anything is written.
	release := draftedReleasePlace(outputDir, draft)
	res.Diagnostics = append(res.Diagnostics, pullNotice(opts, rw, res, release)...)
	if opts.Sync {
		res, err = syncImport(rw.Root(), outputDir, recordFile, opts, res, drafted, rec, recordOnDisk)
		if err != nil {
			return Result{}, err
		}
		return finishImport(ctx, rw, opts, res, owing, release)
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
	return finishImport(ctx, rw, opts, res, owing, release)
}

// owing is what answering the drafts' owed values produced: the drafts with the
// answers written in, the values still owed, the diagnostics naming them, and the
// answers file — its place on this host, empty where the drafts owe nothing, its
// pointer from the output directory, the content it is written with, empty
// where it already holds exactly that, and whether it was written already, as
// the questions were answered.
type owing struct {
	files       []abi.ScaffoldFile
	remaining   []Owed
	diagnostics []abi.Diagnostic
	path        string
	pointer     string
	content     string
	kept        bool
}

// answerDrafts lists what the drafts owe ([readOwed]), answers it from the
// answers file — the one opts names, the one prior names (the import's entry in
// the record beside the drafts), or the one beside the source — and through ask
// where it is set, and composes the answers file that lists every owed value with
// its answer.
//
// Each answer a person gives is written into the answers file at once, held to
// the root a file there is written beneath against the workspace at
// workspaceRoot, so a refusal judged after the questions — a sync's conflicts, or
// a write that fails — leaves what was typed in the file for the next run to read.
func answerDrafts(workspaceRoot, outputDir string, prior *recordEntry, opts Options, ask Ask, recipes RecipeIndex,
	draft []abi.ScaffoldFile) (owing, error) {
	out := owing{files: draft}
	owed := readOwed(draft, recipes)
	if len(owed) == 0 {
		return out, nil
	}
	path := opts.Answers
	if path == "" && prior != nil && prior.Answers != "" {
		path = filepath.FromSlash(prior.Answers)
		if !filepath.IsAbs(path) {
			path = filepath.Join(outputDir, path)
		}
	}
	beside := DefaultAnswersPath(opts.SourcePath)
	if abs, err := filepath.Abs(beside); err == nil {
		beside = abs
	}
	if path == "" {
		path = beside
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	fromFile, _, err := readAnswers(path)
	if err != nil {
		return owing{}, err
	}
	kept := false
	keep := func(given answers) error {
		content, err := answersFileUpdate(path, owed, given)
		if err != nil || content == "" {
			return err
		}
		if err := writeAnswersFile(workspaceRoot, path, content); err != nil {
			return err
		}
		kept = true
		return nil
	}
	ans, err := answerOwed(draft, owed, fromFile, path, ask, keep)
	if err != nil {
		return owing{}, err
	}
	content, err := answersFileUpdate(path, owed, ans.given)
	if err != nil {
		return owing{}, err
	}
	// The record points at the answers file only where it is not the one beside
	// the source: that one is found again from the source every later import
	// names, so pointing at it would add nothing but make the record depend on
	// where the source happens to sit.
	pointer := ""
	if path != beside {
		pointer = answersPointer(outputDir, path)
	}
	out = owing{files: ans.files, remaining: ans.remaining, path: path, pointer: pointer, content: content, kept: kept}
	out.diagnostics = append(out.diagnostics, ans.notices...)
	for _, o := range ans.remaining {
		out.diagnostics = append(out.diagnostics, owedDiagnostic(o))
	}
	return out, nil
}

// draftedReleasePlace is the drafted Release's place on this host, or "" where the
// drafts hold none.
func draftedReleasePlace(outputDir string, draft []abi.ScaffoldFile) string {
	for _, f := range draftedFiles(draft) {
		if draftKind(f.content) == entitykind.Release {
			if place, err := draftPlace(outputDir, f.path); err == nil {
				return place
			}
		}
	}
	return ""
}

// pulls reports whether an import that produced res pulls the dependencies of
// its drafted Release once its files are written: the surface supplies a pull,
// the drafts hold a Release, the delivery writes, the importer finished and the
// drafts owe nothing — a pull of a Release still owing values would resolve
// against drafts the reader has yet to finish.
func pulls(opts Options, res Result, release string) bool {
	return opts.Pull != nil && release != "" && !opts.Delivery.Reports() && !res.Halted() && len(res.Owed) == 0
}

// pullNotice is the diagnostic an import that would pull says it did not under
// the offline posture, naming the pull to run; it is none otherwise.
func pullNotice(opts Options, rw *workspaceconfig.ResolvedWorkspace, res Result, release string) []abi.Diagnostic {
	if !pulls(opts, res, release) || !rw.Config().Offline {
		return nil
	}
	return []abi.Diagnostic{infoDiagnostic(fmt.Sprintf("the workspace is offline, so the dependencies of the "+
		"drafted Release were not pulled; run aggrega pull --release %s next", release))}
}

// finishImport does what an import does once its drafts are written: it writes
// the answers file where the drafts owe values, and pulls the drafted Release's
// dependencies where [pulls] says it does and the workspace is not offline,
// carrying the pull's own account among the diagnostics and its failure as the
// result's [Result.PullRefusal]. A reporting delivery, and a sync whose importer
// halted, wrote nothing, and do neither.
func finishImport(ctx context.Context, rw *workspaceconfig.ResolvedWorkspace, opts Options, res Result,
	owe owing, release string) (Result, error) {
	if opts.Delivery.Reports() || (opts.Sync && res.Halted()) {
		return res, nil
	}
	// An answers file already holding every owed key with its answer is left as it
	// is, so a run that changes nothing writes nothing.
	if owe.path != "" {
		res.answersHeld = true
		if owe.content != "" {
			if err := writeAnswersFile(rw.Root(), owe.path, owe.content); err != nil {
				return res, err
			}
			res.AnswersWritten = true
		}
	}
	// Under the offline posture the diagnostic composed before the write already
	// names the pull to run.
	if !pulls(opts, res, release) || rw.Config().Offline {
		return res, nil
	}
	notices, err := opts.Pull(ctx, release)
	for _, line := range notices {
		res.Diagnostics = append(res.Diagnostics, infoDiagnostic(line))
	}
	if err != nil {
		res.pullFailed = &PullError{Release: release, Err: err}
		return res, nil
	}
	res.pulled = true
	return res, nil
}

// formatBeforeEntry settles the format of an import before the entry of the
// record beside the drafts it reads is chosen, where the entry turns on it, and
// returns "" where the format is left to the recognition over the catalog the
// entry chosen pins ([SelectFormat]).
//
// The entry is chosen before the published catalog snapshot is read, because it
// names the snapshot. So: a format the caller names is the import's at once; a
// sync naming none over a record holding one entry takes that entry's format —
// none, for the entry an older version's record reads as, whose import is then
// recognised as any import is; and an import naming none over a record holding
// entries of several formats has its source recognised now, by the importers of
// the seam built before any snapshot is read — the workspace's catalog folders'
// and the ones the binary carries — through the one recognition every import
// takes. Where none of those recognises the source, it is refused as an
// [UndetectedFormatError] naming the record's formats, so the reader names the
// import it is; an importer only the published catalog holds is never asked.
// Every other import — over no record, or over one entry — is left to the
// recognition over the whole view.
func formatBeforeEntry(ctx context.Context, opts Options, src *Source, seam Seam, record importRecord) (string,
	error) {
	if opts.Format != "" {
		return opts.Format, nil
	}
	if only, ok := record.only(); ok && opts.Sync {
		return only.Format, nil
	}
	if len(record.Imports) < 2 {
		return "", nil
	}
	format, err := SelectFormat(ctx, "", src, seam)
	if undetected := (*UndetectedFormatError)(nil); errors.As(err, &undetected) {
		return "", &UndetectedFormatError{Source: undetected.Source, Advertised: undetected.Advertised,
			Directory: undetected.Directory, DirectoryAdvertised: undetected.DirectoryAdvertised,
			Recorded: record.formats()}
	}
	return format, err
}

// unreadableRecordNotice is the warning of an import that writes anew the record
// beside its drafts because the one there cannot be read: why, and that what it
// held for other formats' imports does not reach the record written.
func unreadableRecordNotice(outputDir string, err error) string {
	reason := err
	if refused := (*ImportRecordError)(nil); errors.As(err, &refused) && refused.Err != nil {
		reason = refused.Err
	}
	return fmt.Sprintf("the record of what imports wrote beneath %q, %s, cannot be read: %v; this import writes it "+
		"anew, holding its own entry alone, so any entry it held for another format's import is lost", outputDir,
		RecordFileName, reason)
}

// checkDraftPlaces takes every judgement of a draft set that turns on where its
// files land and what is on disk there, and none on what they hold: each drafted
// path, and the record's, held inside the output directory and, with its links
// resolved, to the root it picks against the workspace at workspaceRoot
// ([checkFiles]); no draft at the record's own place ([checkRecordPlace]); and,
// without sync, no guarded entity already on disk at a drafted path
// ([checkExistingEntity]) — with sync, none there that rec, the entry of the
// record the sync reads, does not list, and no entry naming no format read in
// place of the sync's own that lists none of the drafted paths
// ([ImportRecordError]). A guarded entity another format's entry in record lists
// refuses either, naming that format's import.
//
// They are taken together, once, before the drafts' owed values are answered and
// before either delivery answers, so a dry run refuses what the write refuses and
// an import these refuse asks nobody anything. Answering an owed value changes
// what a draft holds and never its path or its kind, so the answered drafts are
// judged by what is judged here. An entity on disk is read only after every
// path is held inside the root, so it is read only at a place already judged.
func checkDraftPlaces(workspaceRoot, outputDir string, draft []abi.ScaffoldFile, sync bool, record importRecord,
	format string, rec *recordEntry) error {
	drafted := draftedFiles(draft)
	files := draft
	if len(drafted) > 0 {
		files = append(append(make([]abi.ScaffoldFile, 0, len(draft)+1), draft...),
			abi.ScaffoldFile{Path: RecordFileName})
	}
	if err := checkFiles(workspaceRoot, outputDir, files); err != nil {
		return err
	}
	if err := checkRecordPlace(outputDir, draft); err != nil {
		return err
	}
	if !sync {
		return checkExistingEntity(outputDir, draft, record, format)
	}
	// An entry naming no format is this sync's only where it lists a path the sync
	// drafts: otherwise it may be another import's record, whose files the sync
	// would remove as dropped by the source.
	if rec != nil && rec.Format == "" && !listsAnyOf(*rec, drafted) {
		return &ImportRecordError{Record: RecordFileName, OutputDir: outputDir, Unclaimed: true}
	}
	for _, f := range drafted {
		place, err := draftPlace(outputDir, f.path)
		if err != nil {
			return err
		}
		if kind := guardedKindAt(place); kind != "" && (rec == nil || !recordLists(*rec, f.path)) {
			return &ImportRecordError{Record: RecordFileName, OutputDir: outputDir, Path: f.path, Kind: kind,
				Format: record.listedByAnother(f.path, format)}
		}
	}
	return nil
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

// ownEntry returns the entry of the record beside the drafts whose drafts are
// this import's own — rec, the entry the import reads — or nil where it has none.
// An entry naming no format is this import's only once a sync takes it over: an
// import that is not a sync reads of it the catalog snapshot it pins alone, never
// the answers file it points at or the files it lists, which may be another
// import's.
func ownEntry(sync bool, rec *recordEntry) *recordEntry {
	if !sync && rec != nil && rec.Format == "" {
		return nil
	}
	return rec
}

// listedDrafts reports whether a document lies at a file own lists beneath
// outputDir: one of the drafts of the import own records. A document is matched by
// its place, and where its spelling differs from the listed one — a link on the
// way to either — by the file it is. A listed path that does not place beneath
// outputDir names no file.
func listedDrafts(outputDir string, own recordEntry) func(document string) bool {
	var places []string
	var files []os.FileInfo
	for _, f := range own.Files {
		place, err := draftPlace(outputDir, f.Path)
		if err != nil {
			continue
		}
		places = append(places, filepath.Clean(place))
		if info, err := os.Stat(place); err == nil {
			files = append(files, info)
		}
	}
	return func(document string) bool {
		if abs, err := filepath.Abs(document); err == nil && slices.Contains(places, filepath.Clean(abs)) {
			return true
		}
		if len(files) == 0 {
			return false
		}
		info, err := os.Stat(document)
		if err != nil {
			return false
		}
		return slices.ContainsFunc(files, func(f os.FileInfo) bool { return os.SameFile(f, info) })
	}
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

// checkExistingEntity refuses an import of format one of whose drafts would be
// written over an entity already on disk, as an [ExistingEntityError] naming the
// first such draft in the importer's order, and the format of another import
// whose entry in record lists it, where one does.
//
// What is judged is the file already at the place: a regular file whose
// top-level kind is one an import guards ([guardedKindAt]). The draft's own
// content is not read, because what an import must not do is write over a
// document an author owns, whatever the importer drafted in its place. A file
// that does not read as YAML, or reads as another kind, is not one, and a draft
// lands on it as before.
//
// Every path has already passed checkFiles, and each is placed by [draftPlace]
// again, so the place read is the place that was judged.
func checkExistingEntity(outputDir string, files []abi.ScaffoldFile, record importRecord, format string) error {
	for _, f := range files {
		place, err := draftPlace(outputDir, f.Path)
		if err != nil {
			return err
		}
		if kind := guardedKindAt(place); kind != "" {
			return &ExistingEntityError{Path: f.Path, OutputDir: outputDir, Kind: kind,
				Format: record.listedByAnother(f.Path, format)}
		}
	}
	return nil
}

// guardedKindAt returns the kind of a regular file at place that reads as a
// document of a kind an import never writes over unasked — Aggregate, and the
// kinds a catalog indexes: Component, Feature, Recipe and FeatureRecipe — and ""
// for anything else: no file, a directory, bytes that are not YAML, another kind.
func guardedKindAt(place string) entitykind.Kind {
	info, err := os.Stat(place)
	if err != nil || !info.Mode().IsRegular() {
		return ""
	}
	data, err := os.ReadFile(place)
	if err != nil {
		return ""
	}
	switch kind := draftKind(string(data)); kind {
	case entitykind.Aggregate, entitykind.Component, entitykind.Feature, entitykind.Recipe, entitykind.FeatureRecipe:
		return kind
	}
	return ""
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
