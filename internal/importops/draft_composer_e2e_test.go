// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs of what an import given a target composes around the drafts
// its importer returned: the Manifest the drafted Release builds against, the
// Release's reference to it, the warnings naming every type left unmapped, and the
// refusal of a target no Recipe of the catalog binds to.
//
// Each case imports through the real Import over a workspace whose catalog folder
// holds the draft-composition catalog — three Components, example/x, example/y and
// example/z, and the Recipes implementing them: one compose Recipe for example/x
// (held at two versions), two compose Recipes for example/y, and only a helm one
// for example/z — and the draft-set importer, which drafts exactly the files its
// source spells out, with its module beside its entity. The source drafts an
// Aggregate declaring instances of the three types and a Release naming no
// Manifest.

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// draftComposerWorkspace resolves a scratch workspace whose conventional catalog
// directory holds the draft-composition catalog and the draft-set importer, and
// returns it with the path of a source drafting body.
func draftComposerWorkspace(t *testing.T, body string) (*workspaceconfig.ResolvedWorkspace, string) {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	importopstest.WriteConfiguration(t, wsDir)
	importopstest.LayDraftComposerCatalog(t, wsDir, filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir))
	rw := resolveWorkspaceAt(t, wsDir)
	return rw, importopstest.WriteSource(t, filepath.Join(t.TempDir(), "drafts.txt"), body)
}

// composedImport imports source over rw for target into outputDir, under the
// delivery given, failing the test where the import is refused.
func composedImport(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, source, target, outputDir string,
	delivery authoredcontent.Delivery) Result {
	t.Helper()
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    source,
		SourceName:    source,
		Format:        importopstest.DraftSetFormat,
		Target:        target,
		OutputDir:     outputDir,
		Delivery:      delivery,
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if err != nil {
		t.Fatalf("import for the target %q: %v", target, err)
	}
	return res
}

// fileNamed returns the content of the file at path a result carries, failing the
// test where it carries none.
func fileNamed(t *testing.T, res Result, path string) string {
	t.Helper()
	for _, f := range res.Files {
		if f.Path == path {
			return f.Content
		}
	}
	t.Fatalf("the import drafted no %s; it drafted %v", path, draftPaths(res))
	return ""
}

// wantComposedWarnings are the warnings an import given the compose target over
// the draft-composition catalog reports after the importer's own diagnostics, in
// the order the Aggregate declares the types: example/y with both of its compose
// Recipes named, then example/z, which no compose Recipe implements.
var wantComposedWarnings = []abi.Diagnostic{
	{Severity: abi.SeverityWarn, Message: `the drafted Manifest leaves the component type "example/y:1.0.0" unmapped: ` +
		`the workspace's catalog holds more than one Recipe binding to the target "compose" that implements it ` +
		`("compose/y-one", "compose/y-two"), and the import does not choose between them, so a build of the ` +
		`drafts halts naming each instance of it until the one wanted is mapped`},
	{Severity: abi.SeverityWarn, Message: `the drafted Manifest leaves the component type "example/z:1.0.0" unmapped: ` +
		`no Recipe in the workspace's catalog binds to the target "compose" and implements it, so a build of the ` +
		`drafts halts naming each instance of it until a mapping is added`},
}

