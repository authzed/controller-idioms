package hash

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func ExampleObject() {
	configmap := corev1.ConfigMap{
		Data: map[string]string{
			"some": "data",
		},
	}
	hash := Object(configmap)
	fmt.Println(Equal(hash, "f40a7fcee977cc58"))
	// Output: true
}

func ExampleSecureObject() {
	secret := corev1.Secret{
		StringData: map[string]string{
			"some": "data",
		},
	}
	hash := SecureObject(secret)
	fmt.Println(SecureEqual(hash, "dd40df186063e16c"))
	// Output: true
}

// TestHashTimeLocalWhileZoneLoads hashes a time in time.Local while another
// goroutine makes the first call that loads time.Local's zone. Under -race,
// this fails if the hash reads time.Local before the zone is loaded.
func TestHashTimeLocalWhileZoneLoads(t *testing.T) {
	t.Parallel()

	obj := metav1.ObjectMeta{CreationTimestamp: metav1.Unix(0, 0)}

	var during string
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = time.Unix(0, 0).String()
	}()
	go func() {
		defer wg.Done()
		during = Object(obj)
	}()
	wg.Wait()

	require.Equal(t, during, Object(obj))
}
