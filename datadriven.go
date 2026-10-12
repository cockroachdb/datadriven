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
	"io/ioutil"
	"iter"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
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

// RunTest invokes a data-driven test. The test cases are contained in a
// separate test file and are dynamically loaded, parsed, and executed by this
// testing framework. By convention, test files are typically located in a
// sub-directory called "testdata". Each test file has the following format:
//
//	<command>[,<command>...] [arg | arg=val | arg=(val1, val2, ...)]...
//	<input to the command>
//	----
//	<expected results>
//
// The command input can contain blank lines. However, by default, the expected
// results cannot contain blank lines. This alternate syntax allows the use of
// blank lines:
//
//	<command>[,<command>...] [arg | arg=val | arg=(val1, val2, ...)]...
//	<input to the command>
//	----
//	----
//	<expected results>
//
//	<more expected results>
//	----
//	----
//
// To execute data-driven tests, pass the path of the test file as well as a
// function which can interpret and execute whatever commands are present in
// the test file. The framework invokes the function, passing it information
// about the test case in a TestData struct.
//
// The function must returns the actual results of the case, which
// RunTest() compares with the expected results. If the two are not
// equal, the test is marked to fail.
//
// RunTest works over any testing.TB, typically a *testing.T or a *testing.B.
// The function is passed a *TBWrapper which wraps it (a *T for a *testing.T, a
// *B for a *testing.B) and annotates errors, skips and logs with the position
// of the current directive in the test file; the same wrapper is available as
// td.T.
//
// "subtest" directives are not supported when running over a *testing.B
// (testing.B.Run runs the function of a sub-benchmark multiple times, but the
// directives can only be read once).
//
// Note that RunTest() creates a sub-instance of testing.T for each
// "subtest" directive in the input file. It is thus unsafe/invalid to call
// e.g. Fatal() or Skip() on the parent testing.T from inside the
// callback function. Use the provided wrapper instead.
//
// It is possible for a test to test for an "expected error" as follows:
//   - run the code to test
//   - if an error occurs, report the detail of the error as actual
//     output.
//   - place the expected error details in the expected results
//     in the input file.
//
// It is also possible for a test to report an _unexpected_ test
// error by calling t.Error().
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
// directly.
func RunTestFromString[U testing.TB](
	t U, input string, f func(t *TBWrapper[U], td *TestData) string,
) {
	t.Helper()
	runTestInternal(t, "<string>" /* sourceName */, strings.NewReader(input), f, rewriteFlag())
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

	// The name passed to t.Run is the last component in the subtest
	// name, because all components before that are already prefixed by
	// t.Run from the names of the parent sub-tests.
	testingSubTestName := subTestName[strings.LastIndex(subTestName, "/")+1:]

	// Begin the sub-test.
	subTest(t, testingSubTestName, func(t U) {
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

	if seenSubTestEnd && len(r.data.CmdArgs) == 2 && r.data.CmdArgs[1].Key != subTestName {
		// If a subtest name was provided after "subtest end", ensure that it matches.
		r.fatalf(t,
			"mismatched subtest end directive: expected %q, got %q", r.data.CmdArgs[1].Key, subTestName)
	}

	if !seenSubTestEnd && !t.Failed() {
		// We only report missing "subtest end" if there was no error otherwise;
		// for if there was an error, the reading would have stopped.
		r.fatalf(t,
			"EOF encountered without subtest end directive\n%s: subtest started here", subTestStartPos)
	}

}

func isSubTestStart(t testing.TB, r *testDataReader, mandatorySubTestPrefix string) (string, bool) {
	if r.data.Cmd != "subtest" {
		return "", false
	}
	if _, ok := innermost(t).(*testing.B); ok {
		r.fatalf(t, "subtest directives are not supported in benchmarks")
	}
	if len(r.data.CmdArgs) != 1 {
		r.fatalf(t, "invalid syntax for subtest")
	}
	subTestName := r.data.CmdArgs[0].Key
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
	if len(r.data.CmdArgs) == 0 || r.data.CmdArgs[0].Key != "end" {
		return false
	}
	if len(r.data.CmdArgs) > 2 {
		r.fatalf(t, "invalid syntax for subtest end")
	}
	return true
}

// runDirective runs just one directive in the input.
//
// The stopNow and subTestSkipped booleans are modified by-reference
// instead of returned because the testing module implements t.Skip
// and t.Fatal using panics, and we're not guaranteed to get back to
// the caller via a return in those cases.
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
	// The framework messages use a copy of the directive, which is not affected
	// by changes the callback makes to d.
	orig := r.data
	actual := func() string {
		fReturned := false
		defer func() {
			if r := recover(); r != nil {
				raw.Logf("\npanic during %s:\n%s\n", orig.Pos, orig.Input)
				panic(r)
			} else if !fReturned {
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

	// The test has not failed, we can analyze the expected
	// output.
	if r.rewrite != nil {
		r.emit("----")
		if hasBlankLine(actual) {
			r.emit("----")
			r.rewrite.WriteString(actual)
			r.emit("----")
			r.emit("----")
			r.emit("")
		} else {
			// Here actual already ends in \n so emit adds a blank line.
			r.emit(actual)
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
				raw.Fatalf("\n%s:\n%soutput didn't match expected:\n%s", indentLines(orig.Pos.String()), indentLines(orig.origString()), indentLines(diff))
				return
			}
			raw.Logf("Failed to produce diff %v", err)
		}
		raw.Fatalf("\n%s:\n%s\nexpected:\n%s\nfound:\n%s", orig.Pos, indentLines(orig.origString()), indentLines(orig.Expected), indentLines(actual))
	} else if Verbose() {
		raw.Logf("\n%s:\n%s  ----\n%s", orig.Pos, indentLines(orig.origString()), indentLines(actual))
	}
	return
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
	files, err := ioutil.ReadDir(path)
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
	// through T are annotated with it (except in InputLines loops); changing it
	// has no effect on them.
	Pos Pos

	// Cmd is the first string on the directive line (up to the first whitespace).
	Cmd string

	// CmdArgs contains the k/v arguments to the command. PopArg removes
	// arguments from the front.
	CmdArgs []CmdArg

	// Input is the text between the first directive line and the ---- separator.
	Input string

	// Expected is the value below the ---- separator. In most cases,
	// tests need not check this, and instead return their own actual
	// output.
	// This field is provided so that a test can perform an early return
	// with "return d.Expected" to signal that nothing has changed.
	Expected string

	// Rewrite is set if the test is being run with the -rewrite flag.
	Rewrite bool

	// T is the testing.TB for the directive; it is used by the TestData methods
	// to report errors. When running via RunTest or RunTestFromString, it is the
	// wrapper passed to the callback, which annotates errors, skips and logs with
	// the position of the directive (see TBWrapper).
	//
	// When a TestData is constructed manually, T must be set before using
	// ScanArgs, MaybeScanArgs, PopArg, Retry, RetryFor, Logf, Fatalf or
	// the ScanArg functions.
	T testing.TB

	// directive is the original directive line (with any continuation lines
	// joined). Unlike FullCmd, it is not affected by changes to CmdArgs; it is
	// used when printing errors.
	directive string

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
// reported through T (including by TestData methods like ScanArgs or Fatalf)
// are annotated with the position of the current line, instead of that of the
// directive. This is useful for directives whose input contains a sequence of
// commands; for example:
//
//	for _, line := range td.InputLines() {
//		cmd, args, err := datadriven.ParseLine(line)
//		if err != nil {
//			td.Fatalf("%v", err) // Reports the position of the line.
//		}
//		...
//	}
//
// An empty Input has no lines. If the TestData was not read from a test file,
// the positions are zero and messages are not affected. The annotations are
// only reliable if the loop runs in the callback's goroutine.
func (td *TestData) InputLines() iter.Seq2[Pos, string] {
	return func(yield func(Pos, string) bool) {
		if td.Input == "" {
			return
		}
		for i, line := range strings.Split(td.Input, "\n") {
			var pos Pos
			if td.inputPos.Line != 0 {
				pos = Pos{File: td.inputPos.File, Line: td.inputPos.Line + i}
			}
			if !td.withPos(pos, func() bool { return yield(pos, line) }) {
				return
			}
		}
	}
}

// withPos runs f while messages reported through T are annotated with pos.
func (td *TestData) withPos(pos Pos, f func() bool) bool {
	if td.curPos == nil || pos == (Pos{}) {
		return f()
	}
	// The position is restored even if f panics or calls runtime.Goexit.
	defer td.curPos.push(pos.String())()
	return f()
}

// FullCmd renders the command and the arguments (which reflect any changes to
// CmdArgs, e.g. by PopArg).
func (td *TestData) FullCmd() string {
	fields := make([]string, 0, len(td.CmdArgs)+1)
	fields = append(fields, td.Cmd)
	for i := range td.CmdArgs {
		fields = append(fields, td.CmdArgs[i].String())
	}
	return strings.Join(fields, " ")
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

// HasArg checks whether the CmdArgs array contains an entry for the given key.
func (td *TestData) HasArg(key string) bool {
	_, ok := td.Arg(key)
	return ok
}

// Arg retrieves the first CmdArg matching the given key. The second return
// value indicates whether such an argument exists.
func (td *TestData) Arg(key string) (arg CmdArg, ok bool) {
	for i := range td.CmdArgs {
		if td.CmdArgs[i].Key == key {
			return td.CmdArgs[i], true
		}
	}
	return arg, false
}

// MaybeScanArgs behaves identically to ScanArgs, except that if the arg does
// not exist it leaves the destinations unmodified and returns false. In all
// other cases it returns true.
func (td *TestData) MaybeScanArgs(key string, dests ...interface{}) bool {
	t := td.tb()
	t.Helper()
	if arg, ok := td.Arg(key); ok {
		arg.scan(t, dests...)
		return true
	}
	return false
}

// ScanArgs looks up the first CmdArg matching the given key and scans it into
// the given destinations in order. If the arg does not exist, the number of
// destinations does not match that of the arguments, or a destination can not
// be populated from its matching value, a fatal error results. If the arg
// exists multiple times, the first occurrence is parsed. For example, for a
// TestData originating from
//
//	cmd arg1=50 arg2=yoruba arg3=(50, 50, 50) arg4
//
// the following would be valid:
//
//	var i1, i2, i3, i4 int
//	var s string
//	var l []int
//	var b bool
//	td.ScanArgs("arg1", &i1)
//	td.ScanArgs("arg2", &s)
//	td.ScanArgs("arg3", &i2, &i3, &i4)
//	td.ScanArgs("arg3", &l)
//	td.ScanArgs("arg4", &b) // true
//
// The destinations must be pointers to one of: string, bool, integer and
// floating point types, named types with one of these as the underlying type,
// time.Duration (but not named types defined from it, which are parsed as
// integers), or slices of these (which receive all the values). Integers are
// parsed in base 10. An argument without values (e.g. arg4 above) can be
// scanned into a bool, as true.
//
// See also ScanArg, ScanArgOr and MaybeScanArg.
func (td *TestData) ScanArgs(key string, dests ...interface{}) {
	t := td.tb()
	t.Helper()
	arg, ok := td.Arg(key)
	if !ok {
		t.Fatalf("missing argument: %s", key)
	}
	arg.scan(t, dests...)
}

// PopArg removes the first argument from CmdArgs and returns it, rendered in
// the directive line syntax (see CmdArg.String; e.g. "c=(1,2)" is returned as
// "c=(1, 2)"). It is useful for directives with positional arguments; for
// example, for
//
//	cmd foo bar n=5
//
// the following would be valid:
//
//	a := td.PopArg() // "foo"
//	b := td.PopArg() // "bar"
//	td.ScanArgs("n", &n)
//
// It is a fatal error if there are no arguments left.
func (td *TestData) PopArg() string {
	t := td.tb()
	t.Helper()
	if len(td.CmdArgs) == 0 {
		t.Fatalf("PopArg: no arguments left (directive: %s)", td.directiveLine())
	}
	arg := td.CmdArgs[0]
	td.CmdArgs = td.CmdArgs[1:]
	return arg.String()
}

// Retry is used for tests that depend on background goroutines to finish work.
// It takes a function that produces the output of the testcase and calls it
// repeatedly until it matches the expected output (for at most 1 second).
//
// Returns the last value returned by f (which can be directly returned from the
// function passed to RunTest).
//
// If --rewrite is used, just sleeps for 100ms.
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
				td.Logf("retried for %s (%d times)", time.Duration(i-numStable+1)*d/numAttempts, i-numStable+1)
			}
			return s
		}
		time.Sleep(d/numAttempts + 1)
	}
}

// CmdArg contains information about an argument on the directive line. An
// argument is specified in one of the following forms:
//   - argument
//   - argument=value
//   - argument=(values, ...)
//
// The methods which take a testing.TB report errors through it; pass TestData.T
// so that the errors are annotated with the directive position.
type CmdArg struct {
	Key  string
	Vals []string
}

func (arg CmdArg) String() string {
	switch len(arg.Vals) {
	case 0:
		return arg.Key

	case 1:
		return fmt.Sprintf("%s=%s", arg.Key, arg.Vals[0])

	default:
		return fmt.Sprintf("%s=(%s)", arg.Key, strings.Join(arg.Vals, ", "))
	}
}

// FirstVal asserts that arg has at least one value and returns the first one.
func (arg CmdArg) FirstVal(t testing.TB) string {
	t.Helper()
	arg.ExpectNumValsGE(t, 1)
	return arg.Vals[0]
}

// SingleVal asserts that arg has exactly one value and returns it.
func (arg CmdArg) SingleVal(t testing.TB) string {
	t.Helper()
	arg.ExpectNumVals(t, 1)
	return arg.Vals[0]
}

// TwoVals asserts that arg has exactly two values and returns them.
func (arg CmdArg) TwoVals(t testing.TB) (string, string) {
	t.Helper()
	arg.ExpectNumVals(t, 2)
	return arg.Vals[0], arg.Vals[1]
}

// ExpectNumVals asserts that arg has exactly num values.
func (arg CmdArg) ExpectNumVals(t testing.TB, num int) {
	t.Helper()
	if len(arg.Vals) != num {
		t.Fatalf("argument %q requires %d values, has %d", arg.Key, num, len(arg.Vals))
	}
}

// ExpectNumValsGE asserts that arg has at least minNum values.
func (arg CmdArg) ExpectNumValsGE(t testing.TB, minNum int) {
	t.Helper()
	if len(arg.Vals) < minNum {
		t.Fatalf("argument %q requires at least %d values, has %d", arg.Key, minNum, len(arg.Vals))
	}
}

// Scan attempts to parse the value at index i into the dest.
func (arg CmdArg) Scan(t testing.TB, i int, dest interface{}) {
	t.Helper()
	if err := arg.scanScalarErr(i, dest); err != nil {
		t.Fatal(err)
	}
}

func (arg CmdArg) scan(t testing.TB, dests ...interface{}) {
	t.Helper()
	// If only one destination is provided, use scanAllErr which supports
	// scanning multiple values into a slice destination type.
	if len(dests) == 1 {
		if err := arg.scanAllErr(dests[0]); err != nil {
			t.Fatalf("argument %q: %v", arg.Key, err)
		}
		return
	}

	// Multiple destinations provided; update each corresponding destination to
	// support invocations of the form:
	//
	//   td.ScanArgs("arg3", &i2, &i3, &i4)
	//
	if len(dests) != len(arg.Vals) {
		t.Fatalf("argument %q: %d destinations, but %d values", arg.Key, len(dests), len(arg.Vals))
	}

	for i := range dests {
		if err := arg.scanScalarErr(i, dests[i]); err != nil {
			t.Fatalf("argument %q: value %d: %v", arg.Key, i, err)
		}
	}
}

// scanAllErr scans the argument into dest, which is a pointer to either a
// slice (which receives all the values) or a scalar (which receives the single
// value; a bool also accepts no values, as true).
func (arg CmdArg) scanAllErr(dest interface{}) error {
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("destination must be a non-nil pointer, got %T", dest)
	}

	if sliceV := rv.Elem(); sliceV.Kind() == reflect.Slice {
		slice := reflect.MakeSlice(sliceV.Type(), len(arg.Vals), len(arg.Vals))
		for i := range arg.Vals {
			if err := arg.scanScalarErr(i, slice.Index(i).Addr().Interface()); err != nil {
				return fmt.Errorf("value %d: %w", i, err)
			}
		}
		sliceV.Set(slice)
		return nil
	}

	switch len(arg.Vals) {
	case 0:
		if rv.Elem().Kind() == reflect.Bool {
			// An argument without values (e.g. "cmd verbose") is a true bool.
			rv.Elem().SetBool(true)
			return nil
		}
		return fmt.Errorf("no value")
	case 1:
		return arg.scanScalarErr(0, dest)
	default:
		return fmt.Errorf("%d values; scan into a slice, or pass %d destinations", len(arg.Vals), len(arg.Vals))
	}
}

