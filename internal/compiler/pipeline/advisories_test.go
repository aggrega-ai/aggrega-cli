// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pipeline_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
)

// readFixtureBytes reads a fixture document, failing the test rather than
// returning an empty slice a splice would then be silently inert against.
func readFixtureBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return data
}

// indexOfAdvisory returns the position of the first channel entry whose message
// carries want, or -1.
func indexOfAdvisory(advisories []renderer.Diagnostic, want string) int {
	for i, a := range advisories {
		if strings.Contains(a.Message, want) {
			return i
		}
	}
	return -1
}

// The one advisory channel, proved as a property of what a run RETURNS rather
// than of what it declares.
//
// Two advisory classes are raised by different phases and reported by different
// displays — a binding-lint finding and the static-analysis advisory stream —
// and both reach the same channel on the result. That is what lets a caller with
// no terminal receive either class whichever entry point it drove.
//
// Each assertion below reads the CHANNEL. A test asserting only that a display
// printed something would pass just as happily where the display were the only
// place a finding existed, which is the arrangement these proofs rule out.

const (
	advisoryOverrideAggregatePath = "../../../internal/compiler/testdata/fullstack-app/aggregates/annotation-values-binding-annotation.yaml"
	advisoryOverrideReleasePath   = "../../../internal/compiler/testdata/fullstack-app/releases/annotation-values-binding-annotation.yaml"
	advisoryOverrideManifestPath  = "../../../internal/compiler/testdata/fullstack-app/manifests/annotation-values-binding.yaml"
	advisoryCatalogPath           = "../../../internal/compiler/testdata/fullstack-app/catalog"
)

// authorBothAdvisoryClasses rewrites the fixture so ONE run raises both: a
// declared parameter no reference reads, which the binding lint reports, and an
// annotation contesting an explicitly-authored leaf, which the static-analysis
// advisory stream reports.
//
// Both in one document deliberately. The two are produced by different phases at
// different moments, so a document raising only one cannot show that the channel
// carries them together, in order, from a single run — which is the property a
// caller depends on.
//
// The lint half is an unread parameter rather than a mis-spelled dependency
// socket, and the choice is forced rather than stylistic: a socket needs a target,
// this deployment places exactly one component in a boundary, and a second
// component authored to be the target is a component in no boundary — which halts
// the run before static analysis and takes the other half of the proof with it.
// An unread parameter needs nothing but its own declaration.
//
// The rewrite is in memory: the fixture on disk is shared with other proofs and
// must keep producing exactly what they expect.
func authorBothAdvisoryClasses(t *testing.T, aggregate []byte) []byte {
	t.Helper()
	const authored = "spec:\n" +
		"  subsystems:\n" +
		"    data:\n" +
		"      annotations:\n" +
		"        db-pool: \"values-only-db\"\n" +
		"      components:\n" +
		"        dbclient:\n" +
		"          type: core/compute/web-ui\n"
	const rewritten = "spec:\n" +
		"  parameters:\n" +
		"    unread_probe:\n" +
		"      type: string\n" +
		"  subsystems:\n" +
		"    data:\n" +
		"      features:\n" +
		"        db-pool:\n" +
		"          type: feature.myco/db-pool\n" +
		"          values:\n" +
		"            db_name: \"explicitly-authored-db\"\n" +
		"      components:\n" +
		"        dbclient:\n" +
		"          type: core/compute/web-ui\n" +
		"          annotations:\n" +
		"            db-pool: \"annotation-supplied-db\"\n"
	if !bytes.Contains(aggregate, []byte(authored)) {
		t.Fatalf("fixture no longer carries the authored shape this rewrite anchors on; it would be silently inert")
	}
	out := bytes.Replace(aggregate, []byte(authored), []byte(rewritten), 1)
	if bytes.Equal(out, aggregate) {
		t.Fatal("rewrite changed nothing; neither advisory would be authored")
	}
	return out
}

// tier4OverFixture drives the pre-render entry point over the override fixture
// with the supplied aggregate bytes, returning what it produced and everything
// the run displayed.
//
// It goes through the exported entry point rather than the shared inner core
// because the entry point is what owns the advisory log — a test driving the core
// directly would supply no log, record nothing, and assert over an empty channel
// while every display still printed.
func tier4OverFixture(t *testing.T, aggregate []byte) (*pipeline.Tier4Result, string) {
	t.Helper()
	out, displayed, _ := tier4OverFixtureWithErr(t, aggregate)
	return out, displayed
}

