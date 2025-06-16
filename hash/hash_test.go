package hash

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
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
