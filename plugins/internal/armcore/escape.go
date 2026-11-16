// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armcore

import (
	"strings"
)

// ArmLiteral returns s as literal text inside an ARM template. A string that
// begins with an opening bracket and ends with a closing bracket would be read as
// an expression, so one extra opening bracket is written before it; ARM reads a
// leading double bracket as a literal bracket. Any other string is returned
// unchanged.
func ArmLiteral(s string) string {
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		return "[" + s
	}
	return s
}

// formatLiteralRewriter doubles the characters the format() function reads
// specially inside its first argument: each single quote, which the ARM string
// literal reads, and each brace, which the composite format string reads. It
// rewrites in one pass, so no doubled character is doubled again.
var formatLiteralRewriter = strings.NewReplacer("'", "''", "{", "{{", "}", "}}")

// FormatLiteral returns s as literal text inside the first argument of
// format(), before the ARM string literal quotes are added. Each single quote is
// written as a doubled single quote, and each brace as a doubled brace.
//
// The doubled brace follows the composite format rule of the .NET
// System.String.Format method, which the template functions string reference
// names as the format options format() uses:
// https://learn.microsoft.com/en-us/azure/azure-resource-manager/templates/template-functions-string
// That page's format entry does not restate the brace rule itself.
func FormatLiteral(s string) string {
	return formatLiteralRewriter.Replace(s)
}

// StringLiteral returns s as an ARM string literal: the text in single quotes,
// with each inner single quote written as a doubled single quote.
func StringLiteral(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
