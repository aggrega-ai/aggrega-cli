// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import "github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"

// What the decision on a resource of each group of types says beside its
// class.
const (
	networkSays = "the network a component runs in is the operator's: a component of the catalog takes it by " +
		"its resource ID under infra.overrides"
	identitySays = "identity and access are the operator's: a component of the catalog takes an identity by its " +
		"resource ID, and the operator grants it its roles"
	groupSays = "the resource group a template deploys into is the operator's, and the Release deploys into the " +
		"one it is given"
	workspaceSays = "a workspace collecting logs and metrics is the operator's: a component of the catalog sends " +
		"to one by its resource ID"
	foundrySays = "a Foundry project or a machine learning workspace is the operator's, and the models and agents " +
		"a component of the catalog reads are served from the one it names"
	automationSays = "an Automation account runs the operator's runbooks, which no component of the catalog holds"
	filesSays      = "an Azure Files share is storage the operator mounts into a Container Apps environment, which " +
		"no component of the catalog holds"
	environmentSays = "the Container Apps environment an app runs in is the operator's: a Container Apps Recipe " +
		"takes it by its resource ID under infra.overrides"
	planSays = "the App Service plan a site runs on is the operator's: an App Service Recipe takes it by its " +
		"resource ID under infra.overrides"
	hostSays = "a cluster, a virtual machine or a scale set is a host the components run on, not a component " +
		"of an Aggregate"
	imageSays   = "a virtual machine image and the gallery holding it serve the hosts the operator runs"
	desktopSays = "Azure Virtual Desktop serves desktops to people, and runs on hosts the operator provides"
	lockSays    = "no Feature of the catalog locks a resource, so a lock is applied to the deployed resource " +
		"outside the draft"
	diagnosticsSays = "no Feature of the catalog sends a resource's logs and metrics; the diagnostic settings name " +
		"the destinations they send to"
	apiManagementSays = "the API Management service is the operator's: the azure-api-management Recipe adds an " +
		"API to the service infra.api_management_name names"
	apiSays = "infra.api_management_name names the API Management service the API is added to, which the " +
		"template names as the API's parent: write that service's name there"
	dnsRecordSays = "the record's fully qualified name, its type and its data are not placed, since the template " +
		"writes them as the record set's name within its zone, its resource type and objects of the type's " +
		"fields: write values.name, values.type and values.records, and the zone's name under " +
		"infra.overrides.zone_name"
	scriptSays = "the script runs when Azure Resource Manager deploys the resource, as the template has it, so " +
		"its drafted Recipe runs it when the Release deploys it"
)

