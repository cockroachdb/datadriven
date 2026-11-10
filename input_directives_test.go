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
	"slices"
	"strings"
	"testing"
)

func TestInputDirectives(t *testing.T) {
	RunTestFromString(t, `
batch -top
set a -value=1

  # A comment.
del b c -sync \
  -- -d
get (x y) -keys=(k1, k2)
\

last \
----
<string>:3: cmd="set" flags=[-value=1] args=[a] full="set -value=1 a"
<string>:6: cmd="del" flags=[-sync] args=[b c -d] full="del -sync -- b c -d"
<string>:8: cmd="get" flags=[-keys=(k1, k2)] args=[(x y)] full="get -keys=(k1, k2) (x y)"
<string>:11: cmd="last" flags=[] args=[\] full="last \\"

# No input.
batch -top
----
`, func(t *T, d *TestData) string {
		d.BoolFlag("top")
		var buf strings.Builder
		for sub := range d.InputDirectives() {
			if sub.T != d.T || sub.Input != "" || sub.Expected != "" || sub.Rewrite {
				t.Fatalf("unexpected directive: %+v", sub)
			}
			fmt.Fprintf(&buf, "%s: cmd=%q flags=%v args=%v full=%q\n", sub.Pos, sub.Cmd, sub.Flags, sub.Args, sub.FullCmd())
			sub.MarkAllFlagsProcessed()
		}
		return buf.String()
	})

	// The directives have Rewrite set if the test file is being rewritten.
	numDirectives := 0
	runTestInternal(t, "<string>", strings.NewReader("cmd\na\nb\n----\n"), func(t *T, d *TestData) string {
		for sub := range d.InputDirectives() {
			numDirectives++
			if !d.Rewrite || !sub.Rewrite {
				t.Fatalf("Rewrite is not set")
			}
		}
		return ""
	}, true /* rewrite */)
	if numDirectives != 2 {
		t.Fatalf("expected 2 directives, got %d", numDirectives)
	}

	// A TestData that was not read from a file.
	td := TestData{Input: "a -x=1\nb", T: t}
	var cmds []string
	for sub := range td.InputDirectives() {
		if sub.Pos != (Pos{}) {
			t.Fatalf("unexpected position %s", sub.Pos)
		}
		cmds = append(cmds, fmt.Sprintf("%s:%d", sub.Cmd, sub.FlagValueOr("x", 0)))
	}
	if expected := []string{"a:1", "b:0"}; !reflect.DeepEqual(cmds, expected) {
		t.Fatalf("expected %q, got %q", expected, cmds)
	}
}

// TestInputDirectivesMessages verifies the messages reported while iterating
// over InputDirectives: they are annotated with the position of the line, and
// all the flags on a line must be processed.
func TestInputDirectivesMessages(t *testing.T) {
	const file = "\ncmd\na -x=1 foo\n\nb -y -z=2 \\\n  -w\nc\n----\n"
	testCases := []struct {
		name     string
		file     string
		f        func(t *TBWrapper[*recorderTB], d *TestData)
		expected []string
	}{
		{
			name: "loop",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for sub := range d.InputDirectives() {
					t.Logf("cmd %s", sub.Cmd)
					sub.Fatalf("fatal")
				}
			},
			expected: []string{"<string>:3: cmd a", "<string>:3: fatal"},
		},
		{
			// The position of the directive is restored after the loop.
			name: "after",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for sub := range d.InputDirectives() {
					sub.MarkAllFlagsProcessed()
				}
				t.Logf("after")
			},
			expected: []string{"<string>:2: after"},
		},
		{
			name: "missing-flag",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for sub := range d.InputDirectives() {
					sub.FlagValue[int]("x")
				}
			},
			expected: []string{"<string>:5: missing flag: -x"},
		},
		{
			// Errors show the line.
			name: "pop-arg",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for sub := range d.InputDirectives() {
					sub.MarkAllFlagsProcessed()
					sub.PopArg()
				}
			},
			expected: []string{"<string>:5: PopArg: no arguments left (directive: b -y -z=2  -w)"},
		},
		{
			name: "unprocessed",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for sub := range d.InputDirectives() {
					sub.HasFlag("x")
					sub.HasFlag("z")
				}
			},
			expected: []string{"<string>:5: unprocessed flags: -y -w"},
		},
		{
			// The flags are also checked when the loop is exited early.
			name: "unprocessed-break",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for range d.InputDirectives() {
					break
				}
				t.Logf("not reached")
			},
			expected: []string{"<string>:3: unprocessed flags: -x=1"},
		},
		{
			name: "break",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for sub := range d.InputDirectives() {
					sub.MarkAllFlagsProcessed()
					if sub.Cmd == "b" {
						break
					}
				}
				t.Logf("after")
			},
			expected: []string{"<string>:2: after"},
		},
		{
			// The check is not affected by changes to the directive.
			name: "unprocessed-modified",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for sub := range d.InputDirectives() {
					if sub.Cmd == "b" {
						sub.HasFlag("y")
						sub.Flags = slices.DeleteFunc(sub.Flags, func(f Flag) bool { return f.Name != "y" })
						*sub = TestData{}
					} else {
						sub.MarkAllFlagsProcessed()
					}
				}
			},
			expected: []string{"<string>:5: unprocessed flags: -z=2 -w"},
		},
		{
			// The flags are not checked if the test has already failed.
			name: "error",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for range d.InputDirectives() {
					t.Errorf("error")
				}
			},
			expected: []string{"<string>:3: error", "<string>:5: error", "<string>:7: error"},
		},
		{
			name: "parse-error",
			file: "\ncmd\na\nb (\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for sub := range d.InputDirectives() {
					t.Logf("cmd %s", sub.Cmd)
				}
			},
			expected: []string{
				"<string>:3: cmd a",
				"<string>:4: cannot parse directive at column 3 (unclosed '('): b (",
			},
		},
		{
			name: "repeated-flag",
			file: "\ncmd\na -x -x\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for range d.InputDirectives() {
				}
			},
			expected: []string{"<string>:3: cannot parse directive at column 6 (flag -x is repeated): a -x -x"},
		},
		{
			// The flags on the lines don't count as flags of the directive.
			name: "directive-flag",
			file: "\ncmd -x\na -x\n----\n",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for sub := range d.InputDirectives() {
					sub.BoolFlag("x")
				}
			},
			expected: []string{"<string>:2: unprocessed flags: -x"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.file == "" {
				tc.file = file
			}
			rec := &recorderTB{TB: t}
			runAndWait(func() {
				RunTestFromString(rec, tc.file, func(t *TBWrapper[*recorderTB], d *TestData) string {
					tc.f(t, d)
					return ""
				})
			})
			if !reflect.DeepEqual(rec.msgs, tc.expected) {
				t.Fatalf("expected:\n  %s\ngot:\n  %s", strings.Join(tc.expected, "\n  "), strings.Join(rec.msgs, "\n  "))
			}
		})
	}
}
