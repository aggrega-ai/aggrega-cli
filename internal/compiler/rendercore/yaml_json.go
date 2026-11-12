// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package rendercore

import (
	"encoding/json"

	"github.com/aggrega-ai/aggrega-cli/internal/compiler/parse"
	"github.com/aggrega-ai/aggrega-cli/pkg/types"
)

// YAMLBodyToJSON converts one YAML document body to a JSON RawMessage,
// preserving object key order through OrderedMap so the wire payload is
// deterministic. The stock codec uses it to build a unit's tree-view.
func YAMLBodyToJSON(body []byte) (json.RawMessage, error) {
	var om types.OrderedMap[any]
	if err := parse.TemplateOutput(body, &om); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(om)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
