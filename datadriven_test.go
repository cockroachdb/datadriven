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
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
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
`, func(t *testing.T, d *TestData) string {
		if d.Input != "sentence" {
			return "unknown command"
		}
		return ""
	})
}

func TestParseLine(t *testing.T) {
	RunTestFromString(t, `
parse
xx +++
----
here: cannot parse directive at column 4: xx +++

parse
xx a=b a=c
----
"xx" [a=b a=c]

parse
xx a=b b=c c=(1,2,3)
----
"xx" [a=b b=c c=(1, 2, 3)]
`, func(t *testing.T, d *TestData) string {
		cmd, args, err := ParseLine(d.Input)
		if err != nil {
			return errors.Wrap(err, "here").Error()
		}
		return fmt.Sprintf("%q %+v", cmd, args)
	})
}

func TestSkip(t *testing.T) {
	RunTestFromString(t, `
skip
----

# This error should never happen.
error
----
`, func(t *testing.T, d *TestData) string {
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

func TestArgFormat(t *testing.T) {
	RunTestFromString(t, `
# NB: we allow duplicate args.
# ScanArgs simply picks the first occurrence.
make argTuple=(1, 🍌) argInt=12 argString=greedily,impatient moreIgnore= a,b,c
sentence
----
Did the following: make sentence
1 hungry monkey eats a 🍌
while 12 other monkeys watch greedily,impatient
true I'd say
`, func(t *testing.T, d *TestData) string {
		var one int
		var twelve int
		var banana string
		var greedily string
		d.ScanArgs(t, "argTuple", &one, &banana)
		d.ScanArgs(t, "argInt", &twelve)
		d.ScanArgs(t, "argString", &greedily)
		abc := d.HasArg("a,b,c")
		return fmt.Sprintf(`Did the following: %s %s
%d hungry monkey eats a %s
while %d other monkeys watch %s
%v I'd say`,
			d.Cmd, d.Input, one, banana, twelve, greedily, abc,
		)
	})
}

func TestSubTest(t *testing.T) {
	RunTest(t, "testdata/subtest", func(t *testing.T, d *TestData) string {
		switch d.Cmd {
		case "hello":
			return d.CmdArgs[0].Key + " was said"
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
	RunTest(t, "testdata/multiline", func(t *testing.T, d *TestData) string {
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

func TestRewrite(t *testing.T) {
	const testDir = "testdata/rewrite"
	files, err := ioutil.ReadDir(testDir)
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
		t.Run(test, func(t *testing.T) {
			path := filepath.Join(testDir, fmt.Sprintf("%s-before", test))
			file, err := os.OpenFile(path, os.O_RDONLY, 0644 /* irrelevant */)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = file.Close() }()

			// Implement a few simple directives.
			handler := func(t *testing.T, d *TestData) string {
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
			if *rewriteTestFiles {
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
				expected, err := ioutil.ReadAll(after)
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
