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
	hash, _ := Object(configmap)
	fmt.Println(Equal(hash, "n688h54h56ch64bh677h55fh648hddq"))
	// Output: true
}

func ExampleSecureObject() {
	secret := corev1.Secret{
		StringData: map[string]string{
			"some": "data",
		},
	}
	hash, _ := SecureObject(secret)
	fmt.Println(SecureEqual(hash, "n665hb8h667h68hfbhffh669h54dq"))
	// Output: true
}
