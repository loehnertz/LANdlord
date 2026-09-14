//go:build windows

// Package host returns the Platform implementation for the operating system LANdlord runs on.
package host

import (
	"github.com/loehnertz/LANdlord/internal/platform"
	"github.com/loehnertz/LANdlord/internal/platform/winplat"
)

func New() platform.Platform { return winplat.New() }
