//go:build tools
// +build tools

package tools

//go:generate go run mvdan.cc/gofumpt -l -w .

import (
	_ "github.com/maxbrunsfeld/counterfeiter/v6"
	_ "mvdan.cc/gofumpt"
)
