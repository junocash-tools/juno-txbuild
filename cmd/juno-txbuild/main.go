package main

import (
	"os"

	"github.com/Abdullah1738/juno-txbuild/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
