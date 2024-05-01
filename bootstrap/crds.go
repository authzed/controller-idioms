package bootstrap

import (
	"context"
	"fmt"
	"io/fs"
	"path"
	"time"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/util/retry"
)

const (
	lookaheadBytes         = 100
	maxCRDInstallTime      = 5 * time.Minute
	crdInstallPollInterval = 200 * time.Millisecond
)

// CRD installs the CRDs in the filesystem into the kube cluster configured by the rest config.
// Deprecated: Use CRDs instead.
func CRD(restConfig *rest.Config, crdFS fs.ReadDirFS, dir string) error {
	return CRDs(context.Background(), restConfig, crdFS, dir)
}

// CRDs installs the CRDs in the filesystem into the kube cluster configured by the rest config.
func CRDs(ctx context.Context, restConfig *rest.Config, crdFS fs.ReadDirFS, dir string) error {
	crds := make([]*apiextensionsv1.CustomResourceDefinition, 0)

	crdFiles, err := crdFS.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, crdFile := range crdFiles {
		var crd apiextensionsv1.CustomResourceDefinition
		file, err := crdFS.Open(path.Join(dir, crdFile.Name()))
		if err != nil {
			return err
		}
		if err := yaml.NewYAMLOrJSONDecoder(file, lookaheadBytes).Decode(&crd); err != nil {
			return err
		}
		crds = append(crds, &crd)
	}

	if err := createCRDs(ctx, restConfig, crds); err != nil {
		return err
	}

	if err := waitForDiscovery(ctx, restConfig, crds); err != nil {
		return err
	}

	return err
}

// createCRDs creates (or updates) CRDs in the cluster
func createCRDs(ctx context.Context, config *rest.Config, crds []*apiextensionsv1.CustomResourceDefinition) error {
	c, err := clientset.NewForConfig(config)
	if err != nil {
		return err
	}
	crdClient := c.ApiextensionsV1().CustomResourceDefinitions()
	for _, crd := range crds {
		crd := crd
		_, err = crdClient.Get(ctx, crd.GetName(), metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			if _, err := crdClient.Create(ctx, crd, metav1.CreateOptions{}); err != nil {
				return fmt.Errorf("unable to create CRD %q: %w", crd.Name, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("failed when fetching CRD %q: %w", crd.Name, err)
		}

		if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
			got, err := crdClient.Get(ctx, crd.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			crd.SetResourceVersion(got.GetResourceVersion())
			_, err = crdClient.Update(ctx, crd, metav1.UpdateOptions{})
			return err
		}); err != nil {
			return err
		}
	}
	return nil
}

func waitForDiscovery(ctx context.Context, config *rest.Config, crds []*apiextensionsv1.CustomResourceDefinition) error {
	resourcesByGV := make(map[string]map[string]struct{}, 0)
	for _, crd := range crds {
		for _, version := range crd.Spec.Versions {
			if !version.Served {
				continue
			}
			gv := schema.GroupVersion{Version: version.Name, Group: crd.Spec.Group}.String()
			_, ok := resourcesByGV[gv]
			if !ok {
				resourcesByGV[gv] = make(map[string]struct{}, 0)
			}
			resourcesByGV[gv][crd.Spec.Names.Plural] = struct{}{}
		}
	}
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return err
	}

	return wait.PollUntilContextTimeout(ctx, crdInstallPollInterval, maxCRDInstallTime, true, func(_ context.Context) (done bool, err error) {
		_, serverGVRs, err := discoveryClient.ServerGroupsAndResources()
		if err != nil {
			return false, nil
		}

		for _, gv := range serverGVRs {
			if _, ok := resourcesByGV[gv.GroupVersion]; !ok {
				continue
			}
			for _, r := range gv.APIResources {
				delete(resourcesByGV[gv.GroupVersion], r.Name)
			}
			if len(resourcesByGV[gv.GroupVersion]) == 0 {
				delete(resourcesByGV, gv.GroupVersion)
			}
		}

		return len(resourcesByGV) == 0, nil
	})
}
