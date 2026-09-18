package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "jobd:", err)
		os.Exit(1)
	}
}
