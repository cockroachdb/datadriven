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
	"fmt"
	"os"
	"os/exec"
	"reflect"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
)

func TestT(t *testing.T) {
	outer := t
	RunTestFromString(t, `
cmd
----
`, func(t *T, d *TestData) string {
		if d.T != t {
			t.Fatalf("TestData.T is not the wrapper passed to the callback")
		}
		if t.TB != outer {
			t.Fatalf("TB is not the underlying *testing.T")
		}
		// A wrapper created with Wrap has no position; it must not crash.
		if w := Wrap(t.TB); w.TB != outer {
			t.Fatalf("Wrap: TB is not the underlying *testing.T")
		} else {
			w.Logf("no position")
		}
		return ""
	})
}

// TestB verifies that RunTestFromString and Walk work over a *testing.B.
func TestB(t *testing.T) {
	var ran []string
	var walked []string
	failed := false
	testing.Benchmark(func(b *testing.B) {
		defer func() { failed = failed || b.Failed() }()
		ran = ran[:0]
		RunTestFromString(b, `
cmd
----
ok

cmd
----
ok
`, func(b *B, d *TestData) string {
			b.TB.ReportMetric(1, "things")
			ran = append(ran, d.Pos.String())
			return "ok"
		})
		// Walk creates sub-benchmarks for the files in a directory; it is
		// supported (unlike subtest directives) because each invocation of a
		// sub-benchmark function walks the file again.
		walked = walked[:0]
		Walk(b, "testdata/walk", func(b *testing.B, path string) {
			walked = append(walked, path)
		})
	})
	if failed {
		t.Fatalf("benchmark failed")
	}
	if expected := []string{"<string>:2", "<string>:6"}; !reflect.DeepEqual(ran, expected) {
		t.Fatalf("expected directives %v to run, got %v", expected, ran)
	}
	if len(walked) == 0 {
		t.Fatalf("no files walked")
	}
}

// subtestInBenchmarkChildEnv is set when TestSubtestInBenchmark runs
// BenchmarkSubtestInBenchmark in a child process; the value is "plain" or
// "wrapped" (to run over a wrapper of the *testing.B).
const subtestInBenchmarkChildEnv = "DATADRIVEN_TEST_SUBTEST_IN_BENCHMARK_CHILD"

func BenchmarkSubtestInBenchmark(b *testing.B) {
	const input = `
subtest foo

cmd
----
ok

subtest end
`
	switch os.Getenv(subtestInBenchmarkChildEnv) {
	case "plain":
		RunTestFromString(b, input, func(b *B, d *TestData) string {
			b.Fatalf("directive in subtest ran")
			return ""
		})
	case "wrapped":
		RunTestFromString(Wrap(b), input, func(b *TBWrapper[*B], d *TestData) string {
			b.Fatalf("directive in subtest ran")
			return ""
		})
	default:
		b.Skip("only run by TestSubtestInBenchmark")
	}
}

// TestSubtestInBenchmark verifies that subtest directives fail when running
// over a *testing.B (the failure message can't be intercepted in-process).
func TestSubtestInBenchmark(t *testing.T) {
	for _, mode := range []string{"plain", "wrapped"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0],
				"-test.run=^$", "-test.bench=^BenchmarkSubtestInBenchmark$", "-test.benchtime=1x")
			cmd.Env = append(os.Environ(), subtestInBenchmarkChildEnv+"="+mode)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("expected the benchmark to fail; output:\n%s", out)
			}
			if !strings.Contains(string(out), "<string>:2: subtest directives are not supported in benchmarks") {
				t.Fatalf("unexpected output:\n%s", out)
			}
			if strings.Contains(string(out), "directive in subtest ran") {
				t.Fatalf("directive in subtest ran; output:\n%s", out)
			}
		})
	}
}

// embeddedT embeds a wrapper; it must not be mistaken for one.
type embeddedT struct {
	*T
}

func TestEmbeddedWrapper(t *testing.T) {
	var tb testing.TB = &embeddedT{T: Wrap(t)}
	if _, ok := asWrapper(tb); ok {
		t.Fatalf("embeddedT considered a wrapper")
	}
	if innermost(tb) != tb || unwrap(tb) != tb {
		t.Fatalf("embeddedT was unwrapped")
	}
}

