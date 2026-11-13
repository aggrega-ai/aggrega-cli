// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pipeline

import (
	"fmt"
	"io"
	"sync"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// AdvisoryLog collects every exit-code-neutral advisory a run raises, in the
// order the run raised them, so a caller that reads no terminal receives the
// same findings in the same sequence a terminal shows them.
//
// # Why the order is a property of the log rather than of a later assembly
//
// The advisories are produced at points scattered through the run — one inside
// the resolve scope, one after static analysis, two around the governance
// sockets, one after the render — and a display writes each at the moment it is
// produced. Assembling the list afterwards from per-class slices would mean
// re-deriving that interleaving by hand, and a re-derivation is free to disagree
// with the displays it is supposed to match. So an entry is recorded HERE, beside
// the display that shows it, in the same statement group: there is one append per
// display, which makes the order structural instead of reconstructed.
//
// # Why one log and not a field per class
//
// A second advisory channel beside the first would leave every consuming surface
// deciding which one to read, and the two would disagree the first time an
// advisory moved between them. One log carries every class, and a consumer that
// wants to distinguish them reads each entry's own severity and code.
//
// A nil *AdvisoryLog records nothing and is safe to use: a caller that drives a
// phase directly and wants no collection leaves the field unset, and the
// displays are unaffected either way.
//
// The log is safe for concurrent use: a plugin's bytes can be resolved — and the
// advisory a resolution raises recorded — from wherever the run resolves one.
type AdvisoryLog struct {
	mu      sync.Mutex
	entries []renderer.Diagnostic
	// mentioned holds the keys of the advisories this run raises at most once,
	// however many times their condition is met (see firstMention).
	mentioned map[string]struct{}
}

// record appends one advisory. A nil receiver is a no-op, so a phase invoked
// without a log neither collects nor panics.
func (l *AdvisoryLog) record(d renderer.Diagnostic) {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, d)
}

// firstMention reports whether this is the first time the run raises the
// advisory keyed by key, and remembers that it has. An advisory whose condition
// is met over and over within one run — a plugin resolved without a recorded
// digest is resolved again for every node rendered with it — is displayed and
// recorded the first time only. A nil receiver remembers nothing, so it reports
// every mention as the first.
func (l *AdvisoryLog) firstMention(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.mentioned == nil {
		l.mentioned = map[string]struct{}{}
	}
	if _, seen := l.mentioned[key]; seen {
		return false
	}
	l.mentioned[key] = struct{}{}
	return true
}

// Entries returns the advisories recorded so far, in the order they were
// raised. The slice is a copy, so a consumer holding it cannot be surprised by a
// later append writing into the array behind it — the log outlives some of its
// readers.
func (l *AdvisoryLog) Entries() []renderer.Diagnostic {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.entries) == 0 {
		return nil
	}
	out := make([]renderer.Diagnostic, len(l.entries))
	copy(out, l.entries)
	return out
}

// WarningLine is the line a display shows for one warning-tier advisory: the
// indented "warning:" label and the entry as the one formatter every surface
// displaying this channel reads renders it ([renderer.RenderDiagnosticText]). It
// carries no line break, so a caller holding a list of lines appends it as one.
func WarningLine(d renderer.Diagnostic) string {
	return "  warning: " + renderer.RenderDiagnosticText(d)
}

// display is where the run's displays write: the writer the caller supplied on
// [Inputs.Display], or nowhere.
func (in Inputs) display() io.Writer {
	if in.Display == nil {
		return io.Discard
	}
	return in.Display
}

// OperationDisplay is the display an operation hands every run it makes, so an
// advisory a run shows once per run is shown once per operation. A command that
// fills its lockfile and then renders, a pull that draws its graph and then
// renders, a long-running process answering request after request — each makes
// several runs, and a run cannot know what the runs before it showed; the
// operation can. It is a writer like any other: every line a run writes passes
// through to the writer it wraps unchanged, and only the advisories a run shows
// once (an unrecorded plugin identity, a catalog view's advisory) consult its
// memory. Each run still records those advisories on its own advisory log, so a
// surface reading a run's advisories reads all of them whatever was shown.
//
// Its memory lives as long as the value, so its owner decides what "once" spans:
// a command builds one for its own invocation, a long-running process one for its
// life. A nil *OperationDisplay writes nowhere and remembers nothing.
type OperationDisplay struct {
	w     io.Writer
	mu    sync.Mutex
	shown map[string]struct{}
}

// NewOperationDisplay returns the operation display writing to w; a nil w writes
// nowhere.
func NewOperationDisplay(w io.Writer) *OperationDisplay {
	return &OperationDisplay{w: w, shown: map[string]struct{}{}}
}

// Write passes p to the wrapped writer.
func (d *OperationDisplay) Write(p []byte) (int, error) {
	if d == nil || d.w == nil {
		return len(p), nil
	}
	return d.w.Write(p)
}

