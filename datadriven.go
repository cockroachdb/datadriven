// Copyright 2018 The Cockroach Authors.
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
	"flag"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/pmezard/go-difflib/difflib"
)

var (
	rewriteFlag = boolFlag(
		flag.CommandLine, "rewrite",
		"ignore the expected results and rewrite the test files with the actual results from this "+
			"run. Used to update tests when a change affects many cases; please verify the testfile "+
			"diffs carefully!",
	)

	quietFlag = boolFlag(
		flag.CommandLine, "datadriven-quiet",
		"avoid echoing the directives and responses from test files.",
	)
)

// boolFlag defines a bool flag in the flag set, or reuses it if it is already
// defined. This allows v1 of this package (which defines the same flags on the
// command-line flag set, and panics if they are already defined) to be linked
// in the same binary. This relies on v1 being initialized first: Go initializes
// packages in import path order (among those whose dependencies are
// initialized), and v1.0.2 and later only depend on the standard library and
// go-difflib, which are initialized by the time we are ready. Older versions of
// v1 depend on github.com/cockroachdb/errors and can be initialized after us.
func boolFlag(fs *flag.FlagSet, name, usage string) func() bool {
	if f := fs.Lookup(name); f != nil {
		g, ok := f.Value.(flag.Getter)
		if ok {
			_, ok = g.Get().(bool)
		}
		if !ok {
			panic(fmt.Sprintf("datadriven: flag -%s is already defined, with a non-bool type", name))
		}
		return func() bool { return g.Get().(bool) }
	}
	p := fs.Bool(name, false, usage)
	return func() bool { return *p }
}

// Rewrite returns true if -rewrite was passed: test files are rewritten with
// the actual results (see RunTest). It is only meaningful after the flags are
// parsed (e.g. inside a test).
func Rewrite() bool {
	return rewriteFlag()
}

// Verbose returns true if directives and their outputs are logged: the test is
// run with -test.v, and neither -datadriven-quiet nor DATADRIVEN_QUIET_LOG=true
// is set.
func Verbose() bool {
	return testing.Verbose() && !quietFlag()
}

// In CockroachDB we want to quiesce all the logs across all packages.
// If we had only a flag to work with, we'd get command line parsing
// errors on all packages that do not use datadriven. So
// we make do by also making an environment variable available.
func init() {
	const quietEnvVar = "DATADRIVEN_QUIET_LOG"
	if str, ok := os.LookupEnv(quietEnvVar); ok {
		if _, err := strconv.ParseBool(str); err != nil {
			panic(fmt.Sprintf("error parsing %s: %s", quietEnvVar, err))
		}
		if err := flag.Set("datadriven-quiet", str); err != nil {
			panic(fmt.Sprintf("error setting -datadriven-quiet from %s: %s", quietEnvVar, err))
		}
	}
}

// RunTest runs the data-driven test in the given file (see the package
// documentation for the file format). The function is called for each
// directive in the file and returns the actual output, which RunTest compares
// with the expected output (or, with -rewrite, uses to rewrite the file).
//
// RunTest works over any testing.TB, typically a *testing.T or a *testing.B.
// The function is passed a *TBWrapper which wraps it (a *T for a *testing.T, a
// *B for a *testing.B) and annotates errors, skips and logs with the position
// of the current directive in the test file; the same wrapper is available as
// td.T. A failure (including t.Error) stops the processing of the file after
// the current directive.
//
// RunTest runs each "subtest" section in a subtest, so the function must use
// the wrapper it is passed (and not the parent testing.TB) to fail or skip the
// test. Subtests are not supported over a *testing.B (testing.B.Run runs the
// function of a sub-benchmark multiple times, but the directives can only be
// read once).
//
// An expected error can be tested by returning the error as the output; an
// unexpected error should fail the test (e.g. with t.Fatal).
func RunTest[U testing.TB](t U, path string, f func(t *TBWrapper[U], td *TestData) string) {
	t.Helper()

	mode := os.O_RDONLY
	if rewriteFlag() {
		// We only open read-write if rewriting, so as to enable running
		// tests on read-only copies of the source tree.
		mode = os.O_RDWR
	}
	file, err := os.OpenFile(path, mode, 0644 /* irrelevant */)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = file.Close()
	}()
	finfo, err := file.Stat()
	if err != nil {
		t.Fatal(err)
	} else if finfo.IsDir() {
		t.Fatalf("%s is a directory, not a file; consider using datadriven.Walk", path)
	}

	rewriteData := runTestInternal(t, path, file, f, rewriteFlag())
	if rewriteFlag() {
		if err := rewriteFile(file, rewriteData); err != nil {
			t.Fatal(err)
		}
	}
}

