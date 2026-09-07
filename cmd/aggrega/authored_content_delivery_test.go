// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
	"github.com/aggrega-ai/aggrega-cli/internal/gencatalog"
	"github.com/aggrega-ai/aggrega-cli/internal/wasm/embedded"
	"github.com/aggrega-ai/aggrega-cli/pkg/workspaceconfig"
)

// treeFingerprint records every path under root and, for a file, the digest of
// its content.
//
// It is the instrument the reporting delivery is held to, and it hashes CONTENT
// rather than comparing sizes or timestamps deliberately. A size-and-timestamp
// comparison cannot see a rewrite that happens to preserve both, so it would
// report a clean tree for a delivery that had in fact rewritten a file — which
// is the exact claim these tests exist to make. Directories are recorded too, so
// a delivery that created an empty output directory is caught as well as one
// that wrote into it.
func treeFingerprint(t *testing.T, root string) map[string]string {
	t.Helper()
	prints := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return rerr
		}
		if d.IsDir() {
			prints[rel] = "dir"
			return nil
		}
		body, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		sum := sha256.Sum256(body)
		prints[rel] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("fingerprint %s: %v", root, err)
	}
	return prints
}

// fingerprintDifferences names every way two fingerprints of the same tree
// differ, as full sentences a failure can be read from.
//
// It names what changed rather than reporting a count, because two trees with
// one file added and one removed hold the same number of entries, and a count
// would call that pair identical.
func fingerprintDifferences(before, after map[string]string) []string {
	var diffs []string
	for path, sum := range after {
		prior, existed := before[path]
		switch {
		case !existed:
			diffs = append(diffs, fmt.Sprintf("%s was created", path))
		case prior != sum:
			diffs = append(diffs, fmt.Sprintf("%s was modified", path))
		}
	}
	for path := range before {
		if _, still := after[path]; !still {
			diffs = append(diffs, fmt.Sprintf("%s was removed", path))
		}
	}
	sort.Strings(diffs)
	return diffs
}

// reportedDocuments decodes a capability's reported answer through the reported
// form's own reader, so the test reads the format the way a client would rather
// than re-deriving its framing.
func reportedDocuments(t *testing.T, answer []byte) []authoredcontent.Document {
	t.Helper()
	docs, err := authoredcontent.Decode(answer)
	if err != nil {
		t.Fatalf("decode the reported answer: %v\n%s", err, answer)
	}
	if len(docs) == 0 {
		t.Fatalf("the reported answer carries no documents:\n%s", answer)
	}
	for i, doc := range docs {
		if doc.Path == "" {
			t.Errorf("reported document %d names no file, so a caller cannot tell where it belongs", i)
		}
		if doc.Content == "" {
			t.Errorf("reported document %d for %s carries no content", i, doc.Path)
		}
	}
	return docs
}

// TestSeedingAConfigurationReportsContentAndWritesNothing drives the seeding
// capability's reporting delivery and holds it to the filesystem: the answer
// carries the configuration document and names the file it is for, and the tree
// is byte-for-byte what it was before the call.
//
// The tree fingerprinted spans both the workspace and the directory the global
// config resolves into, because seeding targets the machine-wide file by default
// — a comparison scoped to the workspace would report a clean tree for a
// delivery that had written the global one, which is the mistake that most
// matters here.
func TestSeedingAConfigurationReportsContentAndWritesNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	globalPath := filepath.Join(root, "home", "aggrega", "config.yaml")
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: globalPath})
	wsRoot := filepath.Join(root, "ws")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{Root: wsRoot}, lookup)

	seed := func(report bool, out *bytes.Buffer) error {
		return configInitRun(configInitConfig{
			Registry:    workspaceconfig.HubBaselineHost,
			Report:      report,
			ws:          rw,
			Out:         out,
			trustClient: offlineTrustClient(),
		})
	}

	before := treeFingerprint(t, root)

	var reported bytes.Buffer
	if err := seed(true, &reported); err != nil {
		t.Fatalf("seeding under the reporting delivery: %v", err)
	}

	// Clause: no file was created or modified. The instrument is the
	// content-digest fingerprint above, taken over the whole temporary tree.
	if diffs := fingerprintDifferences(before, treeFingerprint(t, root)); len(diffs) != 0 {
		t.Fatalf("the reporting delivery touched the filesystem:\n  %s", strings.Join(diffs, "\n  "))
	}

	docs := reportedDocuments(t, reported.Bytes())
	if len(docs) != 1 {
		t.Fatalf("seeding reported %d documents; it writes one file, so it reports one", len(docs))
	}
	if docs[0].Path != globalPath {
		t.Errorf("reported document names %s; the write target is %s, and a caller that trusted a wrong path here would write a machine-wide configuration somewhere else",
			docs[0].Path, globalPath)
	}
	if !strings.Contains(docs[0].Content, workspaceconfig.HubBaselineHost) {
		t.Errorf("reported content does not register %s:\n%s", workspaceconfig.HubBaselineHost, docs[0].Content)
	}

	// Clause: the writing delivery puts exactly the reported bytes on disk.
	if err := seed(false, &bytes.Buffer{}); err != nil {
		t.Fatalf("seeding under the writing delivery: %v", err)
	}
	onDisk, err := os.ReadFile(globalPath)
	if err != nil {
		t.Fatalf("read the written configuration: %v", err)
	}
	if string(onDisk) != docs[0].Content {
		t.Errorf("the written bytes differ from the reported content\nreported:\n%s\nwritten:\n%s", docs[0].Content, onDisk)
	}
}

