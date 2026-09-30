// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

// The explain-error tool answers with the shared explanation of the condition a
// diagnostic named, and says so when it cannot add the walk-through.
//
// What these cases cover that a cross-surface comparison cannot: the pages are
// read at THIS session's bundle coordinates. A comparison against another surface
// runs where no session pins a bundle, so it cannot tell a read at the session's
// coordinates from a read at coordinates derived a second time — and the negative
// control below is the case that separates them.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content/memory"

	"github.com/aggrega-ai/aggrega-cli/internal/haltref"
	"github.com/aggrega-ai/aggrega-cli/pkg/docscache"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/oci"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// haltFixtureHeading is the heading the fixture halting-conditions page documents
// the probe condition under.
const haltFixtureHeading = "The bundle carries no page at the path asked for"

// haltProbe returns a condition this binary really declares, with both of its
// codes and the sentence its diagnostics open with.
//
// It is read off an accessor rather than quoted, so the case names a condition
// that exists at the moment it runs. Quoting a number would pin these cases to
// one package's allocation and turn a legitimate renumbering into a failure here.
func haltProbe(t *testing.T) (short, code, message string) {
	t.Helper()
	probe := docscache.ErrPageNotFound()
	short, ok := cerrors.ShortCodeOf(probe)
	if !ok {
		t.Fatal("the probe condition reports no short code, so these cases would assert nothing")
	}
	code, ok = cerrors.CodeOf(probe)
	if !ok {
		t.Fatal("the probe condition reports no descriptive code, so these cases would assert nothing")
	}
	return short, code, probe.Error()
}

// haltDiagnostic renders a diagnostic in the shape a build prints one: the
// bracketed head carrying the category and the condition's short code, then the
// message.
//
// The code has to arrive inside a diagnostic rather than on its own, because that
// is what this surface is handed — a caller here pastes what a build gave it.
func haltDiagnostic(short, message string) string {
	return "[CATALOG_ERROR " + short + "] recipe.yaml: " + message
}

// haltFixturePages renders the two published pages in the shape the generator
// writes them: the reference resolving the condition under both of its codes, and
// the halting-conditions page documenting it under a heading, with a generated
// region pairing the code with that heading.
func haltFixturePages(short, code, sentence string) []docsFixturePage {
	return []docsFixturePage{
		{haltref.ReferencePagePath, "# Error sentinels\n\n" +
			"## " + haltref.ExplanationHeading + "\n\n" +
			"<!-- BEGIN GENERATED: " + haltref.ReferenceRegionID + " -->\n" +
			"| Code | Condition | Message |\n| --- | --- | --- |\n" +
			"| `" + short + "` | `" + code + "` | `" + sentence + "` |\n" +
			"<!-- END GENERATED: " + haltref.ReferenceRegionID + " -->\n"},
		{haltref.PagePath, "# Fatal Halts\n\n" +
			"## When reading documentation\n\n" +
			"### " + haltFixtureHeading + "\n\n" +
			"The bundle predates the page, or the path asked for is not the one it is published under.\n\n" +
			"## Finding a halt from the short code the build printed\n\n" +
			"<!-- BEGIN GENERATED: " + haltref.RegionID + " -->\n" +
			"| Short code | Opens with | Documented under |\n| --- | --- | --- |\n" +
			"| `" + short + "` | `" + sentence + "` | " + haltFixtureHeading + " |\n" +
			"<!-- END GENERATED: " + haltref.RegionID + " -->\n"},
	}
}

// pinnedDocsServer returns a server over an empty workspace, pinned to the
// fixture bundle coordinates.
//
// The pin is what makes an assertion about the coordinates possible: unpinned,
// the reference is derived from the workspace's registry and the version from the
// binary's own build identity, so a case asserting which bundle was looked for
// would be asserting values the implementation chose rather than values it was
// given.
func pinnedDocsServer(t *testing.T, ws string) *server {
	t.Helper()
	return &server{
		out:               io.Discard,
		logw:              io.Discard,
		serverVersion:     docsTestVersion,
		startDir:          ws,
		docsBundleRef:     docsTestRef,
		docsBundleVersion: docsTestVersion,
	}
}

