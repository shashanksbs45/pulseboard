// Command pulseboard collects and stores system and application metrics.
package main

import (
	"fmt"
	"io"
	"os"
)

const usage = `usage: pulseboard <command> [flags]

commands:
  serve   start the HTTP server (run "pulseboard serve -h" for flags)
`

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

// run dispatches to a subcommand and returns the process exit status.
func run(args []string, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 2
	}
	switch args[0] {
	case "serve":
		if err := serve(args[1:], stderr); err != nil {
			fmt.Fprintf(stderr, "pulseboard serve: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "pulseboard: unknown command %q\n\n%s", args[0], usage)
		return 2
	}
}