// TestATargetDraftsTheManifestTheDraftedReleaseNeeds imports the draft set for the
// compose target under a reporting delivery and reads the three files and the
// warnings back: the Manifest maps example/x alone, to its one compose Recipe by
// the capability-qualified reference with no version, though the catalog holds it
// at two versions; the Release names that Manifest, its comment line replaced and
// every other byte kept; the Aggregate is exactly as drafted; and the warnings
// name example/y with both of its Recipes and example/z.
//
// 🔴 PLANTED DEFECTS:
//   - drop the capability comparison in mappingFor (internal/importops/compose.go):
//     example/z then maps to helm/z-chart and the Manifest row reddens, as does the
//     warning row, which loses example/z.
//   - treat two versions of one Recipe as two (append the version to ref in
//     mappingFor): example/x is then left unmapped and both rows redden.
//   - have composeDraftSet keep the Release unedited: the Release row reddens.
func TestATargetDraftsTheManifestTheDraftedReleaseNeeds(t *testing.T) {
	t.Parallel()
	rw, source := draftComposerWorkspace(t, importopstest.DraftComposerSource())
	res := composedImport(t, rw, source, "compose", "", authoredcontent.DeliveryReport)

	if got, want := draftPaths(res), []string{"aggregate.yaml", ManifestFileName, "release.yaml"}; !slices.Equal(got, want) {
		t.Fatalf("the import drafted %v; want %v", got, want)
	}
	if got := fileNamed(t, res, ManifestFileName); got != importopstest.DraftedManifestComposed {
		t.Errorf("the drafted Manifest is\n%s\nwant\n%s", got, importopstest.DraftedManifestComposed)
	}
	if got := fileNamed(t, res, "release.yaml"); got != importopstest.DraftedReleaseComposed {
		t.Errorf("the drafted Release is\n%s\nwant\n%s", got, importopstest.DraftedReleaseComposed)
	}
	if got := fileNamed(t, res, "aggregate.yaml"); got != importopstest.DraftedAggregate {
		t.Errorf("the drafted Aggregate changed:\n%s\nwant it as drafted\n%s", got, importopstest.DraftedAggregate)
	}
	if !slices.Equal(res.Diagnostics, wantComposedWarnings) {
		t.Errorf("the import reported %+v\nwant %+v", res.Diagnostics, wantComposedWarnings)
	}
	if res.Halted() {
		t.Errorf("an import leaving types unmapped reported a halt; it warns and drafts")
	}
	// The Manifest is a draft like any other: the record lists it.
	if record := fileNamed(t, res, RecordFileName); !strings.Contains(record, `"path": "manifest.yaml"`) {
		t.Errorf("the record of what the import wrote does not list the Manifest:\n%s", record)
	}
}

// TestNoTargetDraftsExactlyTheImportersFiles imports the same draft set with no
// target and holds the result to the importer's own files and nothing else: no
// Manifest, the Release with its comment line, the Aggregate as drafted, and no
// diagnostic the composition would add.
//
// 🔴 PLANTED DEFECT: have composeDraftSet ignore an empty target (drop its first
// return): it then drafts a Manifest for the empty capability, and this test
// reddens on the drafted paths.
func TestNoTargetDraftsExactlyTheImportersFiles(t *testing.T) {
	t.Parallel()
	rw, source := draftComposerWorkspace(t, importopstest.DraftComposerSource())
	res := composedImport(t, rw, source, "", "", authoredcontent.DeliveryReport)

	want := []abi.ScaffoldFile{
		{Path: "aggregate.yaml", Content: importopstest.DraftedAggregate},
		{Path: "release.yaml", Content: importopstest.DraftedRelease},
	}
	got := slices.DeleteFunc(slices.Clone(res.Files), func(f abi.ScaffoldFile) bool { return f.Path == RecordFileName })
	if !slices.Equal(got, want) {
		t.Errorf("an import with no target drafted %+v\nwant exactly the importer's files %+v", got, want)
	}
	if len(res.Diagnostics) != 0 {
		t.Errorf("an import with no target reported %+v; want nothing", res.Diagnostics)
	}
}

// TestATargetComposesTheSameBytesOnEveryRun imports the draft set for the compose
// target twice, each over its own fresh workspace, and holds the two results to
// byte-identical files and diagnostics.
func TestATargetComposesTheSameBytesOnEveryRun(t *testing.T) {
	t.Parallel()
	var runs [2]Result
	for i := range runs {
		rw, source := draftComposerWorkspace(t, importopstest.DraftComposerSource())
		runs[i] = composedImport(t, rw, source, "compose", "", authoredcontent.DeliveryReport)
	}
	if !slices.Equal(runs[0].Files, runs[1].Files) {
		t.Errorf("two runs drafted different files:\n%+v\n%+v", runs[0].Files, runs[1].Files)
	}
	if !slices.Equal(runs[0].Diagnostics, runs[1].Diagnostics) {
		t.Errorf("two runs reported different diagnostics:\n%+v\n%+v", runs[0].Diagnostics, runs[1].Diagnostics)
	}
}

