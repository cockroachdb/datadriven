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
	"testing"
)

// popHandler implements directives which exercise PopArg; the
// output includes the remaining arguments.
func popHandler(t testing.TB, d *TestData) string {
	var buf strings.Builder
	var i int
	switch d.Cmd {
	case "pop-all":
		for len(d.CmdArgs) > 0 {
			fmt.Fprintf(&buf, "%q\n", d.PopArg())
		}
	case "pop-twice":
		d.PopArg()
		d.PopArg()
	case "pop-then-key":
		// The positional argument has the same name as the key.
		fmt.Fprintf(&buf, "popped %q\n", d.PopArg())
		d.ScanArgs("x", &i)
		fmt.Fprintf(&buf, "x=%d\n", i)
	default:
		t.Fatalf("unknown command %s", d.Cmd)
	}
	fmt.Fprintf(&buf, "remaining: %s\n", d.FullCmd())
	return buf.String()
}

func TestPopArgs(t *testing.T) {
	RunTestFromString(t, `
pop-all a b=1 c=(1,2) d=
----
"a"
"b=1"
"c=(1, 2)"
"d="
remaining: pop-all

pop-then-key x x=3 y
----
popped "x"
x=3
remaining: pop-then-key x=3 y
`, func(t *T, d *TestData) string {
		return popHandler(t, d)
	})
}

// TestPopArgsErrors verifies the errors reported by PopArg.
func TestPopArgsErrors(t *testing.T) {
	testCases := []struct {
		directive string
		expected  string
	}{
		{"pop-twice", "<string>:1: PopArg: no arguments left (directive: pop-twice)"},
		// The error shows the original directive line.
		{"pop-twice a", "<string>:1: PopArg: no arguments left (directive: pop-twice a)"},
	}
	for _, tc := range testCases {
		t.Run(tc.directive, func(t *testing.T) {
			rec := &recorderTB{TB: t}
			ran := false
			runAndWait(func() {
				runFromString(rec, tc.directive+"\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
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
