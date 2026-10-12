// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package mcp

import "fmt"

// Which release this server comes from, and which release the desktop bundle
// that launched it comes from.
//
// The desktop bundle carries no program: it launches whatever aggrega
// executable its user's setting names, so the bundle and the executable are
// installed separately and can come from different releases. The application
// showing the bundle lists the bundle's own version and not the executable's,
// so the server is the one place both are known. It reports them on the
// argumentless capability answer, and where they differ it says so there and in
// the guidance a client reads on connecting.
//
// The two versions are compared as strings, exactly. That is the honest
// comparison rather than a shortcut: the bundle's build and the binary's build
// stamp their version from one recipe, in the same v-less form, so one release
// yields one string on both sides, and anything else — a different tag, a
// development build, a build from an edited tree — is a different build and is
// reported as one.
//
// Nothing here refuses. A difference is a fact a person may want to act on; it
// is never a reason to decline a launch or a call.

// versionLine is the line the argumentless capability answer opens with: the
// version this server runs, and, where a desktop bundle launched it, that
// bundle's version and whether the two come from one release. It is empty where
// the server reports no version of its own, which no launch of the binary does.
//
// Where the two differ the line is exactly releaseDifference's sentence, so the
// capability answer and the connect-time guidance say it in the same words.
func versionLine(serverVersion, bundleVersion string) string {
	switch {
	case serverVersion == "":
		return ""
	case bundleVersion == "":
		return fmt.Sprintf("This server runs aggrega %s.", serverVersion)
	case bundleVersion == serverVersion:
		return fmt.Sprintf("This server runs aggrega %s, launched by the desktop bundle of the same version, %s.",
			serverVersion, bundleVersion)
	default:
		return releaseDifference(serverVersion, bundleVersion)
	}
}

// releaseDifference is the sentence saying that the aggrega executable this
// server runs and the desktop bundle that launched it come from different
// releases, naming both versions, and the empty string wherever there is no
// such difference to state: no bundle launched the server, the two versions
// are equal, or the server reports no version of its own.
//
// It is the one home of that sentence. The capability answer's opening line
// and the connect-time guidance both read it here, so a client that reads one
// and then the other is told the same thing.
func releaseDifference(serverVersion, bundleVersion string) string {
	if serverVersion == "" || bundleVersion == "" || bundleVersion == serverVersion {
		return ""
	}
	return fmt.Sprintf("This server runs aggrega %s, launched by the desktop bundle version %s: the aggrega "+
		"executable and the bundle come from different releases.", serverVersion, bundleVersion)
}

// reportedVersions returns the two versions the argumentless capability answer
// carries: this server's own, and the launching bundle's where there was one.
// Both are empty where the server reports no version of its own, so a bundle's
// version is never reported without the version it is to be compared with.
func (s *server) reportedVersions() (serverVersion, bundleVersion string) {
	if s.serverVersion == "" {
		return "", ""
	}
	return s.serverVersion, s.bundleVersion
}
