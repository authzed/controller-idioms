// Package cachekeys provides standard utilites for creating and parsing cache
// keys for use in client-go caches and workqueus.
//
// These allow one to build queues that process multiple types of objects by
// annotating the standard namespace/name keys with the GVR.
package cachekeys

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/tools/cache"
)

// GVRMetaNamespaceKeyer creates a cache/queue key from a gvr and an object key
func GVRMetaNamespaceKeyer(gvr schema.GroupVersionResource, key string) string {
	return fmt.Sprintf("%s.%s.%s::%s", gvr.Resource, gvr.Version, gvr.Group, key)
}

// GVRMetaNamespaceKeyFunc creates cache/queue key from a gvr and an object.
func GVRMetaNamespaceKeyFunc(gvr schema.GroupVersionResource, obj interface{}) (string, error) {
	if d, ok := obj.(cache.DeletedFinalStateUnknown); ok {
		return d.Key, nil
	}
	key, err := cache.MetaNamespaceKeyFunc(obj)
	if err != nil {
		return "", err
	}
	return GVRMetaNamespaceKeyer(gvr, key), nil
}

// SplitGVRMetaNamespaceKey splits a cache key into gvr, namespace, and name.
func SplitGVRMetaNamespaceKey(key string) (gvr *schema.GroupVersionResource, namespace, name string, err error) {
	before, after, ok := strings.Cut(key, "::")
	if !ok {
		err = fmt.Errorf("error parsing key: %s", key)
		return
	}
	gvr, _ = schema.ParseResourceArg(before)
	if gvr == nil {
		err = fmt.Errorf("error parsing gvr from key: %s", before)
		return
	}
	namespace, name, err = cache.SplitMetaNamespaceKey(after)
	return
}
