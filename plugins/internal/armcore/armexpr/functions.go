// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

import "strings"

// templateFunctions is the closed set of function names the template function
// reference documents, plus fail, which the Azure Verified Modules call. The
// list* family is matched by prefix in IsTemplateFunction.
var templateFunctions = []string{
	"add", "and", "array", "base64", "base64ToJson", "base64ToString", "bool",
	"cidrHost", "cidrSubnet", "coalesce", "concat", "contains", "copyIndex",
	"createArray", "createObject", "dataUri", "dataUriToString", "dateTimeAdd",
	"dateTimeFromEpoch", "dateTimeToEpoch", "deployer", "deployment", "distinct",
	"div", "empty", "endsWith", "environment", "equals", "extensionResourceId",
	"fail", "false", "filter", "first", "float", "format", "greater",
	"greaterOrEquals", "groupBy", "guid", "if", "indexFromEnd", "indexOf", "int",
	"intersection", "items", "join", "json", "last", "lastIndexOf", "length",
	"less", "lessOrEquals", "like", "managementGroup", "map", "mapValues", "max",
	"min", "mod", "mul", "newGuid", "not", "null", "objectKeys", "or", "padLeft",
	"parameters", "parseCidr", "pickZones", "providers", "range", "reduce",
	"reference", "references", "replace", "resourceGroup", "resourceId",
	"roleDefinitions", "shallowMerge", "skip", "sort", "split", "startsWith",
	"string", "sub", "subscription", "subscriptionResourceId", "substring",
	"take", "tenant", "tenantResourceId", "toLower", "toObject", "toUpper",
	"trim", "true", "tryGet", "tryIndexFromEnd", "union", "uniqueString", "uri",
	"uriComponent", "uriComponentToString", "utcNow", "variables",
	// The lambda helpers that pair with the lambda-taking functions.
	"lambda", "lambdaVariables",
}

var templateFunctionIndex = func() map[string]struct{} {
	m := make(map[string]struct{}, len(templateFunctions))
	for _, n := range templateFunctions {
		m[strings.ToLower(n)] = struct{}{}
	}
	return m
}()

// IsTemplateFunction reports whether name is a documented template function.
// Matching ignores case, as ARM does. Every name beginning with "list" (listKeys,
// listSecrets, listAccountSas, listOutputsWithSecureValues, ...) belongs to the
// documented list* family.
func IsTemplateFunction(name string) bool {
	l := strings.ToLower(name)
	if len(l) > len("list") && strings.HasPrefix(l, "list") {
		return true
	}
	_, ok := templateFunctionIndex[l]
	return ok
}
