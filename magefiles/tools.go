//go:build tools
// +build tools

package magefiles

import (
	_ "github.com/golangci/golangci-lint/cmd/golangci-lint"
	_ "golang.org/x/vuln/cmd/govulncheck"
	_ "mvdan.cc/gofumpt"
)
