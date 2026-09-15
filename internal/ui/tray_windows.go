//go:build windows

package ui

import (
	"context"
	_ "embed"
	"runtime"

	"fyne.io/systray"
)

//go:embed icon.ico
var iconICO []byte

type TrayActions struct {
	Open, Mark, Finish, Quit func()
}

// RunTray shows the notification-area icon until ctx is done. It returns immediately.
func RunTray(ctx context.Context, a TrayActions) {
	go func() {
		runtime.LockOSThread()
		systray.Run(func() {
			systray.SetIcon(iconICO)
			systray.SetTitle("LANdlord")
			systray.SetTooltip("LANdlord is recording your internet connection")
			open := systray.AddMenuItem("Open LANdlord", "Show the status page")
			mark := systray.AddMenuItem("It's bad right now!", "Mark this moment")
			systray.AddSeparator()
			finish := systray.AddMenuItem("Finish and create the report", "Stop recording and write the report")
			quit := systray.AddMenuItem("Pause and quit", "Recording continues when LANdlord is started again")
			systray.SetOnTapped(a.Open)
			go func() {
				for {
					select {
					case <-open.ClickedCh:
						a.Open()
					case <-mark.ClickedCh:
						a.Mark()
					case <-finish.ClickedCh:
						a.Finish()
					case <-quit.ClickedCh:
						a.Quit()
					case <-ctx.Done():
						systray.Quit()
						return
					}
				}
			}()
		}, func() {})
	}()
}
