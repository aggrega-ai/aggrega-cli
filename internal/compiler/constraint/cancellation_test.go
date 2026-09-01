// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package constraint

import (
	"errors"
	"strconv"
	"testing"
)

// The cancellation tests.
//
// One property is under test and it is easy to appear to prove without proving:
// a traversal reads the cancellation signal inside its loop, so a long walk stops
// promptly rather than at the end of the enumeration.
//
// An error return alone does not establish it. A walk that enumerated everything
// and reported the cancellation at the end returns exactly the same error as one
// that stopped on the second iteration, so every assertion here is a count: how
// many subjects the check was invoked on, and how many times the signal was read.
// Over a thousand-instance enumeration the two shapes are then separated by three
// orders of magnitude rather than by a judgement.
//
// The counts also pin the granularity, which is the standard the framework is
// held to: the analyzer's validators read their cancellation as the first
// statement of their outermost loop body, once per outer iteration, and these
// traversals do the same. Read once per outer iteration means the read count over
// an uncancelled walk equals the instance count — a per-pass check would read
// once — and it means a signal that turns non-nil after k invocations stops the
// walk having invoked exactly k. Both are measured below rather than asserted.

var errWalkCancelled = errors.New("walk cancelled")

// largeCancellationFixture is an enumeration big enough that a full walk and an
// early abort cannot be confused: n nodes, n edges, and n boundaries each with
// one provider emitting one path, so every traversal shape below performs exactly
// n outer iterations and exactly n invocations.
func largeCancellationFixture(n int) traversalFixture {
	f := traversalFixture{
		providers:         map[string][]string{},
		emittedPaths:      map[string][]string{},
		assemblyProviders: map[string][]string{},
	}
	for i := range n {
		name := "n" + strconv.Itoa(i)
		node := NodeInstance{
			ID:                   name,
			SourceFile:           "aggregate.yaml",
			AuthoredName:         name,
			SubsystemPath:        "default",
			SynthesizedSubsystem: true,
		}
		f.nodes = append(f.nodes, node)
		f.edges = append(f.edges, EdgeInstance{Node: node, Socket: "db", Alias: "primary"})
		f.boundaries = append(f.boundaries, AuthoredCoordinate{Value: name, SourceFile: "release.yaml"})
		f.providers[fixtureKey(name)] = []string{"helm"}
		f.emittedPaths[fixtureKey(name, "helm")] = []string{"a.yaml"}
		f.assemblyPaths = append(f.assemblyPaths,
			AuthoredCoordinate{Value: name, SourceFile: "release.yaml"})
		f.assemblyProviders[fixtureKey(name)] = []string{"helm"}
	}
	return f
}

// countingCheck raises one objection per invocation, as the transcript check
// does, and records the invocation. It raises rather than returning nil so that a
// cancelled walk is shown to discard objections it had already collected — a
// check that raised nothing would make that assertion vacuous.
func countingCheck[S Subject](visit func()) Check[traversalView, S] {
	return Check[traversalView, S]{
		Sentinel: errRuleBroken,
		Severity: SeverityFatal,
		Run: func(_ traversalView, s S) []Objection {
			visit()
			return []Objection{s.Raise(errRuleBroken)}
		},
	}
}

// cancellationShape is one of the loop shapes every traversal is built from,
// driven over the large fixture.
//
// One case per LOOP rather than one per entry point, deliberately: the kinds
// reached within a node share one node walk and the kinds rooted at a boundary
// share one boundary walk, so a case per entry point would measure a handful of
// loops many times over and prove nothing extra. The shapes are enumerated by the
// loop they own, which is why there are fewer of them than there are traversals
// and why no count of either is written here. That every entry point honours the
// signal is a separate property, established over the whole generated kind set by
// [TestEveryPullTraversalHonoursTheCancellationSignal].
type cancellationShape struct {
	name  string
	outer string
	run   func(Cancellation, traversalFixture, func()) ([]Objection, error)
}

func cancellationShapes() []cancellationShape {
	return []cancellationShape{
		{
			name:  "RunOnNodes",
			outer: "node",
			run: func(cancelled Cancellation, f traversalFixture, visit func()) ([]Objection, error) {
				return RunOnNodes(cancelled, f, traversalView{}, countingCheck[NodeSubject](visit))
			},
		},
		{
			name:  "RunOnEdges",
			outer: "edge",
			run: func(cancelled Cancellation, f traversalFixture, visit func()) ([]Objection, error) {
				return RunOnEdges(cancelled, f, traversalView{}, countingCheck[EdgeSubject](visit))
			},
		},
		{
			name:  "RunOnAssemblyPathProviders",
			outer: "assembly path",
			run: func(cancelled Cancellation, f traversalFixture, visit func()) ([]Objection, error) {
				return RunOnAssemblyPathProviders(
					cancelled, f, f, traversalView{},
					countingCheck[AssemblyPathProviderSubject](visit),
				)
			},
		},
	}
}

