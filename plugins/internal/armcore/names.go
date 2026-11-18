// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"fmt"

	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// TemplateFileName is the file an assembled ARM template is written to.
const TemplateFileName = "arm-template.json"

// KeySpaceSymbolicNames is the key space the symbolic names of the resources a
// node's fragments carry are declared in, so a name two nodes both declare halts
// the build. The symbolic names of the level deployments the assembly adds,
// LevelResourceGroupSymbol and LevelSubscriptionSymbol, are not declared there:
// a top-level resource holding one halts when the level is added (nestLevel).
const KeySpaceSymbolicNames = "symbolic-names"

// KeySpaceOutputNames is the key space the output names are declared in, so a
// name two nodes both declare halts the build.
const KeySpaceOutputNames = "output-names"

// ValidationCodeTemplateParse is the validation code for a template that does not
// parse.
const ValidationCodeTemplateParse = "arm-template-parse"

// ValidationCodeTemplateShape is the validation code for a parsed template whose
// top level does not have the shape an ARM deployment requires.
const ValidationCodeTemplateShape = "arm-template-shape"

// ProvenanceMetadataKey is the Metadata key a resource carries its build
// provenance under. It is the same key the cloudformation provider writes, so
// one provenance reader serves both.
const ProvenanceMetadataKey = rendercore.ProvenanceMetadataKey

// DeploymentNameMetadataKey is the Metadata key a nested deployment records the
// deployment name it was given under.
const DeploymentNameMetadataKey = "aggrega.ai/deployment-name"

// DeploymentsAPIVersion is the apiVersion stamped on every deployments resource
// the provider writes.
const DeploymentsAPIVersion = "2025-04-01"

// LanguageVersion is the languageVersion stamped at the top of every template.
const LanguageVersion = "2.0"

// ContentVersion is the contentVersion stamped at the top of every template.
const ContentVersion = "1.0.0.0"

// SchemaResourceGroup is the $schema of a template deployed to a resource group.
const SchemaResourceGroup = "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#"

// SchemaSubscription is the $schema of a template deployed to a subscription.
const SchemaSubscription = "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#"

// SchemaManagementGroup is the $schema of a template deployed to a management
// group.
const SchemaManagementGroup = "https://schema.management.azure.com/schemas/2019-08-01/managementGroupDeploymentTemplate.json#"

// LevelResourceGroupSymbol is the symbolic name of the nested deployment that
// lands a resource group level boundary.
const LevelResourceGroupSymbol = "_resourceGroup"

// LevelSubscriptionSymbol is the symbolic name of the nested deployment that
// lands a subscription level boundary.
const LevelSubscriptionSymbol = "_subscription"

// TargetName is the name the arm provider is known by in the Release and the
// catalog.
const TargetName = "arm"

// AnnotationSubscription is the annotation naming the subscription a Component
// is deployed to.
const AnnotationSubscription = "aggrega.ai/azure-subscription"

// AnnotationResourceGroup is the annotation naming the resource group a Component
// is deployed to.
const AnnotationResourceGroup = "aggrega.ai/azure-resource-group"

// AnnotationManagementGroup is the annotation naming the management group a
// Component is deployed to.
const AnnotationManagementGroup = "aggrega.ai/azure-management-group"

// AnnotationDeploymentScope is the annotation naming the scope a Component is
// deployed at: one of the Scope values.
const AnnotationDeploymentScope = "aggrega.ai/azure-deployment-scope"

// Scope is the deployment scope a template is written for.
type Scope string

// The scopes a template can be deployed at. Each value is the one the
// AnnotationDeploymentScope annotation carries.
const (
	// ScopeResourceGroup is a deployment to one resource group.
	ScopeResourceGroup Scope = "resourceGroup"
	// ScopeSubscription is a deployment to one subscription.
	ScopeSubscription Scope = "subscription"
	// ScopeManagementGroup is a deployment to one management group.
	ScopeManagementGroup Scope = "managementGroup"
)

// deploymentOpener is the character the deployment name encoding opens its
// escapes with; deployment names admit no hyphen, so it is spared.
const deploymentOpener = '-'

// outputOpener is the character the output name encoding opens its escapes with;
// output names admit no underscore, so it is spared.
const outputOpener = '_'

// deploymentNameLimit is the longest deployment name ARM accepts.
const deploymentNameLimit = 64

// nestedSymbolLimit is the longest nested deployment symbol the provider
// derives, so its history name stays within the deployment name limit.
const nestedSymbolLimit = 50

