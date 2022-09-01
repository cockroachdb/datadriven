// Copyright 2022 The Cockroach Authors.
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
	"testing"
)

// subTest delegates to a suitable implementation of Run().
// This will be obsoleted when we're comfortable using 1.18 templates.
func subTest(t testing.TB, name string, f func(t testing.TB)) {
	switch t := t.(type) {
	case *testing.T:
		t.Run(name, func(t *testing.T) { f(t) })
	case *testing.B:
		t.Run(name, func(t *testing.B) { f(t) })
	case interface {
		Run(string, func(testing.TB))
	}:
		t.Run(name, f)
	default:
		panic(fmt.Sprintf("don't know how to call Run over %T", t))
	}
}
