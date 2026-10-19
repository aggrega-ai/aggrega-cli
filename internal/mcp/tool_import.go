// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// importToolName is the tool's advertised name, referenced where a path is
// resolved and where a refusal names the tool it is about, so the name has one
// home on this surface rather than being spelled again at each site.
const importToolName = "import"

// importArgs is the import tool's argument shape.
//
// Source names the file or the directory to convert, relative to the workspace
// root or by absolute path, as every other path argument here is named. Format
// names the source format outright; leaving it out recognises the format by the
// signatures the importers declare, the core importers' included. DryRun does
// everything the plain request
// does except write, which is a property of the caller rather than of this
// surface, so it is a request field rather than a second tool and it defaults
// to writing — the operation's product is a set of files.
//
// 🔴 There is deliberately NO argument naming the directory the drafts land in,
// and its absence is a design choice rather than a bound this surface could not
// lift. The workspace a call resolves is already settled before the importer
// runs, a path reaching outside it is already refused, and a second way to say
// where files go would only add a way to get the destination wrong. The drafts
// land beneath that workspace's root, which is where the command writes them too
// when it is given no destination. A caller that wants another destination names
// one to the command with its --output-dir, which the command's generated tool
// carries as an argument over the process's own streams.
type importArgs struct {
	Source string `json:"source"`

	// Format is a pointer so that a value a caller sent is distinguishable from
	// one it left out. Absent means the format is recognised by the signatures the
	// importers declare, which is this tool's default and not a failure; a value the
	// caller wrote and left blank is refused, because detecting instead would be
	// an argument quietly substituted — and an argument quietly substituted
	// reads to its sender exactly like one that was honoured. A plain string
	// would make those two the same bytes at the point they have to be told
	// apart.
	Format *string `json:"format"`

	DryRun        bool   `json:"dry_run"`
	WorkspaceRoot string `json:"workspace_root"`

	// Sync is the command line's --sync, a request field for the reason DryRun is
	// one: it asks for what an earlier import drafted to be brought up to date with
	// the source, changing only what the import wrote, rather than the draft being
	// written whole.
	Sync bool `json:"sync"`
}

// importDrift is one part a sync brought up to date, or under `dry_run` would:
// how it drifted, the drafted file it is in, and the keys leading to it in a
// drafted Aggregate, empty where the part is the whole file.
//
// The keys are a list rather than one joined string, so a key holding a full
// stop is read back as the one key it is.
type importDrift struct {
	Change string   `json:"change"`
	File   string   `json:"file"`
	Key    []string `json:"key"`
}

// importedDraft is one drafted file in the answer: the path it is for, relative
// to the workspace root, and its content.
//
// The content travels with its path rather than in a channel of its own,
// because a caller handed content and a list of paths separately would have to
// correlate them and an import producing several files gives it nothing to
// correlate on.
type importedDraft struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// importDiagnostic is one importer diagnostic as this protocol carries it.
//
// It is a projection rather than the socket's own diagnostic type, for the
// reason the payload below is projected: that type spells one of its fields in
// the plugin wire's convention rather than in this surface's, so serializing it
// directly would answer with a key shaped unlike every other key here. Code is
// present only where the importer assigned one, which is a fact about the
// diagnostic rather than a gap.
type importDiagnostic struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	Code     string `json:"code,omitempty"`
}

// importDecision is one of the importer's mapping decisions as this protocol
// carries it: the source node, the catalog Components it weighed, what it chose,
// why, and how to swap the choice.
//
// It is a projection of the socket's own decision type, for the reason the
// diagnostic beside it is one: that type spells its keys in the plugin wire's
// convention, and every key on this surface is snake_case. The values are the
// importer's, unaltered and in its order; only the keys are this surface's, and
// a candidate's Component is named under the one word `component`.
//
// Candidates is absent where the importer weighed none, Chosen where it made no
// choice, and Rationale and SwapHint where it wrote none — each a fact about the
// decision, as it is on the importer's own answer, rather than a gap.
type importDecision struct {
	SourceNode importDecisionNode    `json:"source_node"`
	Candidates []importCandidate     `json:"candidates,omitempty"`
	Chosen     *importDecisionChoice `json:"chosen,omitempty"`
	Rationale  string                `json:"rationale,omitempty"`
	SwapHint   string                `json:"swap_hint,omitempty"`
}

