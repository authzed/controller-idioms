package pause

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	"github.com/authzed/ktrllib/handler"
	"github.com/authzed/ktrllib/queue"
	"github.com/authzed/ktrllib/typedctx"
)

const ConditionTypePaused = "Paused"

// HasStatusConditions is an interface that any object implementing standard
// condition accessors will satisfy.
type HasStatusConditions interface {
	comparable
	metav1.Object
	FindStatusCondition(conditionType string) *metav1.Condition
	SetStatusCondition(metav1.Condition)
	GetStatusConditions() []metav1.Condition
}

func IsPaused(object metav1.Object, pausedLabelKey string) bool {
	objLabels := object.GetLabels()
	if objLabels == nil {
		return false
	}

	_, ok := objLabels[pausedLabelKey]
	return ok
}

type Handler[K HasStatusConditions] struct {
	ctrls          *typedctx.Key[queue.Interface]
	PausedLabelKey string
	Object         *typedctx.DefaultingKey[K]
	PatchStatus    func(ctx context.Context, patch K) error
	Next           handler.ContextHandler
}

func NewPauseContextHandler[K HasStatusConditions](ctrls *typedctx.Key[queue.Interface],
	pausedLabelKey string,
	object *typedctx.DefaultingKey[K],
	patchStatus func(ctx context.Context, patch K) error,
	next handler.ContextHandler,
) *Handler[K] {
	return &Handler[K]{
		ctrls:          ctrls,
		PausedLabelKey: pausedLabelKey,
		Object:         object,
		PatchStatus:    patchStatus,
		Next:           next,
	}
}

func (p *Handler[K]) pause(ctx context.Context, object K) {
	if object.FindStatusCondition(ConditionTypePaused) != nil {
		p.ctrls.MustValue(ctx).Done()
		return
	}
	object.SetStatusCondition(NewPausedCondition(p.PausedLabelKey))
	object.SetManagedFields(nil)
	if err := p.PatchStatus(ctx, object); err != nil {
		p.ctrls.MustValue(ctx).RequeueErr(err)
		return
	}
	p.ctrls.MustValue(ctx).Done()
}

func (p *Handler[K]) Handle(ctx context.Context) {
	if obj := p.Object.MustValue(ctx); IsPaused(obj, p.PausedLabelKey) {
		p.pause(ctx, obj)
		return
	}
	p.Next.Handle(ctx)
}

// SelfPauseHandler is used when the controller pauses itself. This is only
// used when the controller has no good way to tell when the bad state has
// been resolved (i.e. an external resource is behaving poorly).
type SelfPauseHandler[K HasStatusConditions] struct {
	ctrls          *typedctx.Key[queue.Interface]
	CtxKey         *typedctx.DefaultingKey[K]
	PausedLabelKey string
	OwnerUID       types.UID
	Patch          func(ctx context.Context, patch K) error
	PatchStatus    func(ctx context.Context, patch K) error
}

func NewSelfPauseHandler[K HasStatusConditions](ctrls *typedctx.Key[queue.Interface],
	pausedLabelKey string,
	contextKey *typedctx.DefaultingKey[K],
	patch, patchStatus func(ctx context.Context, patch K) error,
) *SelfPauseHandler[K] {
	return &SelfPauseHandler[K]{
		CtxKey:         contextKey,
		ctrls:          ctrls,
		PausedLabelKey: pausedLabelKey,
		Patch:          patch,
		PatchStatus:    patchStatus,
	}
}

func (p *SelfPauseHandler[K]) Handle(ctx context.Context) {
	object := p.CtxKey.MustValue(ctx)
	object.SetStatusCondition(NewSelfPausedCondition(p.PausedLabelKey))
	if err := p.PatchStatus(ctx, object); err != nil {
		p.ctrls.MustValue(ctx).RequeueErr(err)
		return
	}
	labels := object.GetLabels()
	if labels == nil {
		labels = make(map[string]string)
	}
	labels[p.PausedLabelKey] = string(p.OwnerUID)
	object.SetLabels(labels)
	if err := p.Patch(ctx, object); err != nil {
		utilruntime.HandleError(err)
		p.ctrls.MustValue(ctx).Requeue()
		return
	}
	p.ctrls.MustValue(ctx).Done()
}

func NewPausedCondition(pausedLabelKey string) metav1.Condition {
	return metav1.Condition{
		Type:               ConditionTypePaused,
		Status:             metav1.ConditionTrue,
		Reason:             "PausedByLabel",
		LastTransitionTime: metav1.NewTime(time.Now()),
		Message:            fmt.Sprintf("Controller pause requested via label: %s", pausedLabelKey),
	}
}

func NewSelfPausedCondition(pausedLabelKey string) metav1.Condition {
	return metav1.Condition{
		Type:               ConditionTypePaused,
		Status:             metav1.ConditionTrue,
		Reason:             "PausedByController",
		LastTransitionTime: metav1.NewTime(time.Now()),
		Message:            fmt.Sprintf("Reconiciliation has been paused by the controller; see other conditions for more information. When ready, unpause by removing the %s label", pausedLabelKey),
	}
}
