// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudformation

import (
	"slices"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

const (
	formatName = "cloudformation-schema"
	capability = "cloudformation"
	syntax     = "json"
)

// Format is the CloudFormation schema format, cloudformation-schema: one
// resource type per source, a provider schema or a describe-type output, its
// names matched exactly. Its Recipes target the cloudformation provider. No
// property is a placement, since a stack's resources are created in the Region
// it is deployed to; identity is the primary identifier, Name and the property
// named after the type's last segment; a secret is a string property named for
// a credential, declared as a deploy-time parameter; a read-back scalar nested
// in objects is provided, since GetAtt addresses it by a dotted path.
var Format = schemadraft.Format{
	Name:       formatName,
	Capability: capability,
	Syntax:     syntax,
	List:       list,
	Lift:       lift,
	Rules: schemadraft.Rules{
		Identity:       identity,
		Secret:         secret,
		NestedProvides: true,
	},
	Category: category,
	Write:    Write,
}

// nameSuffix closes the name of a property naming its resource: <Leaf>Name, the
// leaf being the type name's last segment, and Name itself.
const nameSuffix = "Name"

// identity reports whether p is the resource's own name or identifier: a
// property of its primary identifier, Name, or the type's last segment followed
// by Name (QueueName for AWS::SQS::Queue).
func identity(t *schemadraft.Type, p *schemadraft.Property) bool {
	segments := strings.Split(t.Name, "::")
	leaf := segments[len(segments)-1]
	return p.Identifier || p.Name == nameSuffix || p.Name == leaf+nameSuffix
}

// secret reports whether p is a secret, which CloudFormation does not mark: a
// string property named for a credential.
func secret(_ *schemadraft.Type, p *schemadraft.Property) bool { return credentialNamed(p) }

// credentialWords are the words naming a credential, compared without regard to
// case.
var credentialWords = []string{"password", "secret", "token", "credential", "privatekey", "apikey", "passphrase"}

// referenceWords are the last words of a name that refers to a credential held
// elsewhere rather than holding one: an ARN, an id, a name or a reference.
var referenceWords = []string{"arn", "id", "name", "ref"}

// credentialNamed reports whether p is a string whose name holds a credential
// word and whose last word does not make it a reference to one: MasterPassword
// is a credential, SecretArn a reference.
func credentialNamed(p *schemadraft.Property) bool {
	if !slices.Equal(p.Kinds, []schemadraft.Kind{schemadraft.KindString}) {
		return false
	}
	words := strings.Split(schemadraft.SnakeKey(p.Name), "_")
	if slices.Contains(referenceWords, words[len(words)-1]) {
		return false
	}
	folded := strings.ToLower(p.Name)
	return slices.ContainsFunc(credentialWords, func(w string) bool { return strings.Contains(folded, w) })
}
