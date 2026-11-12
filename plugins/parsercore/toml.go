// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package parsercore

import (
	"github.com/BurntSushi/toml"

	"github.com/aggrega-ai/aggrega-abi-go/pkg/abi"
)

// ParseTOML reads TOML source into a canonical JSON tree. A TOML table becomes a
// JSON object and an array a JSON array; the re-encoded tree carries object keys
// in sorted order. TOML datetimes decode to their RFC 3339 string form. Malformed
// TOML yields an error diagnostic and a null tree rather than a crash.
func ParseTOML(src []byte) abi.ParseOutput {
	var value map[string]any
	if err := toml.Unmarshal(src, &value); err != nil {
		return errorOutput("toml parse: " + err.Error())
	}
	return treeOutput(value)
}
