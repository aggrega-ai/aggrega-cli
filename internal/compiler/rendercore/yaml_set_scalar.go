// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package rendercore

import (
	"strings"

	frp "github.com/aggrega-ai/aggrega-cli/internal/compiler/featurerecipe_pipeline"
)

// SetTopLevelScalar replaces the scalar a top-level mapping key holds, editing
// only that value's own byte span and returning every other byte of the
// document unchanged — comments, key order, quoting style, indentation and
// trailing content all survive, because nothing outside the span is rewritten.
//
// It exists so a caller that owns one field of a document an author wrote can
// take that field without taking the document. Re-serializing the whole thing
// would be the obvious alternative and it is the wrong one: it discards
// comments, normalizes quoting and reorders nothing predictably, so a file
// promised to a reader as their own would come back subtly not theirs.
//
// It is a thin adapter over the same in-place edit path a pipeline PATCH runs,
// deliberately rather than a second implementation: the node-to-byte-span
// primitive is the delicate part, it already handles the multi-line, quoted and
// flow-context scalar shapes, and a private copy here would be a copy that
// agrees today. The key is addressed as a pointer token, so a key carrying the
// pointer metacharacters is escaped rather than mis-parsed.
//
// It reports an error when the document does not parse, when the key is absent,
// or when the key's value is not a scalar — none of which it repairs, because a
// caller that meant to own a field of a shape it did not expect has a premise
// wrong rather than a value to fix.
func SetTopLevelScalar(body []byte, key, value string, indent int) ([]byte, error) {
	token := strings.ReplaceAll(key, "~", "~0")
	token = strings.ReplaceAll(token, "/", "~1")
	return editDocumentBody(body, []frp.JSONPatchOp{{
		Op:    "replace",
		Path:  "/" + token,
		Value: value,
	}}, indent)
}
