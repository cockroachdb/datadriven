# Migrating from v1 to v2

v2 requires Go 1.24. v1 (v1.0.2 or later; older versions panic at startup)
and v2 can be linked in the same test binary (they share the `-rewrite` and
`-datadriven-quiet` flags), so a large repository can be migrated one package
at a time. Note that `TestData` (and the other types) of v1 and v2 are
different, so packages that pass them to each other (e.g. a shared helper
package with functions that take a `*datadriven.TestData`) must be migrated
together.

## Mechanical changes

The following rewrites (run on the Go files that use datadriven) take care of
most of the changes. The receiver and argument names they match (`d`/`td`,
`t`/`b`/`tb`) are the common ones; adjust them as needed. Afterwards, run
`goimports` (or remove unused imports) and fix the remaining compile errors (see
below).

```bash
# Import path.
perl -pi -e 's#"github.com/cockroachdb/datadriven"#"github.com/cockroachdb/datadriven/v2"#' $FILES

# Callback types; the Any variants are replaced by the generic functions.
perl -pi -e 's/func\((\w+) \*testing\.T, (\w+) \*datadriven\.TestData\) (string|\(\w+ string\))/func($1 *datadriven.T, $2 *datadriven.TestData) $3/g; s/func\((\w+) \*testing\.B, (\w+) \*datadriven\.TestData\) (string|\(\w+ string\))/func($1 *datadriven.B, $2 *datadriven.TestData) $3/g; s/func\((\w+) testing\.TB, (\w+) \*datadriven\.TestData\) (string|\(\w+ string\))/func($1 *datadriven.TBWrapper[testing.TB], $2 *datadriven.TestData) $3/g; s/\bdatadriven\.(RunTest|RunTestFromString|Walk)Any\(/datadriven.${1}[testing.TB](/g' $FILES

# TestData methods no longer take a testing.TB; TestData.Logf is removed.
perl -pi -e 's/\b(d|td)\.(ScanArgs|MaybeScanArgs|Fatalf|Retry|RetryFor)\((t|b|tb), /$1.$2(/g; s/\b(d|td)\.Logf\((t|b|tb), /$2.Logf(/g' $FILES

# Helpers that take a *testing.T along with a TestData can take a testing.TB.
perl -pi -e 's/^(func \w+\(\w+) \*testing\.T(, \w+ \*datadriven\.TestData)/$1 testing.TB$2/' $FILES

# CockroachDB: pkg/testutils/dd is replaced by the generic ScanArg functions.
perl -pi -e 's/\bdd\.ScanArgOpt\b/dd.MaybeScanArg/g; s/\bdd\.(ScanArg|ScanArgOr|MaybeScanArg)(\[(?:[^\[\]]|\[[^\[\]]*\])*\])?\((t|b|tb), /datadriven.$1$2(/g' $FILES
```

## Other changes

- **The callback receives a wrapper.** `*datadriven.T` (or `*datadriven.B`)
  wraps the `*testing.T` (or `*testing.B`) and annotates messages with the
  position of the directive, so including `d.Pos` in messages is no longer
  necessary (it is not printed twice). The wrapper implements `testing.TB`;
  methods specific to `*testing.T` or `*testing.B` are available through the
  `TB` field (e.g. `t.TB.Run(...)`, `b.TB.ReportMetric(...)`). A helper that
  requires a `*testing.T` can be passed `t.TB`.
- **`TestData.T`.** The wrapper is also available as `TestData.T`, which the
  `TestData` methods use to report errors. A `TestData` constructed manually
  (e.g. `TestData{Cmd: ...}`) must set `T` before using those methods.
- **`TestData.Pos` is a struct** (`File`, `Line`). Use `d.Pos.String()` where
  a string is needed, and `d.Pos.File` / `d.Pos.Line` instead of parsing the
  string. Assigning `d.Pos` no longer affects messages: to report errors at the
  position of a line within the input, iterate with `d.InputLines()`.
- **Arguments.** `ScanArg`, `ScanArgOr` and `MaybeScanArg` replace the
  `var x T; d.ScanArgs("x", &x)` pattern. An argument without values (e.g.
  `cmd verbose`) can be scanned into a bool, as true. Positional arguments can
  be retrieved with `PopArg`. `CmdArg.ExpectNumVals` and
  `ExpectNumValsGE` are removed (check `len(arg.Vals)`). Some scan error
  messages have changed.
- **Subtests in benchmarks.** `subtest` directives are not supported when
  running over a `*testing.B`; they now fail with an error (in v1, the repeated
  invocations of a sub-benchmark function consumed the rest of the file).
- **Unknown commands.** A `default:` case that returns an "unknown command"
  output (instead of failing) lets `-rewrite` record that output as expected;
  consider replacing it with `t.Fatalf`.
