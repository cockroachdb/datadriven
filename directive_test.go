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
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type namedInt int

func TestFlagValue(t *testing.T) {
	RunTestFromString(t, `
values -n=5 -names=(a, b) -name=c -d=1m -nm=7 -b=false -empty=() -s= -one=(x)
----
n=5 names=[]string{"a", "b"} name=[]string{"c"} d=1m0s nm=7 b=false empty=[]string{} s="" one=[]string{"x"}

or -n=3
----
n=3 m=10

bool -a -c=1
----
a=true b=false has-c=true
`, func(t *T, d *TestData) string {
		switch d.Cmd {
		case "values":
			return fmt.Sprintf("n=%d names=%#v name=%#v d=%s nm=%d b=%t empty=%#v s=%q one=%#v",
				d.FlagValue[int]("n"), d.FlagValue[[]string]("names"), d.FlagValue[[]string]("name"),
				d.FlagValue[time.Duration]("d"), d.FlagValue[namedInt]("nm"), d.FlagValue[bool]("b"),
				d.FlagValue[[]string]("empty"), d.FlagValue[string]("s"), d.FlagValue[[]string]("one"))
		case "or":
			return fmt.Sprintf("n=%d m=%d", d.FlagValueOr("n", 1), d.FlagValueOr("m", 10))
		case "bool":
			return fmt.Sprintf("a=%t b=%t has-c=%t", d.BoolFlag("a"), d.BoolFlag("b"), d.HasFlag("c"))
		}
		t.Fatalf("unknown command %s", d.Cmd)
		return ""
	})
}

