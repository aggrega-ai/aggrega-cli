// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package serve

import (
	"context"
	"io"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/lintops"
	"github.com/aggrega-ai/aggrega-cli/pkg/entitykind"
	"github.com/aggrega-ai/aggrega-cli/pkg/pathscope"
)

// conventionDiagnostic projects one publishing-conventions finding onto the
// editor wire's diagnostic shape.
//
// The mapping reuses the shape this surface already publishes rather than
// introducing a second one, so a client reads one kind of entry however the
// finding was produced, and Source is what says which question the entry answers.
//
// Code carries the finding's identity: the rule that produced it, or, for a
// finding that carries no rule identity but does carry a code of its own, that
// code. That is the key's own contract on this wire — one key answering "what is
// this, in terms a machine can match" — and the code stands in for an absent rule
// because the key is about what the finding is and not about who named it;
// carrying both would be two keys for one question. The identity is read through
// [lintops.Finding.Identity], which the command line's row reads too, so a
// terminal and an editor name one finding by one identifier.
//
// The entry's other two identifier keys follow from the condition the rule
// names, which [conventionCodes] decides; see ConditionCodes, where that division
// is stated for every layer.
//
// Position defaults to the head of the file, which is where a finding whose rule
// located nothing belongs: the rules run over one entity document, so the file is
// always known while a line is known only when the rule computed one. That is the
// same fallback the render advisories use, so a client's handling of a positionless
// entry does not depend on which layer produced it. The end position is carried
// only alongside a start, since a span with no start locates nothing.
//
// The remedy, the version-impact verdict and the rule's structured coordinate are
// carried through rather than left behind. Each is a fact the rule computed and
// none of them is recoverable from the message, so a surface that dropped them
// would answer a narrower question than the one the rules answered — and the
// remedy is the part a reader acts on, so its absence costs the reader a second
// diagnosis of a finding this surface had already explained.
func conventionDiagnostic(f lintops.Finding) Diagnostic {
	d := Diagnostic{
		Line:           1,
		Column:         1,
		Severity:       editorSeverity(f.Severity),
		Message:        f.Message,
		ConditionCodes: conventionCodes(f),
		Classification: f.Classification,
		Suggest:        f.Suggest,
	}
	if f.Location != nil {
		d.KeyPath = f.Location.KeyPath
		if f.Location.Line > 0 {
			d.Line = f.Location.Line
			if f.Location.Column > 0 {
				d.Column = f.Location.Column
			}
			d.EndLine = f.Location.EndLine
			d.EndColumn = f.Location.EndColumn
		}
	}
	return d
}

// conventionCodes returns the identifiers one conventions finding is published
// under.
//
// A rule the compiler declares is registered as a condition under the rule's own
// identity, so its finding names a registered condition like any other entry
// does and carries all three keys: the identity, the condition's short code —
// the one the command line prints at the head of the same finding's row — and
// the reference to where every condition is listed. They are decided together by
// [conditionCodesOf], which is what keeps the reference present exactly when the
// short code is.
//
// Which condition a rule names is read through the lint operation's own lookup
// rather than through a table here. The command line reads the same lookup to
// print the row, so the two surfaces cannot name different conditions for one
// finding.
//
// A rule no condition names — one a linter attached by the workspace reports
// from a rule set of its own — keeps its identity as a class code and nothing
// else, and so does a finding carrying no rule identity at all, which is named by
// its own code. Both are the finding's identity as [lintops.Finding.Identity]
// answers it, the identifier the command line's row prints in the same place,
// and the command line prints no short code for either. A rule the compiler
// declares needs no separate reading: its condition is registered under the
// rule's identity, so the code [conditionCodesOf] reads off it is that identity.
func conventionCodes(f lintops.Finding) ConditionCodes {
	if condition, ok := lintops.ConditionForRule(f.RuleID); ok {
		return conditionCodesOf(condition)
	}
	return classCode(f.Identity())
}