// rewriteFile replaces the contents of the file with the given data.
func rewriteFile(file *os.File, data []byte) error {
	if _, err := file.WriteAt(data, 0); err != nil {
		return err
	}
	if err := file.Truncate(int64(len(data))); err != nil {
		return err
	}
	return file.Sync()
}

// RunTestFromString is a version of RunTest which takes the contents of a test
// directly. The outputs are always compared with the expected outputs, even
// with -rewrite (there is no file to rewrite).
func RunTestFromString[U testing.TB](
	t U, input string, f func(t *TBWrapper[U], td *TestData) string,
) {
	t.Helper()
	runTestInternal(t, "<string>" /* sourceName */, strings.NewReader(input), f, false /* rewrite */)
}

func runTestInternal[U testing.TB](
	t U,
	sourceName string,
	reader io.Reader,
	f func(t *TBWrapper[U], td *TestData) string,
	rewrite bool,
) (rewriteOutput []byte) {
	t.Helper()
	// If a wrapper passed to a callback is passed back to us as a testing.TB
	// (e.g. to run a nested test file), use the underlying testing.TB: the
	// nested file has its own directive positions.
	t = unwrap(t)

	r := newTestDataReader(t, sourceName, reader, rewrite)
	// Wrappers retained beyond this point (e.g. used in a t.Cleanup function)
	// should not annotate messages with the position of the last directive.
	defer r.pos.set("")
	for r.Next(t) {
		runDirectiveOrSubTest(t, r, "" /*mandatorySubTestPrefix*/, f)
	}

	if r.rewrite != nil {
		data := r.rewrite.Bytes()
		// Remove any trailing blank line.
		if l := len(data); l > 2 && data[l-1] == '\n' && data[l-2] == '\n' {
			data = data[:l-1]
		}
		return data
	}
	return nil
}

// runDirectiveOrSubTest runs either a "subtest" directive or an
// actual test directive. The "mandatorySubTestPrefix" argument indicates
// a mandatory prefix required from all sub-test names at this point.
func runDirectiveOrSubTest[U testing.TB](
	t U,
	r *testDataReader,
	mandatorySubTestPrefix string,
	f func(t *TBWrapper[U], td *TestData) string,
) {
	t.Helper()
	if subTestName, ok := isSubTestStart(t, r, mandatorySubTestPrefix); ok {
		runSubTest(subTestName, t, r, f)
	} else {
		runDirective(t, r, f)
	}
	if t.Failed() {
		// If a test has failed with .Error(), we can't expect any
		// subsequent test to be even able to start. Stop processing the
		// file in that case.
		t.FailNow()
	}
}

