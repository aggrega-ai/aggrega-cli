// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package renderer_test

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderer"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// These tests drive a cloudformation Recipe's omits key end to end through
// RenderAll: a directory of CloudFormation templates fetched from a git repository,
// seeded in the cache as a pull leaves it — pinned at the commit its authored
// reference resolved to — rendered through the embedded cloudformation provider
// guest and assembled into the stack template a build writes. The guest decides what
// an entry leaves out and whether what remains still names it; the host evaluates
// the entries, hands them over, refuses a provider that does not claim to honour
// them and halts on an entry that left nothing out.

const (
	// cfnOmitsURL is the repository the fixture's templates are fetched from.
	cfnOmitsURL = "https://example.com/omits/templates.git"
	// cfnOmitsRef is the reference the Recipe authors, and cfnOmitsCommit the commit
	// a pull pinned it at.
	cfnOmitsRef    = "v1.0.0"
	cfnOmitsCommit = "0123456789abcdef0123456789abcdef01234567"
)

// cfnOmitsTemplate is a fetched template bundling a log bucket beside the bucket the
// Release wants, where nothing names the log bucket.
const cfnOmitsTemplate = `AWSTemplateFormatVersion: "2010-09-09"
Resources:
  Store:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: store
  Logs:
    Type: AWS::S3::Bucket
    Properties:
      BucketName: store-logs
Outputs:
  StoreName:
    Value: !Ref Store
`

// cfnOmitsRecipeLookup resolves the fixture's component types: the fetched-source
// Recipe declaring omits, and — where alongside is set — an artifacts Recipe for a
// second component of the same stack, contributing a resource of its own under the
// logical ID the fetched template's log bucket carries.
func cfnOmitsRecipeLookup(omits []types.OmitEntry, alongside bool) renderer.RecipeLookup {
	return renderer.RecipeLookup{
		Recipe: func(_, componentType string) (*types.Recipe, bool) {
			switch {
			case componentType == "core/compute/api-server":
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "api-recipe", Version: "1.0.0"},
					Spec: types.RecipeSpec{
						Capability: "cloudformation",
						Implements: "core/compute/api-server",
						Source: map[string]any{
							"kind": "git-repository",
							"url":  cfnOmitsURL,
							"ref":  cfnOmitsRef,
						},
						Omits: omits,
					},
				}, true
			case componentType == "core/store" && alongside:
				artifacts := types.OrderedMap[types.ArtifactEntry]{}
				artifacts.Set("logs.yaml", types.ArtifactEntry{Inline: "Resources:\n  Logs:\n    Type: AWS::Logs::LogGroup\n"})
				return &types.Recipe{
					Metadata: types.RecipeMetadata{Name: "logs-recipe", Version: "1.0.0"},
					Spec: types.RecipeSpec{
						Capability: "cloudformation",
						Implements: "core/store",
						Artifacts:  artifacts,
					},
				}, true
			}
			return nil, false
		},
		FeatureRecipe: func(_, _ string) (*types.FeatureRecipe, bool) { return nil, false },
	}
}

// renderCfnOmits renders one component — two where alongside is set — whose
// cloudformation Recipe sources the fetched templates and declares omits, returning
// what the build assembled.
func renderCfnOmits(t *testing.T, templates map[string]string, omits []types.OmitEntry, alongside bool) (map[string]renderer.AssembledFile, error) {
	t.Helper()
	root := t.TempDir()
	blob := buildIntegrationTGZ(t, templates)
	digest := integrationDigest(blob)
	seedIntegrationCacheSlot(t, root, "git-repository", cfnOmitsURL, cfnOmitsCommit, blob, digest)

	input := integrationInput(t, root, "core/compute/api-server", "api", "cloudformation", digest, cfnOmitsURL, cfnOmitsCommit)
	row := &input.Lockfile.SourceArtifacts[0]
	row.Kind = "git-repository"
	row.Mediatype = "application/gzip"
	row.ReferencedBy[0].RequestedRef = cfnOmitsRef
	// The rules over what a dispatch group's contributors declared are invoked only
	// when the render is handed the presentation the graph finalisation produces, as
	// every build is. Without it the rule over a name two contributors derived runs
	// zero times, and a rule invoked zero times reports what a rule that ran and
	// approved reports. The stand-in answers no graph fact.
	input.GraphView = derivedKeyGraphFacts{}
	if alongside {
		labels := types.OrderedMap[string]{}
		labels.Set("component.aggrega.ai/type", "resource")
		input.Graph.Spec.Nodes.Set("logs", types.IRNode{
			Component:       "core/store",
			Labels:          labels,
			ResolvedVersion: "1.0.0",
			BoundaryPath:    "local/default",
		})
		input.Graph.Spec.ValuesMapping.Set("logs", json.RawMessage(`{}`))
	}

	result, err := renderer.RenderAll(context.Background(), input, cfnOmitsRecipeLookup(omits, alongside), wasmEngineFactory(t))
	if err != nil {
		return nil, err
	}
	return result.Files, nil
}

