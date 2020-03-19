// Copyright 2019 The Cockroach Authors.
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
	"io/ioutil"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/cockroachdb/errors"
	"github.com/otiai10/copy"
)

func TestNewLineBetweenDirectives(t *testing.T) {
	RunTestFromString(t, `
# Some testing of sensitivity to newlines
foo
----
unknown command

bar
----
unknown command




bar
----
unknown command
`, func(t *testing.T, d *TestData) string {
		if d.Input != "sentence" {
			return "unknown command"
		}
		return ""
	})
}

func TestParseLine(t *testing.T) {
	RunTestFromString(t, `
parse
xx +++
----
here: cannot parse directive at column 4: xx +++

parse
xx a=b a=c
----
"xx" [a=b a=c]

parse
xx a=b b=c c=(1,2,3)
----
"xx" [a=b b=c c=(1, 2, 3)]
`, func(t *testing.T, d *TestData) string {
		cmd, args, err := ParseLine(d.Input)
		if err != nil {
			return errors.Wrap(err, "here").Error()
		}
		return fmt.Sprintf("%q %+v", cmd, args)
	})
}

func TestSkip(t *testing.T) {
	RunTestFromString(t, `
skip
----

# This error should never happen.
error
----
`, func(t *testing.T, d *TestData) string {
		switch d.Cmd {
		case "skip":
			// Verify that calling t.Skip() does not fail with an API error on
			// testing.T.
			t.Skip("woo")
		case "error":
			// The skip should mask the error afterwards.
			t.Error("never reached")
		}
		return d.Expected
	})
}

func TestArgFormat(t *testing.T) {
	RunTestFromString(t, `
# NB: we allow duplicate args.
# ScanArgs simply picks the first occurrence.
make argTuple=(1, 🍌) argInt=12 argString=greedily,impatient moreIgnore= a,b,c
sentence
----
Did the following: make sentence
1 hungry monkey eats a 🍌
while 12 other monkeys watch greedily,impatient
true I'd say
`, func(t *testing.T, d *TestData) string {
		var one int
		var twelve int
		var banana string
		var greedily string
		d.ScanArgs(t, "argTuple", &one, &banana)
		d.ScanArgs(t, "argInt", &twelve)
		d.ScanArgs(t, "argString", &greedily)
		abc := d.HasArg("a,b,c")
		return fmt.Sprintf(`Did the following: %s %s
%d hungry monkey eats a %s
while %d other monkeys watch %s
%v I'd say`,
			d.Cmd, d.Input, one, banana, twelve, greedily, abc,
		)
	})
}

func TestSubTest(t *testing.T) {
	foundData := false
	Walk(t, "testdata/subtest", func(t *testing.T, path string) {
		foundData = true
		RunTest(t, path, func(t *testing.T, d *TestData) string {
			switch d.Cmd {
			case "hello":
				return d.CmdArgs[0].Key + " was said"
			case "skip":
				// Verify that calling t.Skip() does not fail with an API error on
				// testing.T.
				t.Skip("woo")
			case "error":
				// The skip should mask the error afterwards.
				t.Error("never reached")
			default:
				t.Fatalf("unknown directive: %s", d.Cmd)
			}
			return d.Expected
		})
	})

	if !foundData {
		t.Fatalf("no data file found")
	}
}

func TestGoTestInterface(t *testing.T) {
	runTestOverDirectory(t, "TestRewrite", "testdata/no_rewrite_needed",
		false /*verbose*/, []string{"-rewrite"},
		`ok  	github.com/cockroachdb/datadriven`, ``)

	runTestOverDirectory(t, "TestRewrite", "testdata/rewrite",
		false /*verbose*/, []string{"-rewrite"},
		`ok  	github.com/cockroachdb/datadriven`,
		`diff -uNr testdata/rewrite/example <datadir>/example
--- testdata/rewrite/example
+++ <datadir>/example
@@ -1,6 +1,7 @@
 hello universe
 ----
-incorrect output
+universe was said
 
 hello planet
 ----
+planet was said
diff -uNr testdata/rewrite/whitespace <datadir>/whitespace
--- testdata/rewrite/whitespace
+++ <datadir>/whitespace
@@ -2,22 +2,23 @@
 hello world
 
 ----
-wrong
+world was said
 
 # Command with no output
 nothing
 ----
-wrong
 
 # Command with whitespace output
 blank
 ----
-wrong
+----
 
+----
+----
 blank
 ----
 ----
-wrong
+
 ----
 ----
 `)
}

