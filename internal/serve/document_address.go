// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package serve

import (
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
)

// fileAddressPrefix opens a document's file address: the scheme, and the
// authority marker that follows it.
const fileAddressPrefix = "file://"

// documentPath returns the filesystem path a request's document value names.
//
// A request may name a document by its path — relative to the workspace root, or
// absolute — or by its file address, which is the form a language-server client
// sends for every document it has open. An address is decoded here, its
// percent-escapes included, and what comes out is a path like any other; every
// other value is returned exactly as it arrived. So a document validated under its
// path is found under its address and the other way round, and no value that is
// not an address changes meaning.
//
// An address that names nothing this daemon can open is returned as it arrived
// too, so a refusal names what the request sent; addressPath says which those are.
func documentPath(value string) string {
	p, ok := addressPath(value, runtime.GOOS == "windows")
	if !ok {
		return value
	}
	return filepath.FromSlash(p)
}

// addressPath decodes a file address into the slash-separated path it names, and
// reports whether value was an address naming a document this machine can open.
// driveLetters selects the path spelling of a system whose absolute paths open
// with a drive letter; it is a parameter rather than read here so that both
// spellings are exercised wherever the tests run.
//
// An address is recognised by its opening alone: the scheme, matched without
// regard to case as the address syntax defines it, followed by the authority
// marker. What it carries after that must be what a document's address carries
// and nothing else — no query, no fragment, no credentials, no port, and a path —
// or it names nothing that can be opened.
//
// Only an empty authority and localhost name this machine. On a drive-letter
// system the leading separator before the drive is dropped, since the drive opens
// the path there, and an authority naming another machine reads as that machine's
// network share, which is how such a system opens a document held elsewhere.
// Anywhere else an address naming another machine names nothing this daemon can
// open.
func addressPath(value string, driveLetters bool) (string, bool) {
	if len(value) < len(fileAddressPrefix) || !strings.EqualFold(value[:len(fileAddressPrefix)], fileAddressPrefix) {
		return "", false
	}
	u, err := url.Parse(value)
	if err != nil || u.Opaque != "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return "", false
	}
	if u.Port() != "" || u.Path == "" {
		return "", false
	}
	host := u.Hostname()
	local := host == "" || strings.EqualFold(host, "localhost")
	if !driveLetters {
		if !local {
			return "", false
		}
		return u.Path, true
	}
	if !local {
		return "//" + host + u.Path, true
	}
	p := u.Path
	if len(p) >= 3 && p[0] == '/' && p[2] == ':' && isDriveLetter(p[1]) {
		p = p[1:]
	}
	return p, true
}

// isDriveLetter reports whether b is a letter a drive can be named by.
func isDriveLetter(b byte) bool {
	return ('a' <= b && b <= 'z') || ('A' <= b && b <= 'Z')
}