// TestNested verifies that the wrappers passed to the callbacks can be passed
// back to RunTestFromString and Walk, which create subtests.
func TestNested(t *testing.T) {
	const inner = `
subtest foo

cmd
----
ok

subtest end
`
	innerHandler := func(t *TBWrapper[testing.TB], d *TestData) string {
		if _, ok := t.TB.(wrapper); ok {
			t.Fatalf("nested test runs over a wrapper")
		}
		return "ok"
	}
	RunTestFromString(t, `
nested
----

nested-td
----

walk
----

walk-td
----
`, func(t *T, d *TestData) string {
		var paths []string
		switch d.Cmd {
		case "nested":
			// The wrapper is passed as a testing.TB, so it gets unwrapped.
			RunTestFromString[testing.TB](t, inner, innerHandler)
			return ""
		case "nested-td":
			RunTestFromString(d.T, inner, innerHandler)
			return ""
		case "walk":
			// Walking over the wrapper passes wrappers (for the subtests, with the
			// same position) to the function.
			Walk(t, "testdata/walk", func(w *T, path string) {
				if w.TB == t.TB {
					t.Errorf("not in a subtest")
				}
				if msg := w.pos.prefix("x"); msg != d.Pos.String()+": x" {
					t.Errorf("unexpected position: %s", msg)
				}
				paths = append(paths, path)
			})
		case "walk-td":
			Walk(d.T, "testdata/walk", func(t testing.TB, path string) {
				paths = append(paths, path)
			})
		}
		if len(paths) == 0 {
			t.Fatalf("no files walked")
		}
		return ""
	})
}

