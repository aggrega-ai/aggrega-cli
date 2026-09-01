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
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer/wasmengine"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cfncore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/composecore"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/helmcore"
)

// guestCoreDirs are the packages whose halting conditions are declared inside a
// provider Wasm guest. They are named as directories rather than derived from an
// import graph because the property under test is about what is DECLARED there,
// and a declaration is a fact about source rather than about a symbol anything
// reaches.
var guestCoreDirs = []string{
	filepath.Join("plugins", "internal", "composecore"),
	filepath.Join("plugins", "internal", "helmcore"),
	filepath.Join("plugins", "internal", "cfncore"),
}

// noFailureChannel marks a condition raised only from an export whose output
// shape declares no in-band failure channel, so its code cannot cross however
// the transport is written.
//
// It is a value in the table rather than an omission from it, because an
// omission is indistinguishable from forgetting: the set assertion below
// requires every declared condition to appear, and this is how one appears while
// saying it does not cross. No published condition carries it today; it stays
// because a sub-operation output that declares no failure channel is a real shape
// on this socket, and the next condition raised from one needs somewhere honest
// to sit rather than a silent omission.
const noFailureChannel = ""

// guestCondition is one published halting condition a provider guest core
// declares: its compact identifier, the identity itself, and the export whose
// output carries its failure.
type guestCondition struct {
	shortCode string
	condition error
	export    string
}

// publishedGuestConditions is every halting condition the provider guest cores
// declare, paired with the export whose output shape carries it.
//
// It is held to the DECLARED set by TestPublishedGuestConditionsIsTheDeclaredSet
// below, in both directions, so it is a set rather than a sample: a condition
// registered in a guest core and left out here fails that test, and an entry here
// naming a code no guest core declares fails it too. Without that pairing this
// table would be a list somebody kept in step by eye, which is the shape that
// reads as complete after it stops being so.
var publishedGuestConditions = []guestCondition{
	// compose — workload synthesis, all on render_node.
	{"E0236", composecore.ErrTranslationProbeNoPort(), wasmengine.RenderNodeExport},
	{"E0237", composecore.ErrTranslationProbeShape(), wasmengine.RenderNodeExport},
	{"E0238", composecore.ErrUnconvertibleComposeQuantity(), wasmengine.RenderNodeExport},
	{"E0239", composecore.ErrUnsupportedComposeEnvFrom(), wasmengine.RenderNodeExport},
	{"E0240", composecore.ErrUnsupportedComposeForm(), wasmengine.RenderNodeExport},
	{"E0241", composecore.ErrUnsupportedComposeLifecycle(), wasmengine.RenderNodeExport},
	{"E0258", composecore.ErrUnsupportedComposePortProtocol(), wasmengine.RenderNodeExport},
	// compose — boundary assembly, where a configmap reference and the artifact
	// declaring it first meet.
	{"E0235", composecore.ErrTranslationConfigMapMissingArtifact(), wasmengine.AssembleBoundaryExport},
	// helm — synthesis and rendering, all on render_node.
	{"E0243", helmcore.ErrRenderedFileCollision(), wasmengine.RenderNodeExport},
	{"E0244", helmcore.ErrStatefulSetAmbiguousHeadlessService(), wasmengine.RenderNodeExport},
	{"E0245", helmcore.ErrStatefulSetNoHeadlessService(), wasmengine.RenderNodeExport},
	// cloudformation — fragment normalization and name declaration, on render_node.
	{"E0230", cfncore.ErrDuplicateMappingKey(), wasmengine.RenderNodeExport},
	{"E0233", cfncore.ErrOutputNameCollision(), wasmengine.RenderNodeExport},
	// cloudformation — cross-boundary linking, on the two link exports.
	{"E0231", cfncore.ErrLinkConsumerStackMissing(), wasm.LinkResolveExport},
	{"E0232", cfncore.ErrLinkSentinelInterpolated(), wasm.LinkResolveExport},
	{"E0249", cfncore.ErrLinkExportNameShadowed(), wasm.LinkPublishExport},
}