// tier4OverFixtureWithErr is tier4OverFixture plus the run's error, for a caller
// that needs to distinguish "the phase reported nothing" from "the phase never
// ran". Without that distinction an absence assertion over a run that halted
// early reads as a finding about the phase.
func tier4OverFixtureWithErr(t *testing.T, aggregate []byte) (*pipeline.Tier4Result, string, error) {
	t.Helper()
	rel, err := resolver.LoadRelease(advisoryOverrideReleasePath)
	if err != nil {
		t.Fatalf("load release: %v", err)
	}
	manifest, err := resolver.LoadManifest(advisoryOverrideManifestPath)
	if err != nil {
		t.Fatalf("load manifest: %v", err)
	}
	cat, err := catalog.LoadAll("", advisoryCatalogPath)
	if err != nil {
		t.Fatalf("catalog load: %v", err)
	}
	if extErrs := cat.ResolveAllExtends(); len(extErrs) > 0 {
		t.Fatalf("catalog extends: %d error(s); first: %v", len(extErrs), extErrs[0])
	}

	var out *pipeline.Tier4Result
	var runErr error
	displayed := captureStderr(t, func() {
		out, runErr = pipeline.RunTier4Pipeline(context.Background(), pipeline.Inputs{
			Release:           rel,
			Manifest:          manifest,
			AggregateRawBytes: aggregate,
			AggregatePath:     advisoryOverrideAggregatePath,
			WorkspaceConfig:   fixtureWorkspaceConfig(),
			CompilerVersion:   fixtureCompilerVersion,
		}, wiredLookup(cat, manifest), cat.RecipeLookupWithManifest(manifest).Recipe, nil)
	})
	if out == nil {
		t.Fatal("the run returned no result, so the channel cannot be read")
	}
	return out, displayed, runErr
}

// advisoryNaming returns the channel entries whose message carries want.
func advisoryNaming(advisories []renderer.Diagnostic, want string) []renderer.Diagnostic {
	var found []renderer.Diagnostic
	for _, a := range advisories {
		if strings.Contains(a.Message, want) {
			found = append(found, a)
		}
	}
	return found
}