// runSubTest runs a subtest up to and including the final `subtest
// end`. The opening `subtest` directive has been consumed already.
// The first parameter `subTestName` is the full path to the subtest,
// including the parent subtest names as prefix. This is used to
// validate the nesting and thus prevent mistakes.
func runSubTest[U testing.TB](
	subTestName string, t U, r *testDataReader, f func(t *TBWrapper[U], td *TestData) string,
) {
	// Remember the current reader position in case we need to spell out
	// an error message below.
	subTestStartPos := r.data.Pos
	// seenSubTestEnd is used below to verify that a "subtest end" directive
	// has been detected (as opposed to EOF).
	seenSubTestEnd := false
	// seenSkip is used below to verify that "Skip" has not been used
	// inside a subtest. See below for details.
	seenSkip := false
	// ran is set if the subtest function was called; it is not called if the
	// subtest is not selected (e.g. by the -run flag).
	ran := false

	// The name passed to t.Run is the last component in the subtest
	// name, because all components before that are already prefixed by
	// t.Run from the names of the parent sub-tests.
	testingSubTestName := subTestName[strings.LastIndex(subTestName, "/")+1:]

	// Begin the sub-test.
	subTest(t, testingSubTestName, func(t U) {
		ran = true
		// Cleanup functions registered on the subtest run when it finishes; they
		// should not be annotated with the position of its last directive.
		defer r.pos.set("")
		defer func() {
			// Skips are signalled using Goexit() so we must catch it /
			// remember it here.
			if t.Skipped() {
				seenSkip = true
			}
		}()

		for r.Next(t) {
			if isSubTestEnd(t, r) {
				seenSubTestEnd = true
				return
			}
			runDirectiveOrSubTest(t, r, subTestName+"/" /*mandatorySubTestPrefix*/, f)
		}
	})

	if !ran {
		// The directives of the subtest are skipped (they must not run as part of
		// the parent test).
		skipSubTest(t, r, subTestName, subTestStartPos)
		return
	}

	if seenSkip {
		// t.Skip() is not yet supported inside a subtest. To add
		// this functionality the following extra complexity is needed:
		// - the test reader must continue to read after the skip
		//   until the end of the subtest, and ignore all the directives in-between.
		// - the rewrite logic must be careful to keep the input as-is
		//   for the skipped sub-test, while proceeding to rewrite for
		//   non-skipped tests.
		r.fatalf(t,
			"cannot use t.Skip inside subtest\n%s: subtest started here", subTestStartPos)
	}

	if !seenSubTestEnd && t.Failed() {
		// We only report a missing "subtest end" if there was no error otherwise;
		// for if there was an error, the reading would have stopped.
		return
	}
	checkSubTestEnd(t, r, subTestName, subTestStartPos, seenSubTestEnd)
}

// skipSubTest reads the directives of a subtest that is not run, up to and
// including the final `subtest end` (the opening `subtest` directive has been
// consumed already). When rewriting, the directives are preserved.
func skipSubTest(t testing.TB, r *testDataReader, subTestName string, subTestStartPos Pos) {
	seenSubTestEnd := false
	for !seenSubTestEnd && r.Next(t) {
		if isSubTestEnd(t, r) {
			seenSubTestEnd = true
		} else if nested, ok := isSubTestStart(t, r, subTestName+"/" /* mandatorySubTestPrefix */); ok {
			skipSubTest(t, r, nested, r.data.Pos)
		} else if err := r.emitOutput(r.data.Expected); err != nil {
			r.fatalf(t, "cannot rewrite the output: %v", err)
		}
	}
	checkSubTestEnd(t, r, subTestName, subTestStartPos, seenSubTestEnd)
}

// checkSubTestEnd verifies that a subtest ended with a `subtest end` directive
// (as opposed to EOF) which, if it has a name, matches the name of the subtest.
func checkSubTestEnd(
	t testing.TB, r *testDataReader, subTestName string, subTestStartPos Pos, seenSubTestEnd bool,
) {
	if !seenSubTestEnd {
		r.fatalf(t,
			"EOF encountered without subtest end directive\n%s: subtest started here", subTestStartPos)
	}
	if len(r.data.Args) == 2 && r.data.Args[1] != subTestName {
		// If a subtest name was provided after "subtest end", ensure that it matches.
		r.fatalf(t,
			"mismatched subtest end directive: expected %q, got %q", subTestName, r.data.Args[1])
	}
}

func isSubTestStart(t testing.TB, r *testDataReader, mandatorySubTestPrefix string) (string, bool) {
	if r.data.Cmd != "subtest" {
		return "", false
	}
	if _, ok := innermost(t).(*testing.B); ok {
		r.fatalf(t, "subtest directives are not supported in benchmarks")
	}
	if len(r.data.Args) != 1 || len(r.data.Flags) != 0 {
		r.fatalf(t, "invalid syntax for subtest")
	}
	subTestName := r.data.Args[0]
	if subTestName == "end" {
		r.fatalf(t, "subtest end without corresponding start")
	}
	if !strings.HasPrefix(subTestName, mandatorySubTestPrefix) {
		r.fatalf(t, "name of nested subtest must begin with %q", mandatorySubTestPrefix)
	}
	return subTestName, true
}

