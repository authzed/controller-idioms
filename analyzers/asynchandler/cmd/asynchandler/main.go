package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/authzed/controller-idioms/analyzers/asynchandler"
)

func main() {
	singlechecker.Main(asynchandler.Analyzer)
}
