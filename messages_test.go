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

// recordAll runs the test input over a recorder which keeps the framework
// messages, and returns the recorded messages.
func recordAll(t *testing.T, input string, f func(t *TBWrapper[*recorderTB], d *TestData) string) []string {
	rec := &recorderTB{TB: t, keepFramework: true}
	runAndWait(func() {
		runFromString(rec, input, f)
	})
	return rec.msgs
}

func TestSubtestEndMismatchMessage(t *testing.T) {
	rec := &recorderTB{TB: t}
	runAndWait(func() {
		runFromString(rec, "subtest foo\n\ncmd\n----\n\nsubtest end bar\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
			return ""
		})
	})
	msgs := rec.msgs
	expected := []string{`<string>:6: mismatched subtest end directive: expected "foo", got "bar"`}
	if !reflect.DeepEqual(msgs, expected) {
		t.Fatalf("expected %q, got %q", expected, msgs)
	}
}

func TestWhitespaceNote(t *testing.T) {
	const note = "the outputs differ only in trailing whitespace"
	testCases := []struct {
		name     string
		input    string
		actual   string
		expected string // expected note, or "" if no note
	}{
		{
			name:     "trailing-space",
			input:    "cmd\n----\nx \ny\n",
			actual:   "x\ny",
			expected: note + `; first difference in line 1: expected "x ", found "x"`,
		},
		{
			name:     "diff-form",
			input:    "cmd\n----\n1\n2\n3\n4\n5\n6\t\n",
			actual:   "1\n2\n3\n4\n5\n6",
			expected: note + `; first difference in line 6: expected "6\t", found "6"`,
		},
		{name: "real-difference", input: "cmd\n----\nx \ny\n", actual: "x\nz"},
		{name: "extra-line", input: "cmd\n----\nx\n", actual: "x\n\n"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			msgs := recordAll(t, tc.input, func(t *TBWrapper[*recorderTB], d *TestData) string {
				return tc.actual
			})
			if len(msgs) != 1 {
				t.Fatalf("expected one message, got %q", msgs)
			}
			msg := msgs[0]
			// Both forms of the message start with the same position header.
			if !strings.HasPrefix(msg, "\n<string>:1:\n  cmd\n") {
				t.Fatalf("unexpected message header:\n%s", msg)
			}
			if tc.expected == "" {
				if strings.Contains(msg, note) {
					t.Fatalf("unexpected note:\n%s", msg)
				}
			} else if !strings.HasSuffix(msg, "\n"+tc.expected) {
				t.Fatalf("expected note %q, got:\n%s", tc.expected, msg)
			}
		})
	}
}

// TestSkipNotFatal verifies that a skip is not logged as a fatal error (and
// that a fatal error is).
func TestSkipNotFatal(t *testing.T) {
	msgs := recordAll(t, "cmd\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
		t.Skip("skipped")
		return ""
	})
	if expected := []string{"<string>:1: skipped"}; !reflect.DeepEqual(msgs, expected) {
		t.Fatalf("expected %q, got %q", expected, msgs)
	}
	msgs = recordAll(t, "cmd\ninput\n----\n", func(t *TBWrapper[*recorderTB], d *TestData) string {
		t.Fatal("fatal")
		return ""
	})
	if expected := []string{"<string>:1: fatal", "\nfatal during <string>:1:\ninput"}; !reflect.DeepEqual(msgs, expected) {
		t.Fatalf("expected %q, got %q", expected, msgs)
	}
}
