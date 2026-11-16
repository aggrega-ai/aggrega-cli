// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"encoding/json"
	"fmt"
	"strconv"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
)

// The limits ARM sets on one template. They are measured on the template as
// assembled, before any copy loop expands, so a template that passes can still
// exceed them when ARM deploys it.
const (
	maxTemplateBytes   = 4 * 1024 * 1024
	maxResourceBytes   = 1024 * 1024
	maxParameters      = 256
	maxVariables       = 512
	maxResources       = 800
	maxOutputs         = 64
	maxExpressionChars = 24576
)

// CheckLimits checks an assembled template against the limits ARM sets on one:
// its size (encoded is the bytes it is written as), the size of each resource,
// the number of parameters, variables, resources and outputs, and the length of
// each expression. level is the template as the value tree it is written from.
// The first limit exceeded halts wrapping ErrPluginCannotExpressInput, naming the
// measure and the limit and saying to split the boundary.
func CheckLimits(level map[string]any, encoded []byte) error {
	counts := []struct {
		section, measure string
		limit            int
	}{
		{sectionParameters, "parameters", maxParameters},
		{sectionVariables, "variables", maxVariables},
		{sectionResources, "resources", maxResources},
		{sectionOutputs, "outputs", maxOutputs},
	}
	for _, c := range counts {
		entries, _ := level[c.section].(map[string]any)
		if len(entries) > c.limit {
			return exceeded(fmt.Sprintf("holds %d %s", len(entries), c.measure), fmt.Sprintf("%d %s", c.limit, c.measure))
		}
	}
	resources, _ := level[sectionResources].(map[string]any)
	for _, symbol := range sortedKeys(resources) {
		body, err := json.Marshal(resources[symbol])
		if err != nil {
			return fmt.Errorf("arm-provider: measure resource: %w", err)
		}
		if len(body) > maxResourceBytes {
			return exceeded(fmt.Sprintf("holds the resource at JSON pointer /%s/%s of %d bytes", sectionResources, escapePointer(symbol), len(body)),
				fmt.Sprintf("%d bytes for one resource", maxResourceBytes))
		}
	}
	if pointer, length, found := longExpression(level, ""); found {
		return exceeded(fmt.Sprintf("holds an expression of %d characters at JSON pointer %s", length, pointer),
			fmt.Sprintf("%d characters for one expression", maxExpressionChars))
	}
	if len(encoded) > maxTemplateBytes {
		return exceeded(fmt.Sprintf("is %d bytes", len(encoded)), fmt.Sprintf("%d bytes for one template", maxTemplateBytes))
	}
	return nil
}

// exceeded is the halt for a limit passed: what the template holds and the limit.
func exceeded(what, limit string) error {
	return refuse(plugincondition.ErrPluginCannotExpressInput,
		fmt.Sprintf("the assembled template %s, past the limit of %s that ARM sets on one template; split the boundary so that its resources deploy as more than one template", what, limit))
}

// longExpression finds the first string, in key order, that ARM reads as an
// expression longer than the limit, and returns its JSON pointer and length.
func longExpression(v any, pointer string) (string, int, bool) {
	switch x := v.(type) {
	case string:
		if length := utf8.RuneCountInString(x); length > maxExpressionChars && armexpr.IsExpression(x) {
			return pointer, length, true
		}
	case map[string]any:
		for _, key := range sortedKeys(x) {
			if p, n, found := longExpression(x[key], pointer+"/"+escapePointer(key)); found {
				return p, n, true
			}
		}
	case []any:
		for i, item := range x {
			if p, n, found := longExpression(item, pointer+"/"+strconv.Itoa(i)); found {
				return p, n, true
			}
		}
	}
	return "", 0, false
}
