package typed

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/dynamic/fake"
)

func ExampleIndexer() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	informerFactory := dynamicinformer.NewDynamicSharedInformerFactory(client, 0)
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	indexer := NewIndexer[*corev1.Secret](informerFactory.ForResource(corev1.SchemeGroupVersion.WithResource("secrets")).Informer().GetIndexer())

	secrets, _ := indexer.ByIndex("indexName", "indexValue")
	fmt.Printf("%T", secrets)
	// Output: []*v1.Secret
}

func TestGetByKeyErrorHandling(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	informerFactory := dynamicinformer.NewDynamicSharedInformerFactory(client, 0)
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	indexer := NewIndexer[*corev1.Secret](informerFactory.ForResource(corev1.SchemeGroupVersion.WithResource("secrets")).Informer().GetIndexer())

	secret, exists, err := indexer.GetByKey("nonexistent/key")
	if secret != nil {
		t.Errorf("Expected nil secret for non-existent key, got %v", secret)
	}
	if exists {
		t.Errorf("Expected exists to be false for non-existent key")
	}
	if err != nil {
		t.Errorf("Expected no error for non-existent key, got %v", err)
	}
}
