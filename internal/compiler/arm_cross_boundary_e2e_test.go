// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof of how the arm provider wires a uses edge whose two ends
// assemble into different templates, each deployed under a name of its own.
//
// The producing template gains a string output per provides key the consumer
// reads, and records the name it is deployed under in its metadata. The consuming
// template reads that output with a reference() over the producing deployment's
// resource ID, built from the producer's subscription and resource group where the
// Release declares them and from the consumer's own where it does not. Every build
// runs through the real compiled Wasm guest, and each test builds more than once
// and requires identical bytes.

package compiler_test

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const (
	armLinkDir     = "testdata/arm-two-deployment/"
	armLinkCatalog = armLinkDir + "catalog"

	// The name the data boundary's template is deployed under for the fixture's
	// Release, written as the literal the emitted template carries.
	armLinkDataDeployment = "aggrega--arm-two-deployment--dist-zsdata-zsarm"

	// The expression the datastore's Recipe provides as its host.
	armLinkHostExpression = "reference('Store').fullyQualifiedDomainName"

	// The placement the declared-placement Releases give the data boundary.
	armLinkDeclaredSubscription  = "11111111-1111-1111-1111-111111111111"
	armLinkDeclaredResourceGroup = "orders-data"
)

// armLinkInput is the build input for one Release of the two-deployment fixture,
// built against the shared catalog, the composition catalog and the fixture's own.
func armLinkInput(release string) pipelinetest.Input {
	return armInput(armLinkDir+release, armComposeCatalog, armLinkCatalog)
}

// armLinkRead is the expression a consuming template writes to read an output of
// the producing deployment: the deployments resource ID built from the given
// subscription and resource group expressions and the deployment's name.
func armLinkRead(subscription, resourceGroup, deployment, output string) string {
	return fmt.Sprintf("reference(format('/subscriptions/{0}/resourceGroups/{1}/providers/Microsoft.Resources/deployments/{2}', %s, %s, '%s'), '2025-04-01').outputs.%s.value",
		subscription, resourceGroup, deployment, output)
}

// armLinkSameGroupRead is armLinkRead where the producer shares the consumer's
// subscription and resource group.
func armLinkSameGroupRead(deployment, output string) string {
	return armLinkRead("subscription().subscriptionId", "resourceGroup().name", deployment, output)
}

// armLinkDeclaredRead is armLinkRead where the producer's boundary declares its
// subscription and resource group.
func armLinkDeclaredRead(deployment, output string) string {
	return armLinkRead("'"+armLinkDeclaredSubscription+"'", "'"+armLinkDeclaredResourceGroup+"'", deployment, output)
}

// armNoLinkPlaceholder fails the test when any emitted artifact still holds a
// cross-boundary link placeholder.
func armNoLinkPlaceholder(t *testing.T, artifacts map[string][]byte) {
	t.Helper()
	for k, v := range artifacts {
		if strings.Contains(string(v), rendercore.CrossBoundaryLinkSentinelPrefix) {
			t.Errorf("artifact %q retains a cross-boundary link placeholder:\n%s", k, v)
		}
	}
}

// armLinkOutputs returns the outputs object of a decoded template.
func armLinkOutputs(t *testing.T, template map[string]any) map[string]any {
	t.Helper()
	return armSection(t, template, "outputs")
}

// armLinkOutputNames returns the sorted names of a template's outputs.
func armLinkOutputNames(t *testing.T, template map[string]any) []string {
	t.Helper()
	names := mapKeys(armLinkOutputs(t, template))
	sort.Strings(names)
	return names
}

// armAssertHostOutput asserts the producing template writes the host as a string
// output named for the component and the key, holding the provides expression
// passed through string().
func armAssertHostOutput(t *testing.T, producer map[string]any) {
	t.Helper()
	out, ok := armLinkOutputs(t, producer)["store__host"].(map[string]any)
	if !ok {
		t.Fatalf("the producing template writes no output store__host; it writes %v", armLinkOutputNames(t, producer))
	}
	if out["type"] != "string" {
		t.Errorf("the output store__host has type %#v, want string", out["type"])
	}
	if want := "[string(" + armLinkHostExpression + ")]"; out["value"] != want {
		t.Errorf("the output store__host holds %#v, want %q", out["value"], want)
	}
}

// armAssertDeploymentName asserts the template records the name it is deployed
// under in its metadata.
func armAssertDeploymentName(t *testing.T, template map[string]any, want string) {
	t.Helper()
	metadata := armSection(t, template, "metadata")
	if got := metadata[armDeploymentNameKey]; got != want {
		t.Errorf("the template records the deployment name %#v under %s, want %q", got, armDeploymentNameKey, want)
	}
}

// armDeploymentNameKey is the metadata key a template records the name it is
// deployed under, written as the literal the emitted template carries.
const armDeploymentNameKey = "aggrega.ai/deployment-name"