// conventionDiagnosticsFor projects the findings attributed to one file,
// preserving the order the rules produced them. It returns nil for an empty set,
// which is what the store reads as "this file carries nothing".
func conventionDiagnosticsFor(findings []lintops.Finding, file string) []Diagnostic {
	var out []Diagnostic
	for _, f := range findings {
		if f.Location == nil || f.Location.File != file {
			continue
		}
		out = append(out, conventionDiagnostic(f))
	}
	return out
}

// conventionsForBuffer runs the publishing-conventions rules over the buffer the
// request carried and returns what they found, or nil where the check does not
// apply to that document at all.
//
// It lints the BUFFER rather than the file on disk, which is what makes it a
// per-edit answer: an author who corrects a name stops being told about it on the
// next validation rather than on the next save. The path is still what the rules
// are told about, because a rule comparing an entity's identity against the
// directory it sits in is asking about the path and not about the bytes.
//
// Only documents the conventions rules describe are linted, read from the
// entity-kind vocabulary rather than from a list here — so a kind's coverage is a
// property of that vocabulary rather than of anything this file remembers.
// Foreign YAML, an Aggregate, a Release and a Manifest are all passed over, which
// is why an editor session reports nothing about them: the conventions are about
// what gets published to a catalog.
//
// A run that never reached the point of reporting — an unresolvable linter
// plugin, a document the rules could not be given at all — returns nothing rather
// than a verdict, and never fails the validation carrying it. The check is an
// additional layer, so a workspace that cannot run it is answered exactly as a
// workspace with the dial off is, which is what keeps a validation's own verdict
// independent of it. The collector's own reached-or-not is the discriminator,
// because it distinguishes "the rules ran and found nothing" from "the rules
// never ran" without matching an error's prose.
//
// Nothing here reaches a registry. Only the mode that reads local content runs,
// and the mode that compares an entity against its previously published version
// is unreachable from this path by construction: a check running on every edit is
// exactly where a registry must not be reached, and a caller wanting that
// comparison asks for it by name through aggrega/lint.
//
// ctx is the validation's own, so a client that withdrew a validation stops the
// rules this layer runs rather than only discarding what they said. That matters
// here more than on the explicit request, not less: this layer runs on every
// edit, so it is the path most likely to be in flight when a keystroke
// supersedes it.
func (s *Server) conventionsForBuffer(ctx context.Context, file string, content []byte, doc *yaml.Node) []Diagnostic {
	if s.resolvedWorkspace == nil {
		// Nothing to lint against. Production always resolves a workspace at
		// startup; a Server built as a struct literal is the in-memory harness, and
		// resolving one here would lint whatever directory the process happens to
		// sit in rather than the workspace the session was opened over.
		return nil
	}
	_, kind, ok := peekDocumentHeader(doc)
	if !ok || !entitykind.IsConventionsLinted(entitykind.Kind(kind)) {
		return nil
	}
	collect := &lintops.FindingCollector{}
	// The verdict is deliberately dropped. In this mode every finding is advisory,
	// so a run reports findings rather than refusing, and what a validation owes
	// its caller is the findings either way.
	_ = lintops.RunEntity(ctx, lintops.Config{
		EntityPath:    file,
		ProposedBytes: content,
		Workspace:     s.resolvedWorkspace,
		Out:           io.Discard,
		DiagOut:       io.Discard,
		Collect:       collect,
	})
	if !collect.Reached() {
		return nil
	}
	return conventionDiagnosticsFor(collect.Report().Findings, file)
}

// refreshConventionDiagnostics replaces the publishing-conventions layer for one
// file with what a validation of it found, or clears the layer when the dial is
// off.
//
// Clearing on the off position is what makes the default an absence rather than a
// memory: a session whose dial is turned off reports nothing on its next
// validation, instead of leaving whatever an earlier on position published
// standing in the editor with nothing to refresh it.
func (s *Server) refreshConventionDiagnostics(ctx context.Context, file string, content []byte, doc *yaml.Node) {
	if !s.lintOnValidate {
		s.setConventionDiagnostics(file, nil)
		return
	}
	s.setConventionDiagnostics(file, s.conventionsForBuffer(ctx, file, content, doc))
}