// TestAnnotations verifies that messages reported through the wrapper passed to
// the callback (and through TestData methods) are annotated with the position of
// the current directive.
func TestAnnotations(t *testing.T) {
	var retained testing.TB
	var handler func(t testing.TB, d *TestData) string
	handler = func(t testing.TB, d *TestData) string {
		if d.T != t {
			panic("TestData.T is not the testing.TB passed to the callback")
		}
		switch d.Cmd {
		case "ok":
		case "retain":
			retained = t
		case "use-retained":
			retained.Errorf("retained %d", 1)
		case "log":
			t.Log("log", 1)
		case "logf":
			t.Logf("logf %d", 1)
		case "logf-nl":
			t.Logf("logf-nl %d\n", 1)
		case "error":
			t.Error("error", 1)
		case "errorf":
			t.Errorf("errorf %d", 1)
		case "errorf-nl":
			t.Errorf("errorf-nl %d\n", 1)
		case "fatal":
			t.Fatal("fatal", 1)
		case "fatalf":
			t.Fatalf("fatalf %d", 1)
		case "skip":
			t.Skip("skip", 1)
		case "skipf":
			t.Skipf("skipf %d", 1)
		case "td-fatalf":
			d.Fatalf("td-fatalf %d", 1)
		case "scan":
			var x int
			d.ScanArgs("x", &x)
		case "single-val":
			d.CmdArgs[0].SingleVal(d.T)
		case "cleanup":
			pos := d.Pos
			t.Cleanup(func() { t.Logf("cleanup from %s", pos) })
		case "td-cleanup":
			// The TestData remains valid after the directive.
			t.Cleanup(func() { d.T.Logf("td cleanup from %s", d.Pos) })
		case "return-x":
			return "x"
		case "nested":
			// Messages from the nested test file are annotated only with its own
			// positions.
			RunTestFromString(t, "\n\nerrorf\n----\n", func(t *TBWrapper[testing.TB], d *TestData) string {
				return handler(t, d)
			})
		case "nested-wrapper", "nested-wrapper-mismatch", "nested-wrapper-parse-error",
			"nested-wrapper-subtest-error":
			// Same, when running over the wrapper itself (as opposed to a
			// testing.TB); this includes framework messages.
			input := map[string]string{
				"nested-wrapper":               "\n\nerrorf\n----\n",
				"nested-wrapper-mismatch":      "\n\nreturn-x\n----\ny\n",
				"nested-wrapper-parse-error":   "\n\nxx =\n----\n",
				"nested-wrapper-subtest-error": "\n\nsubtest\n",
			}[d.Cmd]
			w := t.(*TBWrapper[*recorderTB])
			RunTestFromString(w, input, func(t *TBWrapper[*TBWrapper[*recorderTB]], d *TestData) string {
				return handler(t, d)
			})
		default:
			panic(fmt.Sprintf("unknown command %q", d.Cmd))
		}
		return d.Expected
	}

	testCases := []struct {
		// cmds contains the directives, one per line. Each directive is followed
		// by an empty expected output, so directive i is on line 3*i+1.
		cmds string
		// input, if set, is used as the test file instead of cmds.
		input    string
		expected []string
	}{
		{cmds: "log", expected: []string{"<string>:1: log 1"}},
		{cmds: "logf", expected: []string{"<string>:1: logf 1"}},
		// A trailing newline is handled as by testing.T.Logf/Errorf.
		{cmds: "logf-nl", expected: []string{"<string>:1: logf-nl 1"}},
		{cmds: "errorf-nl", expected: []string{"<string>:1: errorf-nl 1"}},
		{cmds: "error", expected: []string{"<string>:1: error 1"}},
		{cmds: "errorf", expected: []string{"<string>:1: errorf 1"}},
		{cmds: "fatal", expected: []string{"<string>:1: fatal 1"}},
		{cmds: "fatalf", expected: []string{"<string>:1: fatalf 1"}},
		{cmds: "skip", expected: []string{"<string>:1: skip 1"}},
		{cmds: "skipf", expected: []string{"<string>:1: skipf 1"}},
		{cmds: "td-fatalf", expected: []string{"<string>:1: td-fatalf 1"}},
		{cmds: "scan", expected: []string{"<string>:1: missing argument: x"}},
		{
			cmds: "scan x=foo",
			expected: []string{
				`<string>:1: argument "x": parse "foo" as int: strconv.ParseInt: parsing "foo": invalid syntax`,
			},
		},
		{
			cmds:     "single-val a=(1,2)",
			expected: []string{`<string>:1: argument "a" requires 1 values, has 2`},
		},
		{cmds: "ok\nok\nlogf\nerrorf", expected: []string{"<string>:7: logf 1", "<string>:10: errorf 1"}},
		{cmds: "ok\nnested", expected: []string{"<string>:3: errorf 1"}},
		{cmds: "ok\nnested-wrapper", expected: []string{"<string>:3: errorf 1"}},
		// Framework messages (which start with a newline) are ignored by the
		// recorder; they must not be annotated with the outer position.
		{cmds: "ok\nnested-wrapper-mismatch", expected: nil},
		{
			cmds:     "ok\nnested-wrapper-parse-error",
			expected: []string{"<string>:3: cannot parse directive at column 4: xx ="},
		},
		{cmds: "ok\nnested-wrapper-subtest-error", expected: []string{"<string>:3: invalid syntax for subtest"}},
		{
			// A retained wrapper reports the directive that is currently running;
			// once the test file is done, it no longer annotates messages.
			cmds:     "retain\nok\nuse-retained",
			expected: []string{"<string>:7: retained 1", "after end"},
		},
		{
			// Cleanup functions (which run after the subtest or the file is done) are
			// not annotated with the position of the last directive.
			cmds: "cleanup-in-subtest",
			input: `subtest foo
cleanup
----

td-cleanup
----

subtest end

cleanup
----

ok
----
`,
			expected: []string{
				"td cleanup from <string>:5", "cleanup from <string>:2", "cleanup from <string>:10",
			},
		},
	}
	for _, tc := range testCases {
		t.Run(strings.ReplaceAll(tc.cmds, "\n", ","), func(t *testing.T) {
			var input strings.Builder
			if tc.input != "" {
				input.WriteString(tc.input)
			} else {
				for _, cmd := range strings.Split(tc.cmds, "\n") {
					fmt.Fprintf(&input, "%s\n----\n\n", cmd)
				}
			}
			retained = nil
			rec := &recorderTB{TB: t}
			// The recorder calls runtime.Goexit() on FailNow and SkipNow, so run in
			// a separate goroutine.
			done := make(chan struct{})
			go func() {
				defer close(done)
				runFromString(rec, input.String(), func(t *TBWrapper[*recorderTB], d *TestData) string {
					return handler(t, d)
				})
			}()
			<-done
			rec.runCleanups()
			if retained != nil {
				retained.Log("after", "end")
			}
			if !reflect.DeepEqual(rec.msgs, tc.expected) {
				t.Errorf("expected:\n  %s\ngot:\n  %s",
					strings.Join(tc.expected, "\n  "), strings.Join(rec.msgs, "\n  "))
			}
		})
	}
}

