// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
// Source names the foreign description to convert, relative to the workspace
// root or by absolute path, as every other path argument here is named. Format
// names the source format outright; leaving it out recognises the format from
// the source's own top-level shape. DryRun does everything the plain request
// does except write, which is a property of the caller rather than of this
// surface, so it is a request field rather than a second tool and it defaults
// to writing — the operation's product is a set of files.
//
// 🔴 There is deliberately NO argument naming the directory the drafts land in,
// and its absence is a design choice rather than a bound this surface could not
// lift. The workspace a call resolves is already settled before the importer
// runs, a path reaching outside it is already refused, and a second way to say
// where files go would only add a way to get the destination wrong. A caller
// that wants to choose a destination has the command's own form for it.
type importArgs struct {
	Source string `json:"source"`

	// Format is a pointer so that a value a caller sent is distinguishable from
	// one it left out. Absent means the format is recognised from the source's
	// own shape, which is this tool's default and not a failure; a value the
	// caller wrote and left blank is refused, because detecting instead would be
	// an argument quietly substituted — and an argument quietly substituted
	// reads to its sender exactly like one that was honoured. A plain string
	// would make those two the same bytes at the point they have to be told
	// apart.
	Format *string `json:"format"`

	DryRun        bool   `json:"dry_run"`
	WorkspaceRoot string `json:"workspace_root"`
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
// root of the workspace the call resolved, because that root is the destination
// this surface hands the layer, so it names nothing WorkspaceRoot does not
// already name, on either transport. It is carried all the same because it is
// the one fact the layer reports about where an import went and every other
// surface calling the layer hands it on: a caller reading it learns where the
// drafts are, and no argument here lets it choose.
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
type importPayload struct {
	WorkspaceRoot         *string `json:"workspace_root"`
	WorkspaceRootWithheld bool    `json:"workspace_root_withheld,omitempty"`
	OutputDir             *string `json:"output_dir"`
	OutputDirWithheld     bool    `json:"output_dir_withheld,omitempty"`

	Source      string             `json:"source"`
	Format      string             `json:"format"`
	Written     []string           `json:"written"`
	Drafts      []importedDraft    `json:"drafts,omitempty"`
	Diagnostics []importDiagnostic `json:"diagnostics"`
	Halted      bool               `json:"halted"`
}

var importToolSpec = toolSpec{
	summary: "Turns a foreign source description into draft Aggrega authoring files, written into the workspace.",
	description: "Turn a foreign source description — a flow or a service definition exported by another tool — " +
		"into draft Aggrega authoring files, WRITTEN into the workspace this server serves. Pass `source`, the " +
		"description to convert. The importer attached to this workspace does the translation: it reads the " +
		"source, drafts entities from it, and this writes what it produced, creating parent directories as " +
		"needed. A drafted file lands at a path relative to the workspace root, which is also the coordinate " +
		"the other tools here report a file under, and THERE IS NO ARGUMENT NAMING A DESTINATION — the drafts " +
		"go into the workspace the call resolved, and nowhere else. The answer names that directory as " +
		"`output_dir`, the one every drafted path is relative to, whether the file was written or handed back: " +
		"it reports where the drafts went and chooses nothing. The workspace must attach an importer that " +
		"advertises the source's format, or the call is refused naming the capability no plugin advertises; " +
		"nothing here ships one. Leave `format` out and the format is recognised from the source's own " +
		"top-level shape, reading that one file and nothing else — no other file, no network — and a shape " +
		"that is not recognised is refused asking for `format` rather than guessed at. Send `dry_run` to be " +
		"handed each drafted file's path and content instead, with nothing created: every other behaviour is " +
		"the write's, the same importer over the same source reporting the same diagnostics, so a source the " +
		"write would refuse is refused under it too. What the importer could not translate comes back as " +
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
      "description": "The foreign description to convert, relative to the workspace root or absolute. It must lie inside the workspace."
    },
    "format": {
      "type": "string",
      "minLength": 1,
      "description": "The source format, named outright — for example langflow or flowise. Leave it out and the format is recognised from the source's own top-level shape."
    },
    "dry_run": {
      "type": "boolean",
      "description": "Do everything except write, and hand back each drafted file's path and content. Every other behaviour is the write's, so a source the write would refuse is refused under it too."
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
				"or leave 'format' out altogether and it is recognised from the source's own top-level shape; " +
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

	// The source is resolved here, through the one helper, before it is opened.
	// That helper is also what decides how far a path a caller named may reach:
	// a client that launched this process names a location on the machine it is
	// sitting at, and a caller reaching a listener names its subject inside the
	// one workspace this process serves.
	path, refusal := tw.workspacePath(importToolName, "source", source)
	if refusal != "" {
		return errorResult(refusal)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		// The resolved location is named rather than the value as sent: a relative
		// source resolves against the workspace root, and a reader looking for the
		// file needs the place that was actually opened.
		return tw.answer(errorResult(explainToolError("Could not read the source at "+path, err, "")))
	}

	// The format is selected before the attachment inputs because the with-config
	// is consumer-scoped to the importer serving THIS format: only the matching
	// attachment's inputs are threaded, never another attachment's.
	format, err := importops.SelectFormat(named, source, content)
	if err != nil {
		return errorResult(importFormatRefusal(err))
	}
	sources, settings, err := importops.AttachmentInputs(tw.rw, format)
	if err != nil {
		return errorResult(explainToolError("Could not read the inputs this workspace threads into its importer.",
			err, ""))
	}

	delivery := authoredcontent.DeliveryFor(a.DryRun)
	res, err := importops.Import(ctx, tw.rw, importops.Options{
		Source:     content,
		SourceName: source,
		Format:     format,
		// The destination, and the whole of what this surface decides about it.
		// It is the workspace the call resolved rather than a directory anything
		// sent, which is why no argument names one.
		OutputDir:     tw.root,
		Delivery:      delivery,
		ResolvePlugin: s.resolveImportPlugin,
		Sources:       sources,
		Settings:      settings,
	})
	if err != nil {
		return errorResult(explainDiagnostic(importRefusal(source, err), ""))
	}

	payload := importPayload{
		Source:      source,
		Format:      res.Format,
		Written:     importWritten(res, delivery),
		Diagnostics: importDiagnosticsFor(tw, res.Diagnostics),
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
	// reached disk, the drafts, and every diagnostic are in the payload beside
	// the sentence.
	if halt := res.Refusal(); halt != nil {
		return tw.answer(structuredErrorResult(explainDiagnostic(importHaltedText(payload, delivery, halt), ""), payload))
	}
	return tw.answer(textResult(renderImportText(payload, delivery), payload))
}

// importFormatRefusal composes this surface's sentence for a source whose format
// could not be settled.
//
// The unrecognised-shape condition is recognised through its own type rather
// than by reading the shipped sentence, because that sentence names the flag a
// reader at a terminal passes and a caller here has no flag to pass. Matching on
// the type is what keeps the two in step: a surface comparing the wording would
// keep compiling and silently stop matching the first time either moved. Every
// other failure to settle a format is carried as it arrived.
//
// It names no format of its own. Which shapes are recognised is the detection's
// own statement and the argument's schema is where this surface carries it, so
// repeating a pair of format names in a refusal would be a second copy that
// stops being true the first time the recognised set moves.
func importFormatRefusal(err error) string {
	var undetected *importops.UndetectedFormatError
	if errors.As(err, &undetected) {
		return fmt.Sprintf("%s did not recognise the source format of %s from the file's own top-level shape, "+
			"and it does not guess: send 'format' naming the format outright and call again. Nothing was read "+
			"beyond that one file and nothing was written.", importToolName, undetected.Path())
	}
	return explainToolError("Could not settle which format to read the source as.", err, "")
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
func importHaltedText(p importPayload, delivery authoredcontent.Delivery, halt error) string {
	var b strings.Builder
	if short, ok := cerrors.ShortCodeOf(halt); ok {
		b.WriteString(short)
		b.WriteString(": ")
	}
	b.WriteString("the importer could not translate the whole of ")
	b.WriteString(p.Source)
	b.WriteString(", so what it drafted is partial. The diagnostics below name what it could not translate.\n\n")
	b.WriteString(renderImportText(p, delivery))
	return b.String()
}

// renderImportText renders the human-readable answer: how the source was read,
// what reached disk, and everything the importer reported about the source.
//
// The three write outcomes are reported differently on purpose. A write names
// the files that reached disk and the root they are relative to, because a
// drafted path carries no anchor of its own. A reporting delivery wrote nothing
// by design and says so, rather than reading as the third case. An import that
// drafted nothing at all is the third, and saying nothing there would leave it
// indistinguishable from a write that failed quietly.
func renderImportText(p importPayload, delivery authoredcontent.Delivery) string {
	var b strings.Builder
	b.WriteString("read " + p.Source + " as the " + p.Format + " format\n")
	switch {
	case delivery.Reports():
		b.WriteString("  nothing was written: each drafted file's path and content is in this result\n")
	case len(p.Written) == 0:
		b.WriteString("  the importer drafted no files, so nothing was written\n")
	default:
		// On this tool the directory the paths are relative to is always the
		// workspace root, so where it is kept back it is named as the served
		// workspace — the root the caller names its own paths against.
		b.WriteString("  wrote " + strings.Join(p.Written, ", ") + ", relative to " +
			workspaceRootWords(p.OutputDir) + "\n")
	}
	for _, d := range p.Diagnostics {
		b.WriteString("  " + d.Severity + ": " + d.Message + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
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