// lintParams is the request shape for `aggrega/lint`.
//
// Entity and Release select the subject and are each a path relative to the
// workspace root, refused in the same shapes every other document parameter here
// is. Sending neither asks for every catalog entity the configured catalog
// directories hold, which is the third subject the capability covers — once for
// each Release, over that Release's directories, where the workspace gives its
// Releases directories of their own — so one parameter set carries all three
// questions the command line asks, rather than this surface answering a subset
// of them under the same name.
//
// PrePublish compares one entity against its previously published version and
// classifies the proposed version bump against it. It is the mode that consults a
// registry, and it runs here because a request named it: what admits an operation
// that reaches out is that a caller asked for it, never which surface the caller
// reached for. It is meaningful with Entity alone, for the same reason it is on
// the command line — a comparison needs one entity's published history, which a
// whole catalog and a Release closure do not name.
type lintParams struct {
	Entity     string `json:"entity"`
	Release    string `json:"release"`
	PrePublish bool   `json:"prePublish"`
}

// lintFileFindings is one file's publishing-conventions findings.
//
// The file is carried beside the entries rather than on each one because the
// diagnostic shape locates a position inside a document and says nothing about
// which document — the same split aggrega/publishDiagnostics uses, so a client
// that already groups by file for the pushed layer groups this answer the same
// way.
type lintFileFindings struct {
	File        string       `json:"file"`
	Diagnostics []Diagnostic `json:"diagnostics"`
}

// lintResult is the `aggrega/lint` response.
//
// Entity and Release repeat the subject the answer is about, spelled as the
// request spelled it, and both are absent for a whole-catalog run — which is the
// subject that has no coordinate to repeat.
//
// Checked is how many entity documents the run covered, so a clean answer is
// readable as a corpus rather than only as an empty list: no findings over
// forty entities and no findings over none are different answers.
//
// Findings, Errors, Warnings and Infos are the run's own counts, one per severity
// plus the total, derived by the lint operation from the findings it produced and
// passed through here rather than recounted. They are carried for the same reason
// Checked is: a caller reading a corpus-sized answer wants the shape of it before
// it walks the groups, and a caller that only wants the shape need not walk them at
// all. Recounting them at this boundary would be a second derivation that agrees
// with the operation's until one of them is edited.
//
// They are spelled as the lint operation's own summary spells them, so one count
// keeps one name wherever it is read. Checked is the exception and keeps its own
// spelling because it is a key on this wire that a client already binds to.
//
// Files carries the findings grouped by the document each concerns, ordered by
// path, so two runs over one workspace answer in the same order. It is never null.
//
// Halted says whether the run's own verdict refuses publication. It is false for
// every advisory run, which is both modes that read only local content, and can be
// true only where the request asked for the pre-publication comparison. The
// findings are carried either way: a run that refuses is the one whose findings a
// caller most needs to read, and reporting them structurally on success while
// describing them in prose on a refusal would be one capability answering at two
// fidelities depending on its verdict.
//
// A refusing verdict is reported here rather than as an error envelope, because
// it is a verdict about the content and not a failure to answer — the same reading
// aggrega/validate already asks for, where a document carrying error-severity
// diagnostics is a successful response. An error envelope is reserved for a run
// that could not produce a verdict at all.
//
// Passes is carried only for a whole-catalog run in a workspace whose
// configuration gives its Releases catalog directories of their own, where the
// check runs once for each Release over the directories that Release reads. It
// says which Releases each finding was found for. Everything above it then covers
// every pass together, so a client that reads only today's keys still reads the
// whole run, and a client that groups by Release reads the passes instead. Every
// other run omits it.
type lintResult struct {
	Entity   string             `json:"entity,omitempty"`
	Release  string             `json:"release,omitempty"`
	Checked  int                `json:"checked"`
	Findings int                `json:"findings"`
	Errors   int                `json:"errors"`
	Warnings int                `json:"warnings"`
	Infos    int                `json:"infos"`
	Files    []lintFileFindings `json:"files"`
	Halted   bool               `json:"halted"`
	Passes   []lintPass         `json:"passes,omitempty"`
}

