// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"fmt"
	"strconv"
	"strings"

	abi "github.com/aggrega-ai/aggrega-abi/go/pkg/abi"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/rendercore"
	"github.com/aggrega-ai/aggrega-cli/internal/compiler/renderctx"
	"github.com/aggrega-ai/aggrega-cli/internal/plugincondition"
	"github.com/aggrega-ai/aggrega-cli/plugins/internal/armcore/armexpr"
)

// placeholderDigits is the number of lowercase hexadecimal digits that follow
// rendercore.CrossBoundaryLinkSentinelPrefix in a delivery placeholder. It is read
// off a placeholder the host's own constructor builds, so the shape has one home.
var placeholderDigits = len(rendercore.CrossBoundaryLinkSentinel("", "", "", "")) - len(rendercore.CrossBoundaryLinkSentinelPrefix)

// composeLiterals rewrites each literal string of the level that holds a
// delivered value into a format() expression: its format string is the literal
// text around the values, escaped by FormatLiteral, and its arguments are the
// values in order. A delivered value inside literal text is either a delivery
// placeholder the host stamped, which becomes a string literal argument, or a
// bracketed span that parses as a complete call to a template function, or to a
// namespace.member function the level declares under functions, with any member
// and index accessors after it. ARM evaluates a bracketed call only as a whole
// string, so a call inside literal text is a value written there. Other
// bracketed text, such as array[0] or [a-z], is literal and stays as written, as
// does a string that opens with a doubled bracket and a string that is a
// placeholder whole. Each rewrite is reported by an info diagnostic naming the
// contributor and the JSON pointer.
//
// A string ARM reads as an expression is never rewritten, and its string
// literals are not read for calls: text meant literally keeps a bracketed call
// by being written whole as an expression, such as [format('...')]. A placeholder
// inside an expression, whole or inside literal text, halts wrapping
// ErrPluginValueNotRepresentable naming the contributor and the JSON pointer,
// since ARM cannot read a value delivered from another deployment there.
func (l *level) composeLiterals() ([]renderctx.Diagnostic, error) {
	functions := l.declaredFunctions()
	var diags []renderctx.Diagnostic
	err := l.eachString(func(s site, text string) (string, error) {
		if armexpr.IsExpression(text) {
			n, err := s.parse(text)
			if err != nil {
				return "", err
			}
			if expressionHoldsPlaceholder(n) {
				return "", placeholderInsideExpression(s)
			}
			return text, nil
		}
		if strings.HasPrefix(text, "[[") {
			return text, nil
		}
		values, err := deliveredValues(s, text, functions)
		if err != nil {
			return "", err
		}
		if len(values) == 0 || len(values) == 1 && values[0].start == 0 && values[0].end == len(text) {
			return text, nil
		}
		diags = append(diags, renderctx.Diagnostic{
			Severity:  abi.SeverityInfo,
			SourceRef: s.pointer(),
			Message: fmt.Sprintf("arm-provider: %s, a value written inside literal text is written as a format() expression, so that ARM evaluates it; to keep the text literal, write the whole string as an expression, such as [format('...')]",
				s.named()),
		})
		return formatExpression(text, values), nil
	})
	if err != nil {
		return nil, err
	}
	return diags, nil
}

// deliveredValue is one value found inside literal text: the bytes it spans and
// the format() argument written in its place.
type deliveredValue struct {
	start, end int
	argument   string
}

// deliveredValues returns the delivered values inside literal text, in order.
// functions holds the lowercase namespace.member names the level declares.
func deliveredValues(s site, text string, functions map[string]bool) ([]deliveredValue, error) {
	if !strings.Contains(text, "[") && !strings.Contains(text, rendercore.CrossBoundaryLinkSentinelPrefix) {
		return nil, nil
	}
	var values []deliveredValue
	for i := 0; i < len(text); {
		if end, ok := placeholderAt(text, i); ok {
			values = append(values, deliveredValue{start: i, end: end, argument: StringLiteral(text[i:end])})
			i = end
			continue
		}
		if n, end, ok := callAt(text, i, functions); ok {
			if expressionHoldsPlaceholder(n) {
				return nil, placeholderInsideExpression(s)
			}
			values = append(values, deliveredValue{start: i, end: end, argument: armexpr.Print(n)})
			i = end
			continue
		}
		i++
	}
	return values, nil
}

