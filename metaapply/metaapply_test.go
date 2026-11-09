package metaapply

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestManagerFor(t *testing.T) {
	short := "finalizer:example.com/finalizer"
	require.Equal(t, short, ManagerFor(short))

	long := "finalizer:" + strings.Repeat("a", 200)
	longer := long + "b"
	require.Len(t, ManagerFor(long), maxManagerLength)
	require.Len(t, ManagerFor(longer), maxManagerLength)
	require.NotEqual(t, ManagerFor(long), ManagerFor(longer), "purposes with a shared prefix get different names")
}

func TestCheckOwnsOnlyMetadata(t *testing.T) {
	entry := func(manager string, op metav1.ManagedFieldsOperationType, fields string) metav1.ManagedFieldsEntry {
		return metav1.ManagedFieldsEntry{Manager: manager, Operation: op, FieldsV1: metav1.NewFieldsV1(fields)}
	}
	for _, tc := range []struct {
		name    string
		entries []metav1.ManagedFieldsEntry
		wantErr bool
	}{
		{name: "no managed fields"},
		{name: "metadata only", entries: []metav1.ManagedFieldsEntry{entry("m", metav1.ManagedFieldsOperationApply, `{"f:metadata":{"f:labels":{}}}`)}},
		{name: "owns data", entries: []metav1.ManagedFieldsEntry{entry("m", metav1.ManagedFieldsOperationApply, `{"f:data":{"f:key":{}}}`)}, wantErr: true},
		// An Update entry is not changed by an apply, so it cannot lose fields.
		{name: "update entry owns data", entries: []metav1.ManagedFieldsEntry{entry("m", metav1.ManagedFieldsOperationUpdate, `{"f:data":{}}`)}},
		{name: "another manager owns data", entries: []metav1.ManagedFieldsEntry{entry("other", metav1.ManagedFieldsOperationApply, `{"f:data":{}}`)}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := checkOwnsOnlyMetadata(tc.entries, "m")
			if tc.wantErr {
				require.ErrorIs(t, err, ErrOwnsNonMetadata)
				return
			}
			require.NoError(t, err)
		})
	}
}
