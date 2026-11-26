// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import "github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"

// propPath is a property or attribute path, its segments given in order.
func propPath(segs ...string) []string { return segs }

// The converters of the hand rows: each maps a value Azure Resource Manager
// spells in a vocabulary of its own to the one the Recipe reads.
const (
	// enabledBool reads a switch written Enabled or Disabled as a boolean.
	enabledBool cloudimport.Conv = "map:Enabled=true,Disabled=false"
	// notBool reads a boolean disabling a setting as one enabling it.
	notBool cloudimport.Conv = "map:true=false,false=true"
	// evictionPolicy reads Azure Managed Redis's name of an eviction policy as
	// the Redis policy it names.
	evictionPolicy cloudimport.Conv = "map:NoEviction=noeviction,AllKeysLRU=allkeys-lru,AllKeysLFU=allkeys-lfu," +
		"AllKeysRandom=allkeys-random,VolatileLRU=volatile-lru,VolatileLFU=volatile-lfu," +
		"VolatileRandom=volatile-random,VolatileTTL=volatile-ttl"
)

// The equivalences of the location and the tags, which a resource and the
// Azure Verified Modules module deploying it write under one name.
var placedAlike = []cloudimport.Equivalence{
	{Raw: propPath("location"), Module: propPath("location")},
	{Raw: propPath("tags"), Module: propPath("tags")},
}

// alike returns placedAlike followed by eqs.
func alike(eqs ...cloudimport.Equivalence) []cloudimport.Equivalence {
	return append(append([]cloudimport.Equivalence(nil), placedAlike...), eqs...)
}

// The labels of the Components whose rows are written here whole, as the hub
// gives them, the category apart.
var (
	serverTraits = map[string]string{"component.aggrega.ai/type": "server",
		"component.aggrega.ai/upstream-licensing": "not-applicable"}
	workerTraits = map[string]string{"component.aggrega.ai/type": "worker",
		"component.aggrega.ai/upstream-licensing": "not-applicable"}
	functionTraits = map[string]string{"component.aggrega.ai/type": "function",
		"component.aggrega.ai/upstream-licensing": "not-applicable"}
	dnsRecordTraits = map[string]string{"component.aggrega.ai/type": "resource",
		"component.aggrega.ai/hosting": "managed", "component.aggrega.ai/upstream-licensing": "proprietary"}
	frontDoorTraits = map[string]string{"component.aggrega.ai/type": "resource",
		"component.aggrega.ai/hosting": "managed", "component.aggrega.ai/upstream-licensing": "proprietary"}
	apiManagementTraits = map[string]string{"component.aggrega.ai/type": "resource",
		"component.aggrega.ai/hosting": "mixed", "component.aggrega.ai/upstream-licensing": "proprietary",
		"gateway.aggrega.ai/intelligence": "semantic", "dataplane.aggrega.ai/period-budget": "tokens"}
)

// containerApp is the row of a container app whose ingress the When given
// holds of: the Component and the Recipe given, its image, its environment
// list, the Container Apps environment it runs in and the registry it pulls
// from, read as the Recipe's ContainerAppParameters entry binds the module's
// parameters, with port its target port where port is set.
func containerApp(when cloudimport.PredOp, component, recipe string, traits map[string]string,
	port bool) cloudimport.Row {
	r := cloudimport.Row{
		Key: "Microsoft.App/containerApps", Component: component, Category: "compute", Recipe: recipe,
		When:   []cloudimport.Pred{{Path: propPath("properties", "configuration", "ingress"), Op: when}},
		Traits: traits, Form: cloudimport.FormRaw,
		Infra: []cloudimport.Placement{
			{From: propPath("properties", "template", "containers", "0", "image"), Key: "image"},
		},
		Overrides: []cloudimport.Placement{
			{From: propPath("properties", "environmentId"), Key: "container_app_environment_id"},
			{From: propPath("properties", "configuration", "registries", "0", "server"), Key: "registry_server"},
			{From: propPath("properties", "configuration", "registries", "0", "identity"), Key: "registry_identity"},
			{From: propPath("properties", "configuration", "registries", "0", "username"), Key: "registry_username"},
		},
		Env: &cloudimport.EnvList{Path: propPath("properties", "template", "containers", "0", "env"), Name: "name",
			Value: "value"},
		Equivalences: []cloudimport.Equivalence{
			{Raw: propPath("properties", "environmentId"), Module: propPath("environmentResourceId")},
			{Raw: propPath("properties", "template", "containers"), Module: propPath("containers")},
			{Raw: propPath("properties", "configuration", "ingress", "targetPort"), Module: propPath("ingressTargetPort")},
			{Raw: propPath("properties", "configuration", "registries"), Module: propPath("registries")},
		},
		Origin: "hand",
	}
	if port {
		r.Infra = append(r.Infra, cloudimport.Placement{From: propPath("properties", "configuration", "ingress", "targetPort"),
			Key: "port"})
	}
	return r
}

