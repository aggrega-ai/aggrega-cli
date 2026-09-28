// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

package stringformat

import (
	"strings"
	"testing"
)

// checkRow is one value driven through one format, with what the row pins.
type checkRow struct {
	format, value string
	valid         bool
	why           string
}

// checkRows drives every supported format with values that conform and values
// that do not. A format with no conforming row or no refusing row here is caught
// by TestEveryFormatIsDrivenBothWays.
var checkRows = []checkRow{
	// email — RFC 5321 Mailbox.
	{"email", "a@example.com", true, "a dot-string local part and a domain"},
	{"email", "first.last+tag@sub.example.co", true, "dots and atext in the local part"},
	{"email", "user@localhost", true, "a single-label domain"},
	{"email", `"john doe"@example.com`, true, "a quoted local part carrying a space"},
	{"email", `"a\"b"@example.com`, true, "a quoted-pair inside a quoted local part"},
	{"email", "user@[192.0.2.1]", true, "an IPv4 address literal"},
	{"email", "user@[IPv6:2001:db8::1]", true, "an IPv6 address literal"},
	{"email", "not an email {\n}", false, "spaces and no @"},
	{"email", "user", false, "no @"},
	{"email", "@example.com", false, "an empty local part"},
	{"email", "user@", false, "an empty domain"},
	{"email", "a..b@example.com", false, "an empty atom"},
	{"email", ".a@example.com", false, "a local part beginning with a dot"},
	{"email", "user@-example.com", false, "a domain label beginning with a hyphen"},
	{"email", "user@exa_mple.com", false, "an underscore in the domain"},
	{"email", "Joe <joe@example.com>", false, "a display name, which the Mailbox production does not carry"},
	{"email", "user@[256.0.0.1]", false, "an IPv4 literal part over 255"},
	{"email", "user@[IPv6:zz::1]", false, "an IPv6 literal that is not an address"},
	{"email", "üser@example.com", false, "a non-ASCII local part"},
	{"email", `"unterminated@example.com`, false, "an unclosed quoted local part"},

	// uri — RFC 3986 URI, scheme required.
	{"uri", "https://example.com", true, "scheme and authority"},
	{"uri", "https://user:pw@example.com:8443/a/b?q=1&r=2#frag", true, "every component"},
	{"uri", "urn:isbn:0451450523", true, "a rootless path"},
	{"uri", "mailto:someone@example.com", true, "an @ in a rootless path"},
	{"uri", "file:///etc/hosts", true, "an empty authority"},
	{"uri", "s3://my-bucket/backups/", true, "a non-registered scheme"},
	{"uri", "http://[2001:db8::1]:80/", true, "an IPv6 host"},
	{"uri", "http://example.com/a%20b", true, "a percent-encoded space"},
	{"uri", "::::", false, "no scheme"},
	{"uri", "/relative/path", false, "a relative reference"},
	{"uri", "example.com", false, "no colon at all"},
	{"uri", "1http://example.com", false, "a scheme beginning with a digit"},
	{"uri", "http://example.com/a b", false, "an unencoded space"},
	{"uri", "http://example.com/%zz", false, "a percent sign opening no escape"},
	{"uri", "http://example.com:80a/", false, "a port with a letter"},
	{"uri", "http://[::1/", false, "an unclosed bracketed host"},
	{"uri", "http://[not-v6]/", false, "a bracketed host that is not an address"},
	{"uri", "http://exa mple.com/", false, "a space in the host"},
	{"uri", "http://example.com/é", false, "an unencoded non-ASCII character"},

	// hostname — RFC 1123 section 2.1.
	{"hostname", "www.example.com", true, "several labels"},
	{"hostname", "localhost", true, "a single label"},
	{"hostname", "1host", true, "a label beginning with a digit"},
	{"hostname", "xn--4gbwdl.xn--wgbh1c", true, "Punycode-encoded labels"},
	{"hostname", "db-primary.data.svc.cluster.local", true, "a cluster service name"},
	{"hostname", strings.Repeat("a", 63) + ".com", true, "a label at the 63-character limit"},
	{"hostname", "bad host {\n}", false, "spaces and braces"},
	{"hostname", "*", false, "a wildcard"},
	{"hostname", "", false, "the empty string"},
	{"hostname", "-host", false, "a label beginning with a hyphen"},
	{"hostname", "host-", false, "a label ending with a hyphen"},
	{"hostname", "host_name", false, "an underscore"},
	{"hostname", "example.com.", false, "a trailing dot"},
	{"hostname", "a..b", false, "an empty label"},
	{"hostname", strings.Repeat("a", 64) + ".com", false, "a label over 63 characters"},
	{"hostname", strings.Repeat("a.", 127) + "ab", false, "a name over 253 characters"},

	// ipv4 — RFC 2673 dotted-quad.
	{"ipv4", "192.0.2.1", true, "four parts"},
	{"ipv4", "0.0.0.0", true, "all zero"},
	{"ipv4", "255.255.255.255", true, "every part at its maximum"},
	{"ipv4", "010.0.0.1", true, "a leading zero, which the decbyte production admits"},
	{"ipv4", "256.0.0.1", false, "a part over 255"},
	{"ipv4", "1.2.3", false, "three parts"},
	{"ipv4", "1.2.3.4.5", false, "five parts"},
	{"ipv4", "1.2.3.a", false, "a letter"},
	{"ipv4", "1234.1.1.1", false, "four digits in a part"},
	{"ipv4", "1.2..4", false, "an empty part"},

	// ipv6 — RFC 4291 section 2.2.
	{"ipv6", "2001:db8:0:0:0:0:2:1", true, "eight groups"},
	{"ipv6", "2001:db8::2:1", true, "a compressed run"},
	{"ipv6", "::", true, "the unspecified address"},
	{"ipv6", "::1", true, "the loopback address"},
	{"ipv6", "::ffff:192.0.2.1", true, "an IPv4 tail after a compressed run"},
	{"ipv6", "0:0:0:0:0:0:13.1.68.3", true, "an IPv4 tail after six groups"},
	{"ipv6", "FE80::ABCD", true, "upper-case digits"},
	{"ipv6", "2001:db8::2::1", false, "two compressed runs"},
	{"ipv6", "2001:db8:::1", false, "three colons in a row"},
	{"ipv6", "1:2:3:4:5:6:7", false, "seven groups without a compressed run"},
	{"ipv6", "1:2:3:4:5:6:7:8:9", false, "nine groups"},
	{"ipv6", "1:2:3:4:5:6:7::8", false, "a compressed run with eight other groups"},
	{"ipv6", "fe80::1%eth0", false, "a zone"},
	{"ipv6", "2001:db8::/32", false, "a prefix length"},
	{"ipv6", "12345::", false, "a five-digit group"},
	{"ipv6", "g::1", false, "a non-hexadecimal digit"},
	{"ipv6", "192.0.2.1", false, "an IPv4 address"},
	{"ipv6", "::ffff:256.0.0.1", false, "an IPv4 tail with a part over 255"},
	{"ipv6", "", false, "the empty string"},

	// uuid — RFC 4122 section 3.
	{"uuid", "2eb8aa08-aa98-11ea-b4aa-73b441d16380", true, "lower case"},
	{"uuid", "2EB8AA08-AA98-11EA-B4AA-73B441D16380", true, "upper case"},
	{"uuid", "00000000-0000-0000-0000-000000000000", true, "the nil UUID"},
	{"uuid", "2eb8aa08-aa98-f1ea-b4aa-73b441d16380", true, "a version digit the RFC does not define"},
	{"uuid", "2eb8aa08aa9811eab4aa73b441d16380", false, "no hyphens"},
	{"uuid", "2eb8aa08-aa98-11ea-b4aa-73b441d1638", false, "one digit short"},
	{"uuid", "2eb8aa0-8aa98-11ea-b4aa-73b441d16380", false, "a hyphen in the wrong place"},
	{"uuid", "2eb8aa08-aa98-11ea-b4aa-73b441d1638g", false, "a non-hexadecimal digit"},

	// date-time — RFC 3339 section 5.6.
	{"date-time", "2026-09-28T12:34:56Z", true, "UTC"},
	{"date-time", "2026-09-28t12:34:56z", true, "lower-case separator and offset"},
	{"date-time", "2026-09-28T12:34:56.789+02:00", true, "fractional seconds and an offset"},
	{"date-time", "1998-12-31T23:59:60Z", true, "a leap second at 23:59 UTC"},
	{"date-time", "1998-12-31T15:59:60.123-08:00", true, "a leap second at 23:59 UTC written with an offset"},
	{"date-time", "2024-02-29T00:00:00Z", true, "the 29th of February in a leap year"},
	{"date-time", "yesterday", false, "not a timestamp"},
	{"date-time", "2026-09-28 12:34:56Z", false, "a space separator"},
	{"date-time", "2026-09-28T12:34:56", false, "no offset"},
	{"date-time", "2026-09-28T24:00:00Z", false, "hour 24"},
	{"date-time", "2026-09-28T12:60:00Z", false, "minute 60"},
	{"date-time", "1998-12-31T23:59:61Z", false, "second 61"},
	{"date-time", "1998-12-31T23:58:60Z", false, "a leap second on the wrong minute"},
	{"date-time", "1998-12-31T22:59:60Z", false, "a leap second on the wrong hour"},
	{"date-time", "2026-09-28T12:34:56.Z", false, "a point with no fraction digits"},
	{"date-time", "2026-09-28T12:34:56+0200", false, "an offset without its colon"},
	{"date-time", "2026-09-28T12:34:56+24:00", false, "an offset hour of 24"},
	{"date-time", "2023-02-29T00:00:00Z", false, "the 29th of February in a common year"},

	// date — RFC 3339 full-date.
	{"date", "2026-09-28", true, "an ordinary date"},
	{"date", "2000-02-29", true, "the 29th of February in a year divisible by 400"},
	{"date", "2026-12-31", true, "the last day of the year"},
	{"date", "1900-02-29", false, "the 29th of February in a year divisible by 100 and not 400"},
	{"date", "2026-04-31", false, "the 31st of a 30-day month"},
	{"date", "2026-13-01", false, "month 13"},
	{"date", "2026-00-10", false, "month 00"},
	{"date", "2026-01-00", false, "day 00"},
	{"date", "2026-9-28", false, "a one-digit month"},
	{"date", "2026-09-28T00:00:00Z", false, "a date-time"},
	{"date", "yesterday", false, "not a date"},
}