// TestBothAdvisoryClassesTravelOnTheOneChannel is the proof that the two classes
// a caller with no terminal previously could not receive both arrive on one
// channel from one run.
//
// The binding-lint half additionally asserts the entry carries a LOCATOR. That is
// the part a presence assertion cannot reach: an entry that arrived carrying no
// path is an entry every positioning surface must place at the head of the file,
// which is the fidelity loss this channel exists not to introduce.
func TestBothAdvisoryClassesTravelOnTheOneChannel(t *testing.T) {
	aggregate := readFixtureBytes(t, advisoryOverrideAggregatePath)
	out, displayed, runErr := tier4OverFixtureWithErr(t, authorBothAdvisoryClasses(t, aggregate))

	// A run that halted before static analysis reports no advisory from it, and an
	// absence assertion over such a run would read as a finding about the phase
	// rather than about the fixture. So the premise is stated first.
	if runErr != nil {
		t.Fatalf("the rewritten document does not compile, so the phases below never ran: %v\ndisplayed:\n%s",
			runErr, displayed)
	}
	if len(out.Advisories) == 0 {
		t.Fatalf("the channel is empty, so every assertion below would be vacuous.\ndisplayed:\n%s", displayed)
	}

	// The binding lint: named by the identity registered against the reading
	// rather than by a sentence, so the discriminator survives a rewording and
	// fails when the reading stops reporting at all.
	lintSentence := resolver.ErrUnreadParameter().Error()
	lint := advisoryNaming(out.Advisories, lintSentence)
	if len(lint) == 0 {
		t.Fatalf("no channel entry carries the binding lint's registered sentence %q.\nchannel: %+v\ndisplayed:\n%s",
			lintSentence, out.Advisories, displayed)
	}
	for _, a := range lint {
		if a.Severity != abi.SeverityWarn {
			t.Errorf("binding-lint entry severity = %q; want %q — an advisory must never arrive at the halting tier",
				a.Severity, abi.SeverityWarn)
		}
		if a.Location == nil || a.Location.KeyPath == "" {
			t.Errorf("binding-lint entry carries no locator (%+v); a positioning surface can then only place it "+
				"at the head of the file, which is the fidelity this channel exists to preserve", a)
			continue
		}
		if !strings.HasPrefix(a.Location.KeyPath, ".spec.") {
			t.Errorf("locator = %q; want a path into the authored document", a.Location.KeyPath)
		}
		// The URI stays empty on this class ON PURPOSE: the shared display
		// formatter reads a location's URI as a graph node, so a document path
		// there would be printed as though it were a node identity.
		if a.Location.URI != "" {
			t.Errorf("locator carries URI %q; a document-scoped advisory must leave it empty or the shared "+
				"formatter prints the document as though it were a graph node", a.Location.URI)
		}
		if a.SourceRef == "" {
			t.Error("binding-lint entry names no subject; the entity is what a surface showing the finding without " +
				"its composed subject needs in order to name it")
		}
		if a.Code == "" {
			t.Error("binding-lint entry carries no code; a consumer matching the class has nothing to match on")
		}
	}

	// The static-analysis advisory stream, which reaches the result's channel
	// rather than only a display.
	analyzerSentence := analyzer.ErrOverriddenAnnotation().Error()
	stream := advisoryNaming(out.Advisories, analyzerSentence)
	if len(stream) == 0 {
		t.Fatalf("no channel entry carries the static-analysis advisory's registered sentence %q.\nchannel: %+v\ndisplayed:\n%s",
			analyzerSentence, out.Advisories, displayed)
	}
	for _, a := range stream {
		if a.Severity != abi.SeverityWarn {
			t.Errorf("static-analysis entry severity = %q; want %q", a.Severity, abi.SeverityWarn)
		}
	}

	// Both still reach a person at a terminal. The channel is an addition, never a
	// replacement, so a display that stopped printing would be a regression this
	// file must catch rather than tolerate.
	for _, want := range []string{lintSentence, analyzerSentence} {
		if !strings.Contains(displayed, want) {
			t.Errorf("the display no longer carries %q; the channel is an addition to the terminal, not a "+
				"substitute for it.\ndisplayed:\n%s", want, displayed)
		}
	}

	// The ORDER is the display's order. The lint is raised inside the resolve
	// scope and the advisory stream after static analysis, so the lint precedes it
	// on the terminal and must precede it on the channel too — otherwise a reader
	// diffing what a run printed against what it returned finds two sequences.
	lintAt := indexOfAdvisory(out.Advisories, lintSentence)
	streamAt := indexOfAdvisory(out.Advisories, analyzerSentence)
	if lintAt < 0 || streamAt < 0 {
		t.Fatalf("could not locate both classes on the channel for the order assertion (lint=%d stream=%d)", lintAt, streamAt)
	}
	if lintAt > streamAt {
		t.Errorf("the channel orders the static-analysis entry (%d) before the binding lint (%d); the displays "+
			"print them the other way round, so the two sequences disagree", streamAt, lintAt)
	}
	if strings.Index(displayed, lintSentence) > strings.Index(displayed, analyzerSentence) {
		t.Error("the display order assumed by the assertion above does not hold; re-derive it rather than " +
			"trusting this test's premise")
	}
}

// TestACleanDocumentProducesAnEmptyChannelAndSilentTerminal is the calibration
// without which every assertion above is unfalsifiable.
//
// A channel that reported something for every run would satisfy the presence
// assertions above and teach a reader to ignore it, so the other direction is
// asserted over the fixture exactly as authored.
func TestACleanDocumentProducesAnEmptyChannelAndSilentTerminal(t *testing.T) {
	out, displayed := tier4OverFixture(t, readFixtureBytes(t, advisoryOverrideAggregatePath))

	if len(out.Advisories) != 0 {
		t.Errorf("a document authoring no advisory produced %d channel entries: %+v",
			len(out.Advisories), out.Advisories)
	}
	if strings.TrimSpace(displayed) != "" {
		t.Errorf("a document authoring no advisory printed to the terminal:\n%s", displayed)
	}
}
