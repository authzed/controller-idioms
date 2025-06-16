package hash

import (
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

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
	conditions.StatusConditions `json:"conditions,omitempty"         patchMergeKey:"type"                            patchStrategy:"merge" protobuf:"bytes,1,rep,name=conditions"`
}

// This test uses complex non-comparable objects to confirm the hashing behavior,
// but the rest of tests use string objects for simplicity
func TestNewSet(t *testing.T) {
	testObject := MyObject{TypeMeta: metav1.TypeMeta{Kind: "test"}}
	test2Object := MyObject{TypeMeta: metav1.TypeMeta{Kind: "test2"}}
	objectWithConditions := MyObject{StatusWithConditions: conditions.StatusWithConditions[*MyObjectStatus]{Status: &MyObjectStatus{
		StatusConditions: conditions.StatusConditions{
			Conditions: []metav1.Condition{{Type: "Condition", Message: "happened"}},
		},
	}}}
	objectWithTwoConditions := MyObject{StatusWithConditions: conditions.StatusWithConditions[*MyObjectStatus]{Status: &MyObjectStatus{
		StatusConditions: conditions.StatusConditions{
			Conditions: []metav1.Condition{
				{Type: "Condition", Message: "happened"},
				{Type: "Condition2", Message: "happened"},
			},
		},
	}}}

	type testCase[T any] struct {
		name    string
		objects []T
		want    Set[T]
	}
	tests := []testCase[MyObject]{
		{
			name: "with kube-like objects",
			objects: []MyObject{
				testObject,
				testObject,
				test2Object,
			},
			want: map[string]MyObject{
				Object(testObject):  testObject,
				Object(test2Object): test2Object,
			},
		},
		{
			name: "with equal non-comparable elements",
			objects: []MyObject{
				objectWithConditions,
				objectWithConditions,
			},
			want: map[string]MyObject{
				Object(objectWithConditions): objectWithConditions,
			},
		},
		{
			name: "with non-equal non-comparable elements",
			objects: []MyObject{
				objectWithConditions,
				objectWithTwoConditions,
			},
			want: map[string]MyObject{
				Object(objectWithConditions):    objectWithConditions,
				Object(objectWithTwoConditions): objectWithTwoConditions,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, NewSet(tt.objects...))
		})
	}
}

func TestSet_Contains(t *testing.T) {
	type testCase[T any] struct {
		name  string
		s     Set[T]
		other Set[T]
		want  bool
	}
	tests := []testCase[string]{
		{
			name:  "contains",
			s:     NewSet("a", "b", "c"),
			other: NewSet("b", "c"),
			want:  true,
		},
		{
			name:  "does not contain",
			s:     NewSet("a", "b", "c"),
			other: NewSet("b", "c", "d"),
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.s.Contains(tt.other))
		})
	}
}

func TestSet_Delete(t *testing.T) {
	type testCase[T any] struct {
		name    string
		s       Set[T]
		objects []T
		want    Set[T]
	}
	tests := []testCase[string]{
		{
			name:    "removes from set",
			s:       NewSet("a", "b", "c"),
			objects: []string{"b", "c"},
			want:    NewSet("a"),
		},
		{
			name:    "no-op",
			s:       NewSet("a", "b", "c"),
			objects: []string{"f"},
			want:    NewSet("a", "b", "c"),
		},
		{
			name:    "removes all",
			s:       NewSet("a", "b", "c"),
			objects: []string{"a", "b", "c"},
			want:    NewSet[string](),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.s.Delete(tt.objects...))
		})
	}
}

func TestSet_EqualElements(t *testing.T) {
	type testCase[T any] struct {
		name  string
		s     Set[T]
		other Set[T]
		want  bool
	}
	tests := []testCase[string]{
		{
			name:  "all elements equal",
			s:     NewSet("a", "b", "c"),
			other: NewSet("a", "b", "c"),
			want:  true,
		},
		{
			name:  "not equal",
			s:     NewSet("a", "b", "c"),
			other: NewSet("a", "c"),
			want:  false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.s.EqualElements(tt.other))
		})
	}
}

func TestSet_Has(t *testing.T) {
	type testCase[T any] struct {
		name    string
		s       Set[T]
		objects []T
		want    bool
	}
	tests := []testCase[string]{
		{
			name:    "has all objects",
			s:       NewSet("a", "b", "c"),
			objects: []string{"b", "c"},
			want:    true,
		},
		{
			name:    "doesn't have all objects",
			s:       NewSet("a", "b", "c"),
			objects: []string{"b", "d"},
			want:    false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.s.Has(tt.objects...))
		})
	}
}

