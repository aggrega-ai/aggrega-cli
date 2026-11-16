// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"errors"
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
)

// TestFrozenNames pins every constant the arm provider emits. These values are
// part of the compatibility contract: a template written today must keep its
// symbolic names, history names and annotations when the provider is upgraded.
func TestFrozenNames(t *testing.T) {
	cases := []struct {
		name string
		got  string
		want string
	}{
		{"TemplateFileName", TemplateFileName, "arm-template.json"},
		{"KeySpaceSymbolicNames", KeySpaceSymbolicNames, "symbolic-names"},
		{"KeySpaceOutputNames", KeySpaceOutputNames, "output-names"},
		{"ValidationCodeTemplateParse", ValidationCodeTemplateParse, "arm-template-parse"},
		{"ValidationCodeTemplateShape", ValidationCodeTemplateShape, "arm-template-shape"},
		{"ProvenanceMetadataKey", ProvenanceMetadataKey, "aggrega.ai/provenance"},
		{"DeploymentNameMetadataKey", DeploymentNameMetadataKey, "aggrega.ai/deployment-name"},
		{"DeploymentsAPIVersion", DeploymentsAPIVersion, "2025-04-01"},
		{"LanguageVersion", LanguageVersion, "2.0"},
		{"ContentVersion", ContentVersion, "1.0.0.0"},
		{"SchemaResourceGroup", SchemaResourceGroup, "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"},
		{"SchemaSubscription", SchemaSubscription, "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#"},
		{"SchemaManagementGroup", SchemaManagementGroup, "https://schema.management.azure.com/schemas/2019-08-01/managementGroupDeploymentTemplate.json#"},
		{"LevelResourceGroupSymbol", LevelResourceGroupSymbol, "_resourceGroup"},
		{"LevelSubscriptionSymbol", LevelSubscriptionSymbol, "_subscription"},
		{"TargetName", TargetName, "arm"},
		{"AnnotationSubscription", AnnotationSubscription, "aggrega.ai/azure-subscription"},
		{"AnnotationResourceGroup", AnnotationResourceGroup, "aggrega.ai/azure-resource-group"},
		{"AnnotationManagementGroup", AnnotationManagementGroup, "aggrega.ai/azure-management-group"},
		{"AnnotationDeploymentScope", AnnotationDeploymentScope, "aggrega.ai/azure-deployment-scope"},
		{"ScopeResourceGroup", string(ScopeResourceGroup), "resourceGroup"},
		{"ScopeSubscription", string(ScopeSubscription), "subscription"},
		{"ScopeManagementGroup", string(ScopeManagementGroup), "managementGroup"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, want %q", c.name, c.got, c.want)
		}
	}
}

// TestOutputName pins the output name derivation: an identity and an entry each
// encode over the lowercase alphanumerics, joined by a double underscore, and the
// name never opens with a digit.
func TestOutputName(t *testing.T) {
	if got, want := OutputName("payments-db", "host"), "payments_zhdb__host"; got != want {
		t.Errorf("OutputName(payments-db, host) = %q, want %q", got, want)
	}
	upper, lower := OutputName("Db", "host"), OutputName("db", "host")
	if upper == lower {
		t.Errorf("OutputName differs only in case for Db and db: both %q", upper)
	}
	if strings.EqualFold(upper, lower) {
		t.Errorf("OutputName(Db) %q and OutputName(db) %q are equal case-insensitively", upper, lower)
	}
	if got := OutputName("9lives", "x"); !strings.HasPrefix(got, "_") {
		t.Errorf("OutputName(9lives, x) = %q, want it to open with _", got)
	}
}

// TestDeploymentName pins the deployment name derivation and its 64-character
// limit, which ARM enforces on a deployment name.
func TestDeploymentName(t *testing.T) {
	got, err := DeploymentName("orders", "stack-a/arm")
	if err != nil {
		t.Fatalf("DeploymentName(orders, stack-a/arm) error: %v", err)
	}
	if want := "aggrega--orders--stack-a-zsarm"; got != want {
		t.Errorf("DeploymentName(orders, stack-a/arm) = %q, want %q", got, want)
	}

	// 9 for "aggrega--", 6 for "orders", 2 for "--", then 48 more: 65 in all.
	long := strings.Repeat("a", 48)
	if _, err := DeploymentName("orders", long); err == nil {
		t.Fatal("DeploymentName of 65 characters returned no error")
	} else {
		if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
			t.Errorf("DeploymentName error = %v, want ErrPluginValueNotRepresentable", err)
		}
		if !strings.Contains(err.Error(), "64") {
			t.Errorf("DeploymentName error %q does not name the 64-character limit", err)
		}
		if !strings.Contains(err.Error(), "orders") || !strings.Contains(err.Error(), long) {
			t.Errorf("DeploymentName error %q does not name the release and boundary", err)
		}
	}

	// Exactly 64 characters is within the limit.
	exact, err := DeploymentName("orders", strings.Repeat("a", 47))
	if err != nil {
		t.Fatalf("DeploymentName of 64 characters error: %v", err)
	}
	if len(exact) != 64 {
		t.Errorf("DeploymentName of 64 characters has length %d", len(exact))
	}
}

// TestNestedDeploymentSymbol pins the nested deployment symbol: the instance id
// capitalised word by word, refused when empty, when it opens with a digit or
// when it exceeds 50 characters.
func TestNestedDeploymentSymbol(t *testing.T) {
	got, err := NestedDeploymentSymbol("payments-db")
	if err != nil {
		t.Fatalf("NestedDeploymentSymbol(payments-db) error: %v", err)
	}
	if got != "PaymentsDb" {
		t.Errorf("NestedDeploymentSymbol(payments-db) = %q, want PaymentsDb", got)
	}

	refused := map[string]string{
		"123":                   "leading digit",
		"--":                    "empty after capitalisation",
		strings.Repeat("a", 51): "51 characters",
	}
	for id := range refused {
		_, err := NestedDeploymentSymbol(id)
		if err == nil {
			t.Errorf("NestedDeploymentSymbol(%q) returned no error", id)
			continue
		}
		if !errors.Is(err, plugincondition.ErrPluginValueNotRepresentable()) {
			t.Errorf("NestedDeploymentSymbol(%q) error = %v, want ErrPluginValueNotRepresentable", id, err)
		}
		if !strings.Contains(err.Error(), id) {
			t.Errorf("NestedDeploymentSymbol(%q) error %q does not name the id", id, err)
		}
	}
}

// TestNestedDeploymentHistoryName pins the history name a nested deployment is
// given, built from its symbol.
func TestNestedDeploymentHistoryName(t *testing.T) {
	want := "[format('PaymentsDb-{0}', uniqueString(deployment().name))]"
	if got := NestedDeploymentHistoryName("PaymentsDb"); got != want {
		t.Errorf("NestedDeploymentHistoryName(PaymentsDb) = %q, want %q", got, want)
	}
}

// TestLevelHistoryName pins the history names of the resource group and
// subscription level deployments.
func TestLevelHistoryName(t *testing.T) {
	cases := map[Scope]string{
		ScopeResourceGroup: "[format('rg-{0}', uniqueString(deployment().name))]",
		ScopeSubscription:  "[format('sub-{0}', uniqueString(deployment().name))]",
	}
	for scope, want := range cases {
		if got := LevelHistoryName(scope); got != want {
			t.Errorf("LevelHistoryName(%q) = %q, want %q", scope, got, want)
		}
	}
}
