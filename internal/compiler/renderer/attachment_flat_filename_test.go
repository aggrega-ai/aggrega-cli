// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package renderer

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegmenttest"
)

// Where a provider declares that its target reads only the files sitting
// directly in a directory, an attachment's coordinates move out of directory
// segments and into a filename prefix. These are the proofs that the move keeps
// two contributors apart.
//
// # Why the derivation has to carry the guarantee by itself
//
// Nothing downstream is watching. A component-scope attachment's files merge
// into the bucket of the node hosting them, and that bucket is stamped with ONE
// producer label computed per node — so two attachments on one component reach
// the assembled-file set as a single producer, and the rule that refuses one
// output path written by more than one contributor cannot fire on them. The
// losing file is then dropped by a plain map assignment. So a collision here
// would be SILENT: no halt, no diagnostic, a file simply absent from the
// output.
//
// That is why these assertions are over a corpus chosen for the join's edges
// rather than over realistic-looking coordinates, and why they run the whole
// cross product rather than a sample.

// flatTails are the file names the derivation is exercised against. Each is a
// name a rendering provider could plausibly write, and the last three are chosen
// to attack the boundary between the prefix and the name rather than to look
// realistic: one beginning with the separator's own character, one carrying it
// inside, and one that is a bare separator run.
//
// A name is held fixed while the coordinates vary, because that is the property
// under test: two contributors that emit THE SAME file name must not meet. Two
// different names are free to collide with each other and no derivation here
// prevents it, since the name arrives as its provider wrote it.
var flatTails = []string{"manifest.yaml", "x.yaml", "c-x.yaml", "-x.yaml", "--x.yaml"}

// attachmentFlatCoordinateLists returns every coordinate list the two attachment
// scopes can present, over the segment corpus: one coordinate as a
// boundary-scope attachment presents (the instance alone, because it sits inside
// the boundary already), and two as a component-scope attachment presents (the
// host and the instance).
//
// Both arities go into ONE list, and the injectivity assertion below holds them
// in one map rather than one map per arity. A per-arity assertion would leave
// the scopes untested against each other, and the two scopes' output can land in
// one directory — so a one-coordinate list reaching the same filename as a
// two-coordinate list is a real collision and has to be refused here.
func attachmentFlatCoordinateLists() [][]string {
	corpus := namesegmenttest.Corpus('-')
	out := make([][]string, 0, len(corpus)+len(corpus)*len(corpus))
	for _, instance := range corpus {
		out = append(out, []string{instance})
	}
	for _, host := range corpus {
		for _, instance := range corpus {
			out = append(out, []string{host, instance})
		}
	}
	return out
}

// TestAttachmentFlatFilenameIsInjectiveAcrossCoordinatesAndScopes establishes
// the guarantee the flat placement rests on: for one file name, distinct
// contributor coordinates always reach distinct filenames.
//
// It runs the whole cross product of the shared segment corpus at both arities
// rather than a realistic sample, because the corpus is the thing closed under
// moving a hyphen across a join — the case a varied-looking sample misses
// entirely, since every coordinate can be distinct and every filename still
// distinct while the property the join carries goes untested.
func TestAttachmentFlatFilenameIsInjectiveAcrossCoordinatesAndScopes(t *testing.T) {
	t.Parallel()

	lists := attachmentFlatCoordinateLists()
	if len(lists) < 100 {
		t.Fatalf("coordinate corpus is %d lists, too small to establish the property", len(lists))
	}
	for _, tail := range flatTails {
		t.Run(tail, func(t *testing.T) {
			t.Parallel()

			seen := make(map[string][]string, len(lists))
			for _, coords := range lists {
				name := attachmentOutputPrefix(coords...) + tail
				if prior, clash := seen[name]; clash {
					t.Fatalf("coordinates %q and %q both reach the filename %q", prior, coords, name)
				}
				seen[name] = coords
			}
			if len(seen) != len(lists) {
				t.Fatalf("built %d filenames from %d coordinate lists; each list must reach its own filename",
					len(seen), len(lists))
			}
			t.Logf("%d coordinate lists reached %d distinct filenames", len(lists), len(seen))
		})
	}
}