func TestSet_Insert(t *testing.T) {
	type testCase[T any] struct {
		name    string
		s       Set[T]
		objects []T
		want    Set[T]
	}
	tests := []testCase[string]{
		{
			name:    "insert new",
			s:       NewSet("a", "b", "c"),
			objects: []string{"d", "e"},
			want:    NewSet("a", "b", "c", "d", "e"),
		},
		{
			name:    "insert existing",
			s:       NewSet("a", "b", "c"),
			objects: []string{"a", "b"},
			want:    NewSet("a", "b", "c"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.s.Insert(tt.objects...))
		})
	}
}

func TestSet_Intersect(t *testing.T) {
	type testCase[T any] struct {
		name  string
		s     Set[T]
		other Set[T]
		want  Set[T]
	}
	tests := []testCase[string]{
		{
			name:  "other subset of s",
			s:     NewSet("a", "b", "c"),
			other: NewSet("a", "b"),
			want:  NewSet("a", "b"),
		},
		{
			name:  "s subset of other",
			s:     NewSet("a", "b"),
			other: NewSet("a", "b", "c"),
			want:  NewSet("a", "b"),
		},
		{
			name:  "empty",
			s:     NewSet("a", "b"),
			other: NewSet("c", "d"),
			want:  NewSet[string](),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.s.Intersect(tt.other))
		})
	}
}

func TestSet_Len(t *testing.T) {
	type testCase[T any] struct {
		name string
		s    Set[T]
		want int
	}
	tests := []testCase[string]{
		{
			name: "zero",
			s:    NewSet[string](),
			want: 0,
		},
		{
			name: "nonzero",
			s:    NewSet("a", "b"),
			want: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.s.Len())
		})
	}
}

func TestSet_Pop(t *testing.T) {
	type testCase[T any] struct {
		name      string
		s         Set[T]
		want      T
		wantFound bool
	}
	tests := []testCase[string]{
		{
			name:      "returns last one",
			s:         NewSet("a"),
			want:      "a",
			wantFound: true,
		},
		{
			name:      "returns one from set",
			s:         NewSet("a", "b", "c"),
			wantFound: true,
		},
		{
			name:      "returns none when empty",
			s:         NewSet[string](),
			want:      "",
			wantFound: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			initialLen := tt.s.Len()
			got, found := tt.s.Pop()
			require.Equal(t, tt.wantFound, found)
			if initialLen < 2 {
				require.Equal(t, tt.want, got)
			}
			if initialLen > 0 {
				require.Equal(t, initialLen-1, tt.s.Len())
			}
			require.False(t, tt.s.Has(got))
		})
	}
}

func TestSet_SetDifference(t *testing.T) {
	type testCase[T any] struct {
		name  string
		s     Set[T]
		other Set[T]
		want  Set[T]
	}
	tests := []testCase[string]{
		{
			name:  "removes all",
			s:     NewSet("a", "b", "c"),
			other: NewSet("a", "b", "c"),
			want:  NewSet[string](),
		},
		{
			name:  "removes all from second set",
			s:     NewSet("a", "b", "c"),
			other: NewSet("b", "c"),
			want:  NewSet("a"),
		},
		{
			name:  "removes subset of second set",
			s:     NewSet("a", "b", "c"),
			other: NewSet("b", "d"),
			want:  NewSet("a", "c"),
		},
		{
			name:  "removes none",
			s:     NewSet("a", "b", "c"),
			other: NewSet("e", "d"),
			want:  NewSet("a", "b", "c"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.s.SetDifference(tt.other))
		})
	}
}

func TestSet_Union(t *testing.T) {
	type testCase[T any] struct {
		name  string
		s     Set[T]
		other Set[T]
		want  Set[T]
	}
	tests := []testCase[string]{
		{
			name:  "disjoint",
			s:     NewSet("a", "b"),
			other: NewSet("c", "d"),
			want:  NewSet("a", "b", "c", "d"),
		},
		{
			name:  "s subset other",
			s:     NewSet("a", "b"),
			other: NewSet("a", "b", "c"),
			want:  NewSet("a", "b", "c"),
		},
		{
			name:  "other subset s",
			s:     NewSet("a", "b", "c"),
			other: NewSet("a", "b"),
			want:  NewSet("a", "b", "c"),
		},
		{
			name:  "equal",
			s:     NewSet("a", "b"),
			other: NewSet("a", "b"),
			want:  NewSet("a", "b"),
		},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, tt.s.Union(tt.other))
	}
}