// TestCheck drives checkRows through Check.
func TestCheck(t *testing.T) {
	t.Parallel()
	for _, r := range checkRows {
		err := Check(r.format, r.value)
		if r.valid && err != nil {
			t.Errorf("%s %q (%s): want valid; got %v", r.format, r.value, r.why, err)
		}
		if !r.valid {
			if err == nil {
				t.Errorf("%s %q (%s): want refused; got valid", r.format, r.value, r.why)
				continue
			}
			// The refusal names the value and the format, whatever the reason.
			if msg := err.Error(); !strings.Contains(msg, "is not a valid "+r.format+":") {
				t.Errorf("%s %q: refusal %q does not name the format", r.format, r.value, msg)
			}
		}
	}
}

// TestEveryFormatIsDrivenBothWays holds checkRows to the checker set: a format
// added with no conforming and no refusing row is named here rather than shipped
// undriven, and a row naming a format outside the set is named too.
func TestEveryFormatIsDrivenBothWays(t *testing.T) {
	t.Parallel()
	driven := map[string]map[bool]bool{}
	for _, r := range checkRows {
		if driven[r.format] == nil {
			driven[r.format] = map[bool]bool{}
		}
		driven[r.format][r.valid] = true
	}
	for _, name := range Names() {
		if !driven[name][true] || !driven[name][false] {
			t.Errorf("format %q is not driven with both a conforming and a refused value", name)
		}
	}
	for name := range driven {
		if !Supported(name) {
			t.Errorf("a row names %q, which is not a supported format", name)
		}
	}
}

