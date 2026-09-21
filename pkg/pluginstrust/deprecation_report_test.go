// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package pluginstrust

import (
	"strings"
	"testing"
)

// withdrawn builds a record entry carrying a withdrawal, so the tables below read
// as the case they describe rather than as struct assembly.
func withdrawn(coordinate, digest, severity, message string) TrustedPlugin {
	return TrustedPlugin{
		Plugin:          coordinate,
		ResolvedVersion: "1.0.0",
		Digest:          digest,
		Deprecation:     &Deprecation{Message: message, Severity: severity},
	}
}

// current builds a record entry carrying no withdrawal.
func current(coordinate, digest string) TrustedPlugin {
	return TrustedPlugin{Plugin: coordinate, ResolvedVersion: "1.0.0", Digest: digest}
}

func TestDeprecationFindingsReportsOnlyWithdrawnEntriesInCoordinateOrder(t *testing.T) {
	trust := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{
		withdrawn("linter/zeta", "sha256:z", "warning", "zeta is going"),
		current("linter/current", "sha256:c"),
		withdrawn("governance/alpha", "sha256:a", "retracted", "alpha is gone"),
	}}

	findings := trust.DeprecationFindings()
	if len(findings) != 2 {
		t.Fatalf("want a finding for each withdrawn entry and none for the current one; got %d: %+v", len(findings), findings)
	}
	// Coordinate order, so which finding a caller's policy acts on first does not
	// depend on the order entries happen to sit in the record.
	if findings[0].Plugin != "governance/alpha" || findings[1].Plugin != "linter/zeta" {
		t.Fatalf("findings are not in coordinate order: %+v", findings)
	}
	if findings[0].Severity != "retracted" || findings[0].Message != "alpha is gone" {
		t.Errorf("the finding must carry the recorded severity and message verbatim; got %+v", findings[0])
	}
}

func TestDeprecationFindingsIsSilentWhereThereIsNothingToReport(t *testing.T) {
	for _, tc := range []struct {
		name  string
		trust *Trust
	}{
		{"a nil record", nil},
		{"an empty record", &Trust{Version: CurrentVersion}},
		{"a record whose entries are all current", &Trust{
			Version: CurrentVersion,
			Plugins: []TrustedPlugin{current("linter/a", "sha256:a")},
		}},
		{"an entry addressing nothing", &Trust{
			Version: CurrentVersion,
			Plugins: []TrustedPlugin{withdrawn("", "sha256:a", "retracted", "unaddressed")},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if findings := tc.trust.DeprecationFindings(); len(findings) != 0 {
				t.Fatalf("want no findings; got %+v", findings)
			}
		})
	}
}

// TestDeprecationNoticeNamesItsSourceAndClaimsNoMoment pins the two properties of
// the sentence a user reads that are easy to lose. It must name where the fact came
// from and how to refresh it, and it must NOT carry an observation moment: this
// record holds no wall-clock stamp, so a notice claiming one would assert a currency
// the record cannot substantiate — and would be inventing it, since there is no
// stamp to read.
func TestDeprecationNoticeNamesItsSourceAndClaimsNoMoment(t *testing.T) {
	notice := DeprecationFinding{
		Plugin:   "linter/probe",
		Severity: "retracted",
		Message:  "superseded; migrate",
	}.Notice()

	for _, want := range []string{"linter/probe", "retracted", "superseded; migrate", "aggrega pull"} {
		if !strings.Contains(notice, want) {
			t.Errorf("the notice must carry %q; got %q", want, notice)
		}
	}
	if strings.Contains(notice, "as of") {
		t.Errorf("the notice must claim no observation moment, because the record carries none; got %q", notice)
	}
}