// seedHaltDocsWorkspace caches a bundle carrying the halting-conditions pages for
// a fresh workspace and returns a server pinned to it.
func seedHaltDocsWorkspace(t *testing.T, pages []docsFixturePage) *server {
	t.Helper()
	ws := t.TempDir()
	cacheDir := workspaceconfig.Default().ResolvedArtifactCacheDir()

	store := memory.New()
	if _, err := oci.Push(context.Background(), store, docsTestVersion, oci.MediaTypeDocs, docsBundleTar(t, pages), nil); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if _, err := docscache.ResolveAndCache(context.Background(), store, docsTestRef, docsTestVersion, ws, cacheDir, nil); err != nil {
		t.Fatalf("ResolveAndCache: %v", err)
	}

	return pinnedDocsServer(t, ws)
}

// explainErrorPayload calls the tool and returns its decoded structured payload
// beside the text block.
func explainErrorPayload(t *testing.T, s *server, args explainErrorArgs) (explainErrorResult, string) {
	t.Helper()
	res := callTool(t, s, "explain-error", args)
	if res.IsError {
		t.Fatalf("explain-error reported error: %s", res.Content[0].Text)
	}
	var payload explainErrorResult
	if err := json.Unmarshal(res.StructuredContent, &payload); err != nil {
		t.Fatalf("unmarshal explain-error payload: %v", err)
	}
	return payload, res.Content[0].Text
}

// TestExplainErrorCarriesThePublishedWalkThrough is the primary in-package proof:
// a diagnostic carrying a real short code is answered with the condition and the
// entry the published page documents it under, not with a path to a document.
func TestExplainErrorCarriesThePublishedWalkThrough(t *testing.T) {
	t.Parallel()
	short, code, sentence := haltProbe(t)
	s := seedHaltDocsWorkspace(t, haltFixturePages(short, code, sentence))

	payload, text := explainErrorPayload(t, s, explainErrorArgs{
		Message: haltDiagnostic(short, sentence),
		Kind:    "recipe",
	})

	if payload.Condition == nil {
		t.Fatalf("the answer carries no condition, so the code in the diagnostic resolved to nothing; note: %q", payload.Note)
	}
	// The condition's own three facts, compared against the accessors rather than
	// against anything the answer supplied.
	if payload.Condition.ShortCode != short || payload.Condition.Code != code || payload.Condition.Message != sentence {
		t.Errorf("the answer reports a different condition than the registry declares:\n"+
			"got  %s / %s / %s\nwant %s / %s / %s",
			payload.Condition.ShortCode, payload.Condition.Code, payload.Condition.Message, short, code, sentence)
	}
	if payload.Reference != haltref.ExplanationURL() {
		t.Errorf("reference = %q; want the same address a diagnostic's pointer carries (%q)",
			payload.Reference, haltref.ExplanationURL())
	}

	// The walk-through arrived, so nothing is missing and the note has nothing to
	// report. Asserting the pair is what separates a real answer from one that
	// returned the condition and quietly dropped the prose.
	if len(payload.Entries) != 1 {
		t.Fatalf("got %d published entries, want the one the page documents this condition under: %+v",
			len(payload.Entries), payload.Entries)
	}
	if payload.Entries[0].Heading != haltFixtureHeading {
		t.Errorf("entry heading = %q, want %q", payload.Entries[0].Heading, haltFixtureHeading)
	}
	if payload.Note != "" {
		t.Errorf("the walk-through was reported missing while %d entries arrived: %q", len(payload.Entries), payload.Note)
	}

	// The prose block and the structured entries are the same answer, so the entry
	// the payload names has to be present in the text a reader is shown.
	if !strings.Contains(text, payload.Entries[0].Body) {
		t.Errorf("the text block omits the entry the payload carries:\n--- text ---\n%s\n--- entry ---\n%s",
			text, payload.Entries[0].Body)
	}
	// The pointer to the kind's authoring contract is unaffected by any of this: it
	// answers a different question — where to read the contract for the thing that
	// failed — and both belong on the answer.
	if payload.DocPointer != docEntityDir+"recipe.md" {
		t.Errorf("doc pointer = %q, want the recipe language-reference page", payload.DocPointer)
	}
}