func TestFlagValueErrors(t *testing.T) {
	testCases := []struct {
		directive string
		f         func(d *TestData)
		expected  string
	}{
		{"cmd", func(d *TestData) { d.FlagValue[int]("n") }, "<string>:1: missing flag: -n"},
		{
			"cmd -n=x", func(d *TestData) { d.FlagValue[int]("n") },
			`<string>:1: flag -n: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{"cmd -n", func(d *TestData) { d.FlagValue[int]("n") }, `<string>:1: flag -n: no value (see BoolFlag)`},
		{"cmd -n", func(d *TestData) { d.FlagValue[[]int]("n") }, `<string>:1: flag -n: no value (see BoolFlag)`},
		{
			"cmd -n=(1,2)", func(d *TestData) { d.FlagValue[int]("n") },
			`<string>:1: flag -n: the value is a list; use a slice type`,
		},
		{
			// A list with one element is still a list.
			"cmd -n=(5)", func(d *TestData) { d.FlagValue[int]("n") },
			`<string>:1: flag -n: the value is a list; use a slice type`,
		},
		{
			"cmd -n=(1,x)", func(d *TestData) { d.FlagValue[[]int]("n") },
			`<string>:1: flag -n: value 1: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{
			"cmd -n=x", func(d *TestData) { d.FlagValue[[]int]("n") },
			`<string>:1: flag -n: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{
			"cmd -n=1", func(d *TestData) { d.FlagValue[struct{}]("n") },
			`<string>:1: flag -n: unsupported destination type struct {}`,
		},
		{
			"cmd -b=yes", func(d *TestData) { d.FlagValue[bool]("b") },
			`<string>:1: flag -b: strconv.ParseBool: parsing "yes": invalid syntax`,
		},
		{
			"cmd -d=5", func(d *TestData) { d.FlagValue[time.Duration]("d") },
			`<string>:1: flag -d: time: missing unit in duration "5"`,
		},
		{
			"cmd -n=x", func(d *TestData) { d.FlagValueOr("n", 1) },
			`<string>:1: flag -n: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{"cmd -n", func(d *TestData) { d.FlagValueOr("n", 1) }, `<string>:1: flag -n: no value (see BoolFlag)`},
		{"cmd -a=1", func(d *TestData) { d.BoolFlag("a") }, `<string>:1: flag -a must not have a value: -a=1`},
		{"cmd -a=", func(d *TestData) { d.BoolFlag("a") }, `<string>:1: flag -a must not have a value: -a=`},
		{"cmd -a=()", func(d *TestData) { d.BoolFlag("a") }, `<string>:1: flag -a must not have a value: -a=()`},
	}
	for _, tc := range testCases {
		t.Run(tc.directive, func(t *testing.T) {
			rec := &recorderTB{TB: t}
			runAndWait(func() {
				runFromString(rec, tc.directive+"\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
					tc.f(d)
					return ""
				})
			})
			if expected := []string{tc.expected}; !reflect.DeepEqual(rec.msgs, expected) {
				t.Fatalf("expected:\n  %s\ngot:\n  %s", tc.expected, strings.Join(rec.msgs, "\n  "))
			}
		})
	}
}

// TestFlagProcessing verifies that all the flags must be processed, and the
// ways in which they can be processed.
func TestFlagProcessing(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		f     func(t *TBWrapper[*recorderTB], d *TestData) string
		// rewrite runs the test in rewrite mode.
		rewrite  bool
		expected []string
	}{
		{
			name:     "none-processed",
			input:    "cmd -a -b=1 x\n----\n",
			f:        func(t *TBWrapper[*recorderTB], d *TestData) string { return "" },
			expected: []string{"<string>:1: unprocessed flags: -a -b=1"},
		},
		{
			name:    "none-processed-rewrite",
			input:   "cmd -a -b=1 x\n----\n",
			f:       func(t *TBWrapper[*recorderTB], d *TestData) string { return "" },
			rewrite: true,
			// The check also applies in rewrite mode.
			expected: []string{"<string>:1: unprocessed flags: -a -b=1"},
		},
		{
			name:  "some-processed",
			input: "cmd -a -b=1\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				d.HasFlag("a")
				return ""
			},
			expected: []string{"<string>:1: unprocessed flags: -b=1"},
		},
		{
			name:  "by-name",
			input: "cmd -a -b=1 -c=2 -d=(3, 4) -e\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				d.HasFlag("a")
				d.FlagValue[int]("b")
				d.FlagValueOr("c", 0)
				d.FlagValue[[]int]("d")
				d.BoolFlag("e")
				// Looking up a flag that doesn't exist is fine.
				d.FlagValueOr("f", 0)
				d.BoolFlag("g")
				d.HasFlag("h")
				return ""
			},
		},
		{
			name:  "iterate",
			input: "cmd -a -b=1\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				for _, f := range d.Flags {
					d.MarkFlagProcessed(f.Name)
				}
				return ""
			},
		},
		{
			name:  "mark-all",
			input: "cmd -a -b=1\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				d.MarkAllFlagsProcessed()
				return ""
			},
		},
		{
			// A copy of the TestData shares the processed flags.
			name:  "copy",
			input: "cmd -a -b=1\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				cp := *d
				cp.HasFlag("a")
				cp.FlagValue[int]("b")
				return ""
			},
		},
		{
			// Flags can be looked up concurrently.
			name:  "concurrent",
			input: "cmd -a -b=1\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				var wg sync.WaitGroup
				for _, name := range []string{"a", "b", "c", "d"} {
					wg.Add(1)
					go func() {
						defer wg.Done()
						for range 100 {
							d.HasFlag(name)
						}
					}()
				}
				wg.Wait()
				return ""
			},
		},
		{
			name:  "has-flag",
			input: "cmd -a\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				if !d.HasFlag("a") || d.HasFlag("b") {
					t.Fatalf("incorrect HasFlag")
				}
				return ""
			},
		},
		{
			// There is no check if the test failed (without t.Fatal).
			name:  "error",
			input: "cmd -a\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				t.Errorf("error")
				return ""
			},
			expected: []string{"<string>:1: error"},
		},
		{
			name:     "subtest-end-with-flags",
			input:    "subtest foo\n\nsubtest end -x\n",
			f:        func(t *TBWrapper[*recorderTB], d *TestData) string { return "" },
			expected: []string{"<string>:3: invalid syntax for subtest end"},
		},
		{
			// The check happens before the output is compared.
			name:     "before-comparison",
			input:    "cmd -a\n----\nwrong\n",
			f:        func(t *TBWrapper[*recorderTB], d *TestData) string { return "right" },
			expected: []string{"<string>:1: unprocessed flags: -a"},
		},
		{
			// There is no check if the test fails.
			name:  "fatal",
			input: "cmd -a\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				t.Fatalf("fatal")
				return ""
			},
			expected: []string{"<string>:1: fatal"},
		},
		{
			name:     "repeated",
			input:    "cmd -x=1 -x=2\n----\n",
			f:        func(t *TBWrapper[*recorderTB], d *TestData) string { return "" },
			expected: []string{"<string>:1: cannot parse directive at column 10 (flag -x is repeated): cmd -x=1 -x=2"},
		},
		{
			// The flags on the directive line must be processed, even if the test
			// changes Flags (e.g. parses the input lines into it).
			name:  "changed",
			input: "cmd -a -b=1\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				d.HasFlag("b")
				var err error
				if d.Cmd, d.Flags, d.Args, err = ParseLine("x -c"); err != nil {
					t.Fatal(err)
				}
				return ""
			},
			expected: []string{"<string>:1: unprocessed flags: -a"},
		},
		{
			name:     "subtest-with-flags",
			input:    "subtest foo -x\n\nsubtest end\n",
			f:        func(t *TBWrapper[*recorderTB], d *TestData) string { return "" },
			expected: []string{"<string>:1: invalid syntax for subtest"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorderTB{TB: t}
			runAndWait(func() {
				runTestInternal(rec, "<string>", strings.NewReader(tc.input), tc.f, tc.rewrite)
			})
			if !reflect.DeepEqual(rec.msgs, tc.expected) {
				t.Fatalf("expected:\n  %s\ngot:\n  %s", strings.Join(tc.expected, "\n  "), strings.Join(rec.msgs, "\n  "))
			}
		})
	}
}

// TestManualFlag verifies that a Flag constructed manually with an empty
// (non-nil) Vals behaves like a flag without a value.
func TestManualFlag(t *testing.T) {
	f := Flag{Name: "x", Vals: []string{}}
	if s := f.String(); s != "-x" {
		t.Fatalf("unexpected String: %q", s)
	}
	td := &TestData{Cmd: "cmd", Flags: []Flag{f}, T: t}
	if !td.BoolFlag("x") {
		t.Fatalf("BoolFlag returned false")
	}
	if s := td.FullCmd(); s != "cmd -x" {
		t.Fatalf("unexpected FullCmd: %q", s)
	}
}
