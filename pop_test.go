// Copyright 2026 The Cockroach Authors.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or
// implied. See the License for the specific language governing
// permissions and limitations under the License.

package datadriven

import (
	"fmt"
	"math/big"
	"net/netip"
	"reflect"
	"strings"
	"testing"
	"time"
)

// popHandler implements directives which exercise PopArg, PopArgs and
// ScanArgs; the output includes the remaining arguments.
func popHandler(t testing.TB, d *TestData) string {
	var buf strings.Builder
	var i int
	var s string
	switch d.Cmd {
	case "pop-all":
		for len(d.Args) > 0 {
			fmt.Fprintf(&buf, "%q\n", d.PopArg())
		}
	case "pop-twice":
		d.PopArg()
		d.PopArg()
	case "pop-then-flag":
		// The positional argument has the same name as the flag.
		fmt.Fprintf(&buf, "popped %q\n", d.PopArg())
		fmt.Fprintf(&buf, "x=%d\n", d.FlagValue[int]("x"))
	case "pop-args":
		d.PopArgs(&i, &s)
		fmt.Fprintf(&buf, "i=%d s=%q\n", i, s)
	case "pop-args-str":
		d.PopArgs(&s)
		fmt.Fprintf(&buf, "s=%q\n", s)
	case "pop-args-none":
		d.PopArgs()
	case "pop-then-pop-args":
		fmt.Fprintf(&buf, "popped %q\n", d.PopArg())
		d.PopArgs(&i)
		fmt.Fprintf(&buf, "i=%d\n", i)
	case "pop-args-then-pop":
		d.PopArgs(&i)
		fmt.Fprintf(&buf, "i=%d\n", i)
		fmt.Fprintf(&buf, "popped %q\n", d.PopArg())
	case "scan-args":
		d.ScanArgs(&i, &s)
		fmt.Fprintf(&buf, "i=%d s=%q\n", i, s)
	case "scan-args-none":
		d.ScanArgs()
	case "pop-args-dur":
		var dur time.Duration
		d.PopArgs(&dur, &s)
		fmt.Fprintf(&buf, "dur=%s s=%q\n", dur, s)
	case "pop-args-named-dur":
		type namedDur time.Duration
		var dur namedDur
		d.PopArgs(&dur)
		fmt.Fprintf(&buf, "dur=%d\n", dur)
	case "pop-args-types":
		var u uint8
		var f float64
		var b bool
		var n namedInt
		d.PopArgs(&i, &u, &f, &b, &n)
		fmt.Fprintf(&buf, "i=%d u=%d f=%g b=%t n=%d\n", i, u, f, b, n)
	case "pop-args-text":
		// Types which implement encoding.TextUnmarshaler.
		var l level
		var addr netip.Addr
		var n *big.Int
		d.PopArgs(&l, &addr, &n)
		fmt.Fprintf(&buf, "l=%d addr=%s n=%s\n", l, addr, n)
	case "pop-args-nonptr":
		d.PopArgs(i)
	case "pop-args-nil":
		d.PopArgs((*int)(nil))
	case "scan-args-v1":
		// The v1 form of ScanArgs.
		d.ScanArgs(d.T, "x", &i)
	default:
		t.Fatalf("unknown command %s", d.Cmd)
	}
	fmt.Fprintf(&buf, "remaining: %s\n", d.FullCmd())
	return buf.String()
}

func TestPopArgs(t *testing.T) {
	RunTestFromString(t, `
# Arguments are returned verbatim.
pop-all a b=1 c=(1,2) (d, e)
----
"a"
"b=1"
"c=(1,2)"
"(d, e)"
remaining: pop-all

pop-then-flag x -x=3 y
----
popped "x"
x=3
remaining: pop-then-flag -x=3 y

pop-args 12 foo bar
----
i=12 s="foo"
remaining: pop-args bar

pop-args 12 foo
----
i=12 s="foo"
remaining: pop-args

pop-args-str a=1 b
----
s="a=1"
remaining: pop-args-str b

pop-args-none a b
----
remaining: pop-args-none a b

pop-args-none
----
remaining: pop-args-none

pop-then-pop-args a 7 b
----
popped "a"
i=7
remaining: pop-then-pop-args b

pop-args-then-pop 7 a b
----
i=7
popped "a"
remaining: pop-args-then-pop b

# Integers are parsed in base 10 (a leading 0 doesn't indicate octal), like for
# flags.
pop-args 010 foo
----
i=10 s="foo"
remaining: pop-args

pop-args -012 000008
----
i=-12 s="000008"
remaining: pop-args

# A string takes the entire argument.
pop-args 1 (a b) c
----
i=1 s="(a b)"
remaining: pop-args c

pop-args-types 08 255 1.5e3 true -7
----
i=8 u=255 f=1500 b=true n=-7
remaining: pop-args-types

pop-args-text high ::1 12345678901234567890 foo
----
l=2 addr=::1 n=12345678901234567890
remaining: pop-args-text foo

# ScanArgs doesn't remove the arguments.
scan-args 12 foo
----
i=12 s="foo"
remaining: scan-args 12 foo

scan-args-none
----
remaining: scan-args-none

# A time.Duration is parsed with time.ParseDuration.
pop-args-dur 1m30s foo bar
----
dur=1m30s s="foo"
remaining: pop-args-dur bar

# A named type defined from time.Duration is parsed as an integer.
pop-args-named-dur 5
----
dur=5
remaining: pop-args-named-dur
`, func(t *T, d *TestData) string {
		return popHandler(t, d)
	})
}

