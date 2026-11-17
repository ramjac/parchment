package main

import (
	"fmt"
	"os"

	"example.com/parchment/internal/cli"
)

func main() {
	root := cli.New(os.Stdout, os.Stderr)
	err := root.Execute()
	cli.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