// TestDeprecationNoticeStatesTheSeverityItRecorded is the wording criterion: a
// notice names the severity value the publisher recorded, so a reader can match what
// they are told against the halt threshold they configure.
//
// The retracted row is the discriminating one. Reporting an excluded artifact as
// merely "deprecated" understates an exclusion as a discouragement, and it is the
// shape a notice falls into by default because "deprecated" reads as the general
// word for all three.
func TestDeprecationNoticeStatesTheSeverityItRecorded(t *testing.T) {
	for _, tc := range []struct {
		severity string
		want     string
		absent   string
	}{
		{severity: "retracted", want: "is retracted:", absent: "deprecated"},
		{severity: "warning", want: "is deprecated (warning):"},
		{severity: "error", want: "is deprecated (error):"},
	} {
		t.Run(tc.severity, func(t *testing.T) {
			notice := DeprecationFinding{Plugin: "linter/probe", Severity: tc.severity, Message: "m"}.Notice()
			if !strings.Contains(notice, tc.want) {
				t.Errorf("want the notice to state %q; got %q", tc.want, notice)
			}
			if tc.absent != "" && strings.Contains(notice, tc.absent) {
				t.Errorf("a %s artifact must not be reported as %q; got %q", tc.severity, tc.absent, notice)
			}
		})
	}
}

// TestEqualComparesTheWithdrawalRatherThanItsAddress is the discriminating test for
// the trap the record walked into the moment it gained a pointer field: `==` still
// compiles and still type-checks, and it compares the POINTER. Two entries carrying
// identical withdrawals at different addresses are the same record, and a comparison
// that said otherwise would rewrite the committed file on every pull.
func TestEqualComparesTheWithdrawalRatherThanItsAddress(t *testing.T) {
	a := withdrawn("linter/probe", "sha256:a", "retracted", "gone")
	b := withdrawn("linter/probe", "sha256:a", "retracted", "gone")

	if a.Deprecation == b.Deprecation {
		t.Fatal("the fixture is not discriminating: both entries share one withdrawal address, " +
			"so this could not tell a value comparison from an address one")
	}
	if !a.Equal(b) {
		t.Error("two entries recording the same thing must be equal however their withdrawals are addressed")
	}

	for _, tc := range []struct {
		name  string
		other TrustedPlugin
	}{
		{"a different severity", withdrawn("linter/probe", "sha256:a", "warning", "gone")},
		{"a different message", withdrawn("linter/probe", "sha256:a", "retracted", "different")},
		{"a different digest", withdrawn("linter/probe", "sha256:b", "retracted", "gone")},
		{"no withdrawal at all", current("linter/probe", "sha256:a")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if a.Equal(tc.other) {
				t.Errorf("%s must not compare equal", tc.name)
			}
		})
	}

	if !current("linter/probe", "sha256:a").Equal(current("linter/probe", "sha256:a")) {
		t.Error("two entries that both record no withdrawal must be equal")
	}
}

