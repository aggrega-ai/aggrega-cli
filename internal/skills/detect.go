// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package skills

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/aggrega-ai/aggrega-cli/pkg/machinedirs"
)

// Detection is one assistant found installed on this machine, and what found
// it.
type Detection struct {
	// Agent is the assistant found.
	Agent Agent

	// Folder is the absolute path of the assistant's configuration folder, when
	// that folder is what found it.
	Folder string

	// Command is the absolute path of the assistant's program on PATH, when
	// that program is what found it. A folder found first leaves it empty.
	Command string
}

// Evidence renders what found the assistant for a message: "the folder
// /home/you/.claude" or "the command /usr/local/bin/codex".
func (d Detection) Evidence() string {
	if d.Folder != "" {
		return "the folder " + d.Folder
	}
	return "the command " + d.Command
}

// Detect reports the assistants installed on this machine, in the order of the
// assistant table.
//
// An assistant counts as installed when its configuration folder exists in the
// home directory, or its program is on PATH. The home directory and PATH are
// read through lookup, as [UserHome] reads them, so a caller holding an
// environment of its own — a daemon serving a client, a test — detects against
// that environment rather than the process's. A PATH entry that is empty or
// relative is skipped, so the folder a command happens to run in never counts
// as a place an assistant is installed.
//
// It writes nothing and reaches no network. It refuses only where the home
// directory is unknown.
func Detect(p machinedirs.Platform, lookup func(string) string) ([]Detection, error) {
	home, err := UserHome(p, lookup)
	if err != nil {
		return nil, err
	}
	var found []Detection
	for _, a := range agents {
		if a.ConfigDir != "" {
			dir := fromSlash(home, a.ConfigDir)
			if info, err := os.Stat(dir); err == nil && info.IsDir() {
				found = append(found, Detection{Agent: a, Folder: dir})
				continue
			}
		}
		if a.Command != "" {
			if path, ok := findCommand(p, lookup, a.Command); ok {
				found = append(found, Detection{Agent: a, Command: path})
			}
		}
	}
	return found, nil
}

// DetectionIDs returns the identifiers of the detected assistants, in order.
func DetectionIDs(found []Detection) []string {
	ids := make([]string, 0, len(found))
	for _, d := range found {
		ids = append(ids, d.Agent.ID)
	}
	return ids
}

// NoAssistantFound is the refusal for an installation asked to install for the
// assistants on this machine where none was found.
func NoAssistantFound() error {
	return errors.New("found no AI assistant installed on this machine, by its configuration folder in the " +
		"home directory or its command on PATH, so nothing was written; name the assistants to install the skill " +
		"for instead, by the identifiers \"aggrega skills list\" prints")
}

// findCommand looks name up on the PATH lookup reads, as a shell on p finds a
// program: every PATH entry in order, and on Windows each extension PATHEXT
// lists. It returns the first match, which on Windows is any file with a listed
// extension and elsewhere a file someone may execute.
func findCommand(p machinedirs.Platform, lookup func(string) string, name string) (string, bool) {
	sep := ":"
	names := []string{name}
	if p == machinedirs.Windows {
		sep = ";"
		exts := lookup("PATHEXT")
		if exts == "" {
			exts = ".COM;.EXE;.BAT;.CMD"
		}
		names = names[:0]
		for _, ext := range strings.Split(exts, ";") {
			if ext != "" {
				names = append(names, name+strings.ToLower(ext))
			}
		}
	}
	for _, entry := range strings.Split(lookup("PATH"), sep) {
		if entry == "" || !isAbsFor(p, entry) {
			continue
		}
		for _, n := range names {
			candidate := filepath.Join(entry, n)
			info, err := os.Stat(candidate)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			if p != machinedirs.Windows && info.Mode().Perm()&0o111 == 0 {
				continue
			}
			return candidate, true
		}
	}
	return "", false
}

// isAbsFor reports whether path is absolute on p. On this host the host's own
// rule answers; for Windows read from elsewhere, a drive letter or a leading
// separator of either kind counts.
func isAbsFor(p machinedirs.Platform, path string) bool {
	if p == machinedirs.Host() {
		return filepath.IsAbs(path)
	}
	if p == machinedirs.Windows {
		return strings.HasPrefix(path, `\`) || strings.HasPrefix(path, "/") ||
			(len(path) >= 3 && path[1] == ':' && (path[2] == '\\' || path[2] == '/'))
	}
	return strings.HasPrefix(path, "/")
}
