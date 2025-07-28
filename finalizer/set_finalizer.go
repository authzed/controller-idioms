package finalizer

import (
	"context"

	"golang.org/x/exp/slices"
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
	AddFinalizer             AddFunc[K]
	RequeueAPIErr            func(context.Context, error)

	Next handler.Handler
}

func (s *SetFinalizerHandler[K]) Handle(ctx context.Context) {
	obj := s.FinalizeableObjectCtxKey.MustValue(ctx)
	if !slices.Contains(obj.GetFinalizers(), s.Finalizer) && obj.GetDeletionTimestamp() == nil {
		db, err := s.AddFinalizer(ctx, types.NamespacedName{Name: obj.GetName(), Namespace: obj.GetNamespace()})
		if err != nil {
			s.RequeueAPIErr(ctx, err)
			return
		}
		ctx = s.FinalizeableObjectCtxKey.WithValue(ctx, db)
	}
	s.Next.Handle(ctx)
}
