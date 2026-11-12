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
	"strings"
	"sync/atomic"
	"testing"
)

// TBWrapper wraps the testing.TB of a running test (typically a *testing.T or
// a *testing.B; see T and B) and is passed to the RunTest and
// RunTestFromString callbacks. Error, Errorf, Fatal, Fatalf, Skip, Skipf, Log
// and Logf prefix the message with the position (file:line) of the directive
// that is currently running (or of the current input line, see
// TestData.InputLines), unless the message already starts with it.
//
// The position is that of the directive running at the time of the call, not
// the directive for which the wrapper was passed to the callback. This means
// that a wrapper retained by a long-lived object (e.g. one created by a setup
// directive) reports the directive in which the failure actually happened. It
// also means that messages from background goroutines (e.g. the logger of a
// component which does background work) are annotated with whatever directive
// happens to be running; such a logger should use the underlying testing.TB
// (the TB field) instead.
//
// *TBWrapper implements testing.TB: the other methods are forwarded to the
// underlying testing.TB, and a *TBWrapper can be passed to helpers which take a
// testing.TB (the messages they report are annotated as well). The underlying
// value is available as the TB field, which can be used to call methods
// specific to its type, e.g. t.TB.Run() for a *testing.T or
// b.TB.ReportMetric() for a *testing.B. To run a nested test file from within
// a callback, pass t.TB (or td.T) to RunTest.
//
// A TBWrapper must not be constructed directly; outside of RunTest, use Wrap.
type TBWrapper[U testing.TB] struct {
	testingTB
	// TB is the underlying testing.TB.
	TB U

	pos *directivePos
}

// testingTB is used to embed testing.TB in TBWrapper without conflicting with
// the TB field.
type testingTB = testing.TB

// T is the wrapper passed to the callbacks when running over a *testing.T.
type T = TBWrapper[*testing.T]

// B is the wrapper passed to the callbacks when running over a *testing.B.
type B = TBWrapper[*testing.B]

var _ testing.TB = (*T)(nil)
var _ testing.TB = (*B)(nil)

// Wrap returns a wrapper for tb which does not annotate messages. It can be
// used to call code that expects a wrapper outside of RunTest.
func Wrap[U testing.TB](tb U) *TBWrapper[U] {
	return newWrapper(tb, nil /* pos */)
}

func newWrapper[U testing.TB](tb U, pos *directivePos) *TBWrapper[U] {
	// Messages are reported directly to the innermost testing.TB, so that they
	// are not annotated twice when tb is itself a wrapper.
	return &TBWrapper[U]{testingTB: innermost(tb), TB: tb, pos: pos}
}

// Error is like testing.T.Error, with the directive position prepended.
func (t *TBWrapper[U]) Error(args ...any) {
	t.Helper()
	t.testingTB.Error(t.pos.prefix(sprintln(args...)))
}

// Errorf is like testing.T.Errorf, with the directive position prepended.
func (t *TBWrapper[U]) Errorf(format string, args ...any) {
	t.Helper()
	t.testingTB.Errorf("%s", t.pos.prefix(fmt.Sprintf(format, args...)))
}

// Fatal is like testing.T.Fatal, with the directive position prepended.
func (t *TBWrapper[U]) Fatal(args ...any) {
	t.Helper()
	t.testingTB.Fatal(t.pos.prefix(sprintln(args...)))
}

// Fatalf is like testing.T.Fatalf, with the directive position prepended.
func (t *TBWrapper[U]) Fatalf(format string, args ...any) {
	t.Helper()
	t.testingTB.Fatalf("%s", t.pos.prefix(fmt.Sprintf(format, args...)))
}

// Skip is like testing.T.Skip, with the directive position prepended.
func (t *TBWrapper[U]) Skip(args ...any) {
	t.Helper()
	t.testingTB.Skip(t.pos.prefix(sprintln(args...)))
}

// Skipf is like testing.T.Skipf, with the directive position prepended.
func (t *TBWrapper[U]) Skipf(format string, args ...any) {
	t.Helper()
	t.testingTB.Skipf("%s", t.pos.prefix(fmt.Sprintf(format, args...)))
}

