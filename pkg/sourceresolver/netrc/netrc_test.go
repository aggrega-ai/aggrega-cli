// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package netrc

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	cerrors "github.com/aggrega-ai/aggrega-cli/pkg/errors"
)

// writeNetrc writes body to a netrc file readable by its owner alone and returns
// its path.
func writeNetrc(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "netrc")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write the netrc file: %v", err)
	}
	return path
}

// TestTheNetrcFileAnswersEachRequestByItsHost reads netrc files through the
// package's own Load and asks them for the credential of one URL per row: the
// entry naming the URL's host answers, whatever its port and letter case; the
// default entry answers a host nothing names; an entry missing its login or its
// password answers nothing; and a URL the file answers no request for — another
// scheme, or one carrying a user of its own — gets nothing.
func TestTheNetrcFileAnswersEachRequestByItsHost(t *testing.T) {
	t.Parallel()

	const file = `# a comment line, ignored
machine charts.example.com login chartuser password charttoken
machine CASE.example.com
  login caseuser
  password casetoken
machine twice.example.com login first password firsttoken
machine twice.example.com login second password secondtoken
machine halfway.example.com login onlylogin
machine quoted.example.com login "a user" password "p\"w\\d"
macdef init
login macrouser password macrotoken
machine inmacro.example.com login no password no

machine 127.0.0.1 login loopuser password looptoken
default login anyuser password anytoken
`
	f, err := FromFile(writeNetrc(t, file)).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	for _, row := range []struct {
		name, url   string
		want        Credential
		wantNothing bool
	}{
		{name: "the entry naming the host", url: "https://charts.example.com/repo/index.yaml",
			want: Credential{"chartuser", "charttoken"}},
		{name: "the port plays no part", url: "https://charts.example.com:8443/chart.tgz",
			want: Credential{"chartuser", "charttoken"}},
		{name: "plain http is answered as https is", url: "http://charts.example.com/chart.tgz",
			want: Credential{"chartuser", "charttoken"}},
		{name: "letter case plays no part", url: "https://case.EXAMPLE.com/x",
			want: Credential{"caseuser", "casetoken"}},
		{name: "the first entry naming a host wins", url: "https://twice.example.com/x",
			want: Credential{"first", "firsttoken"}},
		{name: "quoted words and their escapes", url: "https://quoted.example.com/x",
			want: Credential{"a user", `p"w\d`}},
		{name: "a loopback address", url: "http://127.0.0.1:41234/fragment.yaml",
			want: Credential{"loopuser", "looptoken"}},
		{name: "a host nothing names takes the default entry", url: "https://other.example.org/x",
			want: Credential{"anyuser", "anytoken"}},
		{name: "a macro body is not read as entries", url: "https://inmacro.example.com/x",
			want: Credential{"anyuser", "anytoken"}},
		{name: "an entry without a password answers nothing", url: "https://halfway.example.com/x",
			wantNothing: true},
		{name: "a scheme other than http or https", url: "ssh://charts.example.com/repo.git",
			wantNothing: true},
		{name: "a URL carrying a user of its own", url: "https://someone:secret@charts.example.com/x",
			wantNothing: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			got, ok := f.ForURL(row.url)
			if row.wantNothing {
				if ok {
					t.Fatalf("ForURL(%q) = %+v; want no credential", row.url, got)
				}
				return
			}
			if !ok || got != row.want {
				t.Fatalf("ForURL(%q) = %+v, %v; want %+v", row.url, got, ok, row.want)
			}
		})
	}
}

// TestAMissingNetrcFileHoldsNoCredentials holds that a netrc file that does not
// exist is no error, and that a nil Source reads nothing at all.
func TestAMissingNetrcFileHoldsNoCredentials(t *testing.T) {
	t.Parallel()

	f, err := FromFile(filepath.Join(t.TempDir(), "absent")).Load()
	if err != nil {
		t.Fatalf("a missing netrc file was refused: %v", err)
	}
	if _, ok := f.ForURL("https://charts.example.com/x"); ok {
		t.Error("a missing netrc file answered a request")
	}

	var none *Source
	f, err = none.Load()
	if err != nil {
		t.Fatalf("a nil Source was refused: %v", err)
	}
	if _, ok := f.ForURL("https://charts.example.com/x"); ok {
		t.Error("a nil Source answered a request")
	}
}