// armConsumerSetting returns one entry of the settings of the fixture
// application resource whose symbolic name is symbol.
func armConsumerSetting(t *testing.T, template map[string]any, symbol, key string) any {
	t.Helper()
	properties, ok := armResourceBody(t, template, symbol)["properties"].(map[string]any)
	if !ok {
		t.Fatalf("resource %q carries no properties object", symbol)
	}
	settings, ok := properties["settings"].(map[string]any)
	if !ok {
		t.Fatalf("resource %q carries no settings object", symbol)
	}
	return settings[key]
}

// armWantSetting asserts one setting of a consuming resource holds the whole
// expression, in brackets.
func armWantSetting(t *testing.T, template map[string]any, symbol, key, expression string) {
	t.Helper()
	if got, want := armConsumerSetting(t, template, symbol, key), "["+expression+"]"; got != want {
		t.Errorf("setting %s of %s is %#v, want %q", key, symbol, got, want)
	}
}

// armAssertHaltsTwice builds the input twice, requires both builds to halt on the
// condition, and returns the cause chain after requiring the two to be the same
// text: a halt is as deterministic as an artifact.
func armAssertHaltsTwice(t *testing.T, in pipelinetest.Input, condition error) string {
	t.Helper()
	var chains [2]string
	for i := range chains {
		_, err := pipelinetest.BuildE(t, in)
		if err == nil {
			t.Fatal("the build succeeded; want a halt")
		}
		if !errors.Is(err, condition) {
			t.Fatalf("the build failed for a different reason than the one under test: %v", err)
		}
		chains[i] = cerrors.CauseChain(err)
	}
	if chains[0] != chains[1] {
		t.Errorf("two builds of one input halt with different text:\n--- first ---\n%s\n--- second ---\n%s", chains[0], chains[1])
	}
	return chains[0]
}

// TestArmLinkInTheSameResourceGroup builds a datastore and the application that
// reads its host into two boundaries whose Release declares no placement. The
// datastore's template gains a host output and records its deployment name, and
// the application reads that output from the deployment by a resource ID built
// from its own subscription and resource group.
func TestArmLinkInTheSameResourceGroup(t *testing.T) {
	t.Parallel()
	in := armLinkInput("release-same-group.yaml")
	result := pipelinetest.Build(t, in)
	armNoLinkPlaceholder(t, result.Artifacts)

	producer, producerRaw := findArmTemplate(t, result.Artifacts, "dist/data/")
	consumer, consumerRaw := findArmTemplate(t, result.Artifacts, "dist/web/")
	t.Logf("producing template:\n%s", producerRaw)
	t.Logf("consuming template:\n%s", consumerRaw)

	armAssertHostOutput(t, producer)
	armAssertDeploymentName(t, producer, armLinkDataDeployment)
	armWantSetting(t, consumer, "App", "databaseHost", armLinkSameGroupRead(armLinkDataDeployment, "store__host"))
	pipelinetest.AssertDeterministic(t, in)
}

// TestArmLinkWithDeclaredPlacement builds the same two boundaries with the
// datastore's boundary declaring its subscription and resource group. The
// application's resource ID names them as string literals in place of its own.
func TestArmLinkWithDeclaredPlacement(t *testing.T) {
	t.Parallel()
	in := armLinkInput("release-declared-placement.yaml")
	result := pipelinetest.Build(t, in)
	armNoLinkPlaceholder(t, result.Artifacts)

	producer, _ := findArmTemplate(t, result.Artifacts, "dist/data/")
	consumer, consumerRaw := findArmTemplate(t, result.Artifacts, "dist/web/")
	t.Logf("consuming template:\n%s", consumerRaw)

	armAssertHostOutput(t, producer)
	armAssertDeploymentName(t, producer, armLinkDataDeployment)
	armWantSetting(t, consumer, "App", "databaseHost", armLinkDeclaredRead(armLinkDataDeployment, "store__host"))
	pipelinetest.AssertDeterministic(t, in)
}

// TestArmTwoConsumersShareOneOutput builds a datastore read by two applications
// in two other boundaries. The datastore's template carries one host output, and
// both applications read that output.
func TestArmTwoConsumersShareOneOutput(t *testing.T) {
	t.Parallel()
	in := armLinkInput("release-two-consumers.yaml")
	result := pipelinetest.Build(t, in)
	armNoLinkPlaceholder(t, result.Artifacts)

	producer, _ := findArmTemplate(t, result.Artifacts, "dist/data/")
	if got := armLinkOutputNames(t, producer); len(got) != 1 || got[0] != "store__host" {
		t.Fatalf("the datastore's template writes the outputs %v, want exactly store__host", got)
	}
	armAssertHostOutput(t, producer)

	want := armLinkSameGroupRead(armLinkDataDeployment, "store__host")
	for _, c := range []struct{ boundary, symbol string }{{"dist/web/", "App"}, {"dist/jobs/", "Reports"}} {
		consumer, _ := findArmTemplate(t, result.Artifacts, c.boundary)
		armWantSetting(t, consumer, c.symbol, "databaseHost", want)
	}
	pipelinetest.AssertDeterministic(t, in)
}

