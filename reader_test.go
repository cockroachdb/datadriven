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
