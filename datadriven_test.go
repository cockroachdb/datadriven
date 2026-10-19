// Copyright 2019 The Cockroach Authors.
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
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pmezard/go-difflib/difflib"
)

func TestNewLineBetweenDirectives(t *testing.T) {
	RunTestFromString(t, `
# Some testing of sensitivity to newlines
foo
----
unknown command

bar
----
unknown command




bar
----
unknown command
`, func(t *T, d *TestData) string {
		if d.Input != "sentence" {
			return "unknown command"
		}
		return ""
	})
}

func TestParseLine(t *testing.T) {
	RunTestFromString(t, `
parse
xx -a=b -a=c
----
cannot parse directive at column 9 (flag -a is repeated): xx -a=b -a=c

parse
xx -a=b x -b=c -c=(1,2,3) y
----
"xx" [-a=b -b=c -c=(1, 2, 3)] [x y]

parse
xx (
----
cannot parse directive at column 4 (unclosed '('): xx (

parse
xx (a)) b
----
cannot parse directive at column 7 (unmatched ')'): xx (a)) b

parse
-x
----
cannot parse directive at column 1 (expected a command): -x

parse
xx -f(x)=1
----
cannot parse directive at column 4 (invalid flag name "f(x)"): xx -f(x)=1

parse
-- x
----
cannot parse directive at column 1 (expected a command): -- x

# Columns are in characters.
parse
é 🍌 (
----
cannot parse directive at column 5 (unclosed '('): é 🍌 (
`, func(t *T, d *TestData) string {
		cmd, flags, args, err := ParseLine(d.Input)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("%q %v %v", cmd, flags, args)
	})
}

func TestSkip(t *testing.T) {
	RunTestFromString(t, `
skip
----

# This error should never happen.
error
----
`, func(t *T, d *TestData) string {
		switch d.Cmd {
		case "skip":
			// Verify that calling t.Skip() does not fail with an API error on
			// testing.T.
			t.Skip("woo")
		case "error":
			// The skip should mask the error afterwards.
			t.Error("never reached")
		}
		return d.Expected
	})
}

func TestSubTest(t *testing.T) {
	RunTest(t, "testdata/subtest", func(t *T, d *TestData) string {
		switch d.Cmd {
		case "hello":
			return d.Args[0] + " was said"
		case "skip":
			// Verify that calling t.Skip() does not fail with an API error on
			// testing.T.
			t.Skip("woo")
		case "error":
			// The skip should mask the error afterwards.
			t.Error("never reached")
		default:
			t.Fatalf("unknown directive: %s", d.Cmd)
		}
		return d.Expected
	})
}

func TestMultiLineTest(t *testing.T) {
	RunTest(t, "testdata/multiline", func(t *T, d *TestData) string {
		switch d.Cmd {
		case "small":
			return `just
two lines of output`
		case "large":
			return `more
than
five
lines
of
output`
		default:
			t.Fatalf("unknown directive: %s", d.Cmd)
		}
		return d.Expected
	})
}

func TestRetry(t *testing.T) {
	var v atomic.Uint32
	RunTest(t, "testdata/retry", func(t *T, d *TestData) string {
		switch d.Cmd {
		case "inc":
			n := FlagValueOr(d, "n", 1)
			for i := 0; i < n; i++ {
				go func() {
					time.Sleep(time.Duration(rand.Intn(10)) * time.Microsecond)
					v.Add(1)
				}()
			}
			return ""

		case "read":
			return d.Retry(func() string {
				return fmt.Sprint(v.Load())
			})

		default:
			t.Fatalf("unknown directive: %s", d.Cmd)
		}
		return d.Expected
	})
}

func TestDirective(t *testing.T) {
	RunTest(t, "testdata/directive", func(t *T, d *TestData) string {
		var buf bytes.Buffer
		fmt.Fprintf(&buf, "cmd: %s\n", d.Cmd)
		for _, f := range d.Flags {
			switch {
			case f.List:
				fmt.Fprintf(&buf, "flag: name=%q list=%#v\n", f.Name, f.Vals)
			case f.Vals != nil:
				fmt.Fprintf(&buf, "flag: name=%q value=%q\n", f.Name, f.Vals[0])
			default:
				fmt.Fprintf(&buf, "flag: name=%q\n", f.Name)
			}
		}
		d.MarkAllFlagsProcessed()
		for _, a := range d.Args {
			fmt.Fprintf(&buf, "arg: %q\n", a)
		}
		// FullCmd renders the directive such that it is parsed the same way.
		full := d.FullCmd()
		fmt.Fprintf(&buf, "full: %s\n", full)
		cmd, flags, args, err := ParseLine(full)
		if err != nil {
			t.Fatal(err)
		}
		if cmd != d.Cmd || !reflect.DeepEqual(flags, d.Flags) || !reflect.DeepEqual(args, d.Args) {
			t.Fatalf("FullCmd() parsed as %q %v %v", cmd, flags, args)
		}
		return buf.String()
	})
}

func TestWalk(t *testing.T) {
	Walk(t, "testdata/walk", func(t *testing.T, path string) {
		RunTest(t, path, func(t *T, d *TestData) string {
			return fmt.Sprintf("test name: %s\n", t.Name())
		})
	})
}

