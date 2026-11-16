// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package armexpr

// Walk visits n and its descendants depth first, a node before its children,
// arguments in author order. A visit that returns false skips that node's
// children.
func Walk(n Node, visit func(Node) bool) {
	if n == nil || !visit(n) {
		return
	}
	switch v := n.(type) {
	case Call:
		for _, a := range v.Args {
			Walk(a, visit)
		}
	case Member:
		Walk(v.Target, visit)
	case Index:
		Walk(v.Target, visit)
		Walk(v.Index, visit)
	}
}
