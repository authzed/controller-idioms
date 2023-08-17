// Package bootstrap implements utilities for boostrapping a kube cluster
// with resources on controller start.
//
// This is typically used to bootstrap CRDs and CRs (of the types defined by the
// CRDs) so that a controller can be continuously deployed and still include
// breaking changes.
//
// The bootstrap approach allows the controller to determine when and how to
// coordinate updates to the apis it manages. It should not typically be used
// by end-users of an operator, who may be using one or more other tools to
// manage the deployment of the operator and the resources it manages, and may
// not wish to grant the privileges required to bootstrap.
package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"

	"github.com/cespare/xxhash/v2"
	"github.com/go-logr/logr"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/utils/pointer"
)

// KubeResourceObject is satisfied by any standard kube object.
type KubeResourceObject interface {
	metav1.Object
	runtime.Object
}

// ResourceFromFile creates a KubeResourceObject with the given config file
func ResourceFromFile[O KubeResourceObject](ctx context.Context, fieldManager string, gvr schema.GroupVersionResource, dclient dynamic.Interface, configPath string, lastHash uint64) (uint64, error) {
	if len(configPath) <= 0 {
		logr.FromContextOrDiscard(ctx).V(4).Info("bootstrap file path not specified")
		return 0, nil
	}

	f, err := os.Open(configPath)
	defer func() {
		utilruntime.HandleError(f.Close())
	}()
	if errors.Is(err, os.ErrNotExist) {
		logr.FromContextOrDiscard(ctx).V(4).Info("no bootstrap file present, skipping bootstrapping")
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	contents, err := io.ReadAll(f)
	if err != nil {
		return 0, err
	}
	hash := xxhash.Sum64(contents)

	// no changes since last apply
	if lastHash == hash {
		return hash, nil
	}

	decoder := yaml.NewYAMLToJSONDecoder(bytes.NewReader(contents))
	for {
		var objectDef O
		if err := decoder.Decode(&objectDef); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return hash, err
		}

		data, err := json.Marshal(objectDef)
		if err != nil {
			return hash, err
		}

		_, err = dclient.
			Resource(gvr).
			Namespace(objectDef.GetNamespace()).
			Patch(ctx, objectDef.GetName(), types.ApplyPatchType, data, metav1.PatchOptions{FieldManager: fieldManager, Force: pointer.Bool(true)})
		if err != nil {
			return hash, err
		}
	}

	return hash, nil
}
