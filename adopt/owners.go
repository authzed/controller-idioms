package adopt

import (
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

// OwnerKeysFromMeta returns a set of namespace/name keys for the owners
// of adopted objects with `annotationPrefix`. The namespace is always set
// to the namespace of the object passed in.
func OwnerKeysFromMeta(annotationPrefix string) func(in any) ([]string, error) {
	return func(in any) ([]string, error) {
		obj := in.(runtime.Object)
		objMeta, err := meta.Accessor(obj)
		if err != nil {
			return nil, err
		}

		ownerNames := make([]string, 0)
		for k := range objMeta.GetAnnotations() {
			if strings.HasPrefix(k, annotationPrefix) {
				ownerName := strings.TrimPrefix(k, annotationPrefix)
				nn := types.NamespacedName{Name: ownerName, Namespace: objMeta.GetNamespace()}
				ownerNames = append(ownerNames, nn.String())
			}
		}

		return ownerNames, nil
	}
}
