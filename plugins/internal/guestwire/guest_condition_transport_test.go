// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package guestwire_test

import (
	stderrors "errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// guestCoreDirs are the packages whose halting failures are raised inside a
// provider Wasm guest. They are named as directories rather than derived from an
// import graph because the properties under test are about what those SOURCES
// contain, and source content is a fact about files rather than about a symbol
// anything reaches.
var guestCoreDirs = []string{
	filepath.Join("plugins", "internal", "composecore"),
	filepath.Join("plugins", "internal", "helmcore"),
	filepath.Join("plugins", "internal", "cfncore"),
}

// noFailureChannel marks a failure raised only from an export whose output shape
// declares no in-band failure channel, so its condition cannot cross however the
// transport is written.
//
// It is a value in the table rather than an omission from it, because an
// omission is indistinguishable from forgetting. No row carries it today; it
// stays because a sub-operation output that declares no failure channel is a real
// shape on this socket, and the next failure raised from one needs somewhere
// honest to sit rather than a silent omission.
const noFailureChannel = ""

// guestFailureKind is one failure a provider guest core raises: the number that
// named it while it was a condition of its own, the sentence it reports, the
// generic condition its raise site now names, and the export whose output carries
// it.
//
// The number is here even though nothing raises it any more, and that is the
// point of the row rather than a leftover. It is what ties this table to the
// retirement record: a number that stopped being retired would free itself for
// reuse, and the sentence beside it is what a reader of an old report is
// actually looking for.
type guestFailureKind struct {
	retiredShortCode string
	sentence         string
	condition        error
	export           string
}

// guestFailureKinds is every failure a provider guest core raises that carries a
// halting condition across the socket.
//
// Each row pairs a sentence with the generic condition the raise site names. The
// sentence is the part that says what happened — WHICH construct, which input,
// which name, which value — and it is the only thing in the pair that tells one
// row from another, because several rows deliberately share a condition. So the
// assertions below never test a condition alone.
//
// It is held to the tree in three directions rather than kept in step by eye:
// every number here is still retired, every sentence here is still a literal in
// some guest core's own source, and no guest core registers a condition of its
// own any more. Each is a separate test below, and each would fail loudly on the
// change it is there to catch.
var guestFailureKinds = []guestFailureKind{
	// compose — workload synthesis, all on render_node.
	{"E0236", "probe http: requires at least one container port; container declares none",
		wasm.ErrPluginRequiredInputUnresolved(), wasmengine.RenderNodeExport},
	{"E0237", "probe declares no shape (http/tcp/exec)",
		wasm.ErrPluginRequiredInputUnresolved(), wasmengine.RenderNodeExport},
	{"E0238", "resources quantity cannot be converted to a compose deploy.resources value",
		wasm.ErrPluginValueNotRepresentable(), wasmengine.RenderNodeExport},
	{"E0239", "compose has no env-from-object primitive; container declares envFrom",
		wasm.ErrPluginCannotExpressInput(), wasmengine.RenderNodeExport},
	{"E0240", "compose cannot express this workload form",
		wasm.ErrPluginCannotExpressInput(), wasmengine.RenderNodeExport},
	{"E0241", "compose has no lifecycle-hook primitive; container declares lifecycle hooks",
		wasm.ErrPluginCannotExpressInput(), wasmengine.RenderNodeExport},
	{"E0258", "compose publishes a port's transport as a tcp or udp suffix; this port declares a transport compose cannot express",
		wasm.ErrPluginValueNotRepresentable(), wasmengine.RenderNodeExport},
	// compose — boundary assembly, where a configmap reference and the artifact
	// declaring it first meet.
	{"E0235", "configmap volume requires a file emitted via the Recipe's artifacts: input; none found",
		wasm.ErrPluginRequiredInputUnresolved(), wasmengine.AssembleBoundaryExport},
	// helm — synthesis and rendering, all on render_node.
	{"E0243", "two rendered resources derive the same output filename with differing content",
		wasm.ErrPluginOutputNameCollision(), wasmengine.RenderNodeExport},
	{"E0244", "synthesized StatefulSet has more than one headless Service",
		wasm.ErrPluginRequiredInputUnresolved(), wasmengine.RenderNodeExport},
	{"E0245", "synthesized StatefulSet has no headless governing Service",
		wasm.ErrPluginRequiredInputUnresolved(), wasmengine.RenderNodeExport},
	// cloudformation — fragment normalization and name declaration, on render_node.
	{"E0230", "cloudformation-provider: one mapping key is written more than once",
		wasm.ErrPluginOutputNameCollision(), wasmengine.RenderNodeExport},
	{"E0233", "cloudformation-provider: two entries an author wrote reach one stack Output name",
		wasm.ErrPluginOutputNameCollision(), wasmengine.RenderNodeExport},
	// cloudformation — cross-boundary linking, on the two link exports.
	{"E0231", "cloudformation-provider: cross-boundary link resolve received no consumer stack template",
		wasm.ErrPluginRequiredInputUnresolved(), wasm.LinkResolveExport},
	{"E0232", "cloudformation-provider: cross-boundary link value is interpolated into a larger string; only a whole-scalar injection can carry a cross-stack reference",
		wasm.ErrPluginValueNotRepresentable(), wasm.LinkResolveExport},
	{"E0249", "cloudformation-provider: an export name a cross-boundary link derives is already declared by another Outputs entry in the same stack",
		wasm.ErrPluginOutputNameCollision(), wasm.LinkPublishExport},
}

// TestNoGuestCoreDeclaresAHaltingCondition is the completeness half of the
// migration these rows record: a provider guest core registers no halting
// condition of its own, because the conditions a plugin may report are the
// compiler's and a guest chooses one rather than allocating one.
//
// It is asserted by scanning source rather than by asking a registry, because a
// registration is a fact about source: the registry is not enumerable from
// outside the package that owns it, and a registration added to a guest core
// tomorrow would be invisible to any runtime question this package can ask.
//
// A scan that found nothing would agree with an empty tree for the wrong reason,
// so the extractor is driven over a PLANTED registration first and required to
// find it. Without that needle this test passes on a scanner that was quietly
// broken by a move of the registration form or of the directory layout — which is
// the one failure a zero cannot distinguish from success.
func TestNoGuestCoreDeclaresAHaltingCondition(t *testing.T) {
	t.Parallel()

	// The needle. A registration in exactly the form a guest core used to write,
	// in a file the scan is then pointed at.
	planted := t.TempDir()
	const needleCode = "E0000"
	const needle = `package plantedcore

import cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"

var errPlanted = cerrors.Register("a-condition-planted-to-calibrate-the-scan", cerrors.Meta{
	ShortCode: "` + needleCode + `",
	Message:   "the scan finds this or it finds nothing anywhere",
})
`
	if err := os.WriteFile(filepath.Join(planted, "errors.go"), []byte(needle), 0o600); err != nil {
		t.Fatalf("plant the calibration registration: %v", err)
	}
	found := registeredShortCodes(t, planted)
	if _, ok := found[needleCode]; !ok {
		t.Fatalf("the scan did not find a registration planted in front of it, so its silence over the "+
			"guest cores says nothing about them; found %v", found)
	}

	root := moduleRoot(t)
	declared := map[string]string{}
	for _, dir := range guestCoreDirs {
		for code, file := range registeredShortCodes(t, filepath.Join(root, dir)) {
			declared[code] = filepath.Join(dir, file)
		}
	}
	if len(declared) > 0 {
		var rows []string
		for code, file := range declared {
			rows = append(rows, fmt.Sprintf("%s (%s)", code, file))
		}
		sort.Strings(rows)
		t.Errorf("a provider guest core registers %d halting condition(s) of its own: %v\n"+
			"A plugin chooses one of the conditions the compiler declares and supplies the specific "+
			"sentence itself; a condition registered inside a guest is one the compiler binary cannot "+
			"resolve for a reader who quotes it.", len(declared), rows)
	}
}

// TestEveryRetiredNumberStaysRetired ties this table to the retirement record.
//
// A number that named one of these failures was published, so it may never name
// a different condition. The record is what enforces that — registration refuses a
// retired number — and a row here whose number had quietly left the record would
// be a number free for reuse with nothing saying so.
func TestEveryRetiredNumberStaysRetired(t *testing.T) {
	t.Parallel()

	if len(guestFailureKinds) == 0 {
		t.Fatal("the table is empty, so every assertion below would pass over nothing")
	}
	seen := map[string]bool{}
	for _, k := range guestFailureKinds {
		if seen[k.retiredShortCode] {
			t.Errorf("%s appears in the table more than once", k.retiredShortCode)
		}
		seen[k.retiredShortCode] = true
		note, retired := cerrors.RetiredShortCode(k.retiredShortCode)
		if !retired {
			t.Errorf("%s is no longer recorded as retired, so nothing stops it being handed to a "+
				"different condition — which is the one thing the numbering promises cannot happen",
				k.retiredShortCode)
			continue
		}
		if strings.TrimSpace(note) == "" {
			t.Errorf("%s is retired with no note, so a reader quoting it off an old report is told nothing",
				k.retiredShortCode)
		}
	}
}

// TestEverySentenceIsStillRaisedByItsGuestCore is the preservation proof: the
// sentence each retired condition carried is still a string literal in the guest
// core that raises the failure.
//
// It is the assertion that would fail on the migration's worst plausible outcome
// — the codes retired and the sentences dropped with them, leaving sixteen
// vaguer messages behind. That outcome breaks nothing a compiler or a taxonomy
// test can see: every generic condition still resolves, every code still crosses,
// and the user is simply told less.
//
// String literals are read rather than the raw file text, so a sentence that
// survives only inside a comment does not count as raised.
func TestEverySentenceIsStillRaisedByItsGuestCore(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	var literals []string
	for _, dir := range guestCoreDirs {
		literals = append(literals, stringLiterals(t, filepath.Join(root, dir))...)
	}
	// Calibration: a scan that collected nothing would report every sentence
	// missing, which reads as a catastrophic regression rather than as a broken
	// scan. Say which it is before the rows are allowed to mean anything.
	if len(literals) == 0 {
		t.Fatal("the scan collected no string literal from any guest core; the directory layout moved, so " +
			"this test is measuring itself rather than the tree")
	}

	for _, k := range guestFailureKinds {
		if !containsAny(literals, k.sentence) {
			t.Errorf("no guest core carries the sentence %s reported:\n  %q\n"+
				"The condition was retired into the generic set; the SENTENCE is what still tells a user "+
				"what to change in their own file, and it must survive the move verbatim.",
				k.retiredShortCode, k.sentence)
		}
	}
}

// TestEveryGuestFailureCrossesWithItsConditionAndItsSentence is the transport
// proof over the whole table: for each failure raised on an export whose output
// carries a failure channel, the generic condition's code AND the plugin's own
// sentence both reach the error the host builds — which is the error whose head
// and body a reader sees.
//
// Both halves are asserted because either alone passes on a broken
// implementation. A code with no sentence is a diagnostic naming a KIND of
// failure and nothing a user can act on. A sentence with no code, or with the
// host's own generic one, is the failure this whole migration exists to remove:
// a number a reader cannot look up.
//
// It also asserts the classification, because that could diverge from both: an
// implementation attaching the chosen condition by REPLACING the host condition
// rather than standing over it would carry the code and silently stop matching
// the host taxonomy every socket consumer classifies with.
func TestEveryGuestFailureCrossesWithItsConditionAndItsSentence(t *testing.T) {
	t.Parallel()

	for _, k := range guestFailureKinds {
		t.Run(k.retiredShortCode, func(t *testing.T) {
			t.Parallel()

			if k.export == noFailureChannel {
				t.Skipf("%s is raised only from an export whose output shape declares no failure "+
					"message field, so no in-band channel exists to carry its condition", k.retiredShortCode)
			}

			wantCode, ok := cerrors.CodeOf(k.condition)
			if !ok {
				t.Fatalf("the condition reports no descriptive code, so nothing can be asserted about the one that crosses")
			}
			wantShort, ok := cerrors.ShortCodeOf(k.condition)
			if !ok {
				t.Fatalf("the condition reports no short code, so nothing can be asserted about the one that crosses")
			}

			// A raise as a guest core writes one: the generic condition beneath,
			// and the guest's own categorised frame carrying the sentence.
			raised := cerrors.Wrap(k.condition, cerrors.TranslationError,
				cerrors.SourceLocation{File: "node-a"},
				"provider: node \"node-a\": "+k.sentence)

			f := rendercore.FailureOf(raised)
			if f.ShortCode != wantShort {
				t.Fatalf("the guest reports short code %q for this failure; want %q", f.ShortCode, wantShort)
			}
			if f.Code != wantCode {
				t.Fatalf("the guest reports descriptive code %q; want %q", f.Code, wantCode)
			}
			if !strings.Contains(f.Message, k.sentence) {
				t.Errorf("the crossing message does not carry the plugin's own sentence.\n  message: %s\n  sentence: %s",
					f.Message, k.sentence)
			}
			// The condition's registered sentence belongs to the head the host
			// prints, so it must not also be in the text that crosses — the same
			// one-home rule the authoring domain already follows below.
			if sentence := k.condition.Error(); strings.Contains(f.Message, sentence) {
				t.Errorf("the crossing message repeats the condition's own registered sentence, which the "+
					"host says once when it reports the condition, so it would render twice on one line: %s",
					f.Message)
			}
			if strings.Contains(f.Message, string(cerrors.TranslationError)) {
				t.Errorf("the crossing message names the authoring domain, which the host also composes into the "+
					"head it prints, so the word would render twice on one line: %s", f.Message)
			}

			hostErr := wasm.GuestReturnedError("provider/example-provider", k.export, wasm.GuestFailure{
				Message:   f.Message,
				Code:      f.Code,
				ShortCode: f.ShortCode,
			})
			if hostErr == nil {
				t.Fatal("the host read a populated failure as success")
			}
			if stderrors.Is(hostErr, wasm.ErrWasmGuestConditionUndeclared()) {
				t.Fatalf("the host refused a condition a guest core actually raises as one the socket does "+
					"not declare: %v", hostErr)
			}
			gotShort, ok := cerrors.ShortCodeOf(hostErr)
			if !ok || gotShort != wantShort {
				t.Errorf("the host error reports short code %q (found=%t); want %q", gotShort, ok, wantShort)
			}
			gotCode, ok := cerrors.CodeOf(hostErr)
			if !ok || gotCode != wantCode {
				t.Errorf("the host error reports descriptive code %q (found=%t); want %q", gotCode, ok, wantCode)
			}
			if !strings.Contains(hostErr.Error(), k.sentence) {
				t.Errorf("the plugin's own sentence did not survive into the host's error, so a reader meets a "+
					"kind of failure and nothing they can act on: %s", hostErr.Error())
			}
			if !stderrors.Is(hostErr, wasm.ErrWasmGuestError()) {
				t.Error("the host error no longer classifies as a guest-reported failure, so every caller " +
					"matching the host taxonomy has silently stopped matching")
			}
		})
	}
}

// TestGuestFailureWithoutAConditionNamesTheHostCondition is the negative control
// for the test above: it proves the code assertion is capable of finding nothing,
// so a green there is about the transport and not about ShortCodeOf answering for
// something else.
func TestGuestFailureWithoutAConditionNamesTheHostCondition(t *testing.T) {
	t.Parallel()

	f := rendercore.FailureOf(fmt.Errorf("compose-provider: %s", "a failure naming no registered condition"))
	if f.ShortCode != "" || f.Code != "" {
		t.Fatalf("a chain naming no registered condition reported code %q / short code %q", f.Code, f.ShortCode)
	}

	hostErr := wasm.GuestReturnedError("provider/example-provider", wasmengine.RenderNodeExport, wasm.GuestFailure{Message: f.Message})
	short, ok := cerrors.ShortCodeOf(hostErr)
	// The host's own condition still answers, which is the fallback rather than an
	// absence: what must NOT happen is a code appearing from nowhere.
	if !ok {
		t.Error("the host error carries no condition at all; the host's own guest-error condition should stand")
	}
	if short == "" {
		t.Error("the host error reports an empty short code, which would print as an empty bracket")
	}
	if !stderrors.Is(hostErr, wasm.ErrWasmGuestError()) {
		t.Error("a failure naming no condition no longer classifies as a guest-reported failure")
	}
}

// TestGuestFailureHalfReportNamesNoCondition proves the both-or-neither pairing
// rule the host applies, in both of its half shapes.
//
// It is asserted rather than left to the reader because the alternative is
// invisible: a half report would attach one accessor answering for the chosen
// condition and the other for the host's, on one error, and every gate would stay
// green while a reader resolved two different conditions from one diagnostic.
func TestGuestFailureHalfReportNamesNoCondition(t *testing.T) {
	t.Parallel()

	hostShort, _ := cerrors.ShortCodeOf(wasm.ErrWasmGuestError())
	hostCode, _ := cerrors.CodeOf(wasm.ErrWasmGuestError())
	member := wasm.ErrPluginCannotExpressInput()
	memberShort, _ := cerrors.ShortCodeOf(member)
	memberCode, _ := cerrors.CodeOf(member)

	for _, tc := range []struct {
		name string
		f    wasm.GuestFailure
	}{
		{"a descriptive code with no short code", wasm.GuestFailure{Message: "m", Code: memberCode}},
		{"a short code with no descriptive code", wasm.GuestFailure{Message: "m", ShortCode: memberShort}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := wasm.GuestReturnedError("provider/example-provider", wasmengine.RenderNodeExport, tc.f)
			short, _ := cerrors.ShortCodeOf(err)
			code, _ := cerrors.CodeOf(err)
			if short != hostShort || code != hostCode {
				t.Errorf("a half report was attached: the error reports %q / %q; the host's own condition "+
					"(%q / %q) should stand", code, short, hostCode, hostShort)
			}
		})
	}
}

