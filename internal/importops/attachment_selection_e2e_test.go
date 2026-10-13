// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package importops

// End-to-end coverage for what a plugins.importer attachment does, through a real
// catalog folder holding several importers generated from the reference
// importers' modules, beside the JSON parser they call back to: an attachment
// whose uri names an importer by identity and version attaches it, so it is
// chosen among the importers advertising a format, at the version its uri names,
// and it alone is handed the attachment's settings.
//
// 🔴 Every case runs the REAL seam, as the catalog-folder cases beside this file
// do: Import builds it through [BuildSeam] from the workspace's own catalog and
// configuration, and nothing below points that variable. Which importer ran is
// read twice over — off the importer the plugin cascade was asked for, and off
// the namespace the importer drafted its files under, which is the setting the
// attachments hand it — so a case cannot pass on the right importer handed the
// wrong settings, or on the right settings reaching the wrong importer.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
	"github.com/aggrega-ai/aggrega-cli/pkg/catalog"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// The importer entities the cases lay down, by name.
const (
	importerAlpha = "import-alpha"
	importerBeta  = "import-beta"
	importerGamma = "import-gamma"
)

// The namespaces the attachments hand their importers. The reference importer
// drafts every file under the namespace its settings name, and under
// defaultNamespace when it is handed none.
const (
	northNamespace   = "north"
	southNamespace   = "south"
	defaultNamespace = "imported"
)

// attachedImportWorkspace lays the importers given into a scratch workspace's
// catalog folder, beside the JSON parser, writes the workspace's configuration
// carrying the attachments attach returns for what was laid, and resolves it.
// attach may be nil, for a workspace attaching nothing.
func attachedImportWorkspace(t *testing.T, importers []importopstest.Importer,
	attach func(laid map[string]importopstest.LaidPlugin) []importopstest.Attachment) *workspaceconfig.ResolvedWorkspace {
	t.Helper()
	wsDir := aWorkspaceDir(t)
	laidList, parser := importopstest.LayImporters(t, wsDir,
		filepath.Join(wsDir, workspaceconfig.ConventionalCatalogDir), importers...)
	laid := map[string]importopstest.LaidPlugin{parser.Name: parser}
	for _, l := range laidList {
		laid[l.Name] = l
	}
	var attachments []importopstest.Attachment
	if attach != nil {
		attachments = attach(laid)
	}
	importopstest.WriteConfiguration(t, wsDir, attachments...)
	rw := resolveWorkspaceAt(t, wsDir)
	if _, err := os.Stat(filepath.Join(rw.Root(), workspaceconfig.ConfigFilename)); err != nil {
		t.Fatalf("the workspace resolved at %s, which is not the directory its configuration was written in: %v",
			rw.Root(), err)
	}
	return rw
}

// handing returns an attachment naming the laid plugin by its identity and
// version, handing it the namespace given.
func handing(p importopstest.LaidPlugin, namespace string) importopstest.Attachment {
	return importopstest.Attachment{URI: p.Identity, Settings: map[string]any{"target_namespace": namespace}}
}

// sourceFor returns the committed source fixture of the format given.
func sourceFor(t *testing.T, format string) (path string, drafts []string) {
	t.Helper()
	for _, src := range catalogFolderSources {
		if src.format == format {
			return src.source, src.drafts
		}
	}
	t.Fatalf("no committed source fixture is of the %s format", format)
	return "", nil
}

// importFromTheFolder runs Import over the committed source of format through
// rw, reporting rather than writing, and returns the result, the importer
// references the plugin cascade was asked to resolve, and the refusal if the
// import was refused.
func importFromTheFolder(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, format string) (Result, []string, error) {
	t.Helper()
	path, _ := sourceFor(t, format)
	cascade := &recordingCascade{}
	res, err := Import(context.Background(), rw, Options{
		SourcePath:    path,
		SourceName:    filepath.Base(path),
		Format:        format,
		Delivery:      authoredcontent.DeliveryFor(true),
		ResolvePlugin: cascade.resolve,
	})
	var importers []string
	for _, call := range cascade.resolutions() {
		if strings.HasPrefix(call.uri, "importer/") {
			importers = append(importers, call.uri)
		}
	}
	return res, importers, err
}