// scanScalarErr is like Scan but returns an error rather than taking a testing.T to fatal.
func (arg CmdArg) scanScalarErr(i int, dest interface{}) error {
	if i < 0 || i >= len(arg.Vals) {
		return fmt.Errorf("cannot scan index %d of key %s", i, arg.Key)
	}
	val := arg.Vals[i]

	// Special cases.
	switch dest := dest.(type) {
	case *time.Duration:
		t, err := time.ParseDuration(val)
		if err != nil {
			return err
		}
		*dest = t
		return nil
	}

	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return fmt.Errorf("out must be a non-nil pointer")
	}
	return parseArgVal(val, rv.Elem())
}

// Logf is a shorthand for td.T.Logf, which adds file position information, so
// that it's easy to locate the source of the log.
func (td TestData) Logf(format string, args ...interface{}) {
	t := td.tb()
	t.Helper()
	t.Logf(format, args...)
}

// Fatalf is a shorthand for td.T.Fatalf, which adds file position information,
// so that it's easy to locate the source of the error.
func (td TestData) Fatalf(format string, args ...interface{}) {
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

// hasBlankLine returns true iff `s` contains at least one line that's
// empty or contains only whitespace.
func hasBlankLine(s string) bool {
	return blankLineRe.MatchString(s)
}

// blankLineRe matches lines that contain only whitespaces (or
// entirely empty/blank lines).  We use the "m" flag for "multiline"
// mode so that "^" can match the beginning of individual lines inside
// the input, not just the beginning of the input.  In multiline mode,
// "$" also matches the end of lines. However, note how the regexp
// uses "\n" to match the end of lines instead of "$". This is
// because of an oddity in the Go regexp engine: at the very end of
// the input, *after the final \n in the input*, Go estimates there is
// still one more line containing no characters but that matches the
// "^.*$" regexp. The result of this oddity is that an input text like
// "foo\n" will match as "foo\n" (no match) + "" (yes match). We don't
// want that final match to be included, so we force the end-of-line
// match using "\n" specifically.
var blankLineRe = regexp.MustCompile(`(?m)^[\t ]*\n`)

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
// Integers are parsed in base 10.
func parseArgVal(s string, dest reflect.Value) error {
	t := dest.Type()

	s = strings.TrimSpace(s)
	switch t.Kind() {
	case reflect.String:
		dest.SetString(s)

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
