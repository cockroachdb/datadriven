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

func TestScanFlag(t *testing.T) {
	RunTestFromString(t, `
scan -n=5 -names=(a, b) -verbose -d=1m -nm=7 -b=false
----
n=5 names=[]string{"a", "b"} verbose=true d=1m0s nm=7 b=false

scan-or -n=3
----
n=3 m=10 verbose=false

maybe -n=3
----
n=3 ok=true; m=0 ok=false
`, func(t *T, d *TestData) string {
		switch d.Cmd {
		case "scan":
			return fmt.Sprintf("n=%d names=%#v verbose=%t d=%s nm=%d b=%t",
				ScanFlag[int](d, "n"), ScanFlag[[]string](d, "names"), ScanFlag[bool](d, "verbose"),
				ScanFlag[time.Duration](d, "d"), ScanFlag[namedInt](d, "nm"), ScanFlag[bool](d, "b"))
		case "scan-or":
			return fmt.Sprintf("n=%d m=%d verbose=%t",
				ScanFlagOr(d, "n", 1), ScanFlagOr(d, "m", 10), ScanFlagOr(d, "verbose", false))
		case "maybe":
			n, nOK := MaybeScanFlag[int](d, "n")
			m, mOK := MaybeScanFlag[int](d, "m")
			return fmt.Sprintf("n=%d ok=%t; m=%d ok=%t", n, nOK, m, mOK)
		}
		t.Fatalf("unknown command %s", d.Cmd)
		return ""
	})
}

func TestScanErrors(t *testing.T) {
	testCases := []struct {
		directive string
		f         func(d *TestData)
		expected  string
	}{
		{"cmd", func(d *TestData) { ScanFlag[int](d, "n") }, "<string>:1: missing flag: -n"},
		{
			"cmd -n=x", func(d *TestData) { ScanFlag[int](d, "n") },
			`<string>:1: flag -n: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{"cmd -n", func(d *TestData) { ScanFlag[int](d, "n") }, `<string>:1: flag -n: no value`},
		{
			"cmd -n=(1,2)", func(d *TestData) { ScanFlag[int](d, "n") },
			`<string>:1: flag -n: 2 values; scan into a slice, or pass 2 destinations`,
		},
		{
			"cmd -n=(1,x)", func(d *TestData) { ScanFlag[[]int](d, "n") },
			`<string>:1: flag -n: value 1: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{
			"cmd -n=(1,x)", func(d *TestData) { var a, b int; d.ScanFlagVals("n", &a, &b) },
			`<string>:1: flag -n: value 1: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{
			"cmd -n=(1,2)", func(d *TestData) { var a, b, c int; d.ScanFlagVals("n", &a, &b, &c) },
			`<string>:1: flag -n: 3 destinations, but 2 values`,
		},
		{
			"cmd -n=1", func(d *TestData) { ScanFlag[struct{}](d, "n") },
			`<string>:1: flag -n: unsupported destination type struct {}`,
		},
		{
			// A bool with a value must be a valid bool.
			"cmd -b=yes", func(d *TestData) { ScanFlag[bool](d, "b") },
			`<string>:1: flag -b: strconv.ParseBool: parsing "yes": invalid syntax`,
		},
		{
			"cmd -a=(1,2)", func(d *TestData) { var x int; d.Flags[0].Scan(d.T, 2, &x) },
			`<string>:1: flag -a: value 2: index out of range (2 values)`,
		},
		{
			"cmd -a=(1,x)", func(d *TestData) { var x int; d.Flags[0].Scan(d.T, 1, &x) },
			`<string>:1: flag -a: value 1: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{"cmd -a", func(d *TestData) { d.Flags[0].FirstVal(d.T) }, `<string>:1: flag -a requires at least 1 value, has 0`},
		{"cmd -a=(1,2)", func(d *TestData) { d.Flags[0].SingleVal(d.T) }, `<string>:1: flag -a requires 1 values, has 2`},
		{"cmd -a=(1,2,3)", func(d *TestData) { d.Flags[0].TwoVals(d.T) }, `<string>:1: flag -a requires 2 values, has 3`},
		{
			"cmd -n=x", func(d *TestData) { ScanFlagOr(d, "n", 1) },
			`<string>:1: flag -n: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
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

func TestFlagVals(t *testing.T) {
	a := Flag{Name: "a", Vals: []string{"x", "y"}}
	if v := a.FirstVal(t); v != "x" {
		t.Fatalf("FirstVal: %q", v)
	}
	if v1, v2 := a.TwoVals(t); v1 != "x" || v2 != "y" {
		t.Fatalf("TwoVals: %q %q", v1, v2)
	}
	if v := (Flag{Name: "b", Vals: []string{"z"}}).SingleVal(t); v != "z" {
		t.Fatalf("SingleVal: %q", v)
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
			input: "cmd -a -b=1 -c=2 -d=3 -e=(4, 5)\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				var c, e int
				d.HasFlag("a")
				ScanFlag[int](d, "b")
				ScanFlagOr(d, "c", 0)
				d.ScanFlagVals("d", &c)
				d.Flag("e")
				// Looking up a flag that doesn't exist is fine.
				MaybeScanFlag[int](d, "f")
				d.MaybeScanFlagVals("g", &e)
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
				d.HasFlag("c")
				return ""
			},
			expected: []string{"<string>:1: unprocessed flags: -a"},
		},
		{
			// A copy of the TestData shares the processed flags.
			name:  "copy",
			input: "cmd -a -b=1\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				cp := *d
				cp.HasFlag("a")
				ScanFlag[int](&cp, "b")
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
			name:  "repeated",
			input: "cmd -x=1 -x=2\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				ScanFlag[int](d, "x")
				return ""
			},
			expected: []string{"<string>:1: flag -x is repeated (see FlagValues)"},
		},
		{
			name:  "repeated-has-flag",
			input: "cmd -x=1 -x=2\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				if !d.HasFlag("x") {
					t.Fatalf("HasFlag returned false")
				}
				return ""
			},
		},
		{
			name:  "flag-values",
			input: "cmd -x=1 -y -x=(2, 3) -x\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				d.HasFlag("y")
				vals := d.FlagValues("x")
				if expected := [][]string{{"1"}, {"2", "3"}, nil}; !reflect.DeepEqual(vals, expected) {
					t.Fatalf("FlagValues: expected %v, got %v", expected, vals)
				}
				return ""
			},
		},
		{
			name:  "scan-flag-values",
			input: "cmd -x=1 -x=(2, 3)\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				vals := ScanFlagValues[[]int](d, "x")
				if expected := [][]int{{1}, {2, 3}}; !reflect.DeepEqual(vals, expected) {
					t.Fatalf("ScanFlagValues: expected %v, got %v", expected, vals)
				}
				return ""
			},
		},
		{
			name:  "scan-flag-values-error",
			input: "cmd -x=1 -x=y\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				ScanFlagValues[int](d, "x")
				return ""
			},
			expected: []string{`<string>:1: flag -x: parse "y" as int: strconv.ParseInt: parsing "y": invalid syntax`},
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