// TestGeneratingCatalogEntitiesReportsContentAndWritesNothing drives the
// entity-generating capability's reporting delivery and holds it to the same
// filesystem comparison, then proves the writing delivery lands the reported
// bytes at the reported paths.
func TestGeneratingCatalogEntitiesReportsContentAndWritesNothing(t *testing.T) {
	t.Parallel()
	wasmBytes, ok := embedded.Lookup(embedded.PublishingLinterURI)
	if !ok {
		t.Skip("publishing-linter guest not embedded in this build")
	}
	root := t.TempDir()
	wasmPath := filepath.Join(root, "linter.wasm")
	if err := os.WriteFile(wasmPath, wasmBytes, 0o644); err != nil {
		t.Fatalf("write guest: %v", err)
	}
	overlayPath := filepath.Join(root, "overlay.yaml")
	writeFixture(t, overlayPath, `# SPDX-License-Identifier: Apache-2.0
apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: publishing-linter
spec:
  runtime:
    maxMemoryPages: 4096
    timeoutMs: 300000
`)
	outDir := filepath.Join(root, "out")

	generate := func(report bool, out *bytes.Buffer) error {
		return gencatalog.Run(context.Background(), gencatalog.Options{
			PluginPath:  wasmPath,
			OutputDir:   outDir,
			OverlayPath: overlayPath,
			Delivery:    authoredcontent.DeliveryFor(report),
		}, out)
	}

	before := treeFingerprint(t, root)

	var reported bytes.Buffer
	if err := generate(true, &reported); err != nil {
		t.Fatalf("generating under the reporting delivery: %v", err)
	}

	if diffs := fingerprintDifferences(before, treeFingerprint(t, root)); len(diffs) != 0 {
		t.Fatalf("the reporting delivery touched the filesystem:\n  %s", strings.Join(diffs, "\n  "))
	}

	docs := reportedDocuments(t, reported.Bytes())
	for _, doc := range docs {
		if !strings.HasPrefix(doc.Path, outDir+string(filepath.Separator)) {
			t.Errorf("reported document %s does not sit under the requested output directory %s", doc.Path, outDir)
		}
	}

	if err := generate(false, &bytes.Buffer{}); err != nil {
		t.Fatalf("generating under the writing delivery: %v", err)
	}
	for _, doc := range docs {
		onDisk, err := os.ReadFile(doc.Path)
		if err != nil {
			t.Fatalf("the writing delivery did not produce the reported path %s: %v", doc.Path, err)
		}
		if string(onDisk) != doc.Content {
			t.Errorf("%s: the written bytes differ from the reported content\nreported:\n%s\nwritten:\n%s", doc.Path, doc.Content, onDisk)
		}
	}
}

// machineStateEntity is a Plugin entity carrying neither machine-owned record, so
// bringing it into step with its module is a real write rather than a no-op.
const machineStateEntity = `apiVersion: aggrega.ai/v1alpha1
kind: Plugin
metadata:
  name: publishing-linter
  pluginType: linter
  version: "1.0.0"
spec:
  abiVersion: v1alpha1
`

