// Package dbtype holds column types sqlc maps to (internal/store/sqlc.yaml).
package dbtype

import (
	"database/sql/driver"
	"fmt"
)

// JSON is a JSON data column stored as TEXT. Binding []byte would store a BLOB, which
// SQLite's JSON functions read as binary JSON (JSONB), and database/sql can't scan TEXT into
// a json.RawMessage.
type JSON []byte

func (j *JSON) Scan(v any) error {
	switch v := v.(type) {
	case nil:
		*j = nil
	case string:
		*j = JSON(v)
	case []byte:
		*j = append(JSON(nil), v...)
	default:
		return fmt.Errorf("dbtype.JSON: can't scan %T", v)
	}
	return nil
}

func (j JSON) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return string(j), nil
}

// MarshalJSON embeds the value as is, like json.RawMessage, so audit rows nest it.
func (j JSON) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}