// site completes the row r of an App Service site running a container: its
// environment from its app settings, and the plan it runs on, the subnet it
// reaches and its region.
func site(r cloudimport.Row) cloudimport.Row {
	r.Key, r.Category, r.Form = "Microsoft.Web/sites", "compute", cloudimport.FormRaw
	r.Env = &cloudimport.EnvList{Path: propPath("properties", "siteConfig", "appSettings"), Name: "name", Value: "value"}
	r.Overrides = []cloudimport.Placement{
		{From: propPath("properties", "serverFarmId"), Key: "service_plan_id"},
		{From: propPath("properties", "virtualNetworkSubnetId"), Key: "virtual_network_subnet_id"},
		{From: propPath("location"), Key: "location"},
	}
	r.Origin = "hand"
	return r
}

// notWorkflow holds of an App Service site that is no Logic Apps Standard
// app, which the catalog maps to no Component.
var notWorkflow = cloudimport.Pred{Path: propPath("kind"), Op: cloudimport.OpLacks, Value: "workflowapp"}

// flexibleServer is the row merged into the generated row of an Azure
// Database flexible server of the provider namespace, whose administrator's
// password the Recipe takes in the credential slot given and whose module
// names its private DNS zone's parameter dnsZone: its engine version, its
// password, its address, the database folded into values.database, its
// server parameters and firewall rules not carried, and the module's
// parameters named as the resource writes them.
func flexibleServer(namespace, slot, dnsZone string) cloudimport.Row {
	typ := namespace + "/flexibleServers"
	return cloudimport.Row{
		Key: typ, Category: "datastore",
		Infra:    []cloudimport.Placement{{From: propPath("version"), Key: "engine_version", Conv: "string"}},
		Secrets:  []cloudimport.Placement{{From: propPath("administratorLoginPassword"), Key: slot}},
		Provides: []cloudimport.Placement{{From: propPath("outputs", "fqdn", "value"), Key: "host"}},
		Folds: []cloudimport.Fold{
			{Type: typ + "/databases", Key: propPath("values", "database"), From: propPath("name"), One: true},
			notCarried(typ+"/configurations", mapEntries("infra.overrides.server_configuration",
				"the server parameters")),
			notCarried(typ+"/firewallRules", "the Recipe places the server in the operator's virtual network, "+
				"where it takes no firewall rule"),
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("properties", "administratorLogin"),
				Module: propPath("administratorLogin")},
			cloudimport.Equivalence{Raw: propPath("properties", "administratorLoginPassword"),
				Module: propPath("administratorLoginPassword")},
			cloudimport.Equivalence{Raw: propPath("properties", "version"), Module: propPath("version")},
			cloudimport.Equivalence{Raw: propPath("properties", "network", "delegatedSubnetResourceId"),
				Module: propPath("delegatedSubnetResourceId")},
			cloudimport.Equivalence{Raw: propPath("properties", "network", dnsZone), Module: propPath(dnsZone)},
			cloudimport.Equivalence{Raw: propPath("properties", "fullyQualifiedDomainName"),
				Module: propPath("outputs", "fqdn", "value")},
		),
	}
}

// dnsRecord is the row of a record set of the type typ in an Azure DNS zone,
// the zone the operator's.
func dnsRecord(typ string) cloudimport.Row {
	return cloudimport.Row{
		Key: "Microsoft.Network/dnsZones/" + typ, Component: "core/network/dns-record", Category: "network",
		Recipe: "dns-record-azure-dns", Traits: dnsRecordTraits, Form: cloudimport.FormRaw,
		Values:    []cloudimport.Placement{{From: propPath("properties", "TTL"), Key: "ttl", Conv: "integer"}},
		Overrides: []cloudimport.Placement{{From: propPath("properties", "metadata"), Key: "tags"}},
		Origin:    "hand",
	}
}

// dnsRecordTypes are the record types the dns-record Component takes.
var dnsRecordTypes = []string{"A", "AAAA", "CNAME", "MX", "TXT", "SRV", "CAA", "NS", "PTR"}

