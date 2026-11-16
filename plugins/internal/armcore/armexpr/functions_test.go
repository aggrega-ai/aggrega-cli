// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

import (
	"sort"
	"strings"
	"testing"
)

func lowerSorted(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = strings.ToLower(n)
	}
	sort.Strings(out)
	return out
}

// TestTemplateFunctionSetIsTheDocumentedList is a deliberate independent
// statement of the same set functions.go holds. The list below was transcribed
// by hand from the template function reference pages (array, CIDR, comparison,
// date, template-value, lambda, logical, numeric, object, resource, scope and string
// functions); keeping it separate means a mistake in either copy shows up as a
// difference rather than agreeing with itself. The list* family is matched by
// prefix and is not enumerated. lambda and lambdaVariables appear only in the
// reference pages' examples and fail is not documented there; all three are
// listed because the pinned module calls them.
func TestTemplateFunctionSetIsTheDocumentedList(t *testing.T) {
	documented := []string{
		// array
		"array", "concat", "contains", "createArray", "distinct", "empty", "first", "indexFromEnd", "indexOf",
		"intersection", "last", "lastIndexOf", "length", "max", "min", "range", "skip", "take", "tryGet",
		"tryIndexFromEnd", "union",
		// CIDR
		"parseCidr", "cidrSubnet", "cidrHost",
		// comparison
		"coalesce", "equals", "greater", "greaterOrEquals", "less", "lessOrEquals",
		// date
		"dateTimeAdd", "dateTimeFromEpoch", "dateTimeToEpoch", "utcNow",
		// template values
		"deployer", "deployment", "environment", "parameters", "variables",
		// lambda
		"filter", "groupBy", "map", "mapValues", "reduce", "sort", "toObject",
		// logical
		"and", "bool", "false", "if", "not", "or", "true",
		// numeric
		"add", "copyIndex", "div", "float", "int", "mod", "mul", "sub",
		// object
		"createObject", "items", "json", "null", "objectKeys", "shallowMerge",
		// resource
		"extensionResourceId", "pickZones", "providers", "reference", "references", "resourceId",
		"roleDefinitions", "subscriptionResourceId", "tenantResourceId",
		// scope
		"managementGroup", "resourceGroup", "subscription", "tenant",
		// string
		"base64", "base64ToJson", "base64ToString", "dataUri", "dataUriToString", "endsWith", "format", "guid",
		"join", "like", "newGuid", "padLeft", "replace", "split", "startsWith", "string", "substring",
		"toLower", "toUpper", "trim", "uniqueString", "uri", "uriComponent", "uriComponentToString",
		// called by the pinned module, not documented as functions of their own
		"fail", "lambda", "lambdaVariables",
	}
	want := dedupe(lowerSorted(documented))
	got := dedupe(lowerSorted(templateFunctions))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("function set differs from the documented list\n got: %v\nwant: %v", got, want)
	}
	for _, n := range want {
		if !IsTemplateFunction(n) || !IsTemplateFunction(strings.ToUpper(n)) {
			t.Errorf("IsTemplateFunction(%q) = false, want true in any case", n)
		}
	}
	for _, n := range []string{"listOutputsWithSecureValues", "LISTKEYS", "listFoo"} {
		if !IsTemplateFunction(n) {
			t.Errorf("IsTemplateFunction(%q) = false, want true (list* family)", n)
		}
	}
	for _, n := range []string{"", "list", "prod", "concatenate", "my"} {
		if IsTemplateFunction(n) {
			t.Errorf("IsTemplateFunction(%q) = true, want false", n)
		}
	}
}

func dedupe(s []string) []string {
	var out []string
	for i, v := range s {
		if i == 0 || v != s[i-1] {
			out = append(out, v)
		}
	}
	return out
}

// TestTemplateFunctionsCoverAVMFlexibleServer holds the function names called by
// the compiled template of avm/res/db-for-postgre-sql/flexible-server at
// release tag avm/res/db-for-postgre-sql/flexible-server/0.15.1 in
// Azure/bicep-registry-modules, transcribed by reading every bracketed string
// of main.json. listOutputsWithSecureValues is included to pin the list{Value}
// family; that release does not call it.
func TestTemplateFunctionsCoverAVMFlexibleServer(t *testing.T) {
	called := []string{
		"and", "coalesce", "contains", "copyIndex", "createArray", "createObject", "deployment", "empty", "equals",
		"fail", "false", "format", "guid", "if", "lambda", "lambdaVariables", "last", "length", "map", "not", "null",
		"or", "parameters", "reduce", "reference", "replace", "resourceGroup", "resourceId", "split", "string",
		"subscriptionResourceId", "substring", "tenant", "true", "tryGet", "union", "uniqueString", "variables",
		"listOutputsWithSecureValues",
	}
	for _, n := range called {
		if !IsTemplateFunction(n) || !IsTemplateFunction(strings.ToUpper(n)) {
			t.Errorf("IsTemplateFunction(%q) = false", n)
		}
	}
}
