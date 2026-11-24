// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package cloudimport

// Order returns the nodes of g with every producer before its consumers, ties
// broken by ID, so one graph gives one order whatever order its nodes are held
// in. A node's references to itself are not waited on. Where every node left
// waits on another, a cycle holds them: of the cycles no node outside them
// feeds, the node with the smallest ID is placed next and the rest follow by
// the same rule. An edge to an ID that names no node of g is not waited on.
func Order(g *Graph) []*Node {
	nodes := byID(g)
	index := make(map[string]int, len(nodes))
	for i, n := range nodes {
		if _, held := index[n.ID]; !held {
			index[n.ID] = i
		}
	}
	producers := make([][]int, len(nodes))
	for _, e := range edges(nodes) {
		from, ok := index[e.From]
		to, held := index[e.To]
		if ok && held && from != to {
			producers[from] = append(producers[from], to)
		}
	}

	placed := make([]bool, len(nodes))
	out := make([]*Node, 0, len(nodes))
	for len(out) < len(nodes) {
		next := firstReady(producers, placed)
		if next < 0 {
			next = cycleBreak(producers, placed)
		}
		placed[next] = true
		out = append(out, nodes[next])
	}
	return out
}

// firstReady returns the first node not yet placed whose producers are all
// placed, or -1 where none is.
func firstReady(producers [][]int, placed []bool) int {
	for i := range producers {
		if placed[i] {
			continue
		}
		ready := true
		for _, p := range producers[i] {
			if !placed[p] {
				ready = false
				break
			}
		}
		if ready {
			return i
		}
	}
	return -1
}

// cycleBreak returns the node to place where every node not yet placed waits
// on another: the first node of a cycle that waits on no node outside it. The
// cycles are the strongly connected components of the nodes not yet placed;
// such a component always exists, since the components form no cycle.
func cycleBreak(producers [][]int, placed []bool) int {
	comp := components(producers, placed)
	fed := make([]bool, len(producers))
	for i := range producers {
		if placed[i] {
			continue
		}
		for _, p := range producers[i] {
			if !placed[p] && comp[p] != comp[i] {
				fed[comp[i]] = true
			}
		}
	}
	first := -1
	for i := range producers {
		if placed[i] {
			continue
		}
		if !fed[comp[i]] {
			return i
		}
		if first < 0 {
			first = i
		}
	}
	return first
}

// components returns the strongly connected component of each node not yet
// placed, by Tarjan's algorithm over the producer edges between them.
func components(producers [][]int, placed []bool) []int {
	n := len(producers)
	visit, low, comp := make([]int, n), make([]int, n), make([]int, n)
	for i := range visit {
		visit[i], comp[i] = -1, -1
	}
	onStack := make([]bool, n)
	var stack []int
	next, count := 0, 0
	var walk func(v int)
	walk = func(v int) {
		visit[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range producers[v] {
			switch {
			case placed[w]:
			case visit[w] < 0:
				walk(w)
				low[v] = min(low[v], low[w])
			case onStack[w]:
				low[v] = min(low[v], visit[w])
			}
		}
		if low[v] != visit[v] {
			return
		}
		for {
			w := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[w] = false
			comp[w] = count
			if w == v {
				break
			}
		}
		count++
	}
	for v := 0; v < n; v++ {
		if !placed[v] && visit[v] < 0 {
			walk(v)
		}
	}
	return comp
}
