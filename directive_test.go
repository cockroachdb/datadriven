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
	"encoding"
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

type namedInt int

type namedSlice []string

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

slices -ds=(1s, 2m) -d1=3s -ns=(a, b) -ni=(4, 5)
----
ds=[1s 2m0s] d1=[3s] ns=datadriven.namedSlice{"a", "b"} ni=[]datadriven.namedInt{4, 5}

# A flag can be repeated if it is retrieved with FlagValues.
repeated -p=(a, 1) -n=3 -p=(b, 2) -p=c -x --p='d e' -q -q
----
p=[[a 1] [b 2] [c] [d e]] n=[3] d=[] missing-nil=true has-p=true has-q=true x=true full=repeated -p=(a, 1) -n=3 -p=(b, 2) -p=c -x -p='d e' -q -q

# A quoted value is a single value, which is not trimmed if it is a string.
quoted -s='  a b ' -l='(x, y)' -n=' 5 ' -ns='a, b' ' c '
----
s="  a b " l="(x, y)" n=5 ns=datadriven.namedSlice{"a, b"} arg=" c "
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
		case "repeated":
			return fmt.Sprintf("p=%v n=%v d=%v missing-nil=%t has-p=%t has-q=%t x=%t full=%s",
				d.FlagValues[[]string]("p"), d.FlagValues[int]("n"), d.FlagValues[time.Duration]("d"),
				d.FlagValues[string]("missing") == nil, d.HasFlag("p"), d.HasFlag("q"), d.BoolFlag("x"), d.FullCmd())
		case "quoted":
			return fmt.Sprintf("s=%q l=%q n=%d ns=%#v arg=%q",
				d.FlagValue[string]("s"), d.FlagValue[string]("l"), d.FlagValue[int]("n"),
				d.FlagValue[namedSlice]("ns"), d.PopArg())
		case "slices":
			return fmt.Sprintf("ds=%v d1=%v ns=%#v ni=%#v",
				d.FlagValue[[]time.Duration]("ds"), d.FlagValue[[]time.Duration]("d1"),
				d.FlagValue[namedSlice]("ns"), d.FlagValue[[]namedInt]("ni"))
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
			`<string>:1: flag -n: the value is a list; use a slice type (or quote the value)`,
		},
		{
			// A list with one element is still a list.
			"cmd -n=(5)", func(d *TestData) { d.FlagValue[int]("n") },
			`<string>:1: flag -n: the value is a list; use a slice type (or quote the value)`,
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
			`<string>:1: flag -n: unsupported type struct {} (see FlagValue)`,
		},
		// An unsupported type is an error even if no value would be parsed.
		{
			"cmd", func(d *TestData) { d.FlagValueOr("n", struct{}{}) },
			`<string>:1: flag -n: unsupported type struct {} (see FlagValue)`,
		},
		{
			"cmd", func(d *TestData) { d.FlagValue[any]("n") },
			`<string>:1: flag -n: unsupported type interface {} (see FlagValue)`,
		},
		{
			"cmd -n=()", func(d *TestData) { d.FlagValue[[]struct{}]("n") },
			`<string>:1: flag -n: unsupported type []struct {} (see FlagValue)`,
		},
		{
			"cmd -n=()", func(d *TestData) { d.FlagValueOr("n", [][]int(nil)) },
			`<string>:1: flag -n: unsupported type [][]int (see FlagValue)`,
		},
		{
			"cmd", func(d *TestData) { d.FlagValueOr("n", (*int)(nil)) },
			`<string>:1: flag -n: unsupported type *int (see FlagValue)`,
		},
		{
			"cmd", func(d *TestData) { d.FlagValueOr("n", map[string]int(nil)) },
			`<string>:1: flag -n: unsupported type map[string]int (see FlagValue)`,
		},
		{
			"cmd", func(d *TestData) { d.FlagValueOr[encoding.TextUnmarshaler]("n", nil) },
			`<string>:1: flag -n: unsupported type encoding.TextUnmarshaler (see FlagValue)`,
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
		// A repeated flag can only be retrieved with FlagValues.
		{"cmd -n=1 -n=2", func(d *TestData) { d.FlagValue[int]("n") }, `<string>:1: flag -n is repeated (see FlagValues)`},
		{"cmd -n=1 -n=2", func(d *TestData) { d.FlagValue[[]int]("n") }, `<string>:1: flag -n is repeated (see FlagValues)`},
		{"cmd -n=1 --n=1", func(d *TestData) { d.FlagValueOr("n", 0) }, `<string>:1: flag -n is repeated (see FlagValues)`},
		{"cmd -v -x -v", func(d *TestData) { d.BoolFlag("v") }, `<string>:1: flag -v is repeated (see FlagValues)`},
		{"cmd -n=1 -n", func(d *TestData) { d.FlagValues[int]("n") }, `<string>:1: flag -n: no value (see BoolFlag)`},
		{
			"cmd -n=1 -n=x", func(d *TestData) { d.FlagValues[int]("n") },
			`<string>:1: flag -n=x: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{
			"cmd -n=1 -n=(2, 3)", func(d *TestData) { d.FlagValues[int]("n") },
			`<string>:1: flag -n=(2, 3): the value is a list; use a slice type (or quote the value)`,
		},
		{
			"cmd -n=(1, 2) -n=(3, x)", func(d *TestData) { d.FlagValues[[]int]("n") },
			`<string>:1: flag -n=(3, x): value 1: parse "x" as int: strconv.ParseInt: parsing "x": invalid syntax`,
		},
		{
			"cmd", func(d *TestData) { d.FlagValues[struct{}]("n") },
			`<string>:1: flag -n: unsupported type struct {} (see FlagValue)`,
		},
		{"cmd -a=1", func(d *TestData) { d.BoolFlag("a") }, `<string>:1: flag -a must not have a value: -a=1`},
		{"cmd -a=", func(d *TestData) { d.BoolFlag("a") }, `<string>:1: flag -a must not have a value: -a=`},
		{"cmd -a=()", func(d *TestData) { d.BoolFlag("a") }, `<string>:1: flag -a must not have a value: -a=()`},
	}
	for _, tc := range testCases {
		t.Run(tc.directive, func(t *testing.T) {
			rec := &recorderTB{TB: t}
			runAndWait(func() {
				RunTestFromString(rec, tc.directive+"\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
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
			// Modifying Flags in place doesn't affect the check (DeleteFunc moves and
			// zeroes elements of the slice).
			name:  "in-place",
			input: "cmd -a -b=1 -c\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				d.BoolFlag("a")
				d.FlagValue[int]("b")
				d.BoolFlag("c")
				d.Flags = slices.DeleteFunc(d.Flags, func(f Flag) bool { return f.Name != "c" })
				d.Flags[0].Name = "renamed"
				return ""
			},
		},
		{
			name:  "in-place-unprocessed",
			input: "cmd -a -b=1 -c\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				d.BoolFlag("a")
				d.Flags = slices.DeleteFunc(d.Flags, func(f Flag) bool { return f.Name != "a" })
				return ""
			},
			expected: []string{"<string>:1: unprocessed flags: -b=1 -c"},
		},
		{
			// Overwriting the TestData doesn't affect the check.
			name:  "overwrite",
			input: "cmd -a -b=1\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				d.HasFlag("a")
				*d = TestData{Cmd: "x", T: t}
				return ""
			},
			expected: []string{"<string>:1: unprocessed flags: -b=1"},
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
			// All the occurrences of a repeated flag are unprocessed.
			name:     "repeated",
			input:    "cmd -x=1 -y --x=2\n----\n",
			f:        func(t *TBWrapper[*recorderTB], d *TestData) string { return "" },
			expected: []string{"<string>:1: unprocessed flags: -x=1 -y -x=2"},
		},
		{
			name:  "repeated-processed",
			input: "cmd -x=1 -y -x=2 -z -z\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) string {
				d.FlagValues[int]("x")
				d.BoolFlag("y")
				d.HasFlag("z")
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

// TestFullCmdRoundTrip verifies that FullCmd renders arbitrary values and
// arguments such that they are parsed back unchanged.
func TestFullCmdRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []string{"a", "b", " ", "\t", "'", "(", ")", ",", "=", "-", `"`, "é"}
	randString := func() string {
		var b strings.Builder
		for range rng.IntN(6) {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		return b.String()
	}
	// The elements of a list can't be quoted, so they are restricted to those
	// that can be written as they are.
	randElem := func() string {
		for {
			e := randString()
			_, flags, _, err := ParseLine("cmd -x=(" + e + ", z)")
			if e != "" && err == nil && reflect.DeepEqual(flags, []Flag{{Name: "x", Vals: []string{e, "z"}, List: true}}) {
				return e
			}
		}
	}
	quoted := 0
	for range 20000 {
		td := TestData{Cmd: "cmd"}
		for i := range rng.IntN(4) {
			f := Flag{Name: fmt.Sprintf("f%d", i)}
			switch rng.IntN(3) {
			case 1:
				f.Vals = []string{randString()}
			case 2:
				f.List = true
				for range rng.IntN(3) {
					f.Vals = append(f.Vals, randElem())
				}
			}
			td.Flags = append(td.Flags, f)
		}
		for range rng.IntN(4) {
			td.Args = append(td.Args, randString())
		}
		full := td.FullCmd()
		if strings.Contains(full, "'") {
			quoted++
		}
		cmd, flags, args, err := ParseLine(full)
		if err != nil {
			t.Fatalf("%#v rendered as %q: %v", td, full, err)
		}
		if cmd != td.Cmd || !reflect.DeepEqual(flags, td.Flags) || !reflect.DeepEqual(args, td.Args) {
			t.Fatalf("flags %q and args %q rendered as %q, parsed as flags %q and args %q",
				td.Flags, td.Args, full, flags, args)
		}
	}
	if quoted < 1000 {
		t.Fatalf("only %d directives with quotes", quoted)
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

// TestManualTestData verifies that the flag API can be used (including
// concurrently) on a TestData constructed manually.
func TestManualTestData(t *testing.T) {
	cmd, flags, args, err := ParseLine("cmd -a -b=1 x")
	if err != nil {
		t.Fatal(err)
	}
	td := &TestData{Cmd: cmd, Flags: flags, Args: args, T: t}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				if !td.BoolFlag("a") || td.FlagValue[int]("b") != 1 || td.FlagValueOr("c", 2) != 2 {
					t.Error("unexpected value")
				}
			}
		}()
	}
	wg.Wait()
}

type (
	customInt    int
	customUint8  uint8
	customFloat  float32
	customString string
	customBool   bool
	customInts   []int
	customElems  []customInt
	customStrs   []customString
)

// TestFlagValueCustomTypes verifies that FlagValue and FlagValueOr work with
// named types (scalars and slices).
func TestFlagValueCustomTypes(t *testing.T) {
	RunTestFromString(t, `
custom -x=5 -u=7 -f=1.5 -s=foo -b=true -ints=(1, 2) -int=3 -elems=(4, 5) -strs=(a, b)
----
ok
`, func(t *T, d *TestData) string {
		check := func(name string, got, expected any) {
			t.Helper()
			if !reflect.DeepEqual(got, expected) {
				t.Errorf("%s: expected %#v, got %#v", name, expected, got)
			}
		}
		check("x", d.FlagValue[customInt]("x"), customInt(5))
		check("u", d.FlagValue[customUint8]("u"), customUint8(7))
		check("f", d.FlagValue[customFloat]("f"), customFloat(1.5))
		check("s", d.FlagValue[customString]("s"), customString("foo"))
		check("b", d.FlagValue[customBool]("b"), customBool(true))
		check("ints", d.FlagValue[customInts]("ints"), customInts{1, 2})
		check("int", d.FlagValue[customInts]("int"), customInts{3})
		check("elems", d.FlagValue[customElems]("elems"), customElems{4, 5})
		check("[]customInt", d.FlagValue[[]customInt]("elems"), []customInt{4, 5})
		check("strs", d.FlagValue[customStrs]("strs"), customStrs{"a", "b"})
		// FlagValueOr infers the type from the default value.
		check("or-x", d.FlagValueOr("x", customInt(0)), customInt(5))
		check("or-missing", d.FlagValueOr("missing", customInt(9)), customInt(9))
		check("or-ints", d.FlagValueOr("ints", customInts(nil)), customInts{1, 2})
		check("or-missing-ints", d.FlagValueOr("missing-ints", customInts{7}), customInts{7})
		return "ok"
	})
}

func TestFlagValueCustomTypeErrors(t *testing.T) {
	testCases := []struct {
		directive string
		f         func(d *TestData)
		expected  string
	}{
		{
			"cmd -u=300", func(d *TestData) { d.FlagValue[customUint8]("u") },
			`<string>:1: flag -u: parse "300" as datadriven.customUint8: strconv.ParseUint: parsing "300": value out of range`,
		},
		{
			"cmd -x=(1, 2)", func(d *TestData) { d.FlagValue[customInt]("x") },
			`<string>:1: flag -x: the value is a list; use a slice type (or quote the value)`,
		},
		{
			"cmd -e=(1, x)", func(d *TestData) { d.FlagValue[customElems]("e") },
			`<string>:1: flag -e: value 1: parse "x" as datadriven.customInt: strconv.ParseInt: parsing "x": invalid syntax`,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.directive, func(t *testing.T) {
			rec := &recorderTB{TB: t}
			runAndWait(func() {
				RunTestFromString(rec, tc.directive+"\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
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