// netrcSecret is a word written into every malformed file below, in the
// position a credential takes. No refusal may repeat it.
const netrcSecret = "s3cr3t-never-printed"

// TestAMalformedNetrcFileIsRefusedNamingTheFileAndLine reads one malformed netrc
// file per row, each carrying one fault, and holds that it is refused with the
// unreadable-netrc condition naming the file and the line of the fault — and that
// the refusal repeats nothing the file holds.
func TestAMalformedNetrcFileIsRefusedNamingTheFileAndLine(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name, body string
		line       int
	}{
		{name: "a misspelt keyword",
			body: "machine a.example.com\n  login user\n  pasword " + netrcSecret + "\n", line: 3},
		{name: "a keyword with no value at the end of the file",
			body: "machine a.example.com login user password " + netrcSecret + "\nmachine", line: 2},
		{name: "a login before any machine",
			body: "\n\nlogin " + netrcSecret + "\n", line: 3},
		{name: "a quoted word left open",
			body: "machine a.example.com login user\npassword \"" + netrcSecret + "\n", line: 2},
		{name: "a stray word after a complete entry",
			body: "machine a.example.com login user password " + netrcSecret + " " + netrcSecret + "\n", line: 1},
	} {
		t.Run(row.name, func(t *testing.T) {
			path := writeNetrc(t, row.body)

			var f *File
			var err error
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("reading a malformed netrc file panicked: %v", r)
					}
				}()
				f, err = FromFile(path).Load()
			}()

			if err == nil {
				t.Fatalf("a malformed netrc file was read; got %+v", f)
			}
			if !errors.Is(err, ErrNetrcFileUnreadable()) {
				t.Fatalf("the refusal is %v; want the unreadable-netrc condition", err)
			}
			if code, _ := cerrors.ShortCodeOf(err); code != "E0360" {
				t.Errorf("the refusal carries short code %q; want E0360", code)
			}
			msg := err.Error()
			if !strings.Contains(msg, path) {
				t.Errorf("the refusal does not name the file %s: %q", path, msg)
			}
			if want := "line " + strconv.Itoa(row.line) + ","; !strings.Contains(msg, want) {
				t.Errorf("the refusal does not name %s: %q", want, msg)
			}
			if strings.Contains(msg, netrcSecret) {
				t.Errorf("the refusal repeats a word the file holds: %q", msg)
			}
		})
	}
}

// TestANetrcFileThatCannotBeReadIsRefused holds that a netrc path naming
// something that exists and cannot be read as a file is refused with the same
// condition, naming the path.
func TestANetrcFileThatCannotBeReadIsRefused(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	_, err := FromFile(dir).Load()
	if !errors.Is(err, ErrNetrcFileUnreadable()) {
		t.Fatalf("reading a directory as the netrc file gave %v; want the unreadable-netrc condition", err)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Errorf("the refusal does not name %s: %q", dir, err.Error())
	}
}

