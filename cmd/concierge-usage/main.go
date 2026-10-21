package main

import (
	"os"

	"github.com/roman/concierge/pkg/sessionusagecli"
)

func main() {
	os.Exit(sessionusagecli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
