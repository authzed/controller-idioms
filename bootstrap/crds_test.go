package bootstrap

import (
	"embed"

	"k8s.io/client-go/rest"
)

//go:embed example/*.yaml
var crdFS embed.FS

func ExampleCRD() {
	_ = CRD(&rest.Config{}, crdFS, "example")
	// Output:
}
