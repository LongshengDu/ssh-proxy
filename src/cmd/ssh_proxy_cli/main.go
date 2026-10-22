package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"ssh_proxy/src/proxy"
)

func main() {
	if err := proxy.Run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
