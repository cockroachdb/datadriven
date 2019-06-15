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
	"sort"
	"testing"
)

func TestYAMLDriven(t *testing.T) {
	const input = `
## run1
- line 1
- line 2
---
# The slice gets put into the tuple component wise (that's
# just what the dummy function we're testing here does)
{foo: "line 1", bar: "line 2"}
---

## run2
# The result is the sum of the lengths (again, just what
# we're using as a dummy test here).
{color: yellow, size: large}
---
11
---

## run3
# Same as run2, but uses pointers in the func.
{color: yellow, size: tiny}
---
10
---

## keys
{key1: 1, key2: 2}
---
[key1, key2]
---

# A stateful example.
## add
# Empty input.
---
0
---

## add
3
---
3
---

## add
7
---
10
---
`

	type Out1 struct {
		Foo, Bar string
	}

	type Inp2 struct {
		Color string
		Size  string
	}

	var n int
	m := DriverMap{
		"run1": func(sl []string) Out1 {
			return Out1{
				Foo: sl[0],
				Bar: sl[1],
			}
		},

		"run2": func(tup Inp2) int {
			return len(tup.Color) + len(tup.Size)
		},

		"run3": func(tup *Inp2) *int {
			i := len(tup.Color) + len(tup.Size)
			return &i
		},

		"keys": func(m map[string]int) []string {
			var sl []string
			for k := range m {
				sl = append(sl, k)
			}
			sort.Slice(sl, func(i, j int) bool {
				return sl[i] < sl[j]
			})
			return sl
		},

		"add": func(delta int) int {
			n += delta
			return n
		},
	}
	RunYAMLFromString(t, input, m)
}
