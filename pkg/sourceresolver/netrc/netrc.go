// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

// Package netrc reads the netrc file — the per-user credential file curl, Git
// and the Go toolchain also read — for the source kinds fetched over HTTP: a
// classic Helm chart repository or chart archive, a compose fragment, and a Git
// repository over HTTPS. It is the one reader of that file; the adapters ask it
// for the credential of each request's host and parse nothing themselves.
//
// # Which file
//
// The file named by the NETRC environment variable when that is set. Otherwise
// `.netrc` in the user's home directory; on Windows, `_netrc` in %USERPROFILE%
// when that file exists, and `.netrc` there when it does not. A file that does
// not exist is no error: it holds no credentials.
//
// # Which entry
//
// A request is answered by the first `machine` entry naming the request's host —
// its host name without the port, compared without regard to letter case —
// and, when no entry names it, by the `default` entry. An entry is used only
// when it carries both a login and a password, which are then sent as HTTP
// basic credentials.
//
// # What is never done with it
//
// The file is read when a fetch needs it and at no other time, and what it holds
// is sent with the request and written nowhere: a lockfile, the source cache and
// every diagnostic carry the source's URL as it was written. That URL carries no
// credential of its own, because a Recipe or FeatureRecipe whose source URL
// carries one ([URLCredential]) is refused as its document is read. A file that does not
// follow the grammar is refused naming the file and the line, never repeating
// what the line holds.
//
// On a host other than Windows, a file that users other than its owner can read
// draws one warning naming the file — once per file for the life of the process
// — and is still read.
package netrc

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
)

// Credential is a login and password a netrc entry holds for a host.
type Credential struct {
	Login    string
	Password string
}

// File is a netrc file as read: its entries, in the order it writes them. The
// zero value and a nil *File hold no entries.
type File struct {
	entries []entry
}

// ForURL returns the credential to send with a request for rawURL, and whether
// there is one.
//
// There is none for a URL whose scheme is not http or https, for a URL that does
// not parse, and for a URL that carries a credential of its own
// ([URLCredential]): such a URL is refused as its document is read, so it reaches
// no fetch, and the file is not asked to stand in for a credential the URL already
// names. Otherwise the answer is the entry for the URL's host, as the package
// documentation describes.
func (f *File) ForURL(rawURL string) (Credential, bool) {
	if f == nil {
		return Credential{}, false
	}
	if _, carries := URLCredential(rawURL); carries {
		return Credential{}, false
	}
	host, ok := HostFor(rawURL)
	if !ok {
		return Credential{}, false
	}
	return f.forHost(host)
}

// URLCredential reports whether a source URL carries a credential of its own in
// its authority part, and the host it names that credential for — the host name
// without its port, so a diagnostic can name the host without repeating the
// credential.
//
// It reads the URL's grammar and nothing else: the user-information part before
// the host, which is where a URL carries a user and password. Over http or https
// any user information counts, a user written alone included, because a service
// reached over HTTP commonly takes a token in the user's place, and the netrc file
// is where a credential for those schemes is read from. Over any other scheme only
// a password counts: a user written alone there names an account rather than a
// secret — `ssh://git@host/…` is how a Git repository reached over SSH is
// commonly written, and the SSH agent supplies its key — while a password is a
// secret whatever the transport. A token written into a query string or a path is
// not user information, and nothing here guesses whether a value is one.
//
// It is the single answer to the question for every reader of it: the check
// refusing a Recipe or FeatureRecipe whose source URL carries a credential, and
// [File.ForURL], which declines such a URL.
func URLCredential(rawURL string) (host string, carries bool) {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
		return u.Hostname(), true
	}
	if _, hasPassword := u.User.Password(); hasPassword {
		return u.Hostname(), true
	}
	return "", false
}

// HostFor returns the host whose entry answers a request for rawURL — the URL's
// host name without its port — and false for a URL the netrc file answers no
// request for: one whose scheme is not http or https, or that does not parse.
func HostFor(rawURL string) (string, bool) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", false
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return "", false
	}
	host := u.Hostname()
	return host, host != ""
}

// forHost returns the credential the file holds for host, a host name without a
// port.
func (f *File) forHost(host string) (Credential, bool) {
	var chosen *entry
	for i := range f.entries {
		e := &f.entries[i]
		if !e.isDefault && strings.EqualFold(e.machine, host) {
			chosen = e
			break
		}
	}
	if chosen == nil {
		for i := range f.entries {
			if f.entries[i].isDefault {
				chosen = &f.entries[i]
				break
			}
		}
	}
	if chosen == nil || chosen.login == "" || chosen.password == "" {
		return Credential{}, false
	}
	return Credential{Login: chosen.login, Password: chosen.password}, true
}

