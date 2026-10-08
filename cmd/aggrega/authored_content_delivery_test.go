// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
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

	seed := func(dryRun bool, out *bytes.Buffer) error {
		return configInitRun(t.Context(), configInitConfig{
			Registry:    workspaceconfig.HubBaselineHost,
			DryRun:      dryRun,
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

	// No file was created or modified. The instrument is the
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

	// The writing delivery puts exactly the reported bytes on disk.
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
		if err := stampPluginFile(context.Background(), &out, entityPath, pluginPath); err != nil {
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

// trustAdvisoryStreams drives the seeding capability once and returns what each
// of its two streams carried separately.
//
// The streams are kept apart rather than merged into one buffer because the
// property under test is the split itself: a merged buffer would report the same
// bytes whichever stream had carried them, which is exactly the distinction these
// assertions exist to make.
func trustAdvisoryStreams(t *testing.T, dryRun bool, registry string, client *http.Client) (answer, advisory string) {
	t.Helper()
	root := t.TempDir()
	globalPath := filepath.Join(root, "home", "aggrega", "config.yaml")
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: globalPath})
	wsRoot := filepath.Join(root, "ws")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	rw := resolveTempWorkspaceAgainst(t, tempWorkspaceOptions{Root: wsRoot}, lookup)

	var out, errOut bytes.Buffer
	if err := configInitRun(t.Context(), configInitConfig{
		Registry:    registry,
		DryRun:      dryRun,
		ws:          rw,
		Out:         &out,
		ErrOut:      &errOut,
		trustClient: client,
	}); err != nil {
		t.Fatalf("seeding %s (dryRun=%v): %v", registry, dryRun, err)
	}
	return out.String(), errOut.String()
}

// TestTheTrustAdvisoryReachesTheErrorStreamOnlyWhereItIsUnrecoverable holds the
// trust-fetch notice to the stream each of its outcomes belongs on, across both
// deliveries.
//
// The grid is what matters, not the two axes separately. Delivery and fetch
// outcome are independent, so there are six cells — two deliveries by three
// outcomes: an advertised policy pinned, no policy advertised and none behind it,
// and the hub's built-in baseline pinned where it could not be reached — and the
// notice's behaviour differs in three ways across them:
//
//   - writing, any outcome — the notice is on the answer stream, where a person
//     watching a terminal reads it, and the error stream is silent;
//   - reporting, policy not advertised — the answer stream holds one parseable
//     document and nothing else, and the advisory is on the error stream, still
//     naming the flags that supply a policy by hand;
//   - reporting, a policy pinned, advertised or the built-in baseline — nothing
//     is emitted anywhere, because the pinned policy IS the reported content and
//     a notice about it would be a duplicate.
//
// The pinned cell is the one that fails a notice emitted on the delivery alone:
// an implementation that moved BOTH outcomes to the error stream under a reporting
// delivery satisfies every other assertion here. The baseline cells are the ones
// that fail a notice routed by "was it advertised": the hub could not be reached,
// so it advertised nothing and ALSO carries a policy, and only the second fact
// decides the stream.
func TestTheTrustAdvisoryReachesTheErrorStreamOnlyWhereItIsUnrecoverable(t *testing.T) {
	t.Parallel()

	// The remedy is the half of the advisory that cannot be recovered from
	// content: an absence conveys neither that a policy is missing nor what to do.
	const remedy = "--identity or --key"
	const absence = "pin its trust by hand"
	const baseline = "; using the built-in trust baseline for " + workspaceconfig.HubBaselineHost + " instead\n"

	// A registry other than the hub, which carries no baseline, so a registry that
	// cannot be reached leaves its entry with no policy at all.
	const absentHost = "private.example.test"

	pinnedHost, pinnedClient := advertisedTrustServer(t, `{
  "requires_signatures": true,
  "trusted_identities": [
    {"identity": "dev@acme.example", "issuer": "https://accounts.google.com"}
  ]
}`)

	// Cell 1 — writing, policy not advertised.
	writeAbsentAnswer, writeAbsentAdvisory := trustAdvisoryStreams(t, false, absentHost, offlineTrustClient())
	if !strings.Contains(writeAbsentAnswer, absence) || !strings.Contains(writeAbsentAnswer, remedy) {
		t.Errorf("a writing delivery must keep the advisory on the answer stream, remedy included; it carried:\n%s", writeAbsentAnswer)
	}
	if writeAbsentAdvisory != "" {
		t.Errorf("a writing delivery has a free answer stream, so nothing belongs on the error stream; it carried:\n%s", writeAbsentAdvisory)
	}

	// Cell 2 — writing, policy pinned.
	writePinnedAnswer, writePinnedAdvisory := trustAdvisoryStreams(t, false, pinnedHost, pinnedClient)
	if !strings.Contains(writePinnedAnswer, "Pinned advertised trust policy") {
		t.Errorf("a writing delivery must report a pinned policy on the answer stream; it carried:\n%s", writePinnedAnswer)
	}
	if writePinnedAdvisory != "" {
		t.Errorf("a pinned policy under a writing delivery must leave the error stream empty; it carried:\n%s", writePinnedAdvisory)
	}

	// Cell 3 — reporting, policy not advertised. The advisory moves to the error
	// stream and the answer stream keeps its contract.
	reportAbsentAnswer, reportAbsentAdvisory := trustAdvisoryStreams(t, true, absentHost, offlineTrustClient())
	if !strings.Contains(reportAbsentAdvisory, absence) {
		t.Errorf("the unrecoverable outcome must reach the error stream under a reporting delivery; it carried:\n%s", reportAbsentAdvisory)
	}
	if !strings.Contains(reportAbsentAdvisory, remedy) {
		t.Errorf("the advisory reached the error stream without its remedy, which is the half a caller cannot recover from content:\n%s", reportAbsentAdvisory)
	}
	if strings.Contains(reportAbsentAnswer, absence) {
		t.Errorf("prose reached the reported answer stream, which must hold one parseable document and nothing else:\n%s", reportAbsentAnswer)
	}

	// The reported stream held one report and nothing else, asserted by
	// re-encoding what was decoded through the format's own writer and comparing
	// bytes. A containment check cannot make this claim: an unknown key beside
	// `documents:` still decodes, so a stream carrying prose as well as the
	// document would satisfy a decode and a substring test both.
	docs := reportedDocuments(t, []byte(reportAbsentAnswer))
	var reencoded bytes.Buffer
	if err := authoredcontent.Report(&reencoded, docs); err != nil {
		t.Fatalf("re-encode the decoded report: %v", err)
	}
	if reencoded.String() != reportAbsentAnswer {
		t.Errorf("the reported stream is not exactly one report:\nstream:\n%s\nthe report alone:\n%s", reportAbsentAnswer, reencoded.String())
	}

	// One composition, two streams: the advisory a reader gets on the error stream
	// is the SAME sentence the answer stream carries under a writing delivery. A
	// second phrasing written for the new stream would make these differ, and the
	// remedy is exactly what such a rewrite tends to shorten away.
	writtenAdvisoryLine := strings.SplitAfter(afterSeedingLocations(t, writeAbsentAnswer), "\n")[0]
	if reportAbsentAdvisory != writtenAdvisoryLine {
		t.Errorf("the advisory is phrased twice rather than composed once\non the error stream:\n%q\non the answer stream:\n%q", reportAbsentAdvisory, writtenAdvisoryLine)
	}

	// Cell 4 — reporting, policy pinned. Nothing is emitted on either stream, and
	// this is the assertion a both-outcomes implementation cannot pass.
	reportPinnedAnswer, reportPinnedAdvisory := trustAdvisoryStreams(t, true, pinnedHost, pinnedClient)
	if reportPinnedAdvisory != "" {
		t.Errorf("a pinned policy is the reported content, so a notice about it is a duplicate and belongs on no stream; the error stream carried:\n%s", reportPinnedAdvisory)
	}
	if strings.Contains(reportPinnedAnswer, "Pinned advertised trust policy") {
		t.Errorf("prose reached the reported answer stream:\n%s", reportPinnedAnswer)
	}
	if pinnedDocs := reportedDocuments(t, []byte(reportPinnedAnswer)); len(pinnedDocs) != 1 {
		t.Errorf("seeding reported %d documents; it writes one file, so it reports one", len(pinnedDocs))
	}

	// Cell 5 — writing, the hub's built-in baseline pinned. The hub could not be
	// reached, so it advertised nothing; the entry carries its baseline anyway, and
	// the notice says so rather than sending the reader to pin identities by hand.
	writeBaselineAnswer, writeBaselineAdvisory := trustAdvisoryStreams(t, false, workspaceconfig.HubBaselineHost, offlineTrustClient())
	if first := strings.SplitAfter(afterSeedingLocations(t, writeBaselineAnswer), "\n")[0]; !strings.HasSuffix(first, baseline) {
		t.Errorf("a writing delivery must say the built-in baseline was pinned; it carried:\n%s", writeBaselineAnswer)
	}
	if strings.Contains(writeBaselineAnswer, absence) || strings.Contains(writeBaselineAnswer, remedy) {
		t.Errorf("the hub's baseline was reported as an absence to fill by hand:\n%s", writeBaselineAnswer)
	}
	if writeBaselineAdvisory != "" {
		t.Errorf("a writing delivery has a free answer stream, so nothing belongs on the error stream; it carried:\n%s", writeBaselineAdvisory)
	}

	// Cell 6 — reporting, the hub's built-in baseline pinned. Nothing is emitted on
	// either stream: the baseline is a pinned policy, and the reported content
	// carries it.
	reportBaselineAnswer, reportBaselineAdvisory := trustAdvisoryStreams(t, true, workspaceconfig.HubBaselineHost, offlineTrustClient())
	if reportBaselineAdvisory != "" {
		t.Errorf("the built-in baseline is in the reported content, so a notice about it belongs on no stream; the error stream carried:\n%s", reportBaselineAdvisory)
	}
	baselineDocs := reportedDocuments(t, []byte(reportBaselineAnswer))
	if len(baselineDocs) != 1 {
		t.Fatalf("seeding reported %d documents; it writes one file, so it reports one", len(baselineDocs))
	}
	if issuer := workspaceconfig.DefaultBaseline().TrustedIdentities[0].Issuer; !strings.Contains(baselineDocs[0].Content, issuer) {
		t.Errorf("the reported content does not carry the built-in baseline the notice was dropped for:\n%s", baselineDocs[0].Content)
	}
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
//
// 🔴 One command carries BOTH classes and its row cannot say so, which is why the
// sibling test below exists rather than a second column here. Attaching a plugin
// produces configuration its owner edits for every type but one; the command
// extension records the content digest of the bytes it verified, which is machine
// state by the same rule. So the flag is offered at the command and refused at
// that one type, and the row asserts only the first half.
func TestTheProductClassificationIsCarriedByTheCommandSurface(t *testing.T) {
	// The selector is named per row rather than once for the whole table, because
	// a command spells the delivery in whatever words its own surface reads best
	// and one constant would make a row assert a spelling its command never
	// offered.
	//
	// 🔴 An absence row is checked against EVERY spelling in use, not against the
	// one its neighbours happen to offer. Absence is the half that has to hold
	// against all of them: a row checking one spelling passes unchanged for a
	// command that grew a different one, which is precisely how machine state
	// would come to carry this delivery without any row noticing.
	//
	// It holds the spellings IN USE and no retired one. A retired spelling here
	// would read as an assertion and be incapable of failing, since no command
	// offers it — so it would grow the list without adding cover, and a reader
	// would take the list for the surface rather than for what it is.
	everySpelling := []string{"dry-run"}

	for _, row := range []struct {
		path    string
		product string
		// selector is the flag this command spells the delivery with, empty on a
		// row whose product is machine state and must therefore offer none of them.
		selector string
	}{
		{"aggrega config init", "a seeded configuration the operator goes on editing", "dry-run"},
		{"aggrega plugin gen-catalog", "catalog entities a catalog author goes on editing", "dry-run"},
		{"aggrega import", "draft authoring files the author goes on editing", "dry-run"},
		{"aggrega plugin add", "a plugin attachment the operator goes on editing", "dry-run"},
		{"aggrega config capture", "a record of what the configuration resolved to", ""},
		{"aggrega plugin sync", "the compiler's own bookkeeping about a plugin module", ""},
	} {
		t.Run(row.path, func(t *testing.T) {
			cmd, _, err := newRootCmd().Find(strings.Fields(row.path)[1:])
			if err != nil {
				t.Fatalf("the command tree does not carry %q: %v", row.path, err)
			}
			if row.selector == "" {
				for _, spelling := range everySpelling {
					if cmd.Flags().Lookup(spelling) != nil {
						t.Errorf("%s produces %s, which is machine state, so it must NOT offer --%s: reporting it invites a caller to edit what the machine keeps about itself",
							row.path, row.product, spelling)
					}
				}
				return
			}
			flag := cmd.Flags().Lookup(row.selector)
			switch {
			case flag == nil:
				t.Errorf("%s produces %s, which is content its owner edits, so it must offer --%s", row.path, row.product, row.selector)
			case flag.Hidden:
				// A hidden flag is not part of the documented surface, and the
				// machine-driven surfaces derive their arguments from the documented
				// flags — so hiding it would leave the delivery reachable only by
				// someone who already knew it was there.
				t.Errorf("%s hides --%s, which withholds it from every surface derived from the documented flags", row.path, row.selector)
			}
		})
	}
}

// attachmentFixture is a workspace whose machine-wide layer is redirected at a
// path whose PARENT DIRECTORY DOES NOT EXIST, with the registry and version seams
// pointed at fakes so an attachment runs without a network.
//
// The absent parent is the fixture's whole point rather than a convenience. The
// writing delivery creates it, so a reporting delivery that leaked a directory
// into existence is caught by the directory's own absence — an assertion a fixture
// that pre-created the tree could not make at all, because there would be nothing
// left for the report to wrongly create. It returns the two files an attachment
// can resolve to, so a test names the target rather than re-deriving it.
func attachmentFixture(t *testing.T) (rw *workspaceconfig.ResolvedWorkspace, root, workspaceFile, machineFile string) {
	t.Helper()
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/governance/tag-injector"}}, nil)
	withTagLister(t, []string{"1.0.0", "1.2.0", "2.0.0-rc1"})

	root = t.TempDir()
	wsRoot := filepath.Join(root, "ws")
	if err := os.MkdirAll(wsRoot, 0o755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}
	workspaceFile = filepath.Join(wsRoot, workspaceconfig.ConfigFilename)
	// The seeded settings are what make "the merged whole document" falsifiable: a
	// composition against an empty base would produce a document carrying only the
	// attachment, and writing THAT to the reported path would erase these.
	if err := os.WriteFile(workspaceFile, []byte("workspace: {}\ndefault_registry: hub.aggrega.ai\noffline: false\n"), 0o644); err != nil {
		t.Fatalf("seed workspace config: %v", err)
	}
	machineFile = filepath.Join(root, "home", "aggrega", "config.yaml")
	lookup := frozenEnv(map[string]string{workspaceconfig.EnvConfigOverride: machineFile})
	resolved, err := workspaceconfig.ResolveWithoutGlobal(wsRoot, "", lookup)
	if err != nil {
		t.Fatalf("resolve the attachment fixture: %v", err)
	}
	return resolved, root, workspaceFile, machineFile
}

// attachPlugin drives one governance attachment through the command line and
// returns what each of its two streams carried.
//
// The streams are kept apart rather than merged, because the property under test
// includes which stream the answer is on: a merged buffer would report the same
// bytes whichever had carried them.
func attachPlugin(t *testing.T, rw *workspaceconfig.ResolvedWorkspace, dryRun, global bool) (answer, advisory string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer
	err = pluginAddRun(context.Background(), pluginAddConfig{
		Socket: "governance",
		Role:   "label-transform",
		Query:  "tag-injector",
		Global: global,
		DryRun: dryRun,
		ws:     rw,
		Out:    &out,
		ErrOut: &errOut,
	})
	return out.String(), errOut.String(), err
}

// TestAttachingAPluginReportsContentAndWritesNothing drives the attachment's
// reporting delivery at BOTH write-target defaults and holds each to the
// filesystem: the answer carries the configuration document, names the file it is
// for, and the tree is byte-for-byte what it was before the call.
//
// Both targets are driven because the file an attachment resolves to is not a
// property of the operation — a socket attachment lands in this project's own
// configuration and the machine-wide override lands in the file every workspace on
// the machine resolves through. A proof scoped to the workspace would report a
// clean tree for a delivery that had written the machine-wide file, which is the
// mistake that matters most here.
func TestAttachingAPluginReportsContentAndWritesNothing(t *testing.T) {
	// Not parallel: the registry and version seams are process-global.
	for _, tc := range []struct {
		name   string
		global bool
	}{
		{"the workspace default", false},
		{"the machine-wide override", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rw, root, workspaceFile, machineFile := attachmentFixture(t)
			target := workspaceFile
			if tc.global {
				target = machineFile
			}

			before := treeFingerprint(t, root)

			answer, _, err := attachPlugin(t, rw, true, tc.global)
			if err != nil {
				t.Fatalf("attaching under the reporting delivery: %v", err)
			}

			// Nothing was created or modified, measured by content digest over
			// the whole temporary tree rather than by size or timestamp.
			if diffs := fingerprintDifferences(before, treeFingerprint(t, root)); len(diffs) != 0 {
				t.Fatalf("the reporting delivery touched the filesystem:\n  %s", strings.Join(diffs, "\n  "))
			}
			// No parent directory was created. The fingerprint above already
			// records directories, so this is the same claim said in the words the
			// failure would be read in — and it is the assertion that fails loudest if
			// the composition were ever routed through the write scaffold, which
			// creates the directory before it writes.
			if _, statErr := os.Stat(filepath.Dir(machineFile)); !os.IsNotExist(statErr) {
				t.Errorf("the reporting delivery created %s; a report writes nothing, so it creates no directory to write into",
					filepath.Dir(machineFile))
			}

			docs := reportedDocuments(t, []byte(answer))
			if len(docs) != 1 {
				t.Fatalf("the attachment reported %d documents; it writes one file, so it reports one", len(docs))
			}
			if docs[0].Path != target {
				t.Errorf("reported document names %s; the write target is %s, and a caller that trusted a wrong path here would write a machine-wide configuration into a workspace",
					docs[0].Path, target)
			}
			if !strings.Contains(docs[0].Content, "governance/tag-injector:1.2.0") {
				t.Errorf("reported content does not carry the pinned attachment a write would have made:\n%s", docs[0].Content)
			}
			// The report is the MERGED WHOLE document, not a minimal one. On the workspace
			// target the file already carries settings, and a composition against an
			// empty base would drop them — which, written to the reported path, would
			// erase them. On the machine-wide target no file exists yet, so the honest
			// answer is the freshly seeded document and there is nothing to preserve.
			if !tc.global && !strings.Contains(docs[0].Content, "default_registry") {
				t.Errorf("the reported document dropped a setting the file already carried, so writing it back would erase it:\n%s", docs[0].Content)
			}

			// The writing delivery puts exactly the reported bytes on disk, at
			// the reported path. Each delivery is the other's control — a reporting
			// delivery that quietly wrote would pass a stream-only assertion, and a
			// writing delivery that quietly stopped writing would pass an
			// untouched-tree one.
			writeAnswer, _, werr := attachPlugin(t, rw, false, tc.global)
			if werr != nil {
				t.Fatalf("attaching under the writing delivery: %v", werr)
			}
			onDisk, rerr := os.ReadFile(target)
			if rerr != nil {
				t.Fatalf("the writing delivery did not produce the reported path %s: %v", target, rerr)
			}
			if string(onDisk) != docs[0].Content {
				t.Errorf("the written bytes differ from the reported content\nreported:\n%s\nwritten:\n%s", docs[0].Content, onDisk)
			}
			// The writing delivery reports no document. It answers with the
			// confirmation sentence a terminal expects, and that stream must not parse
			// as a report — the file is on disk at the path the sentence already names,
			// so carrying it again would duplicate what was just persisted.
			if docs := decodedDocuments(writeAnswer); len(docs) > 0 {
				t.Errorf("the writing delivery answered with %d document(s) as well as writing the file:\n%s", len(docs), writeAnswer)
			}
			if !strings.Contains(writeAnswer, "Added") {
				t.Errorf("the writing delivery did not confirm what it added:\n%s", writeAnswer)
			}
		})
	}
}

// decodedDocuments decodes a stream as the reported form and returns the
// documents it carried, or none where the stream is not a report at all.
//
// It exists so the writing delivery's answer can be tested for the ABSENCE of
// documents: a plain confirmation sentence can decode as a report carrying an
// empty set, so "decoding failed" is not the assertion — "carried no document"
// is.
func decodedDocuments(answer string) []authoredcontent.Document {
	docs, err := authoredcontent.Decode([]byte(answer))
	if err != nil {
		return nil
	}
	return docs
}

// TestTheCommandExtensionTypeRefusesToReport pins the one type this delivery is
// refused on, and pins that the refusal is LOUD.
//
// A silent ignore here is the failure the loud refusal exists to prevent: a
// caller that asked for a report, was written for instead, and told success would
// read the answer as the report it asked for. The refusal is asserted to name what
// to do instead, and the tree is read back afterwards.
//
// 🔴 The fetch seam is pointed at a fake that FAILS the test if it is reached, and
// that is not belt-and-braces. Measured by disabling the refusal: the addition
// then ran on through the production fetch and issued a live request to the real
// registry host. So a test that relied on the refusal to stay network-free would
// become a network test the moment the refusal regressed — which is the moment it
// is most needed. The fake turns that into an assertion: reaching the fetch at all
// means the refusal did not come first.
func TestTheCommandExtensionTypeRefusesToReport(t *testing.T) {
	// Not parallel: the registry, version and fetch seams are process-global.
	withSearchTarget(t, fakeRepoLister{repos: []string{"plugin/cli/moo"}}, nil)
	withTagLister(t, []string{"1.0.0"})
	prevFetch := installPluginFetch
	installPluginFetch = func(context.Context, *workspaceconfig.ResolvedWorkspace, string) (resolvedInstallable, error) {
		t.Error("the install fetch was reached for a request the operation should have refused")
		return resolvedInstallable{}, errors.New("the fetch seam must not be reached")
	}
	t.Cleanup(func() { installPluginFetch = prevFetch })
	rw := resolvedTestWorkspace(t)

	before := treeFingerprint(t, rw.Root())
	var out, errOut bytes.Buffer
	err := pluginAddRun(context.Background(), pluginAddConfig{
		Socket:    "cli",
		Query:     "moo",
		AssumeYes: true,
		DryRun:    true,
		ws:        rw,
		Out:       &out,
		ErrOut:    &errOut,
		In:        strings.NewReader(""),
	})
	if err == nil {
		t.Fatal("the command-extension type accepted a reporting delivery; its product is a record the machine maintains")
	}
	for _, want := range []string{"cli type cannot report", "content digest", "provider or socket attachment"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal does not carry %q: %v", want, err)
		}
	}
	// The refusal must not name any one surface's spelling for the delivery: all
	// three offer it, and a message naming a flag tells a request-driven caller to
	// change something it cannot reach.
	if strings.Contains(err.Error(), "--dry-run") {
		t.Errorf("the refusal names the command line's flag, which no request-driven caller can send: %v", err)
	}
	if diffs := fingerprintDifferences(before, treeFingerprint(t, rw.Root())); len(diffs) != 0 {
		t.Errorf("the refused addition changed the filesystem:\n  %s", strings.Join(diffs, "\n  "))
	}
}
