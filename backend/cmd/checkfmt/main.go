package main

import (
	"fmt"
	"os"

	"github.com/hujinrun/flowspace/internal/testsupport/gofmtcheck"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: checkfmt <path> [path...]")
		os.Exit(2)
	}
	unformatted, err := gofmtcheck.Check(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if len(unformatted) == 0 {
		return
	}
	fmt.Fprintln(os.Stderr, "the following files need gofmt:")
	for _, path := range unformatted {
		fmt.Fprintln(os.Stderr, path)
	}
	os.Exit(1)
}