func runTestOverDirectory(
	t *testing.T, testName, dataDir string, verbose bool, extraArgs []string, refTest, refDiff string,
) {
	tmpDir, err := ioutil.TempDir("", "go-datadriven")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if t.Failed() {
			t.Logf("test files remaining in: %s", tmpDir)
		} else {
			os.RemoveAll(tmpDir)
		}
	}()

	if err := copy.Copy(dataDir, tmpDir); err != nil {
		t.Fatal(err)
	}

	// Run go test.
	args := []string{"test", "-tags", "gotest"}
	if verbose {
		args = append(args, "-v")
	}
	args = append(args,
		"-run", testName+"$",
		".",
		"-args", "-datadir", tmpDir)
	args = append(args, extraArgs...)
	testCmd := exec.Command("go", args...)
	testOut, err := testCmd.CombinedOutput()
	if err != nil {
		// Special case: if the exit code is specifically 1, we're going
		// to ignore it -- this simply signals there was a test error. The
		// expected/actual compare below will catch it.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Fatalf("cmd %v:\n%s\nerror: %v", testCmd, testOut, err)
		}
	}

	if refTest != "" && !strings.HasSuffix(refTest, "\n") {
		refTest += "\n"
	}
	actual := postProcessGoTestOutput(tmpDir, string(testOut))
	if string(actual) != refTest {
		t.Errorf("\nexpected go test output:\n%s\ngot:\n%s", refTest, actual)
	}

	// Diff the test files, to check if rewriting happened.
	diffCmd := exec.Command("diff", "-uNr", dataDir, tmpDir)
	diffOut, err := diffCmd.CombinedOutput()
	if err != nil {
		// Special case: if the exit code is specifically 1, we're going
		// to ignore it -- this simply signals the diff is not empty. The
		// expected/actual compare below will catch it.
		if ee, ok := err.(*exec.ExitError); !ok || ee.ExitCode() != 1 {
			t.Fatalf("cmd %v:\n%s\neerror: %v", diffCmd, diffOut, err)
		}
	}

	if refDiff != "" && !strings.HasSuffix(refDiff, "\n") {
		refDiff += "\n"
	}
	actual = postProcessDiffOutput(dataDir, tmpDir, string(diffOut))
	if string(actual) != refDiff {
		t.Errorf("\nexpected testadata diff:\n%s\ngot:\n%s", refDiff, actual)
	}
}

var resultTs = regexp.MustCompile(`(?m:^((?:FAIL|ok)\s+\S+).*$)`)
var intermediateTs = regexp.MustCompile(`(?m:^(\s*---.*)\s+\(.*\)$)`)

func postProcessGoTestOutput(tmpDir, testOut string) string {
	testOut = strings.ReplaceAll(testOut, tmpDir, "<datadir>")
	testOut = resultTs.ReplaceAllString(testOut, "$1")
	testOut = intermediateTs.ReplaceAllString(testOut, "$1")
	return testOut
}

var diffTs = regexp.MustCompile(`(?m:^((?:\+\+\+|---)\s+\S+).*$)`)

func postProcessDiffOutput(dataDir, tmpDir, testOut string) string {
	testOut = strings.ReplaceAll(testOut, tmpDir, "<datadir>")
	testOut = diffTs.ReplaceAllString(testOut, "$1")
	return testOut
}
