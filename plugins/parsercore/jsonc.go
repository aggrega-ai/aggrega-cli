// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsercore

import (
	"github.com/aggrega-ai/aggrega-abi/go/pkg/abi"

	"github.com/aggrega-ai/aggrega-cli/plugins/internal/jsonc"
)

// ParseJSONC reads JSON with comments into a canonical JSON tree: its line and
// block comments and its trailing commas are written as spaces (jsonc.Strip) and
// the text is then decoded by ParseJSON, so a document yields exactly the tree
// ParseJSON yields for the same document without them, and a refusal names the
// line and column the source has there. A leading UTF-8 byte-order mark is
// dropped, so a refusal counts its line and column from the first character
// after the mark. A comment opened with /* and never closed is left in place, so
// the document is refused rather than read cut short.
func ParseJSONC(src []byte) abi.ParseOutput {
	return ParseJSON(jsonc.Strip(src))
}
