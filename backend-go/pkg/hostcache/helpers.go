package hostcache

import (
	"database/sql"
	"encoding/json"
	"time"
)

// nowMS returns the current time in Unix milliseconds, matching the TS
// Date.now() timestamps used throughout hostCacheDb.ts.
func nowMS() int64 { return time.Now().UnixMilli() }

// orEmpty returns a copy of b, or "[]" when b is empty so NOT NULL JSON columns
// always receive a valid array literal.
func orEmpty(b json.RawMessage) []byte {
	if len(b) == 0 {
		return []byte("[]")
	}
	return b
}

// rawOrNil converts a possibly-NULL text column into json.RawMessage (nil when NULL).
func rawOrNil(ns sql.NullString) json.RawMessage {
	if !ns.Valid {
		return nil
	}
	return json.RawMessage(ns.String)
}

// boolToInt maps a bool to the 0/1 integer the has_docker column stores.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// nullable returns nil for the empty string so optional TEXT columns store NULL,
// matching how TS passes `x || null`.
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// prependJSON returns a JSON array with item prepended to the existing array in
// raw. A nil/empty raw is treated as "[]". Mirrors the TS
// `[identity, ...(existing?.customIdentities || [])]`.
func prependJSON(raw json.RawMessage, item json.RawMessage) (json.RawMessage, error) {
	var arr []json.RawMessage
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &arr); err != nil {
			arr = nil
		}
	}
	arr = append([]json.RawMessage{item}, arr...)
	return json.Marshal(arr)
}