// assembledStackResources returns each resource the assembled stack template
// declares, by logical ID, with its Type, or fails when the build assembled no stack.
func assembledStackResources(t *testing.T, files map[string]renderer.AssembledFile) map[string]string {
	t.Helper()
	for _, path := range slices.Sorted(maps.Keys(files)) {
		if !strings.HasSuffix(path, "cloudformation-stack.json") {
			continue
		}
		var doc struct {
			Resources map[string]struct {
				Type string `json:"Type"`
			} `json:"Resources"`
		}
		if err := json.Unmarshal(files[path].Content, &doc); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		out := make(map[string]string, len(doc.Resources))
		for id, r := range doc.Resources {
			out[id] = r.Type
		}
		return out
	}
	t.Fatalf("the build assembled no stack template; files: %v", slices.Sorted(maps.Keys(files)))
	return nil
}

// requireCloudFormationNotRefusedForTheContract fails the test with the reason when
// the build was refused because the cloudformation provider's declared contract does
// not claim to honour omits — the refusal every cloudformation Recipe declaring the
// key met before the provider claimed it, and the one a stale embedded guest still
// produces.
func requireCloudFormationNotRefusedForTheContract(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, renderer.ErrUnsupportedOmits()) {
		t.Fatalf("the cloudformation Recipe declaring omits was refused before it rendered, because the provider does "+
			"not claim to honour the key — is the embedded cloudformation guest built from this tree?\n%s", cerrors.CauseChain(err))
	}
}

// TestRenderAll_CloudFormationOmitsLeaveAResourceOutOfAFetchedSource is the key's
// purpose read through the build: a fetched template bundles a log bucket the Release
// must not create. Without the key the assembled stack carries both buckets, which is
// what shows the build can see the difference; with an entry naming the log bucket by
// its Type and logical ID it carries the store alone, and two builds assemble the same
// bytes.
func TestRenderAll_CloudFormationOmitsLeaveAResourceOutOfAFetchedSource(t *testing.T) {
	t.Parallel()

	templates := map[string]string{"store.yaml": cfnOmitsTemplate}
	control, err := renderCfnOmits(t, templates, nil, false)
	if err != nil {
		t.Fatalf("the build without the key: %s", cerrors.CauseChain(err))
	}
	if got, want := slices.Sorted(maps.Keys(assembledStackResources(t, control))), []string{"Logs", "Store"}; !slices.Equal(got, want) {
		t.Fatalf("the build without the key assembles %v, want %v — the rows below read a stack the source no longer renders", got, want)
	}

	omits := []types.OmitEntry{{Kind: "AWS::S3::Bucket", Name: "Logs"}}
	once, err := renderCfnOmits(t, templates, omits, false)
	requireCloudFormationNotRefusedForTheContract(t, err)
	if err != nil {
		t.Fatalf("the build leaving Logs out: %s", cerrors.CauseChain(err))
	}
	if got, want := slices.Sorted(maps.Keys(assembledStackResources(t, once))), []string{"Store"}; !slices.Equal(got, want) {
		t.Errorf("the build leaving Logs out assembles %v, want %v", got, want)
	}
	twice, err := renderCfnOmits(t, templates, omits, false)
	if err != nil {
		t.Fatalf("the second build leaving Logs out: %s", cerrors.CauseChain(err))
	}
	if !reflect.DeepEqual(once, twice) {
		t.Errorf("two builds leaving Logs out assembled different files")
	}
}