// notCarried is a fold of a child of typ the Recipe takes none of, and why.
func notCarried(typ, why string) cloudimport.Fold {
	return cloudimport.Fold{Type: typ, NotCarried: why}
}

// mapEntries says why a child the Recipe takes as an entry of a map is not
// folded: no fold writes an entry keyed by a child's name.
func mapEntries(key, what string) string {
	return "the Recipe takes " + what + " under " + key + ", a map from each one's name to its settings, which no " +
		"fold writes an entry of: write an entry for it there"
}

// whyEnabled is the reason a hand placement of a switch written Enabled or
// Disabled supersedes the generated one reading it as written.
const whyEnabled = "Azure writes the switch Enabled or Disabled, and the Recipe reads the key as a boolean"

// handRows are the rows of the mapping table no arm Recipe's text says, each
// keyed by a vendor type and When as a generated row is. A hand row sharing a
// generated row's key and When is merged into it field by field, and a field
// it sets over a different generated value carries the reason under its
// Supersedes. A row whose Recipe the hub's text alone cannot tell apart from
// another's — two Recipes claiming one key and When for different Components
// — is written here whole, and its Recipe then generates no row.
var handRows = append([]cloudimport.Row{
	containerApp(cloudimport.OpPresent, "core/compute/server", "server-container-apps", serverTraits, true),
	containerApp(cloudimport.OpAbsent, "core/compute/worker", "worker-container-apps", workerTraits, false),
	{
		Key: "Microsoft.App/jobs", Category: "compute",
		Values: []cloudimport.Placement{{From: propPath("scheduleTriggerConfig", "cronExpression"), Key: "schedule"}},
		Infra:  []cloudimport.Placement{{From: propPath("containers", "0", "image"), Key: "image"}},
		Env:    &cloudimport.EnvList{Path: propPath("containers", "0", "env"), Name: "name", Value: "value"},
		Equivalences: []cloudimport.Equivalence{
			{Raw: propPath("properties", "environmentId"), Module: propPath("environmentResourceId")},
			{Raw: propPath("properties", "template", "containers"), Module: propPath("containers")},
			{Raw: propPath("properties", "configuration", "scheduleTriggerConfig"), Module: propPath("scheduleTriggerConfig")},
			{Raw: propPath("properties", "configuration", "replicaTimeout"), Module: propPath("replicaTimeout")},
			{Raw: propPath("properties", "configuration", "registries"), Module: propPath("registries")},
		},
	},
	site(cloudimport.Row{Component: "core/compute/function", Recipe: "function-azure-functions",
		When: []cloudimport.Pred{{Path: propPath("kind"), Op: cloudimport.OpContains, Value: "functionapp"},
			notWorkflow}, Traits: functionTraits}),
	site(cloudimport.Row{Component: "core/compute/server", Recipe: "server-app-service", Traits: serverTraits,
		When: []cloudimport.Pred{notWorkflow}}),
	flexibleServer("Microsoft.DBforPostgreSQL", "password", "privateDnsZoneArmResourceId"),
	flexibleServer("Microsoft.DBforMySQL", "root_password", "privateDnsZoneResourceId"),
	{
		Key: "Microsoft.Cache/redisEnterprise", Category: "datastore",
		Values: []cloudimport.Placement{
			{From: propPath("database", "evictionPolicy"), Key: "maxmemory_policy", Conv: evictionPolicy},
		},
		Overrides: []cloudimport.Placement{
			{From: propPath("highAvailability"), Key: "high_availability_enabled", Conv: enabledBool},
			{From: propPath("database", "accessKeysAuthentication"),
				Key: "default_database_access_keys_authentication_enabled", Conv: enabledBool},
		},
		Provides: []cloudimport.Placement{{From: propPath("outputs", "hostName", "value"), Key: "host"}},
		Folds: []cloudimport.Fold{
			notCarried("Microsoft.Cache/redisEnterprise/databases", "the Recipe creates the instance's one "+
				"database itself, its eviction policy from values.maxmemory_policy, its persistence from "+
				"values.appendonly and values.persistence, and its protocol from infra.client_tls: set the "+
				"database's settings there"),
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("sku", "name"), Module: propPath("skuName")},
			cloudimport.Equivalence{Raw: propPath("properties", "highAvailability"), Module: propPath("highAvailability")},
			cloudimport.Equivalence{Raw: propPath("properties", "publicNetworkAccess"), Module: propPath("publicNetworkAccess")},
			cloudimport.Equivalence{Raw: propPath("properties", "hostName"), Module: propPath("outputs", "hostName", "value")},
		),
		Supersedes: map[string]string{
			"Overrides.high_availability_enabled":                           whyEnabled,
			"Overrides.default_database_access_keys_authentication_enabled": whyEnabled,
		},
	},
	{
		Key: "Microsoft.DocumentDB/databaseAccounts", Category: "datastore",
		Values: []cloudimport.Placement{{From: propPath("sqlDatabases", "0", "name"), Key: "database"}},
		Overrides: []cloudimport.Placement{
			{From: propPath("networkRestrictions", "publicNetworkAccess"), Key: "public_network_access_enabled",
				Conv: enabledBool},
		},
		Provides: []cloudimport.Placement{{From: propPath("outputs", "endpoint", "value"), Key: "endpoint"}},
		Folds: []cloudimport.Fold{
			{Type: "Microsoft.DocumentDB/databaseAccounts/sqlDatabases", Key: propPath("values", "database"),
				From: propPath("name"), One: true},
			notCarried("Microsoft.DocumentDB/databaseAccounts/sqlDatabases/containers", mapEntries(
				"values.containers", "the database's containers")),
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("properties", "consistencyPolicy", "defaultConsistencyLevel"),
				Module: propPath("defaultConsistencyLevel")},
			cloudimport.Equivalence{Raw: propPath("properties", "publicNetworkAccess"),
				Module: propPath("networkRestrictions", "publicNetworkAccess")},
			cloudimport.Equivalence{Raw: propPath("properties", "documentEndpoint"),
				Module: propPath("outputs", "endpoint", "value")},
		),
	},
	{
		Key: "Microsoft.Sql/servers", Category: "datastore",
		Overrides: []cloudimport.Placement{
			{From: propPath("publicNetworkAccess"), Key: "public_network_access_enabled", Conv: enabledBool},
		},
		Secrets:  []cloudimport.Placement{{From: propPath("administratorLoginPassword"), Key: "password"}},
		Provides: []cloudimport.Placement{{From: propPath("outputs", "fullyQualifiedDomainName", "value"), Key: "host"}},
		Folds: []cloudimport.Fold{
			{Type: "Microsoft.Sql/servers/databases", Key: propPath("values", "database"), From: propPath("name"),
				Instances: true},
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("properties", "administratorLogin"), Module: propPath("administratorLogin")},
			cloudimport.Equivalence{Raw: propPath("properties", "administratorLoginPassword"),
				Module: propPath("administratorLoginPassword")},
			cloudimport.Equivalence{Raw: propPath("sku", "name"), Module: propPath("databases", "0", "sku", "name")},
			cloudimport.Equivalence{Raw: propPath("properties", "publicNetworkAccess"), Module: propPath("publicNetworkAccess")},
			cloudimport.Equivalence{Raw: propPath("properties", "fullyQualifiedDomainName"),
				Module: propPath("outputs", "fullyQualifiedDomainName", "value")},
		),
	},
	{
		Key: "Microsoft.Sql/managedInstances", Category: "datastore",
		Values: []cloudimport.Placement{{From: propPath("properties", "administratorLogin"), Key: "user"}},
		Overrides: []cloudimport.Placement{
			{From: propPath("properties", "subnetId"), Key: "delegated_subnet_id"},
			{From: propPath("sku", "name"), Key: "sku_name"},
			{From: propPath("properties", "vCores"), Key: "vcores"},
			{From: propPath("properties", "licenseType"), Key: "license_type"},
			{From: propPath("location"), Key: "location"},
			{From: propPath("tags"), Key: "tags"},
		},
		Secrets:  []cloudimport.Placement{{From: propPath("properties", "administratorLoginPassword"), Key: "password"}},
		Provides: []cloudimport.Placement{{From: propPath("properties", "fullyQualifiedDomainName"), Key: "host"}},
		Folds: []cloudimport.Fold{
			{Type: "Microsoft.Sql/managedInstances/databases", Key: propPath("values", "database"), From: propPath("name"),
				One: true},
		},
	},
	{
		Key: "Microsoft.DocumentDB/mongoClusters", Category: "datastore",
		Values: []cloudimport.Placement{{From: propPath("properties", "administrator", "userName"), Key: "user"}},
		Overrides: []cloudimport.Placement{
			{From: propPath("properties", "compute", "tier"), Key: "sku_name"},
			{From: propPath("properties", "sharding", "shardCount"), Key: "shard_count"},
			{From: propPath("properties", "publicNetworkAccess"), Key: "public_network_access"},
			{From: propPath("location"), Key: "location"},
			{From: propPath("tags"), Key: "tags"},
		},
		Secrets: []cloudimport.Placement{{From: propPath("properties", "administrator", "password"), Key: "password"}},
	},
	{
		Key: "Microsoft.Search/searchServices", Category: "datastore",
		Overrides: []cloudimport.Placement{
			{From: propPath("publicNetworkAccess"), Key: "public_network_access_enabled", Conv: enabledBool},
		},
		Provides: []cloudimport.Placement{{From: propPath("outputs", "endpoint", "value"), Key: "endpoint"}},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("sku", "name"), Module: propPath("sku")},
			cloudimport.Equivalence{Raw: propPath("properties", "replicaCount"), Module: propPath("replicaCount")},
			cloudimport.Equivalence{Raw: propPath("properties", "partitionCount"), Module: propPath("partitionCount")},
			cloudimport.Equivalence{Raw: propPath("properties", "semanticSearch"), Module: propPath("semanticSearch")},
			cloudimport.Equivalence{Raw: propPath("properties", "publicNetworkAccess"), Module: propPath("publicNetworkAccess")},
		),
	},
	{
		Key: "Microsoft.Storage/storageAccounts", Category: "storage",
		When:   []cloudimport.Pred{{Path: propPath("kind"), Op: cloudimport.OpEquals, Value: "StorageV2"}},
		Values: []cloudimport.Placement{{From: propPath("properties", "isHnsEnabled"), Key: "is_hns_enabled"}},
		Overrides: []cloudimport.Placement{
			{From: propPath("sku", "name"), Key: "sku_name"},
			{From: propPath("properties", "accessTier"), Key: "access_tier"},
			{From: propPath("properties", "publicNetworkAccess"), Key: "public_network_access"},
			{From: propPath("location"), Key: "location"},
			{From: propPath("tags"), Key: "tags"},
		},
		Provides: []cloudimport.Placement{
			{From: propPath("properties", "primaryEndpoints", "blob"), Key: "endpoint"},
			{From: propPath("properties", "primaryEndpoints", "dfs"), Key: "dfs_endpoint"},
		},
		Folds: []cloudimport.Fold{
			notCarried("Microsoft.Storage/storageAccounts/blobServices", "the Recipe writes the account's blob "+
				"service itself, with the retention it sets"),
			notCarried("Microsoft.Storage/storageAccounts/blobServices/containers", mapEntries(
				"values.containers", "the account's blob containers")),
		},
	},
	{
		Key: "Microsoft.ServiceBus/namespaces", Category: "messaging",
		Overrides: []cloudimport.Placement{
			{From: propPath("skuObject", "name"), Key: "sku_name"},
			{From: propPath("skuObject", "capacity"), Key: "sku_capacity"},
			{From: propPath("publicNetworkAccess"), Key: "public_network_access_enabled", Conv: enabledBool},
		},
		Provides: []cloudimport.Placement{{From: propPath("outputs", "serviceBusEndpoint", "value"), Key: "endpoint"}},
		Folds: []cloudimport.Fold{
			notCarried("Microsoft.ServiceBus/namespaces/queues", mapEntries("values.queues", "the namespace's queues")),
			notCarried("Microsoft.ServiceBus/namespaces/topics", mapEntries("values.topics", "the namespace's topics")),
			notCarried("Microsoft.ServiceBus/namespaces/authorizationRules", "the Recipe writes no shared access "+
				"rule: a client reaches the namespace with an identity granted access outside the draft"),
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("sku", "name"), Module: propPath("skuObject", "name")},
			cloudimport.Equivalence{Raw: propPath("sku", "capacity"), Module: propPath("skuObject", "capacity")},
			cloudimport.Equivalence{Raw: propPath("properties", "publicNetworkAccess"), Module: propPath("publicNetworkAccess")},
			cloudimport.Equivalence{Raw: propPath("properties", "serviceBusEndpoint"),
				Module: propPath("outputs", "serviceBusEndpoint", "value")},
		),
	},
	{
		Key: "Microsoft.EventHub/namespaces", Category: "messaging",
		Overrides: []cloudimport.Placement{
			{From: propPath("sku", "name"), Key: "sku_name"},
			{From: propPath("sku", "capacity"), Key: "sku_capacity"},
			{From: propPath("properties", "publicNetworkAccess"), Key: "public_network_access_enabled", Conv: enabledBool},
			{From: propPath("location"), Key: "location"},
			{From: propPath("tags"), Key: "tags"},
		},
		Provides: []cloudimport.Placement{{From: propPath("properties", "serviceBusEndpoint"), Key: "endpoint"}},
		Folds: []cloudimport.Fold{
			notCarried("Microsoft.EventHub/namespaces/eventhubs", mapEntries("values.eventhubs",
				"the namespace's event hubs")),
			notCarried("Microsoft.EventHub/namespaces/eventhubs/consumergroups", mapEntries("values.eventhubs",
				"each event hub's consumer groups, inside its entry,")),
			notCarried("Microsoft.EventHub/namespaces/authorizationRules", "the Recipe writes no shared access "+
				"rule: a client reaches the namespace with an identity granted access outside the draft"),
		},
	},
	{
		Key: "Microsoft.EventGrid/topics", Category: "messaging",
		Values: []cloudimport.Placement{{From: propPath("inputSchema"), Key: "input_schema"}},
		Overrides: []cloudimport.Placement{
			{From: propPath("publicNetworkAccess"), Key: "public_network_access_enabled", Conv: enabledBool},
		},
		Provides: []cloudimport.Placement{{From: propPath("outputs", "endpoint", "value"), Key: "endpoint"}},
		Folds: []cloudimport.Fold{
			notCarried("Microsoft.EventGrid/topics/eventSubscriptions", "the Recipe writes the topic alone, and a "+
				"subscription to it is its consumer's to declare"),
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("properties", "inputSchema"), Module: propPath("inputSchema")},
			cloudimport.Equivalence{Raw: propPath("properties", "publicNetworkAccess"), Module: propPath("publicNetworkAccess")},
			cloudimport.Equivalence{Raw: propPath("properties", "endpoint"), Module: propPath("outputs", "endpoint", "value")},
		),
	},
	{
		Key: "Microsoft.KeyVault/vaults", Category: "security",
		Overrides: []cloudimport.Placement{
			{From: propPath("publicNetworkAccess"), Key: "public_network_access_enabled", Conv: enabledBool},
		},
		Provides: []cloudimport.Placement{{From: propPath("outputs", "uri", "value"), Key: "endpoint"}},
		Folds: []cloudimport.Fold{
			notCarried("Microsoft.KeyVault/vaults/secrets", "a secret is attached to the component reading it "+
				"by reference, and the draft never carries its value"),
			notCarried("Microsoft.KeyVault/vaults/keys", "the Recipe writes the vault alone, and a key in it "+
				"is created outside the draft"),
			notCarried("Microsoft.KeyVault/vaults/accessPolicies", "the Recipe grants access to the vault by "+
				"role, which is the operator's"),
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("properties", "sku", "name"), Module: propPath("sku")},
			cloudimport.Equivalence{Raw: propPath("properties", "softDeleteRetentionInDays"),
				Module: propPath("softDeleteRetentionInDays")},
			cloudimport.Equivalence{Raw: propPath("properties", "enablePurgeProtection"),
				Module: propPath("enablePurgeProtection")},
			cloudimport.Equivalence{Raw: propPath("properties", "publicNetworkAccess"), Module: propPath("publicNetworkAccess")},
			cloudimport.Equivalence{Raw: propPath("properties", "vaultUri"), Module: propPath("outputs", "uri", "value")},
		),
	},
	{
		Key: "Microsoft.Cdn/profiles", Component: "core/network/azure-front-door", Category: "network",
		Recipe: "azure-front-door", Traits: frontDoorTraits, Form: cloudimport.FormRaw, Origin: "hand",
		When: []cloudimport.Pred{{Path: propPath("sku", "name"), Op: cloudimport.OpContains, Value: "AzureFrontDoor"}},
		Overrides: []cloudimport.Placement{
			{From: propPath("sku", "name"), Key: "sku_name"},
			{From: propPath("tags"), Key: "tags"},
		},
		Folds: []cloudimport.Fold{
			{Type: "Microsoft.Cdn/profiles/securityPolicies", Key: propPath("infra", "waf_policy_id"),
				From: propPath("properties", "parameters", "wafPolicy", "id"), One: true},
			notCarried("Microsoft.Cdn/profiles/afdEndpoints", "the Recipe creates the profile's one endpoint "+
				"itself"),
			notCarried("Microsoft.Cdn/profiles/afdEndpoints/routes", "the Recipe creates the endpoint's one "+
				"route itself, to the origin the instance's connection names"),
			notCarried("Microsoft.Cdn/profiles/originGroups", "the Recipe creates the profile's one origin "+
				"group itself"),
			notCarried("Microsoft.Cdn/profiles/originGroups/origins", "the Recipe creates the origin itself, "+
				"from the server the instance's connection names"),
			notCarried("Microsoft.Cdn/profiles/customDomains", "the Recipe serves the endpoint's own host name "+
				"alone"),
			notCarried("Microsoft.Cdn/profiles/ruleSets", "the Recipe writes no rule set"),
		},
	},
	{
		Key: "Microsoft.Insights/components", Category: "observability",
		Overrides: []cloudimport.Placement{
			{From: propPath("applicationType"), Key: "application_type"},
			{From: propPath("disableLocalAuth"), Key: "local_authentication_enabled", Conv: notBool},
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("properties", "WorkspaceResourceId"), Module: propPath("workspaceResourceId")},
			cloudimport.Equivalence{Raw: propPath("properties", "SamplingPercentage"), Module: propPath("samplingPercentage")},
			cloudimport.Equivalence{Raw: propPath("properties", "Application_Type"), Module: propPath("applicationType")},
			cloudimport.Equivalence{Raw: propPath("properties", "DisableLocalAuth"), Module: propPath("disableLocalAuth")},
			cloudimport.Equivalence{Raw: propPath("properties", "ConnectionString"),
				Module: propPath("outputs", "connectionString", "value")},
		),
	},
	{
		Key: "Microsoft.Databricks/workspaces", Category: "bigdata",
		Overrides: []cloudimport.Placement{
			{From: propPath("skuName"), Key: "sku_name"},
			{From: propPath("publicNetworkAccess"), Key: "public_network_access_enabled", Conv: enabledBool},
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("sku", "name"), Module: propPath("skuName")},
			cloudimport.Equivalence{Raw: propPath("properties", "parameters", "customPrivateSubnetName", "value"),
				Module: propPath("customPrivateSubnetName")},
			cloudimport.Equivalence{Raw: propPath("properties", "parameters", "customPublicSubnetName", "value"),
				Module: propPath("customPublicSubnetName")},
			cloudimport.Equivalence{Raw: propPath("properties", "publicNetworkAccess"), Module: propPath("publicNetworkAccess")},
			cloudimport.Equivalence{Raw: propPath("properties", "workspaceUrl"),
				Module: propPath("outputs", "workspaceUrl", "value")},
		),
	},
	{
		Key: "Microsoft.DataFactory/factories", Category: "bigdata",
		Overrides: []cloudimport.Placement{
			{From: propPath("properties", "publicNetworkAccess"), Key: "public_network_access_enabled", Conv: enabledBool},
			{From: propPath("location"), Key: "location"},
			{From: propPath("tags"), Key: "tags"},
		},
		Folds: []cloudimport.Fold{
			notCarried("Microsoft.DataFactory/factories/pipelines", mapEntries("values.pipelines",
				"the factory's pipelines")),
			notCarried("Microsoft.DataFactory/factories/linkedservices", "the Recipe writes no linked service: "+
				"a pipeline's connection to a store is written in its entry under values.pipelines"),
			notCarried("Microsoft.DataFactory/factories/datasets", "the Recipe writes no dataset: a "+
				"pipeline's data is written in its entry under values.pipelines"),
		},
	},
	{
		Key: "Microsoft.Logic/workflows", Category: "ai-pipeline",
		Values:       []cloudimport.Placement{{From: propPath("definition"), Key: "definition"}},
		Equivalences: alike(cloudimport.Equivalence{Raw: propPath("properties", "definition"), Module: propPath("definition")}),
	},
	{
		Key: "Microsoft.ContainerRegistry/registries", Category: "devtool",
		Overrides: []cloudimport.Placement{
			{From: propPath("publicNetworkAccess"), Key: "public_network_access_enabled", Conv: enabledBool},
		},
		Equivalences: alike(
			cloudimport.Equivalence{Raw: propPath("sku", "name"), Module: propPath("acrSku")},
			cloudimport.Equivalence{Raw: propPath("properties", "publicNetworkAccess"), Module: propPath("publicNetworkAccess")},
			cloudimport.Equivalence{Raw: propPath("properties", "loginServer"),
				Module: propPath("outputs", "loginServer", "value")},
		),
	},
	{
		Key: "Microsoft.ApiManagement/service/apis", Component: "core/gateway/azure-api-management",
		Category: "gateway", Recipe: "azure-api-management", Traits: apiManagementTraits, Form: cloudimport.FormRaw,
		Infra: []cloudimport.Placement{
			{From: propPath("properties", "path"), Key: "api_path"},
			{From: propPath("properties", "serviceUrl"), Key: "backend_url"},
			{From: propPath("properties", "subscriptionRequired"), Key: "subscription_required"},
		},
		Folds: []cloudimport.Fold{
			notCarried("Microsoft.ApiManagement/service/apis/operations", "the Recipe writes the API's one "+
				"operation itself"),
			notCarried("Microsoft.ApiManagement/service/apis/policies", "the Recipe writes the API's policy "+
				"itself, from the instance's infra and connections"),
		},
		Origin: "hand",
	},
}, dnsRecords()...)

