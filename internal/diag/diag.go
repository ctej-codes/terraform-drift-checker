package diag

import (
    "fmt"
    "log"
)

var Verbose = false

func Info(format string, args ...interface{}) {
    log.Printf(format, args...)
}

func Debug(format string, args ...interface{}) {
    if Verbose {
        fmt.Printf("DEBUG: "+format+"\n", args...)
    }
}