// lintPass is one pass of a whole-catalog run in a workspace that gives its
// Releases catalog directories of their own: the Releases it was run for, the
// directories it read, and what it found there, counted and grouped as the
// answer around it counts and groups the whole run.
//
// Releases are spelled relative to the workspace root, the coordinate every
// Release this surface names takes, and Releases reading the same directories
// share one pass. CatalogDirs are spelled by the rule Files follows. An empty
// CatalogDirs is a pass whose Releases read no catalog directory, so it checked
// nothing and failed none of the others. Neither list is ever null.
type lintPass struct {
	Releases    []string           `json:"releases"`
	CatalogDirs []string           `json:"catalogDirs"`
	Checked     int                `json:"checked"`
	Findings    int                `json:"findings"`
	Errors      int                `json:"errors"`
	Warnings    int                `json:"warnings"`
	Infos       int                `json:"infos"`
	Files       []lintFileFindings `json:"files"`
}

// resolveEntityParam turns the `entity` request parameter into the absolute path
// the daemon reads, under the contract every document parameter on this surface
// shares: a path relative to the workspace root, naming a document inside it.
func resolveEntityParam(workspaceRoot, entity string) (string, *jsonRPCError) {
	return resolveWorkspacePathParam("entity", "a catalog entity", "the request named no entity", workspaceRoot, entity, nil)
}

// dispatchLint answers with the publishing-conventions findings for the subject
// the request names: one catalog entity, every entity a Release's dependency
// closure reaches, or every entity under the configured catalog directories.
//
// It is the surface's explicitly-invoked form of the check, and it answers
// whatever the request asks for whether or not the per-validation layer is turned
// on. The two are not the same capability wearing one name: this one is a question
// a caller asked, and the layer is the same rules running where nobody asked by
// name — so the dial gates the layer and never this.
//
// It publishes nothing into the session's diagnostic layers. A request is a
// question about the subject it named, and writing its findings into the editor's
// problem list would replace what a validation published for files this request
// never covered.
//
// Nothing is written to disk under any mode, and nothing is prompted for. The
// pre-publication comparison reaches a registry, and it runs because the request
// asked for it by name — the rule the whole surface runs under is about when an
// operation reaches out, not about what this process happens to link.
func (s *Server) dispatchLint(ctx context.Context, req *jsonRPCRequest) error {
	var params lintParams
	if len(req.Params) > 0 {
		if err := decodeParams(req, &params); err != nil {
			return s.writeError(ctx, req.ID, jsonRPCInvalidParams, "invalid params: "+err.Error())
		}
	}
	entity := strings.TrimSpace(params.Entity)
	release := strings.TrimSpace(params.Release)
	switch {
	case entity != "" && release != "":
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: entity and release name different subjects; send one, or neither to check every "+
				"catalog entity")
	case params.PrePublish && entity == "":
		return s.writeError(ctx, req.ID, jsonRPCInvalidParams,
			"invalid params: prePublish compares one entity against its previously published version, so it "+
				"requires entity")
	}
	if s.resolvedWorkspace == nil {
		return s.writeError(ctx, req.ID, jsonRPCInternalError,
			"lint: the session resolved no workspace, so there is nothing to check")
	}

	collect := &lintops.FindingCollector{}
	var runErr error
	switch {
	case entity != "":
		abs, paramErr := resolveEntityParam(s.workspaceRoot, entity)
		if paramErr != nil {
			return s.writeErrorEnvelope(ctx, req.ID, paramErr)
		}
		runErr = lintops.RunEntity(ctx, lintops.Config{
			EntityPath: abs,
			PrePublish: params.PrePublish,
			Workspace:  s.resolvedWorkspace,
			Out:        io.Discard,
			DiagOut:    io.Discard,
			Collect:    collect,
		})
	case release != "":
		abs, paramErr := resolveReleaseParam(s.workspaceRoot, s.wsConfig, release)
		if paramErr != nil {
			return s.writeErrorEnvelope(ctx, req.ID, paramErr)
		}
		runErr = lintops.RunRelease(ctx, s.resolvedWorkspace, abs, nil, io.Discard, io.Discard, collect)
	default:
		runErr = lintops.RunCatalogWide(ctx, s.resolvedWorkspace, WholeCatalogPasses(s.resolvedWorkspace, nil), io.Discard, io.Discard, collect)
	}
	// The request's own context is what the arms above were given, so by the time
	// this is read the run has already abandoned its remaining work rather than
	// finished it — which is the difference this check used to hide. It stays
	// because the response must still be dropped: a caller that withdrew is not
	// waiting for an answer, and a partial one would misreport its corpus.
	if ctx.Err() != nil {
		return nil
	}
	if runErr != nil && !collect.Reached() {
		// The run produced no verdict at all, so there is nothing to answer with.
		// The collector is the discriminator rather than the error's prose: it is
		// set exactly when the rules got as far as reporting, which is the
		// difference between a refusal and a failure.
		return s.writeError(ctx, req.ID, jsonRPCInternalError, "lint: "+runErr.Error())
	}
	report := collect.Report()
	return s.writeResult(ctx, req.ID, lintResult{
		Entity:   entity,
		Release:  release,
		Checked:  report.Summary.EntitiesChecked,
		Findings: report.Summary.Findings,
		Errors:   report.Summary.Errors,
		Warnings: report.Summary.Warnings,
		Infos:    report.Summary.Infos,
		Files:    s.lintFindingsByFile(report.Findings),
		Halted:   runErr != nil,
		Passes:   s.lintPasses(report.Passes),
	})
}

