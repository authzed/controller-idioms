package metrics

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/component-base/metrics/legacyregistry"

	"github.com/authzed/controller-idioms/conditions"
)

type MyObject struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// this implements the conditions interface for MyObject, but note that
	// this is not supported by kube codegen at the moment (don't try to use
	// this in a real controller)
	conditions.StatusWithConditions[*MyObjectStatus] `json:"-"`
}
type MyObjectStatus struct {
	ObservedGeneration          int64 `json:"observedGeneration,omitempty" protobuf:"varint,3,opt,name=observedGeneration"`
	conditions.StatusConditions `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type" protobuf:"bytes,1,rep,name=conditions"`
}

func ExampleNewConditionStatusCollector() {
	databaseMetrics := NewConditionStatusCollector[*MyObject]("my_controller", "owned_objects", "myobjecttype")
	// register a listerbuilder for the object with:
	// databaseMetrics.AddListerBuilder()
	legacyregistry.CustomMustRegister(databaseMetrics)
	// Output:
}
