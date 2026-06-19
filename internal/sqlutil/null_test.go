package sqlutil

import (
	"database/sql"
	"testing"
)

func TestInt64Ptr(t *testing.T) {
	if got := Int64Ptr(sql.NullInt64{}); got != nil {
		t.Fatalf("Int64Ptr(null) = %v, want nil", got)
	}
	got := Int64Ptr(sql.NullInt64{Int64: 42, Valid: true})
	if got == nil || *got != 42 {
		t.Fatalf("Int64Ptr(42) = %v, want pointer to 42", got)
	}
}

func TestInt64Value(t *testing.T) {
	if got := Int64Value(sql.NullInt64{}); got != nil {
		t.Fatalf("Int64Value(null) = %v, want nil", got)
	}
	if got := Int64Value(sql.NullInt64{Int64: 42, Valid: true}); got != int64(42) {
		t.Fatalf("Int64Value(42) = %v, want int64(42)", got)
	}
}
