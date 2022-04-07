package libctrl

import (
	"crypto/sha512"
	"crypto/subtle"
	"fmt"

	"github.com/cespare/xxhash"
	"github.com/davecgh/go-spew/spew"
	"k8s.io/apimachinery/pkg/util/rand"
)

type (
	ObjectHashFunc func(obj any) (string, error)
	HashEqualFunc  func(a, b string) bool
)

type ObjectHasher interface {
	Hash(obj any) (string, error)
	Equal(a, b string) bool
}

type hasher struct {
	ObjectHashFunc
	HashEqualFunc
}

func (h *hasher) Hash(obj interface{}) (string, error) {
	return h.ObjectHashFunc(obj)
}

func (h *hasher) Equal(a, b string) bool {
	return h.HashEqualFunc(a, b)
}

func NewSecureObjectHash() ObjectHasher {
	return &hasher{
		ObjectHashFunc: SecureHashObject,
		HashEqualFunc:  SecureHashEqual,
	}
}

func NewObjectHash() ObjectHasher {
	return &hasher{
		ObjectHashFunc: HashObject,
		HashEqualFunc:  HashEqual,
	}
}

func SecureHashObject(obj interface{}) (string, error) {
	// 224 fits into an annotation value, 256 does not
	hasher := sha512.New512_224()
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

func SecureHashEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

func HashObject(obj interface{}) (string, error) {
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

func HashEqual(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
