package main

import (
	"fmt"
	"os"

	"github.com/loehnertz/LANdlord/internal/version"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println("landlord", version.String())
		return
	}
	fmt.Fprintln(os.Stderr, "usage: landlord version")
	os.Exit(2)
}
