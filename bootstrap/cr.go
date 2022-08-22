package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/ioutil"
	"os"

	"github.com/cespare/xxhash/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/dynamic"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/authzed/spicedb-operator/pkg/metadata"
)

type KubeResourceObject interface {
	metav1.Object
	runtime.Object
}

// ResourceFromFile bootstraps a CustomResource with the given config file
func ResourceFromFile[O KubeResourceObject](ctx context.Context, gvr schema.GroupVersionResource, dclient dynamic.Interface, configPath string, lastHash uint64) (uint64, error) {
	if len(configPath) <= 0 {
		klog.V(4).Info("bootstrap file path not specified")
		return 0, nil
	}

	f, err := os.Open(configPath)
	defer func() {
		utilruntime.HandleError(f.Close())
	}()
	if errors.Is(err, os.ErrNotExist) {
		klog.V(4).Info("no bootstrap file present, skipping bootstrapping")
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	contents, err := ioutil.ReadAll(f)
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

		data, err := client.Apply.Data(objectDef)
		if err != nil {
			return hash, err
		}

		_, err = dclient.
			Resource(gvr).
			Namespace(objectDef.GetNamespace()).
			Patch(ctx, objectDef.GetName(), types.ApplyPatchType, data, metadata.PatchForceOwned)
		if err != nil {
			return hash, err
		}
	}

	return hash, nil
}
