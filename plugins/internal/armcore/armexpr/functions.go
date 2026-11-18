// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

import "strings"

// templateFunctions is the closed set of function names the template function
// reference documents, plus fail, lambda and lambdaVariables, and the zone
// functions the Bicep resource function reference documents. The reference
// pages use lambda and lambdaVariables in their examples without a page of their
// own, and document no fail; they are included because the pinned Azure Verified
// Module template calls them. Bicep compiles each zone function to the template
// function of the same name, so a compiled template calls it. The open
// list{Value} family is matched by IsListFunction.
var templateFunctions = []string{
	"add", "and", "array", "base64", "base64ToJson", "base64ToString", "bool",
	"cidrHost", "cidrSubnet", "coalesce", "concat", "contains", "copyIndex",
	"createArray", "createObject", "dataUri", "dataUriToString", "dateTimeAdd",
	"dateTimeFromEpoch", "dateTimeToEpoch", "deployer", "deployment", "distinct",
	"div", "empty", "endsWith", "environment", "equals", "extensionResourceId",
	"fail", "false", "filter", "first", "float", "format", "greater",
	"greaterOrEquals", "groupBy", "guid", "if", "indexFromEnd", "indexOf", "int",
	"intersection", "items", "join", "json", "last", "lastIndexOf", "length",
	"less", "lessOrEquals", "like", "managementGroup", "managementGroupResourceId",
	"map", "mapValues", "max",
	"min", "mod", "mul", "newGuid", "not", "null", "objectKeys", "or", "padLeft",
	"parameters", "parseCidr", "pickZones", "providers", "range", "reduce",
	"reference", "references", "replace", "resourceGroup", "resourceId",
	"roleDefinitions", "shallowMerge", "skip", "sort", "split", "startsWith",
	"string", "sub", "subscription", "subscriptionResourceId", "substring",
	"take", "tenant", "tenantResourceId", "toLogicalZone", "toLogicalZones",
	"toLower", "toObject", "toPhysicalZone", "toPhysicalZones", "toUpper",
	"trim", "true", "tryGet", "tryIndexFromEnd", "union", "uniqueString", "uri",
	"uriComponent", "uriComponentToString", "utcNow", "variables",
	// Used in the lambda examples of the reference pages.
	"lambda", "lambdaVariables",
}

var templateFunctionIndex = func() map[string]struct{} {
	m := make(map[string]struct{}, len(templateFunctions))
	for _, n := range templateFunctions {
		m[strings.ToLower(n)] = struct{}{}
	}
	return m
}()

// IsTemplateFunction reports whether name is a template function: a member of
// the documented closed list (with fail, lambda and lambdaVariables, which the
// pinned Azure Verified Module template calls, and the zone functions Bicep
// compiles to), or a name of the open list{Value} family (IsListFunction).
// Matching ignores case, as ARM does.
func IsTemplateFunction(name string) bool {
	if IsListFunction(name) {
		return true
	}
	_, ok := templateFunctionIndex[strings.ToLower(name)]
	return ok
}

// IsListFunction reports whether name belongs to the list{Value} family the
// resource function reference documents: the operation name starts with list
// and might have a suffix, so list itself and listKeys, listSecrets,
// listAccountSas, listOutputsWithSecureValues and every other name beginning
// with list. Matching ignores case, as ARM does.
func IsListFunction(name string) bool {
	return len(name) >= len("list") && strings.EqualFold(name[:len("list")], "list")
}
