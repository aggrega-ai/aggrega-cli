// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package pathsegment_test

import (
	"strings"
	"testing"

	"github.com/aggrega-ai/aggrega-cli/pkg/pathsegment"
)

// deviceNames is every name Windows reserves for a device, in the spelling the
// reference documentation gives it.
var deviceNames = []string{
	"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$",
	"COM0", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT0", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
	"COM¹", "COM²", "COM³", "LPT¹", "LPT²", "LPT³",
}

// TestWindowsCannotHold drives the one predicate over each class it decides: every
// device name in two letter cases, bare, with an extension and as a directory above
// another segment; a segment ending in a full stop and one ending in a space, as the
// last segment and as a directory; the two dot names, which it holds; a segment
// read across a backslash, as a Windows join reads it; and ordinary names. Each row
// names the segment it must report, so a predicate that refused the right names for
// the wrong segment fails too.
//
// 🔴 PLANTED DEFECT: drop the space from the final-character rule in
// cannotHoldSegment (pkg/pathsegment/pathsegment.go), leaving `last == '.'`, and
// every row whose segment ends in a space reddens — the ".. " rows among them —
// while TestValidAgreesWithWindowsCannotHoldForOneSegment stays green, because
// both read that one rule.
func TestWindowsCannotHold(t *testing.T) {
	type row struct {
		name    string
		in      string
		segment string // empty: Windows holds the name as written
	}
	var rows []row
	for _, device := range deviceNames {
		lower := strings.ToLower(device)
		rows = append(rows,
			row{device + " bare", device, device},
			row{lower + " bare", lower, lower},
			row{device + " with an extension", device + ".txt", device + ".txt"},
			row{lower + " with two extensions", lower + ".tar.gz", lower + ".tar.gz"},
			row{lower + " as a directory", lower + "/x.yaml", lower},
			row{device + " beneath a directory", "dir/" + device, device},
		)
	}
	rows = append(rows,
		row{"aux as a directory", "aux/x.yaml", "aux"},
		row{"a device name with a space before its extension", "nul .txt", "nul .txt"},
		row{"a trailing full stop, last", "report.", "report."},
		row{"a trailing full stop, as a directory", "report./x.yaml", "report."},
		row{"a trailing space, last", "report ", "report "},
		row{"a trailing space, as a directory", "report /x.yaml", "report "},
		row{"three full stops", "a/.../b", "..."},
		row{"two dots and a space read across a backslash", `.. \evil.yaml`, ".. "},
		row{"two dots and a space read across a slash", ".. /evil.yaml", ".. "},
		row{"a device name read across a backslash", `dir\nul\x.yaml`, "nul"},
		row{"the current directory is held", "./x.yaml", ""},
		row{"the parent directory is held", "../x.yaml", ""},
		row{"a bare dot is held", ".", ""},
		row{"a bare pair of dots is held", "..", ""},
		row{"an ordinary name", "config.yaml", ""},
		row{"an ordinary nested name", "a/b/c.yaml", ""},
		row{"a name opening with a full stop", ".gitignore", ""},
		row{"a name opening with two dots", "..config", ""},
		row{"a device name inside a longer word", "console.log", ""},
		row{"a device name as a suffix", "my-aux.yaml", ""},
		row{"a device name with a trailing digit too many", "COM10", ""},
		row{"a doubled separator", "a//b", ""},
		row{"a leading backslash", `\notes.txt`, ""},
		row{"empty", "", ""},
	)
	for _, tc := range rows {
		t.Run(tc.name, func(t *testing.T) {
			segment, held := pathsegment.WindowsCannotHold(tc.in)
			want := tc.segment != ""
			if held != want || segment != tc.segment {
				t.Fatalf("WindowsCannotHold(%q) = (%q, %v); want (%q, %v)", tc.in, segment, held, tc.segment, want)
			}
		})
	}
}

// TestValidAgreesWithWindowsCannotHoldForOneSegment holds the shared home: for a
// single non-empty segment other than "." and "..", carrying none of the characters
// Valid refuses for itself, Valid accepts exactly the segments WindowsCannotHold
// holds. The two read one list of
// device names and one rule about a final full stop or space, so this cannot
// disagree unless one of them stops reading it.
func TestValidAgreesWithWindowsCannotHoldForOneSegment(t *testing.T) {
	segments := []string{
		"config.yaml", "report.", "report ", "report. ", "...", ".. ", ". ",
		"a", "A", "console", "my-aux", "COM10", "LPT", "nul .txt", "aux.", "aux ",
	}
	for _, device := range deviceNames {
		lower := strings.ToLower(device)
		segments = append(segments, device, lower, device+".txt", lower+".tar.gz")
	}
	for _, segment := range segments {
		_, held := pathsegment.WindowsCannotHold(segment)
		if valid := pathsegment.Valid(segment); valid == held {
			t.Errorf("Valid(%q) = %v and WindowsCannotHold reports %v; a segment Valid accepts is one Windows "+
				"holds, and one it refuses for no character of its own is one Windows cannot hold", segment, valid, held)
		}
	}
}
