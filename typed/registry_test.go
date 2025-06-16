package typed

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"
)

func ExampleRegistry() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	secretGVR := corev1.SchemeGroupVersion.WithResource("secrets")
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: "example",
		Name:      "mysecret",
		Labels: map[string]string{
			"my-controller.com/related-to": "myobjecttype",
		},
	}}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	client := fake.NewSimpleDynamicClient(scheme, &secret)
	registry := NewRegistry()

	dependentObjectKey := NewFactoryKey("my-controller", "localCluster", "dependentObjects")
	informerFactory := registry.MustNewFilteredDynamicSharedInformerFactory(
		dependentObjectKey,
		client,
		0,
		metav1.NamespaceAll,
		func(options *metav1.ListOptions) {
			options.LabelSelector = "my-controller.com/related-to=myobjecttype"
		},
	)
	informerFactory.ForResource(secretGVR)
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	dependentSecretKey := NewRegistryKey(dependentObjectKey, secretGVR)

	// the registry can be passed around, and other controllers can get direct
	// access to the cache's contents
	anotherController := func(r *Registry) {
		cachedSecret, _ := r.ListerFor(dependentSecretKey).ByNamespace("example").Get("mysecret")
		fmt.Printf("%s/%s", cachedSecret.(metav1.Object).GetNamespace(), cachedSecret.(metav1.Object).GetName())
	}
	anotherController(registry)
	// Output: example/mysecret
}

func ExampleListerFor() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	secretGVR := corev1.SchemeGroupVersion.WithResource("secrets")
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: "example",
		Name:      "mysecret",
		Labels: map[string]string{
			"my-controller.com/related-to": "myobjecttype",
		},
	}}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	client := fake.NewSimpleDynamicClient(scheme, &secret)
	registry := NewRegistry()

	dependentObjectKey := NewFactoryKey("my-controller", "localCluster", "dependentObjects")
	informerFactory := registry.MustNewFilteredDynamicSharedInformerFactory(
		dependentObjectKey,
		client,
		0,
		metav1.NamespaceAll,
		func(options *metav1.ListOptions) {
			options.LabelSelector = "my-controller.com/related-to=myobjecttype"
		},
	)
	informerFactory.ForResource(secretGVR)
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	dependentSecretKey := NewRegistryKey(dependentObjectKey, secretGVR)

	cachedSecret, _ := MustListerForKey[*corev1.Secret](registry, dependentSecretKey).ByNamespace("example").Get("mysecret")
	fmt.Printf("%T %s/%s", cachedSecret, cachedSecret.GetNamespace(), cachedSecret.GetName())
	// Output: *v1.Secret example/mysecret
}

func ExampleIndexerFor() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	secretGVR := corev1.SchemeGroupVersion.WithResource("secrets")
	secret := corev1.Secret{ObjectMeta: metav1.ObjectMeta{
		Namespace: "example",
		Name:      "mysecret",
		Labels: map[string]string{
			"my-controller.com/related-to": "myobjecttype",
		},
	}}
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	client := fake.NewSimpleDynamicClient(scheme, &secret)
	registry := NewRegistry()

	dependentObjectKey := NewFactoryKey("my-controller", "localCluster", "dependentObjects")
	informerFactory := registry.MustNewFilteredDynamicSharedInformerFactory(
		dependentObjectKey,
		client,
		0,
		metav1.NamespaceAll,
		func(options *metav1.ListOptions) {
			options.LabelSelector = "my-controller.com/related-to=myobjecttype"
		},
	)

	// add an index that indexes all objects with a constant value
	const indexName = "ExampleIndex"
	const constantIndexValue = "indexVal"
	if err := informerFactory.ForResource(secretGVR).Informer().AddIndexers(map[string]cache.IndexFunc{
		indexName: func(_ interface{}) ([]string, error) {
			return []string{constantIndexValue}, nil
		},
	}); err != nil {
		panic(err)
	}

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	dependentSecretKey := NewRegistryKey(dependentObjectKey, secretGVR)

	matchingCachedSecrets, _ := MustIndexerForKey[*corev1.Secret](registry, dependentSecretKey).ByIndex(indexName, constantIndexValue)
	fmt.Printf("%T %s/%s", matchingCachedSecrets, matchingCachedSecrets[0].GetNamespace(), matchingCachedSecrets[0].GetName())
	// Output: []*v1.Secret example/mysecret
}

func TestRemove(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	secretGVR := corev1.SchemeGroupVersion.WithResource("secrets")
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	client := fake.NewSimpleDynamicClient(scheme)
	registry := NewRegistry()

	dependentObjectKey := NewFactoryKey("my-controller", "localCluster", "dependentObjects")
	informerFactory := registry.MustNewFilteredDynamicSharedInformerFactory(
		dependentObjectKey,
		client,
		0,
		metav1.NamespaceAll,
		func(options *metav1.ListOptions) {
			options.LabelSelector = "my-controller.com/related-to=myobjecttype"
		},
	)
	informerFactory.ForResource(secretGVR)
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	registry.Remove(dependentObjectKey)
}

func TestForKey(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	registry := NewRegistry()

	secretGVR := corev1.SchemeGroupVersion.WithResource("secrets")
	scheme := runtime.NewScheme()
	if err := corev1.AddToScheme(scheme); err != nil {
		panic(err)
	}
	client := fake.NewSimpleDynamicClient(scheme)
	dependentObjectKey := NewFactoryKey("my-controller", "localCluster", "dependentObjects")
	informerFactory := registry.MustNewFilteredDynamicSharedInformerFactory(
		dependentObjectKey,
		client,
		0,
		metav1.NamespaceAll,
		func(options *metav1.ListOptions) {
			options.LabelSelector = "my-controller.com/related-to=myobjecttype"
		},
	)
	informerFactory.ForResource(secretGVR)
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	// good key works
	dependentSecretKey := NewRegistryKey(dependentObjectKey, secretGVR)
	require.NotPanics(t, func() {
		registry.MustInformerFactoryForKey(dependentSecretKey)
		registry.MustListerForKey(dependentSecretKey)
		registry.MustIndexerForKey(dependentSecretKey)
		MustListerForKey[*corev1.Secret](registry, dependentSecretKey)
		MustIndexerForKey[*corev1.Secret](registry, dependentSecretKey)
	})

	// bad key panics Must*ForKey
	badKey := NewRegistryKey(NewFactoryKey("other-controller", "othercluster", "dependentObjects"), corev1.SchemeGroupVersion.WithResource("pods"))
	require.Panics(t, func() {
		registry.MustInformerFactoryForKey(badKey)
	})
	require.Panics(t, func() {
		registry.MustListerForKey(badKey)
	})
	require.Panics(t, func() {
		registry.MustIndexerForKey(badKey)
	})
	require.Panics(t, func() {
		MustListerForKey[*corev1.Pod](registry, badKey)
	})
	require.Panics(t, func() {
		MustIndexerForKey[*corev1.Pod](registry, badKey)
	})

	// bad key returns error for *ForKey
	_, err := registry.InformerFactoryForKey(badKey)
	require.Error(t, err)
	_, err = registry.ListerForKey(badKey)
	require.Error(t, err)
	_, err = registry.IndexerForKey(badKey)
	require.Error(t, err)
	_, err = ListerForKey[*corev1.Pod](registry, badKey)
	require.Error(t, err)
	_, err = IndexerForKey[*corev1.Pod](registry, badKey)
	require.Error(t, err)
}
