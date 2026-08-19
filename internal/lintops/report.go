// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package lintops

import "github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

// FindingLocation is where a lint finding points.
//
// File is always populated: the operation knows which entity file it is linting
// whether or not the rule that produced the finding located the issue inside it.
// Every other field is populated only when the producing rule supplied it, so a
// consumer distinguishes "this file, position unknown" from "this file, this
// line" instead of reading a fabricated zero as a real position.
type FindingLocation struct {
	File      string `json:"file"`
	Line      int    `json:"line,omitempty"`
	Column    int    `json:"column,omitempty"`
	EndLine   int    `json:"endLine,omitempty"`
	EndColumn int    `json:"endColumn,omitempty"`
	// KeyPath is a dotted path to the offending element within the entity
	// document, when the producing rule located the issue structurally.
	KeyPath string `json:"keyPath,omitempty"`
}

// Finding is one lint finding in the shape every surface renders it in — the
// command line, the tool interface, and any later request/response surface —
// so a finding has one field vocabulary rather than one per surface.
//
// Message is the producing rule's diagnostic text carried through byte for byte.
// It is never re-worded, truncated, or re-wrapped on the way here: a surface
// that renders it renders what the rule said, so two surfaces reporting the same
// finding cannot describe it differently.
type Finding struct {
	Severity string `json:"severity"`
	Message  string `json:"message"`
	// RuleID names the rule that produced the finding.
	RuleID string `json:"ruleId,omitempty"`
	// Code is a stable machine-matchable code, independent of Message, when the
	// producing rule assigns one.
	Code string `json:"code,omitempty"`
	// Classification is the version-impact verdict a finding carries when its
	// rule assigns one.
	Classification string `json:"classification,omitempty"`
	// Suggest is a remediation hint, when the producing rule offers one.
	Suggest  string           `json:"suggest,omitempty"`
	Location *FindingLocation `json:"location,omitempty"`
}

// Report is the structured result of a lint operation: every finding the run
// produced, in the order the rules produced them, plus the counts a caller would
// otherwise recompute by walking the list.
//
// Findings is never nil on the wire — an empty run emits an empty list rather
// than null, so a consumer iterates unconditionally.
type Report struct {
	Findings []Finding     `json:"findings"`
	Summary  ReportSummary `json:"summary"`
}

// ReportSummary counts a run's findings by severity. EntitiesChecked is the
// number of entity files the run covered: one for a single-entity run, and the
// resolved file set's size for a whole-catalog or Release-closure run.
//
// There is one count per severity the shared diagnostic contract declares, so
// Errors, Warnings and Infos sum to Findings for any run. A summary carrying a
// count per severity is the only shape that cannot report a finding as
// uncounted: a summary that named a subset of the severities would leave the
// findings carrying the rest visible in the list and invisible in the counts,
// and the gap between the two would be silent.
type ReportSummary struct {
	EntitiesChecked int `json:"entitiesChecked"`
	Findings        int `json:"findings"`
	// Errors counts the findings at the halting severity, Warnings those at the
	// advisory severity that surfaces alongside a completed run, and Infos those
	// at the severity that records an observation without affecting the outcome.
	Errors   int `json:"errors"`
	Warnings int `json:"warnings"`
	Infos    int `json:"infos"`
}

// FindingFrom projects one plugin diagnostic onto the shared finding shape,
// attaching file as the location's file.
//
// This is the one mapping home. Severity and message are copied verbatim; the
// finer-grained position is copied only where the diagnostic actually carried
// one, since a rule that located nothing must not be reported as having located
// line zero.
func FindingFrom(d abi.Diagnostic, file string) Finding {
	f := Finding{
		Severity:       d.Severity,
		Message:        d.Message,
		RuleID:         d.RuleID,
		Code:           d.Code,
		Classification: d.Classification,
		Suggest:        d.Suggest,
		Location:       &FindingLocation{File: file},
	}
	if d.Location != nil {
		f.Location.KeyPath = d.Location.KeyPath
		if s := d.Location.Span; s != nil {
			f.Location.Line = s.StartLine
			f.Location.Column = s.StartColumn
			f.Location.EndLine = s.EndLine
			f.Location.EndColumn = s.EndColumn
		} else if d.Location.Line != 0 {
			// A rule that reports a document line without a byte span still
			// locates the finding to a line; carry it rather than dropping it.
			f.Location.Line = d.Location.Line
		}
	}
	return f
}

// FindingsFrom projects a run's diagnostics onto the shared shape, all attached
// to the same entity file, preserving order.
func FindingsFrom(ds []abi.Diagnostic, file string) []Finding {
	out := make([]Finding, 0, len(ds))
	for _, d := range ds {
		out = append(out, FindingFrom(d, file))
	}
	return out
}

// FindingCollector gathers a run's findings as data while the run also writes
// its human report, so the two renderings describe one set of findings rather
// than two independently assembled ones.
//
// A caller that wants only the human report passes no collector; a nil
// *FindingCollector is inert at every call site, which is what keeps collection
// additive. Reached reports whether the run got as far as reporting at all: an
// operation that fails before any rule runs — an unreadable entity, an
// unresolvable linter plugin — leaves the collector untouched, and a caller
// distinguishes that from a clean run with no findings instead of emitting an
// empty report over a run that never happened.
type FindingCollector struct {
	findings []Finding
	entities int
	reached  bool
}

// Add appends the diagnostics one entity file produced, attributing every one to
// that file. It is safe on a nil receiver.
func (c *FindingCollector) Add(ds []abi.Diagnostic, file string) {
	if c == nil {
		return
	}
	c.reached = true
	c.findings = append(c.findings, FindingsFrom(ds, file)...)
}

// Entities records how many entity files the run covered. It is safe on a nil
// receiver.
func (c *FindingCollector) Entities(n int) {
	if c == nil {
		return
	}
	c.reached = true
	c.entities = n
}

// Reached reports whether the run reached the point of reporting findings.
func (c *FindingCollector) Reached() bool {
	return c != nil && c.reached
}

// Report assembles what the collector gathered.
func (c *FindingCollector) Report() Report {
	if c == nil {
		return NewReport(nil, 0)
	}
	return NewReport(c.findings, c.entities)
}

// NewReport assembles a report over findings gathered across entitiesChecked
// entity files, deriving the severity counts from the findings themselves so the
// summary cannot disagree with the list it summarizes.
//
// Every severity the shared diagnostic contract declares has its own arm, so a
// finding always increments exactly one count. There is no arm for anything
// else, and none is needed: a finding's severity arrives either from a guest
// output the socket boundary already checked against the contract (see
// [github.com/aggrega-ai/aggrega-cli/internal/wasm.GuestDiagnosticSeverities])
// or from this package's own re-severitizing, which writes the declared consts.
func NewReport(findings []Finding, entitiesChecked int) Report {
	if findings == nil {
		findings = []Finding{}
	}
	r := Report{
		Findings: findings,
		Summary: ReportSummary{
			EntitiesChecked: entitiesChecked,
			Findings:        len(findings),
		},
	}
	for _, f := range findings {
		switch f.Severity {
		case abi.SeverityHalt:
			r.Summary.Errors++
		case abi.SeverityWarn:
			r.Summary.Warnings++
		case abi.SeverityInfo:
			r.Summary.Infos++
		}
	}
	return r
}
