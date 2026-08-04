// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

// Cancellation is the caller's answer to one question, asked once per outer
// iteration of a traversal: should this walk stop, and why? Returning nil means
// carry on. A nil Cancellation never stops a walk, so a caller with no
// cancellation concern supplies nothing rather than a no-op function.
//
// # Why a bare function and not a broader carrier
//
// A traversal needs a check point and nothing else — no deadline, no clock, no
// request-scoped values, no ambient state of any kind. A one-bit question asked
// repeatedly is answerable by a function of no arguments, so this type states
// exactly that and is structurally unable to carry more. The narrowness is the
// mechanism rather than a simplification: the fence is in the type, not in a
// convention about how the type is used.
//
// It returns an error rather than a bool because the reason belongs to the
// caller. A traversal that stopped hands back whatever the signal returned, so
// the caller reads its own reason and the framework invents none.
//
// A caller already participating in cancellation wires it in one expression,
// because a context.Context's Err method is already a value of this type:
//
//	objections, err := RunOnNodes(ctx.Err, nodes, view, check)
//
// The context stays with the caller. No traversal takes one, so a deadline, a
// value or any other ambient state a context can carry has no route in.
//
// # Granularity, and its bound
//
// A traversal reads the signal as the first statement of its outermost loop
// body, so a walk stops within one outer iteration of the signal turning
// non-nil. That bound is what it says and no more: for a node-rooted kind one
// outer iteration is one node's subjects, and for [RunOnBoundaryProviderPaths]
// it is one boundary's providers and paths. Inner loops carry no additional
// read.
//
// # Cancellation is not a host power
//
// It is never declared on a check's capability axis. A host power is something a
// check's predicate reads in order to decide; this is something the caller tells
// the traversal, and no check ever sees it.
type Cancellation func() error

// err reports the cancellation the caller signalled, and nil when no signal was
// supplied. The nil case lives here rather than at each of the sixteen reads so
// that an uncancellable walk is the default a caller gets for free.
func (c Cancellation) err() error {
	if c == nil {
		return nil
	}
	return c()
}
