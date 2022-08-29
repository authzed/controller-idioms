package typed

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/dynamic/dynamicinformer"
	"k8s.io/client-go/dynamic/fake"
)

func ExampleLister() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client := fake.NewSimpleDynamicClient(runtime.NewScheme())
	informerFactory := dynamicinformer.NewDynamicSharedInformerFactory(client, 0)
	informerFactory.Start(ctx.Done())
	informerFactory.WaitForCacheSync(ctx.Done())

	lister := NewLister[*corev1.Secret](informerFactory.ForResource(corev1.SchemeGroupVersion.WithResource("secrets")).Lister())

	secret, _ := lister.ByNamespace("example").Get("mysecret")
	fmt.Printf("%T", secret)
	// Output: *v1.Secret
}