// TestExplainErrorSaysWhyTheWalkThroughIsMissing is the page-less case, and the
// failure mode it exists for is silence: a surface that omitted the section would
// leave a reader believing no explanation had ever been written.
//
// The condition itself is declared inside the binary, so it is answered with no
// bundle at all — which is what makes the note a statement about the walk-through
// rather than about the whole reply.
func TestExplainErrorSaysWhyTheWalkThroughIsMissing(t *testing.T) {
	t.Parallel()
	short, code, sentence := haltProbe(t)
	// An empty workspace: no bundle cached, and this surface acquires at the
	// start of the run rather than here, so nothing is fetched.
	s := pinnedDocsServer(t, t.TempDir())

	payload, text := explainErrorPayload(t, s, explainErrorArgs{Message: haltDiagnostic(short, sentence)})

	if payload.Condition == nil {
		t.Fatal("the condition is declared inside the binary and does not depend on the documentation, " +
			"yet the answer carries none")
	}
	if payload.Condition.ShortCode != short || payload.Condition.Code != code || payload.Condition.Message != sentence {
		t.Errorf("the answer reports a different condition than the registry declares:\n"+
			"got  %s / %s / %s\nwant %s / %s / %s",
			payload.Condition.ShortCode, payload.Condition.Code, payload.Condition.Message, short, code, sentence)
	}
	if len(payload.Entries) != 0 {
		t.Errorf("a workspace with no cached documentation produced %d published entries: %+v",
			len(payload.Entries), payload.Entries)
	}
	if payload.Note == "" {
		t.Fatal("the walk-through is absent and the answer says nothing about it, which reads to a " +
			"caller as though no explanation existed")
	}
	// The reason has to name the artifact rather than report that something was not
	// found, and it has to reach the prose as well as the payload — a caller reading
	// only the text block is the one most likely to conclude nothing exists.
	for _, want := range []string{docsTestRef, docsTestVersion, haltref.PublishedPath(haltref.PagePath)} {
		if !strings.Contains(payload.Note, want) {
			t.Errorf("the note omits %q, so it does not say what was missing: %s", want, payload.Note)
		}
	}
	if !strings.Contains(text, payload.Note) {
		t.Errorf("the note reaches the payload and not the prose:\n--- text ---\n%s\n--- note ---\n%s",
			text, payload.Note)
	}
}

// explainRetiredProbe is a short code the retirement record holds, and
// explainUnregisteredProbe one that is shaped like a code and is neither registered
// nor retired.
//
// Both are quoted rather than read off an accessor because there is nothing to read
// them off: the retirement record answers a lookup — is THIS number retired — and
// enumerates nothing, deliberately, since a retired number is a positive
// declaration rather than a gap in a sequence. Both quotes are checked against the
// registry and the record before they are used, so a case pointed at a number whose
// status changed fails saying so rather than quietly measuring nothing.
const (
	explainRetiredProbe      = "E0157"
	explainUnregisteredProbe = "E0000"
)

