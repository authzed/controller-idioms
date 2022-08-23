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

func NewSecureObjectHash() ObjectHasher {
	return &hasher{
		ObjectHashFunc: SecureObject,
		EqualFunc:      SecureEqual,
	}
}

func NewObjectHash() ObjectHasher {
	return &hasher{
		ObjectHashFunc: Object,
		EqualFunc:      Equal,
	}
}

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

func SecureEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

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

func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
