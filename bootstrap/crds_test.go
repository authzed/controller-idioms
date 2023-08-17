package bootstrap

import (
	"context"
	"embed"

	"k8s.io/client-go/rest"
)

//go:embed example/*.yaml
var crdFS embed.FS

func ExampleCRD() {
	_ = CRDs(context.Background(), &rest.Config{}, crdFS, "example")
	// Output:
}
