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

func TestPos(t *testing.T) {
	if s := (Pos{File: "a/b", Line: 3}).String(); s != "a/b:3" {
		t.Fatalf("unexpected: %s", s)
	}
	if s := (Pos{}).String(); s != "" {
		t.Fatalf("unexpected: %q", s)
	}
}

func TestInputLines(t *testing.T) {
	type posLine struct {
		pos  string
		line string
	}
	var got [][]posLine
	RunTestFromString(t, strings.ReplaceAll(`
cmd

   a
b

c
----

ws-first
${WS}
x
----

empty
----
`, "${WS}", "  \t"), func(t *T, d *TestData) string {
		var lines []posLine
		for pos, line := range d.InputLines() {
			lines = append(lines, posLine{pos.String(), line})
		}
		got = append(got, lines)
		return ""
	})
	expected := [][]posLine{
		{{"<string>:4", "a"}, {"<string>:5", "b"}, {"<string>:6", ""}, {"<string>:7", "c"}},
		{{"<string>:12", "x"}},
		nil,
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatalf("expected %v, got %v", expected, got)
	}

	// A TestData that was not read from a file has zero positions.
	td := TestData{Input: "x\ny"}
	var positions []Pos
	for pos := range td.InputLines() {
		positions = append(positions, pos)
	}
	if !reflect.DeepEqual(positions, []Pos{{}, {}}) {
		t.Fatalf("unexpected positions: %v", positions)
	}
}

// TestInputLinesAnnotations verifies that messages reported while iterating
// over InputLines are annotated with the position of the line.
func TestInputLinesAnnotations(t *testing.T) {
	testCases := []struct {
		name     string
		f        func(t *TBWrapper[*recorderTB], d *TestData)
		expected []string
	}{
		{
			name: "loop",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for _, line := range d.InputLines() {
					t.Logf("line %s", line)
				}
				t.Logf("after")
			},
			expected: []string{"<string>:3: line a", "<string>:4: line b -x=1", "<string>:2: after"},
		},
		{
			name: "break",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for range d.InputLines() {
					break
				}
				t.Logf("after")
			},
			expected: []string{"<string>:2: after"},
		},
		{
			// The position is restored if the loop body panics.
			name: "panic",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				func() {
					defer func() { _ = recover() }()
					for range d.InputLines() {
						panic("boom")
					}
				}()
				t.Logf("after")
			},
			expected: []string{"<string>:2: after"},
		},
		{
			// Lines without a position (here, because the callback replaced the
			// input) don't affect the annotations.
			name: "no-position",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				d.inputPos = Pos{}
				for range d.InputLines() {
					t.Logf("in loop")
					break
				}
			},
			expected: []string{"<string>:2: in loop"},
		},
		{
			// TestData methods (through td.T) report the position of the line.
			name: "fatal",
			f: func(t *TBWrapper[*recorderTB], d *TestData) {
				for _, line := range d.InputLines() {
					var err error
					if d.Cmd, d.Flags, d.Args, err = ParseLine(line); err != nil {
						d.Fatalf("%v", err)
					}
					var x int
					d.ScanFlagInto("x", &x)
				}
			},
			expected: []string{"<string>:3: missing flag: -x"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorderTB{TB: t}
			runAndWait(func() {
				runFromString(rec, "\ncmd\na\nb -x=1\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
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

// TestFrameworkMessagesUseOriginal verifies that changes to the TestData by the
// callback don't affect the framework's messages (or the comparison).
func TestFrameworkMessagesUseOriginal(t *testing.T) {
	rec := &recorderTB{TB: t, keepFramework: true}
	runAndWait(func() {
		runFromString(rec, "\ncmd\ninput\n----\nwrong\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
			d.Pos = Pos{File: "other", Line: 100}
			d.Input = "other input"
			d.Expected = "right\n"
			return "right"
		})
	})
	expected := []string{
		"\n<string>:2:\n  cmd\n  input\n\nexpected:\n  wrong\n\nfound:\n  right",
	}
	if !reflect.DeepEqual(rec.msgs, expected) {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected, rec.msgs)
	}

	rec = &recorderTB{TB: t, keepFramework: true}
	runAndWait(func() {
		runFromString(rec, "\ncmd\ninput\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
			d.Pos = Pos{File: "other", Line: 100}
			d.Input = "other input"
			t.Fatal("fatal")
			return ""
		})
	})
	expected = []string{"<string>:2: fatal", "\nfatal during <string>:2:\ninput"}
	if !reflect.DeepEqual(rec.msgs, expected) {
		t.Fatalf("expected:\n%q\ngot:\n%q", expected, rec.msgs)
	}
}

// TestInputLinesAsync verifies that a loop over InputLines which ends after the
// next directive started doesn't change the position of the next directive.
func TestInputLinesAsync(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	rec := &recorderTB{TB: t}
	runAndWait(func() {
		runFromString(rec, "async\nline\n----\n\nnext\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
			switch d.Cmd {
			case "async":
				go func() {
					defer close(done)
					for range d.InputLines() {
						close(started)
						<-release
					}
				}()
				<-started
			case "next":
				close(release)
				<-done
				t.Logf("in next")
			}
			return ""
		})
	})
	if expected := []string{"<string>:5: in next"}; !reflect.DeepEqual(rec.msgs, expected) {
		t.Fatalf("expected %q, got %q", expected, rec.msgs)
	}
}