// TestTheNetrcFileTheEnvironmentNames drives the lookup that chooses the netrc
// file, with the operating system and the environment as its arguments. No
// Windows host runs this suite, so the Windows rows are exercised on whatever
// host runs it, with the Windows path shape computed rather than observed.
func TestTheNetrcFileTheEnvironmentNames(t *testing.T) {
	t.Parallel()

	for _, row := range []struct {
		name     string
		goos     string
		env      map[string]string
		existing []string
		want     string
		wantNone bool
	}{
		{name: "NETRC wins on Linux", goos: "linux",
			env: map[string]string{"NETRC": "/etc/aggrega/netrc", "HOME": "/home/u"}, want: "/etc/aggrega/netrc"},
		{name: "the home directory's .netrc on Linux", goos: "linux",
			env: map[string]string{"HOME": "/home/u"}, want: "/home/u/.netrc"},
		{name: "the home directory's .netrc on macOS", goos: "darwin",
			env: map[string]string{"HOME": "/Users/u/"}, want: "/Users/u/.netrc"},
		{name: "no home directory and no NETRC names no file", goos: "linux",
			env: map[string]string{}, wantNone: true},
		{name: "NETRC wins on Windows", goos: "windows",
			env:      map[string]string{"NETRC": `D:\creds\netrc`, "USERPROFILE": `C:\Users\u`},
			existing: []string{`C:\Users\u\_netrc`}, want: `D:\creds\netrc`},
		{name: "_netrc in the profile on Windows when it exists", goos: "windows",
			env:      map[string]string{"USERPROFILE": `C:\Users\u`},
			existing: []string{`C:\Users\u\_netrc`, `C:\Users\u\.netrc`}, want: `C:\Users\u\_netrc`},
		{name: ".netrc in the profile on Windows when _netrc does not exist", goos: "windows",
			env: map[string]string{"USERPROFILE": `C:\Users\u\`}, want: `C:\Users\u\.netrc`},
		{name: "Windows reads USERPROFILE, not HOME", goos: "windows",
			env: map[string]string{"HOME": "/home/u"}, wantNone: true},
	} {
		t.Run(row.name, func(t *testing.T) {
			getenv := func(k string) string { return row.env[k] }
			exists := func(p string) bool {
				for _, e := range row.existing {
					if e == p {
						return true
					}
				}
				return false
			}
			got, ok := lookupPath(row.goos, getenv, exists)
			if row.wantNone {
				if ok {
					t.Fatalf("lookupPath chose %q; want no file", got)
				}
				return
			}
			if !ok || got != row.want {
				t.Fatalf("lookupPath = %q, %v; want %q", got, ok, row.want)
			}
		})
	}
}

// TestNETRCPointsTheProductionLookupAtAFile drives FromEnvironment — the Source
// the production caller hands every resolution — with NETRC naming a file, and
// holds that the file it names is the one read. It sets the process environment,
// so it does not run in parallel.
func TestNETRCPointsTheProductionLookupAtAFile(t *testing.T) {
	path := writeNetrc(t, "machine charts.example.com login envuser password envtoken\n")
	t.Setenv("NETRC", path)
	t.Setenv("HOME", t.TempDir())

	src := FromEnvironment()
	if got, ok := src.Path(); !ok || got != path {
		t.Fatalf("FromEnvironment reads %q, %v; want %q", got, ok, path)
	}
	f, err := src.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := f.ForURL("https://charts.example.com/x")
	if !ok || got != (Credential{"envuser", "envtoken"}) {
		t.Fatalf("the file NETRC names answered %+v, %v; want its entry", got, ok)
	}
}

// TestANetrcFileOthersCanReadDrawsOneWarning holds that, on a host other than
// Windows, a netrc file its group or other users can read is still read and
// draws one warning naming it — once for the life of the process — while a file
// only its owner can read draws none, and Windows draws none at all.
func TestANetrcFileOthersCanReadDrawsOneWarning(t *testing.T) {
	t.Parallel()

	const body = "machine a.example.com login user password token\n"
	open := writeNetrc(t, body)
	if err := os.Chmod(open, 0o644); err != nil {
		t.Fatal(err)
	}
	private := writeNetrc(t, body)
	onWindows := writeNetrc(t, body)
	if err := os.Chmod(onWindows, 0o644); err != nil {
		t.Fatal(err)
	}

	read := func(path, goos string) string {
		t.Helper()
		var out bytes.Buffer
		src := &Source{path: func() (string, bool) { return path, true }, goos: goos, warnings: &out}
		f, err := src.Load()
		if err != nil {
			t.Fatalf("Load(%s): %v", path, err)
		}
		if _, ok := f.ForURL("https://a.example.com/x"); !ok {
			t.Errorf("the file %s was not read after its warning", path)
		}
		return out.String()
	}

	first := read(open, "linux")
	if !strings.Contains(first, "warning:") || !strings.Contains(first, open) {
		t.Errorf("a netrc file others can read drew %q; want a warning naming %s", first, open)
	}
	if strings.Contains(first, "token") {
		t.Errorf("the warning repeats what the file holds: %q", first)
	}
	if again := read(open, "linux"); again != "" {
		t.Errorf("the same file drew a second warning: %q", again)
	}
	if got := read(private, "linux"); got != "" {
		t.Errorf("a netrc file only its owner can read drew %q; want nothing", got)
	}
	if got := read(onWindows, "windows"); got != "" {
		t.Errorf("on Windows the permission bits drew %q; want nothing", got)
	}
}
