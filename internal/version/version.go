// Package version holds the build version, set by the release build via -ldflags.
package version

var (
	Version = "dev"
	Commit  = ""
)

func String() string {
	if Commit == "" {
		return Version
	}
	return Version + " (" + Commit + ")"
}
