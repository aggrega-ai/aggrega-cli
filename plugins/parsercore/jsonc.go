// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsercore

import (
	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/jsonc"
)

// ParseJSONC reads JSON with comments into a canonical JSON tree: its line and
// block comments and its trailing commas are written as spaces (jsonc.Strip) and
// the text is then decoded as ParseJSON decodes it, so a document yields exactly
// the tree ParseJSON yields for the same document without them. A refusal at a
// character names the line and the column the source has there, the columns
// counted in characters: a raw line break inside a string is refused where the
// string breaks. A leading UTF-8 byte-order mark is dropped, so a refusal counts
// its line and column from the first character after the mark. A comment opened
// with /* and never closed is left in place, so the document is refused rather
// than read cut short.
func ParseJSONC(src []byte) abi.ParseOutput {
	text := jsonc.Strip(src)
	value, err := decodeJSON(text)
	if err != nil {
		return errorOutput("json parse: " + err.Error() + position(text, err))
	}
	return treeOutput(value)
}