// Source is where a fetch reads the netrc file from. It is read each time Load
// is called, so a fetch sees the file as it is when the fetch runs. A nil
// *Source holds no credentials.
type Source struct {
	// path chooses the file, or reports that there is none to read.
	path func() (string, bool)
	// goos is the operating system whose habits apply to the file's permissions.
	goos string
	// warnings receives the warning about a file other users can read.
	warnings io.Writer
}

// FromEnvironment returns the Source this process's environment names: the file
// the lookup described in the package documentation chooses, on the operating
// system the process runs on.
func FromEnvironment() *Source {
	goos := runtime.GOOS
	return &Source{
		path: func() (string, bool) {
			return lookupPath(goos, os.Getenv, fileExists)
		},
		goos:     goos,
		warnings: os.Stderr,
	}
}

// FromFile returns a Source reading the file at path, whatever the environment
// names.
func FromFile(path string) *Source {
	return &Source{
		path:     func() (string, bool) { return path, true },
		goos:     runtime.GOOS,
		warnings: os.Stderr,
	}
}

// Path returns the file s reads, and false when there is none: a nil Source, or
// an environment naming no home directory.
func (s *Source) Path() (string, bool) {
	if s == nil || s.path == nil {
		return "", false
	}
	return s.path()
}

// Load reads the file s names. A file that does not exist, a nil Source, and an
// environment naming no home directory each yield a File holding no entries and
// no error. A file that exists and cannot be read, or does not follow the netrc
// grammar, is refused with ErrNetrcFileUnreadable.
func (s *Source) Load() (*File, error) {
	path, ok := s.Path()
	if !ok {
		return &File{}, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return &File{}, nil
		}
		return nil, readError(path, err)
	}
	s.warnIfOthersCanRead(path)
	entries, err := parse(path, string(data))
	if err != nil {
		return nil, err
	}
	return &File{entries: entries}, nil
}

// othersCanRead is the permission bits a file's group or other users read it by.
const othersCanRead fs.FileMode = 0o044

// warned holds every file the permission warning has already been written for,
// so a process resolving many sources writes it once per file.
var warned sync.Map

// warnIfOthersCanRead writes one warning, once per file for the life of the
// process, when users other than the file's owner can read it. Windows keeps
// access in lists the permission bits do not reflect, so the bits are not read
// there.
func (s *Source) warnIfOthersCanRead(path string) {
	if s.goos == "windows" || s.warnings == nil {
		return
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm()&othersCanRead == 0 {
		return
	}
	if _, already := warned.LoadOrStore(path, true); already {
		return
	}
	fmt.Fprintf(s.warnings, "warning: the netrc file %s can be read by other users of this machine, "+
		"and it holds credentials; make it readable by its owner alone (chmod 600 %s)\n", path, path)
}

// lookupPath chooses the netrc file for an operating system and an environment,
// both given as arguments so the choice on every operating system can be
// exercised on any of them. exists reports whether a file is present, and true
// for one whose presence cannot be determined, so the read that follows reports
// what is wrong with it rather than passing it over.
//
// It reports false only when the environment names no home directory and NETRC
// is unset: there is then no file to read.
func lookupPath(goos string, getenv func(string) string, exists func(string) bool) (string, bool) {
	if p := getenv("NETRC"); p != "" {
		return p, true
	}
	if goos == "windows" {
		home := getenv("USERPROFILE")
		if home == "" {
			return "", false
		}
		if legacy := joinPath(goos, home, "_netrc"); exists(legacy) {
			return legacy, true
		}
		return joinPath(goos, home, ".netrc"), true
	}
	home := getenv("HOME")
	if home == "" {
		return "", false
	}
	return joinPath(goos, home, ".netrc"), true
}

// joinPath joins a directory and a file name with the separator of goos, so a
// path chosen for one operating system reads the same when computed on another.
func joinPath(goos, dir, name string) string {
	sep := "/"
	if goos == "windows" {
		sep = `\`
	}
	return strings.TrimRight(dir, `/\`) + sep + name
}

// fileExists reports whether a file is present at path, and true where the
// answer is anything but a plain absence.
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
}
