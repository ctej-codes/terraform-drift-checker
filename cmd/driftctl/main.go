package main

import (
    "context"
    "encoding/json"
    "flag"
    "fmt"
    "io"
    "log"
    "os"
    "sort"
    "strings"

    "github.com/ctej-codes/terraform-drift-checker/internal/awsfetcher"
    "github.com/ctej-codes/terraform-drift-checker/internal/comparator"
    "github.com/ctej-codes/terraform-drift-checker/internal/diag"
    "github.com/ctej-codes/terraform-drift-checker/internal/report"
    "github.com/ctej-codes/terraform-drift-checker/internal/tfstate"
)

func main() {
    var tfStatePath string
    var region string
    var outFile string
    var useColor bool

    flag.StringVar(&tfStatePath, "tfstate", "terraform.tfstate", "path to terraform state file (local)")
    flag.StringVar(&region, "region", "us-east-1", "AWS region to query")
    flag.StringVar(&outFile, "out", "", "output JSON file (optional)")
    flag.BoolVar(&useColor, "color", true, "enable ANSI color in terminal output")
    var verbose bool
    flag.BoolVar(&verbose, "verbose", false, "enable verbose output")
    flag.Parse()

    diag.Verbose = verbose

    ctx := context.Background()

    // Read tfstate
    expected, err := tfstate.ReadLocalState(tfStatePath)
    if err != nil {
        log.Fatalf("reading tfstate: %v", err)
    }

    // Fetch actual resources from AWS for the expected tfstate resources only
    actual, err := awsfetcher.FetchForExpected(ctx, region, expected)
    if err != nil {
        log.Printf("warning: fetching AWS resources failed: %v", err)
    }

    // Compare
    diffs := comparator.Compare(expected, actual)

    // Generate report
    rep := report.Report{
        ExpectedCount: len(expected),
        ActualCount:   len(actual),
        Diffs:         diffs,
    }

    // If an output JSON file was requested, write the JSON report to that file.
    if outFile != "" {
        f, err := os.Create(outFile)
        if err != nil {
            log.Fatalf("create output file: %v", err)
        }
        enc := json.NewEncoder(f)
        enc.SetIndent("", "  ")
        if err := enc.Encode(rep); err != nil {
            f.Close()
            log.Fatalf("encode report: %v", err)
        }
        f.Close()
    }

    // Human friendly summary (printed to console only)
    printTextReport(os.Stdout, rep, diag.Verbose, useColor)

    fmt.Println("Scan complete")
}

func parseMapString(s string) map[string]string {
    m := map[string]string{}
    if s == "" {
        return m
    }
    // input format: k=v;k2=v2; or JSON fallback
    if strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[") {
        // try JSON map parse
        var j map[string]interface{}
        if err := json.Unmarshal([]byte(s), &j); err == nil {
            for k, v := range j {
                m[k] = fmt.Sprintf("%v", v)
            }
            return m
        }
    }
    parts := strings.Split(s, ";")
    for _, p := range parts {
        if strings.TrimSpace(p) == "" {
            continue
        }
        kv := strings.SplitN(p, "=", 2)
        if len(kv) == 2 {
            m[kv[0]] = kv[1]
        } else {
            // treat as flag
            m[p] = ""
        }
    }
    return m
}

func prettyValue(s string) string {
    if s == "" {
        return "<empty>"
    }
    // try to detect JSON
    if strings.HasPrefix(strings.TrimSpace(s), "{") || strings.HasPrefix(strings.TrimSpace(s), "[") {
        var out interface{}
        if err := json.Unmarshal([]byte(s), &out); err == nil {
            jb, _ := json.MarshalIndent(out, "        ", "  ")
            return "\n" + string(jb)
        }
    }
    // long semicolon lists (tags) -> inline
    if strings.Contains(s, ";") {
        return s
    }
    return s
}

// ANSI color helpers
const (
    colReset = "\x1b[0m"
    colRed   = "\x1b[31m"
    colGreen = "\x1b[32m"
    colYellow = "\x1b[33m"
    colBlue  = "\x1b[34m"
)

func colorize(s, col string, enabled bool) string {
    if !enabled {
        return s
    }
    return col + s + colReset
}

func printTextReport(w io.Writer, rep report.Report, verbose, useColor bool) {
    // header
    fmt.Fprintln(w, colorize("--- Drift Scan Summary ---", colBlue, useColor))
    fmt.Fprintf(w, "Expected resources: %d\n", rep.ExpectedCount)
    fmt.Fprintf(w, "Actual resources:   %d\n", rep.ActualCount)
    fmt.Fprintf(w, "Diffs found:        %d\n", len(rep.Diffs))

    if !verbose || len(rep.Diffs) == 0 {
        fmt.Fprintln(w)
        return
    }

    fmt.Fprintln(w)
    fmt.Fprintln(w, colorize("Detailed diffs:", colYellow, useColor))
    for _, d := range rep.Diffs {
        head := fmt.Sprintf("- %s: %s (%s)", d.ChangeType, d.ResourceID, d.Type)
        if d.ChangeType == "Modified" {
            fmt.Fprintln(w, colorize(head, colRed, useColor))
        } else {
            fmt.Fprintln(w, head)
        }
        sort.SliceStable(d.Changes, func(i, j int) bool { return d.Changes[i].Path < d.Changes[j].Path })
        for _, c := range d.Changes {
            // print tags and attributes in single-line expected/actual form
            // tags are already formatted as semicolon lists by comparator.formatMap
            exp := c.Expected
            act := c.Actual
            fmt.Fprintf(w, "    * %s: expected='%s' actual='%s'\n", c.Path, exp, act)
        }
        fmt.Fprintln(w)
    }
}
