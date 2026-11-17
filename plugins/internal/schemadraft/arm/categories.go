// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package arm

import (
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// namespaceCategories names the category of a type by its resource provider
// namespace, the segment of its name before the first slash, lowercased: each
// Azure service under the category a Component realizing it takes.
var namespaceCategories = map[string]string{
	"microsoft.app":                     "compute",
	"microsoft.web":                     "compute",
	"microsoft.containerinstance":       "compute",
	"microsoft.containerservice":        "compute",
	"microsoft.compute":                 "compute",
	"microsoft.logic":                   "ai-pipeline",
	"microsoft.dbforpostgresql":         "datastore",
	"microsoft.dbformysql":              "datastore",
	"microsoft.sql":                     "datastore",
	"microsoft.documentdb":              "datastore",
	"microsoft.cache":                   "datastore",
	"microsoft.search":                  "datastore",
	"microsoft.servicebus":              "messaging",
	"microsoft.eventhub":                "messaging",
	"microsoft.eventgrid":               "messaging",
	"microsoft.synapse":                 "bigdata",
	"microsoft.databricks":              "bigdata",
	"microsoft.datafactory":             "bigdata",
	"microsoft.kusto":                   "bigdata",
	"microsoft.cognitiveservices":       "ai-model",
	"microsoft.machinelearningservices": "ai-model",
	"microsoft.botservice":              "ai-agent",
	"microsoft.apimanagement":           "gateway",
	"microsoft.storage":                 "storage",
	"microsoft.netapp":                  "storage",
	"microsoft.keyvault":                "security",
	"microsoft.managedidentity":         "security",
	"microsoft.network":                 "network",
	"microsoft.cdn":                     "network",
	"microsoft.insights":                "observability",
	"microsoft.operationalinsights":     "observability",
	"microsoft.containerregistry":       "devtool",
}

// typeCategories names the category of a type, lowercased, whose namespace
// holds types of several categories, ahead of its namespace's.
var typeCategories = map[string]string{
	"microsoft.network/applicationgateways": "gateway",
	"microsoft.network/frontdoors":          "gateway",
}

// category guesses the category of t by its name, read without regard to case
// as Azure Resource Manager reads it: its own entry, or its namespace's; "" for
// a namespace the tables do not name.
func category(t *schemadraft.Type) string {
	name := strings.ToLower(t.Name)
	if c, ok := typeCategories[name]; ok {
		return c
	}
	namespace, _, found := strings.Cut(name, "/")
	if !found {
		return ""
	}
	return namespaceCategories[namespace]
}
