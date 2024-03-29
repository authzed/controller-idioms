//go:build mage

package main

import (
	"fmt"

	"github.com/magefile/mage/mg"
)

type Test mg.Namespace

// All Runs all test suites
func (t Test) All() error {
	mg.Deps(t.Unit, t.Integration)
	return nil
}

// Unit Runs the unit tests
func (Test) Unit() error {
	fmt.Println("running unit tests")
	return goTest("./...")
}

// Integration Run integration tests
func (Test) Integration() error {
	mg.Deps(checkDocker)
	_, _, _, err := provisionKind("controller-idioms-e2e")
	if err != nil {
		return err
	}
	return goTest("./...", "-tags", "e2e", "-timeout", "15m")
}