// TestAttachmentFlatFilenameSeparatesTheSingleHyphenCollision pins the one
// worked pair that decides the width of the boundary between the prefix and the
// file's own name, and it asserts BOTH directions: that the derivation separates
// the pair, and that a one-character boundary would not.
//
// The second half is what keeps the first from being decoration. An assertion
// whose failure mode nobody has demonstrated proves nothing about the choice it
// encodes — so the collision a single hyphen produces is computed here and
// required to be a collision, which makes the reason for the two-character
// boundary executable rather than a claim in a comment.
func TestAttachmentFlatFilenameSeparatesTheSingleHyphenCollision(t *testing.T) {
	t.Parallel()

	// One coordinate list carries the "c" as part of the instance and the other
	// carries it at the head of the file name. Under a boundary either side could
	// itself contain, the two are indistinguishable once assembled.
	leftCoords, leftTail := []string{"a", "b"}, "c-x.yaml"
	rightCoords, rightTail := []string{"a", "b-c"}, "x.yaml"

	left := attachmentOutputPrefix(leftCoords...) + leftTail
	right := attachmentOutputPrefix(rightCoords...) + rightTail
	if left == right {
		t.Fatalf("(%q,%q) and (%q,%q) both reach %q, so the boundary between the prefix and the file name is ambiguous",
			leftCoords, leftTail, rightCoords, rightTail, left)
	}
	t.Logf("separated: %q and %q", left, right)

	// The negative control for the choice: the same two inputs assembled with a
	// one-character boundary, which must collide. Were this to stop colliding,
	// the pair above would have stopped exercising the boundary's width and the
	// assertion would be passing for the wrong reason.
	single := func(coords []string, tail string) string {
		return strings.Join(coords, "-") + "-" + tail
	}
	if single(leftCoords, leftTail) != single(rightCoords, rightTail) {
		t.Fatalf("the worked pair no longer collides under a one-character boundary (%q vs %q), so it no longer demonstrates why the boundary is two",
			single(leftCoords, leftTail), single(rightCoords, rightTail))
	}
	t.Logf("under a one-character boundary both reach %q", single(leftCoords, leftTail))
}