// lintPasses projects a whole-catalog run's passes onto the answer, each spelled
// and grouped by the rules the answer around it follows. It returns nil for a run
// that carries no passes, which leaves the key out of the answer.
func (s *Server) lintPasses(passes []lintops.PassReport) []lintPass {
	if len(passes) == 0 {
		return nil
	}
	out := make([]lintPass, 0, len(passes))
	for _, p := range passes {
		dirs := make([]string, 0, len(p.CatalogDirs))
		for _, d := range p.CatalogDirs {
			dirs = append(dirs, pathscope.ReportedName(s.workspaceRoot, d))
		}
		out = append(out, lintPass{
			Releases:    append([]string{}, p.Releases...),
			CatalogDirs: dirs,
			Checked:     p.Summary.EntitiesChecked,
			Findings:    p.Summary.Findings,
			Errors:      p.Summary.Errors,
			Warnings:    p.Summary.Warnings,
			Infos:       p.Summary.Infos,
			Files:       s.lintFindingsByFile(p.Findings),
		})
	}
	return out
}

// lintFindingsByFile groups a run's findings by the document each concerns, in
// path order, with each file spelled by the one rule every reported path here
// follows: relative to the workspace root for a document inside it, and the
// absolute path for one outside.
//
// The relative spelling is the one every entity this surface names carries, so a
// client joins a lint answer against the entity index without converting between
// two coordinate systems. A catalog directory need not sit inside the root,
// though, and for a document in one the absolute path is what a reader can open
// and what the rules' own message text already names — so a climb above the root
// would spell one path two ways inside a single entry. It is not the spelling the
// diagnostic stores use for a contained document, and deliberately: those are
// keyed absolute throughout because a pushed notification's file identifier is
// resolved by a client as a filesystem path, whereas this answer names
// author-owned documents the way the rest of the surface addresses them.
//
// A finding whose location names no file is grouped under the empty path rather
// than dropped, so a run that produced one is not answered as a run that produced
// nothing.
func (s *Server) lintFindingsByFile(findings []lintops.Finding) []lintFileFindings {
	byFile := map[string][]Diagnostic{}
	for _, f := range findings {
		file := ""
		if f.Location != nil {
			file = pathscope.ReportedName(s.workspaceRoot, f.Location.File)
		}
		byFile[file] = append(byFile[file], conventionDiagnostic(f))
	}
	files := make([]string, 0, len(byFile))
	for f := range byFile {
		files = append(files, f)
	}
	sort.Strings(files)
	out := make([]lintFileFindings, 0, len(files))
	for _, f := range files {
		// The source is stamped here, at the one place this answer is assembled,
		// for the same reason the stores stamp their own layer: an entry's source
		// is a fact about which layer produced it, and every entry in this answer
		// came from the conventions rules.
		out = append(out, lintFileFindings{
			File:        f,
			Diagnostics: stampSource(byFile[f], DiagnosticSourceConventions),
		})
	}
	return out
}
