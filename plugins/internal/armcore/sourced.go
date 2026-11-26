// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/jsonc"
)

// The two file names a directory source may hold its compiled template under, in
// the order they are preferred.
const (
	templateFileMain        = "main.json"
	templateFileAzureDeploy = "azuredeploy.json"
)

// SelectTemplate names the file of an acquired source that holds the compiled
// template, as a path relative to the source root. A source that is one file is
// that file whatever its name, and its path is the empty string, which the host
// reads as the root itself. A directory source holds the template at its root as
// main.json, or else azuredeploy.json; where both are present main.json is
// chosen. A directory holding neither returns an error wrapping
// ErrPluginRequiredInputUnresolved.
func SelectTemplate(listing rendercore.SourceListing) (path string, err error) {
	if !listing.RootIsDir {
		return "", nil
	}
	present := map[string]bool{}
	for _, e := range listing.Entries {
		if !e.IsDir {
			present[e.Path] = true
		}
	}
	for _, name := range []string{templateFileMain, templateFileAzureDeploy} {
		if present[name] {
			return name, nil
		}
	}
	return "", refuse(plugincondition.ErrPluginRequiredInputUnresolved,
		fmt.Sprintf("the template source %q holds neither %s nor %s at its root; name the compiled template as a single file, or add one of those files to the directory, and compile a .bicep source to a template with bicep build first",
			listing.Root, templateFileMain, templateFileAzureDeploy))
}

// templateSubject names the template in a sentence: the file when the source
// has one named, otherwise the source as a whole.
func templateSubject(path string) string {
	if path == "" {
		return "the sourced template"
	}
	return fmt.Sprintf("the template %q", path)
}

// readTemplate reads a sourced template as a JSON object tree with numbers kept
// as written. The text goes through the shared JSON-with-comments reader, which
// drops a leading byte-order mark and admits comments and trailing commas. A
// document that is not one JSON value is an error naming what the reader found.
func readTemplate(content []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(jsonc.Strip(content)))
	dec.UseNumber()
	var doc any
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("text after the end of the template")
	}
	return doc, nil
}

// ValidateTemplate reads one sourced template and returns a halt diagnostic for
// each fault: ValidationCodeTemplateParse when it does not parse, and
// ValidationCodeTemplateShape when it parses but is not a template the provider
// wraps. Its shape is a JSON object that names a $schema of a resource group,
// subscription or management group template, and writes resources as an object
// under a 2.x languageVersion and as a list without one. path names the file in
// the diagnostics; the empty path names the source as a whole.
func ValidateTemplate(path string, content []byte) []renderctx.Diagnostic {
	halt := func(code, message string) []renderctx.Diagnostic {
		return []renderctx.Diagnostic{{
			Severity: abi.SeverityHalt,
			Code:     code,
			Message:  "arm-provider: " + message,
			Location: &renderctx.DiagnosticLocation{Kind: "arm", URI: path},
		}}
	}
	subject := templateSubject(path)
	doc, err := readTemplate(content)
	if err != nil {
		return halt(ValidationCodeTemplateParse, fmt.Sprintf("%s does not parse as JSON: %v; correct the file, or compile it to a template again", subject, err))
	}
	if problem := shapeProblem(doc); problem != "" {
		return halt(ValidationCodeTemplateShape, fmt.Sprintf("%s %s", subject, problem))
	}
	return nil
}

// shapeProblem says what keeps a parsed template from being wrapped, as a
// clause that follows the template's name, or returns "" when it can be.
func shapeProblem(doc any) string {
	root, ok := doc.(map[string]any)
	if !ok {
		return "is not a JSON object; write the template as one object with a $schema and resources"
	}
	schema, present := root[sectionSchema]
	if !present {
		return "has no $schema; add the address of the template schema, which names the scope the template deploys to"
	}
	if _, err := scopeOfSchema(schema); err != nil {
		return "has a $schema the provider does not wrap: " + strings.TrimPrefix(messageOf(err), "arm-provider: ")
	}
	resources, present := root[sectionResources]
	if !present {
		return ""
	}
	switch resources.(type) {
	case []any:
		if isLanguageVersion2(root[sectionLanguageVersion]) {
			return "declares a languageVersion of 2.x and writes resources as a list; key resources by symbolic name, or remove the languageVersion"
		}
	case map[string]any:
		if !isLanguageVersion2(root[sectionLanguageVersion]) {
			return "writes resources as an object without a languageVersion of 2.x; declare languageVersion 2.0, or write resources as a list"
		}
	default:
		return "writes resources as neither a list nor an object"
	}
	return ""
}

