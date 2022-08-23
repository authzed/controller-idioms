package libctrl

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	applycorev1 "k8s.io/client-go/applyconfigurations/core/v1"

	"github.com/authzed/ktrllib/handler"
)

type Annotator[T any] interface {
	WithAnnotations(entries map[string]string) T
}

type EnsureComponentByHash[K KubeObject, A Annotator[A]] struct {
	*HashableComponent[K]
	ctrls        *ContextKey[ControlAll]
	nn           MustValueContext[types.NamespacedName]
	applyObject  func(ctx context.Context, apply A) (K, error)
	deleteObject func(ctx context.Context, nn types.NamespacedName) error
	newObj       func(ctx context.Context) A
}

var _ handler.ContextHandler = &EnsureComponentByHash[*corev1.Service, *applycorev1.ServiceApplyConfiguration]{}

func NewEnsureComponentByHash[K KubeObject, A Annotator[A]](
	component *HashableComponent[K],
	owner MustValueContext[types.NamespacedName],
	ctrls *ContextKey[ControlAll],
	applyObj func(ctx context.Context, apply A) (K, error),
	deleteObject func(ctx context.Context, nn types.NamespacedName) error,
	newObj func(ctx context.Context) A,
) *EnsureComponentByHash[K, A] {
	return &EnsureComponentByHash[K, A]{
		ctrls:             ctrls,
		HashableComponent: component,
		nn:                owner,
		applyObject:       applyObj,
		deleteObject:      deleteObject,
		newObj:            newObj,
	}
}

func (e *EnsureComponentByHash[K, A]) Handle(ctx context.Context) {
	ownedObjs := e.List(ctx, e.nn.MustValue(ctx))

	newObj := e.newObj(ctx)
	hash, err := e.Hash(newObj)
	if err != nil {
		e.ctrls.MustValue(ctx).RequeueErr(err)
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
		// apply if no matching KubeObject in cluster
		_, err = e.applyObject(ctx, newObj)
		if err != nil {
			e.ctrls.MustValue(ctx).RequeueErr(err)
			return
		}
	}

	if len(matchingObjs) == 1 {
		// delete extra objects
		for _, o := range extraObjs {
			if err := e.deleteObject(ctx, types.NamespacedName{
				Namespace: o.GetNamespace(),
				Name:      o.GetName(),
			}); err != nil {
				e.ctrls.MustValue(ctx).RequeueErr(err)
				return
			}
		}
	}
}
