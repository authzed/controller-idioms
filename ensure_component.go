package libctrl

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"

	"github.com/authzed/ktrllib/handler"
)

type Annotator[T any] interface {
	WithAnnotations(entries map[string]string) T
}

type EnsureComponentByHash[K metav1.Object, A Annotator[A]] struct {
	ControlRequeueErr
	*HashableComponent[K]
	nn           types.NamespacedName
	applyObject  func(ctx context.Context, apply A) (K, error)
	deleteObject func(ctx context.Context, name string) error
	newObj       func(ctx context.Context) A
}

var _ handler.ContextHandler = &EnsureComponentByHash[*corev1.Service, *applycorev1.ServiceApplyConfiguration]{}

func NewEnsureComponentByHash[K metav1.Object, A Annotator[A]](
	component *HashableComponent[K],
	owner types.NamespacedName,
	ctrls ControlRequeueErr,
	applyObj func(ctx context.Context, apply A) (K, error),
	deleteObject func(ctx context.Context, name string) error,
	newObj func(ctx context.Context) A,
) *EnsureComponentByHash[K, A] {
	return &EnsureComponentByHash[K, A]{
		ControlRequeueErr: ctrls,
		HashableComponent: component,
		nn:                owner,
		applyObject:       applyObj,
		deleteObject:      deleteObject,
		newObj:            newObj,
	}
}

func (e *EnsureComponentByHash[K, A]) Handle(ctx context.Context) {
	ownedObjs := e.List(e.nn)

	newObj := e.newObj(ctx)
	hash, err := e.Hash(newObj)
	if err != nil {
		e.RequeueErr(err)
		return
	}
	newObj = newObj.WithAnnotations(map[string]string{e.HashAnnotationKey: hash})

	matchingObjs := make([]K, 0)
	extraObjs := make([]K, 0)
	for _, o := range ownedObjs {
		annotations := o.GetAnnotations()
		if annotations == nil {
			extraObjs = append(extraObjs, o)
		}
		if e.Equal(annotations[e.HashAnnotationKey], hash) {
			matchingObjs = append(matchingObjs, o)
		} else {
			extraObjs = append(extraObjs, o)
		}
	}

	if len(matchingObjs) == 0 {
		// apply if no matching object in cluster
		_, err = e.applyObject(ctx, newObj)
		if err != nil {
			e.RequeueErr(err)
			return
		}
	}

	if len(matchingObjs) == 1 {
		// delete extra objects
		for _, o := range extraObjs {
			if err := e.deleteObject(ctx, o.GetName()); err != nil {
				e.RequeueErr(err)
				return
			}
		}
	}
}
