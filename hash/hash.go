// Package hash provides common utilities hashing kube objects and comparing
// hashes.
package hash

import (
	"crypto/sha512"
	"crypto/subtle"
	"fmt"

	"github.com/cespare/xxhash/v2"
	"github.com/davecgh/go-spew/spew"
	"k8s.io/apimachinery/pkg/util/rand"
)

type (
	ObjectHashFunc func(obj any) (string, error)
	EqualFunc      func(a, b string) bool
)

// ObjectHasher hashes and object and can compare hashes for equality
type ObjectHasher interface {
	Hash(obj any) (string, error)
	Equal(a, b string) bool
}

type hasher struct {
	ObjectHashFunc
	EqualFunc
}

func (h *hasher) Hash(obj interface{}) (string, error) {
	return h.ObjectHashFunc(obj)
}

func (h *hasher) Equal(a, b string) bool {
	return h.EqualFunc(a, b)
}

// NewSecureObjectHash returns a new ObjectHasher using SecureObject
func NewSecureObjectHash() ObjectHasher {
	return &hasher{
		ObjectHashFunc: SecureObject,
		EqualFunc:      SecureEqual,
	}
}

// NewObjectHash returns a new ObjectHasher using Object
func NewObjectHash() ObjectHasher {
	return &hasher{
		ObjectHashFunc: Object,
		EqualFunc:      Equal,
	}
}

// SecureObject canonicalizes the object before hashing with sha512 and then
// with xxhash
func SecureObject(obj interface{}) (string, error) {
	hasher := sha512.New512_256()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	_, err := printer.Fprintf(hasher, "%#v", obj)
	if err != nil {
		return "", err
	}
	// xxhash the sha512 hash to get a shorter value
	xxhasher := xxhash.New()
	_, err = xxhasher.Write(hasher.Sum(nil))
	if err != nil {
		return "", err
	}
	return rand.SafeEncodeString(fmt.Sprint(xxhasher.Sum(nil))), nil
}

// SecureEqual compares hashes safely
func SecureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Object canonicalizes the object before hashing with xxhash
func Object(obj interface{}) (string, error) {
	hasher := xxhash.New()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	_, err := printer.Fprintf(hasher, "%#v", obj)
	if err != nil {
		return "", err
	}
	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum(nil))), nil
}

// Equal compares hashes safely
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
