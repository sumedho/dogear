package sqlutil

import "database/sql"

// Int64Ptr converts a nullable SQL integer to its pointer representation.
func Int64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

// Int64Value converts a nullable SQL integer to a database driver value.
func Int64Value(value sql.NullInt64) any {
	if !value.Valid {
		return nil
	}
	return value.Int64
}
