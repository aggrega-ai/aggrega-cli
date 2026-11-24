// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// Code is the code of a diagnostic the core or a format raises. Each code has
// one severity, which [Report] gives it. The codes the importer kit raises —
// parse-failed, no-nodes, credential-carried, credential-emptied — are raised
// by the kit and are not codes of this vocabulary.
type Code string

// The vocabulary.
const (
	// CodeSourceRefused halts on a source the import cannot read as a whole, or
	// on several sources it could start from and no root setting naming one.
	CodeSourceRefused Code = "source-refused"
	// CodeParameterFilesNotRead warns that several parameters files were
	// handed, and none was read.
	CodeParameterFilesNotRead Code = "parameter-files-not-read"
	// CodeLinkedSourceNotRead warns of a linked template, nested stack or
	// module whose source the import does not hold.
	CodeLinkedSourceNotRead Code = "linked-source-not-read"
	// CodeSubstrateLeftToOperator warns of a substrate resource the source
	// creates and the draft leaves to the operator.
	CodeSubstrateLeftToOperator Code = "substrate-left-to-operator"
	// CodeResourceNotCarried warns of a folded resource or module input no
	// Recipe takes.
	CodeResourceNotCarried Code = "resource-not-carried"
	// CodeResourceFolded records a child resource folded into its parent's
	// instance.
	CodeResourceFolded Code = "resource-folded"
	// CodeResourceDraftedFromSource records a resource no Recipe maps, drafted
	// as a Component and a Recipe from the source: the code the drafting step
	// raises its record under.
	CodeResourceDraftedFromSource = Code(schemadraft.CodeResourceDraftedFromSource)
	// CodeReferenceNotWired warns of a reference no inject carries.
	CodeReferenceNotWired Code = "reference-not-wired"
	// CodeValueNotCarried warns of a value or expression the draft does not
	// carry.
	CodeValueNotCarried = Code(schemadraft.CodeValueNotCarried)
	// CodeCredentialFlow warns of a credential read from another resource.
	CodeCredentialFlow Code = "credential-flow"
	// CodeDiscriminatorUnresolved warns that the property choosing a resource's
	// Component is set by a value with no default.
	CodeDiscriminatorUnresolved Code = "discriminator-unresolved"
	// CodeRepetitionNotExpanded warns of a repeated resource drafted once.
	CodeRepetitionNotExpanded Code = "repetition-not-expanded"
	// CodeConditionNotCarried warns of a conditional resource drafted
	// unconditionally.
	CodeConditionNotCarried Code = "condition-not-carried"
	// CodeDeploymentScopeMerged warns of a nested template deployed into
	// another scope, drafted into the one boundary the draft writes.
	CodeDeploymentScopeMerged Code = "deployment-scope-merged"
	// CodeAVMModuleRecognised records an Azure Verified Module recognised, with
	// its identity and version.
	CodeAVMModuleRecognised Code = "avm-module-recognised"
	// CodeModuleVersionUnpinned warns of a module-wrapping draft whose exact
	// module version the source does not state.
	CodeModuleVersionUnpinned = Code(schemadraft.CodeModuleVersionUnpinned)
	// CodeComponentExists warns of a resource held back because the Component
	// name its draft would take is taken already: the resource-schema drafting
	// code's component-exists, raised here as a warning.
	CodeComponentExists = Code(schemadraft.CodeComponentExists)
)

// vocabulary gives each code its severity, in one place.
var vocabulary = []struct {
	code     Code
	severity string
}{
	{CodeSourceRefused, aggrega.SeverityHalt},
	{CodeParameterFilesNotRead, aggrega.SeverityWarn},
	{CodeLinkedSourceNotRead, aggrega.SeverityWarn},
	{CodeSubstrateLeftToOperator, aggrega.SeverityWarn},
	{CodeResourceNotCarried, aggrega.SeverityWarn},
	{CodeResourceFolded, aggrega.SeverityInfo},
	{CodeResourceDraftedFromSource, aggrega.SeverityInfo},
	{CodeReferenceNotWired, aggrega.SeverityWarn},
	{CodeValueNotCarried, aggrega.SeverityWarn},
	{CodeCredentialFlow, aggrega.SeverityWarn},
	{CodeDiscriminatorUnresolved, aggrega.SeverityWarn},
	{CodeRepetitionNotExpanded, aggrega.SeverityWarn},
	{CodeConditionNotCarried, aggrega.SeverityWarn},
	{CodeDeploymentScopeMerged, aggrega.SeverityWarn},
	{CodeAVMModuleRecognised, aggrega.SeverityInfo},
	{CodeModuleVersionUnpinned, aggrega.SeverityWarn},
	{CodeComponentExists, aggrega.SeverityWarn},
}

// Report returns the diagnostic of code c with message, which says what to fix
// and quotes no value of the source. The severity is the one the vocabulary
// gives c; a code outside the vocabulary halts, so a misspelt code cannot pass
// as a warning. It is the one place the core and its formats make a diagnostic
// of the vocabulary.
func Report(c Code, message string) aggrega.Diagnostic {
	severity := aggrega.SeverityHalt
	for _, entry := range vocabulary {
		if entry.code == c {
			severity = entry.severity
			break
		}
	}
	return aggrega.Diagnostic{Severity: severity, Code: string(c), Message: message}
}
