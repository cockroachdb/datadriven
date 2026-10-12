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
	"path/filepath"
	"strings"
	"testing"
)

func TestClearResults(t *testing.T) {
	testCases := []struct {
		name  string
		input string
		// Either expected or expectedErr is set. The path of the file is
		// replaced with <path> in the error.
		expected    string
		expectedErr string
	}{
		{
			name: "basic",
			input: `# A comment.
cmd a=1 \
  b=2
input line
----
some output

subtest foo

no-output
----

subtest foo/bar

blank-lines
----
----
out

more out
----
----

subtest end foo/bar

subtest end foo

last
----
last output
`,
			expected: `# A comment.
cmd a=1 \
  b=2
input line
----

subtest foo

no-output
----

subtest foo/bar

blank-lines
----

subtest end foo/bar

subtest end foo

last
----
`,
		},
		{
			name:        "parse-error",
			input:       "cmd\n----\nfoo\n\nxx =\n----\nbar\n",
			expectedErr: "<path>:5: cannot parse directive at column 4: xx =",
		},
		{
			// Processing stops at the first error.
			name:        "two-errors",
			input:       "xx =\n\nyy =\n",
			expectedErr: "<path>:1: cannot parse directive at column 4: xx =",
		},
		{
			name:        "missing-subtest-end",
			input:       "subtest foo\n\ncmd\n----\nfoo\n",
			expectedErr: "<path>:3: EOF encountered without subtest end directive\n<path>:1: subtest started here",
		},
		{
			name:        "error-in-nested-subtest",
			input:       "subtest foo\n\nsubtest bar\n\nsubtest end bar\n\nsubtest end foo\n",
			expectedErr: `<path>:3: name of nested subtest must begin with "foo/"`,
		},
		{
			name:        "double-separator",
			input:       "cmd\n----\n----\nfoo\n----\n----\nbar\n",
			expectedErr: "<path>:7: non-blank line after the end of a double ---- separator section",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "test")
			if err := os.WriteFile(path, []byte(tc.input), 0644); err != nil {
				t.Fatal(err)
			}
			err := ClearResults(path)
			contents, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if tc.expectedErr != "" {
				if err == nil {
					t.Fatalf("expected error %q", tc.expectedErr)
				}
				if msg := strings.ReplaceAll(err.Error(), path, "<path>"); msg != tc.expectedErr {
					t.Fatalf("expected error:\n%s\ngot:\n%s", tc.expectedErr, msg)
				}
				if string(contents) != tc.input {
					t.Fatalf("file modified on error:\n%s", contents)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(contents) != tc.expected {
				t.Fatalf("expected:\n%s\ngot:\n%s", tc.expected, contents)
			}
			// Clearing again is a no-op.
			if err := ClearResults(path); err != nil {
				t.Fatal(err)
			}
			if again, err := os.ReadFile(path); err != nil || string(again) != tc.expected {
				t.Fatalf("clearing again changed the file (err: %v):\n%s", err, again)
			}
		})
	}

	t.Run("directory", func(t *testing.T) {
		dir := t.TempDir()
		if err := ClearResults(dir); err == nil || !strings.Contains(err.Error(), "is a directory") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}
