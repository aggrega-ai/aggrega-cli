// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	stderrors "errors"
	"fmt"
	"sort"
	"strings"

	cerrors "github.com/cognitio/aggrega-cli/pkg/errors"
)

// formatCompilerError renders a CompilerError in the documented multi-line
// shape (category tag in brackets, source pointer, message, optional
// expression, optional pillar snapshot). When err is not (or does not wrap)
// a *CompilerError, falls back to err.Error() so non-CLI callers and
// non-categorised errors retain their existing presentation.
func formatCompilerError(err error) string {
	if err == nil {
		return ""
	}
	var ce *cerrors.CompilerError
	if !stderrors.As(err, &ce) || ce == nil {
		return err.Error()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[%s]", ce.Category)
	if src := ce.Source.String(); src != "" {
		fmt.Fprintf(&b, " %s", src)
	}
	if ce.Message != "" {
		fmt.Fprintf(&b, "\n  %s", ce.Message)
	}
	if ce.Expression != "" {
		fmt.Fprintf(&b, "\n  expression: %s", ce.Expression)
	}
	if len(ce.Context) > 0 {
		fmt.Fprintf(&b, "\n  active pillars:")
		keys := make([]string, 0, len(ce.Context))
		for k := range ce.Context {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(&b, "\n    %s", k)
		}
	}
	return b.String()
}
