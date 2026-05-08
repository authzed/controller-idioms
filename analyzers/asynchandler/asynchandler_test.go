package asynchandler_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/authzed/controller-idioms/analyzers/asynchandler"
)

func TestAnalyzer(t *testing.T) {
	testdata := analysistest.TestData()
	analysistest.Run(t, testdata, asynchandler.Analyzer, "example")
}
