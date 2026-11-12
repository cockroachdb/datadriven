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

// subTest runs f in a subtest of tb, using a suitable implementation of Run().
// The testing.TB of the subtest must be a U.
func subTest[U testing.TB](tb U, name string, f func(tb U)) {
	if w, ok := asWrapper(tb); ok {
		// A wrapper passed to a callback was passed back to us (e.g. to Walk).
		w.subTestWrapped(name, func(t testing.TB) { f(t.(U)) })
		return
	}
	switch t := any(tb).(type) {
	case *testing.T:
		t.Run(name, func(t *testing.T) { f(any(t).(U)) })
	case *testing.B:
		t.Run(name, func(t *testing.B) { f(any(t).(U)) })
	case interface {
		Run(string, func(testing.TB))
	}:
		t.Run(name, func(t testing.TB) {
			u, ok := t.(U)
			if !ok {
				panic(fmt.Sprintf(
					"%T.Run passed a %T to the subtest; use testing.TB as the type parameter", tb, t,
				))
			}
			f(u)
		})
	default:
		panic(fmt.Sprintf("don't know how to call Run over %T", tb))
	}
}