// TestPopArgsErrors verifies the errors reported by PopArg, PopArgs and
// ScanArgs.
func TestPopArgsErrors(t *testing.T) {
	testCases := []struct {
		directive string
		expected  string
	}{
		{"pop-twice", "<string>:1: PopArg: no arguments left (directive: pop-twice)"},
		// The error shows the original directive line.
		{"pop-twice a", "<string>:1: PopArg: no arguments left (directive: pop-twice a)"},
		{"pop-args 12", `<string>:1: PopArgs: not enough arguments (directive: pop-args 12)`},
		{"pop-args", `<string>:1: PopArgs: not enough arguments (directive: pop-args)`},
		{
			"pop-args foo bar",
			`<string>:1: PopArgs: argument "foo": parse "foo" as int: strconv.ParseInt: parsing "foo": invalid syntax ` +
				`(directive: pop-args foo bar)`,
		},
		{
			// Unlike fmt.Sscan, an argument is not split into multiple values.
			"pop-args 12abc foo",
			`<string>:1: PopArgs: argument "12abc": parse "12abc" as int: strconv.ParseInt: parsing "12abc": invalid syntax ` +
				`(directive: pop-args 12abc foo)`,
		},
		{
			// Integers are parsed in base 10 only.
			"pop-args 0x10 foo",
			`<string>:1: PopArgs: argument "0x10": parse "0x10" as int: strconv.ParseInt: parsing "0x10": invalid syntax ` +
				`(directive: pop-args 0x10 foo)`,
		},
		{
			"pop-args-types 1 256 1 true 1",
			`<string>:1: PopArgs: argument "256": parse "256" as uint8: strconv.ParseUint: parsing "256": value out of range ` +
				`(directive: pop-args-types 1 256 1 true 1)`,
		},
		{
			"pop-args-text medium ::1 1",
			`<string>:1: PopArgs: argument "medium": parse "medium" as datadriven.level: unknown level ` +
				`(directive: pop-args-text medium ::1 1)`,
		},
		{
			"pop-args-dur 5 x",
			`<string>:1: PopArgs: argument "5": time: missing unit in duration "5" (directive: pop-args-dur 5 x)`,
		},
		{
			"pop-args-dur 1hour x",
			`<string>:1: PopArgs: argument "1hour": time: unknown unit "hour" in duration "1hour" (directive: pop-args-dur 1hour x)`,
		},
		{"pop-args-nonptr 5", `<string>:1: PopArgs: value 0 must be a non-nil pointer, got int`},
		{"pop-args-nil 5", `<string>:1: PopArgs: value 0 must be a non-nil pointer, got *int`},
		{
			"scan-args 12 foo bar baz",
			`<string>:1: ScanArgs: unexpected arguments: bar baz (directive: scan-args 12 foo bar baz)`,
		},
		{"scan-args 12", `<string>:1: ScanArgs: not enough arguments (directive: scan-args 12)`},
		{
			"scan-args-none a b",
			`<string>:1: ScanArgs: unexpected arguments: a b (directive: scan-args-none a b)`,
		},
		{
			"scan-args-v1 5",
			`<string>:1: ScanArgs: value 0 is a *datadriven.TBWrapper[*github.com/cockroachdb/datadriven/v2.recorderTB]; ` +
				`ScanArgs scans the positional arguments (use FlagValue to retrieve the value of a flag)`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.directive, func(t *testing.T) {
			rec := &recorderTB{TB: t}
			ran := false
			runAndWait(func() {
				RunTestFromString(rec, tc.directive+"\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
					popHandler(t, d)
					ran = true
					return ""
				})
			})
			if ran {
				t.Fatalf("directive did not fail")
			}
			if expected := []string{tc.expected}; !reflect.DeepEqual(rec.msgs, expected) {
				t.Fatalf("expected:\n  %s\ngot:\n  %s", tc.expected, strings.Join(rec.msgs, "\n  "))
			}
		})
	}
}
