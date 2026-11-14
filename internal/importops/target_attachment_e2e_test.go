// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package importops

// End-to-end proofs that the target an import is given reaches the importer and
// decides what the draft writes for one target alone.
//
// Each case imports through the real Import, naming no format, over a workspace
// whose catalog folder holds the reference importers and a JSON parser beside
// their entities — the folder importopstest lays down — together with the
// standard library's platform and model Components and the model's compose and
// helm Recipes, copied in from the aggrega-hub repository checked out beside this
// one, so a Recipe of each target binds and neither target is refused.
//
// These tests read catalog entities from the aggrega-hub repository checked out
// beside this one. Without that checkout they fail with a catalog-load error
// rather than skipping, so check out aggrega-hub beside this repository first.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
)

// targetAttachmentHubEntities are platformImportEntities with the compose and
// helm Recipes of the model the Langflow platform's model socket draws.
var targetAttachmentHubEntities = append(slices.Clone(platformImportEntities),
	"recipe/compose/openai/1.0.0/recipe.yaml",
	"recipe/helm/openai/1.0.0/recipe.yaml",
)

// TestTheImportsTargetDecidesTheLinesOnlyOneTargetNeeds imports Langflow 1.6.0's
// Basic Prompting, whose Language Model node is referenced and wired to the
// platform's model socket, once for each target and once naming none, and reads
// the drafted Aggregate's Langflow instance:
//
//   - for compose, whose Recipe reads every secret the Release alone attaches under
//     one default key, the instance declares the administrator's password with the
//     key the compose Recipe reads it under, as lines of the draft, beneath a
//     comment saying why, and shows nothing commented out for an author to
//     uncomment;
//   - for helm it writes none of those lines;
//   - naming no target it shows them commented out, as the draft has for every
//     target the import does not name.
//
// Every draft reads as an Aggregate, and the drafted Release names the drafted
// default environment. The target reaches the importer on the payload the host
// hands it: nothing else tells the importer which target the import drafts for.
//
// 🔴 PLANTED DEFECT: in Import (internal/importops/import.go), hand
// importersocket.ImportArtifact "" in place of target: the compose row fails, its
// lines commented out, and the helm row fails, its lines shown.
func TestTheImportsTargetDecidesTheLinesOnlyOneTargetNeeds(t *testing.T) {
	t.Parallel()
	const (
		declared = "      features:\n" +
			"        # For the compose target, the lines below declare admin_password on langflow: "
		lines = "        admin_password:\n" +
			"          type: core/secret-value\n" +
			"          values:\n" +
			"            key: LANGFLOW_SUPERUSER_PASSWORD\n"
		commented = "      # For the compose target only, uncomment the lines below, which declare admin_password on " +
			"langflow: "
	)
	for _, row := range []struct {
		target                      string
		declared, commented, absent bool
	}{
		{target: "compose", declared: true},
		{target: "helm", absent: true},
		{target: "", commented: true},
	} {
		t.Run("target "+row.target, func(t *testing.T) {
			t.Parallel()
			rw, folder := catalogFolderImportWorkspaceAttaching(t, referencingLanguageModel())
			layHubEntities(t, folder.Dir, targetAttachmentHubEntities)
			res, dir := targetedImport(t, rw, "basic-prompting.json", readRealExport(t, langflowSelectingProject),
				row.target, authoredcontent.DeliveryFor(false))
			if res.Format != "langflow" || res.Halted() {
				t.Fatalf("the source was read as %q, halted %v: %+v", res.Format, res.Halted(), res.Diagnostics)
			}
			text := readDraftedAggregate(t, dir)
			if got := strings.Contains(text, declared) && strings.Contains(text, lines); got != row.declared {
				t.Errorf("the instance declares the compose lines: %t, want %t:\n%s", got, row.declared, text)
			}
			if got := strings.Contains(text, commented); got != row.commented {
				t.Errorf("the instance shows the compose lines commented out: %t, want %t:\n%s", got, row.commented,
					text)
			}
			if row.absent && strings.Contains(text, "LANGFLOW_SUPERUSER_PASSWORD") {
				t.Errorf("the draft for %s writes a line of the compose attachment:\n%s", row.target, text)
			}
			if agg := readAsTheCompilerDoes(t, text); agg.Spec.Components["langflow"].Type != "core/ai-agent/langflow" {
				t.Errorf("the draft holds no Langflow instance keyed langflow:\n%s", text)
			}
			release, err := os.ReadFile(filepath.Join(dir, "release.yaml"))
			if err != nil {
				t.Fatalf("read the drafted Release: %v", err)
			}
			if !strings.Contains(string(release), "\n  environment: development\n") {
				t.Errorf("the drafted Release names no environment:\n%s", release)
			}
		})
	}
}