// importDecisionNode is the source node a decision resolved.
type importDecisionNode struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	ParamsSummary string `json:"params_summary"`
}

// importCandidate is one catalog Component the importer weighed for a node, with
// its score, the tier the score falls in and the per-signal breakdown behind it.
type importCandidate struct {
	Component string                 `json:"component"`
	Score     float64                `json:"score"`
	Tier      string                 `json:"tier"`
	Signals   importCandidateSignals `json:"signals"`
}

// importCandidateSignals is the contribution each signal made to a candidate's
// score.
type importCandidateSignals struct {
	Table        float64 `json:"table"`
	Category     float64 `json:"category"`
	DomainLabels float64 `json:"domain_labels"`
	Shape        float64 `json:"shape"`
	Name         float64 `json:"name"`
}

// importDecisionChoice is what the importer chose for a node: the target, how it
// emitted it, and the tier of the choice.
type importDecisionChoice struct {
	Target string `json:"target"`
	Mode   string `json:"mode"`
	Tier   string `json:"tier"`
}

// importPayload is the import tool's structured payload: the workspace the
// drafts were written into, the directory the drafted paths are relative to,
// how the source was read, what reached disk, and what the importer had to say
// about the parts it could not translate.
//
// It is projected from the shared layer's own result rather than embedded, and
// the keys are this protocol's own — the same division the edit tool beside it
// draws. What must not be restated is the layer's result, and nothing here
// does: every field that result carries is read off it.
//
// OutputDir is the directory the drafted paths are relative to, exactly as the
// layer reports it, under both deliveries — a dry run writes nothing, but each
// path it hands back is still relative to it. On this tool it is always the
// root of the workspace the call resolved, because this surface hands the layer
// no destination and the layer writes beneath the workspace root when none is
// named, so it names nothing WorkspaceRoot does not already name, on either
// transport. It is carried all the same because it is the one fact the layer
// reports about where an import went and every other surface calling the layer
// hands it on: a caller reading it learns where the drafts are, and no argument
// here lets it choose.
//
// Both are the workspace root, a directory on this host, so both are told the
// way every location here is: for a caller reaching a listener each is absent and
// marked by the withheld flag beside it, which is omitted where nothing was kept
// back. That caller already has the coordinate the drafts are in, because every
// drafted path is relative to the root it names its own arguments against.
//
// A diagnostic's message is prose the importer composed, and is told the same way.
//
// Drafts carries each drafted file's content under a reporting delivery and is
// absent otherwise. Under a write the files are on disk and Written names them,
// so handing their whole text back as well would spend a caller's budget on
// bytes it asked to have written; under a dry run the content is the entire
// answer, and withholding it would leave the delivery answering nothing.
//
// Halted says the importer reported something that stops the import, so the
// source was not fully turned into draft files. It is carried as a field rather
// than left to the failure flag alone because a partial import still drafts
// what it could, and a caller deciding whether to keep that needs the fact
// rather than only the refusal.
//
// Decisions is the importer's mapping decision for each node it mapped, in its
// order, under both deliveries and whether or not the import halted — what tells
// a caller where the import guessed. It is always an array, empty for an importer
// that reports none rather than absent.
//
// Drift is every part a `sync` call brought up to date with the source, or under
// `dry_run` would, in the order the draft names them: the answer a command line
// gives through its exit status, carried here as data because this surface has
// none. It is always an array, empty for a call that is not a sync and for a sync
// in step.
type importPayload struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`
	OutputDir             *string `json:"output_dir"`
	OutputDirWithheld     bool    `json:"output_dir_withheld,omitempty"`

	Source      string             `json:"source"`
	Format      string             `json:"format"`
	Written     []string           `json:"written"`
	Drafts      []importedDraft    `json:"drafts,omitempty"`
	Drift       []importDrift      `json:"drift"`
	Diagnostics []importDiagnostic `json:"diagnostics"`
	Decisions   []importDecision   `json:"decisions"`
	Halted      bool               `json:"halted"`
}

var importToolSpec = toolSpec{
	summary: "Turns a foreign source description into draft Aggrega authoring files, written into the workspace.",
	description: "Turn a foreign source description — a flow or a service definition exported by another tool — " +
		"into draft Aggrega authoring files, WRITTEN into the workspace this server serves. Pass `source`, the " +
		"file or the directory to convert. The importer plugin that advertises the " +
		"source's format does the translation: it reads the source, drafts entities from it, and this writes what " +
		"it produced, creating parent directories as needed. A directory — a chart or a project, say — is handed to " +
		"the importer as exactly the files of it the importer's declared file patterns select, each named by its " +
		"path inside the directory: an importer declaring no file patterns refuses a directory, and a link among " +
		"the files it would read, or to a directory, that leads out of it is refused before anything is read. " +
		"A drafted file lands at a path relative to the workspace root, which is also the coordinate " +
		"the other tools here report a file under, and THERE IS NO ARGUMENT NAMING A DESTINATION — the drafts " +
		"go into the workspace the call resolved, and nowhere else. The answer names that directory as " +
		"`output_dir`, the one every drafted path is relative to, whether the file was written or handed back: " +
		"it reports where the drafts went and chooses nothing. The importer is one this workspace's catalog " +
		"holds or one of the compiler's core importers, which serve their formats in every workspace; a format " +
		"no importer advertises is refused naming the capability no plugin advertises. A core importer this " +
		"server's machine does not hold yet is fetched by the first import that needs it, from the workspace's " +
		"default registry with this machine's own login, held to the module the compiler's release publishes " +
		"and verified as a pull verifies a plugin, then kept in the workspace's content cache, so later imports " +
		"reach no registry; nothing in the call names a registry or carries a credential. Where it cannot be " +
		"fetched, the call is refused naming the importer, its version and what puts it on the machine. A " +
		"`plugins.importer` attachment whose `uri` names an importer " +
		"advertising the format, by identity and version, runs that importer at that version whatever other " +
		"importer advertises the format too, and hands it — and no other importer — its `with.sources` and " +
		"`with.settings`; where several advertise the format and no single one is attached — a core importer " +
		"and one of the catalog's among them — the call is refused naming them. " +
		"Leave `format` out and the format is recognised by the signatures the importers declare — the syntax a " +
		"source is read in, the file names it applies to and the paths its parsed tree carries — parsing through " +
		"the parsers the importer reads the source with, the one this workspace's catalog holds for a syntax or " +
		"the compiler's own for a syntax it holds none for, reading the file named, or of a directory only the files a " +
		"signature's file patterns select, and no network: a source no signature matches is refused asking for " +
		"`format` and naming the formats the importers advertise, and a source " +
		"signatures of more than one format match is refused naming them, rather than guessed at. Send `dry_run` to be " +
		"handed each drafted file's path and content instead, with nothing created: every other behaviour is " +
		"the write's, the same importer over the same source reporting the same diagnostics, so a source the " +
		"write would refuse is refused under it too. Every import that drafts anything also writes, beside its " +
		"drafts, `.aggrega-import.json`, the record of what it wrote — each drafted file, and each key of a " +
		"drafted Aggregate at every depth, with a digest of its text — and `dry_run` hands it back among the " +
		"drafts. A call whose drafts would be written over an Aggregate " +
		"already in the workspace is refused, naming it, and nothing is written — under `dry_run` too — because " +
		"an Aggregate is the document an author goes on editing once it is drafted; every other file already at " +
		"a drafted path is written over. Send `sync` to bring what an earlier import drafted up to date with the " +
		"source instead, changing only what the import wrote: a part the source left alone stays as it is on " +
		"disk, edited or not; a part it changed, dropped or gained is written anew, removed — a drafted file " +
		"deleted whole — or added, where what is on disk is what the import last wrote; and everything else — a " +
		"key an author added anywhere, a comment not written directly above a drafted key, a file the record does " +
		"not list — stays byte for byte. The answer's `drift` lists each part brought up to date, or under " +
		"`dry_run` each part that would be, empty when the drafts are in step. A sync that would change or remove " +
		"what an author edited or added, or add a part where an author already wrote one, is refused naming each " +
		"part and why, and nothing is written; so is a sync over an Aggregate no record lists, and one whose " +
		"importer could not finish writes nothing. What the importer could not translate comes back as " +
		"diagnostics, one per construct, and an import it could not finish still drafts what it could — read " +
		"`halted` for that outcome and the diagnostics beside it for what stopped it. The drafts are a " +
		"starting point an author owns and goes on editing; whether what they say is admissible is what " +
		"validate answers, not this.",
	listenerNote: "You did not start this server, so you are not told where its workspace sits on its host: " +
		"`output_dir` is absent and marked `output_dir_withheld`, and so is the answer's workspace root. Every " +
		"drafted path is still relative to that root, the one your own paths are named against.",
	inputSchema: `{
  "type": "object",
  "properties": {
    "source": {
      "type": "string",
      "minLength": 1,
      "description": "The file or directory to convert, relative to the workspace root or absolute. A directory is read into the files of it the importer's declared file patterns select, and no link inside it is followed out of it. Over HTTP it must lie inside the workspace this server serves."
    },
    "format": {
      "type": "string",
      "minLength": 1,
      "description": "The source format, named outright — for example langflow, flowise, n8n, mcp, compose or dify. Leave it out and the format is recognised by the signatures the importers declare, the workspace catalog's and the compiler's core importers' alike."
    },
    "dry_run": {
      "type": "boolean",
      "description": "Do everything except write, and hand back each drafted file's path and content. Every other behaviour is the write's, so a source the write would refuse is refused under it too."
    },
    "sync": {
      "type": "boolean",
      "description": "Bring what an earlier import drafted up to date with the source, changing only what the import wrote, as the record beside the drafts lists it. The answer's drift names each part brought up to date, or under dry_run each part that would be."
    },
    "workspace_root": {
      "type": "string",
      "description": "Optional workspace root override. Defaults to the workspace root the server was launched with, or, where it was launched with none, to discovery from its working directory."
    }
  },
  "required": ["source"]
}`,
	invoke: importInvoke,
}

func importInvoke(s *server, ctx context.Context, args json.RawMessage) (*callToolResult, *rpcError) {
	var a importArgs
	if len(args) > 0 {
		if err := json.Unmarshal(args, &a); err != nil {
			return invalidToolArguments(importToolName, err)
		}
	}
	source := strings.TrimSpace(a.Source)
	if source == "" {
		return errorResult(importToolName + " requires a 'source': the foreign description to turn into draft " +
			"authoring files, named relative to the workspace root. Nothing here searches for one.")
	}
	named := ""
	if a.Format != nil {
		if named = strings.TrimSpace(*a.Format); named == "" {
			return errorResult(importToolName + " was sent a blank 'format'. Name the source format outright, " +
				"or leave 'format' out altogether and it is recognised by the signatures the importers declare; " +
				"a blank value is neither, so nothing was read and nothing was written.")
		}
	}
	if err := ctx.Err(); err != nil {
		return errorResult(explainToolError("Could not draft anything from the source.", err, ""))
	}
	if s.resolveImportPlugin == nil {
		return errorResult(importToolName + " cannot run here: this server was started without the plugin " +
			"resolution an importer is loaded through, so no importer can be reached however the workspace is " +
			"configured. Nothing was written. Start the server through the aggrega command, which supplies it.")
	}

	tw, err := s.resolveToolWorkspace(a.WorkspaceRoot, nil)
	if err != nil {
		return errorResult(explainToolError("Could not resolve the workspace to draft the entities in.", err, ""))
	}

	// The source is resolved here, through the one helper, before the import opens
	// it. That helper is also what decides how far a path a caller named may reach:
	// a client that launched this process names a location on the machine it is
	// sitting at, and a caller reaching a listener names its subject inside the
	// one workspace this process serves. The path resolved — a file or a directory
	// — is handed to the import, which reads it; a directory is held to itself
	// there, so every file read from one a listener caller names stays inside the
	// directory, which this helper has already held inside the workspace.
	path, refusal := tw.workspacePath(importToolName, "source", source)
	if refusal != "" {
		return errorResult(refusal)
	}

	delivery := authoredcontent.DeliveryFor(a.DryRun)
	// No destination is handed, and that is the whole of what this surface
	// decides about one: the layer writes beneath the root of the workspace it is
	// handed when none is named — the workspace this call resolved — which is
	// also where the command writes when it is given none. So the two land the
	// same drafts in one place, and no argument here names another.
	//
	// The format the caller named, or none, is handed on as it is: the layer
	// settles it, recognising an unnamed one from the signatures of the importers
	// in the workspace's catalog, and a source it cannot settle is refused here in
	// this surface's own words — naming the argument a caller here can send —
	// rather than in the command line's.
	res, err := importops.Import(ctx, tw.rw, importops.Options{
		SourcePath:    path,
		SourceName:    source,
		Format:        named,
		Sync:          a.Sync,
		Delivery:      delivery,
		ResolvePlugin: s.resolveImportPlugin,
	})
	if readErr := (*importops.SourceReadError)(nil); errors.As(err, &readErr) {
		// The location read is named rather than the value as sent: a relative
		// source resolves against the workspace root, and a reader looking for the
		// file needs the place that was actually opened.
		return tw.answer(errorResult(explainToolError("Could not read the source at "+readErr.Path, readErr.Err, "")))
	}
	if refusal, linked := tw.importLinkRefusal(source, err); linked {
		return tw.answer(errorResult(refusal))
	}
	if refusal, unsettled := importFormatRefusal(err); unsettled {
		return errorResult(refusal)
	}
	if existing := (*importops.ExistingAggregateError)(nil); errors.As(err, &existing) {
		return errorResult(importExistingAggregateRefusal(existing))
	}
	if conflicts := (*importops.SyncConflictError)(nil); errors.As(err, &conflicts) {
		return errorResult(importConflictRefusal(conflicts))
	}
	if record := (*importops.ImportRecordError)(nil); errors.As(err, &record) {
		return tw.answer(errorResult(importRecordRefusal(tw, record)))
	}
	if err != nil {
		return errorResult(explainDiagnostic(importRefusal(source, err), ""))
	}

	payload := importPayload{
		Source:      source,
		Format:      res.Format,
		Written:     importWritten(res, delivery),
		Drift:       importDriftFor(res.Drift),
		Diagnostics: importDiagnosticsFor(tw, res.Diagnostics),
		Decisions:   importDecisionsFor(tw, res.Decisions),
		Halted:      res.Halted(),
	}
	payload.WorkspaceRoot, payload.WorkspaceRootWithheld = tw.reportedLocationField(tw.root)
	payload.OutputDir, payload.OutputDirWithheld = tw.reportedLocationField(res.OutputDir)
	if delivery.Reports() {
		payload.Drafts = importDrafts(res.Files)
	}

	// An import the importer could not finish is reported as a failure carrying
	// its whole answer, rather than as a success with a flag set. This surface's
	// failure flag marks an operation that ran and reported a problem, which is
	// exactly this, and it is the outcome the command reaches for the same
	// condition — so the two agree about whether one import completed. What a
	// refusal here does NOT do is drop what the run produced: the files that
	// reached disk, the drafts, every diagnostic and every mapping decision are in
	// the payload beside the sentence.
	if halt := res.Refusal(); halt != nil {
		return tw.answer(structuredErrorResult(explainDiagnostic(
			importHaltedText(payload, res, delivery, a.Sync, halt), ""), payload))
	}
	return tw.answer(textResult(renderImportText(payload, res, delivery, a.Sync), payload))
}

// importFormatRefusal composes this surface's sentence for a source whose format
// could not be settled, and reports whether err is one; any other error is left
// to the caller.
//
// The two conditions — no importer recognises the source, and importers of more
// than one format do — are recognised through their own types rather than by
// reading the shipped sentences, because those sentences name the flag a reader
// at a terminal passes and a caller here has no flag to pass. Matching on the
// type is what keeps the two in step: a surface comparing the wording would keep
// compiling and silently stop matching the first time either moved.
//
// The formats it names are read off the refusal itself — the ones the importers
// advertise, or the ones the source was recognised as — so they are the import's
// statement rather than a list of this surface's that would stop being true the
// first time the importers changed.
func importFormatRefusal(err error) (string, bool) {
	var undetected *importops.UndetectedFormatError
	if errors.As(err, &undetected) {
		return fmt.Sprintf("%s did not recognise the source format of %s from the signatures the importers "+
			"declare, and it does not guess: send 'format' naming the format outright (%s) and "+
			"call again. Nothing was written.", importToolName, undetected.Path(), formatChoices(undetected.Formats())), true
	}
	var ambiguous *importops.AmbiguousFormatError
	if errors.As(err, &ambiguous) {
		var b strings.Builder
		if short, ok := cerrors.ShortCodeOf(err); ok {
			b.WriteString(short)
			b.WriteString(": ")
		}
		fmt.Fprintf(&b, "%s recognised the source %s as more than one format — %s — because importers of each "+
			"declare a signature it matches, and it does not choose between them: send 'format' naming the one to "+
			"read it as and call again. Nothing was written.", importToolName, ambiguous.Path(),
			quotedFormats(ambiguous.Formats()))
		return b.String(), true
	}
	return "", false
}

// importLinkRefusal composes this surface's sentence for a directory source
// holding a link that leads out of the directory, for a caller who is not told
// where things sit on this host, and reports whether it composed one; any other
// error, and every error over the process's own streams, is left to the caller.
//
// The condition's own occurrence names the place the link leads to, which is a
// location on this host the caller reaching a listener is told nothing about, and
// which the rewriting of prose cannot keep back when it lies outside the workspace.
// So the sentence is composed here instead, recognised through the refusal's type
// rather than by reading its text: it opens with the condition's short code and its
// registered sentence, names the link by its place relative to the workspace root,
// as every path this caller sends is named, and says nothing of where it leads.
func (tw *toolWorkspace) importLinkRefusal(source string, err error) (string, bool) {
	var linked *importops.DirectoryLinkError
	if !errors.As(err, &linked) || !tw.withholdsHostLocations() {
		return "", false
	}
	// The check that refused the read records the link it judged as the refusal's
	// source location, on this host, so the link is reported from there rather than
	// rebuilt here.
	link, told := "", false
	var occurrence *cerrors.CompilerError
	if errors.As(linked, &occurrence) && occurrence.Source().File != "" {
		link, told = tw.reportedLocation(occurrence.Source().File)
	}
	if !told {
		link = keptBackLocationWords
	}
	var b strings.Builder
	if short, ok := cerrors.ShortCodeOf(err); ok {
		b.WriteString(short)
		b.WriteString(": ")
	}
	if sentence, ok := cerrors.MessageOf(err); ok {
		b.WriteString(sentence)
		b.WriteString(". ")
	}
	fmt.Fprintf(&b, "%s's 'source' names %s, a directory holding the link %s, which leads out of that "+
		"directory; a directory is imported only from inside itself, so nothing was read through the link and "+
		"nothing was written. Remove the link, or keep the file itself in the directory rather than a link to it.",
		importToolName, strconv.Quote(source), strconv.Quote(link))
	return b.String(), true
}

// importExistingAggregateRefusal composes this surface's sentence for a call
// whose drafts would be written over an Aggregate already in the workspace,
// naming the argument a caller here sends rather than the command line's flag.
//
// The Aggregate is named by its drafted path alone. This tool writes beneath the
// root of the workspace the call resolved, so that path is relative to the root a
// caller names its own paths against, and the root itself — a place on this host
// — is not named.
func importExistingAggregateRefusal(e *importops.ExistingAggregateError) string {
	return fmt.Sprintf("%s would write the draft %s over the Aggregate already at that path in the workspace, which "+
		"an author may have edited since it was drafted, so nothing was written. Send 'sync' to ask for it to be "+
		"brought up to date with the source.", importToolName, strconv.Quote(e.Path))
}

// importConflictRefusal composes this surface's sentence for a sync that would
// change or remove what an author wrote, naming each conflicting part in the
// shared wording ([importops.ConflictLine]) and the argument a caller here sent
// rather than the command line's flag.
//
// Each part is named by its drafted path, relative to the workspace root, and
// its keys; the root itself is not named.
func importConflictRefusal(e *importops.SyncConflictError) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s was sent 'sync', and bringing the drafts up to date with the source would change or remove "+
		"what an author wrote, so nothing was written. Reconcile each part below by hand, or put back what the "+
		"import wrote there, and call again:", importToolName)
	for _, c := range e.Conflicts {
		b.WriteString("\n  ")
		b.WriteString(importops.ConflictLine(c))
	}
	return b.String()
}

// importRecordRefusal composes this surface's sentence for a sync the record
// beside the drafts cannot answer for: no record lists the Aggregate it would
// bring up to date, or the record cannot be read. Why it cannot be read is
// prose the operation composed, so it is told the way every such sentence here
// is, keeping back a place on this host from a caller who is not told one.
func importRecordRefusal(tw *toolWorkspace, e *importops.ImportRecordError) string {
	if e.Err != nil {
		return fmt.Sprintf("%s was sent 'sync', and the record of what an import wrote, %s, cannot be read: %s. "+
			"Nothing was written.", importToolName, strconv.Quote(e.Record), tw.reportedText(e.Err.Error()))
	}
	return fmt.Sprintf("%s was sent 'sync', and no record of what an import wrote lists the Aggregate at %s (%s, "+
		"which every import writes beside its drafts), so its parts cannot be told from parts an author wrote. "+
		"Nothing was written.", importToolName, strconv.Quote(e.Aggregate), strconv.Quote(e.Record))
}

// formatChoices names the formats a caller may send, or says the catalog
// advertises none.
func formatChoices(formats []string) string {
	if len(formats) == 0 {
		return "no importer in this workspace's catalog advertises a format to name"
	}
	return "one of " + quotedFormats(formats)
}

// quotedFormats lists formats quoted, joined by commas.
func quotedFormats(formats []string) string {
	quoted := make([]string, 0, len(formats))
	for _, f := range formats {
		quoted = append(quoted, strconv.Quote(f))
	}
	return strings.Join(quoted, ", ")
}

// importRefusal composes this surface's sentence for an import the shared layer
// would not perform, opening with the short code where the failure names a
// registered condition.
//
// The code is read off the error rather than written here, so this surface
// cannot come to report a code the condition was not registered under, and the
// guidance route this feeds then resolves that code into the descriptive one a
// caller matches on.
//
// It claims nothing about what reached disk, deliberately. Each of these
// refusals already states that for itself where it can — an importer path
// climbing out of the workspace says no files were written, and a write that
// failed names the file it failed on — and a blanket sentence here would be
// this surface asserting something it did not establish.
func importRefusal(source string, err error) string {
	var b strings.Builder
	b.WriteString(importToolName)
	b.WriteString(" could not turn ")
	b.WriteString(source)
	b.WriteString(" into draft authoring files. ")
	if short, ok := cerrors.ShortCodeOf(err); ok {
		b.WriteString(short)
		b.WriteString(": ")
	}
	b.WriteString(err.Error())
	return b.String()
}

// importHaltedText opens the answer for an import the importer could not finish,
// and then reports that import exactly as a completed one is reported.
//
// The outcome sentence is composed here while what happened is rendered by the
// one renderer, so a caller reading a halted answer and a caller reading a clean
// one are told what was written in the same words.
func importHaltedText(p importPayload, res importops.Result, delivery authoredcontent.Delivery, sync bool,
	halt error) string {
	var b strings.Builder
	if short, ok := cerrors.ShortCodeOf(halt); ok {
		b.WriteString(short)
		b.WriteString(": ")
	}
	b.WriteString("the importer could not translate the whole of ")
	b.WriteString(p.Source)
	if sync {
		b.WriteString(", so the sync wrote nothing. The diagnostics below name what it could not translate.\n\n")
	} else {
		b.WriteString(", so what it drafted is partial. The diagnostics below name what it could not translate.\n\n")
	}
	b.WriteString(renderImportText(p, res, delivery, sync))
	return b.String()
}

// renderImportText renders the human-readable answer: how the source was read,
// what reached disk, everything the importer reported about the source, and one
// line for each node it mapped.
//
// The three write outcomes are reported differently on purpose. A write names
// the files that reached disk and the root they are relative to, because a
// drafted path carries no anchor of its own. A reporting delivery wrote nothing
// by design and says so, rather than reading as the third case. An import that
// drafted nothing at all is the third, and saying nothing there would leave it
// indistinguishable from a write that failed quietly.
//
// The decision lines come after the diagnostics, as they do on the command
// line's error stream, and each is the shared sentence ([importops.DecisionLine])
// rendered from the importer's own decision, so a reader of either surface reads
// one decision in one wording. They are rendered from the result, the importer's
// own values, rather than from the payload's projection of them, because the
// projection exists to carry this surface's keys and the sentence carries none.
// A sync's drift lines come last, in the shared wording ([importops.DriftLine])
// the command line prints them in.
//
// A sync is reported by what it did: a sync in step wrote nothing because
// nothing drifted, which is neither a reporting delivery nor an importer that
// drafted nothing.
func renderImportText(p importPayload, res importops.Result, delivery authoredcontent.Delivery, sync bool) string {
	var b strings.Builder
	b.WriteString("read " + p.Source + " as the " + p.Format + " format\n")
	switch {
	case sync && res.Halted():
		b.WriteString("  nothing was written: a sync writes nothing from a draft the importer could not finish\n")
	case sync && len(res.Drift) == 0 && len(p.Written) == 0:
		b.WriteString("  nothing was written: the drafts are in step with the source\n")
	case delivery.Reports() && sync:
		b.WriteString("  nothing was written: each file the sync would write, with its content, is in this result\n")
	case delivery.Reports():
		b.WriteString("  nothing was written: each drafted file's path and content is in this result\n")
	case sync && len(p.Written) == 0 && len(res.Removed) == 0:
		b.WriteString("  nothing was written: the drafts are in step with the source\n")
	case len(p.Written) == 0:
		b.WriteString("  the importer drafted no files, so nothing was written\n")
	default:
		// On this tool the directory the paths are relative to is always the
		// workspace root, so where it is kept back it is named as the served
		// workspace — the root the caller names its own paths against.
		if len(p.Written) > 0 {
			b.WriteString("  wrote " + strings.Join(p.Written, ", ") + ", relative to " +
				workspaceRootWords(p.OutputDir) + "\n")
		}
		if len(res.Removed) > 0 {
			b.WriteString("  removed " + strings.Join(res.Removed, ", ") + ", relative to " +
				workspaceRootWords(p.OutputDir) + "\n")
		}
	}
	for _, d := range p.Diagnostics {
		b.WriteString("  " + d.Severity + ": " + d.Message + "\n")
	}
	for _, d := range res.Decisions {
		b.WriteString("  " + importops.DecisionLine(d) + "\n")
	}
	for _, d := range res.Drift {
		b.WriteString("  " + importops.DriftLine(d) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// importDriftFor projects the parts a sync brought up to date onto this
// surface's shape, in the operation's order, always as an array — and each
// part's keys as an array too, empty for a whole file — so a call that is not a
// sync, or a sync in step, arrives as an empty list rather than a missing field.
func importDriftFor(drift []importops.Drift) []importDrift {
	out := make([]importDrift, 0, len(drift))
	for _, d := range drift {
		out = append(out, importDrift{Change: d.Change, File: d.File, Key: append([]string{}, d.Key...)})
	}
	return out
}

// importWritten projects what reached disk, always as an array so a caller
// reading it as a list finds one — empty under a reporting delivery rather than
// absent.
func importWritten(res importops.Result, delivery authoredcontent.Delivery) []string {
	if delivery.Reports() || res.Written == nil {
		return []string{}
	}
	return res.Written
}

// importDrafts projects the drafted files onto this surface's shape.
func importDrafts(files []abi.ScaffoldFile) []importedDraft {
	out := make([]importedDraft, 0, len(files))
	for _, f := range files {
		out = append(out, importedDraft{Path: f.Path, Content: f.Content})
	}
	return out
}

// importDiagnosticsFor projects what the importer reported onto this surface's
// shape, always as an array so an import that reported nothing arrives as an
// empty list rather than a missing field.
func importDiagnosticsFor(tw *toolWorkspace, diags []abi.Diagnostic) []importDiagnostic {
	out := make([]importDiagnostic, 0, len(diags))
	for _, d := range diags {
		out = append(out, importDiagnostic{Severity: d.Severity, Message: tw.reportedText(d.Message), Code: d.Code})
	}
	return out
}

// importDecisionsFor projects the importer's mapping decisions onto this
// surface's shape, in the importer's order, always as an array so an importer
// that reports none arrives as an empty list rather than a missing field.
//
// Every value is carried as the importer returned it. The three that are prose
// the importer composed — a node's parameter summary, the rationale and the swap
// hint — are told the way a diagnostic's message is, so a caller reaching a
// listener is told no place on this host through them either.
func importDecisionsFor(tw *toolWorkspace, decisions []abi.ImportDecision) []importDecision {
	out := make([]importDecision, 0, len(decisions))
	for _, d := range decisions {
		projected := importDecision{
			SourceNode: importDecisionNode{
				ID:            d.SourceNode.ID,
				Type:          d.SourceNode.Type,
				ParamsSummary: tw.reportedText(d.SourceNode.ParamsSummary),
			},
			Rationale: tw.reportedText(d.Rationale),
			SwapHint:  tw.reportedText(d.SwapHint),
		}
		for _, c := range d.Candidates {
			projected.Candidates = append(projected.Candidates, importCandidate{
				Component: c.ComponentName,
				Score:     c.Score,
				Tier:      c.Tier,
				Signals: importCandidateSignals{
					Table:        c.Signals.Table,
					Category:     c.Signals.Category,
					DomainLabels: c.Signals.DomainLabels,
					Shape:        c.Signals.Shape,
					Name:         c.Signals.Name,
				},
			})
		}
		if d.Chosen != nil {
			projected.Chosen = &importDecisionChoice{Target: d.Chosen.Target, Mode: d.Chosen.Mode, Tier: d.Chosen.Tier}
		}
		out = append(out, projected)
	}
	return out
}
