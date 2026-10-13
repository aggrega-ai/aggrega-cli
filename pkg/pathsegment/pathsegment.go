// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package pathsegment spells the file and directory names this program derives
// from values it did not choose — an entity's reference, a version, a content
// digest, a registry host, a path a URL carries — so that Windows, Linux and
// macOS all accept every such name, two different values never share one, and
// the name for a value is the same on every machine.
//
// # Why a value needs spelling at all
//
// A reference such as hub.aggrega.ai/plugin/cli/moo:0.2.4, or a digest such as
// sha256:<hex>, carries characters a file name cannot: a separator, which would
// turn one name into several directories, and a colon, which Windows refuses
// outright. Windows also refuses a name ending in a full stop or a space, and a
// name it reserves for a device — CON, NUL, COM1 and the rest — whatever
// extension follows it; and Windows and macOS compare names without regard to
// letter case by default, so two values differing only in case would share one
// file there.
//
// # The spelling
//
// [Encode] writes a lowercase ASCII letter, a digit, the full stop, the
// underscore and the hyphen as themselves, and every other character as an
// escape opening with the plus sign. An escape is self-delimiting and is itself
// written in lowercase letters and digits, so a name never carries an uppercase
// letter: two values differing only in case are two names, whatever a file
// system folds. A full stop ending a value is written as an escape, and so is the
// first character of a value whose name would otherwise be one Windows reserves.
// So hub.aggrega.ai/plugin/cli/moo:0.2.4 is stored under
// hub.aggrega.ai+zsplugin+zscli+zsmoo+zx00003a0.2.4, and 1.0.0 under 1.0.0.
//
// The spelling is the segment encoding of the namesegment package over that
// alphabet, so a name reads back into the value it was written for ([Decode]).
// [Valid] is the rule a name has to meet to be accepted on every platform; every
// name [Encode] writes meets it, and so does a name formed by appending a fixed
// extension — a full stop followed by lowercase letters — to one.
//
// # A name somebody else chose
//
// A name this program did not spell — an archive entry, a draft an importer
// returned, a rendered file's name — keeps the spelling it was given, so Windows
// may be unable to hold it as written. [WindowsCannotHold] answers that for a
// whole slash-separated path, segment by segment, by the same rule [Valid] applies
// to one name: there is one list of the names Windows reserves for a device, and
// one rule about a name ending in a full stop or a space. What to do with the
// answer is the caller's: it refuses nothing on its own.
//
// Every derivation here is deterministic: identical input yields byte-identical
// output.
package pathsegment

import (
	"strings"
	"unicode/utf8"

	"github.com/aggrega-ai/aggrega-cli/pkg/namesegment"
)

// opener opens every escape a name carries. It is a character every platform
// accepts in a file name and one the alphabet a name carries as written leaves
// out, which the encoding requires of it.
const opener = '+'

// encoding is the one spelling of a derived file or directory name. It is
// unexported so a name is reached through [Encode] or not at all.
var encoding = namesegment.NewEncoding(opener, carried,
	namesegment.AdmitsLast(carriedLast),
	namesegment.Reserved(reservedName),
)

// carried reports whether a character is written into a name as itself: a
// lowercase ASCII letter, a digit, the full stop, the underscore or the hyphen.
// An uppercase letter is not carried, so a name is the same string under any
// case folding a file system applies.
func carried(r rune) bool {
	return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-'
}

// carriedLast reports whether a carried character may also end a name. A full
// stop may not, because Windows refuses a name ending in one.
func carriedLast(r rune) bool { return r != '.' }

// Encode returns the file or directory name value is stored under.
//
// The name is valid on Windows, Linux and macOS ([Valid]); it is never empty and
// never the current or parent directory, so joining it onto a directory names
// exactly one entry inside that directory whatever value carries; two different
// values always yield two different names, and do so under any letter-case
// folding; and the same value yields the same name every time. Characters every
// platform accepts are written as themselves, so a name stays readable where the
// value allows it.
func Encode(value string) string {
	return encoding.Segment(value)
}

// Decode returns the value a name [Encode] wrote was written for, and whether
// name is exactly such a name. It is false for a name written some other way —
// one no derivation in this program produced — and the value is then not
// meaningful.
func Decode(name string) (string, bool) {
	return encoding.ReadSegment(name)
}