// assertDraftedBy holds an import's result to the importer that should have run
// it and the namespace that importer should have been handed: the cascade was
// asked for that importer and no other, every drafted path is the reference
// importer's draft of the source under that namespace, and the drafted Aggregate
// is named in it.
func assertDraftedBy(t *testing.T, res Result, importers []string, format, wantImporter, namespace string) {
	t.Helper()
	if want := []string{"importer/" + wantImporter}; !slices.Equal(importers, want) {
		t.Errorf("the import resolved the importers %v; want exactly %v", importers, want)
	}
	if res.Halted() {
		t.Fatalf("the importer halted: %+v", res.Diagnostics)
	}
	_, defaults := sourceFor(t, format)
	want := make([]string, 0, len(defaults))
	for _, d := range defaults {
		want = append(want, strings.Replace(d, "components/"+defaultNamespace+"-", "components/"+namespace+"-", 1))
	}
	if got := draftPaths(res); !slices.Equal(got, want) {
		t.Errorf("the importer drafted %v; want %v, the drafts under the namespace %q", got, want, namespace)
	}
	for _, f := range res.Files {
		if f.Path == "aggregate.yaml" && !strings.Contains(f.Content, "name: "+namespace+"/flow") {
			t.Errorf("the drafted Aggregate is not named in the namespace %q:\n%s", namespace, f.Content)
		}
	}
}

// TestEachImporterAttachmentConfiguresOnlyTheImporterItNames drives a workspace
// whose catalog holds two importers, one generated from the Langflow reference
// importer and so advertising the Langflow format, the other from the Flowise
// one, and whose configuration attaches both, each handing its importer a
// different namespace. An import of each format runs the importer advertising it,
// and that importer drafts under the namespace its own attachment handed it —
// never the other's.
func TestEachImporterAttachmentConfiguresOnlyTheImporterItNames(t *testing.T) {
	t.Parallel()
	rw := attachedImportWorkspace(t, []importopstest.Importer{
		{Name: importerAlpha, Format: "langflow"},
		{Name: importerBeta, Format: "flowise"},
	}, func(laid map[string]importopstest.LaidPlugin) []importopstest.Attachment {
		return []importopstest.Attachment{
			handing(laid[importerAlpha], northNamespace),
			handing(laid[importerBeta], southNamespace),
		}
	})

	for _, row := range []struct {
		format, importer, namespace string
	}{
		{"langflow", importerAlpha, northNamespace},
		{"flowise", importerBeta, southNamespace},
	} {
		t.Run(row.format, func(t *testing.T) {
			res, importers, err := importFromTheFolder(t, rw, row.format)
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			assertDraftedBy(t, res, importers, row.format, row.importer, row.namespace)
		})
	}
}

// TestAnImporterNoAttachmentNamesIsHandedNothing drives workspaces whose one
// attachment names something other than the importer the requested format
// resolves to — the other importer of the pair, an importer the catalog does not
// hold, the parser — and requires the importer that runs to draft under its own
// default namespace: it was handed nothing. Each row fails if a workspace's sole
// attachment is handed to whichever importer runs, whatever it names.
func TestAnImporterNoAttachmentNamesIsHandedNothing(t *testing.T) {
	t.Parallel()
	for _, row := range []struct {
		name      string
		importers []importopstest.Importer
		attach    func(map[string]importopstest.LaidPlugin) []importopstest.Attachment
		format    string
		importer  string
	}{
		{
			name: "the attachment names the importer of the other format",
			importers: []importopstest.Importer{
				{Name: importerAlpha, Format: "langflow"},
				{Name: importerBeta, Format: "flowise"},
			},
			attach: func(laid map[string]importopstest.LaidPlugin) []importopstest.Attachment {
				return []importopstest.Attachment{handing(laid[importerAlpha], northNamespace)}
			},
			format:   "flowise",
			importer: importerBeta,
		},
		{
			name:      "the attachment names an importer the catalog does not hold",
			importers: []importopstest.Importer{{Name: importerAlpha, Format: "langflow"}},
			attach: func(map[string]importopstest.LaidPlugin) []importopstest.Attachment {
				return []importopstest.Attachment{{
					URI:      "importer/import-elsewhere:1.0.0",
					Settings: map[string]any{"target_namespace": northNamespace},
				}}
			},
			format:   "langflow",
			importer: importerAlpha,
		},
		{
			name:      "the attachment names the parser",
			importers: []importopstest.Importer{{Name: importerAlpha, Format: "langflow"}},
			attach: func(laid map[string]importopstest.LaidPlugin) []importopstest.Attachment {
				return []importopstest.Attachment{handing(laid[importopstest.ParserName], northNamespace)}
			},
			format:   "langflow",
			importer: importerAlpha,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rw := attachedImportWorkspace(t, row.importers, row.attach)
			res, importers, err := importFromTheFolder(t, rw, row.format)
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			assertDraftedBy(t, res, importers, row.format, row.importer, defaultNamespace)
		})
	}
}

