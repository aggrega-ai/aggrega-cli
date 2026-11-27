// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft/cloudformation"
	"github.com/aggrega-ai/aggrega-pdk/go/pkg/aggrega"
)

// syntax is the syntactic format the cloudformation format names for its
// sources: a template is YAML, JSON being YAML too, read by the template
// reader as CloudFormation reads it.
const syntax = "yaml"

// Format is the cloudformation import format: CloudFormation templates,
// AWS SAM templates among them, read as CloudFormation reads them and drafted
// for the cloudformation target. It reads a request's templates with [Read],
// maps their resources through [Table], leaves the substrate and the types no
// Recipe carries to its type rules, attaches a Secrets Manager secret a
// mapped instance reads at a credential slot as the Feature its attach rules
// name, keys an instance by its logical ID's words ([Key]), writes in an
// instance's comment where the stack the build writes names its resource
// otherwise than the template does, writes deploy-time text reading a
// resource the draft declares under the same logical ID, and drafts a
// resource no row maps from the source as the cloudformation-schema format
// drafts one, publishing its identifier alone, which a resource's Ref reads
// on every type. Its pointer writer writes deploy-time text alone: a Ref to a
// resource the draft leaves to the operator names nothing in the stack the
// build writes.
var Format = cloudimport.Format{
	Name:            formatName,
	Target:          target,
	Syntax:          syntax,
	Read:            Read,
	Table:           Table(),
	Types:           typeRules,
	Attach:          attachRules,
	Key:             Key,
	InstanceComment: instanceComment,
	Pointer:         pointer,
	Declares:        declares,
	Draft:           cloudformation.Format,
	DraftProvides:   []schemadraft.Provide{{Key: "id", Attribute: []string{"id"}, Identifier: true}},
}

// Import turns one import request into the Aggregate, the Release and the
// catalog entities its CloudFormation templates draft, as
// [cloudimport.Import] does for any format, through [Format].
func Import(in aggrega.ImportInput, parse cloudimport.ParseFunc) aggrega.ImportOutput {
	return cloudimport.Import(in, Format, parse)
}