// TestAttachmentFlatFilenameOpensNoDirectoryLevel is the proof that the flat
// placement is actually flat.
//
// A component identity may be composed from a nested path, so a coordinate can
// carry a path separator. Carried through as authored it would open a directory
// level and put the attachment's files back underneath one — leaving them
// exactly where the declaration exists to move them from, and doing it silently,
// since a nested path is a perfectly ordinary map key.
func TestAttachmentFlatFilenameOpensNoDirectoryLevel(t *testing.T) {
	t.Parallel()

	for _, coords := range attachmentFlatCoordinateLists() {
		prefix := attachmentOutputPrefix(coords...)
		if strings.ContainsAny(prefix, `/\`) {
			t.Errorf("coordinates %q reached a prefix carrying a path separator: %q", coords, prefix)
		}
	}
}

// TestAttachmentFlatFilenameJoinsOnTheEncodingsSeparator states where the
// boundary comes from, so that a later change spelling it locally is caught.
//
// The separator is the one rule the whole derivation's injectivity rests on, and
// it belongs to the encoding that reserves it. A second spelling anywhere —
// here, or at either composition site — would agree on the day it was written
// and be free to drift afterwards, with nothing comparing the two.
func TestAttachmentFlatFilenameJoinsOnTheEncodingsSeparator(t *testing.T) {
	t.Parallel()

	prefix := attachmentOutputPrefix("host", "instance")
	if !strings.HasSuffix(prefix, attachmentFileEncoding.SegmentSeparator()) {
		t.Errorf("the prefix does not end at the encoding's own separator: %q", prefix)
	}
	if want := "host" + attachmentFileEncoding.SegmentSeparator() + "instance" + attachmentFileEncoding.SegmentSeparator(); prefix != want {
		t.Errorf("prefix = %q, want %q", prefix, want)
	}
}

// TestContributorDirSegmentSpellsTheDirectoryAContributorSitsAt holds the
// directory answer for a group that gives each contributor a position of its
// own, which is the other arm from the filename prefix above.
//
// It is the readable-spelling half. The alphabet admits what a person reads in a
// directory name and what a pointer to that directory can carry, so an ordinary
// identity reaches the directory it always reached and a capital or a dot
// survives as written — which is the whole reason this segment is not written in
// the filename alphabet, where the same capital becomes a run of hex digits.
func TestContributorDirSegmentSpellsTheDirectoryAContributorSitsAt(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		coords []string
		want   string
	}{
		{"a component's own name passes through", []string{"api"}, "api"},
		{"a boundary-scope attachment is its instance alone", []string{"aux"}, "aux"},
		{"a component-scope attachment joins host and instance", []string{"api", "mon"}, "api--mon"},
		{"an authored hyphen is kept where the next character is admitted", []string{"web-api", "mon"}, "web-api--mon"},
		{"a dot is admitted rather than escaped", []string{"my.api", "mon"}, "my.api--mon"},
		{"an underscore is admitted", []string{"my_api", "mon"}, "my_api--mon"},
		// The row the alphabet exists for: under an alphabet admitting only
		// lowercase this pair reaches a name opening with a hyphen, which a
		// command line reads as a flag.
		{"an upper-cased coordinate stays readable", []string{"API", "aux"}, "API--aux"},
		// A doubled hyphen is the one hyphen that has to escape, because the
		// pair is the join.
		{"a doubled hyphen escapes so the join stays readable back", []string{"a--b", "c"}, "a-zh-b--c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := contributorDirSegment(tc.coords...); got != tc.want {
				t.Errorf("contributorDirSegment(%q) = %q, want %q", tc.coords, got, tc.want)
			}
		})
	}
}

// TestContributorDirSegmentSeparatesEveryDISTINCTCoordinateList is the
// injectivity half, and it is what stops two contributors sharing one directory.
//
// Two contributors that meet at one directory do NOT collide on any path when
// their file names differ, so the attribution that reports two producers meeting
// at one path reports nothing at all — the meet is silent, and each contributor's
// files sit in a directory holding the other's. Nothing else in the pipeline
// catches it, which is why the property is asserted here rather than assumed.
//
// The two rows that matter are the ones a naive join gets wrong: a raw
// two-hyphen join maps ("a--b","c") and ("a","b--c") to one string, and taking a
// single coordinate as its raw name maps a component called "a--b" onto the
// attachment "b" of host "a".
func TestContributorDirSegmentSeparatesEveryDISTINCTCoordinateList(t *testing.T) {
	t.Parallel()

	lists := [][]string{
		{"api"}, {"api", "mon"}, {"api", "aux"}, {"aux"},
		{"a--b"}, {"a", "b"}, {"a--b", "c"}, {"a", "b--c"},
		{"API"}, {"api", ""}, {""}, {"my.api", "mon"}, {"my", "api--mon"},
	}
	seen := map[string][]string{}
	for _, list := range lists {
		got := contributorDirSegment(list...)
		if prior, clash := seen[got]; clash {
			t.Errorf("%q and %q both reach the directory %q, so one contributor's files "+
				"would sit in a directory holding another's and no path would collide",
				prior, list, got)
			continue
		}
		seen[got] = list
	}
	// A positive control on the control itself: the clash above has to be
	// reachable, or this test passes on a set that could not have collided.
	if a, b := contributorDirSegment("a", "b"), contributorDirSegment("a", "b"); a != b {
		t.Errorf("the composition is not deterministic: %q then %q", a, b)
	}
	if raw := "a--b" + "--" + "c"; raw != "a"+"--"+"b--c" {
		t.Errorf("the raw join this composition replaces is no longer ambiguous, so the "+
			"injectivity rows above no longer pin anything: %q", raw)
	}
}

// TestAttachmentFlatFilenamePinsTheWorkedExampleCoordinates pins the filename
// prefixes that the worked example pages for this target print as output.
//
// Those pages quote emitted paths, and a reader is invited to build the example
// and compare their own output against the page line by line — so a page whose
// quoted path no longer matches what the build writes is not a stale note, it is
// an instruction that fails. The pages are hand-written and nothing captures
// them, so this is what makes the derivation behind them fail loudly instead.
//
// 🔴 It catches a REGRESSION and cannot catch a page that was wrong when
// written. If a row here changes, the page named beside it has to be rebuilt and
// re-captured; the row moving on its own only means the prefix moved.
//
// The coordinates are the real ones from those pages, and each is chosen because
// it exercises a character the filename alphabet refuses: an underscore in an
// authored attachment key, and the dots and slash of an attachment key an
// annotation mints rather than an author typing it.
func TestAttachmentFlatFilenamePinsTheWorkedExampleCoordinates(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		page   string
		coords []string
		want   string
	}{
		{"examples/ai-agent-and-model-router", []string{"router", "api_key"}, "router--api-zukey--"},
		{"examples/ai-agent-and-model-router", []string{"router", "provider_key"}, "router--provider-zukey--"},
		{"examples/ai-agent-and-model-router", []string{"router", "published"}, "router--published--"},
		{"examples/ai-agent-and-model-router", []string{"router", "metrics"}, "router--metrics--"},
		{"examples/ai-agent-and-model-router", []string{"agent", "model_api_key"}, "agent--model-zuapi-zukey--"},
		{"examples/model-api-key-sharing", []string{"researcher", "model_api_key"}, "researcher--model-zuapi-zukey--"},
		{"examples/model-api-key-sharing", []string{"writer", "model_api_key"}, "writer--model-zuapi-zukey--"},
		{"examples/credential-secretkeyref", []string{"db", "password"}, "db--password--"},
		{"examples/mariadb-bitnami-credential-set", []string{"db", "root_password"}, "db--root-zupassword--"},
		{"examples/credential-shape-per-target", []string{"db", "app_user_password"}, "db--app-zuuser-zupassword--"},
		{"examples/annotation-driven-monitoring", []string{"api", "feature.aggrega.ai/monitoring"},
			"api--feature-zdaggrega-zdai-zsmonitoring--"},
	} {
		t.Run(tc.page+" "+strings.Join(tc.coords, "/"), func(t *testing.T) {
			t.Parallel()

			if got := attachmentOutputPrefix(tc.coords...); got != tc.want {
				t.Errorf("%s quotes output built on %q, which now composes to %q, want %q — rebuild that example and re-capture its page",
					tc.page, tc.coords, got, tc.want)
			}
		})
	}
}
