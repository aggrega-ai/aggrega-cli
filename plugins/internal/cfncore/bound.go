// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package cfncore

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/wasm"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// boundReference is one place the assembled stack's content still names a
// parameter a contributor's binding filled: the contributor whose content names
// it, the file that content came from, and the parameter.
type boundReference struct {
	node      string
	file      string
	parameter string
}

// refuseReferencesToBoundParameters halts where a contributor's content references
// a parameter another contributor's binding filled and nothing in the assembled
// stack still declares.
//
// A sourced template's binding replaces every reference to a bound parameter in
// every template of its own source and removes the parameter's declaration, so
// nothing in that source names it any more. Content from anywhere else in the stack
// that names the parameter — through Ref, an Fn::Sub variable, or a rule's
// Fn::ValueOf — then names something the stack no longer declares, which
// CloudFormation refuses when the stack deploys. The build made that reference
// dangle, so the build reports it, naming each contributor, its file and the
// parameter, together with the contributors whose binding filled it. A name some
// contributor still declares — a parameter of its own, or a resource's logical ID —
// is a reference the stack resolves, and is left as written.
//
// It reads only where some contributor bound a parameter; a stack whose
// contributors bound nothing is not walked, so it assembles exactly as before.
// Every finding is reported rather than the first, in the order the contributors,
// their files, the sections and the parameter names are walked, so the same input
// halts with the same sentence.
func refuseReferencesToBoundParameters(nodes []NodeFragment, decoded [][]map[string]any, declared func(name string) bool) error {
	boundBy := map[string][]string{}
	for _, node := range nodes {
		identity := ContributorNameIdentity(node)
		for _, name := range node.Bound {
			if !slices.Contains(boundBy[name], identity) {
				boundBy[name] = append(boundBy[name], identity)
			}
		}
	}
	if len(boundBy) == 0 {
		return nil
	}
	names := make([]string, 0, len(boundBy))
	for name := range boundBy {
		names = append(names, name)
	}
	sort.Strings(names)
	var dangling []string
	for _, name := range names {
		if !declared(name) {
			dangling = append(dangling, name)
		}
	}
	if len(dangling) == 0 {
		return nil
	}

	var found []boundReference
	for i, node := range nodes {
		identity := ContributorNameIdentity(node)
		for j, frag := range decoded[i] {
			for _, name := range dangling {
				if fragmentNamesParameter(frag, name) {
					found = append(found, boundReference{node: identity, file: node.fileName(j), parameter: name})
				}
			}
		}
	}
	if len(found) == 0 {
		return nil
	}

	phrases := make([]string, 0, len(found))
	for _, f := range found {
		phrases = append(phrases, fmt.Sprintf("component %s's template %s references parameter %s, which the valuesMapping of a Recipe or FeatureRecipe rendering for %s binds",
			strconv.Quote(f.node), strconv.Quote(f.file), strconv.Quote(f.parameter), componentPhrase(boundBy[f.parameter])))
	}
	return cerrors.Wrap(wasm.ErrPluginRequiredInputUnresolved(),
		cerrors.TranslationError, cerrors.SourceLocation{},
		fmt.Sprintf("cloudformation-provider: a parameter a sourced template's binding filled is still referenced from elsewhere in the assembled stack: %s; binding a parameter removes its declaration from the source that declares it, and no template in the stack declares it any more, so the reference names nothing CloudFormation can resolve: reference what the binding component publishes instead, declare the parameter in the template that references it, or remove the key from the valuesMapping",
			strings.Join(phrases, "; ")))
}

// fragmentNamesParameter reports whether a fragment names one parameter in a
// section CloudFormation evaluates a parameter reference in: by Ref or as an
// Fn::Sub variable its variable map does not name ([referencesParameter]), or by a
// rule's Fn::ValueOf, which names a declared parameter by its logical ID.
func fragmentNamesParameter(frag map[string]any, name string) bool {
	for _, section := range boundSections {
		body, present := frag[section]
		if !present {
			continue
		}
		if referencesParameter(body, name) {
			return true
		}
	}
	var valueOf bool
	walkValues(frag[sectionRules], func(node map[string]any) {
		if args, ok := node[ruleFunctionValue].([]any); ok && len(args) > 0 {
			if named, ok := args[0].(string); ok && named == name {
				valueOf = true
			}
		}
	})
	return valueOf
}

// fileName names one of a contributor's files in a message: the path it was
// rendered under, or its position where the contributor carries no paths.
func (n NodeFragment) fileName(i int) string {
	if i < len(n.Paths) && n.Paths[i] != "" {
		return n.Paths[i]
	}
	return fmt.Sprintf("#%d", i+1)
}

// componentPhrase names one contributor or several, as a sentence's object.
func componentPhrase(identities []string) string {
	quoted := make([]string, 0, len(identities))
	for _, id := range identities {
		quoted = append(quoted, strconv.Quote(id))
	}
	if len(quoted) == 1 {
		return "component " + quoted[0]
	}
	return "components " + joinPhrases(quoted)
}
