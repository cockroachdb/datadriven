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
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

const subtestFile = `
cmd top
----
top

subtest a

cmd a1
----
a1

subtest a/b

cmd b1
in
----
----
b1

in
----
----

subtest end a/b

cmd a2
----
a2

subtest end

subtest c

cmd c1
----
c1

subtest end c

cmd last
----
last
`

// TestSubtestNotRun verifies that the directives of a subtest that is not run
// (e.g. because it is not selected by the -run flag) are skipped.
func TestSubtestNotRun(t *testing.T) {
	testCases := []struct {
		skip     string
		expected []string
	}{
		{skip: "", expected: []string{"top", "a1", "b1", "a2", "c1", "last"}},
		{skip: "a", expected: []string{"top", "c1", "last"}},
		{skip: "b", expected: []string{"top", "a1", "a2", "c1", "last"}},
		{skip: "c", expected: []string{"top", "a1", "b1", "a2", "last"}},
	}
	for _, tc := range testCases {
		t.Run("skip="+tc.skip, func(t *testing.T) {
			for _, rewrite := range []bool{false, true} {
				var ran []string
				rec := &recorderTB{TB: t, skipRun: func(name string) bool { return name == tc.skip }}
				var rewritten []byte
				runAndWait(func() {
					rewritten = runTestInternal(rec, "<string>", strings.NewReader(subtestFile),
						func(t *TBWrapper[*recorderTB], d *TestData) string {
							ran = append(ran, d.Args[0])
							if d.Input != "" {
								return d.Args[0] + "\n\n" + d.Input
							}
							return d.Args[0]
						}, rewrite)
				})
				if len(rec.msgs) > 0 {
					t.Fatalf("rewrite=%t: unexpected messages: %q", rewrite, rec.msgs)
				}
				if !reflect.DeepEqual(ran, tc.expected) {
					t.Fatalf("rewrite=%t: expected %q, ran %q", rewrite, tc.expected, ran)
				}
				// The directives that are skipped are preserved.
				if rewrite && string(rewritten) != subtestFile {
					t.Fatalf("the file was rewritten to:\n%s", rewritten)
				}
			}
		})
	}
}

// TestSubtestNotRunErrors verifies that the structure of a subtest that is not
// run is still checked.
func TestSubtestNotRunErrors(t *testing.T) {
	testCases := []struct {
		file     string
		expected string
	}{
		{
			file: "subtest a\n\ncmd\n----\n",
			expected: "<string>:3: EOF encountered without subtest end directive\n" +
				"<string>:1: subtest started here",
		},
		{
			file:     "subtest a\n\nsubtest end b\n",
			expected: `<string>:3: mismatched subtest end directive: expected "a", got "b"`,
		},
		{
			file:     "subtest a\n\nsubtest b\n\nsubtest end\n\nsubtest end\n",
			expected: `<string>:3: name of nested subtest must begin with "a/"`,
		},
		{
			file: "subtest a\n\nsubtest a/b\n\nsubtest end\n",
			expected: "<string>:5: EOF encountered without subtest end directive\n" +
				"<string>:1: subtest started here",
		},
	}
	for _, tc := range testCases {
		rec := &recorderTB{TB: t, skipRun: func(name string) bool { return true }}
		runAndWait(func() {
			RunTestFromString(rec, tc.file, func(t *TBWrapper[*recorderTB], d *TestData) string {
				t.Errorf("directive was run")
				return ""
			})
		})
		if expected := []string{tc.expected}; !reflect.DeepEqual(rec.msgs, expected) {
			t.Errorf("%q: expected %q, got %q", tc.file, expected, rec.msgs)
		}
	}
}

// subtestRunChildEnv is set when TestSubtestRunFlag runs itself in a child
// process.
const subtestRunChildEnv = "DATADRIVEN_TEST_SUBTEST_RUN_CHILD"

// TestSubtestRunFlag verifies that the -run flag can be used to select the
// subtests of a test file.
func TestSubtestRunFlag(t *testing.T) {
	if os.Getenv(subtestRunChildEnv) != "" {
		RunTestFromString(t, subtestFile, func(t *T, d *TestData) string {
			t.Logf("ran %s", d.Args[0])
			if d.Input != "" {
				return d.Args[0] + "\n\n" + d.Input
			}
			return d.Args[0]
		})
		return
	}
	testCases := []struct {
		run      string
		expected []string
	}{
		{run: "", expected: []string{"top", "a1", "b1", "a2", "c1", "last"}},
		{run: "/^a$", expected: []string{"top", "a1", "b1", "a2", "last"}},
		{run: "/^a$/^b$", expected: []string{"top", "a1", "b1", "a2", "last"}},
		{run: "/^a$/^none$", expected: []string{"top", "a1", "a2", "last"}},
		{run: "/^c$", expected: []string{"top", "c1", "last"}},
		{run: "/^none$", expected: []string{"top", "last"}},
	}
	ranRe := regexp.MustCompile(`(?m): ran (\w+)$`)
	for _, tc := range testCases {
		cmd := exec.Command(
			os.Args[0], "-test.run=^TestSubtestRunFlag$"+tc.run, "-test.v", "-test.count=1",
		)
		cmd.Env = append(os.Environ(), subtestRunChildEnv+"=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Errorf("-run %q: %v\n%s", tc.run, err, out)
			continue
		}
		var ran []string
		for _, m := range ranRe.FindAllStringSubmatch(string(out), -1) {
			ran = append(ran, m[1])
		}
		if !reflect.DeepEqual(ran, tc.expected) {
			t.Errorf("-run %q: expected %q, ran %q\n%s", tc.run, tc.expected, ran, out)
		}
	}
}