// containsAny reports whether any of the collected literals carries want.
//
// It is a substring test rather than equality because a raise site composes the
// sentence into a format string alongside the occurrence's own values, which is
// exactly the shape the migration produced.
func containsAny(literals []string, want string) bool {
	for _, lit := range literals {
		if strings.Contains(lit, want) {
			return true
		}
	}
	return false
}

// moduleRoot resolves the module root from this package's directory, and refuses
// rather than guessing when it does not find one — a scan rooted in the wrong
// place reports a clean tree while reading nothing.
func moduleRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve module root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("module root %q carries no go.mod; the scan is looking in the wrong place: %v", root, err)
	}
	return root
}

// nonTestGoFiles returns the non-test Go files of one directory, by full path.
func nonTestGoFiles(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		out = append(out, filepath.Join(dir, name))
	}
	sort.Strings(out)
	return out
}

// registeredShortCodes reads the short codes registered in the non-test Go files
// of one directory, keyed by code and valued by the file that declares it.
//
// It reads the metadata literal's own ShortCode field rather than matching a
// pattern over the line, so a registration written across several lines is found
// exactly as a single-line one is.
func registeredShortCodes(t *testing.T, dir string) map[string]string {
	t.Helper()

	out := map[string]string{}
	for _, path := range nonTestGoFiles(t, dir) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Meta" {
				return true
			}
			for _, elt := range lit.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok || key.Name != "ShortCode" {
					continue
				}
				val, ok := kv.Value.(*ast.BasicLit)
				if !ok || val.Kind != token.STRING {
					continue
				}
				out[strings.Trim(val.Value, `"`)] = filepath.Base(path)
			}
			return true
		})
	}
	return out
}

// stringLiterals returns every string literal in the non-test Go files of one
// directory, with its quoting resolved.
//
// Literals rather than raw file text, so a sentence surviving only in a comment
// is not mistaken for one a build reports. Concatenated literals are collected as
// their own pieces: a sentence written across a + join would not be found, which
// is a bound worth knowing — a raise site that wraps a sentence mid-string has to
// be written so the sentence stays inside one piece.
func stringLiterals(t *testing.T, dir string) []string {
	t.Helper()

	var out []string
	for _, path := range nonTestGoFiles(t, dir) {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if s, err := strconv.Unquote(lit.Value); err == nil {
				out = append(out, s)
			}
			return true
		})
	}
	return out
}
