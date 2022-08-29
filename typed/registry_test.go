package typed

import (
	"context"
	"fmt"

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
	corev1.AddToScheme(scheme)
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
	corev1.AddToScheme(scheme)
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

	cachedSecret, _ := ListerFor[*corev1.Secret](registry, dependentSecretKey).ByNamespace("example").Get("mysecret")
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
	corev1.AddToScheme(scheme)
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
	informerFactory.ForResource(secretGVR).Informer().AddIndexers(map[string]cache.IndexFunc{
		indexName: func(obj interface{}) ([]string, error) {
			return []string{constantIndexValue}, nil
		},
	})

	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	dependentSecretKey := NewRegistryKey(dependentObjectKey, secretGVR)

	matchingCachedSecrets, _ := IndexerFor[*corev1.Secret](registry, dependentSecretKey).ByIndex(indexName, constantIndexValue)
	fmt.Printf("%T %s/%s", matchingCachedSecrets, matchingCachedSecrets[0].GetNamespace(), matchingCachedSecrets[0].GetName())
	// Output: []*v1.Secret example/mysecret
}
