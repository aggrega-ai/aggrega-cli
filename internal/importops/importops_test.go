// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/internal/wasmtest"
	"github.com/aggrega-ai/aggrega-cli/pkg/pluginpath"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// demoEchoBody is the body the demo importer echoes for the "demo" format — one
// drafted Component whose name is the requested format — and the content of the
// matching scaffold file. It mirrors the guest at aggrega-pdk/go/examples/import.
const demoEchoBody = "kind: Component\nmetadata:\n  name: demo\n"

// demoImporterIdentity is the identity and version an attachment names the demo
// importer entity below by.
const demoImporterIdentity = "importer/demo-importer:1.0.0"

// stubImporterResolver returns the demo importer entity for any requested
// format, standing in for the catalog's resolution so a run drives the real
// import socket and the real guest without a catalog on disk.
//
// attachedBy is the position of the attachment it answers as attaching the demo
// importer, or -1 for none. It is an answer the stand-in is given rather than one
// it derives: which attachment names an importer is the catalog's rule, proven
// against a real catalog beside this file, and the cases using this stand-in are
// about what an import does with the attachment it is told about.
type stubImporterResolver struct {
	attachedBy int
}

func (r stubImporterResolver) ImporterPluginForFormat(_ string, attached []string) (types.Plugin, int, error) {
	at := -1
	if r.attachedBy >= 0 && r.attachedBy < len(attached) {
		at = r.attachedBy
	}
	return types.Plugin{
		Metadata: types.PluginMetadata{Name: "demo-importer", Version: "1.0.0", PluginType: "importer"},
		Spec:     types.PluginSpec{Importer: &types.PluginImporter{Capabilities: []string{"import:demo"}}},
	}, at, nil
}

// pointSeamAtDemoGuest points BuildSeam at the built demo importer fixture for
// the duration of one test, restoring it afterwards, with no attachment
// attaching it.
//
// The demo guest echoes its source directly and never draws on the parser seam,
// so the parser pair is left nil; the parser side is exercised at the
// import-socket level rather than through this guest.
func pointSeamAtDemoGuest(t *testing.T) {
	t.Helper()
	pointSeamAtDemoGuestAttachedBy(t, -1)
}

// pointSeamAtDemoGuestAttachedBy is pointSeamAtDemoGuest with the demo importer
// attached by the workspace's attachment at position at.
func pointSeamAtDemoGuestAttachedBy(t *testing.T, at int) {
	t.Helper()
	fixture := wasmtest.EnsureFixtureBuilt(t, "import-go.wasm")
	prev := BuildSeam
	t.Cleanup(func() { BuildSeam = prev })
	BuildSeam = func(_ *workspaceconfig.ResolvedWorkspace, _ PluginBytesResolver) (Seam, error) {
		return Seam{
			ImporterRes: stubImporterResolver{attachedBy: at},
			ImporterLoad: func(p types.Plugin) (wasm.PluginSpec, error) {
				return wasm.PluginSpec{Name: p.Coordinate(), Path: fixture}, nil
			},
		}, nil
	}
}

// aWorkspace resolves a real workspace under a fresh scratch directory, with the
// global configuration layer redirected into that same scratch tree.
//
// It runs the single real resolution path rather than fabricating a value, which
// is what the sibling operations layers do. The redirect matters as much as the
// resolution: without it a run here would read whatever configuration this
// machine happens to carry, so the test's subject would depend on the machine it
// ran on.
func aWorkspace(t *testing.T) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write workspace config: %v", err)
	}
	return resolveWorkspaceAt(t, wsDir)
}

// aWorkspaceAttaching resolves a real workspace, as aWorkspace does, whose
// configuration carries the plugins.importer attachments given, in order.
func aWorkspaceAttaching(t *testing.T, attachments ...importopstest.Attachment) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	importopstest.WriteConfiguration(t, wsDir, attachments...)
	return resolveWorkspaceAt(t, wsDir)
}