func isSubTestEnd(t testing.TB, r *testDataReader) bool {
	if r.data.Cmd != "subtest" {
		return false
	}
	if len(r.data.Args) == 0 || r.data.Args[0] != "end" {
		return false
	}
	if len(r.data.Args) > 2 || len(r.data.Flags) != 0 {
		r.fatalf(t, "invalid syntax for subtest end")
	}
	return true
}

// runDirective runs just one directive in the input.
func runDirective[U testing.TB](
	t U, r *testDataReader, f func(t *TBWrapper[U], td *TestData) string,
) {
	t.Helper()
	// Framework messages are reported directly to the innermost testing.TB (in
	// case t is itself a wrapper), so that they are not annotated.
	raw := innermost(t)

	// Each directive gets its own TestData, so that it remains valid (and
	// unchanged) if the callback retains it (e.g. in a t.Cleanup function).
	d := new(TestData)
	*d = r.data
	processed := &processedFlags{}
	// The framework messages and the check for unprocessed flags use a copy of
	// the directive, which is not affected by changes the callback makes to d
	// (including to the elements of d.Flags).
	orig := r.data
	orig.Flags = slices.Clone(orig.Flags)
	actual := func() string {
		fReturned := false
		defer func() {
			if r := recover(); r != nil {
				raw.Logf("\npanic during %s:\n%s\n", orig.Pos, orig.Input)
				panic(r)
			} else if !fReturned && !raw.Skipped() {
				// t.Fatal calls runtime.Goexit() which runs the defers but there is no
				// panic to recover.
				raw.Logf("\nfatal during %s:\n%s\n", orig.Pos, orig.Input)
			}
		}()

		// Set up a goroutine to log periodically if the function is taking a long
		// time. This is useful to pinpoint the cause of a test timeout.
		done := make(chan struct{})
		go func() {
			startTime := time.Now()
			for {
				select {
				case <-done:
					return
				case <-time.After(10 * time.Second):
					raw.Logf("%s: still running after %s\n", orig.Pos, time.Since(startTime))
				}
			}
		}()
		defer func() {
			// Because the channel is unbuffered, we wait here until the goroutine is
			// exiting.
			done <- struct{}{}
		}()

		r.pos.set(orig.Pos.String())
		w := newWrapper(t, &r.pos)
		d.T = w
		d.curPos = &r.pos
		// The processed flags are shared by all the copies of d. The check below
		// uses this set even if the callback overwrites *d.
		d.processed = processed
		actual := f(w, d)
		fReturned = true
		if actual != "" && !strings.HasSuffix(actual, "\n") {
			actual += "\n"
		}
		return actual
	}()

	if t.Failed() {
		// If the test has failed with .Error(), then we can't hope it
		// will have produced a useful actual output. Trying to do
		// something with it here would risk corrupting the expected
		// output.
		//
		// Moreover, we can't expect any subsequent test to be even
		// able to start. Stop processing the file in that case.
		t.FailNow()
	}

	// All the flags on the directive line must have been processed; otherwise,
	// the test is probably not doing what the test file says (e.g. a flag is
	// misspelled).
	if unprocessed := processed.unprocessed(orig.Flags); len(unprocessed) > 0 {
		raw.Fatalf("%s: unprocessed flags: %s", orig.Pos, strings.Join(unprocessed, " "))
	}

	// The test has not failed, we can analyze the expected
	// output.
	if r.rewrite != nil {
		if err := r.emitOutput(actual); err != nil {
			raw.Fatalf("%s: cannot rewrite the output: %v", orig.Pos, err)
		}
	} else if orig.Expected != actual {
		expectedLines := difflib.SplitLines(orig.Expected)
		actualLines := difflib.SplitLines(actual)
		if len(expectedLines) > 5 {
			// Print a unified diff if there is a lot of output to compare.
			diff, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
				Context: 5,
				A:       expectedLines,
				B:       actualLines,
			})
			if err == nil {
				raw.Fatalf("\n%s:\n%soutput didn't match expected:\n%s%s", orig.Pos, indentLines(orig.origString()), indentLines(diff), whitespaceNote(orig.Expected, actual))
				return
			}
			raw.Logf("Failed to produce diff %v", err)
		}
		raw.Fatalf("\n%s:\n%s\nexpected:\n%s\nfound:\n%s%s", orig.Pos, indentLines(orig.origString()), indentLines(orig.Expected), indentLines(actual), whitespaceNote(orig.Expected, actual))
	} else if Verbose() {
		raw.Logf("\n%s:\n%s  ----\n%s", orig.Pos, indentLines(orig.origString()), indentLines(actual))
	}
}