// cancellationInstances is the size of the enumeration every measurement below
// runs over, and cancellationBoundDivisor is the factor an aborted walk's visited
// count must sit under for the abort to be distinguishable from a full walk.
const (
	cancellationInstances    = 1000
	cancellationBoundDivisor = 10
)

// TestATraversalStopsOnTheIterationAfterTheSignalTurns is the mid-walk assertion:
// a signal that turns non-nil once the check has been invoked trigger times stops
// the walk having invoked it exactly trigger times, out of a thousand.
//
// The exact equality is the point. Bounding the visited count would prove the
// walk stopped early; equality proves *where* — the very next outer iteration —
// which is what per-iteration granularity means. The looser bound is asserted
// alongside it because it is the property the equality would silently stop
// establishing if the fixture ever shrank.
func TestATraversalStopsOnTheIterationAfterTheSignalTurns(t *testing.T) {
	t.Parallel()

	fixture := largeCancellationFixture(cancellationInstances)

	for _, shape := range cancellationShapes() {
		for _, trigger := range []int{0, 1, 3, 17} {
			t.Run(shape.name+"/after-"+strconv.Itoa(trigger), func(t *testing.T) {
				t.Parallel()

				visited, reads := 0, 0
				cancelled := Cancellation(func() error {
					reads++
					if visited >= trigger {
						return errWalkCancelled
					}
					return nil
				})

				objections, err := shape.run(cancelled, fixture, func() { visited++ })

				if !errors.Is(err, errWalkCancelled) {
					t.Fatalf("the traversal returned %v; want the signal's own error.\n"+
						"INVARIANT: a stopped walk hands back whatever the cancellation signal returned.\n"+
						"CONSEQUENCE: the reason a walk stopped belongs to the caller. A framework that "+
						"substituted its own would make a caller unable to tell a cancellation it asked for "+
						"from a failure it did not.", err)
				}

				if visited != trigger {
					t.Errorf("the check was invoked %d time(s) before the walk stopped; want exactly %d, "+
						"out of %d.\n"+
						"INVARIANT: the signal is read as the first statement of the outermost loop body, so "+
						"a walk stops on the iteration after the signal turns non-nil.\n"+
						"CONSEQUENCE: too many and the read is not per-%s — at the limit it is per-pass, which "+
						"bounds the wait by the duration of the whole walk and is the thing being cancelled. "+
						"Too few and the walk stopped before the signal asked it to.",
						visited, trigger, cancellationInstances, shape.outer)
				}

				if reads != trigger+1 {
					t.Errorf("the signal was read %d time(s); want %d — once per %s walked, plus the read "+
						"that stopped it.\n"+
						"INVARIANT: exactly one read per outer iteration.\n"+
						"CONSEQUENCE: the read count is the only direct evidence of the granularity. Fewer "+
						"reads than iterations means some iterations are uninterruptible; more means the "+
						"signal is consulted somewhere it was not designed to be.",
						reads, trigger+1, shape.outer)
				}

				if visited*cancellationBoundDivisor >= cancellationInstances {
					t.Errorf("the walk visited %d of %d subject(s), which is not bounded well below the "+
						"total, so this result no longer distinguishes an early abort from a full walk that "+
						"reported the cancellation at the end", visited, cancellationInstances)
				}

				if len(objections) != 0 {
					t.Errorf("the stopped walk returned %d objection(s); want none.\n"+
						"INVARIANT: a cancelled walk returns no objections.\n"+
						"CONSEQUENCE: it collected some before it stopped, and handing back a subset of the "+
						"violations over a subset of the instances would read as all of them over all of "+
						"them.", len(objections))
				}

				t.Logf("cancellation: %s visited %d of %d %s(s) and read the signal %d time(s)",
					shape.name, visited, cancellationInstances, shape.outer, reads)
			})
		}
	}
}