// dnsRecords are the rows of the record sets of each type the dns-record
// Component takes.
func dnsRecords() []cloudimport.Row {
	out := make([]cloudimport.Row, len(dnsRecordTypes))
	for i, typ := range dnsRecordTypes {
		out[i] = dnsRecord(typ)
	}
	return out
}

// notImported are the hub's arm Recipes and FeatureRecipes no row reaches,
// each with the reason. A Recipe listed here generates no row.
var notImported = []cloudimport.NotImported{
	{Recipe: "audit-logging-azure-api-management", Reason: "a FeatureRecipe, binding core/audit-logging on the " +
		"azure-api-management Recipe's API to a halt: it emits nothing, so no resource of a template is an " +
		"instance of it"},
	{Recipe: "azure-managed", Reason: "a reference realization: it writes no resource and publishes the endpoint " +
		"infra.endpoint names, so no resource of a template is an instance of it"},
	{Recipe: "azure-openai", Reason: "the model each deployment of an Azure OpenAI account serves chooses " +
		"between this Recipe and azure-openai-embedding, and the table keys a row by the account, which serves " +
		"both kinds of model, so no row maps an account to either and an account is drafted from the source"},
	{Recipe: "azure-openai-embedding", Reason: "the model each deployment of an Azure OpenAI account serves " +
		"chooses between this Recipe and azure-openai, and the table keys a row by the account, which serves " +
		"both kinds of model, so no row maps an account to either and an account is drafted from the source"},
	{Recipe: "foundry-agent-service", Reason: "a reference realization: it writes no resource and publishes the " +
		"agent the endpoint infra.endpoint names serves, so no resource of a template is an instance of it"},
	{Recipe: "key-vault-secret", Reason: "a FeatureRecipe, realizing core/referenced-secret on a Key Vault " +
		"secret: the importer attaches a Feature for a secret a template reads, and maps no resource to it"},
	{Recipe: "manual-trigger", Reason: "a FeatureRecipe, realizing core/manual-trigger: it emits nothing, and the " +
		"Recipe realizing the task deploys a Container Apps job with a manual trigger, so no resource of a " +
		"template is an instance of it"},
	{Recipe: "period-budget-azure-api-management", Reason: "a FeatureRecipe, realizing core/period-budget as an " +
		"element of the API Management API policy the azure-api-management Recipe writes: no deployed " +
		"resource is its own, so no resource of a template is an instance of it"},
	{Recipe: "prompt-injection-shield-azure-api-management", Reason: "a FeatureRecipe, realizing " +
		"core/prompt-injection-shield as an element of the API Management API policy the azure-api-management " +
		"Recipe writes: no deployed resource is its own, so no resource of a template is an instance of it"},
	{Recipe: "public-ingress-native", Reason: "a FeatureRecipe, realizing core/public-ingress: it emits nothing, " +
		"and the Recipe realizing the server publishes it, so no resource of a template is an instance of it"},
	{Recipe: "retry-policy-azure-api-management", Reason: "a FeatureRecipe, realizing core/retry-policy as an " +
		"element of the API Management API policy the azure-api-management Recipe writes: no deployed " +
		"resource is its own, so no resource of a template is an instance of it"},
	{Recipe: "semantic-caching-azure-api-management", Reason: "a FeatureRecipe, realizing core/semantic-caching " +
		"as an element of the API Management API policy the azure-api-management Recipe writes: no deployed " +
		"resource is its own, so no resource of a template is an instance of it"},
}

// Table returns the arm format's mapping table: the rows generated from the
// hub's arm Recipes at TableRevision, the hand rows, and the Recipes and
// FeatureRecipes no row reaches. Its rows are read and never changed.
func Table() cloudimport.Table {
	return cloudimport.Table{Revision: TableRevision, Generated: generatedRows, Hand: handRows, Images: generatedImages,
		NotImported: notImported}
}