// TestAnAttachmentChoosesAmongImportersAdvertisingOneFormat drives catalogs
// holding more than one importer advertising the Langflow format. With no
// attachment the import halts as ambiguous, naming every advertiser; with one
// attachment naming one of them, that one runs, handed that attachment's
// settings; with attachments naming two of three, the import halts as ambiguous
// naming the two attached and not the third.
func TestAnAttachmentChoosesAmongImportersAdvertisingOneFormat(t *testing.T) {
	t.Parallel()
	two := []importopstest.Importer{{Name: importerAlpha, Format: "langflow"}, {Name: importerBeta, Format: "langflow"}}
	three := []importopstest.Importer{
		{Name: importerAlpha, Format: "langflow"}, {Name: importerBeta, Format: "langflow"}, {Name: importerGamma, Format: "langflow"},
	}

	for _, row := range []struct {
		name      string
		importers []importopstest.Importer
		attach    func(map[string]importopstest.LaidPlugin) []importopstest.Attachment
		// ambiguous, when set, is the sentence the halt must carry; otherwise the
		// import must run importer, handed namespace.
		ambiguous string
		importer  string
		namespace string
	}{
		{
			name:      "no attachment",
			importers: two,
			ambiguous: `format "langflow" is advertised by importer plugins [import-alpha import-beta]`,
		},
		{
			name:      "an attachment naming the second advertiser",
			importers: two,
			attach: func(laid map[string]importopstest.LaidPlugin) []importopstest.Attachment {
				return []importopstest.Attachment{handing(laid[importerBeta], southNamespace)}
			},
			importer:  importerBeta,
			namespace: southNamespace,
		},
		{
			name:      "attachments naming two of three advertisers",
			importers: three,
			attach: func(laid map[string]importopstest.LaidPlugin) []importopstest.Attachment {
				return []importopstest.Attachment{
					handing(laid[importerGamma], southNamespace),
					handing(laid[importerAlpha], northNamespace),
				}
			},
			ambiguous: `format "langflow" is advertised by importer plugins [import-alpha import-gamma]`,
		},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rw := attachedImportWorkspace(t, row.importers, row.attach)
			res, importers, err := importFromTheFolder(t, rw, "langflow")
			if row.ambiguous == "" {
				if err != nil {
					t.Fatalf("the import was refused: %v", err)
				}
				assertDraftedBy(t, res, importers, "langflow", row.importer, row.namespace)
				return
			}
			if !errors.Is(err, catalog.ErrAmbiguousImporterPlugin()) {
				t.Fatalf("the import was not refused as ambiguous: %v", err)
			}
			if !strings.Contains(err.Error(), row.ambiguous) {
				t.Errorf("the ambiguity halt reads %q; want it to carry %q", err.Error(), row.ambiguous)
			}
			if len(importers) != 0 {
				t.Errorf("an ambiguous import loaded the importers %v", importers)
			}
		})
	}
}