// formatExpression writes text as a format() expression with each delivered
// value an argument in its place.
func formatExpression(text string, values []deliveredValue) string {
	var format, args strings.Builder
	prev := 0
	for i, v := range values {
		format.WriteString(FormatLiteral(text[prev:v.start]))
		format.WriteString("{" + strconv.Itoa(i) + "}")
		args.WriteString(", ")
		args.WriteString(v.argument)
		prev = v.end
	}
	format.WriteString(FormatLiteral(text[prev:]))
	return "[format('" + format.String() + "'" + args.String() + ")]"
}

// declaredFunctions returns the lowercase namespace.member name of each
// user-defined function the level declares.
func (l *level) declaredFunctions() map[string]bool {
	return functionNames(l.sections[sectionFunctions])
}

// functionNames returns the lowercase form of each namespace.member name that
// keys functions. ARM reads function names without regard to case.
func functionNames[V any](functions map[string]V) map[string]bool {
	out := map[string]bool{}
	for _, name := range sortedKeys(functions) {
		out[strings.ToLower(name)] = true
	}
	return out
}

// callAt returns the call a bracketed span opening at text[i] holds and the
// offset just past the span, where the span parses as a complete call to a
// template function, or to a user-defined function in functions, with any
// member and index accessors after it. ARM evaluates a bracketed call only as a
// whole string, so such a span inside literal text is a value written there.
// Other bracketed text, such as array[0] or [a-z], is not one.
func callAt(text string, i int, functions map[string]bool) (armexpr.Node, int, bool) {
	if text[i] != '[' {
		return nil, 0, false
	}
	n, end, ok := armexpr.ParseSpan(text, i)
	if !ok || !callsKnownFunction(n, functions) {
		return nil, 0, false
	}
	return n, end, true
}

// callsKnownFunction reports whether the call at the root of n, beneath any
// member and index accessors, is a template function or a user-defined function
// in functions.
func callsKnownFunction(n armexpr.Node, functions map[string]bool) bool {
	for {
		switch v := n.(type) {
		case armexpr.Call:
			if v.Namespace == "" {
				return armexpr.IsTemplateFunction(v.Name)
			}
			return functions[strings.ToLower(v.Namespace+"."+v.Name)]
		case armexpr.Member:
			n = v.Target
		case armexpr.Index:
			n = v.Target
		default:
			return false
		}
	}
}

// placeholderAt returns the offset just past a delivery placeholder that begins
// at text[i]: the prefix followed by its count of lowercase hexadecimal digits.
// The prefix followed by anything else is not a delivery placeholder.
func placeholderAt(text string, i int) (int, bool) {
	prefix := rendercore.CrossBoundaryLinkSentinelPrefix
	if !strings.HasPrefix(text[i:], prefix) {
		return 0, false
	}
	end := i + len(prefix) + placeholderDigits
	if end > len(text) {
		return 0, false
	}
	for j := i + len(prefix); j < end; j++ {
		c := text[j]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return 0, false
		}
	}
	return end, true
}

// holdsPlaceholder reports whether text holds a delivery placeholder anywhere.
func holdsPlaceholder(text string) bool {
	for i := 0; ; i++ {
		j := strings.Index(text[i:], rendercore.CrossBoundaryLinkSentinelPrefix)
		if j < 0 {
			return false
		}
		i += j
		if _, ok := placeholderAt(text, i); ok {
			return true
		}
	}
}

// expressionHoldsPlaceholder reports whether a string literal of n holds a
// delivery placeholder.
func expressionHoldsPlaceholder(n armexpr.Node) bool {
	found := false
	armexpr.Walk(n, func(node armexpr.Node) bool {
		if str, ok := node.(armexpr.String); ok && holdsPlaceholder(str.Value) {
			found = true
		}
		return !found
	})
	return found
}

// placeholderInsideExpression is the halt for a delivery placeholder written
// inside an expression.
func placeholderInsideExpression(s site) error {
	return refuse(plugincondition.ErrPluginValueNotRepresentable,
		fmt.Sprintf("%s, a value delivered from another deployment is written inside an expression, where ARM cannot read it; write the delivered value whole as the string, or inside literal text, which is written as a format() expression",
			s.named()))
}
