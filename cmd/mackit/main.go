package main

import (
	"os"

	"github.com/devcell-sh/go-mackit/internal/cli"
)

func main() {
	if err := cli.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
