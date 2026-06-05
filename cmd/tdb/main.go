package main

import (
	"fmt"
	"os"

	"tdb/internal/app"
	"tdb/internal/cli"
)

func main() {
	opts, err := cli.ParseOptions(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	if err := app.Run(app.Options{ConfigPath: opts.ConfigPath}); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