// TestTheComposedDraftsBuildOnceEveryTypeIsMapped writes the drafts the compose
// target composes and builds them through the build-faithful harness over the
// same catalog: as drafted the build halts on the unmapped-instance condition,
// naming the instances of example/y and example/z and only those; with those two
// types mapped by hand — example/y to one of its compose Recipes, example/z to its
// helm Recipe — it builds, rendering example/x and example/y through the compose
// provider and example/z through the helm one.
//
// 🔴 PLANTED DEFECTS:
//   - have mappingFor (internal/importops/compose.go) keep the first of several
//     Recipes rather than leave the type unmapped (return references[0] for any
//     non-empty list): example/y is then mapped, and the halted-instance row
//     reddens naming z-one alone.
//   - drop the capability comparison in mappingFor: example/z is then mapped to
//     helm/z-chart, so the halted-instance row reddens naming y-one alone, and
//     the hand-added example/z key repeats one the draft already wrote, so the
//     Manifest no longer loads and the rendered-file row fails.
func TestTheComposedDraftsBuildOnceEveryTypeIsMapped(t *testing.T) {
	t.Parallel()
	rw, source := draftComposerWorkspace(t, importopstest.DraftComposerSource())
	out := filepath.Join(t.TempDir(), "drafts")
	composedImport(t, rw, source, "compose", out, authoredcontent.DeliveryWrite)
	in := pipelinetest.Input{
		ReleasePath:  filepath.Join(out, "release.yaml"),
		CatalogPaths: []string{importopstest.DraftComposerCatalogDir(t)},
	}

	_, err := pipelinetest.BuildE(t, in)
	if !errors.Is(err, catalog.ErrNoRecipeResolved()) {
		t.Fatalf("the drafts as composed built with %v; want the unmapped-instance halt", err)
	}
	halted := haltedInstances(err)
	sort.Strings(halted)
	if want := []string{".spec.components.y-one", ".spec.components.z-one"}; !slices.Equal(halted, want) {
		t.Errorf("the build halted naming %q; want exactly %q", halted, want)
	}

	manifest := filepath.Join(out, ManifestFileName)
	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("read the drafted Manifest: %v", err)
	}
	mapped := string(body) + "        example/y:1.0.0: compose/y-one\n        example/z:1.0.0: helm/z-chart\n"
	if err := os.WriteFile(manifest, []byte(mapped), 0o644); err != nil {
		t.Fatalf("map the two types by hand: %v", err)
	}
	rendered := pipelinetest.Build(t, in).Artifacts
	// The artifact keys are the paths the build reports, relative to the output
	// directory it writes them beneath.
	var files []string
	for path := range rendered {
		files = append(files, path)
	}
	sort.Strings(files)
	want := []string{"compose/docker-compose.yaml", "helm/z-one-deployment.yaml", "helm/z-one-svc-service.yaml"}
	if !slices.Equal(files, want) {
		t.Errorf("the mapped drafts rendered %v; want %v", files, want)
	}
}

// haltedInstances returns the place every unmapped-instance finding a refused
// build carries names, read off the findings themselves rather than off the
// message text.
//
// The rules over the flattened composition report an instance no mapping
// resolves as a positioned finding each, grouped into one refusal whose
// violations keep each finding's place, and the build wraps that group in a frame
// of its own naming the gate. So the group is read out of the chain and its
// violations walked, each kept where it carries the unmapped-instance condition
// by its own matching.
func haltedInstances(err error) []string {
	var group *resolver.MultiResolverError
	if !errors.As(err, &group) {
		return nil
	}
	var out []string
	for _, v := range group.Violations {
		if errors.Is(v, catalog.ErrNoRecipeResolved()) {
			out = append(out, v.Field)
		}
	}
	return out
}