// TestArmReadKeysLiteralAndExpression builds an application whose Recipe reads
// the datastore's host and port directly off its connection, which injects
// nothing. The host is an expression, so it crosses through an output; the port
// is literal text, which means the same in any template, so it is written in
// place and no output carries it.
func TestArmReadKeysLiteralAndExpression(t *testing.T) {
	t.Parallel()
	in := armLinkInput("release-read.yaml")
	result := pipelinetest.Build(t, in)
	armNoLinkPlaceholder(t, result.Artifacts)

	producer, _ := findArmTemplate(t, result.Artifacts, "dist/data/")
	consumer, consumerRaw := findArmTemplate(t, result.Artifacts, "dist/web/")
	t.Logf("consuming template:\n%s", consumerRaw)

	if got := armLinkOutputNames(t, producer); len(got) != 1 || got[0] != "store__host" {
		t.Errorf("the datastore's template writes the outputs %v, want exactly store__host: the literal port needs none", got)
	}
	armWantSetting(t, consumer, "App", "databaseHost", armLinkSameGroupRead(armLinkDataDeployment, "store__host"))
	if got := armConsumerSetting(t, consumer, "App", "databasePort"); got != "5432" {
		t.Errorf("setting databasePort is %#v, want the literal \"5432\"", got)
	}
	pipelinetest.AssertDeterministic(t, in)
}

// TestArmPlaceholderAsAFormatArgument builds an application that writes the host
// inside literal text. Within one template the assembly writes that text as a
// format() expression; across the link the host is an argument of that format()
// call, and the link replaces the argument with the read of the producing
// deployment's output.
func TestArmPlaceholderAsAFormatArgument(t *testing.T) {
	t.Parallel()
	in := armLinkInput("release-compose.yaml")
	result := pipelinetest.Build(t, in)
	armNoLinkPlaceholder(t, result.Artifacts)

	consumer, consumerRaw := findArmTemplate(t, result.Artifacts, "dist/web/")
	t.Logf("consuming template:\n%s", consumerRaw)

	got, ok := armConsumerSetting(t, consumer, "App", "connection").(string)
	if !ok {
		t.Fatalf("setting connection is not a string")
	}
	for _, want := range []string{
		"[format('postgres://{0}:5432', reference(format(",
		"'" + armLinkDataDeployment + "'",
		").outputs.store__host.value)]",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("setting connection is %q, which does not contain %q", got, want)
		}
	}
	pipelinetest.AssertDeterministic(t, in)
}

// TestArmPlaceholderInTagsHalts builds an application that writes the host into a
// tag. ARM evaluates reference() only inside a resource's properties, an output's
// value or an output's copy input, so the build halts naming the pointer and where
// the value may be written.
func TestArmPlaceholderInTagsHalts(t *testing.T) {
	t.Parallel()
	msg := armAssertHaltsTwice(t, armLinkInput("release-tags.yaml"), plugincondition.ErrPluginValueNotRepresentable())
	for _, want := range []string{
		"arm-provider:",
		"/resources/App/tags/endpoint",
		`provides key "host" of component "store"`,
		"reference()",
		"inside a resource's properties",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmSecretAcrossDeploymentsHalts builds a datastore whose provides host reads
// a secret with a list function. A link would write it as an output the other
// deployment reads, which stores it in that deployment's history, so the build
// halts naming the key and the component, and quotes no part of the expression.
func TestArmSecretAcrossDeploymentsHalts(t *testing.T) {
	t.Parallel()
	msg := armAssertHaltsTwice(t, armLinkInput("release-secret.yaml"), plugincondition.ErrPluginValueNotRepresentable())
	for _, want := range []string{
		"arm-provider:",
		`provides key "host" of component "store" reads a secret`,
		"core/referenced-secret",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
	for _, quoted := range []string{"listKeys", "primaryKey"} {
		if strings.Contains(msg, quoted) {
			t.Errorf("the halt quotes %q from the expression: %s", quoted, msg)
		}
	}
}

// TestArmDeploymentNameMetadata builds the same Release with the datastore's
// output directory changed. The producing template records the name it is
// deployed under, derived from the Release and the boundary's output directory,
// and the consumer reads the deployment by that same recorded name.
func TestArmDeploymentNameMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		release, producerDir, name string
	}{
		{"release-same-group.yaml", "dist/data/", "aggrega--arm-two-deployment--dist-zsdata-zsarm"},
		{"release-renamed.yaml", "dist/storage/", "aggrega--arm-two-deployment--dist-zsstorage-zsarm"},
	} {
		t.Run(tc.release, func(t *testing.T) {
			in := armLinkInput(tc.release)
			result := pipelinetest.Build(t, in)
			producer, _ := findArmTemplate(t, result.Artifacts, tc.producerDir)
			consumer, _ := findArmTemplate(t, result.Artifacts, "dist/web/")
			armAssertDeploymentName(t, producer, tc.name)
			armWantSetting(t, consumer, "App", "databaseHost", armLinkSameGroupRead(tc.name, "store__host"))
			pipelinetest.AssertDeterministic(t, in)
		})
	}
}
