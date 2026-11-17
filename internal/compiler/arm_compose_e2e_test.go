// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

// End-to-end proof of how an ARM template composes a value a neighbour delivers
// into the literal text around it.
//
// A datastore publishes its host as an ARM expression. An application's Recipe
// may write that value whole, inside literal text, or inside an expression of its
// own. Whole, the expression is evaluated as written. Inside literal text, the
// provider writes the text as a format() expression so ARM evaluates the value,
// and reports each rewrite. Inside an expression of its own, the nested brackets
// make the expression unreadable, and the build halts naming the pointer. Text
// that merely contains brackets (array[0], [a-z], [test] value) is not an ARM
// expression and reaches the template byte for byte.

package compiler_test

import (
	"errors"
	"strings"
	"testing"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/pipeline/pipelinetest"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

const (
	armComposeDir     = "testdata/arm-compose/"
	armComposeCatalog = armComposeDir + "catalog"

	// The host the datastore publishes, written as the expression the emitted
	// template carries.
	armComposeHost = "reference('Store').fullyQualifiedDomainName"
)

func armComposeInput(release string) pipelinetest.Input {
	return armInput(armComposeDir+release, armComposeCatalog)
}

// armAppSettings returns the settings object of the fixture application's
// resource.
func armAppSettings(t *testing.T, template map[string]any) map[string]any {
	t.Helper()
	properties, ok := armResourceBody(t, template, "App")["properties"].(map[string]any)
	if !ok {
		t.Fatalf("the application's resource carries no properties object")
	}
	settings, ok := properties["settings"].(map[string]any)
	if !ok {
		t.Fatalf("the application's resource carries no settings object")
	}
	return settings
}

// TestArmComposeInOneTemplate builds an application that writes the delivered
// host inside literal text, the way a connection string is written:
// postgres://<host>:5432. The datastore lands in the same template, so the text
// is written as a format() expression holding the host expression, and the
// rewrite is reported as an information diagnostic pointing at the setting.
func TestArmComposeInOneTemplate(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armComposeInput("release-compose.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	t.Logf("assembled template:\n%s", raw)

	want := "[format('postgres://{0}:5432', " + armComposeHost + ")]"
	if got := armAppSettings(t, template)["connection"]; got != want {
		t.Errorf("the connection setting is %#v, want %q", got, want)
	}

	const pointer = "/resources/App/properties/settings/connection"
	reported := armAdvisoryBySourceRef(result.Advisories, pointer)
	if len(reported) != 1 {
		t.Fatalf("want exactly one diagnostic at %s; got %+v", pointer, result.Advisories)
	}
	d := reported[0]
	if d.Severity != abi.SeverityInfo {
		t.Errorf("the rewrite is reported at %q severity, want info: the template is correct and the author is told what was written", d.Severity)
	}
	if d.Code != "" {
		t.Errorf("the rewrite carries the code %q, want none: it is an observation, not a finding", d.Code)
	}
	for _, text := range []string{"arm-provider:", "format()", `"app"`} {
		if !strings.Contains(d.Message, text) {
			t.Errorf("the diagnostic does not say %q: %s", text, d.Message)
		}
	}
	pipelinetest.AssertDeterministic(t, armComposeInput("release-compose.yaml"))
}

// TestArmBracketedTextUntouched builds an application whose settings hold
// bracketed text that ARM does not read as an expression, beside the delivered
// host written whole. The bracketed text reaches the template byte for byte, the
// host is the expression as it stands, and nothing is reported for the settings
// that were left alone.
func TestArmBracketedTextUntouched(t *testing.T) {
	t.Parallel()
	result := pipelinetest.Build(t, armComposeInput("release-bracketed.yaml"))
	template, raw := findArmTemplate(t, result.Artifacts, "stack")
	settings := armAppSettings(t, template)

	for key, want := range map[string]string{
		"databaseHost": "[" + armComposeHost + "]",
		"pattern":      "match [a-z]+ against array[0] in a [test] value",
		"probe":        "if [ -f /etc/ready ]; then echo ready; fi",
	} {
		if got := settings[key]; got != want {
			t.Errorf("setting %s is %#v, want %q\n--- template ---\n%s", key, got, want, raw)
		}
	}
	for _, d := range result.Advisories {
		if strings.HasPrefix(d.SourceRef, "/resources/App") {
			t.Errorf("a diagnostic reports a rewrite in the application though every setting is whole or literal: %+v", d)
		}
	}
	pipelinetest.AssertDeterministic(t, armComposeInput("release-bracketed.yaml"))
}

// TestArmValueInsideExpressionHalts builds an application that writes the
// delivered host inside an expression of its own. The nested brackets make the
// expression unreadable to ARM, so the build halts as a value ARM cannot
// represent, naming the pointer and what to write instead.
func TestArmValueInsideExpressionHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, armComposeInput("release-inside-expression.yaml"))
	if err == nil {
		t.Fatal("a delivered value written inside an expression built cleanly")
	}
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Fatalf("the build failed for a different reason than the value inside an expression: %v", err)
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		"arm-provider:",
		"/resources/App/properties/settings/connection",
		"writes a value inside an expression",
		"compose the value in literal text",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmReferenceInTagsHalts builds an application that writes the delivered
// host into a tag. The host is a reference() call, which ARM evaluates only
// inside a resource's properties, an output's value or an output's copy input, so
// the build halts naming the function and where it may be written.
func TestArmReferenceInTagsHalts(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, armComposeInput("release-tags.yaml"))
	if err == nil {
		t.Fatal("a reference() call written into a tag built cleanly")
	}
	if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
		t.Fatalf("the build failed for a different reason than the reference in tags: %v", err)
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		"arm-provider:",
		"/resources/App/tags/endpoint",
		"reference",
		"inside a resource's properties",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
}

// TestArmCrossDeploymentLinkHaltsOnTheProducersOwnOutput builds a datastore and
// the application that reads it into two boundaries, each assembled into a
// template of its own. The application reads the datastore's host across the
// boundary, so the link writes the host as a string output of the datastore's
// template; the datastore's fragment already writes its own host output, which
// lands under that same name with another body. The build halts as two things
// occupying one name, naming the output, the provides key and the fragment to
// change.
func TestArmCrossDeploymentLinkHaltsOnTheProducersOwnOutput(t *testing.T) {
	t.Parallel()
	_, err := pipelinetest.BuildE(t, armComposeInput("release-two-boundaries.yaml"))
	if err == nil {
		t.Fatal("a link onto an output the producer already writes built cleanly")
	}
	if !errors.Is(err, plugincondition.ErrPluginOutputNameCollision()) {
		t.Fatalf("the build failed for a different reason than the output the link writes: %v", err)
	}
	msg := cerrors.CauseChain(err)
	for _, want := range []string{
		"arm-provider:",
		`the output "store__host"`,
		`provides key "host" of component "store"`,
		`the fragment of "store" writes an output "host"`,
		"rename that output",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the halt does not say %q: %s", want, msg)
		}
	}
	if strings.Contains(msg, "fullyQualifiedDomainName") {
		t.Errorf("the halt quotes the host expression: %s", msg)
	}
}
