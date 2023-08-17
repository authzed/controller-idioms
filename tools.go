//go:build tools
// +build tools

package magefiles

import (
	// counterfeiter doesn't like running within magefiles, so its top-level
	_ "github.com/maxbrunsfeld/counterfeiter/v6"
)
