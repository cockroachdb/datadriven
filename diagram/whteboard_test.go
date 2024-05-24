// Copyright 2024 The Cockroach Authors.
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

package diagram

import (
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/cockroachdb/datadriven"
)

func TestDataDriven(t *testing.T) {
	datadriven.RunTest(t, "testdata/whiteboard", func(t *testing.T, d *datadriven.TestData) string {
		var wb Whiteboard
		switch d.Cmd {
		case "simple":
			wb.Write(0, 0, "ello")
			wb.Write(0, 7, "world")
			wb.Write(0, -1, "h")

		case "circle":
			const radius = 6
			for a := 0; a < 360; a++ {
				radians := float64(a) * math.Pi / 180
				x := int(math.Round(radius * math.Cos(radians) * 2.5))
				y := int(math.Round(radius * math.Sin(radians)))
				wb.Write(y, x, "*")
			}

		case "axis":
			const spacing = 4
			for i := -6; i <= 6; i++ {
				c := i * spacing
				wb.Write(0, c-spacing+1, strings.Repeat("-", spacing-1))
				wb.Write(0, c, "|")
				wb.Write(0, c+1, strings.Repeat("-", spacing-1))
				wb.Write(1, c, "|")
				str := fmt.Sprint(i)
				wb.Write(2, c-len(str)/2, str)
			}
			wb.Write(-1, 0, "ℤ")

		default:
			d.Fatalf(t, "unknown command: %s", d.Cmd)
		}
		return wb.String()
	})

}
