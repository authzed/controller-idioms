//go:build mage

package main

import (
	"fmt"
	"os"

	"github.com/magefile/mage/mg"
	"github.com/magefile/mage/sh"
)

type Lint mg.Namespace

// All Run all linters
func (l Lint) All() error {
	mg.Deps(l.Go, l.Extra)
	return nil
}

// Extra Lint everything that's not code
func (l Lint) Extra() error {
	mg.Deps(l.Markdown, l.Yaml)
	return nil
}

// Yaml Lint yaml
func (Lint) Yaml() error {
	mg.Deps(checkDocker)
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	return sh.RunV("docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/src:ro", cwd),
		"cytopia/yamllint:1", "-c", "/src/.yamllint", "/src")
}

// Markdown Lint markdown
func (Lint) Markdown() error {
	mg.Deps(checkDocker)
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	return sh.RunV("docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/src:ro", cwd),
		"ghcr.io/igorshubovych/markdownlint-cli:v0.34.0", "--config", "/src/.markdownlint.yaml", "/src")
}

// Go Run all go linters
func (l Lint) Go() error {
	err := Gen{}.All()
	if err != nil {
		return err
	}
	mg.Deps(l.Gofumpt)
	return nil
}

// Gofumpt Run gofumpt
func (Lint) Gofumpt() error {
	fmt.Println("formatting go")
	return RunSh("go", Tool())("run", "mvdan.cc/gofumpt", "-l", "-w", "..")
}
