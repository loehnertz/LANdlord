//go:build !windows

package ui

import "context"

type TrayActions struct {
	Open, Mark, Finish, Quit func()
}

// RunTray is a no-op outside Windows; macOS and Linux builds use the status page only.
func RunTray(context.Context, TrayActions) {}
