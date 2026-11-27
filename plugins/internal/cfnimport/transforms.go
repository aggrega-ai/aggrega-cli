// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/cloudimport"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/schemadraft"
)

// readTransforms are the transforms under which the import reads a template
// as it is written: the serverless transform, whose resource types the table
// maps as they are; the language extensions, whose loops alone it refuses;
// and the Secrets Manager and Service Catalog transforms, each of which
// rewrites only the resources it names.
var readTransforms = []string{
	"AWS::LanguageExtensions",
	"AWS::SecretsManager-2020-07-23",
	"AWS::SecretsManager-2024-09-16",
	"AWS::ServiceCatalog",
	serverlessTransform,
}

const (
	// includeTransform inserts a fragment of template read from Amazon S3.
	includeTransform = "AWS::Include"
	// blueGreenTransform rewrites a template so AWS CodeDeploy shifts the
	// traffic of its Amazon ECS services blue/green.
	blueGreenTransform = "AWS::CodeDeployBlueGreen"
	// languageExtensions is the transform whose loops are Fn::ForEach keys.
	languageExtensions = "AWS::LanguageExtensions"
	// transformKey is the function calling a transform where it is written.
	transformKey = "Fn::Transform"
	// forEachPrefix opens the key of a loop of the language extensions.
	forEachPrefix = "Fn::ForEach"
	// processedFix is what a halt on a transform the import cannot read says
	// to do.
	processedFix = "import the processed template CloudFormation writes for the deployed stack (aws cloudformation " +
		"get-template --template-stage Processed), or write into the template what the transform writes"
)

// transform is one transform a template names.
type transform struct {
	// name is the transform's name, empty where the template names it by no
	// text; at is the JSON pointer where it is named.
	name, at string
}

// declaredTransforms returns each transform raw, a template's Transform
// section, declares, in order: one name, a list of names, or a transform
// written as an object naming it, each item at its JSON pointer.
func declaredTransforms(raw json.RawMessage) []transform {
	one := func(item json.RawMessage, at string) transform {
		if name, ok := schemadraft.JSONText(item); ok {
			return transform{name: name, at: at}
		}
		o, _ := objectOf(item)
		name, _ := schemadraft.JSONText(o.get("Name"))
		return transform{name: name, at: at}
	}
	if raw == nil || isNull(raw) {
		return nil
	}
	items, isList := itemsOf(raw)
	if !isList {
		return []transform{one(raw, jsonPointer("Transform"))}
	}
	out := make([]transform, len(items))
	for i, item := range items {
		out[i] = one(item, jsonPointer("Transform", strconv.Itoa(i)))
	}
	return out
}

// transformDoes names the transform name and says what it does that the
// import cannot read; both empty for a transform under which the import reads
// a template as it is written.
func transformDoes(name string) (named, does string) {
	named = "the transform " + name
	switch {
	case slices.Contains(readTransforms, name):
		return "", ""
	case name == includeTransform:
		does = "which inserts a template fragment it reads from Amazon S3 when the stack deploys"
	case name == blueGreenTransform:
		does = "which rewrites the template to deploy Amazon ECS services blue/green through AWS CodeDeploy when " +
			"the stack deploys"
	case name == "":
		named, does = "a transform it names by no text", "which rewrites the template when the stack deploys"
	default:
		does = "a macro of the account, which rewrites the template when the stack deploys"
	}
	return named, does
}

// refuseTransforms halts source-refused on each template the import reads
// that CloudFormation deploys otherwise than it is written: each transform its
// Transform section declares other than those of readTransforms — AWS::Include,
// AWS::CodeDeployBlueGreen, or a macro of the account, any other name —, each
// call of Fn::Transform wherever it is written, and each loop of the language
// extensions, a key opening Fn::ForEach, whose items are known only once
// CloudFormation evaluates it. Each is named where it is written, the
// templates in path order and the members of each in the order the template
// reader writes them.
func (r *reader) refuseTransforms() {
	for _, t := range r.templates {
		for _, tr := range declaredTransforms(t.top.get("Transform")) {
			if named, does := transformDoes(tr.name); does != "" {
				r.refuse(t, tr.at, "the template "+t.file+" declares "+named+", "+does)
			}
		}
		for _, e := range t.top {
			r.refuseCalls(t, e.Value, jsonPointer(e.Key))
		}
	}
}

// refuseCalls halts on each call of Fn::Transform and each Fn::ForEach loop
// raw, written in t at the JSON pointer at, holds.
func (r *reader) refuseCalls(t *template, raw json.RawMessage, at string) {
	switch kind(raw) {
	case '{':
		o, _ := objectOf(raw)
		for _, e := range o {
			here := at + jsonPointer(e.Key)
			switch {
			case e.Key == transformKey:
				call, _ := objectOf(e.Value)
				name, _ := schemadraft.JSONText(call.get("Name"))
				named, does := transformDoes(name)
				if does == "" {
					named, does = "the transform "+name, "which rewrites the template when the stack deploys"
				}
				r.refuse(t, here, "the template "+t.file+" calls "+named+" with "+transformKey+" at "+here+", "+does)
			case strings.HasPrefix(e.Key, forEachPrefix):
				r.refuse(t, here, "the template "+t.file+" writes the loop "+e.Key+" of the transform "+
					languageExtensions+" at "+here+", which generates what it writes from a collection when the stack "+
					"deploys")
			default:
				r.refuseCalls(t, e.Value, here)
			}
		}
	case '[':
		items, _ := itemsOf(raw)
		for i, item := range items {
			r.refuseCalls(t, item, at+jsonPointer(strconv.Itoa(i)))
		}
	}
}

// refuse halts source-refused on what the template t writes at the JSON
// pointer at, what saying what it is.
func (r *reader) refuse(t *template, at, what string) {
	d := cloudimport.Report(cloudimport.CodeSourceRefused, what+", so the import cannot read the template "+
		"CloudFormation deploys: "+processedFix)
	d.SourceRef = t.file + "#" + at
	r.halt(d)
}
