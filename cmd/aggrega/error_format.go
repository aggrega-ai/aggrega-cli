// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package main

import (
	stderrors "errors"
	"fmt"
	"sort"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/analyzer"
	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
	"github.com/aggrega-ai/aggrega-cli/pkg/resolver"
	"github.com/aggrega-ai/aggrega-cli/pkg/validator"
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
	// Multi-error chains FIRST. The standard library's errors.Join (Go 1.20+)
	// produces an error whose Unwrap returns a slice of underlying errors.
	// errors.As walks the chain and matches the FIRST matching descendant,
	// so a Join'd chain of N CompilerErrors would render only the first
	// without this branch. Iterating Unwrap() []error and rendering each
	// independently surfaces every joined error to the user. Recursion is
	// safe — each sub-error is rendered via formatCompilerError(sub) which
	// hits the terminal CompilerError-rendering branch (or one of the
	// structured-type-conversion branches that ultimately renders a
	// CompilerError) and returns without re-entering this multi-error arm
	// (the per-sub error is not a multi-error itself unless explicitly
	// nested, which is also handled correctly via the recursive call).
	type multiErr interface{ Unwrap() []error }
	if me, ok := err.(multiErr); ok {
		parts := me.Unwrap()
		if len(parts) > 0 {
			rendered := make([]string, 0, len(parts))
			for _, sub := range parts {
				if s := formatCompilerError(sub); s != "" {
					rendered = append(rendered, s)
				}
			}
			if len(rendered) > 0 {
				return strings.Join(rendered, "\n---\n")
			}
		}
	}
	// Detection order: CompilerError FIRST. errors.As walks the chain via
	// Unwrap; a CompilerError that wraps a structured-error type (the result
	// of any ToCompilerError below) would also match errors.As against that
	// structured-error type. Checking CompilerError first ensures the
	// rich-rendering path takes a converted error to its terminal branch
	// without re-entering the structured-error detection arms.
	//
	// For inputs that are NOT already CompilerError, the structured-error
	// detection branches convert via ToCompilerError. The conversion
	// produces a *CompilerError carrying the structured error in its Wrap
	// chain; the recursive call then hits the CompilerError-first arm and
	// renders.
	var ce *cerrors.CompilerError
	if stderrors.As(err, &ce) && ce != nil {
		return renderCompilerError(ce)
	}
	var re *resolver.ResolverError
	if stderrors.As(err, &re) && re != nil {
		return formatCompilerError(re.ToCompilerError())
	}
	var rve *validator.RecipeValidationError
	if stderrors.As(err, &rve) && rve != nil {
		return formatCompilerError(rve.ToCompilerError())
	}
	var wve *validator.WorkloadValidationError
	if stderrors.As(err, &wve) && wve != nil {
		return formatCompilerError(wve.ToCompilerError())
	}
	var ve *validator.ValidationError
	if stderrors.As(err, &ve) && ve != nil {
		return formatCompilerError(ve.ToCompilerError())
	}
	var ae *analyzer.AnalysisError
	if stderrors.As(err, &ae) && ae != nil {
		return formatCompilerError(ae.ToCompilerError())
	}
	return err.Error()
}

// renderCompilerError formats a *CompilerError per the documented
// multi-line shape: category tag in brackets, source pointer, message,
// optional expression, optional pillar snapshot. Extracted from the
// formatCompilerError detection-and-render combo so the rendering stays
// pure (no chain-walking) and the caller controls when rendering fires.
func renderCompilerError(ce *cerrors.CompilerError) string {
	if ce == nil {
		return ""
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
