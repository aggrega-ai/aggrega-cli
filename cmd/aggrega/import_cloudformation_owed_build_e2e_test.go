// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package main

// End-to-end proof, through the real command tree, that a template whose
// resources the catalog maps but which leaves a value their Component or
// Recipe requires drafts an Aggregate or a Release owing that value, and that
// once the answers file the import writes is answered, the drafts validate and
// build: a SAM function packaged as a .zip owes its container image in the
// Release, a Lambda function run from an image and stating no runtime owes its
// runtime in the Aggregate, and a bucket owes its Region in the Release.
// Nothing the import drafted is edited.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/aggrega-ai/aggrega-cli/internal/embeddedentities"
	"github.com/aggrega-ai/aggrega-cli/internal/importops"
	"github.com/aggrega-ai/aggrega-cli/internal/importops/importopstest"
)

// cfnOwedFixtures is where the committed templates the cases import sit.
const cfnOwedFixtures = "../../internal/importops/testdata/cloudformation/owed"

// cfnStack is the part of the stack template a build writes the cases read.
type cfnStack struct {
	Rules     map[string]map[string]any `json:"Rules"`
	Resources map[string]map[string]any `json:"Resources"`
}

// TestCloudFormationImportOwesWhatItsMappedResourcesRequireAndBuildsOnceAnswered
// imports each template naming no target: the import exits 1, writing an
// answers file listing exactly the values owed, each unanswered; with those
// answered a sync of the drafts exits 0, and the pull, the validation of the catalog and
// of the Release, and the build pass over the drafts, the stack template
// holding each answer where the resource's Recipe writes it.
func TestCloudFormationImportOwesWhatItsMappedResourcesRequireAndBuildsOnceAnswered(t *testing.T) {
	embeddedentities.Require(t)
	withCLIRegistrations(t, nil)
	importopstest.CarryImporters(t, cfnImporterURI)
	isolateFromThisMachine(t)
	hub := siblingCatalogRootForExamples(t)
	const ecrImage = "123456789012.dkr.ecr.eu-west-1.amazonaws.com/resize-image:1.0.0"

	cases := []struct {
		name    string
		answers map[string]map[string]string
		check   func(t *testing.T, stack cfnStack)
	}{
		{
			name: "function-zip",
			answers: map[string]map[string]string{
				"release.yaml": {"spec.components.resize-image.infra.image": ecrImage},
			},
			check: func(t *testing.T, stack cfnStack) {
				fn := objectAt(t, stack.Resources["ResizeImage"], "Properties")
				if got := objectAt(t, fn, "Code")["ImageUri"]; got != ecrImage {
					t.Errorf("the function runs %v, want the answered image %s", got, ecrImage)
				}
				if got := objectAt(t, fn, "Environment", "Variables")["FUNCTION_RUNTIME"]; got != "python3.12" {
					t.Errorf("the function's runtime is %v, want python3.12, the template's", got)
				}
			},
		},
		{
			name: "function-image",
			answers: map[string]map[string]string{
				"aggregate.yaml": {"spec.components.resize-image.values.runtime": "python3.13"},
			},
			check: func(t *testing.T, stack cfnStack) {
				fn := objectAt(t, stack.Resources["ResizeImage"], "Properties")
				if got := objectAt(t, fn, "Code")["ImageUri"]; got != ecrImage {
					t.Errorf("the function runs %v, want the template's image %s", got, ecrImage)
				}
				if got := objectAt(t, fn, "Environment", "Variables")["FUNCTION_RUNTIME"]; got != "python3.13" {
					t.Errorf("the function's runtime is %v, want python3.13, the answered one", got)
				}
			},
		},
		{
			name: "bucket",
			answers: map[string]map[string]string{
				"release.yaml": {"spec.components.order-archive.infra.region": "eu-west-1"},
			},
			check: func(t *testing.T, stack cfnStack) {
				if got := objectAt(t, stack.Resources["OrderArchive"], "Properties")["BucketName"]; got !=
					"orders-archive-example" {
					t.Errorf("the bucket is named %v, want orders-archive-example", got)
				}
				rule, err := json.Marshal(stack.Rules["OrderArchiveRegion"])
				if err != nil || !strings.Contains(string(rule), `"eu-west-1"`) {
					t.Errorf("the stack holds the Region rule %s, want it to hold the stack to eu-west-1", rule)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture, err := filepath.Abs(filepath.Join(cfnOwedFixtures, tc.name))
			if err != nil {
				t.Fatal(err)
			}
			root := armBuildWorkspace(t, hub)
			source := armTemplateSource(t, fixture)
			if code, stdout, commentary, process := runImportAtTheRoot(t, root, source); code != 1 {
				t.Fatalf("the import owing values exited %d, want 1:\n%s\n%s\n%s", code, stdout, commentary, process)
			}
			answersPath := filepath.Join(source, importops.AnswersFileName)
			body, err := os.ReadFile(answersPath)
			if err != nil {
				t.Fatalf("read the answers file the import wrote: %v", err)
			}
			var listed map[string]map[string]string
			if err := yaml.Unmarshal(body, &listed); err != nil {
				t.Fatalf("the answers file does not read as drafted files of key paths: %v\n%s", err, body)
			}
			unanswered := map[string]map[string]string{}
			for file, keys := range tc.answers {
				unanswered[file] = map[string]string{}
				for key := range keys {
					unanswered[file][key] = ""
				}
			}
			if !reflect.DeepEqual(listed, unanswered) {
				t.Fatalf("the answers file lists %v, want exactly %v, unanswered", listed, unanswered)
			}
			answered, err := yaml.Marshal(tc.answers)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(answersPath, answered, 0o644); err != nil {
				t.Fatal(err)
			}
			if code, stdout, commentary, process := runImportAtTheRoot(t, root, source, "--sync"); code != 0 {
				t.Fatalf("the answered sync exited %d:\n%s\n%s\n%s", code, stdout, commentary, process)
			}
			t.Chdir(root)
			mustRunDriftCommand(t, "the pull", "pull", "--release", "release.yaml")
			mustRunDriftCommand(t, "the validation of the catalog", "validate")
			mustRunDriftCommand(t, "the validation of the Release", "validate", "--release", "release.yaml")
			mustRunDriftCommand(t, "the build", "build", "--release", "release.yaml")
			var stack cfnStack
			stackFile := filepath.Join(root, "dist", "cloudformation", "cloudformation-stack.json")
			if err := json.Unmarshal(mustReadFile(t, stackFile), &stack); err != nil {
				t.Fatalf("the stack template is not JSON: %v", err)
			}
			if names := sortedKeys(stack.Resources); !slices.Equal(names, []string{map[string]string{
				"function-zip": "ResizeImage", "function-image": "ResizeImage", "bucket": "OrderArchive"}[tc.name]}) {
				t.Fatalf("the stack template deploys %v", names)
			}
			tc.check(t, stack)
		})
	}
}
