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
// beside this one. Without that checkout they skip, naming it.

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/authoredcontent"
)

// targetAttachmentHubEntities are platformImportEntities with the compose and
// helm Recipes of the model the Langflow platform's model socket draws, and the
// two secret Features with the realization of each the drafts map them to.
var targetAttachmentHubEntities = append(slices.Clone(platformImportEntities),
	"recipe/compose/openai/1.0.0/recipe.yaml",
	"recipe/helm/openai/1.0.0/recipe.yaml",
	"feature/core/secret-value/1.0.0/feature.yaml",
	"feature/core/referenced-secret/1.0.0/feature.yaml",
	"feature-recipe/compose/dotenv-secret/1.0.0/recipe.yaml",
	"feature-recipe/helm/k8s-existing-secret/1.0.0/recipe.yaml",
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
// The model connection's credential follows the same split, declared on the
// connection with its own key, OPENAI_API_KEY, for compose alone. The drafted
// Release runs the platform from the image the importer drafts, explicitly, and
// attaches the administrator's password and the model connection's credential
// as references, never values: for compose and for no target as the variables
// LANGFLOW_ADMIN_PASSWORD and OPENAI_API_KEY, for helm as Kubernetes Secrets
// whose names are left empty, with the drafted Manifest mapping
// core/referenced-secret to helm/k8s-existing-secret.
//
// Every draft reads as an Aggregate, and the drafted Release names the drafted
// default environment. The target reaches the importer on the payload the host
// hands it: nothing else tells the importer which target the import drafts for.
//
// 🔴 PLANTED DEFECT: in Import (internal/importops/import.go), hand
// importersocket.ImportArtifact "" in place of target: the compose row fails, its
// lines commented out, and the helm row fails, its lines shown and its secrets
// written as variable references.
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
		credentialKey = "            features:\n              credential:\n                type: core/secret-value\n" +
			"                values:\n                  key: OPENAI_API_KEY\n"
		image      = "        image: \"langflowai/langflow:1.6.0\"\n"
		byVariable = "origin: \"${LANGFLOW_ADMIN_PASSWORD}\"\n"
		byKey      = "origin: \"${OPENAI_API_KEY}\"\n"
		bySecret   = "type: core/referenced-secret\n"
		mapped     = "        core/referenced-secret: helm/k8s-existing-secret\n"
	)
	for _, row := range []struct {
		target                      string
		declared, commented, absent bool
		release, notInRelease       []string
		secretMapped                bool
	}{
		{target: "compose", declared: true, release: []string{image, byVariable, byKey},
			notInRelease: []string{bySecret}},
		{target: "helm", absent: true, release: []string{image, bySecret, "origin: \"\"\n"},
			notInRelease: []string{"${"}, secretMapped: true},
		{target: "", commented: true, release: []string{image, byVariable, byKey}, notInRelease: []string{bySecret}},
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
			if row.absent && (strings.Contains(text, "LANGFLOW_SUPERUSER_PASSWORD") || strings.Contains(text,
				"OPENAI_API_KEY\n")) {
				t.Errorf("the draft for %s writes a line of the compose attachments:\n%s", row.target, text)
			}
			if got := strings.Contains(text, credentialKey); got != row.declared {
				t.Errorf("the model connection declares its credential's key: %t, want %t:\n%s", got, row.declared, text)
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
			for _, want := range row.release {
				if !strings.Contains(string(release), want) {
					t.Errorf("the drafted Release does not hold %q:\n%s", want, release)
				}
			}
			for _, absent := range row.notInRelease {
				if strings.Contains(string(release), absent) {
					t.Errorf("the drafted Release holds %q:\n%s", absent, release)
				}
			}
			manifest, err := os.ReadFile(filepath.Join(dir, "manifest.yaml"))
			if row.target != "" && err != nil {
				t.Fatalf("read the drafted Manifest: %v", err)
			}
			if got := strings.Contains(string(manifest), mapped); got != row.secretMapped {
				t.Errorf("the drafted Manifest maps core/referenced-secret to helm/k8s-existing-secret: %t, want %t:\n%s",
					got, row.secretMapped, manifest)
			}
		})
	}
}