// whitespaceNote returns a note to append to a mismatch message if the expected
// and actual outputs differ only in trailing whitespace (which is not visible in
// the message), or "" otherwise.
func whitespaceNote(expected, actual string) string {
	e := strings.Split(expected, "\n")
	a := strings.Split(actual, "\n")
	if len(e) != len(a) {
		return ""
	}
	first := -1
	for i := range e {
		if e[i] == a[i] {
			continue
		}
		if strings.TrimRight(e[i], " \t\r") != strings.TrimRight(a[i], " \t\r") {
			return ""
		}
		if first == -1 {
			first = i
		}
	}
	if first == -1 {
		return ""
	}
	return fmt.Sprintf(
		"\nthe outputs differ only in trailing whitespace; first difference in line %d: expected %q, found %q",
		first+1, e[first], a[first],
	)
}

// Walk goes through all the files in a subdirectory, creating subtests to match
// the file hierarchy; for each "leaf" file, the given function is called.
//
// This can be used in conjunction with RunTest. For example:
//
//	datadriven.Walk(t, path, func(t *testing.T, path string) {
//		// initialize per-test state
//		datadriven.RunTest(t, path, func(t *datadriven.T, d *datadriven.TestData) string {
//			// ...
//			return ""
//		})
//	})
//
//	Files:
//	  testdata/typing
//	  testdata/logprops/scan
//	  testdata/logprops/select
//
// If path is "testdata/typing", the function is called once and no subtests
// are created.
//
// If path is "testdata/logprops", the function is called two times, in
// separate subtests /scan, /select.
//
// If path is "testdata", the function is called three times, in subtest
// hierarchy /typing, /logprops/scan, /logprops/select.
//
// Walk works over any testing.TB, typically a *testing.T or a *testing.B; the
// function is passed the testing.TB of the (sub)test.
func Walk[U testing.TB](t U, path string, f func(t U, path string)) {
	t.Helper()
	finfo, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !finfo.IsDir() {
		f(t, path)
		return
	}
	files, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if tempFileRe.MatchString(file.Name()) {
			// Temp or hidden file, don't even try processing.
			continue
		}
		subTest(t, cutExt(file.Name()), func(t U) {
			Walk(t, filepath.Join(path, file.Name()), f)
		})
	}
}

// cutExt returns the given file name with the extension removed, if there is
// one.
func cutExt(fileName string) string {
	extStart := len(fileName) - len(filepath.Ext(fileName))
	return fileName[:extStart]
}

// Ignore files named .XXXX, XXX~ or #XXX#.
var tempFileRe = regexp.MustCompile(`(^\..*)|(.*~$)|(^#.*#$)`)

