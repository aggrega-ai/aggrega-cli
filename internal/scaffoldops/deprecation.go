// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package scaffoldops

// ScaffoldSpellingRemovedIn is the release that removes the scaffold spelling of
// drafting: the hidden `aggrega scaffold` command and the assistant tool named
// scaffold, both of which answer exactly as their draft spelling does until
// then. Every surface that announces the deprecation reads the release from
// here, so the command line and the tool protocol name the same one.
const ScaffoldSpellingRemovedIn = "0.10.0"