// Log is like testing.T.Log, with the directive position prepended.
func (t *TBWrapper[U]) Log(args ...any) {
	t.Helper()
	t.testingTB.Log(t.pos.prefix(sprintln(args...)))
}

// Logf is like testing.T.Logf, with the directive position prepended.
func (t *TBWrapper[U]) Logf(format string, args ...any) {
	t.Helper()
	t.testingTB.Logf("%s", t.pos.prefix(fmt.Sprintf(format, args...)))
}

// wrapper is implemented by all TBWrapper instantiations (and by types which
// embed one; see asWrapper).
type wrapper interface {
	testing.TB
	// self returns the *TBWrapper.
	self() any
	// underlying returns the wrapped testing.TB.
	underlying() testing.TB
	// subTestWrapped runs f in a subtest of the wrapped testing.TB; f is passed
	// a wrapper (of the same type) for the subtest.
	subTestWrapped(name string, f func(testing.TB))
}

func (t *TBWrapper[U]) self() any {
	return t
}

func (t *TBWrapper[U]) underlying() testing.TB {
	return t.TB
}

func (t *TBWrapper[U]) subTestWrapped(name string, f func(testing.TB)) {
	subTest(t.TB, name, func(sub U) {
		f(newWrapper(sub, t.pos))
	})
}

// asWrapper returns tb as a wrapper if it is a *TBWrapper. A type which embeds
// a *TBWrapper is not considered a wrapper: it can have its own behavior.
func asWrapper(tb any) (wrapper, bool) {
	w, ok := tb.(wrapper)
	if !ok || w.self() != tb {
		return nil, false
	}
	return w, true
}

// innermost returns the testing.TB underlying the wrappers in tb.
func innermost(tb testing.TB) testing.TB {
	for {
		w, ok := asWrapper(tb)
		if !ok {
			return tb
		}
		tb = w.underlying()
	}
}

// unwrap returns the testing.TB underlying the wrappers in tb, as long as it is
// also a U (e.g. when U is testing.TB); otherwise it returns tb.
func unwrap[U testing.TB](tb U) U {
	for {
		w, ok := asWrapper(tb)
		if !ok {
			return tb
		}
		u, ok := w.underlying().(U)
		if !ok {
			return tb
		}
		tb = u
	}
}

// directivePos holds the position of the directive that is currently running.
// It is shared by all the wrappers created for a test file and it can be
// accessed concurrently (e.g. by goroutines started by an earlier directive).
type directivePos struct {
	p atomic.Pointer[string]
}

func (p *directivePos) set(pos string) {
	p.p.Store(&pos)
}

// push sets the position, and returns a function that restores the previous
// position, unless the position was changed in the meantime (e.g. because the
// next directive started).
func (p *directivePos) push(pos string) (restore func()) {
	np := &pos
	old := p.p.Swap(np)
	return func() { p.p.CompareAndSwap(np, old) }
}

func (p *directivePos) get() string {
	if pos := p.p.Load(); pos != nil {
		return *pos
	}
	return ""
}

// prefix prepends the current position (if any) to the given message, unless
// the message already starts with it (e.g. t.Fatalf("%s: ...", td.Pos, ...)). A
// nil directivePos (see Wrap) has no position.
func (p *directivePos) prefix(msg string) string {
	if p == nil {
		return msg
	}
	if pos := p.get(); pos != "" && !hasPosPrefix(msg, pos) {
		return pos + ": " + msg
	}
	return msg
}

// hasPosPrefix returns true if msg starts with pos (ignoring leading
// whitespace), followed by a colon, whitespace, or the end of the message.
func hasPosPrefix(msg, pos string) bool {
	m := strings.TrimLeft(msg, " \t\n")
	if !strings.HasPrefix(m, pos) {
		return false
	}
	rest := m[len(pos):]
	return rest == "" || strings.IndexByte(": \t\n", rest[0]) >= 0
}

// sprintln formats the arguments like testing.T.Log does.
func sprintln(args ...any) string {
	return strings.TrimSuffix(fmt.Sprintln(args...), "\n")
}