// TestRenderAll_CloudFormationOmitsLeftOutResourceIsNotDeclared reads the names the
// guest declares for the duplicate-name check through the build. A second component
// of the same stack contributes a log group under the logical ID the fetched
// template's log bucket carries: without the key the two meet and the build halts on
// the name two contributors derived; with the bucket left out the fetched component
// no longer declares it, the build passes, and the stack carries the other
// component's log group.
func TestRenderAll_CloudFormationOmitsLeftOutResourceIsNotDeclared(t *testing.T) {
	t.Parallel()

	templates := map[string]string{"store.yaml": cfnOmitsTemplate}
	_, err := renderCfnOmits(t, templates, nil, true)
	if !errors.Is(err, renderer.ErrDerivedKeyCollision()) {
		t.Fatalf("two components declaring Logs: got %v, want the halt on a name two contributors derived", err)
	}
	if chain := cerrors.CauseChain(err); !strings.Contains(chain, "Logs") {
		t.Errorf("the halt does not name the logical ID the two met on:\n%s", chain)
	}

	files, err := renderCfnOmits(t, templates, []types.OmitEntry{{Kind: "AWS::S3::Bucket", Name: "Logs"}}, true)
	requireCloudFormationNotRefusedForTheContract(t, err)
	if err != nil {
		t.Fatalf("the build leaving the fetched Logs out: %s", cerrors.CauseChain(err))
	}
	resources := assembledStackResources(t, files)
	if got, want := resources["Logs"], "AWS::Logs::LogGroup"; got != want {
		t.Errorf("the stack's Logs is a %q, want the other component's %q", got, want)
	}
	if _, ok := resources["Store"]; !ok {
		t.Errorf("the stack lost the fetched template's Store: %v", resources)
	}
}

// TestRenderAll_CloudFormationOmitsEntryThatLeftNothingOutHalts holds the host's
// halt on an entry the cloudformation provider reported as having left nothing out,
// naming the entry's position and the kind and name it resolved to.
func TestRenderAll_CloudFormationOmitsEntryThatLeftNothingOutHalts(t *testing.T) {
	t.Parallel()

	_, err := renderCfnOmits(t, map[string]string{"store.yaml": cfnOmitsTemplate}, []types.OmitEntry{
		{Kind: "AWS::S3::Bucket", Name: "Logs"},
		{Kind: "AWS::S3::Bucket", Name: "Archive"},
	}, false)
	requireCloudFormationNotRefusedForTheContract(t, err)
	if !errors.Is(err, renderer.ErrOmitMatchedNothing()) {
		t.Fatalf("an entry naming a resource the template does not declare: got %v, want the halt on an entry that left nothing out", err)
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{`"api-recipe" omits[1]`, `kind "AWS::S3::Bucket"`, `name "Archive"`} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
	if strings.Contains(chain, "omits[0]") {
		t.Errorf("the halt names the entry that left its resource out:\n%s", chain)
	}
}

// TestRenderAll_CloudFormationOmitsRemainingRefHalts holds the provider's halt on a
// template in which a remaining resource still names the one an entry left out: the
// store bucket writes its access logs to the log bucket by Ref, so leaving the log
// bucket out would assemble a stack CloudFormation refuses, and the build stops with
// the provider's own sentence naming the template, the resource, the Ref and where it
// sits.
func TestRenderAll_CloudFormationOmitsRemainingRefHalts(t *testing.T) {
	t.Parallel()

	source := strings.Replace(cfnOmitsTemplate, "      BucketName: store\n",
		"      BucketName: store\n      LoggingConfiguration:\n        DestinationBucketName: !Ref Logs\n", 1)
	templates := map[string]string{"store.yaml": source}
	if _, err := renderCfnOmits(t, templates, nil, false); err != nil {
		t.Fatalf("the template builds without the key, so the halt below is the reference's: %s", cerrors.CauseChain(err))
	}

	_, err := renderCfnOmits(t, templates, []types.OmitEntry{{Kind: "AWS::S3::Bucket", Name: "Logs"}}, false)
	requireCloudFormationNotRefusedForTheContract(t, err)
	if !errors.Is(err, plugincondition.ErrPluginRequiredInputUnresolved()) {
		t.Fatalf("leaving out a bucket another still names: got %v, want the provider's halt on the remaining reference", err)
	}
	chain := cerrors.CauseChain(err)
	for _, want := range []string{
		plugincondition.ErrPluginRequiredInputUnresolved().Error(),
		`cloudformation-provider: what an omits entry left out is still named by the source's templates: ` +
			`template "store.yaml" names resource "Logs" (type "AWS::S3::Bucket") by Ref at /Resources/Store/Properties/LoggingConfiguration/DestinationBucketName`,
	} {
		if !strings.Contains(chain, want) {
			t.Errorf("the halt does not carry %q:\n%s", want, chain)
		}
	}
}
