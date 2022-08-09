package bootstrap

import (
	"io/fs"
	"path"
	"time"

	v1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

const (
	lookaheadBytes         = 100
	maxCRDInstallTime      = 5 * time.Minute
	crdInstallPollInterval = 200 * time.Millisecond
)

// CRD installs the CRDs in the filesystem into the kube cluster configured by the rest config.
func CRD(restConfig *rest.Config, crdFS fs.ReadDirFS, dir string) error {
	crds := make([]*v1.CustomResourceDefinition, 0)

	crdFiles, err := crdFS.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, crdFile := range crdFiles {
		var crd v1.CustomResourceDefinition
		file, err := crdFS.Open(path.Join(dir, crdFile.Name()))
		if err != nil {
			return err
		}
		if err := yaml.NewYAMLOrJSONDecoder(file, lookaheadBytes).Decode(&crd); err != nil {
			return err
		}
		crds = append(crds, &crd)
	}

	_, err = envtest.InstallCRDs(restConfig, envtest.CRDInstallOptions{
		CRDs:            crds,
		MaxTime:         maxCRDInstallTime,
		PollInterval:    crdInstallPollInterval,
		CleanUpAfterUse: false,
	})
	return err
}
