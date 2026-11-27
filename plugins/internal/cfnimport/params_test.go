// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"reflect"
	"slices"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// supplied returns the value each parameter of g is supplied, by name, nil
// for one supplied none.
func supplied(g *cloudimport.Graph) map[string]cloudimport.Value {
	out := map[string]cloudimport.Value{}
	for _, p := range g.Params {
		out[p.Name] = p.Supplied
	}
	return out
}

// readParams reads the parameters template with the parameters file named.
func readParams(t *testing.T, file string) (*cloudimport.Graph, []aggrega.Diagnostic) {
	t.Helper()
	return readAll("", fixtures(t, "params", file, "template.yaml")...)
}

// TestParamsReadsEveryFormTheCliReads reads one set of values written in each
// form the AWS CLI reads a parameters file in — a list of ParameterKey and
// ParameterValue, a list of Key=Value strings each split at its first =, and a
// Parameters object — as the same supplied values.
func TestParamsReadsEveryFormTheCliReads(t *testing.T) {
	t.Parallel()
	want := map[string]cloudimport.Value{"Env": lit(`"prod"`), "Expression": lit(`"a=b=c"`),
		"ImageTag": lit(`"1.4.2"`)}
	for _, file := range []string{"list.json", "pairs.json", "object.json"} {
		g, diags := readParams(t, file)
		noDiagnostics(t, diags)
		if got := supplied(g); !reflect.DeepEqual(got, want) {
			t.Errorf("%s supplies %#v, want %#v", file, got, want)
		}
		if want := []string{file, "template.yaml"}; !slices.Equal(g.Read, want) {
			t.Errorf("%s: the read reads %q, want %q", file, g.Read, want)
		}
	}
}

// TestParamsUsePreviousValueIsNotSupplied supplies no value for a parameter the
// file keeps at the value the deployed stack holds, and names it.
func TestParamsUsePreviousValueIsNotSupplied(t *testing.T) {
	t.Parallel()
	g, diags := readParams(t, "previous.json")
	onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
		Message: "the parameters file previous.json keeps the parameter Env at the value the deployed stack holds, " +
			"with UsePreviousValue, which the import cannot read, so no value is supplied for it: write its " +
			"ParameterValue in the file", SourceRef: "previous.json#/0"})
	want := map[string]cloudimport.Value{"Env": nil, "Expression": nil, "ImageTag": lit(`"1.4.2"`)}
	if got := supplied(g); !reflect.DeepEqual(got, want) {
		t.Errorf("previous.json supplies %#v, want %#v", got, want)
	}
}

// TestParamsOtherMembersOfTheObjectAreNotRead reads a Parameters object's
// members and names each other member of the file, its stack tags or its
// stack policy, not read.
func TestParamsOtherMembersOfTheObjectAreNotRead(t *testing.T) {
	t.Parallel()
	for file, member := range map[string]string{"tags.json": "Tags", "policy.json": "StackPolicy"} {
		g, diags := readParams(t, file)
		onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
			Message: "the parameters file " + file + " holds " + member + " beside its Parameters, and the import " +
				"reads its Parameters alone, so " + member + " is not carried: set it where the stack is deployed",
			SourceRef: file + "#/" + member})
		want := map[string]cloudimport.Value{"Env": nil, "Expression": nil, "ImageTag": lit(`"1.4.2"`)}
		if got := supplied(g); !reflect.DeepEqual(got, want) {
			t.Errorf("%s supplies %#v, want %#v", file, got, want)
		}
	}
}

// TestParamsUndeclaredParameterIsNotRead names a parameter the file sets and
// the template does not declare not read.
func TestParamsUndeclaredParameterIsNotRead(t *testing.T) {
	t.Parallel()
	g, diags := readParams(t, "undeclared.json")
	onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
		Message: "the parameters file undeclared.json sets the parameter Region, which the template declares no " +
			"parameter of, so it is not read: remove it from the file, or declare it in the template",
		SourceRef: "undeclared.json#/1"})
	want := map[string]cloudimport.Value{"Env": nil, "Expression": nil, "ImageTag": lit(`"1.4.2"`)}
	if got := supplied(g); !reflect.DeepEqual(got, want) {
		t.Errorf("undeclared.json supplies %#v, want %#v", got, want)
	}
}

// TestParamsKeyValueListMustSetADeclaredParameter takes a list of Key=Value
// strings for a parameters file only where one of its keys is a parameter the
// root template declares: an environment list beside a parameters file is
// named not read and leaves that file read, while a list setting a declared
// parameter and one the template does not declare is read, the other named.
func TestParamsKeyValueListMustSetADeclaredParameter(t *testing.T) {
	t.Parallel()
	t.Run("an environment list", func(t *testing.T) {
		t.Parallel()
		g, diags := readAll("", fixtures(t, "params", "env.json", "list.json", "template.yaml")...)
		noDiagnostics(t, diags)
		if want := []string{"list.json", "template.yaml"}; !slices.Equal(g.Read, want) {
			t.Errorf("the read reads %q, want %q", g.Read, want)
		}
		if want := []string{"env.json"}; !slices.Equal(g.NotRead, want) {
			t.Errorf("the read names %q not read, want %q", g.NotRead, want)
		}
		want := map[string]cloudimport.Value{"Env": lit(`"prod"`), "Expression": lit(`"a=b=c"`),
			"ImageTag": lit(`"1.4.2"`)}
		if got := supplied(g); !reflect.DeepEqual(got, want) {
			t.Errorf("list.json supplies %#v, want %#v", got, want)
		}
	})
	t.Run("a list setting a declared parameter", func(t *testing.T) {
		t.Parallel()
		g, diags := readParams(t, "extra.json")
		onlyDiagnostic(t, diags, aggrega.Diagnostic{Severity: aggrega.SeverityWarn, Code: "value-not-carried",
			Message: "the parameters file extra.json sets the parameter Region, which the template declares no " +
				"parameter of, so it is not read: remove it from the file, or declare it in the template",
			SourceRef: "extra.json#/1"})
		want := map[string]cloudimport.Value{"Env": nil, "Expression": nil, "ImageTag": lit(`"1.4.2"`)}
		if got := supplied(g); !reflect.DeepEqual(got, want) {
			t.Errorf("extra.json supplies %#v, want %#v", got, want)
		}
	})
}
