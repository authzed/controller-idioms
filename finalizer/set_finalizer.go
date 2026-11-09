package finalizer

import (
	"context"
	"slices"

	"k8s.io/apimachinery/pkg/types"

	"github.com/authzed/controller-idioms/component"
	"github.com/authzed/controller-idioms/handler"
	"github.com/authzed/controller-idioms/typedctx"
)

type ctxKey[K component.KubeObject] interface {
	typedctx.SettableContext[K]
	typedctx.MustValueContext[K]
}

type SetFinalizerHandler[K component.KubeObject] struct {
	FinalizeableObjectCtxKey ctxKey[K]
	Finalizer                string
	// AddFinalizer adds the finalizer if ApplyFinalizer is not set.
	//
	// Deprecated: Use ApplyFinalizer with Finalizer.Add.
	AddFinalizer AddFunc[K]
	// If ApplyFinalizer is set, the handler uses it instead of AddFinalizer.
	ApplyFinalizer ApplyAddFunc[K]
	RequeueAPIErr  func(context.Context, error)

	Next handler.Handler
}

func (s *SetFinalizerHandler[K]) Handle(ctx context.Context) {
	obj := s.FinalizeableObjectCtxKey.MustValue(ctx)
	if !slices.Contains(obj.GetFinalizers(), s.Finalizer) && obj.GetDeletionTimestamp() == nil {
		var db K
		var err error
		if s.ApplyFinalizer != nil {
			db, err = s.ApplyFinalizer(ctx, obj)
		} else {
			db, err = s.AddFinalizer(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()})
		}
		if err != nil {
			s.RequeueAPIErr(ctx, err)
			return
		}
		ctx = s.FinalizeableObjectCtxKey.WithValue(ctx, db)
	}
	s.Next.Handle(ctx)
}
