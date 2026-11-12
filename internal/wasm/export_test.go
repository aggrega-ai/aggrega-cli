// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package wasm

import "context"

// Call invokes funcName on the plugin under the compile-time exit-code policy.
// It exists for tests only: a test drives describe() and arbitrary fixture
// exports directly, while production code outside this package reaches a guest
// only through InvokeExport, for an export the plugin-type registry lists, or
// through the entry points for an export authored content names. A file ending
// in _test.go is compiled into test builds alone, so this name is unreachable
// from the compiler itself.
func (p *Plugin) Call(ctx context.Context, funcName string, input []byte) ([]byte, error) {
	return p.call(ctx, funcName, input)
}
