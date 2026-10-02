package main

import (
	"fmt"
	"os"

	"example.com/parchment/internal/cli"
)

func main() {
	root := cli.New(os.Stdout, os.Stderr)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