// tOutputChildEnv is set when TestTOutput runs itself in a child process.
const tOutputChildEnv = "DATADRIVEN_TEST_T_OUTPUT_CHILD"

// TestTOutput verifies the actual test output produced through *T (which can't
// be intercepted in-process): messages are annotated with the directive
// position and attributed to the caller's file:line (not to the wrapper).
func TestTOutput(t *testing.T) {
	cases := []struct {
		name     string
		fn       func(t *T, d *TestData)
		expected string
	}{
		{"log", func(t *T, d *TestData) { t.Log("log", 1) }, "log 1"},
		{"logf", func(t *T, d *TestData) { t.Logf("logf %d", 1) }, "logf 1"},
		{"logf-nl", func(t *T, d *TestData) { t.Logf("logf-nl %d\n", 1) }, "logf-nl 1"},
		{"error", func(t *T, d *TestData) { t.Error("error", 1) }, "error 1"},
		{"errorf", func(t *T, d *TestData) { t.Errorf("errorf %d", 1) }, "errorf 1"},
		{"errorf-nl", func(t *T, d *TestData) { t.Errorf("errorf-nl %d\n", 1) }, "errorf-nl 1"},
		{"fatal", func(t *T, d *TestData) { t.Fatal("fatal", 1) }, "fatal 1"},
		{"fatalf", func(t *T, d *TestData) { t.Fatalf("fatalf %d", 1) }, "fatalf 1"},
		{"fatalf-nl", func(t *T, d *TestData) { t.Fatalf("fatalf-nl %d\n", 1) }, "fatalf-nl 1"},
		{"skip", func(t *T, d *TestData) { t.Skip("skip", 1) }, "skip 1"},
		{"skipf", func(t *T, d *TestData) { t.Skipf("skipf %d", 1) }, "skipf 1"},
		{"skipf-nl", func(t *T, d *TestData) { t.Skipf("skipf-nl %d\n", 1) }, "skipf-nl 1"},
		{"tb-helper", func(t *T, d *TestData) { tbHelperErrorf(t, "tb-helper %d", 1) }, "tb-helper 1"},
		{"t-helper", func(t *T, d *TestData) { tHelperErrorf(t, "t-helper %d", 1) }, "t-helper 1"},
		{"scan-arg", func(t *T, d *TestData) { ScanArg[int](d, "scan-arg") }, "missing argument: scan-arg"},
		{
			"scan-arg-or",
			func(t *T, d *TestData) {
				d.CmdArgs = []CmdArg{{Key: "or", Vals: []string{"foo"}}}
				ScanArgOr(d, "or", 1)
			},
			`argument "or": parse "foo" as int: strconv.ParseInt: parsing "foo": invalid syntax`,
		},
		{
			"maybe-scan-arg",
			func(t *T, d *TestData) {
				d.CmdArgs = []CmdArg{{Key: "maybe", Vals: []string{"foo"}}}
				MaybeScanArg[int](d, "maybe")
			},
			`argument "maybe": parse "foo" as int: strconv.ParseInt: parsing "foo": invalid syntax`,
		},
		// The position is not added again if the message already starts with it.
		{"has-pos", func(t *T, d *TestData) { t.Errorf("%s: has-pos", d.Pos) }, "has-pos"},
		{"td-fatalf", func(t *T, d *TestData) { d.Fatalf("td-fatalf %d", 1) }, "td-fatalf 1"},
		{"td-scan", func(t *T, d *TestData) { var x int; d.ScanArgs("x", &x) }, "missing argument: x"},
	}

	if os.Getenv(tOutputChildEnv) != "" {
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				RunTestFromString(t, "\ncmd\n----\n", func(t *T, d *TestData) string {
					c.fn(t, d)
					return ""
				})
			})
		}
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestTOutput$", "-test.v", "-test.count=1")
	cmd.Env = append(os.Environ(), tOutputChildEnv+"=1")
	// The child test fails, so we ignore the error.
	out, _ := cmd.CombinedOutput()
	lines := strings.Split(string(out), "\n")
	for _, c := range cases {
		// The message must be attributed to this file (not to the wrappers) and
		// must not be followed by a blank line (which happens if a trailing
		// newline is not handled like testing does).
		re := regexp.MustCompile(`^\s+t_test\.go:\d+: <string>:2: ` + regexp.QuoteMeta(c.expected) + `$`)
		found := false
		for i, l := range lines {
			if re.MatchString(l) {
				found = true
				if i+1 < len(lines) && lines[i+1] != "" && strings.TrimSpace(lines[i+1]) == "" {
					t.Errorf("%s: message followed by a blank line", c.name)
				}
				break
			}
		}
		if !found {
			t.Errorf("%s: no line matching %s", c.name, re)
		}
	}
	if t.Failed() {
		t.Logf("child output:\n%s", out)
	}
}

