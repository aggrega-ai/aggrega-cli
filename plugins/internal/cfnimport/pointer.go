// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"github.com/aggrega-ai/aggrega-cli/internal/alphanumeric"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/draftyaml"
)

// pointer returns the pointer the cloudformation target holds for v: the text
// of deploy-time text written for that target and owing nothing, which
// CloudFormation evaluates when the stack the build writes deploys. Any other
// value has none: a Ref or an Fn::GetAtt names a resource by its logical ID
// within one stack, and a resource the draft leaves to the operator is no
// resource of the stack the build writes, so the operator names it.
func pointer(v cloudimport.Value, _ *cloudimport.Graph) (string, bool) {
	o, ok := v.(cloudimport.Opaque)
	if !ok || o.Target != target || o.Owed != "" {
		return "", false
	}
	return o.Text, true
}

// declares reports whether the stack the build writes declares the resource
// of read under its logical ID in the template, so deploy-time text naming it
// by that ID reads it: a resource drafted from the source is declared under
// the capitalised words of its instance key, and a mapped one is where the
// Recipe of its row names it by those words alone ([logicalIDSuffix]) and
// deploys a resource of the type the template writes; in both, only where
// those words are the ID the template declares it under. A mapped resource
// whose Recipe deploys one of another type in its place ([retyped]) is none,
// as the text reads attributes the resource the build writes does not have,
// and any other resource is no resource of that stack under its ID.
func declares(read cloudimport.Classified) bool {
	if read.Node == nil || read.Key == "" {
		return false
	}
	switch read.Class {
	case cloudimport.ClassDrafted:
	case cloudimport.ClassMapped:
		if read.Row == nil || retyped(read.Row) {
			return false
		}
		if suffix, known := logicalIDSuffix(read.Row.Recipe); !known || suffix != "" {
			return false
		}
	default:
		return false
	}
	return alphanumeric.CapitalizedWords(read.Key) == read.Node.ID
}

// instanceComment is the line written in the comment above the instance of
// cl where the stack the build writes names its resource otherwise than the
// template does, so updating a stack the template deployed replaces it: a
// resource drafted from the source is named by the capitalised words of its
// key, and a mapped one as the Recipe of its row names it.
func instanceComment(cl cloudimport.Classified) string {
	if cl.Node == nil || cl.Key == "" {
		return ""
	}
	switch {
	case cl.Class == cloudimport.ClassDrafted:
		return keyNote(cl.Node.ID, cl.Key)
	case cl.Class == cloudimport.ClassMapped && cl.Row != nil:
		return mappedNote(cl.Node.ID, cl.Key, cl.Row.Recipe)
	case cl.Class == cloudimport.ClassMapped:
		return "The stack the build writes names it as the Recipe realizing " + cl.Name + " names the resource " +
			"it deploys, where the template names it " + draftyaml.Comment(cl.Node.ID) + ": where the two differ, " +
			"updating a stack the template deployed replaces the resource."
	}
	return ""
}