// TestData contains information about one data-driven test case that was
// parsed from the test file.
type TestData struct {
	// Pos is the position of the directive in the test file. Messages reported
	// through T are annotated with it (except in InputLines and InputDirectives
	// loops); changing it has no effect on them.
	Pos Pos

	// Cmd is the command (the first word on the directive line).
	Cmd string

	// Flags contains the flags on the directive line, in order (see ParseLine).
	// They are usually retrieved by name (with HasFlag, BoolFlag, FlagValue,
	// FlagValueOr, or FlagValues for a flag that can be repeated). All the flags
	// on the directive line must be processed by the test (see
	// MarkFlagProcessed).
	Flags []Flag

	// Args contains the positional arguments on the directive line (the words
	// that are not flags). PopArg and PopArgs remove arguments from the front;
	// SingleArg, TwoArgs and ScanArgs retrieve all the arguments of a directive
	// which takes a fixed number of them.
	Args []string

	// Input is the text between the directive line and the ---- separator, with
	// leading and trailing whitespace removed.
	Input string

	// Expected is the value below the ---- separator. In most cases,
	// tests need not check this, and instead return their own actual
	// output.
	// This field is provided so that a test can perform an early return
	// with "return d.Expected" to signal that nothing has changed.
	Expected string

	// Rewrite is set if the test file is being rewritten with the actual outputs
	// (i.e. the test is run with the -rewrite flag; see Rewrite), in which case
	// the output of the directive is not compared with Expected. It is never set
	// by RunTestFromString.
	Rewrite bool

	// T is the testing.TB for the directive; it is used by the TestData methods
	// to report errors. When running via RunTest or RunTestFromString, it is the
	// wrapper passed to the callback, which annotates errors, skips and logs with
	// the position of the directive (see TBWrapper).
	//
	// When a TestData is constructed manually, T must be set before using
	// BoolFlag, FlagValue, FlagValueOr, FlagValues, PopArg, PopArgs, ScanArgs,
	// SingleArg, TwoArgs, InputDirectives, Retry, RetryFor or Fatalf.
	T testing.TB

	// directive is the original directive line (with any continuation lines
	// joined). Unlike FullCmd, it is not affected by changes to Flags and Args;
	// it is used when printing errors.
	directive string

	// processed contains the names of the flags that were processed (see
	// MarkFlagProcessed).
	processed *processedFlags

	// inputPos is the position of the first line of Input in the test file (zero
	// if the TestData was not read from a file, or Input is empty).
	inputPos Pos

	// curPos is the position used by the wrapper to annotate messages (nil if
	// the TestData was not created by RunTest or RunTestFromString).
	curPos *directivePos
}

// Pos is a position in a test file.
type Pos struct {
	// File is the path of the test file (as passed to RunTest), or "<string>"
	// for RunTestFromString.
	File string
	// Line is the 1-based line number.
	Line int
}

