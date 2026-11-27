// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cfnimport

import "strings"

// The names by which a directory's files are handed to the importer, as the
// file patterns of its recognition signatures declare them: a directory is
// read through its templates and its parameters file alone, claimed by the
// names CloudFormation and AWS SAM projects give them, so no other file a
// directory holds is opened. A template of another name is read when it is
// imported as a file on its own, and a parameters file of another name is
// not read.
var (
	// YAMLTemplateFiles are the names of a template read as YAML: SAM's
	// template.yaml and template.yml, and a name ending .template,
	// .template.yaml, .template.yml, .cfn.yaml or .cfn.yml.
	YAMLTemplateFiles = []string{"template.yaml", "template.yml", "*.template", "*.template.yaml",
		"*.template.yml", "*.cfn.yaml", "*.cfn.yml"}
	// JSONTemplateFiles are the names of a template read as JSON: SAM's
	// template.json, and a name ending .template.json or .cfn.json.
	JSONTemplateFiles = []string{"template.json", "*.template.json", "*.cfn.json"}
	// ParametersFiles are the names of a parameters file: parameters.json,
	// params.json, and a name ending .parameters.json, -parameters.json,
	// .params.json or -params.json.
	ParametersFiles = []string{"parameters.json", "params.json", "*.parameters.json", "*-parameters.json",
		"*.params.json", "*-params.json"}
)

// templateNames lists the names a directory's template is read under, for a
// message naming them.
func templateNames() string {
	var names []string
	for _, n := range append(append([]string(nil), YAMLTemplateFiles...), JSONTemplateFiles...) {
		if !strings.HasPrefix(n, "*") {
			names = append(names, n)
		}
	}
	var endings []string
	for _, n := range append(append([]string(nil), YAMLTemplateFiles...), JSONTemplateFiles...) {
		if e, ok := strings.CutPrefix(n, "*"); ok {
			endings = append(endings, e)
		}
	}
	return strings.Join(names, ", ") + ", or a name ending " + strings.Join(endings[:len(endings)-1], ", ") +
		" or " + endings[len(endings)-1]
}