// TestAnAttachmentVersionChoosesThatVersionOfItsImporter drives a catalog
// holding two versions of one importer, each generated from a build of the module
// reporting that version, and reads which version the import's own resolution
// chooses: the highest with no attachment, and the one an attachment's uri names
// — exactly, or by a range — where one does. The pinned attachment is then the
// one whose settings the import hands its importer.
func TestAnAttachmentVersionChoosesThatVersionOfItsImporter(t *testing.T) {
	t.Parallel()
	versions := []importopstest.Importer{
		{Name: importerAlpha, Version: "1.0.0", Format: "langflow"},
		{Name: importerAlpha, Version: "2.0.0", Format: "langflow"},
	}

	for _, row := range []struct {
		name    string
		uri     string
		version string
		at      int
	}{
		{name: "no attachment", version: "2.0.0", at: -1},
		{name: "an exact version", uri: "importer/" + importerAlpha + ":1.0.0", version: "1.0.0", at: 0},
		{name: "a range", uri: "importer/" + importerAlpha + ":^1.0.0", version: "1.0.0", at: 0},
		{name: "the highest version, named", uri: "importer/" + importerAlpha + ":2.0.0", version: "2.0.0", at: 0},
	} {
		t.Run(row.name, func(t *testing.T) {
			t.Parallel()
			rw := attachedImportWorkspace(t, versions, func(map[string]importopstest.LaidPlugin) []importopstest.Attachment {
				if row.uri == "" {
					return nil
				}
				return []importopstest.Attachment{{URI: row.uri, Settings: map[string]any{"target_namespace": northNamespace}}}
			})

			seam, err := BuildSeam(rw, (&recordingCascade{}).resolve)
			if err != nil {
				t.Fatalf("build the import seam: %v", err)
			}
			got, at, err := seam.ImporterRes.ImporterPluginForFormat("langflow",
				attachmentURIs(rw.Config().Plugins.Importer))
			if err != nil {
				t.Fatalf("resolve the importer: %v", err)
			}
			if got.Metadata.Name != importerAlpha || got.Metadata.Version != row.version || at != row.at {
				t.Errorf("resolved %s at %s, attached by position %d; want %s at %s, attached by position %d",
					got.Metadata.Name, got.Metadata.Version, at, importerAlpha, row.version, row.at)
			}

			res, importers, err := importFromTheFolder(t, rw, "langflow")
			if err != nil {
				t.Fatalf("the import was refused: %v", err)
			}
			namespace := defaultNamespace
			if row.uri != "" {
				namespace = northNamespace
			}
			assertDraftedBy(t, res, importers, "langflow", importerAlpha, namespace)
		})
	}
}

// TestAnImporterAttachmentPinningAVersionTheCatalogDoesNotHoldIsRefused drives a
// catalog holding one version of an importer and an attachment naming that
// importer at a version the catalog does not hold. The import is refused before
// any importer is loaded, naming the version, under the importer's own
// unsatisfiable-pin condition.
func TestAnImporterAttachmentPinningAVersionTheCatalogDoesNotHoldIsRefused(t *testing.T) {
	t.Parallel()
	rw := attachedImportWorkspace(t, []importopstest.Importer{{Name: importerAlpha, Version: "1.0.0", Format: "langflow"}},
		func(map[string]importopstest.LaidPlugin) []importopstest.Attachment {
			return []importopstest.Attachment{{
				URI:      "importer/" + importerAlpha + ":9.9.9",
				Settings: map[string]any{"target_namespace": northNamespace},
			}}
		})

	_, importers, err := importFromTheFolder(t, rw, "langflow")
	if err == nil {
		t.Fatal("an import whose attachment pins a version the catalog does not hold ran")
	}
	if !errors.Is(err, catalog.ErrImporterVersionUnsatisfiable()) {
		t.Errorf("the refusal is not reported under the importer's unsatisfiable-pin condition: %v", err)
	}
	if !strings.Contains(err.Error(), `pins "9.9.9"`) {
		t.Errorf("the refusal does not name the version the attachment pins: %v", err)
	}
	if len(importers) != 0 {
		t.Errorf("a refused import loaded the importers %v", importers)
	}
}