func TestRewrite(t *testing.T) {
	const testDir = "testdata/rewrite"
	files, err := os.ReadDir(testDir)
	if err != nil {
		t.Fatal(err)
	}
	var tests []string
	for _, file := range files {
		if name := file.Name(); strings.HasSuffix(name, "-before") {
			tests = append(tests, strings.TrimSuffix(name, "-before"))
		} else if !strings.HasSuffix(name, "-after") {
			t.Fatalf("all files in %s must end in either -before or -after: %s", testDir, name)
		}
	}
	sort.Strings(tests)

	for _, test := range tests {
		subTest(t, test, func(t *testing.T) {
			path := filepath.Join(testDir, fmt.Sprintf("%s-before", test))
			file, err := os.OpenFile(path, os.O_RDONLY, 0644 /* irrelevant */)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()

			// Implement a few simple directives.
			handler := func(t *T, d *TestData) string {
				switch d.Cmd {
				case "noop":
					return d.Input

				case "duplicate":
					return fmt.Sprintf("%s\n%s", d.Input, d.Input)

				case "duplicate-with-blank":
					return fmt.Sprintf("%s\n\n%s", d.Input, d.Input)

				case "no-output":
					return ""

				default:
					t.Fatalf("unknown directive %s", d.Cmd)
					return ""
				}
			}

			rewriteData := runTestInternal(t, path, file, handler, true /* rewrite */)

			afterPath := filepath.Join(testDir, fmt.Sprintf("%s-after", test))
			if rewriteFlag() {
				// We are rewriting the rewrite tests. Dump the output into -after files
				out, err := os.Create(afterPath)
				defer func() { _ = out.Close() }()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := out.Write(rewriteData); err != nil {
					t.Fatal(err)
				}
			} else {
				after, err := os.Open(afterPath)
				defer func() { _ = after.Close() }()
				if err != nil {
					t.Fatal(err)
				}
				expected, err := io.ReadAll(after)
				if err != nil {
					t.Fatal(err)
				}

				if string(rewriteData) != string(expected) {
					linesExp := difflib.SplitLines(string(expected))
					linesActual := difflib.SplitLines(string(rewriteData))
					diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
						A: linesExp,
						B: linesActual,
					})
					if err != nil {
						t.Fatal(err)
					}
					t.Errorf("expected didn't match actual:\n%s", diff)
				}
			}
		})
	}
}

func TestFlagValueOrMissing(t *testing.T) {
	RunTestFromString(t, `
cmd
----
"default"
`, func(t *T, d *TestData) string {
		return fmt.Sprintf("%q", FlagValueOr(d, "vals", "default"))
	})
}

// checkFlagValue returns FlagValue[V](d, "vals"), after checking that
// FlagValueOr returns the same.
func checkFlagValue[V any](d *TestData) V {
	v1 := FlagValue[V](d, "vals")
	var def V
	if v2 := FlagValueOr(d, "vals", def); !reflect.DeepEqual(v1, v2) {
		d.T.Fatalf("FlagValue and FlagValueOr returned %#v and %#v", v1, v2)
	}
	return v1
}

func TestFlagValueTypes(t *testing.T) {

	RunTestFromString(t, `
[]string -vals=(foo, bar, bax)
----
[]string{"foo", "bar", "bax"}

[]int -vals=(1, 2, 3, 4)
----
[]int{1, 2, 3, 4}

[]uint64 -vals=(1, 2, 3, 4)
----
[]uint64{0x1, 0x2, 0x3, 0x4}

[]int64 -vals=(10, 000016)
----
[]int64{10, 16}

string -vals=foo
----
"foo"

# A single value can be retrieved as a slice.
[]int -vals=5
----
[]int{5}

# An empty list.
[]string -vals=()
----
[]string{}

bool -vals=true
----
true

float64 -vals=1.1
----
1.1

[]float64 -vals=(1.1, 2.2, 3.3, 4.4)
----
[]float64{1.1, 2.2, 3.3, 4.4}

time.Duration -vals=10.0m
----
10m0s
	`, func(t *T, d *TestData) string {
		switch d.Cmd {
		case "[]string":
			dest1 := checkFlagValue[[]string](d)
			return fmt.Sprintf("%#v", dest1)
		case "[]int":
			dest1 := checkFlagValue[[]int](d)
			return fmt.Sprintf("%#v", dest1)
		case "[]int64":
			dest1 := checkFlagValue[[]int64](d)
			return fmt.Sprintf("%#v", dest1)
		case "[]uint64":
			dest1 := checkFlagValue[[]uint64](d)
			return fmt.Sprintf("%#v", dest1)
		case "[]float64":
			dest1 := checkFlagValue[[]float64](d)
			return fmt.Sprintf("%#v", dest1)
		case "float64":
			dest1 := checkFlagValue[float64](d)
			return fmt.Sprintf("%#v", dest1)
		case "time.Duration":
			dest1 := checkFlagValue[time.Duration](d)
			return fmt.Sprintf("%s", dest1)
		case "string":
			dest1 := checkFlagValue[string](d)
			return fmt.Sprintf("%#v", dest1)
		case "bool":
			dest1 := checkFlagValue[bool](d)
			return fmt.Sprintf("%#v", dest1)
		default:
			return fmt.Sprintf("unrecognized type %s", d.Cmd)
		}
	})
}

func TestString(t *testing.T) {
	RunTest(t, "testdata/string", func(t *T, d *TestData) string {
		d.MarkAllFlagsProcessed()
		return d.String()
	})
}

func BenchmarkInput(b *testing.B) {
	RunTestFromString(b, `
foo
----
unknown command
`, func(b *B, d *TestData) string {
		return "unknown command"
	})
}

// BEGIN: TestData.T required
func TestTestDataWithoutT(t *testing.T) {
	defer func() {
		if r := fmt.Sprint(recover()); !strings.Contains(r, "TestData.T is not set") {
			t.Fatalf("unexpected panic: %s", r)
		}
	}()
	td := TestData{Cmd: "cmd"}
	var x int
	FlagValueOr(&td, "x", x)
}

// END: TestData.T required