// deploymentNamePrefix is the fixed leading segment of every deployment name.
const deploymentNamePrefix = "aggrega"

// lowerAlnum reports whether r is a lowercase ASCII letter or a digit.
func lowerAlnum(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9'
}

// notDigit reports whether r is anything but a digit, so the output name
// encoding can refuse a digit where a segment opens.
func notDigit(r rune) bool {
	return r < '0' || r > '9'
}

// outputNameEncoding encodes the identity and entry of an output name. It admits
// the lowercase alphanumerics, refuses a digit where a segment opens, and joins
// segments with a double underscore, which no encoded segment contains.
var outputNameEncoding = namesegment.NewEncoding(outputOpener, lowerAlnum, namesegment.AdmitsFirst(notDigit))

// deploymentNameEncoding encodes the segments of a deployment name. It admits the
// lowercase alphanumerics and joins segments with a double hyphen.
var deploymentNameEncoding = namesegment.NewEncoding(deploymentOpener, lowerAlnum)

// OutputName names the template output one entry of one component's identity
// exports: the identity and the entry, each encoded over the lowercase
// alphanumerics and joined by a double underscore. Two calls produce one name
// only where both inputs match exactly, so names differing only in case stay
// distinct.
func OutputName(identity, entry string) string {
	return outputNameEncoding.Name(identity, entry)
}

// DeploymentName names the deployment the boundary of one Release is landed by:
// the fixed prefix, the Release and the boundary, encoded over the lowercase
// alphanumerics. It returns an error wrapping ErrPluginValueNotRepresentable when
// the result is longer than 64 characters.
func DeploymentName(release, boundary string) (string, error) {
	name := deploymentNameEncoding.Name(deploymentNamePrefix, release, boundary)
	if len(name) > deploymentNameLimit {
		return "", cerrors.Wrap(plugincondition.ErrPluginValueNotRepresentable(),
			cerrors.TranslationError, cerrors.SourceLocation{},
			fmt.Sprintf("arm-provider: the deployment name for Release %q and boundary %q would be %d characters, past the %d-character limit ARM sets on a deployment name; shorten the Release name or the boundary name",
				release, boundary, len(name), deploymentNameLimit))
	}
	return name, nil
}

// NestedDeploymentSymbol derives the symbolic name of the nested deployment that
// lands one sourced instance: the instance id capitalised word by word. It returns
// an error wrapping ErrPluginValueNotRepresentable when the symbol is empty, opens
// with a digit, or is longer than 50 characters.
func NestedDeploymentSymbol(identity string) (string, error) {
	symbol := alphanumeric.CapitalizedWords(identity)
	switch {
	case symbol == "":
		return "", valueNotRepresentable(fmt.Sprintf("arm-provider: the instance id %q has no letters or digits to name a nested deployment by; rename the instance", identity))
	case symbol[0] >= '0' && symbol[0] <= '9':
		return "", valueNotRepresentable(fmt.Sprintf("arm-provider: the instance id %q names a nested deployment that opens with a digit, which ARM does not accept; rename the instance so it opens with a letter", identity))
	case len(symbol) > nestedSymbolLimit:
		return "", valueNotRepresentable(fmt.Sprintf("arm-provider: the instance id %q names a nested deployment of %d characters, past the %d-character limit; shorten the instance id",
			identity, len(symbol), nestedSymbolLimit))
	}
	return symbol, nil
}

// NestedDeploymentHistoryName is the name a nested deployment is given: the
// symbol and a hash of the outer deployment's name, so it is stable for one outer
// deployment and no longer than the deployment name limit.
func NestedDeploymentHistoryName(symbol string) string {
	return historyName(FormatLiteral(symbol))
}

// LevelHistoryName is the name the resource group or subscription level
// deployment is given. It returns "" for a scope that has no level deployment.
func LevelHistoryName(level Scope) string {
	switch level {
	case ScopeResourceGroup:
		return historyName("rg")
	case ScopeSubscription:
		return historyName("sub")
	}
	return ""
}

// historyName is the bracketed format expression that gives a deployment a name
// made of prefix, a hyphen and a hash of the outer deployment's name. The prefix
// is already a format-string fragment.
func historyName(prefix string) string {
	return "[format(" + StringLiteral(prefix+"-{0}") + ", uniqueString(deployment().name))]"
}

// valueNotRepresentable wraps ErrPluginValueNotRepresentable around a message.
func valueNotRepresentable(message string) error {
	return cerrors.Wrap(plugincondition.ErrPluginValueNotRepresentable(),
		cerrors.TranslationError, cerrors.SourceLocation{}, message)
}