// TestExplainErrorDiscriminatesASpentNumberFromAnUnrecognisedOne is the pin on what
// the answer adds when nothing resolves, and the two codes are opposites.
//
// A short code that named a condition once is a fact about the number, and every
// other surface reports it, so this one carries that refusal as its note. A run of
// characters that merely looks like a code — under no registration and in no
// retirement record — is not a fact about the diagnostic, so the answer adds nothing
// to it. Both directions are pinned because a repair aimed at the first that also
// fired on the second would start asserting something about every code-shaped token
// a caller happened to paste.
//
// Each code runs twice, against a workspace holding the published pages and against
// one holding nothing, and the pair discriminates rather than decorates: a spent
// number is answered out of a declaration and reads no pages at all, so a change
// that consulted the documentation first would reach the empty workspace with a
// combined refusal and fail that row alone.
func TestExplainErrorDiscriminatesASpentNumberFromAnUnrecognisedOne(t *testing.T) {
	t.Parallel()
	short, code, sentence := haltProbe(t)

	note, retired := cerrors.RetiredShortCode(explainRetiredProbe)
	if !retired {
		t.Fatalf("%s is not in the retirement record, so this case no longer exercises a spent "+
			"number; point it at one that is retired", explainRetiredProbe)
	}
	// Built from the retirement record and the refusal's own type, neither of which
	// this surface reads to compose an answer — so a reworded sentence fails here
	// instead of moving both sides of the comparison at once.
	spent := (&haltref.RetiredCodeError{ShortCode: explainRetiredProbe, Note: note}).Error()

	if _, err := haltref.Resolve(explainUnregisteredProbe); err == nil {
		t.Fatalf("%s resolves to a condition, so the silent rows no longer exercise an unrecognised "+
			"code; point them at one nothing declares", explainUnregisteredProbe)
	}
	if _, alsoRetired := cerrors.RetiredShortCode(explainUnregisteredProbe); alsoRetired {
		t.Fatalf("%s is in the retirement record, so the silent rows are measuring the other case",
			explainUnregisteredProbe)
	}

	for _, tc := range []struct {
		name     string
		code     string
		seeded   bool
		wantNote string
	}{
		{"a spent number, with the pages cached", explainRetiredProbe, true, spent},
		{"a spent number, with no documentation at all", explainRetiredProbe, false, spent},
		{"an unrecognised code, with the pages cached", explainUnregisteredProbe, true, ""},
		{"an unrecognised code, with no documentation at all", explainUnregisteredProbe, false, ""},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			s := pinnedDocsServer(t, t.TempDir())
			if tc.seeded {
				s = seedHaltDocsWorkspace(t, haltFixturePages(short, code, sentence))
			}

			diagnostic := haltDiagnostic(tc.code, "what a build reported")
			payload, text := explainErrorPayload(t, s, explainErrorArgs{Message: diagnostic, Kind: "recipe"})

			if payload.Note != tc.wantNote {
				t.Errorf("note = %q\nwant %q", payload.Note, tc.wantNote)
			}
			// Nothing resolved on any row, so none of them may report a condition: a
			// caller would render one whose every value is blank as an answer.
			if payload.Condition != nil {
				t.Errorf("%s was reported as a condition: %+v", tc.code, *payload.Condition)
			}
			if len(payload.Entries) != 0 {
				t.Errorf("%s carried %d published entries: %+v", tc.code, len(payload.Entries), payload.Entries)
			}
			if payload.Reference != "" {
				t.Errorf("%s carried the address a condition is published at: %q", tc.code, payload.Reference)
			}

			// The prose is the diagnostic, whatever the note carries, and the pointer,
			// composed here rather than read back from the surface that wrote it.
			want := diagnostic
			if tc.wantNote != "" {
				want += "\n\n" + tc.wantNote
			}
			want += "\n\nSee: " + docEntityDir + "recipe.md"
			if payload.Guidance != want {
				t.Errorf("the prose is not the diagnostic, the note and the pointer.\n--- got ---\n%s\n"+
					"--- want ---\n%s", payload.Guidance, want)
			}
			if text != payload.Guidance {
				t.Errorf("the text block and the payload's prose differ.\n--- text ---\n%s\n"+
					"--- payload ---\n%s", text, payload.Guidance)
			}
		})
	}
}