// TestATraversalReadsTheSignalOncePerOuterIteration separates per-iteration from
// per-pass by measurement rather than by inspection: over a thousand instances an
// uncancelled walk reads a live signal a thousand times. A traversal that checked
// once before or after its loop would read it once, and would pass every
// error-shaped assertion in this file.
func TestATraversalReadsTheSignalOncePerOuterIteration(t *testing.T) {
	t.Parallel()

	fixture := largeCancellationFixture(cancellationInstances)

	for _, shape := range cancellationShapes() {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()

			visited, reads := 0, 0
			cancelled := Cancellation(func() error {
				reads++
				return nil
			})

			objections, err := shape.run(cancelled, fixture, func() { visited++ })

			if err != nil {
				t.Fatalf("the traversal returned %v over a walk the signal never stopped; want no error", err)
			}
			if visited != cancellationInstances {
				t.Fatalf("the check was invoked %d time(s); want %d, so the fixture no longer drives one "+
					"invocation per %s and the read count below measures nothing",
					visited, cancellationInstances, shape.outer)
			}
			if reads != cancellationInstances {
				t.Errorf("the signal was read %d time(s) over %d %s(s); want one read each.\n"+
					"INVARIANT: the read is inside the loop, once per outer iteration.\n"+
					"CONSEQUENCE: a single read over a walk this size is a check outside the loop, which "+
					"honours cancellation only at the moment the walk it was meant to interrupt has already "+
					"finished.", reads, cancellationInstances, shape.outer)
			}
			if len(objections) != cancellationInstances {
				t.Errorf("the completed walk returned %d objection(s); want %d — a live signal that never "+
					"turns must not change what a walk produces", len(objections), cancellationInstances)
			}

			t.Logf("granularity: %s read the signal %d time(s) over %d %s(s) — one read per outer "+
				"iteration, against 1 for a per-pass check", shape.name, reads, visited, shape.outer)
		})
	}
}

// TestAnUncancellableTraversalWalksTheWholeEnumeration pins the zero value: a nil
// signal is never read and never stops a walk, so a caller with no cancellation
// concern supplies nothing rather than a no-op function.
func TestAnUncancellableTraversalWalksTheWholeEnumeration(t *testing.T) {
	t.Parallel()

	fixture := largeCancellationFixture(cancellationInstances)

	for _, shape := range cancellationShapes() {
		t.Run(shape.name, func(t *testing.T) {
			t.Parallel()

			visited := 0
			objections, err := shape.run(nil, fixture, func() { visited++ })

			if err != nil {
				t.Fatalf("the traversal returned %v with no cancellation signal supplied; want no error", err)
			}
			if visited != cancellationInstances {
				t.Errorf("the check was invoked %d time(s); want %d — a nil signal must not stop a walk",
					visited, cancellationInstances)
			}
			if len(objections) != cancellationInstances {
				t.Errorf("the walk returned %d objection(s); want %d", len(objections), cancellationInstances)
			}
		})
	}
}

// TestEveryPullTraversalHonoursTheCancellationSignal is the completeness half:
// every entry point stops when the signal is already non-nil at its first read.
//
// It rides the transcript table rather than a list of its own, because that table
// is itself held against the generated kind set by
// [TestEveryPullKindsTraversalIsExercised] — so an entry point added without a
// cancellation read cannot be absent from here without also failing there. A
// hand-written list beside a generated set is the arrangement that reads as
// complete while missing one.
func TestEveryPullTraversalHonoursTheCancellationSignal(t *testing.T) {
	t.Parallel()

	fixture := newTraversalFixture()

	for _, tc := range traversalCases() {
		t.Run(string(tc.kind), func(t *testing.T) {
			t.Parallel()

			// Calibration: over an enumeration this traversal never enters, a
			// stopped walk and a completed one both return nothing, and the
			// assertions below would pass without the signal being read at all.
			if len(tc.want) == 0 {
				t.Fatalf("the case expects no invocation over the fixture, so a stopped walk is "+
					"indistinguishable from a completed one and nothing here certifies %s", tc.kind)
			}

			reads := 0
			objections, err := tc.run(func() error {
				reads++
				return errWalkCancelled
			}, fixture, traversalView{})

			if !errors.Is(err, errWalkCancelled) {
				t.Errorf("the traversal returned %v; want the signal's own error.\n"+
					"INVARIANT: every enumerated kind's traversal reads the cancellation signal.\n"+
					"CONSEQUENCE: one entry point that ignores it honours cancellation nowhere a caller "+
					"can predict, and the caller cannot tell which entry point it reached.", err)
			}
			if reads != 1 {
				t.Errorf("the signal was read %d time(s) before the walk stopped; want 1", reads)
			}
			if len(objections) != 0 {
				t.Errorf("the stopped walk returned %d objection(s); want none", len(objections))
			}
		})
	}
}