// aWorkspaceDir creates the directory a scratch workspace is rooted at, inside a
// fresh scratch tree that also holds its redirected global layer.
func aWorkspaceDir(t *testing.T) string {
	t.Helper()
	wsDir := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	return wsDir
}

// resolveWorkspaceAt resolves the workspace whose configuration the caller wrote
// at wsDir, with the global configuration layer redirected into the scratch tree
// wsDir sits in.
func resolveWorkspaceAt(t *testing.T, wsDir string) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	root := filepath.Dir(wsDir)
	// The redirected global layer must EXIST, not merely be pointed at: the
	// resolution refuses a configured global path that is absent rather than
	// treating it as a layer that contributed nothing. Writing an empty document
	// is what makes the redirect a real empty layer instead of a failure.
	globalPath := filepath.Join(root, "global", workspaceconfig.ConfigFilename)
	if err := os.MkdirAll(filepath.Dir(globalPath), 0o755); err != nil {
		t.Fatalf("mkdir global layer: %v", err)
	}
	if err := os.WriteFile(globalPath, []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	lookup := func(k string) string {
		if k == workspaceconfig.EnvConfigOverride {
			return globalPath
		}
		return ""
	}
	rw, err := workspaceconfig.Resolve(wsDir, "", lookup)
	if err != nil {
		t.Fatalf("resolve workspace: %v", err)
	}
	return rw
}

// noopResolver satisfies the plugin-bytes seam for a run whose loader is pointed
// past it.
func noopResolver(*workspaceconfig.ResolvedWorkspace, string, map[string]pluginpath.ColocatedDir) (pluginpath.ResolvedPlugin, error) {
	return pluginpath.ResolvedPlugin{}, nil
}

// TestSelectFormat exercises the format-selection contract: the Langflow shape's
// nested data.nodes resolves to "langflow"; the Flowise shape's flat
// nodes+edges resolves to "flowise"; a JSON shape matching neither halts with a
// loud diagnostic rather than guessing; and an explicit format wins over
// whatever the content would detect.
func TestSelectFormat(t *testing.T) {
	t.Parallel()

	langflow := []byte(`{"data":{"nodes":[],"edges":[]}}`)
	flowise := []byte(`{"nodes":[],"edges":[]}`)

	cases := []struct {
		name     string
		explicit string
		source   string
		content  []byte
		want     string
		wantErr  bool
	}{
		{name: "langflow nested data.nodes", source: "flow.json", content: langflow, want: "langflow"},
		{name: "flowise flat nodes+edges", source: "chatflow.json", content: flowise, want: "flowise"},
		{name: "ambiguous json is a loud fall-through", source: "x.json", content: []byte(`{"foo":"bar"}`), wantErr: true},
		{name: "garbage is a loud fall-through", source: "x.json", content: []byte("not json at all"), wantErr: true},
		{name: "top-level array is a loud fall-through", source: "x.json", content: []byte(`[1,2,3]`), wantErr: true},
		{name: "a null data.nodes is not an array", source: "x.json", content: []byte(`{"data":{"nodes":null}}`), wantErr: true},
		{name: "explicit format wins over langflow content", explicit: "flowise", source: "flow.json", content: langflow, want: "flowise"},
		{name: "explicit format wins over garbage content", explicit: "langflow", source: "x.json", content: []byte("not json"), want: "langflow"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := SelectFormat(tc.explicit, tc.source, tc.content)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("SelectFormat(%q, %q, ...) = %q, nil; want error", tc.explicit, tc.source, got)
				}
				var undetected *UndetectedFormatError
				if !errors.As(err, &undetected) {
					t.Fatalf("SelectFormat refusal must be an *UndetectedFormatError so a surface can re-word it; got %T", err)
				}
				if undetected.Path() != tc.source {
					t.Fatalf("UndetectedFormatError.Path() = %q; want %q", undetected.Path(), tc.source)
				}
				return
			}
			if err != nil {
				t.Fatalf("SelectFormat(%q, %q, ...): unexpected error: %v", tc.explicit, tc.source, err)
			}
			if got != tc.want {
				t.Fatalf("SelectFormat(%q, %q, ...) = %q; want %q", tc.explicit, tc.source, got, tc.want)
			}
		})
	}
}

