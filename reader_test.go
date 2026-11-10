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
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

// TestLongLines verifies that there is no limit on the length of the lines in a
// test file.
func TestLongLines(t *testing.T) {
	long := strings.Repeat("x", 200_000)
	file := "echo " + long + "\n" + long + "\n----\n" + long + "\n" + long + "\n\necho bar\nfoo\n----\nbar\nfoo\n"
	numDirectives := 0
	echo := func(t *T, d *TestData) string {
		numDirectives++
		return strings.Join(append(d.Args, d.Input), "\n")
	}
	runFromString(t, file, echo)
	if numDirectives != 2 {
		t.Fatalf("ran %d directives", numDirectives)
	}
	// Rewriting must preserve the file.
	if out := string(runTestInternal(t, "<string>", strings.NewReader(file), echo, true /* rewrite */)); out != file {
		t.Fatalf("the file was rewritten to %d bytes (from %d)", len(out), len(file))
	}
}

// TestLineEndings verifies that CRLF line endings and a missing final newline
// are tolerated.
func TestLineEndings(t *testing.T) {
	var inputs []string
	runFromString(t, "cmd\r\nfoo\r\nbar\r\n----\r\nok\r\n\r\ncmd\nbaz\n----\nok", func(t *T, d *TestData) string {
		inputs = append(inputs, d.Input)
		return "ok"
	})
	if expected := []string{"foo\nbar", "baz"}; !reflect.DeepEqual(inputs, expected) {
		t.Fatalf("expected %q, got %q", expected, inputs)
	}
}

// failingReader returns some data, followed by an error.
type failingReader struct {
	r io.Reader
}

func (f *failingReader) Read(p []byte) (int, error) {
	n, err := f.r.Read(p)
	if err == io.EOF {
		err = errors.New("injected read error")
	}
	return n, err
}

// TestReadError verifies that an error reading the test file is reported (and
// is not treated as the end of the file).
func TestReadError(t *testing.T) {
	rec := &recorderTB{TB: t}
	ran := false
	runAndWait(func() {
		r := &failingReader{r: strings.NewReader("cmd\n----\nok\n")}
		runTestInternal(rec, "file", r, func(t *TBWrapper[*recorderTB], d *TestData) string {
			ran = true
			return "ok"
		}, false /* rewrite */)
	})
	if ran {
		t.Fatalf("the directive was run")
	}
	if expected := []string{"file: injected read error"}; !reflect.DeepEqual(rec.msgs, expected) {
		t.Fatalf("expected %q, got %q", expected, rec.msgs)
	}
}

// TestRewriteRoundTrip verifies that rewriting produces a file in which the
// expected output is the actual output (and the directives that follow are
// preserved), for outputs with ---- lines, blank lines and carriage returns.
// Rewriting must only fail for an output that can't be written in any form.
func TestRewriteRoundTrip(t *testing.T) {
	alphabet := []string{"----", "", "a", "  ", "a\r", "----\r"}
	const maxLines = 4
	const rest = "\nlast\n----\nfoo\n"
	var numErrors, numOutputs int
	var test func(lines []string)
	test = func(lines []string) {
		if len(lines) < maxLines {
			for _, l := range alphabet {
				test(append(lines[:len(lines):len(lines)], l))
			}
		}
		if len(lines) == 0 {
			return
		}
		numOutputs++
		output := strings.Join(lines, "\n")
		handler := func(t *TBWrapper[*recorderTB], d *TestData) string {
			if d.Cmd == "last" {
				return "foo"
			}
			return output
		}
		// run returns the rewritten file (if rewriting) and the messages, and
		// whether the file passed.
		run := func(file string, rewrite bool) (rewritten string, msgs []string, ok bool) {
			rec := &recorderTB{TB: t}
			numDirectives := 0
			runAndWait(func() {
				rewritten = string(runTestInternal(rec, "<string>", strings.NewReader(file),
					func(t *TBWrapper[*recorderTB], d *TestData) string {
						numDirectives++
						return handler(t, d)
					}, rewrite))
			})
			return rewritten, rec.msgs, !rec.Failed() && numDirectives == 2
		}
		passes := func(file string) bool {
			_, _, ok := run(file, false /* rewrite */)
			return ok
		}
		file, msgs, ok := run("cmd\n----\n"+rest, true /* rewrite */)
		if !ok {
			if len(msgs) != 1 || !strings.HasPrefix(msgs[0], "<string>:1: cannot rewrite the output: ") {
				t.Fatalf("output %q: unexpected messages: %q", output, msgs)
			}
			// Neither form must work.
			normalized := output
			if normalized != "" && !strings.HasSuffix(normalized, "\n") {
				normalized += "\n"
			}
			for _, form := range []string{
				"cmd\n----\n" + normalized + rest,
				"cmd\n----\n----\n" + normalized + "----\n----\n" + rest,
			} {
				if passes(form) {
					t.Fatalf("output %q: %s\nbut it can be written as:\n%s", output, msgs[0], form)
				}
			}
			numErrors++
			return
		}
		if !passes(file) {
			t.Fatalf("output %q was written as:\n%s\nwhich doesn't pass", output, file)
		}
		if again, _, _ := run(file, true /* rewrite */); again != file {
			t.Fatalf("output %q was written as:\n%s\nwhich was rewritten to:\n%s", output, file, again)
		}
	}
	test(nil)
	if numErrors == 0 || numErrors == numOutputs {
		t.Fatalf("%d of the %d outputs could not be written", numErrors, numOutputs)
	}
}

// TestRewriteErrors verifies the errors for outputs that can't be written to a
// test file.
func TestRewriteErrors(t *testing.T) {
	const consecutive = "<string>:1: cannot rewrite the output: an output with blank lines (or which " +
		"starts with a ---- line) can't end with a ---- line or contain two consecutive ---- lines"
	testCases := []struct {
		output   string
		expected string
	}{
		{"----", consecutive},
		{"a\n\n----\n----\nb", consecutive},
		{"a\n\n----", consecutive},
		{"a\r\nb", "<string>:1: cannot rewrite the output: line 1 of the output ends with a carriage return"},
		{"a\nb\r", "<string>:1: cannot rewrite the output: line 2 of the output ends with a carriage return"},
	}
	for _, tc := range testCases {
		rec := &recorderTB{TB: t}
		runAndWait(func() {
			runTestInternal(rec, "<string>", strings.NewReader("cmd\n----\n"),
				func(t *TBWrapper[*recorderTB], d *TestData) string { return tc.output }, true /* rewrite */)
		})
		if expected := []string{tc.expected}; !reflect.DeepEqual(rec.msgs, expected) {
			t.Errorf("output %q: expected %q, got %q", tc.output, expected, rec.msgs)
		}
	}
}
