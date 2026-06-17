package main

import (
	"fmt"
	"os"

	"github.com/srbouffard/arok/internal/cli"
)

func main() {
	app := cli.New(os.Stdin, os.Stdout, os.Stderr)
	if err := app.Run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}
