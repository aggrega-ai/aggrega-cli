// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armimport

import "github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"

// handRows are the rows of the mapping table no arm Recipe's text says, each
// keyed by a vendor type and When as a generated row is. A hand row sharing a
// generated row's key and When is merged into it field by field, and a field
// it sets over a different generated value carries the reason under its
// Supersedes. A row whose Recipe the hub's text alone cannot tell apart from
// another's — two Recipes claiming one key and When for different
// Components — is written here whole, and its Recipe then generates no row.
var handRows = []cloudimport.Row{}

// notImported are the hub's arm Recipes and FeatureRecipes no row reaches,
// each with the reason. A Recipe listed here generates no row.
var notImported = []cloudimport.NotImported{
	{Recipe: "azure-api-management", Reason: "its fragment keys none of its resources by the instance's symbol " +
		"alone: it adds an API, with its operation, policy, backend and named value, to an API Management service " +
		"the operator holds, which it reads as existing, so no one of its resources is the instance by its text"},
	{Recipe: "azure-managed", Reason: "a reference realization: it writes no resource and publishes the endpoint " +
		"infra.endpoint names, so no resource of a template is an instance of it"},
	{Recipe: "azure-openai", Reason: "it sources the Azure Verified Modules module avm/res/cognitive-services/account " +
		"with kind OpenAI, as azure-openai-embedding does for another Component; the model the account serves " +
		"tells the two apart, and no literal of either Recipe writes it, so no row is generated for it"},
	{Recipe: "azure-openai-embedding", Reason: "it sources the Azure Verified Modules module " +
		"avm/res/cognitive-services/account with kind OpenAI, as azure-openai does for another Component; the " +
		"model the account serves tells the two apart, and no literal of either Recipe writes it, so no " +
		"row is generated for it"},
	{Recipe: "dns-record-azure-dns", Reason: "its record set's type is composed from the record type the Release " +
		"names, Microsoft.Network/dnsZones/<type>, and the zone it is added to is the operator's, read as " +
		"existing, so no literal type of its fragment is the instance's"},
	{Recipe: "foundry-agent-service", Reason: "a reference realization: it writes no resource and publishes the " +
		"agent the endpoint infra.endpoint names serves, so no resource of a template is an instance of it"},
	{Recipe: "key-vault-secret", Reason: "a FeatureRecipe, realizing core/referenced-secret on a Key Vault " +
		"secret: the importer attaches a Feature for a secret a template reads, and maps no resource to it"},
	{Recipe: "server-container-apps", Reason: "it sources the Azure Verified Modules module " +
		"avm/res/app/container-app, as worker-container-apps does for another Component; whether the app takes " +
		"ingress tells the two apart, and no literal of either Recipe writes it, so no row is generated for it"},
	{Recipe: "worker-container-apps", Reason: "it sources the Azure Verified Modules module " +
		"avm/res/app/container-app, as server-container-apps does for another Component; whether the app takes " +
		"ingress tells the two apart, and no literal of either Recipe writes it, so no row is generated for it"},
}

// Table returns the arm format's mapping table: the rows generated from the
// hub's arm Recipes at TableRevision, the hand rows, and the Recipes and
// FeatureRecipes no row reaches. Its rows are read and never changed.
func Table() cloudimport.Table {
	return cloudimport.Table{Revision: TableRevision, Generated: generatedRows, Hand: handRows, Images: generatedImages,
		NotImported: notImported}
}
