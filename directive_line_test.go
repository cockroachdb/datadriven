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
	"reflect"
	"strings"
	"testing"
)

// TestDirectiveLineInErrors verifies that framework messages show the original
// directive line, even if the test modified Flags or Args.
func TestDirectiveLineInErrors(t *testing.T) {
	const input = `
cmd -a=(1,2)  b \
  -c=3
input
----
wrong
`
	rec := &recorderTB{TB: t, keepFramework: true}
	runAndWait(func() {
		RunTestFromString(rec, input, func(t *TBWrapper[*recorderTB], d *TestData) string {
			if d.FullCmd() != "cmd -a=(1, 2) -c=3 b" {
				t.Fatalf("unexpected FullCmd: %s", d.FullCmd())
			}
			d.MarkAllFlagsProcessed()
			d.Flags = d.Flags[1:]
			d.Args = nil
			if d.FullCmd() != "cmd -c=3" {
				t.Fatalf("unexpected FullCmd: %s", d.FullCmd())
			}
			return "right"
		})
	})
	expected := []string{
		"\n<string>:2:\n  cmd -a=(1,2)  b  -c=3\n  input\n\nexpected:\n  wrong\n\nfound:\n  right",
	}
	// Ignore the "fatal during" log.
	var msgs []string
	for _, m := range rec.msgs {
		if !strings.Contains(m, "fatal during") {
			msgs = append(msgs, m)
		}
	}
	if !reflect.DeepEqual(msgs, expected) {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected, msgs)
	}

	// A manually constructed TestData falls back to FullCmd.
	td := TestData{Cmd: "cmd", Args: []string{"b"}, Input: "x"}
	if s := td.origString(); s != "cmd b\nx\n" {
		t.Fatalf("unexpected origString: %q", s)
	}
	if s := td.DirectiveLine(); s != "cmd b" {
		t.Fatalf("unexpected DirectiveLine: %q", s)
	}
}

// TestDirectiveLine verifies that DirectiveLine returns the line as it is
// written, and that it disables the check for unprocessed flags.
func TestDirectiveLine(t *testing.T) {
	const input = `
  cmd  --dir=foo -a=(1,2)   'x y' \
  --yes -- -z
a -x
b --y='q'
----
`
	const directive = "cmd  --dir=foo -a=(1,2)   'x y'  --yes -- -z"
	for _, tc := range []struct {
		// forDirective and forLines indicate whether DirectiveLine is used for
		// the directive and for the lines of its input (which are checked
		// separately).
		forDirective, forLines bool
		lines                  []string
		expected               []string
	}{
		{forDirective: true, forLines: true, lines: []string{directive, "a -x", "b --y='q'"}},
		{
			forDirective: false, forLines: true,
			lines:    []string{"a -x", "b --y='q'"},
			expected: []string{"<string>:2: unprocessed flags: -dir=foo -a=(1, 2) -yes"},
		},
		{
			forDirective: true, forLines: false,
			lines:    []string{directive},
			expected: []string{"<string>:4: unprocessed flags: -x"},
		},
	} {
		var lines []string
		rec := &recorderTB{TB: t}
		runAndWait(func() {
			RunTestFromString(rec, input, func(t *TBWrapper[*recorderTB], d *TestData) string {
				if tc.forDirective {
					lines = append(lines, d.DirectiveLine())
				}
				for sub := range d.InputDirectives() {
					if tc.forLines {
						lines = append(lines, sub.DirectiveLine())
					}
				}
				return ""
			})
		})
		if !reflect.DeepEqual(lines, tc.lines) {
			t.Fatalf("expected lines %q, got %q", tc.lines, lines)
		}
		if !reflect.DeepEqual(rec.msgs, tc.expected) {
			t.Fatalf("expected messages %q, got %q", tc.expected, rec.msgs)
		}
	}
}
