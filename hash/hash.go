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
	ObjectHashFunc func(obj any) string
	EqualFunc      func(a, b string) bool
)

// ObjectHasher hashes and object and can compare hashes for equality
type ObjectHasher interface {
	Hash(obj any) string
	Equal(a, b string) bool
}

type hasher struct {
	ObjectHashFunc
	EqualFunc
}

func (h *hasher) Hash(obj interface{}) string {
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
func SecureObject(obj interface{}) string {
	hasher := sha512.New512_256()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}
	// sha512's hasher.Write never returns an error, and Fprintf just passes up
	// the underlying Write call's error, so we can safely ignore the error here
	_, _ = printer.Fprintf(hasher, "%#v", obj)
	// xxhash the sha512 hash to get a shorter value
	xxhasher := xxhash.New()

	// xxhash's hasher.Write never returns an error, so we can safely ignore
	// the error here tpp
	_, _ = xxhasher.Write(hasher.Sum(nil))
	return rand.SafeEncodeString(fmt.Sprint(xxhasher.Sum(nil)))
}

// SecureEqual compares hashes safely
func SecureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Object canonicalizes the object before hashing with xxhash
func Object(obj interface{}) string {
	hasher := xxhash.New()
	printer := spew.ConfigState{
		Indent:         " ",
		SortKeys:       true,
		DisableMethods: true,
		SpewKeys:       true,
	}

	// xxhash's hasher.Write never returns an error, and Fprintf just passes up
	// the underlying Write call's error, so we can safely ignore the error here
	_, _ = printer.Fprintf(hasher, "%#v", obj)
	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum(nil)))
}

// Equal compares hashes safely
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