// TestNamesIsTheClosedSetInOrder pins the supported set and its order, which is
// the order every listing of it shows an author.
func TestNamesIsTheClosedSetInOrder(t *testing.T) {
	t.Parallel()
	want := []string{"email", "uri", "hostname", "ipv4", "ipv6", "uuid", "date-time", "date"}
	got := Names()
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("Names() = %v; want %v", got, want)
	}
	got[0] = "mutated"
	if Names()[0] != "email" {
		t.Errorf("Names() handed out its own backing array")
	}
}

// TestUnsupportedNameIsRefusedListingTheSet pins the one wording of an
// unsupported name, from both entry points.
func TestUnsupportedNameIsRefusedListingTheSet(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"emial", "password", "", "URI"} {
		if Supported(name) {
			t.Errorf("Supported(%q) = true; want false", name)
		}
		want := `format "` + name + `" is not supported; the supported formats are email, uri, hostname, ipv4, ipv6, uuid, date-time, date`
		if err := CheckName(name); err == nil || err.Error() != want {
			t.Errorf("CheckName(%q) = %v; want %q", name, err, want)
		}
		if err := Check(name, "anything"); err == nil || err.Error() != want {
			t.Errorf("Check(%q, …) = %v; want %q", name, err, want)
		}
	}
	for _, name := range Names() {
		if err := CheckName(name); err != nil {
			t.Errorf("CheckName(%q) = %v; want nil", name, err)
		}
	}
}

// TestRefusalNamesValueFormatAndReason pins the shape of a value refusal.
func TestRefusalNamesValueFormatAndReason(t *testing.T) {
	t.Parallel()
	err := Check("uri", "::::")
	want := `"::::" is not a valid uri: a URI begins with a scheme followed by a colon, such as https:`
	if err == nil || err.Error() != want {
		t.Fatalf("Check(uri, ::::) = %v; want %q", err, want)
	}
}