// TestPublishedGuestConditionsIsTheDeclaredSet holds the table above to the
// conditions the guest cores actually register, in both directions.
//
// The declared side is read from source rather than from the registry, because
// the registry is not enumerable from outside the package that owns it — and
// reading source is what makes a newly registered condition fail here instead of
// being quietly uncovered. The scan is calibrated by the count it finds: a scan
// that found nothing would agree with an empty table.
func TestPublishedGuestConditionsIsTheDeclaredSet(t *testing.T) {
	t.Parallel()

	root := moduleRoot(t)
	declared := map[string]string{}
	for _, dir := range guestCoreDirs {
		for code, file := range registeredShortCodes(t, filepath.Join(root, dir)) {
			if prior, dup := declared[code]; dup {
				t.Fatalf("short code %s is registered in both %s and %s", code, prior, file)
			}
			declared[code] = file
		}
	}
	// Calibration. An empty scan reads exactly like a table that covers
	// everything, so the scan states that it found something before its silence
	// is allowed to mean anything.
	if len(declared) == 0 {
		t.Fatal("the scan found no registered condition in any guest core; the registration form or the " +
			"directory layout moved, so this test is measuring itself rather than the tree")
	}

	tabled := map[string]bool{}
	for _, c := range publishedGuestConditions {
		if tabled[c.shortCode] {
			t.Errorf("short code %s appears twice in the table", c.shortCode)
		}
		tabled[c.shortCode] = true
	}

	var missing, extra []string
	for code, file := range declared {
		if !tabled[code] {
			missing = append(missing, fmt.Sprintf("%s (%s)", code, file))
		}
	}
	for code := range tabled {
		if _, ok := declared[code]; !ok {
			extra = append(extra, code)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 {
		t.Errorf("a guest core registers %d condition(s) this table does not carry, so the transport is "+
			"unproven for them: %v", len(missing), missing)
	}
	if len(extra) > 0 {
		t.Errorf("this table carries %d code(s) no guest core registers: %v", len(extra), extra)
	}
}

// TestEveryPublishedGuestConditionCrossesWithItsCode is the transport proof over
// the whole published set: for each condition raised on an export whose output
// carries a failure channel, the code the guest published reaches the error the
// host builds — which is the error whose head a reader sees.
//
// It asserts the code AND the classification, because the two could diverge: an
// implementation that attached the guest's identifiers by replacing the host
// condition rather than standing over it would carry the code and silently stop
// matching the host taxonomy every caller classifies with.
func TestEveryPublishedGuestConditionCrossesWithItsCode(t *testing.T) {
	t.Parallel()

	for _, c := range publishedGuestConditions {
		t.Run(c.shortCode, func(t *testing.T) {
			t.Parallel()

			if c.export == noFailureChannel {
				t.Skipf("%s is raised only from an export whose output shape declares no failure "+
					"message field, so no in-band channel exists to carry its code", c.shortCode)
			}

			// A raise as a guest core writes one: the condition beneath, an
			// occurrence clause above it, and the guest's own categorised frame
			// around both.
			raised := cerrors.New(cerrors.TranslationError,
				cerrors.SourceLocation{File: "node-a"},
				"provider: node \"node-a\": the occurrence's own clause",
				nil, c.condition)

			f := rendercore.FailureOf(raised)
			if f.ShortCode != c.shortCode {
				t.Fatalf("the guest reports short code %q for this condition; want %q", f.ShortCode, c.shortCode)
			}
			wantCode, ok := cerrors.CodeOf(c.condition)
			if !ok {
				t.Fatalf("the condition reports no descriptive code, so nothing can be asserted about the one that crosses")
			}
			if f.Code != wantCode {
				t.Fatalf("the guest reports descriptive code %q; want %q", f.Code, wantCode)
			}
			if !strings.Contains(f.Message, c.condition.Error()) {
				t.Errorf("the crossing message does not carry the condition's registered sentence.\n  message: %s\n  sentence: %s",
					f.Message, c.condition.Error())
			}
			// The domain word belongs to the head the host prints, so it must not
			// also be in the text that crosses.
			if strings.Contains(f.Message, string(cerrors.TranslationError)) {
				t.Errorf("the crossing message names the authoring domain, which the host also composes into the "+
					"head it prints, so the word would render twice on one line: %s", f.Message)
			}

			hostErr := wasm.GuestReturnedError(c.export, wasm.GuestFailure{
				Message:   f.Message,
				Code:      f.Code,
				ShortCode: f.ShortCode,
			})
			if hostErr == nil {
				t.Fatal("the host read a populated failure as success")
			}
			gotShort, ok := cerrors.ShortCodeOf(hostErr)
			if !ok || gotShort != c.shortCode {
				t.Errorf("the host error reports short code %q (found=%t); want %q", gotShort, ok, c.shortCode)
			}
			gotCode, ok := cerrors.CodeOf(hostErr)
			if !ok || gotCode != wantCode {
				t.Errorf("the host error reports descriptive code %q (found=%t); want %q", gotCode, ok, wantCode)
			}
			if !stderrors.Is(hostErr, wasm.ErrWasmGuestError()) {
				t.Error("the host error no longer classifies as a guest-reported failure, so every caller " +
					"matching the host taxonomy has silently stopped matching")
			}
		})
	}
}

// TestGuestFailureWithoutACondition is the negative control for the test above:
// it proves the code assertion is capable of finding nothing, so a green there is
// about the transport and not about ShortCodeOf answering for something else.
func TestGuestFailureWithoutACondition(t *testing.T) {
	t.Parallel()

	f := rendercore.FailureOf(fmt.Errorf("compose-provider: %s", "a failure naming no registered condition"))
	if f.ShortCode != "" || f.Code != "" {
		t.Fatalf("a chain naming no registered condition reported code %q / short code %q", f.Code, f.ShortCode)
	}

	hostErr := wasm.GuestReturnedError(wasmengine.RenderNodeExport, wasm.GuestFailure{Message: f.Message})
	short, ok := cerrors.ShortCodeOf(hostErr)
	// The host's own condition still answers, which is the fallback rather than an
	// absence: what must NOT happen is the guest's code appearing from nowhere.
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
// invisible: a half report would attach one accessor answering for the guest's
// condition and the other for the host's, on one error, and every gate would stay
// green while a reader resolved two different conditions from one diagnostic.
func TestGuestFailureHalfReportNamesNoCondition(t *testing.T) {
	t.Parallel()

	hostShort, _ := cerrors.ShortCodeOf(wasm.ErrWasmGuestError())
	hostCode, _ := cerrors.CodeOf(wasm.ErrWasmGuestError())

	for _, tc := range []struct {
		name string
		f    wasm.GuestFailure
	}{
		{"a descriptive code with no short code", wasm.GuestFailure{Message: "m", Code: "compose/x"}},
		{"a short code with no descriptive code", wasm.GuestFailure{Message: "m", ShortCode: "E0241"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := wasm.GuestReturnedError(wasmengine.RenderNodeExport, tc.f)
			short, _ := cerrors.ShortCodeOf(err)
			code, _ := cerrors.CodeOf(err)
			if short != hostShort || code != hostCode {
				t.Errorf("a half report was attached: the error reports %q / %q; the host's own condition "+
					"(%q / %q) should stand", code, short, hostCode, hostShort)
			}
		})
	}
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

// registeredShortCodes reads the short codes registered in the non-test Go files
// of one directory, keyed by code and valued by the file that declares it.
//
// It reads the metadata literal's own ShortCode field rather than matching a
// pattern over the line, so a registration written across several lines is found
// exactly as a single-line one is.
func registeredShortCodes(t *testing.T, dir string) map[string]string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read %s: %v", dir, err)
	}
	out := map[string]string{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
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
