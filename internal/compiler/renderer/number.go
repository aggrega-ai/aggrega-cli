// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import "encoding/json"

// asNumber coerces template-output numeric values to float64. Accepts the
// numeric kinds that yaml.v3 and encoding/json may produce when decoding into
// interface{}: yaml.v3 yields int (and int64 for large literals); encoding/json
// yields float64 (or json.Number when UseNumber is set). Returns (0, false) on
// non-numeric input.
//
// This shim exists because the template-output parse contract switched from
// json.Unmarshal to yaml.Unmarshal: a Recipe whose valuesMapping emits
// {"replicas": 3} now decodes the integer as int rather than float64.
// Downstream consumers that previously type-asserted .(float64) on
// template-output map values must coerce through this helper.
func asNumber(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case int32:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