// firstShowing reports whether the advisory keyed by key has not yet been shown
// through this display, and remembers that it now has. A nil display reports
// every showing as the first.
func (d *OperationDisplay) firstShowing(key string) bool {
	if d == nil {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, seen := d.shown[key]; seen {
		return false
	}
	d.shown[key] = struct{}{}
	return true
}

// showOnce writes line to the run's display, unless the run was handed an
// [OperationDisplay] that has already shown the advisory keyed by key — so the
// advisory reaches a reader once per operation however many runs the operation
// makes. A plain writer has no memory beyond the run, so it is written to.
func (in Inputs) showOnce(key, line string) {
	if d, ok := in.Display.(*OperationDisplay); ok && !d.firstShowing(key) {
		return
	}
	fmt.Fprintln(in.display(), line)
}

// unrecordedPluginKey and catalogAdvisoryKey name the advisories a run shows
// once, in the one key space a run's advisory log and an operation display share.
func unrecordedPluginKey(uri string) string    { return "unrecorded-plugin\x00" + uri }
func catalogAdvisoryKey(message string) string { return "catalog-advisory\x00" + message }

// reportUnrecordedPlugin is the run's receiver for the advisory a plugin
// resolution raises when no recorded digest governs the bytes it resolved. The
// advisory joins the run's one advisory channel beside its display, so a surface
// that reads no terminal — an editor's diagnostics among them — receives it with
// the run's other advisories. A render resolves the same plugin for every node it
// renders, so it is recorded once per reference per run; each run records it
// afresh, so a surface replacing what it shows with each run's advisories keeps
// showing it for as long as a run resolves the plugin unverified, and stops once
// none does. It is displayed once per operation where the run was handed an
// [OperationDisplay], since an operation making several runs is still one
// account to its reader.
func (in Inputs) reportUnrecordedPlugin(a pluginpath.UnrecordedIdentity) {
	key := unrecordedPluginKey(a.URI)
	if !in.AdvisoryLog.firstMention(key) {
		return
	}
	d := renderer.Diagnostic{Severity: abi.SeverityWarn, Message: a.Message(), SourceRef: a.URI}
	in.showOnce(key, WarningLine(d))
	in.AdvisoryLog.record(d)
}

// advisoryFromResolverFinding renders one resolver-class finding as a structured
// advisory at the supplied severity.
//
// # The two wordings, and why the entry carries the subject-free one
//
// A resolver finding has two renderings that differ only in whether the subject
// — the entity reference and the locator — is spelled inside the sentence. A
// terminal has nowhere else to put the subject, so it shows the rendering that
// carries it. A surface that draws the finding ON its authoring key already shows
// the file, the position and the highlighted token, so it shows the rendering
// without it. Both come from one composer in the resolver, so the two cannot
// drift into two spellings of one condition.
//
// The entry carries the subject-free rendering and the subject STRUCTURALLY —
// the entity on SourceRef, the locator on the location's key path — which is what
// lets a consumer that positions the entry reproduce the subject-bearing line if
// it wants one, and lets a consumer that cannot position it fall back without
// having to unpick a composed sentence.
//
// # Why the location carries a key path and deliberately no instance or uri
//
// The locator is a path into the authoring document, not a resolved line: the
// position is recovered by the surface that holds the parsed document, which is
// where that capability lives. Carrying the locator rather than a line is what
// keeps the recovery there and stops this function inventing a position it cannot
// know.
//
// The instance, the uri and the kind are left empty ON PURPOSE. The shared
// formatter every display reads prints a location's instance — or, on a location
// naming none, its uri — as the node the advisory concerns, and its kind as a
// provider; this finding concerns a document rather than an instance of the
// build. With all three empty the formatter renders the message alone, which is
// exactly the line a positioning surface wants.
func advisoryFromResolverFinding(e *resolver.ResolverError, severity string) renderer.Diagnostic {
	code, _ := cerrors.CodeOf(e)
	d := renderer.Diagnostic{
		Severity:  severity,
		Message:   e.MessageWithoutSubject(),
		Code:      code,
		SourceRef: e.Entity,
	}
	if e.Field != "" {
		d.Location = &renderer.DiagnosticLocation{KeyPath: e.Field}
	}
	return d
}

// advisoryFromPolicyVerdict renders one non-halting governance verdict as a
// structured advisory, keeping everything the guest set and replacing only the
// message with the attributed line the display shows.
//
// # Why this class carries its attribution INSIDE the message
//
// A render advisory carries its attribution structurally — the node and the
// provider live on its location — so a display composes the attributed line and a
// consumer reads the parts. A governance verdict's attribution is the identity of
// the PLUGIN that raised it, and the shared diagnostic shape has no field for an
// emitter: there is no location, code or reference field whose meaning is "who
// reported this". So the only way the attribution reaches a structured consumer
// at all is inside the message, which is also exactly the line the display shows.
// The asymmetry with the render class is therefore a property of the wire shape
// rather than an inconsistency to tidy away.
//
// Everything else the guest set survives — the severity it chose, its code, rule
// identifier, classification, source reference, suggestion, and its LOCATION.
// That last is the point: a guest that located its finding keeps that position,
// where flattening the verdict to a sentence would have destroyed it.
func advisoryFromPolicyVerdict(d renderer.Diagnostic, attributedMessage string) renderer.Diagnostic {
	d.Message = attributedMessage
	return d
}