// TestMachineStateCapabilitiesWriteDirectly holds the other half of the rule:
// the two capabilities whose product is state the machine keeps about itself
// write when invoked, and offer nothing else.
//
// Freezing the resolved configuration produces a record of what the machine
// resolved, not content anyone composes. Bringing a plugin's machine-owned
// records into step with its module produces the compiler's own bookkeeping —
// and it writes that bookkeeping INSIDE a document the author owns, which is why
// the line is drawn on the content rather than on the file: classified by the
// file it would land in the reported half and be handed to a reader to edit.
func TestMachineStateCapabilitiesWriteDirectly(t *testing.T) {
	t.Run("freezing the effective configuration", func(t *testing.T) {
		t.Parallel()
		wsDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(wsDir, workspaceconfig.ConfigFilename), []byte("workspace: {}\n"), 0o644); err != nil {
			t.Fatalf("seed workspace: %v", err)
		}
		globalDir := t.TempDir()
		globalPath := filepath.Join(globalDir, "aggrega", "config.yaml")
		writeGlobalConfig(t, globalPath, filepath.Join(globalDir, "catalog"))
		rw, err := workspaceconfig.Resolve(wsDir, "", frozenEnv(map[string]string{"AGGREGA_CONFIG": globalPath}))
		if err != nil {
			t.Fatalf("resolve with global: %v", err)
		}

		wsFile := filepath.Join(wsDir, workspaceconfig.ConfigFilename)
		before := treeFingerprint(t, wsDir)
		if err := configCaptureRun(configCaptureConfig{ws: rw, Out: &bytes.Buffer{}}); err != nil {
			t.Fatalf("configCaptureRun: %v", err)
		}
		diffs := fingerprintDifferences(before, treeFingerprint(t, wsDir))
		if len(diffs) == 0 {
			t.Fatal("freezing the configuration wrote nothing; its product is machine state, which is written directly")
		}
		if got := strings.Join(diffs, "; "); !strings.Contains(got, "was modified") {
			t.Errorf("expected the workspace config file to be modified in place; the tree changed as: %s", got)
		}
		body, err := os.ReadFile(wsFile)
		if err != nil {
			t.Fatalf("read the frozen configuration: %v", err)
		}
		if !strings.Contains(string(body), "oci://hub.aggrega.ai/plugin/provider/helm:1.0.0") {
			t.Errorf("the frozen configuration does not carry the previously global-only binding:\n%s", body)
		}
	})

	t.Run("bringing machine-owned records into step", func(t *testing.T) {
		t.Parallel()
		wasmBytes, ok := embedded.Lookup(embedded.PublishingLinterURI)
		if !ok {
			t.Skip("publishing-linter guest not embedded in this build")
		}
		root := t.TempDir()
		pluginPath := filepath.Join(root, "linter.wasm")
		if err := os.WriteFile(pluginPath, wasmBytes, 0o644); err != nil {
			t.Fatalf("write guest: %v", err)
		}
		entityPath := filepath.Join(root, "plugin.yaml")
		if err := os.WriteFile(entityPath, []byte(machineStateEntity), 0o644); err != nil {
			t.Fatalf("write entity: %v", err)
		}

		before := treeFingerprint(t, root)
		var out bytes.Buffer
		if err := stampPluginFile(&out, entityPath, pluginPath); err != nil {
			t.Fatalf("stampPluginFile: %v", err)
		}
		diffs := fingerprintDifferences(before, treeFingerprint(t, root))
		if len(diffs) == 0 {
			t.Fatal("bringing the records into step wrote nothing; its product is machine state, which is written directly")
		}
		body, err := os.ReadFile(entityPath)
		if err != nil {
			t.Fatalf("read the entity: %v", err)
		}
		if !strings.Contains(string(body), "hostFunctions") {
			t.Errorf("the entity does not carry the machine-owned host-function record:\n%s", body)
		}
	})
}

// TestTheProductClassificationIsCarriedByTheCommandSurface asserts the rule's
// classification on the surface a caller meets, one row per capability.
//
// The criterion is the rule, not this list: a capability whose product is
// content an author will own and go on editing offers the reporting delivery, so
// a caller with its own document model can place that content itself; a
// capability whose product is state the machine keeps about itself does not,
// because nobody is going to edit it. A capability added later is classified by
// asking that question of it, and this table is where the answer is asserted.
//
// The absent half is the half worth asserting. A reporting delivery offered on a
// snapshot of resolved state would invite a caller to edit a record of what the
// machine worked out, which is not authoring; and offered on a plugin's
// machine-owned records it would hand a reader the compiler's own bookkeeping to
// amend.
func TestTheProductClassificationIsCarriedByTheCommandSurface(t *testing.T) {
	const reportFlag = "report"

	for _, row := range []struct {
		path    string
		product string
		offers  bool
	}{
		{"aggrega config init", "a seeded configuration the operator goes on editing", true},
		{"aggrega plugin gen-catalog", "catalog entities a catalog author goes on editing", true},
		{"aggrega config capture", "a record of what the configuration resolved to", false},
		{"aggrega plugin sync", "the compiler's own bookkeeping about a plugin module", false},
	} {
		t.Run(row.path, func(t *testing.T) {
			cmd, _, err := newRootCmd().Find(strings.Fields(row.path)[1:])
			if err != nil {
				t.Fatalf("the command tree does not carry %q: %v", row.path, err)
			}
			flag := cmd.Flags().Lookup(reportFlag)
			switch {
			case row.offers && flag == nil:
				t.Errorf("%s produces %s, which is content its owner edits, so it must offer --%s", row.path, row.product, reportFlag)
			case !row.offers && flag != nil:
				t.Errorf("%s produces %s, which is machine state, so it must NOT offer --%s: reporting it invites a caller to edit what the machine keeps about itself",
					row.path, row.product, reportFlag)
			case row.offers && flag.Hidden:
				// A hidden flag is not part of the documented surface, and the
				// machine-driven surfaces derive their arguments from the documented
				// flags — so hiding it would leave the delivery reachable only by
				// someone who already knew it was there.
				t.Errorf("%s hides --%s, which withholds it from every surface derived from the documented flags", row.path, reportFlag)
			}
		})
	}
}
