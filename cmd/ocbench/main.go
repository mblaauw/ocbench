package main

import (
	"os"

	"mbl/ocbench/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
