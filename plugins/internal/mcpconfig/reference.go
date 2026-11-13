// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package mcpconfig

import (
	"encoding/json"

	"github.com/aggrega-ai/aggrega-pdk/go/pkg/importer"
)

// Reference reports whether a member's value is a JSON text that is wholly a
// variable reference, as importer.IsVariableReference reads one, and so holds
// no value of its own. A locator writing a value anew with the members inside it
// written empty keeps such a member as written, since the kit's credential
// decision sees only the value as a whole.
func Reference(value json.RawMessage) bool {
	var text string
	return json.Unmarshal(value, &text) == nil && importer.IsVariableReference(text)
}
