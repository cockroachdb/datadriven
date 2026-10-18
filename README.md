# Data-Driven Tests for Go

[![Go Reference](https://pkg.go.dev/badge/github.com/cockroachdb/datadriven/v2.svg)](https://pkg.go.dev/github.com/cockroachdb/datadriven/v2)

This repository implements an extension of [Table-Driven Testing]. Instead of
building and iterating over a table in the test code, the input is further
separated into files (or inline strings). For certain classes of tests, this
can significantly reduce the friction involved in writing and reading these
tests.

[Table-Driven Testing]: https://go.dev/wiki/TableDrivenTests

```go
import "github.com/cockroachdb/datadriven/v2"

func TestAdd(t *testing.T) {
	datadriven.RunTest(t, "testdata/add", func(t *datadriven.T, td *datadriven.TestData) string {
		switch td.Cmd {
		case "add":
			a := datadriven.ScanFlag[int](td, "a")
			b := datadriven.ScanFlagOr(td, "b", 1)
			return fmt.Sprint(a + b)
		default:
			t.Fatalf("unknown command %q", td.Cmd)
			return ""
		}
	})
}
```

with `testdata/add`:

```
add -a=1 -b=2
----
3

add -a=5
----
6
```

Run the tests with `-rewrite` to update the expected outputs in the test files
with the actual outputs.

See the [package documentation](https://pkg.go.dev/github.com/cockroachdb/datadriven/v2)
for the details of the file format and the API, and [MIGRATION.md](MIGRATION.md)
for migrating from v1.
