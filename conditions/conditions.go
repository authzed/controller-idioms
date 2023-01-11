// Package conditions is a mix-in for custom resource types that adds in
// standard condition accessors.
// WARNING: this cannot be used with standard kube-ecosystem codegen tooling,
// which does not yet understand generics. It is mostly useful for tests.
package conditions

import (
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type StatusConditions struct {
	// Conditions for the current state of the resource.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

// GetStatusConditions returns a pointer to all status conditions.
func (c *StatusConditions) GetStatusConditions() *[]metav1.Condition {
	return &c.Conditions
}

type HasConditions interface {
	comparable
	GetStatusConditions() *[]metav1.Condition
}

type StatusWithConditions[S HasConditions] struct {
	// +optional
	Status S `json:"status,omitempty"`
}

// GetStatusConditions returns all status conditions.
func (s *StatusWithConditions[S]) GetStatusConditions() *[]metav1.Condition {
	var nilS S
	if s.Status == nilS {
		return nil
	}
	return s.Status.GetStatusConditions()
}

// FindStatusCondition finds the conditionType in conditions.
func (s StatusWithConditions[S]) FindStatusCondition(conditionType string) *metav1.Condition {
	if s.GetStatusConditions() == nil {
		return nil
	}
	return meta.FindStatusCondition(*s.GetStatusConditions(), conditionType)
}

// SetStatusCondition sets the corresponding condition in conditions to newCondition.
// conditions must be non-nil.
//  1. if the condition of the specified type already exists (all fields of the existing condition are updated to
//     newCondition, LastTransitionTime is set to now if the new status differs from the old status)
//  2. if a condition of the specified type does not exist (LastTransitionTime is set to now() if unset, and newCondition is appended)
func (s StatusWithConditions[S]) SetStatusCondition(condition metav1.Condition) {
	meta.SetStatusCondition(s.Status.GetStatusConditions(), condition)
}

// RemoveStatusCondition removes the corresponding conditionType from conditions.
// conditions must be non-nil.
func (s StatusWithConditions[S]) RemoveStatusCondition(conditionType string) {
	meta.RemoveStatusCondition(s.Status.GetStatusConditions(), conditionType)
}

// IsStatusConditionTrue returns true when the conditionType is present and set to `metav1.ConditionTrue`
func (s StatusWithConditions[S]) IsStatusConditionTrue(conditionType string) bool {
	if s.GetStatusConditions() == nil {
		return false
	}
	return meta.IsStatusConditionTrue(*s.GetStatusConditions(), conditionType)
}

// IsStatusConditionFalse returns true when the conditionType is present and set to `metav1.ConditionFalse`
func (s StatusWithConditions[S]) IsStatusConditionFalse(conditionType string) bool {
	if s.GetStatusConditions() == nil {
		return false
	}
	return meta.IsStatusConditionFalse(*s.GetStatusConditions(), conditionType)
}

// IsStatusConditionPresentAndEqual returns true when conditionType is present and equal to status.
func (s StatusWithConditions[S]) IsStatusConditionPresentAndEqual(conditionType string, status metav1.ConditionStatus) bool {
	if s.GetStatusConditions() == nil {
		return false
	}
	return meta.IsStatusConditionPresentAndEqual(*s.GetStatusConditions(), conditionType, status)
}

// IsStatusConditionChanged returns true if the passed in condition is different
// from the condition of the same type.
func (s StatusWithConditions[S]) IsStatusConditionChanged(conditionType string, condition *metav1.Condition) bool {
	existing := s.FindStatusCondition(conditionType)
	if existing == nil && condition == nil {
		return false
	}
	// if only one is nil, the condition has changed
	if (existing == nil && condition != nil) || (existing != nil && condition == nil) {
		return true
	}
	// if not nil, changed if the message or reason is different
	return existing.Message != condition.Message || existing.Reason != condition.Reason
}