// Valid reports whether name is a file or directory name Windows, Linux and macOS
// each accept: not empty, valid UTF-8, neither "." nor "..", carrying none of
// < > : " / \ | ? * and no control character, not ending in a full stop or a
// space, and not a name Windows reserves for a device.
//
// It does not judge length: what a platform accepts there depends on the
// directory a name is placed in rather than on the name.
func Valid(name string) bool {
	if name == "" || name == "." || name == ".." || !utf8.ValidString(name) {
		return false
	}
	for _, r := range name {
		if r < 0x20 || strings.ContainsRune(`<>:"/\|?*`, r) {
			return false
		}
	}
	return !cannotHoldSegment(name)
}

// WindowsCannotHold reports the first segment of name that Windows cannot hold as
// written, and whether name holds one.
//
// name is a slash-separated logical name — an archive entry, a rendered output
// name, the path of a drafted file — and a backslash in it is read as a separator
// as well, as a Windows join reads one, so ".. \evil.yaml" is judged by its
// segment ".. ". A segment Windows cannot hold as written is a name it reserves for
// a device, in any letter case and whether or not an extension follows — "NUL",
// "aux", "COM1.txt" — and a segment other than "." and ".." ending in a full stop
// or a space, such as "report." or ".. ". The empty segments a leading, trailing
// or doubled separator leaves name nothing and are passed over.
//
// It reads the one list of device names and the one rule about a final full stop
// or space that [Valid] reads, so for a single segment that is neither "." nor "..",
// is valid UTF-8 and carries no character [Valid] refuses for itself, the two give
// one answer. Its own answer is the same on every platform — it says what Windows
// would do with the name, wherever it is asked — and it refuses nothing: whoever
// writes the name decides what the answer means where it writes.
func WindowsCannotHold(name string) (string, bool) {
	for _, segment := range strings.FieldsFunc(name, isSeparator) {
		if cannotHoldSegment(segment) {
			return segment, true
		}
	}
	return "", false
}

// WindowsCannotHoldReason says, in the words a diagnostic gives its reader, what
// [WindowsCannotHold] reports. Every message about a name it flags — a write
// refusing the name on Windows, a publication warning that it carries one — says it
// with this text, so the reason a reader is given is stated once, beside the rule
// it describes.
const WindowsCannotHoldReason = "Windows reserves device names such as CON, PRN, AUX, NUL, COM1 and LPT1, in any " +
	"letter case and whatever extension follows, and does not hold a segment ending in a full stop or a space as written"

// isSeparator reports whether r separates the segments of a name as a Windows
// join reads it: a slash, or a backslash.
func isSeparator(r rune) bool { return r == '/' || r == '\\' }

// cannotHoldSegment reports whether Windows cannot hold segment, one segment of a
// path, as written: a segment other than "." and ".." that ends in a full stop or
// a space, or a name Windows reserves for a device. It is the one statement of
// both rules, so [Valid] and [WindowsCannotHold] cannot come to disagree about a
// segment.
func cannotHoldSegment(segment string) bool {
	if segment == "" || segment == "." || segment == ".." {
		return false
	}
	if last := segment[len(segment)-1]; last == '.' || last == ' ' {
		return true
	}
	return reservedName(segment)
}

// reservedName reports whether Windows reserves name for a device: CON, PRN,
// AUX, NUL, CONIN$, CONOUT$, COM0 to COM9 and LPT0 to LPT9, together with the
// COM and LPT names spelled with a superscript one, two or three, in any letter
// case and whether or not an extension follows. The part before the first full
// stop decides, with any spaces ending it ignored, as Windows ignores them.
func reservedName(name string) bool {
	stem, _, _ := strings.Cut(name, ".")
	stem = strings.TrimRight(stem, " ")
	for _, device := range reservedDevices {
		if strings.EqualFold(stem, device) {
			return true
		}
	}
	return false
}

// reservedDevices are the device names reservedName compares against.
var reservedDevices = func() []string {
	names := []string{"CON", "PRN", "AUX", "NUL", "CONIN$", "CONOUT$"}
	for _, port := range []string{"COM", "LPT"} {
		for _, digit := range []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "¹", "²", "³"} {
			names = append(names, port+digit)
		}
	}
	return names
}()