// String returns "file:line" (or "" for the zero Pos).
func (p Pos) String() string {
	if p == (Pos{}) {
		return ""
	}
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// InputLines returns an iterator over the lines of Input, along with their
// positions in the test file. While the body of the loop runs, messages
// reported through T (including by TestData methods like Fatalf) are annotated
// with the position of the current line, instead of that of the directive. For
// example:
//
//	for _, line := range td.InputLines() {
//		key, value, ok := strings.Cut(line, "=")
//		if !ok {
//			td.Fatalf("invalid line %q", line) // Reports the position of the line.
//		}
//		...
//	}
//
// An empty Input has no lines. If the TestData was not read from a test file,
// the positions are zero and messages are not affected. The annotations are
// only reliable if the loop runs in the callback's goroutine.
//
// See InputDirectives for inputs in which each line is a command with flags
// and arguments.
func (td *TestData) InputLines() iter.Seq2[Pos, string] {
	return func(yield func(Pos, string) bool) {
		if td.Input == "" {
			return
		}
		for i, line := range strings.Split(td.Input, "\n") {
			pos := td.inputLinePos(i)
			if !td.withPos(pos, func() bool { return yield(pos, line) }) {
				return
			}
		}
	}
}

// InputDirectives parses each line of Input like a directive line (see
// ParseLine) and returns an iterator over the resulting directives. This is
// used for directives whose input is a sequence of commands; for example, for
//
//	batch
//	set a -value=1
//	del b -sync
//	----
//
// the test could run:
//
//	for cmd := range td.InputDirectives() {
//		switch cmd.Cmd {
//		case "set":
//			b.Set(cmd.PopArg(), cmd.FlagValue[string]("value"))
//		case "del":
//			b.Delete(cmd.PopArg(), cmd.BoolFlag("sync"))
//		default:
//			cmd.Fatalf("unknown command %q", cmd.Cmd)
//		}
//	}
//
// Each directive is a TestData with Pos (the position of the line), Cmd, Flags
// and Args set; it has no Input, and it shares T and Rewrite with td. Like in
// a test file, blank lines and lines that start with # are skipped, and a line
// that ends with a backslash continues on the next line.
//
// While the body of the loop runs, messages reported through T are annotated
// with the position of the current line, like for InputLines. It is a fatal
// error if a line can't be parsed, or if an iteration of the loop finishes
// without processing all the flags on the line (see MarkFlagProcessed), like
// for the flags on the directive line.
func (td *TestData) InputDirectives() iter.Seq[*TestData] {
	return func(yield func(*TestData) bool) {
		t := td.tb()
		t.Helper()
		lines := strings.Split(td.Input, "\n")
		for i := 0; i < len(lines); i++ {
			pos := td.inputLinePos(i)
			line := strings.TrimSpace(lines[i])
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			for strings.HasSuffix(line, `\`) && i+1 < len(lines) {
				i++
				line = strings.TrimSuffix(line, `\`) + " " + strings.TrimSpace(lines[i])
			}
			proceed := td.withPos(pos, func() bool {
				t.Helper()
				cmd, flags, args, err := ParseLine(line)
				if err != nil {
					t.Fatalf("%v", err)
				}
				if cmd == "" {
					// A backslash followed by a blank line.
					return true
				}
				// Like for the directive line, the check for unprocessed flags is not
				// affected by changes that the loop body makes to the TestData.
				processed := &processedFlags{}
				proceed := yield(&TestData{
					Pos:       pos,
					Cmd:       cmd,
					Flags:     slices.Clone(flags),
					Args:      args,
					Rewrite:   td.Rewrite,
					T:         td.T,
					directive: line,
					processed: processed,
				})
				if unprocessed := processed.unprocessed(flags); len(unprocessed) > 0 && !t.Failed() {
					t.Fatalf("unprocessed flags: %s", strings.Join(unprocessed, " "))
				}
				return proceed
			})
			if !proceed {
				return
			}
		}
	}
}

// inputLinePos returns the position of the i-th line of Input (or the zero Pos
// if the TestData was not read from a test file).
func (td *TestData) inputLinePos(i int) Pos {
	if td.inputPos.Line == 0 {
		return Pos{}
	}
	return Pos{File: td.inputPos.File, Line: td.inputPos.Line + i}
}

// withPos runs f while messages reported through T are annotated with pos.
func (td *TestData) withPos(pos Pos, f func() bool) bool {
	if td.T != nil {
		td.T.Helper()
	}
	if td.curPos == nil || pos == (Pos{}) {
		return f()
	}
	// The position is restored even if f panics or calls runtime.Goexit.
	defer td.curPos.push(pos.String())()
	return f()
}

// String renders the entire testcase. The string ends in a newline.
func (td *TestData) String() string {
	return td.render(td.FullCmd())
}

// directiveLine returns the original directive line, or FullCmd if it is not
// set (e.g. for a TestData constructed manually).
func (td *TestData) directiveLine() string {
	if td.directive != "" {
		return td.directive
	}
	return td.FullCmd()
}

// origString is like String, but uses the original directive line.
func (td *TestData) origString() string {
	return td.render(td.directiveLine())
}

func (td *TestData) render(directive string) string {
	if td.Input == "" {
		return directive + "\n"
	}
	return fmt.Sprintf("%s\n%s\n", directive, td.Input)
}

// Retry is used for tests that depend on background goroutines to finish work.
// It takes a function that produces the output of the testcase and calls it
// repeatedly until it matches the expected output (for at most 1 second).
//
// Returns the last value returned by f (which can be directly returned from the
// function passed to RunTest).
//
// If the test file is being rewritten (td.Rewrite is set), it just sleeps for
// 100ms.
func (td *TestData) Retry(f func() string) string {
	td.tb().Helper()
	return td.RetryFor(time.Second, f)
}

// RetryFor is like Retry but with a custom timeout.
func (td *TestData) RetryFor(d time.Duration, f func() string) string {
	td.tb().Helper()
	if td.Rewrite {
		// For rewrite mode, we have nothing to compare the output to. Just sleep a
		// reasonable amount, under the assumption that --rewrite won't be used
		// under stress or a loaded system.
		time.Sleep(d / 10)
		return f()
	}
	runtime.Gosched()
	// We are going to evaluate f until it produces the correct answer numStable
	// times in a row.
	const numAttempts = 100
	const numStable = 3
	// numOk is the number of consecutive calls of f() that have returned the
	// correct answer.
	numOk := 0
	expected := strings.TrimSpace(td.Expected)
	for i := 0; ; i++ {
		s := f()
		if strings.TrimSpace(s) == expected {
			numOk++
		} else {
			numOk = 0
		}
		if numOk == numStable || i == numAttempts {
			if i >= numStable {
				td.tb().Logf("retried for %s (%d times)", time.Duration(i-numStable+1)*d/numAttempts, i-numStable+1)
			}
			return s
		}
		time.Sleep(d/numAttempts + 1)
	}
}

// Fatalf is a shorthand for td.T.Fatalf, which adds file position information,
// so that it's easy to locate the source of the error.
func (td *TestData) Fatalf(format string, args ...any) {
	t := td.tb()
	t.Helper()
	t.Fatalf(format, args...)
}

// tb returns td.T, or panics with an explanation if it is not set.
func (td *TestData) tb() testing.TB {
	if td.T == nil {
		panic("datadriven: TestData.T is not set (it must be set for TestData values constructed manually)")
	}
	return td.T
}

// needsDoubleSeparators returns true if an output must be written to the test
// file between double ---- separators (see the package documentation), and
// false if it can be written after a single separator. It returns an error if
// the output can't be written in either form (i.e. such that it is read back
// unchanged).
func needsDoubleSeparators(output string) (bool, error) {
	if output == "" {
		return false, nil
	}
	lines := strings.Split(strings.TrimSuffix(output, "\n"), "\n")
	// A carriage return at the end of a line is removed when the file is read
	// (as part of the end-of-line marker).
	for i, line := range lines {
		if strings.HasSuffix(line, "\r") {
			return false, fmt.Errorf("line %d of the output ends with a carriage return", i+1)
		}
	}
	// After a single separator, the output ends at the first blank line, and a
	// ---- line right after the separator makes it a double separator.
	isBlank := func(line string) bool { return strings.TrimSpace(line) == "" }
	if lines[0] != "----" && !slices.ContainsFunc(lines, isBlank) {
		return false, nil
	}
	// Between double separators, the output ends at the first two consecutive
	// ---- lines.
	for i, line := range lines {
		if line == "----" && (i == len(lines)-1 || lines[i+1] == "----") {
			return false, fmt.Errorf(
				"an output with blank lines (or which starts with a ---- line) can't " +
					"end with a ---- line or contain two consecutive ---- lines",
			)
		}
	}
	return true, nil
}

func indentLines(str string) string {
	var b strings.Builder
	if str == "" {
		return ""
	}
	for i, l := range strings.Split(str, "\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		if l != "" {
			b.WriteString("  ")
			b.WriteString(l)
		}
	}
	return b.String()
}

// parseArgVal parses s and stores it into dest, which must have type int*,
// uint*, float*, string, bool, or a named type with one of those underlying
// kinds.
//
// Integers are parsed in base 10. A string is stored as it is (it can have
// leading or trailing whitespace if the value is quoted).
func parseArgVal(s string, dest reflect.Value) error {
	t := dest.Type()

	if t.Kind() == reflect.String {
		dest.SetString(s)
		return nil
	}
	s = strings.TrimSpace(s)
	switch t.Kind() {
	case reflect.Bool:
		b, err := strconv.ParseBool(s)
		if err != nil {
			return err
		}
		dest.SetBool(b)

	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(s, 10, int(t.Bits()))
		if err != nil {
			return fmt.Errorf("parse %q as %s: %w", s, t, err)
		}
		dest.SetInt(n)

	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(s, 10, int(t.Bits()))
		if err != nil {
			return fmt.Errorf("parse %q as %s: %w", s, t, err)
		}
		dest.SetUint(n)

	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(s, int(t.Bits()))
		if err != nil {
			return fmt.Errorf("parse %q as %s: %w", s, t, err)
		}
		dest.SetFloat(f)

	default:
		return fmt.Errorf("unsupported destination type %s", t)
	}

	return nil
}