// isLanguageVersion2 reports whether a languageVersion value names a 2.x
// language version, the versions that key resources by symbolic name.
func isLanguageVersion2(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(s, "2.")
}

// messageOf returns the message of a refusal built by refuse, without its
// condition's own text.
func messageOf(err error) string {
	type messager interface{ Message() string }
	if m, ok := err.(messager); ok {
		return m.Message()
	}
	return err.Error()
}

// SourcedNode is one node that sources a compiled template.
type SourcedNode struct {
	// Identity is the instance id the nested deployment's symbolic name is
	// derived from.
	Identity string
	// Path is the file the template was read from, as SelectTemplate returned it;
	// it is empty for a single-file source.
	Path string
	// Content is the template file's bytes.
	Content []byte
	// Values is the node's values mapping, read where ValuesMapped; SensitiveValues
	// reports that the mapping's output depends on a value declared sensitive: the
	// mapping reads one, and what it produces changes, or it fails, when the values
	// declared sensitive are replaced.
	Values                        types.OrderedMap[any]
	ValuesMapped, SensitiveValues bool
	// Omits are the resources the node leaves out of the template.
	Omits []rendercore.ResolvedOmit
	// Provenance is the node's provenance set, stamped on the nested deployment
	// that carries the template and never inside the template.
	Provenance map[string]string
}

// RenderSourced wraps one sourced template as a fragment that holds one nested
// deployment resource: the template, parsed and written again with sorted keys,
// is its properties.template, evaluated in its own scope and deployed
// incrementally, and the values mapping binds its properties.parameters where
// ValuesMapped. A template whose $schema names a subscription or a management
// group is wrapped in a fragment written for that scope, and its nested
// deployment takes the location of the deployment that holds it,
// [deployment().location], which a deployment at those scopes requires; a
// resource group template's fragment writes neither. The node's provenance set
// is stamped on the nested deployment's own metadata. It returns the fragment,
// the nested deployment's symbolic name and the positions of the omits it
// honoured.
func RenderSourced(n SourcedNode) (fragment []byte, symbol string, omitted []int, err error) {
	symbol, err = NestedDeploymentSymbol(n.Identity)
	if err != nil {
		return nil, "", nil, err
	}
	doc, err := readTemplate(n.Content)
	if err != nil {
		return nil, "", nil, valueNotRepresentable(fmt.Sprintf("arm-provider: %s does not parse as JSON: %v; correct the file, or compile it to a template again", templateSubject(n.Path), err))
	}
	if problem := shapeProblem(doc); problem != "" {
		return nil, "", nil, valueNotRepresentable(fmt.Sprintf("arm-provider: %s %s", templateSubject(n.Path), problem))
	}
	template := doc.(map[string]any)
	// shapeProblem has read the $schema, so it names a scope.
	scope, _ := scopeOfSchema(template[sectionSchema])
	omitted, err = leaveOut(templateSubject(n.Path), template, n.Omits)
	if err != nil {
		return nil, "", nil, err
	}

	var parameters map[string]any
	if n.ValuesMapped {
		if parameters, err = bindParameters(n, template); err != nil {
			return nil, "", nil, err
		}
	}
	resource := nestedDeploymentBody(NestedDeploymentHistoryName(symbol), template, parameters)
	if scope != ScopeResourceGroup {
		resource["location"] = "[deployment().location]"
	}
	if len(n.Provenance) > 0 {
		// The wrapper is built here with no metadata of its own, so withProvenance
		// has no non-object metadata to refuse and always reports true.
		resource, _ = withProvenance(resource, n.Provenance)
	}
	wrapped := map[string]any{sectionResources: map[string]any{symbol: resource}}
	if scope != ScopeResourceGroup {
		wrapped[sectionSchema] = schemaOf(scope)
	}
	fragment, err = encodeTemplate(wrapped)
	if err != nil {
		return nil, "", nil, err
	}
	return fragment, symbol, omitted, nil
}

// nestedDeploymentBody returns the body of a nested deployment resource under
// the deployment name name: the deployments apiVersion, template deployed
// incrementally and evaluated in its own scope, and parameters passed to it
// where parameters is not nil.
func nestedDeploymentBody(name string, template, parameters map[string]any) map[string]any {
	properties := map[string]any{
		"mode":                        "Incremental",
		"expressionEvaluationOptions": map[string]any{"scope": "inner"},
		"template":                    template,
	}
	if parameters != nil {
		properties["parameters"] = parameters
	}
	return map[string]any{
		"type":       DeploymentsType,
		"apiVersion": DeploymentsAPIVersion,
		"name":       name,
		"properties": properties,
	}
}
