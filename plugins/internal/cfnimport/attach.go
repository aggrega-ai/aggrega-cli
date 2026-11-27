// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
)

// secretType is the type of a Secrets Manager secret.
const secretType = "AWS::SecretsManager::Secret"

// generated is the property of a Secrets Manager secret whose value Secrets
// Manager generates.
var generated = []string{"Properties", "GenerateSecretString"}

// attachRules are the cloudformation format's attach rules: a Secrets Manager
// secret an instance a row maps reads at a credential slot is no instance of
// its own. One Secrets Manager generates is the core/random-password the
// cloudformation/random-password FeatureRecipe deploys as that secret, on
// the first reader, its key, its length, whether it holds punctuation and its
// name read from the secret, and the kit's referenced secret on each other
// reader, naming the first's secret — a reader whose Recipe admits
// core/random-password alone at the slot decided as halting there, the fix a
// core/random-password of its own ([AdmittedFeature]); one holding a value the
// template writes is the kit's referenced secret on each reader, its origin
// the secret's name, and its value is never written.
var attachRules = []cloudimport.AttachRule{
	{
		Type:    secretType,
		When:    []cloudimport.Pred{{Path: generated, Op: cloudimport.OpPresent}},
		Feature: "core/random-password",
		Via:     cloudimport.AttachReader,
		Values: []cloudimport.Placement{
			{From: append(generated[:2:2], "GenerateStringKey"), Key: "key", Conv: "string"},
			{From: append(generated[:2:2], "PasswordLength"), Key: "length"},
			{From: append(generated[:2:2], "ExcludePunctuation"), Key: "symbols", Conv: "map:true=false,false=true"},
			{From: []string{"Properties", "Name"}, Key: "name", Conv: "string"},
		},
		Others:   "core/referenced-secret",
		Admitted: AdmittedFeature,
	},
	{
		Type:   secretType,
		Via:    cloudimport.AttachReader,
		Values: []cloudimport.Placement{{From: []string{"Properties", "Name"}, Key: "origin", Conv: "string"}},
		Secret: true,
	},
}

// masterPasswordTypes are the types of an RDS instance and an Aurora cluster,
// whose Recipes have RDS generate the master password and keep it in AWS
// Secrets Manager.
var masterPasswordTypes = []string{"AWS::RDS::DBInstance", "AWS::RDS::DBCluster"}

// masterPasswords records, on each RDS instance and Aurora cluster of g
// writing one, what the Recipe a row maps it to makes of its master password:
// a password it writes is never attached, as the Recipe has RDS generate the
// master password and keep it in AWS Secrets Manager, admitting no credential
// but core/generated-secret in its slot; and ManageMasterUserPassword is the
// Recipe's own. A resource drafted from the source keeps both, so neither is
// said where no row maps it.
func masterPasswords(g *cloudimport.Graph) {
	for _, n := range g.Nodes {
		if !slices.ContainsFunc(masterPasswordTypes, func(t string) bool { return strings.EqualFold(t, n.Type) }) {
			continue
		}
		props := propAt(n.Body, []string{"Properties"})
		if props == nil {
			continue
		}
		if propAt(props.Props, []string{"MasterUserPassword"}) != nil {
			n.SaysIfMapped = append(n.SaysIfMapped, "its master password, written at Properties.MasterUserPassword, "+
				"is never attached: the Recipe has RDS generate the master password and keep it in AWS Secrets "+
				"Manager, and admits no credential but core/generated-secret in the master password's slot, so a "+
				"client reads the password RDS keeps there")
		}
		if propAt(props.Props, []string{"ManageMasterUserPassword"}) != nil {
			n.SaysIfMapped = append(n.SaysIfMapped, "it writes Properties.ManageMasterUserPassword, which the "+
				"Recipe writes true itself, RDS generating the master password and keeping it in AWS Secrets Manager")
		}
	}
}
