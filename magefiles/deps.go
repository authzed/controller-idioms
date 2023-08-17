//go:build mage

package main

import "github.com/magefile/mage/mg"

var goModules = []string{
	".", "magefiles",
}

type Deps mg.Namespace

// Tidy go mod tidy all go modules
func (Deps) Tidy() error {
	for _, mod := range goModules {
		if err := RunSh("go", WithDir(mod))("mod", "tidy"); err != nil {
			return err
		}
	}

	return nil
}