// runFromString is like RunTestFromString, but it always compares the outputs
// (even with -rewrite); it is used by tests that check the framework's
// messages.
func runFromString[U testing.TB](t U, input string, f func(t *TBWrapper[U], td *TestData) string) {
	t.Helper()
	runTestInternal(t, "<string>", strings.NewReader(input), f, false /* rewrite */)
}

// recorderTB is a testing.TB which records messages instead of reporting them.
// Messages logged by the framework itself (which start with a newline) are
// ignored, unless keepFramework is set.
type recorderTB struct {
	testing.TB
	keepFramework bool

	mu       sync.Mutex
	msgs     []string
	failed   bool
	skipped  bool
	cleanups []func()
}

// Run runs a subtest (on the same recorder) and then the cleanup functions
// registered during the subtest.
func (r *recorderTB) Run(name string, f func(testing.TB)) {
	r.mu.Lock()
	saved := r.cleanups
	r.cleanups = nil
	r.mu.Unlock()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(r)
	}()
	<-done
	r.runCleanups()
	r.mu.Lock()
	r.cleanups = saved
	r.mu.Unlock()
}

func (r *recorderTB) Cleanup(f func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cleanups = append(r.cleanups, f)
}

// runCleanups runs (in reverse order) and clears the registered cleanup
// functions.
func (r *recorderTB) runCleanups() {
	r.mu.Lock()
	cleanups := r.cleanups
	r.cleanups = nil
	r.mu.Unlock()
	for i := len(cleanups) - 1; i >= 0; i-- {
		cleanups[i]()
	}
}

func (r *recorderTB) record(msg string) {
	if strings.HasPrefix(msg, "\n") && !r.keepFramework {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.msgs = append(r.msgs, strings.TrimSuffix(msg, "\n"))
}

func (r *recorderTB) Helper() {}

func (r *recorderTB) Log(args ...any) { r.record(fmt.Sprintln(args...)) }

func (r *recorderTB) Logf(format string, args ...any) { r.record(fmt.Sprintf(format, args...)) }

func (r *recorderTB) Error(args ...any) {
	r.Log(args...)
	r.Fail()
}

func (r *recorderTB) Errorf(format string, args ...any) {
	r.Logf(format, args...)
	r.Fail()
}

func (r *recorderTB) Fatal(args ...any) {
	r.Log(args...)
	r.FailNow()
}

func (r *recorderTB) Fatalf(format string, args ...any) {
	r.Logf(format, args...)
	r.FailNow()
}

func (r *recorderTB) Skip(args ...any) {
	r.Log(args...)
	r.SkipNow()
}

func (r *recorderTB) Skipf(format string, args ...any) {
	r.Logf(format, args...)
	r.SkipNow()
}

func (r *recorderTB) Fail() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.failed = true
}

func (r *recorderTB) FailNow() {
	r.Fail()
	runtime.Goexit()
}

func (r *recorderTB) Failed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}

func (r *recorderTB) SkipNow() {
	r.mu.Lock()
	r.skipped = true
	r.mu.Unlock()
	runtime.Goexit()
}

func (r *recorderTB) Skipped() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.skipped
}
