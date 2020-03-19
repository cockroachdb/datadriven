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

// +build gotest

package datadriven

import (
	"flag"
	"fmt"
	"testing"
)

var dataDir = flag.String("datadir", "", "directory where to find datafiles")

func TestRewrite(t *testing.T) {
	if *dataDir == "" {
		t.Fatal("don't run this test directly; run TestGoTestInterface or similar instead")
	}
	Walk(t, *dataDir, func(t *testing.T, path string) {
		RunTest(t, path, func(t *testing.T, d *TestData) string {
			t.Logf("directive: %v %+v", d.Cmd, d.CmdArgs)
			switch d.Cmd {
			case "hello":
				return d.CmdArgs[0].Key + " was said"
			case "nothing":
				return ""
			case "blank":
				l := 0
				if len(d.CmdArgs) > 0 {
					l = len(d.CmdArgs[1].Key)
				}
				return fmt.Sprintf("%*s\n", l, "")
			case "skip":
				// Verify that calling t.Skip() does not fail with an API error on
				// testing.T.
				t.Skip("woo")
			case "error":
				t.Error("some error: " + d.CmdArgs[0].Key)
			default:
				t.Fatalf("unknown directive: %s", d.Cmd)
			}
			return d.Expected
		})
	})
}