// TestExplainErrorReadsTheSessionsOwnBundle is the negative control that separates
// a read at this session's coordinates from a read at coordinates derived a second
// time.
//
// The workspace holds a bundle carrying the pages. The session is pinned to a
// different version of it — the state a session pinned to a bundle it cannot see
// is in — so an answer carrying entries would mean the read went looking somewhere
// the session never named. Without this case, a loader deriving its own
// coordinates would satisfy every other assertion here.
func TestExplainErrorReadsTheSessionsOwnBundle(t *testing.T) {
	t.Parallel()
	short, _, sentence := haltProbe(t)
	s := seedHaltDocsWorkspace(t, haltFixturePages(haltProbe(t)))

	// The positive control: at the session's own coordinates the pages are found.
	// Without it the assertion below would hold just as well over a bundle that was
	// never cached at all.
	warm, _ := explainErrorPayload(t, s, explainErrorArgs{Message: haltDiagnostic(short, sentence)})
	if len(warm.Entries) == 0 {
		t.Fatalf("the pages are not readable at the session's own coordinates, so this case cannot "+
			"measure anything; note: %q", warm.Note)
	}

	s.docsBundleVersion = docsTestVersion + "-not-cached"
	cold, _ := explainErrorPayload(t, s, explainErrorArgs{Message: haltDiagnostic(short, sentence)})
	if len(cold.Entries) != 0 {
		t.Errorf("the session is pinned to %s and the answer still carried %d published entries, so the "+
			"read resolved coordinates the session never named", s.docsBundleVersion, len(cold.Entries))
	}
	if !strings.Contains(cold.Note, s.docsBundleVersion) {
		t.Errorf("the note does not name the bundle version the session is pinned to (%s): %s",
			s.docsBundleVersion, cold.Note)
	}
}

// TestAPointerNamesAKindsPageOrAPageOutrightThroughOneLine pins the two ways a
// refusal selects the page it closes with, and that both close with the same
// line.
//
// The kind rows are the control on every refusal that did not change: a kind with
// a page is sent to it, and no kind is sent to the index, byte for byte as before.
// The page row is the refusal whose fix no kind's page teaches.
func TestAPointerNamesAKindsPageOrAPageOutrightThroughOneLine(t *testing.T) {
	const diagnostic = "the operation did not complete"
	for _, tc := range []struct {
		scenario string
		got      string
		want     string
	}{
		{
			scenario: "a kind with a page of its own",
			got:      explainDiagnostic(diagnostic, "recipe"),
			want:     diagnostic + "\n\nSee: aggrega-docs/language-reference/entities/recipe.md",
		},
		{
			scenario: "no kind at all",
			got:      explainDiagnostic(diagnostic, ""),
			want:     diagnostic + "\n\nSee: aggrega-docs/language-reference/",
		},
		{
			scenario: "a page named outright",
			got:      explainDiagnosticAt(diagnostic, docServerReference),
			want:     diagnostic + "\n\nSee: aggrega-docs/language-reference/cli/aggrega-mcp.md",
		},
		{
			scenario: "a tool failure selecting by kind",
			got:      explainToolError("It stopped.", errors.New(diagnostic), ""),
			want:     "It stopped.\n\n" + diagnostic + "\n\nSee: aggrega-docs/language-reference/",
		},
		{
			scenario: "a tool failure naming its page outright",
			got:      explainToolErrorAt("It stopped.", errors.New(diagnostic), docServerReference),
			want:     "It stopped.\n\n" + diagnostic + "\n\nSee: aggrega-docs/language-reference/cli/aggrega-mcp.md",
		},
	} {
		t.Run(tc.scenario, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("the guidance reads\n  %q\nwant\n  %q", tc.got, tc.want)
			}
		})
	}
}
