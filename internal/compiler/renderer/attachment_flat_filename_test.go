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
				name := attachmentOutputPrefix("", coords...) + tail
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

	left := attachmentOutputPrefix("", leftCoords...) + leftTail
	right := attachmentOutputPrefix("", rightCoords...) + rightTail
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
		prefix := attachmentOutputPrefix("", coords...)
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

	prefix := attachmentOutputPrefix("", "host", "instance")
	if !strings.HasSuffix(prefix, attachmentFileEncoding.SegmentSeparator()) {
		t.Errorf("the prefix does not end at the encoding's own separator: %q", prefix)
	}
	if want := "host" + attachmentFileEncoding.SegmentSeparator() + "instance" + attachmentFileEncoding.SegmentSeparator(); prefix != want {
		t.Errorf("prefix = %q, want %q", prefix, want)
	}
}

// TestAttachmentOutputPrefixKeepsTheDirectoryShapeUnchanged is the
// nothing-moved half: the arm a provider takes when its group gives each
// contributor a position of its own, which is where an attachment still sits in
// a directory.
//
// It composes the coordinates as directory segments, each as authored and none
// encoded, which is why a host whose identity carries a dot still gives a
// directory carrying that dot. That is the opposite of what the filename arm
// does with the same character, and the reason is the position rather than the
// character: a directory segment stands alone, so every character an author may
// put in a component identity is legal in it, while a filename prefix shares one
// string with the file's own name and has no separator to fall back on.
//
// The dotted row is the one that would otherwise lose its cover. The end-to-end
// case that used to hold the unescaped-directory property runs under a provider
// whose group gives no contributor a position, so it now asserts the escaped
// filename instead — and this row is where the directory answer is held.
func TestAttachmentOutputPrefixKeepsTheDirectoryShapeUnchanged(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		dir    string
		coords []string
		want   string
	}{
		{"component scope under the host default", "features/", []string{"api", "mon"}, "features/api/mon/"},
		{"boundary scope under the host default", "features/", []string{"aux"}, "features/aux/"},
		{"component scope under a declared directory", "addons/", []string{"api", "mon"}, "addons/api/mon/"},
		{"boundary scope under a declared directory", "addons/", []string{"aux"}, "addons/aux/"},
		{"a coordinate carrying a dot is not escaped", "features/", []string{"my.api", "mon"}, "features/my.api/mon/"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := attachmentOutputPrefix(tc.dir, tc.coords...); got != tc.want {
				t.Errorf("attachmentOutputPrefix(%q, %q) = %q, want %q", tc.dir, tc.coords, got, tc.want)
			}
		})
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
		{"examples/ai-agent-and-model-router", []string{"router", "master_key"}, "router--master-zukey--"},
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

			if got := attachmentOutputPrefix("", tc.coords...); got != tc.want {
				t.Errorf("%s quotes output built on %q, which now composes to %q, want %q — rebuild that example and re-capture its page",
					tc.page, tc.coords, got, tc.want)
			}
		})
	}
}
