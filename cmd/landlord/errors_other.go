//go:build !windows

package main

import (
	"fmt"
	"os"
)

func showError(err error) { fmt.Fprintln(os.Stderr, "landlord:", err) }