// rules are the type rules of single types, typeRules the arm format's.
var rules = []cloudimport.TypeRule{
	{Type: "Microsoft.Network/virtualNetworks", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/virtualNetworks/subnets", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/virtualNetworks/virtualNetworkPeerings", Class: cloudimport.ClassSubstrate,
		Says: networkSays},
	{Type: "Microsoft.Network/natGateways", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/routeTables", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/routeTables/routes", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/azureFirewalls", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/firewallPolicies", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/firewallPolicies/ruleCollectionGroups", Class: cloudimport.ClassSubstrate,
		Says: networkSays},
	{Type: "Microsoft.Network/networkSecurityGroups", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/networkSecurityGroups/securityRules", Class: cloudimport.ClassSubstrate,
		Says: networkSays},
	{Type: "Microsoft.Network/privateEndpoints", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/privateEndpoints/privateDnsZoneGroups", Class: cloudimport.ClassSubstrate,
		Says: networkSays},
	{Type: "Microsoft.Network/privateDnsZones", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/privateDnsZones/virtualNetworkLinks", Class: cloudimport.ClassSubstrate,
		Says: networkSays},
	{Type: "Microsoft.Network/dnsZones", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/dnsZones/SOA", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/publicIPAddresses", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/publicIPPrefixes", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/applicationGateways", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/ApplicationGatewayWebApplicationFirewallPolicies", Class: cloudimport.ClassSubstrate,
		Says: networkSays},
	{Type: "Microsoft.Network/FrontDoorWebApplicationFirewallPolicies", Class: cloudimport.ClassSubstrate,
		Says: networkSays},
	{Type: "Microsoft.Network/loadBalancers", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/networkInterfaces", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/bastionHosts", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.Network/virtualNetworkGateways", Class: cloudimport.ClassSubstrate, Says: networkSays},
	{Type: "Microsoft.ManagedIdentity/userAssignedIdentities", Class: cloudimport.ClassSubstrate, Says: identitySays},
	{Type: "Microsoft.ManagedIdentity/userAssignedIdentities/federatedIdentityCredentials",
		Class: cloudimport.ClassSubstrate, Says: identitySays},
	{Type: "Microsoft.Authorization/roleDefinitions", Class: cloudimport.ClassSubstrate, Says: identitySays},
	{Type: "Microsoft.Authorization/roleAssignments", Class: cloudimport.ClassSubstrate, Says: identitySays},
	{Type: "Microsoft.Resources/resourceGroups", Class: cloudimport.ClassSubstrate, Says: groupSays},
	{Type: "Microsoft.OperationalInsights/workspaces", Class: cloudimport.ClassSubstrate, Says: workspaceSays},
	{Type: "Microsoft.CognitiveServices/accounts/projects", Class: cloudimport.ClassSubstrate, Says: foundrySays},
	{Type: "Microsoft.MachineLearningServices/workspaces", Class: cloudimport.ClassSubstrate, Says: foundrySays},
	{Type: "Microsoft.Automation/automationAccounts", Class: cloudimport.ClassSubstrate, Says: automationSays},
	{Type: "Microsoft.Storage/storageAccounts/fileServices", Class: cloudimport.ClassSubstrate, Says: filesSays},
	{Type: "Microsoft.Storage/storageAccounts/fileServices/shares", Class: cloudimport.ClassSubstrate,
		Says: filesSays},
	{Type: "Microsoft.App/managedEnvironments", Class: cloudimport.ClassSubstrate, Says: environmentSays},
	{Type: "Microsoft.App/managedEnvironments/storages", Class: cloudimport.ClassSubstrate, Says: filesSays},
	{Type: "Microsoft.App/managedEnvironments/certificates", Class: cloudimport.ClassSubstrate,
		Says: environmentSays},
	{Type: "Microsoft.App/managedEnvironments/managedCertificates", Class: cloudimport.ClassSubstrate,
		Says: environmentSays},
	{Type: "Microsoft.App/managedEnvironments/daprComponents", Class: cloudimport.ClassSubstrate,
		Says: environmentSays},
	{Type: "Microsoft.Web/serverfarms", Class: cloudimport.ClassSubstrate, Says: planSays},
	{Type: "Microsoft.ContainerService/managedClusters", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "Microsoft.ContainerService/managedClusters/agentPools", Class: cloudimport.ClassSubstrate,
		Says: hostSays},
	{Type: "Microsoft.RedHatOpenShift/openShiftClusters", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "Microsoft.Kubernetes/connectedClusters", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "Microsoft.Compute/virtualMachines", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "Microsoft.Compute/virtualMachines/extensions", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "Microsoft.Compute/virtualMachineScaleSets", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "Microsoft.Compute/virtualMachineScaleSets/extensions", Class: cloudimport.ClassSubstrate,
		Says: hostSays},
	{Type: "Microsoft.Compute/availabilitySets", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "Microsoft.Compute/disks", Class: cloudimport.ClassSubstrate, Says: hostSays},
	{Type: "Microsoft.Compute/images", Class: cloudimport.ClassSubstrate, Says: imageSays},
	{Type: "Microsoft.Compute/galleries", Class: cloudimport.ClassSubstrate, Says: imageSays},
	{Type: "Microsoft.Compute/galleries/images", Class: cloudimport.ClassSubstrate, Says: imageSays},
	{Type: "Microsoft.Compute/galleries/images/versions", Class: cloudimport.ClassSubstrate, Says: imageSays},
	{Type: "Microsoft.VirtualMachineImages/imageTemplates", Class: cloudimport.ClassSubstrate, Says: imageSays},
	{Type: "Microsoft.DesktopVirtualization/hostPools", Class: cloudimport.ClassSubstrate, Says: desktopSays},
	{Type: "Microsoft.DesktopVirtualization/applicationGroups", Class: cloudimport.ClassSubstrate,
		Says: desktopSays},
	{Type: "Microsoft.DesktopVirtualization/workspaces", Class: cloudimport.ClassSubstrate, Says: desktopSays},
	{Type: "Microsoft.DesktopVirtualization/scalingPlans", Class: cloudimport.ClassSubstrate, Says: desktopSays},
	{Type: "Microsoft.ApiManagement/service", Class: cloudimport.ClassSubstrate, Says: apiManagementSays},
	{Type: "Microsoft.Authorization/locks", Class: cloudimport.ClassNotCarried, Says: lockSays},
	{Type: "Microsoft.Insights/diagnosticSettings", Class: cloudimport.ClassNotCarried, Says: diagnosticsSays},
	{Type: "Microsoft.Resources/deploymentScripts", Class: cloudimport.ClassDrafted, Says: scriptSays},
	{Type: "Microsoft.ApiManagement/service/apis", Class: cloudimport.ClassDrafted, Says: apiSays},
}

// typeRules are the arm format's type rules: the substrate a template may
// create, which the draft leaves to the operator — the networks, identities,
// resource groups, workspaces and environments a component runs in, the record
// sets of a private DNS zone and a public zone's start of authority, the hosts
// a component runs on, and the API Management service an API is added to —
// the resources no Recipe carries, and what the decision on a resource of a
// type says beside its class: a deployment script, an API added to a service,
// and a DNS record set.
var typeRules = append(append(append([]cloudimport.TypeRule(nil), rules...), privateDNSRecordRules()...),
	dnsRecordRules()...)

// privateDNSRecordTypes are the record types of an Azure Private DNS zone.
var privateDNSRecordTypes = []string{"A", "AAAA", "CNAME", "MX", "PTR", "SRV", "TXT", "SOA"}

// privateDNSRecordRules leave each record set of a private DNS zone to the
// operator, as the zone is.
func privateDNSRecordRules() []cloudimport.TypeRule {
	out := make([]cloudimport.TypeRule, len(privateDNSRecordTypes))
	for i, typ := range privateDNSRecordTypes {
		out[i] = cloudimport.TypeRule{Type: "Microsoft.Network/privateDnsZones/" + typ,
			Class: cloudimport.ClassSubstrate, Says: networkSays}
	}
	return out
}

// dnsRecordRules say, of a DNS record set of each type the dns-record
// Component takes, what its row does not place.
func dnsRecordRules() []cloudimport.TypeRule {
	out := make([]cloudimport.TypeRule, len(dnsRecordTypes))
	for i, typ := range dnsRecordTypes {
		out[i] = cloudimport.TypeRule{Type: "Microsoft.Network/dnsZones/" + typ, Class: cloudimport.ClassDrafted,
			Says: dnsRecordSays}
	}
	return out
}
