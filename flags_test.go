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
	"flag"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

func TestBoolFlag(t *testing.T) {
	fs := flag.NewFlagSet("test", flag.PanicOnError)
	// A new flag.
	get := boolFlag(fs, "datadriven-test-new", "usage")
	if f := fs.Lookup("datadriven-test-new"); f == nil || f.Usage != "usage" {
		t.Fatalf("flag not defined")
	}
	if get() {
		t.Fatalf("expected false")
	}
	if err := fs.Set("datadriven-test-new", "true"); err != nil {
		t.Fatal(err)
	}
	if !get() {
		t.Fatalf("expected true")
	}

	// An existing flag (e.g. defined by v1 of the package) is reused.
	p := fs.Bool("datadriven-test-existing", false, "existing")
	get = boolFlag(fs, "datadriven-test-existing", "usage")
	if get() {
		t.Fatalf("expected false")
	}
	*p = true
	if !get() {
		t.Fatalf("expected true")
	}

	// An existing flag of a different type.
	fs.String("datadriven-test-string", "", "string")
	defer func() {
		if r := fmt.Sprint(recover()); !strings.Contains(r, "already defined, with a non-bool type") {
			t.Fatalf("unexpected panic: %s", r)
		}
	}()
	boolFlag(fs, "datadriven-test-string", "usage")
}

func TestRewriteFlag(t *testing.T) {
	orig := flag.Lookup("rewrite").Value.String()
	defer func() { _ = flag.Set("rewrite", orig) }()
	for _, val := range []bool{true, false} {
		if err := flag.Set("rewrite", fmt.Sprint(val)); err != nil {
			t.Fatal(err)
		}
		if Rewrite() != val {
			t.Fatalf("Rewrite() = %t with -rewrite=%t", Rewrite(), val)
		}
	}
}

// quietEnvChildEnv is set when TestQuietEnvVar runs itself in a child process.
const quietEnvChildEnv = "DATADRIVEN_TEST_QUIET_ENV_CHILD"

// TestQuietEnvVar verifies that DATADRIVEN_QUIET_LOG sets -datadriven-quiet
// (which is done at init time, so it requires a child process).
func TestQuietEnvVar(t *testing.T) {
	if os.Getenv(quietEnvChildEnv) != "" {
		if Verbose() != (os.Getenv("DATADRIVEN_QUIET_LOG") == "false") {
			t.Fatalf("unexpected Verbose() = %t", Verbose())
		}
		return
	}
	for _, val := range []string{"true", "false"} {
		cmd := exec.Command(os.Args[0], "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.v", "-test.count=1")
		cmd.Env = append(os.Environ(), quietEnvChildEnv+"=1", "DATADRIVEN_QUIET_LOG="+val)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("DATADRIVEN_QUIET_LOG=%s: %v\n%s", val, err, out)
		}
		// Make sure the test actually ran in the child process.
		if !strings.Contains(string(out), "--- PASS: "+t.Name()) {
			t.Fatalf("DATADRIVEN_QUIET_LOG=%s: test did not run:\n%s", val, out)
		}
	}
}