// TestAnUnknownTargetIsRefusedBeforeTheImporterRuns imports, for a target no
// Recipe of the catalog binds to, a source drafting a marker file, through the
// real seam with its loader counting the importers it loads, and holds three
// things to the refusal: it is the registered condition, naming the target as
// given and the capabilities the catalog's Recipes bind to; no importer was
// loaded; and nothing was written — the output directory is never created.
//
// 🔴 PLANTED DEFECT: move the checkTarget call in Import
// (internal/importops/import.go) to after importersocket.ImportArtifact: the
// refusal still comes, but the importer-load row reddens with one load.
func TestAnUnknownTargetIsRefusedBeforeTheImporterRuns(t *testing.T) {
	rw, source := draftComposerWorkspace(t,
		importopstest.DraftSetBody(abi.ScaffoldFile{Path: "marker.txt", Content: "the importer ran\n"}))
	var mu sync.Mutex
	loaded := 0
	realSeam := BuildSeam
	t.Cleanup(func() { BuildSeam = realSeam })
	BuildSeam = func(rw *workspaceconfig.ResolvedWorkspace, resolve PluginBytesResolver) (Seam, error) {
		seam, err := realSeam(rw, resolve)
		if err != nil || seam.loadIn == nil {
			return seam, err
		}
		load := seam.loadIn
		seam.loadIn = func(ctx context.Context) func(types.Plugin) (wasm.PluginSpec, error) {
			inner := load(ctx)
			return func(p types.Plugin) (wasm.PluginSpec, error) {
				if p.Metadata.PluginType == "importer" {
					mu.Lock()
					loaded++
					mu.Unlock()
				}
				return inner(p)
			}
		}
		return seam, nil
	}

	out := filepath.Join(t.TempDir(), "out")
	_, err := Import(context.Background(), rw, Options{
		SourcePath:    source,
		SourceName:    source,
		Format:        importopstest.DraftSetFormat,
		Target:        "helmm",
		OutputDir:     out,
		Delivery:      authoredcontent.DeliveryWrite,
		ResolvePlugin: (&recordingCascade{}).resolve,
	})
	if !errors.Is(err, ErrImportTargetUnbound()) {
		t.Fatalf("the import for the target \"helmm\" returned %v; want the unbound-target refusal", err)
	}
	if short, ok := cerrors.ShortCodeOf(err); !ok || short != "E0349" {
		t.Errorf("the refusal carries the short code %q (%v); want E0349", short, ok)
	}
	for _, want := range []string{`target "helmm"`, `the catalog's Recipes bind to "compose", "helm"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal %q does not name %s", err.Error(), want)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if loaded != 0 {
		t.Errorf("the import loaded %d importer(s) before refusing the target; want none", loaded)
	}
	if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
		t.Errorf("the output directory exists after the refusal (%v); want nothing written", statErr)
	}
}

// TestTheTargetIsComposedFromTheSharedOperationAlone parses the import
// operation's own source and the source of every surface that reaches it — the
// command line, the editor's session and the assistant's tools — and holds the
// set of functions calling the composition and the target refusal to the
// operation's entry point alone. Both are unexported, so no surface can call
// either; this holds a second caller inside the operation's own package out too.
//
// 🔴 PLANTED DEFECT: call composeDraftSet from syncImport
// (internal/importops/sync.go) as well: this test reddens naming it.
func TestTheTargetIsComposedFromTheSharedOperationAlone(t *testing.T) {
	t.Parallel()
	units := []struct{ dir, prefix string }{
		{".", "importops"},
		{filepath.Join("..", "..", "cmd", "aggrega"), "main"},
		{filepath.Join("..", "serve"), "serve"},
		{filepath.Join("..", "mcp"), "mcp"},
	}
	callers := map[string][]string{}
	for _, unit := range units {
		fset := token.NewFileSet()
		pkgs, err := parser.ParseDir(fset, unit.dir, func(fi os.FileInfo) bool {
			return !strings.HasSuffix(fi.Name(), "_test.go")
		}, 0)
		if err != nil || len(pkgs) == 0 {
			t.Fatalf("parse the source at %s: %v (%d packages)", unit.dir, err, len(pkgs))
		}
		for _, pkg := range pkgs {
			for _, file := range pkg.Files {
				for _, decl := range file.Decls {
					fn, ok := decl.(*ast.FuncDecl)
					if !ok || fn.Body == nil {
						continue
					}
					ast.Inspect(fn.Body, func(n ast.Node) bool {
						call, ok := n.(*ast.CallExpr)
						if !ok {
							return true
						}
						var name string
						switch f := call.Fun.(type) {
						case *ast.Ident:
							name = f.Name
						case *ast.SelectorExpr:
							name = f.Sel.Name
						}
						if name == "composeDraftSet" || name == "checkTarget" {
							callers[name] = append(callers[name], unit.prefix+"."+fn.Name.Name)
						}
						return true
					})
				}
			}
		}
	}
	for _, callee := range []string{"composeDraftSet", "checkTarget"} {
		if got, want := callers[callee], []string{"importops.Import"}; !slices.Equal(got, want) {
			t.Errorf("%s is called from %v; want from %v alone — the draft set is composed in one place, "+
				"reached by every surface through Import", callee, got, want)
		}
	}
}

// stubRecipes is a Recipe index standing in for a catalog, answering the
// Recipes it holds by the type each implements.
type stubRecipes struct {
	recipes []types.Recipe
}

func (s stubRecipes) RecipeCapabilities() []string {
	var out []string
	for _, r := range s.recipes {
		if !slices.Contains(out, r.Spec.Capability) {
			out = append(out, r.Spec.Capability)
		}
	}
	sort.Strings(out)
	return out
}

func (s stubRecipes) RecipesImplementing(componentType string) []types.Recipe {
	var out []types.Recipe
	for _, r := range s.recipes {
		if r.Spec.Implements == componentType {
			out = append(out, r)
		}
	}
	return out
}

// TestTheCompositionAroundEachShapeOfDraft drives composeDraftSet over the
// shapes of draft set an importer can return, one row each, with a Recipe index
// holding one compose Recipe for example/x: a Release with no comment line where
// the Manifest goes, which gains the key as spec's first entry; a Release
// already naming a Manifest, left as drafted with an information diagnostic; no
// Release, where the Manifest is drafted beside the Aggregate with its name and
// version and nothing references it; no Aggregate, where no Manifest is drafted;
// and a file the importer already drafted where the Manifest would go, left as it
// is with a warning.
func TestTheCompositionAroundEachShapeOfDraft(t *testing.T) {
	t.Parallel()
	recipes := stubRecipes{recipes: []types.Recipe{{
		Metadata: types.RecipeMetadata{Name: "x-server", Version: "1.0.0"},
		Spec:     types.RecipeSpec{Capability: "compose", Implements: "example/x"},
	}}}
	aggregate := "apiVersion: aggrega.ai/v1beta1\nkind: Aggregate\nmetadata:\n  name: app\n  version: 1.2.3\n" +
		"spec:\n  components:\n    a:\n      type: example/x\n"
	manifestFor := func(name, version string) string {
		return "apiVersion: aggrega.ai/v1beta1\nkind: Manifest\nmetadata:\n  name: " + name + "\n  version: " +
			version + "\nspec:\n  mappings:\n    components:\n      type:\n        example/x: compose/x-server\n"
	}
	releaseHead := "apiVersion: aggrega.ai/v1beta1\nkind: Release\nmetadata:\n  name: app-dev\n  environment: dev\n" +
		"  version: 0.2.0\nspec:\n"
	for _, tc := range []struct {
		name      string
		files     []abi.ScaffoldFile
		wantFiles []abi.ScaffoldFile
		wantDiags []string
	}{
		{
			name: "a Release with no comment line where the Manifest goes",
			files: []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: aggregate},
				{Path: "deploy/release.yaml", Content: releaseHead + "  aggregate: app\n  components:\n    a: {}\n"}},
			wantFiles: []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: aggregate},
				{Path: "deploy/release.yaml", Content: releaseHead + "  manifest: app-dev\n  aggregate: app\n  components:\n    a: {}\n"},
				{Path: "deploy/manifest.yaml", Content: manifestFor("app-dev", "0.2.0")}},
		},
		{
			name: "a Release already naming a Manifest",
			files: []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: aggregate},
				{Path: "release.yaml", Content: releaseHead + "  aggregate: app\n  manifest: ops\n  components:\n    a: {}\n"}},
			wantFiles: []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: aggregate},
				{Path: "release.yaml", Content: releaseHead + "  aggregate: app\n  manifest: ops\n  components:\n    a: {}\n"},
				{Path: "manifest.yaml", Content: manifestFor("app-dev", "0.2.0")}},
			wantDiags: []string{`info: the drafted Release "release.yaml" already names the Manifest "ops", so it is ` +
				`left as the importer drafted it; the drafted Manifest "manifest.yaml" is not referenced by it`},
		},
		{
			name:  "no Release",
			files: []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: aggregate}},
			wantFiles: []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: aggregate},
				{Path: "manifest.yaml", Content: manifestFor("app", "1.2.3")}},
			wantDiags: []string{`info: the importer drafted no Release, so nothing references the drafted Manifest ` +
				`"manifest.yaml"; name it as a Release's spec.manifest to build against it`},
		},
		{
			name:      "no Aggregate",
			files:     []abi.ScaffoldFile{{Path: "notes.txt", Content: "nothing to map\n"}},
			wantFiles: []abi.ScaffoldFile{{Path: "notes.txt", Content: "nothing to map\n"}},
			wantDiags: []string{`info: the target "compose" drafts no Manifest, since the importer drafted no ` +
				`Aggregate whose component types it could map`},
		},
		{
			name: "a file already drafted where the Manifest would go",
			files: []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: aggregate},
				{Path: "manifest.yaml", Content: "kept\n"}},
			wantFiles: []abi.ScaffoldFile{{Path: "aggregate.yaml", Content: aggregate},
				{Path: "manifest.yaml", Content: "kept\n"}},
			wantDiags: []string{`warn: the target "compose" drafts no Manifest, since the importer already drafted ` +
				`"manifest.yaml", which is left as it drafted it`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files, diags, err := composeDraftSet("compose", recipes, tc.files)
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			if !slices.Equal(files, tc.wantFiles) {
				t.Errorf("composed\n%+v\nwant\n%+v", files, tc.wantFiles)
			}
			var got []string
			for _, d := range diags {
				got = append(got, d.Severity+": "+d.Message)
			}
			if !slices.Equal(got, tc.wantDiags) {
				t.Errorf("reported %q\nwant %q", got, tc.wantDiags)
			}
		})
	}
}