// TestRecordDeprecationFollowsArtifactIdentity is the lifecycle rule at the level it
// is decided. A withdrawal is a fact about ONE artifact, so it is recorded against
// the entry whose recorded digest is the digest just resolved and against no other —
// which is what keeps a fact about one set of bytes from being written against a
// different set.
func TestRecordDeprecationFollowsArtifactIdentity(t *testing.T) {
	d := &Deprecation{Message: "gone", Severity: "retracted"}

	t.Run("records onto the entry whose digest matches", func(t *testing.T) {
		trust := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{current("linter/probe", "sha256:a")}}
		if !trust.RecordDeprecation("linter/probe", "sha256:a", d) {
			t.Fatal("a withdrawal discovered for the artifact the entry records must be recorded")
		}
		held, _ := trust.Find("linter/probe")
		if held.Deprecation == nil || held.Deprecation.Severity != "retracted" {
			t.Fatalf("the withdrawal was not recorded: %+v", held)
		}
		// The identity is untouched: recording a fact about the recorded artifact is
		// not adoption, which is the whole reason a plain pull may do it.
		if held.ResolvedVersion != "1.0.0" || held.Digest != "sha256:a" {
			t.Errorf("recording a withdrawal must not move the identity; got %+v", held)
		}
		// It stores a copy rather than the caller's pointer, so a caller reusing its
		// value cannot reach back into the record.
		d.Severity = "warning"
		if held, _ = trust.Find("linter/probe"); held.Deprecation.Severity != "retracted" {
			t.Error("the record must hold a copy of the withdrawal, not the caller's value")
		}
		d.Severity = "retracted"
	})

	t.Run("refuses an entry whose digest differs", func(t *testing.T) {
		trust := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{current("linter/probe", "sha256:a")}}
		if trust.RecordDeprecation("linter/probe", "sha256:different", d) {
			t.Fatal("a withdrawal discovered for different bytes must not be written against these bytes")
		}
		if held, _ := trust.Find("linter/probe"); held.Deprecation != nil {
			t.Fatalf("nothing must have been recorded; got %+v", held.Deprecation)
		}
	})

	t.Run("a second identical recording is not a change", func(t *testing.T) {
		trust := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{
			withdrawn("linter/probe", "sha256:a", "retracted", "gone"),
		}}
		if trust.RecordDeprecation("linter/probe", "sha256:a", d) {
			t.Fatal("re-recording the withdrawal already held must report no change, " +
				"or every pull would rewrite the committed file")
		}
	})

	t.Run("nothing clears a withdrawal", func(t *testing.T) {
		trust := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{
			withdrawn("linter/probe", "sha256:a", "retracted", "gone"),
		}}
		if trust.RecordDeprecation("linter/probe", "sha256:a", nil) {
			t.Fatal("a nil withdrawal must record nothing and report no change")
		}
		if held, _ := trust.Find("linter/probe"); held.Deprecation == nil {
			t.Fatal("a withdrawal that has stopped being published is not chased: " +
				"it leaves only with the record that carries it")
		}
	})

	for _, tc := range []struct{ name, coordinate, digest string }{
		{"an unheld coordinate", "linter/absent", "sha256:a"},
		{"an empty coordinate", "", "sha256:a"},
	} {
		t.Run(tc.name+" records nothing", func(t *testing.T) {
			trust := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{current("linter/probe", "sha256:a")}}
			if trust.RecordDeprecation(tc.coordinate, tc.digest, d) {
				t.Fatalf("%s must record nothing", tc.name)
			}
		})
	}

	t.Run("a nil record records nothing", func(t *testing.T) {
		var trust *Trust
		if trust.RecordDeprecation("linter/probe", "sha256:a", d) {
			t.Fatal("a nil record holds nothing and must record nothing")
		}
	})
}

// TestWriteOmitsAnAbsentWithdrawalEntirely is the byte-identity criterion at the
// level the bytes are produced: a record for a plugin nobody has withdrawn must
// carry no withdrawal key at all, so adding this field moved no byte of any record
// written before it existed.
//
// It also covers the canonicaliser's documented hazard, which this field is the
// first to trigger: a pointer field must be CLONED into the canonical form, because
// a struct copy shares pointers — so without the clone the writer's output and the
// caller's record would address one withdrawal between them.
func TestWriteOmitsAnAbsentWithdrawalEntirely(t *testing.T) {
	plain := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{current("linter/probe", "sha256:a")}}
	out, err := CanonicalBytes(plain)
	if err != nil {
		t.Fatalf("CanonicalBytes: %v", err)
	}
	if body := bodyOf(string(out)); strings.Contains(body, "deprecation") {
		t.Fatalf("an entry carrying no withdrawal must emit no withdrawal key;\ngot:\n%s", body)
	}

	source := &Trust{Version: CurrentVersion, Plugins: []TrustedPlugin{
		withdrawn("linter/probe", "sha256:a", "retracted", "gone"),
	}}
	canonical := canonicalise(source)
	if canonical.Plugins[0].Deprecation == source.Plugins[0].Deprecation {
		t.Fatal("the canonical form shares the caller's withdrawal pointer, so a mutation of either reaches the other")
	}
	canonical.Plugins[0].Deprecation.Severity = "warning"
	if source.Plugins[0].Deprecation.Severity != "retracted" {
		t.Error("mutating the canonical form reached back into the caller's record")
	}
}

// bodyOf strips the header comment block, so an assertion about the emitted KEYS is
// not satisfied — or defeated — by the prose above them, which mentions the same
// words on purpose.
func bodyOf(written string) string {
	idx := strings.Index(written, "\nplugins:")
	if idx < 0 {
		return written
	}
	return written[idx:]
}