// TestUndetectedFormatMessageNamesTheTerminalRemedy pins the sentence the
// command line has always printed, so lifting the subsystem left that surface's
// output unchanged. A protocol surface re-words it through the type instead.
func TestUndetectedFormatMessageNamesTheTerminalRemedy(t *testing.T) {
	t.Parallel()
	err := &UndetectedFormatError{Source: "flow.json"}
	want := `cannot detect the source format of "flow.json"; pass --format to name it explicitly ` +
		`(for example --format langflow or --format flowise)`
	if err.Error() != want {
		t.Fatalf("Error() = %q; want %q", err.Error(), want)
	}
}

// TestImportWritesTheDraftedFiles drives the real demo importer guest and
// asserts the echoed scaffold file lands on disk under the named output
// directory, with the written set naming it.
func TestImportWritesTheDraftedFiles(t *testing.T) {
	pointSeamAtDemoGuest(t)
	out := t.TempDir()

	res, err := Import(context.Background(), aWorkspace(t), Options{
		Source:        []byte("demo source"),
		SourceName:    "demo-source.txt",
		Format:        "demo",
		OutputDir:     out,
		ResolvePlugin: noopResolver,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	got, rerr := os.ReadFile(filepath.Join(out, "demo.yaml"))
	if rerr != nil {
		t.Fatalf("read written demo.yaml: %v", rerr)
	}
	if string(got) != demoEchoBody {
		t.Fatalf("written demo.yaml = %q; want %q", string(got), demoEchoBody)
	}
	if len(res.Written) != 1 || res.Written[0] != "demo.yaml" {
		t.Fatalf("Written = %v; want exactly [demo.yaml]", res.Written)
	}
	if len(res.Files) != 1 || res.Files[0].Content != demoEchoBody {
		t.Fatalf("Files must carry the content as well as the path; got %+v", res.Files)
	}
	if res.Format != "demo" {
		t.Fatalf("Format = %q; want demo", res.Format)
	}
}

// TestImportUnderAReportingDeliveryWritesNothing proves the reporting delivery
// returns the same file set and leaves the output directory untouched.
func TestImportUnderAReportingDeliveryWritesNothing(t *testing.T) {
	pointSeamAtDemoGuest(t)
	out := t.TempDir()

	res, err := Import(context.Background(), aWorkspace(t), Options{
		Source:        []byte("demo source"),
		SourceName:    "demo-source.txt",
		Format:        "demo",
		OutputDir:     out,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: noopResolver,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "demo.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("a reporting delivery must write nothing; stat err = %v", statErr)
	}
	if len(res.Written) != 0 {
		t.Fatalf("Written = %v; want empty under a reporting delivery", res.Written)
	}
	if len(res.Files) != 1 || res.Files[0].Path != "demo.yaml" || res.Files[0].Content != demoEchoBody {
		t.Fatalf("a reporting delivery must carry the same set a write puts on disk; got %+v", res.Files)
	}
	entries, _ := os.ReadDir(out)
	if len(entries) != 0 {
		t.Fatalf("output directory must be untouched; it holds %d entry/entries", len(entries))
	}
}

// TestAnImportNamingNoOutputDirectoryUsesTheWorkspaceRoot holds the one home of
// the default destination: an import handed no output directory writes beneath
// the root of the workspace it is handed, and reports that root as the directory
// its drafted paths are relative to, under both deliveries — never the directory
// the process happens to be sitting in.
//
// The process is moved to a folder outside the workspace first, so a default
// read from the process's folder and one read from the workspace are two
// different places and the assertions below can tell them apart.
//
// 🔴 PLANTED DEFECT: have outputDirFor (internal/importops/import.go) return "."
// rather than rw.Root() for an empty name, and both rows redden — the written row
// finds demo.yaml in the process's folder instead of at the root, and both name
// "." as the root their paths are relative to.
//
// Not parallel: it moves the process working directory and points the shared
// import seam.
func TestAnImportNamingNoOutputDirectoryUsesTheWorkspaceRoot(t *testing.T) {
	pointSeamAtDemoGuest(t)

	for _, row := range []struct {
		name   string
		dryRun bool
	}{
		{name: "writing the drafts"},
		{name: "reporting the drafts", dryRun: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			rw := aWorkspace(t)
			elsewhere := t.TempDir()
			t.Chdir(elsewhere)

			res, err := Import(context.Background(), rw, Options{
				Source:        []byte("demo source"),
				SourceName:    "demo-source.txt",
				Format:        "demo",
				Delivery:      authoredcontent.DeliveryFor(row.dryRun),
				ResolvePlugin: noopResolver,
			})
			if err != nil {
				t.Fatalf("Import: %v", err)
			}
			if res.OutputDir != rw.Root() {
				t.Errorf("the drafted paths are reported relative to %q; want the workspace root %q",
					res.OutputDir, rw.Root())
			}
			if entries, rerr := os.ReadDir(elsewhere); rerr != nil || len(entries) != 0 {
				t.Errorf("the import touched the folder the process runs in (%v): %v", rerr, entries)
			}
			_, statErr := os.Stat(filepath.Join(rw.Root(), "demo.yaml"))
			if row.dryRun {
				if !os.IsNotExist(statErr) {
					t.Errorf("a reporting delivery wrote demo.yaml at the workspace root (stat: %v)", statErr)
				}
				return
			}
			body, rerr := os.ReadFile(filepath.Join(rw.Root(), "demo.yaml"))
			if rerr != nil {
				t.Fatalf("the draft is not at the workspace root: %v", rerr)
			}
			if string(body) != demoEchoBody {
				t.Errorf("the draft at the workspace root holds %q; want %q", body, demoEchoBody)
			}
		})
	}
}

// 🔴 TestImportRefusesAnImporterPathEscapingTheOutputRoot is the containment
// proof, and it is driven through the REAL guest rather than a fabricated file
// list: the demo importer names its scaffold file from a setting, so a setting
// carrying a climbing segment, handed by the attachment attaching it, makes the
// PLUGIN return the escaping path.
//
// It asserts three things, because refusing is only two thirds of the contract:
// the refusal happens, it names the offending path, and nothing was written
// anywhere — neither inside the output root nor outside it.
func TestImportRefusesAnImporterPathEscapingTheOutputRoot(t *testing.T) {
	pointSeamAtDemoGuestAttachedBy(t, 0)

	base := t.TempDir()
	out := filepath.Join(base, "out")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatalf("mkdir output root: %v", err)
	}

	rw := aWorkspaceAttaching(t, importopstest.Attachment{
		URI:      demoImporterIdentity,
		Settings: map[string]any{"target_namespace": "../escaped"},
	})
	_, err := Import(context.Background(), rw, Options{
		Source:        []byte("demo source"),
		SourceName:    "demo-source.txt",
		Format:        "demo",
		OutputDir:     out,
		ResolvePlugin: noopResolver,
	})
	if err == nil {
		t.Fatal("an importer path climbing out of the output root must be refused, not written")
	}
	var escaping *EscapingPathError
	if !errors.As(err, &escaping) {
		t.Fatalf("the refusal must be an *EscapingPathError so a surface can name the path; got %T: %v", err, err)
	}
	if escaping.Offending() != "../escaped-demo.yaml" {
		t.Fatalf("Offending() = %q; want the path the importer returned, ../escaped-demo.yaml", escaping.Offending())
	}
	if !strings.Contains(escaping.Error(), "../escaped-demo.yaml") {
		t.Fatalf("the message must name the offending path; got %q", escaping.Error())
	}

	// Nothing anywhere: not outside the root, and not inside it either.
	if _, statErr := os.Stat(filepath.Join(base, "escaped-demo.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("bytes escaped the output root; stat err = %v", statErr)
	}
	inside, _ := os.ReadDir(out)
	if len(inside) != 0 {
		t.Fatalf("a refused import must write nothing at all; the output root holds %d entry/entries", len(inside))
	}
}

// TestImportRefusesEveryEscapingShape drives the containment predicate over the
// shapes a plugin could return, so a later change that reached for a
// string-prefix comparison instead reddens here.
//
// The contained rows matter as much as the refused ones: a guard that refused an
// interior "a/../b" or a name merely beginning with two dots would reject
// legitimate importer output, and a bare-prefix comparison against the root
// would wrongly accept a sibling directory whose name extends it.
func TestImportRefusesEveryEscapingShape(t *testing.T) {
	pointSeamAtDemoGuest(t)

	cases := []struct {
		name    string
		path    string
		refused bool
	}{
		{name: "climbs one level", path: "../out.yaml", refused: true},
		{name: "climbs several levels", path: "../../../etc/out.yaml", refused: true},
		{name: "climbs after descending", path: "a/b/../../../out.yaml", refused: true},
		{name: "bare parent", path: "..", refused: true},
		{name: "absolute", path: "/etc/out.yaml", refused: true},
		{name: "empty", path: "", refused: true},
		{name: "plain name is contained", path: "out.yaml"},
		{name: "nested name is contained", path: "a/b/out.yaml"},
		{name: "interior climb resolving inside is contained", path: "a/../b/out.yaml"},
		{name: "a name beginning with two dots is an ordinary name", path: "..config.yaml"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			_, err := writeFiles(out, []abi.ScaffoldFile{{Path: tc.path, Content: "x"}})
			var escaping *EscapingPathError
			refused := errors.As(err, &escaping)
			if refused != tc.refused {
				t.Fatalf("writeFiles(%q) refused = %v (err %v); want refused = %v", tc.path, refused, err, tc.refused)
			}
		})
	}
}

// 🔴 TestImportRefusesTheWholeSetWhenOnePathEscapes pins the ordering half of
// the guard: the contained file in the same set must not reach disk, because a
// refused run leaving a partial prefix behind is a result nobody asked for.
func TestImportRefusesTheWholeSetWhenOnePathEscapes(t *testing.T) {
	out := t.TempDir()
	_, err := writeFiles(out, []abi.ScaffoldFile{
		{Path: "contained.yaml", Content: "first"},
		{Path: "../escaped.yaml", Content: "second"},
	})
	if err == nil {
		t.Fatal("a set carrying an escaping path must be refused")
	}
	if _, statErr := os.Stat(filepath.Join(out, "contained.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("the contained file was written before the set was refused; stat err = %v", statErr)
	}
}

// TestHaltedIsOneSpellingOfTheOutcome pins the predicate both surfaces read, so
// the command line's exit outcome and the wire answer's field cannot come to
// disagree about whether one import completed.
func TestHaltedIsOneSpellingOfTheOutcome(t *testing.T) {
	t.Parallel()
	res := Result{Diagnostics: []abi.Diagnostic{
		{Severity: "warn", Message: "one node was not mapped"},
		{Severity: abi.SeverityHalt, Message: "an edge referenced an unknown node"},
	}}
	if !res.Halted() {
		t.Fatal("a halt-severity diagnostic must make Halted report true")
	}
	warnOnly := Result{Diagnostics: []abi.Diagnostic{{Severity: "warn", Message: "one node was not mapped"}}}
	if warnOnly.Halted() {
		t.Fatal("a warn-only set must not report halted")
	}
	if (Result{}).Halted() {
		t.Fatal("an empty diagnostic set must not report halted")
	}
}

// TestImportRefusesWithoutAWorkspaceOrAResolver pins both wiring refusals, and
// pins the workspace one's sentence, which names the remedy a reader outside a
// workspace has to apply.
func TestImportRefusesWithoutAWorkspaceOrAResolver(t *testing.T) {
	t.Parallel()

	_, err := Import(context.Background(), nil, Options{ResolvePlugin: noopResolver})
	if err == nil {
		t.Fatal("a nil workspace must be refused")
	}
	want := "no workspace configuration resolved; run import from within a workspace whose catalog holds an " +
		"importer plugin advertising the source's format"
	if err.Error() != want {
		t.Fatalf("nil-workspace refusal = %q; want %q", err.Error(), want)
	}

	_, err = Import(context.Background(), &workspaceconfig.ResolvedWorkspace{}, Options{})
	if err == nil {
		t.Fatal("a nil plugin resolver must be refused rather than defaulted")
	}
	if !strings.Contains(err.Error(), "plugin resolver") {
		t.Fatalf("the resolver refusal must name what is missing; got %q", err.Error())
	}
}

// TestAnImportReadsOnlyTheAttachmentAtThePositionItIsNamed pins what an import
// hands an importer, given the position of the attachment the resolution
// reported: that attachment's with-config and no other's, and nothing at all
// where no attachment is named — including where the workspace declares exactly
// one attachment, which names some other importer.
//
// A position past the end of the list names no attachment the workspace
// declares, so it is refused rather than read as none.
func TestAnImportReadsOnlyTheAttachmentAtThePositionItIsNamed(t *testing.T) {
	t.Parallel()
	settingsOf := func(ns string) *workspaceconfig.PluginRefWith {
		return &workspaceconfig.PluginRefWith{Settings: map[string]any{"target_namespace": ns}}
	}
	two := []workspaceconfig.PluginRef{
		{URI: "importer/first:1.0.0", With: settingsOf("first-ns")},
		{URI: "importer/second:1.0.0", With: settingsOf("second-ns")},
	}
	sole := []workspaceconfig.PluginRef{{URI: "importer/elsewhere:1.0.0", With: settingsOf("elsewhere-ns")}}

	for _, tc := range []struct {
		name    string
		refs    []workspaceconfig.PluginRef
		at      int
		want    string
		refused bool
	}{
		{name: "the second of two attachments", refs: two, at: 1, want: `{"target_namespace":"second-ns"}`},
		{name: "the first of two attachments", refs: two, at: 0, want: `{"target_namespace":"first-ns"}`},
		{name: "no attachment named, of two", refs: two, at: -1},
		{name: "no attachment named, of one", refs: sole, at: -1},
		{name: "a position past the end", refs: two, at: 2, refused: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			sources, settings, err := attachmentInputs(tc.refs, tc.at)
			if tc.refused {
				if err == nil {
					t.Fatalf("position %d of %d attachments was read as %q rather than refused", tc.at, len(tc.refs), settings)
				}
				return
			}
			if err != nil {
				t.Fatalf("attachmentInputs: %v", err)
			}
			if sources != nil {
				t.Errorf("sources = %+v; none of these attachments declares any", sources)
			}
			if string(settings) != tc.want {
				t.Errorf("settings = %q; want %q", settings, tc.want)
			}
		})
	}
}

// TestImportSourcesAreNameSortedAndLoudOnAMiss proves the with.sources path:
// glob-matched files are read into base-name-sorted named sources, and a pattern
// matching nothing is a loud miss rather than a silent empty import.
func TestImportSourcesAreNameSortedAndLoudOnAMiss(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for name, body := range map[string]string{"beta.json": `{"b":2}`, "alpha.json": `{"a":1}`} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	sources, _, err := withInputs(&workspaceconfig.PluginRefWith{
		Sources: []string{filepath.Join(dir, "*.json")},
	})
	if err != nil {
		t.Fatalf("withInputs: %v", err)
	}
	if len(sources) != 2 || sources[0].Name != "alpha.json" || sources[1].Name != "beta.json" {
		t.Fatalf("sources must be base-name sorted; got %+v", sources)
	}

	if _, _, missErr := withInputs(&workspaceconfig.PluginRefWith{
		Sources: []string{filepath.Join(dir, "*.nothing")},
	}); missErr == nil {
		t.Fatal("a pattern matching no files must be a loud miss")
	}
}

// TestSettingsReachTheGuest proves the with.settings path end-to-end: a setting
// on the attachment attaching the importer flows through Import into the guest,
// which prefixes the emitted entity name with it — so the drafted file lands
// under the prefixed name rather than the bare format.
func TestSettingsReachTheGuest(t *testing.T) {
	pointSeamAtDemoGuestAttachedBy(t, 0)
	out := t.TempDir()

	rw := aWorkspaceAttaching(t, importopstest.Attachment{
		URI:      demoImporterIdentity,
		Settings: map[string]any{"target_namespace": "prod"},
	})
	res, err := Import(context.Background(), rw, Options{
		Source:        []byte("demo source"),
		SourceName:    "demo-source.txt",
		Format:        "demo",
		OutputDir:     out,
		ResolvePlugin: noopResolver,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	want := "kind: Component\nmetadata:\n  name: prod-demo\n"
	got, rerr := os.ReadFile(filepath.Join(out, "prod-demo.yaml"))
	if rerr != nil {
		t.Fatalf("read prod-demo.yaml (target_namespace must prefix the entity name): %v", rerr)
	}
	if string(got) != want {
		t.Fatalf("written prod-demo.yaml = %q; want %q", string(got), want)
	}
	if _, statErr := os.Stat(filepath.Join(out, "demo.yaml")); !os.IsNotExist(statErr) {
		t.Fatalf("demo.yaml must not exist when target_namespace prefixes the name; stat err = %v", statErr)
	}
	if len(res.Written) != 1 || res.Written[0] != "prod-demo.yaml" {
		t.Fatalf("Written = %v; want [prod-demo.yaml]", res.Written)
	}
}

// TestSourcesReachTheGuest proves the with.sources path end-to-end: several
// glob-matched files named by the attachment attaching the importer are threaded
// into the guest, which reflects each source name back as a diagnostic,
// confirming the host delivered every source.
func TestSourcesReachTheGuest(t *testing.T) {
	pointSeamAtDemoGuestAttachedBy(t, 0)

	srcDir := t.TempDir()
	for name, body := range map[string]string{"beta.json": `{"b":2}`, "alpha.json": `{"a":1}`} {
		if err := os.WriteFile(filepath.Join(srcDir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	rw := aWorkspaceAttaching(t, importopstest.Attachment{
		URI:     demoImporterIdentity,
		Sources: []string{filepath.Join(srcDir, "*.json")},
	})
	res, err := Import(context.Background(), rw, Options{
		Source:        []byte("demo source"),
		SourceName:    "demo-source.txt",
		Format:        "demo",
		OutputDir:     t.TempDir(),
		ResolvePlugin: noopResolver,
	})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	var joined strings.Builder
	for _, d := range res.Diagnostics {
		joined.WriteString(d.Message)
		joined.WriteString("\n")
	}
	got := joined.String()
	if !strings.Contains(got, "received source: alpha.json") || !strings.Contains(got, "received source: beta.json") {
		t.Fatalf("guest did not reflect both sources; diagnostics = %q", got)
	}
}

// TestTheSeamHasOneSpelling guards the seam against the alias that cannot be
// pointed: a package-level variable initialised from BuildSeam would copy the
// function value at initialisation, so pointing BuildSeam would not reach it.
// Driving a run through a pointed seam is what proves the read happens at call
// time.
func TestTheSeamHasOneSpelling(t *testing.T) {
	prev := BuildSeam
	t.Cleanup(func() { BuildSeam = prev })

	called := false
	BuildSeam = func(*workspaceconfig.ResolvedWorkspace, PluginBytesResolver) (Seam, error) {
		called = true
		return Seam{}, errSeamProbe
	}
	_, err := Import(context.Background(), aWorkspace(t), Options{
		Source:        []byte("x"),
		Format:        "demo",
		ResolvePlugin: noopResolver,
	})
	if !called {
		t.Fatal("Import must read BuildSeam at call time, not hold a value captured earlier")
	}
	if !strings.Contains(err.Error(), "seam probe") {
		t.Fatalf("the pointed seam's error must surface; got %v", err)
	}
}

// errSeamProbe is the sentinel the seam probe above returns.
var errSeamProbe = errors.New("seam probe")
