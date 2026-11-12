// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio s.r.l.

package stringformat

import "fmt"

// checkDate is the date format. The JSON Schema specification defines it as the
// full-date production of RFC 3339, section 5.6:
//
//	full-date      = date-fullyear "-" date-month "-" date-mday
//	date-fullyear  = 4DIGIT
//	date-month     = 2DIGIT  ; 01-12
//	date-mday      = 2DIGIT  ; 01-28, 01-29, 01-30, 01-31 based on month/year
//
// The day is checked against the month it falls in, with February carrying a 29th
// day in a leap year of the Gregorian calendar, as RFC 3339 section 5.7 and its
// Appendix C state.
func checkDate(s string) string {
	return fullDate(s)
}

// checkDateTime is the date-time format. The JSON Schema specification defines it
// as the date-time production of RFC 3339, section 5.6:
//
//	date-time      = full-date "T" full-time
//	full-time      = partial-time time-offset
//	partial-time   = time-hour ":" time-minute ":" time-second [time-secfrac]
//	time-hour      = 2DIGIT  ; 00-23
//	time-minute    = 2DIGIT  ; 00-59
//	time-second    = 2DIGIT  ; 00-58, 00-59, 00-60 based on leap second rules
//	time-secfrac   = "." 1*DIGIT
//	time-offset    = "Z" / time-numoffset
//	time-numoffset = ("+" / "-") time-hour ":" time-minute
//
// The separator "T" and the offset "Z" may also be written in lower case, as the
// note under that production allows. A second of 60 is accepted only where the
// time, brought to UTC by its offset, is 23:59 — the one minute a leap second is
// inserted at. Whether a leap second was actually inserted on that date is a
// historical fact, not a syntactic one, and is not checked.
func checkDateTime(s string) string {
	const shape = "a date-time is written YYYY-MM-DDTHH:MM:SS, with optional fractional seconds, then Z or an offset such as +01:00"
	if len(s) < len("2006-01-02T15:04:05Z") {
		return shape
	}
	if reason := fullDate(s[:10]); reason != "" {
		return reason
	}
	if s[10] != 'T' && s[10] != 't' {
		return shape
	}
	rest := s[11:]
	if len(rest) < 8 || rest[2] != ':' || rest[5] != ':' ||
		!allDigits(rest[0:2]) || !allDigits(rest[3:5]) || !allDigits(rest[6:8]) {
		return shape
	}
	hour, minute, second := atoiDigits(rest[0:2]), atoiDigits(rest[3:5]), atoiDigits(rest[6:8])
	if hour > 23 {
		return fmt.Sprintf("hour %s is not between 00 and 23", rest[0:2])
	}
	if minute > 59 {
		return fmt.Sprintf("minute %s is not between 00 and 59", rest[3:5])
	}
	if second > 60 {
		return fmt.Sprintf("second %s is not between 00 and 60", rest[6:8])
	}
	rest = rest[8:]
	if len(rest) > 0 && rest[0] == '.' {
		n := 1
		for n < len(rest) && isDigit(rest[n]) {
			n++
		}
		if n == 1 {
			return "a fractional second carries at least one digit after the point"
		}
		rest = rest[n:]
	}
	offsetMinutes := 0
	switch {
	case rest == "Z" || rest == "z":
	case len(rest) == 6 && (rest[0] == '+' || rest[0] == '-') && rest[3] == ':' &&
		allDigits(rest[1:3]) && allDigits(rest[4:6]):
		oh, om := atoiDigits(rest[1:3]), atoiDigits(rest[4:6])
		if oh > 23 {
			return fmt.Sprintf("offset hour %s is not between 00 and 23", rest[1:3])
		}
		if om > 59 {
			return fmt.Sprintf("offset minute %s is not between 00 and 59", rest[4:6])
		}
		offsetMinutes = oh*60 + om
		if rest[0] == '-' {
			offsetMinutes = -offsetMinutes
		}
	default:
		return "a date-time ends in Z or an offset such as +01:00"
	}
	if second == 60 {
		utc := ((hour*60+minute-offsetMinutes)%(24*60) + 24*60) % (24 * 60)
		if utc != 23*60+59 {
			return "a leap second (second 60) falls only at 23:59 UTC"
		}
	}
	return ""
}

// fullDate checks RFC 3339's full-date production, reporting what is wrong.
func fullDate(s string) string {
	const shape = "a date is written YYYY-MM-DD"
	if len(s) != 10 || s[4] != '-' || s[7] != '-' ||
		!allDigits(s[0:4]) || !allDigits(s[5:7]) || !allDigits(s[8:10]) {
		return shape
	}
	year, month, day := atoiDigits(s[0:4]), atoiDigits(s[5:7]), atoiDigits(s[8:10])
	if month < 1 || month > 12 {
		return fmt.Sprintf("month %s is not between 01 and 12", s[5:7])
	}
	if last := daysIn(year, month); day < 1 || day > last {
		return fmt.Sprintf("day %s does not exist in month %s of year %s, which has %d days",
			s[8:10], s[5:7], s[0:4], last)
	}
	return ""
}

// daysIn is the number of days in a month of the Gregorian calendar.
func daysIn(year, month int) int {
	switch month {
	case 2:
		if year%4 == 0 && (year%100 != 0 || year%400 == 0) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	default:
		return 31
	}
}
