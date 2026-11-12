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
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// ClearResults rewrites the given test file, removing the expected results of
// all directives. The file is left unchanged if it cannot be parsed.
func ClearResults(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0644 /* irrelevant */)
	if err != nil {
		return err
	}
	defer func() {
		_ = file.Close()
	}()
	finfo, err := file.Stat()
	if err != nil {
		return err
	}

	if finfo.IsDir() {
		return fmt.Errorf("%s is a directory, not a file", path)
	}

	tb := &clearResultsTB{errs: &errorList{}}
	var data []byte
	runAndWait(func() {
		data = runTestInternal[testing.TB](
			tb, path, file,
			func(t *TBWrapper[testing.TB], td *TestData) string {
				td.MarkAllFlagsProcessed()
				return ""
			},
			true, /* rewrite */
		)
	})
	if tb.Failed() {
		if msgs := tb.errs.get(); len(msgs) > 0 {
			return errors.New(strings.Join(msgs, "\n"))
		}
		return fmt.Errorf("%s: failed to clear results", path)
	}
	return rewriteFile(file, data)
}

// clearResultsTB is the testing.TB used by ClearResults, outside of a test. It
// records errors and implements FailNow, SkipNow and Run like testing.T does.
//
// Only the methods used by the framework are implemented.
type clearResultsTB struct {
	// testing.TB is embedded for the unexported method of the interface; it is
	// nil, so calling a method which is not implemented below panics.
	testing.TB

	name    string
	errs    *errorList
	failed  atomic.Bool
	skipped atomic.Bool
}

var _ testing.TB = (*clearResultsTB)(nil)

// errorList is shared by a clearResultsTB and its subtests.
type errorList struct {
	mu   sync.Mutex
	msgs []string
}

func (l *errorList) add(msg string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, msg)
}

func (l *errorList) get() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.msgs...)
}

// runAndWait runs f in a separate goroutine (so that it can call
// runtime.Goexit) and waits for it to finish.
func runAndWait(f func()) {
	done := make(chan struct{})
	go func() {
		defer close(done)
		f()
	}()
	<-done
}

func (t *clearResultsTB) Helper() {}

func (t *clearResultsTB) Name() string { return t.name }

func (t *clearResultsTB) Log(args ...any) {}

func (t *clearResultsTB) Logf(format string, args ...any) {}

func (t *clearResultsTB) Error(args ...any) {
	t.errs.add(sprintln(args...))
	t.Fail()
}

func (t *clearResultsTB) Errorf(format string, args ...any) {
	t.errs.add(fmt.Sprintf(format, args...))
	t.Fail()
}

func (t *clearResultsTB) Fatal(args ...any) {
	t.Error(args...)
	t.FailNow()
}

func (t *clearResultsTB) Fatalf(format string, args ...any) {
	t.Errorf(format, args...)
	t.FailNow()
}

func (t *clearResultsTB) Fail() { t.failed.Store(true) }

func (t *clearResultsTB) FailNow() {
	t.Fail()
	runtime.Goexit()
}

func (t *clearResultsTB) Failed() bool { return t.failed.Load() }

func (t *clearResultsTB) Skip(args ...any) { t.SkipNow() }

func (t *clearResultsTB) Skipf(format string, args ...any) { t.SkipNow() }

func (t *clearResultsTB) SkipNow() {
	t.skipped.Store(true)
	runtime.Goexit()
}

func (t *clearResultsTB) Skipped() bool { return t.skipped.Load() }

// Run runs f in a subtest; a failure of the subtest fails t (like testing.T).
func (t *clearResultsTB) Run(name string, f func(testing.TB)) {
	sub := &clearResultsTB{name: t.name + "/" + name, errs: t.errs}
	runAndWait(func() { f(sub) })
	if sub.Failed() {
		t.Fail()
	}
}
