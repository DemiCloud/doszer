// Command gen converts a Redump datfile (as downloaded: a zip, or its
// Logiqx XML) to the compact form internal/redump embeds. `make dat` runs
// it.
//
// Usage: go run ./internal/redump/gen IN OUT
package main

import (
	"bytes"
	"fmt"
	"os"

	"github.com/DemiCloud/doszer/internal/redump"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: gen IN OUT")
		os.Exit(2)
	}
	db, err := redump.Load(os.Args[1])
	if err == nil {
		var buf bytes.Buffer
		if err = db.Write(&buf); err == nil {
			err = os.WriteFile(os.Args[2], buf.Bytes(), 0o644)
		}
		if err == nil {
			fmt.Printf("%s: %d discs, Redump datfile %s\n", os.Args[2], len(db.Discs), db.Version)
		}
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
