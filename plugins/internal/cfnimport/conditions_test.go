// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	cfndraft "github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft/cloudformation"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// switched is a template deploying the queue under a condition comparing one
// parameter, allowing only true and false, with true.
const switched = `AWSTemplateFormatVersion: "2010-09-09"
Parameters:
  Enabled: {Type: String, Default: 'true', AllowedValues: ['true', 'false']}
Conditions:
  IsEnabled: !Equals [!Ref Enabled, 'true']
Resources:
  Queue:
    Type: AWS::SQS::Queue
    Condition: IsEnabled
`

// TestConditionOnABooleanParameterIsTheParameter lowers a resource's condition
// comparing one parameter that allows only true and false with true, either
// way round and written as text or as booleans, to that parameter, and reads
// the parameter as a boolean: its type, its default, the value the parameters
// file supplies and its allowed values.
func TestConditionOnABooleanParameterIsTheParameter(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ text, params string }{
		"as written":         {switched, ""},
		"either way round":   {replaceOnce(t, switched, "[!Ref Enabled, 'true']", "['true', !Ref Enabled]"), ""},
		"written as boolean": {strings.ReplaceAll(switched, "'true'", "true"), ""},
		"supplied":           {switched, `[{"ParameterKey": "Enabled", "ParameterValue": "false"}]`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g, diags := lowered(c.text, c.params)
			noDiagnostics(t, diags)
			if got, want := node(t, g, "Queue").Condition, (cloudimport.Param{Name: "Enabled"}); !reflect.DeepEqual(got,
				want) {
				t.Errorf("the condition lowers to %#v, want %#v", got, want)
			}
			want := cloudimport.Parameter{Name: "Enabled", Type: "boolean", Default: lit(`true`),
				Allowed: []json.RawMessage{json.RawMessage(`true`), json.RawMessage(`false`)}}
			if c.params != "" {
				want.Supplied = lit(`false`)
			}
			if !reflect.DeepEqual(g.Params, []cloudimport.Parameter{want}) {
				t.Errorf("the parameters read\n%+v\nwant\n%+v", g.Params, []cloudimport.Parameter{want})
			}
		})
	}
}

// TestConditionOtherwiseIsNotCarried warns condition-not-carried, naming the
// condition, of each condition that is not one parameter allowing only true
// and false compared with true, reads the resource as deployed whatever the
// condition decides, and leaves the parameter as the template declares it.
func TestConditionOtherwiseIsNotCarried(t *testing.T) {
	t.Parallel()
	notCarried := func(condition string) aggrega.Diagnostic {
		named := "the condition " + condition
		if condition == "" {
			named = "a condition it names by no text"
		}
		return aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "condition-not-carried",
			Message: "the template template.yaml deploys Queue under " + named + ", which is not " +
				"one template parameter allowing only true and false compared with true, so the condition is not " +
				"carried and the resource is drafted as deployed whatever the condition decides: write active on its " +
				"instance by hand, switching off with it every instance whose connection reaches it, as a " +
				"switched-off target halts the build", SourceRef: "template.yaml#/Resources/Queue/Condition"}
	}
	for name, c := range map[string]struct{ old, new, condition string }{
		"compared with false": {"!Equals [!Ref Enabled, 'true']", "!Equals [!Ref Enabled, 'false']", "IsEnabled"},
		"allowing another value": {"AllowedValues: ['true', 'false']", "AllowedValues: ['true', 'false', 'maybe']",
			"IsEnabled"},
		"allowing any value": {", AllowedValues: ['true', 'false']", "", "IsEnabled"},
		"composed": {"!Equals [!Ref Enabled, 'true']",
			"!And [!Equals [!Ref Enabled, 'true'], !Equals [!Ref Enabled, 'true']]", "IsEnabled"},
		"of a NoEcho parameter": {"Type: String,", "Type: String, NoEcho: true,", "IsEnabled"},
		"of a pseudo parameter": {"!Ref Enabled", "!Ref 'AWS::Region'", "IsEnabled"},
		"declared nowhere":      {"Condition: IsEnabled", "Condition: IsMissing", "IsMissing"},
		"named by no text":      {"Condition: IsEnabled", "Condition: [IsEnabled]", ""},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			g, diags := lowered(replaceOnce(t, switched, c.old, c.new), "")
			onlyDiagnostic(t, diags, notCarried(c.condition))
			if got := node(t, g, "Queue").Condition; got != nil {
				t.Errorf("the condition lowers to %#v, want none", got)
			}
			if g.Params[0].Type != "String" {
				t.Errorf("the parameter Enabled reads as type %q, want String", g.Params[0].Type)
			}
		})
	}
}

// TestConditionWritesActiveThroughTheCore imports the switched template
// through the core: the parameter the condition switches on is an Aggregate
// parameter of type boolean, and the instance's active reads it, with no
// condition-not-carried warning.
func TestConditionWritesActiveThroughTheCore(t *testing.T) {
	t.Parallel()
	f := cloudimport.Format{Name: formatName, Target: target, Syntax: "yaml", Read: Read, Key: Key,
		Draft: cfndraft.Format}
	out := cloudimport.Import(aggrega.ImportInput{Format: formatName, Target: target,
		Sources: []aggrega.ImportSource{src("template.yaml", []byte(switched))}}, f, parse)
	for _, d := range out.Diagnostics {
		if d.Code == "condition-not-carried" {
			t.Errorf("the import reports %+v", d)
		}
	}
	var aggregate string
	for _, file := range out.Files {
		if file.Path == "aggregate.yaml" {
			aggregate = string(file.Content)
		}
	}
	for _, line := range []string{"    enabled:\n      type: boolean\n      default: true\n",
		"      active: \"{{ .parameters.enabled }}\"\n"} {
		if !strings.Contains(aggregate, line) {
			t.Errorf("the Aggregate holds no %q:\n%s", line, aggregate)
		}
	}
}
