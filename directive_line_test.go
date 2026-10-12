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
// directive line, even if the test modified CmdArgs.
func TestDirectiveLineInErrors(t *testing.T) {
	const input = `
cmd a=(1,2)  b \
  c=3
input
----
wrong
`
	rec := &recorderTB{TB: t, keepFramework: true}
	runAndWait(func() {
		runFromString(rec, input, func(t *TBWrapper[*recorderTB], d *TestData) string {
			if d.FullCmd() != "cmd a=(1, 2) b c=3" {
				t.Fatalf("unexpected FullCmd: %s", d.FullCmd())
			}
			d.CmdArgs = d.CmdArgs[1:]
			if d.FullCmd() != "cmd b c=3" {
				t.Fatalf("unexpected FullCmd: %s", d.FullCmd())
			}
			return "right"
		})
	})
	expected := []string{
		"\n<string>:2:\n  cmd a=(1,2)  b  c=3\n  input\n\nexpected:\n  wrong\n\nfound:\n  right",
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
	td := TestData{Cmd: "cmd", CmdArgs: []CmdArg{{Key: "a"}}, Input: "x"}
	if s := td.origString(); s != "cmd a\nx\n" {
		t.Fatalf("unexpected origString: %q", s)
	}
}
