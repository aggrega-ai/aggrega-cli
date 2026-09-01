// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package guestwire holds the assertions that keep the provider render socket's
// failure transport in step with the halting conditions the provider guest cores
// publish. It declares nothing of its own.
//
// It exists as a package because of where the assertion can be written. The
// property is about the SOCKET rather than about any one guest core — every core
// serving the socket must carry its conditions' identifiers across it — so no
// single core is its subject. The guest cores are internal to plugins/, which
// puts any code that reads all three under plugins/, and there is no aggregating
// package there. That left two alternatives, both worse: one core's test
// importing its two siblings, which reads as arbitrary to whoever opens it, or
// one copy of the same declared-set scan in each of the three, which is the
// dis-unification a shared home removes.
package guestwire
