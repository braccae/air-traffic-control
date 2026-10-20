package nodeapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

// Object is an ordered JSON object that preserves key insertion order
// across unmarshaling and marshaling, maintaining byte-level fidelity
// with Express / JavaScript objects.
type Object struct {
	keys []string
	vals map[string]json.RawMessage
}

// NewObject initializes an empty ordered Object.
func NewObject() *Object {
	return &Object{
		keys: make([]string, 0),
		vals: make(map[string]json.RawMessage),
	}
}

// ParseObject decodes a JSON object byte slice while preserving key order.
func ParseObject(b []byte) (*Object, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok || delim != '{' {
		return nil, errors.New("expected JSON object '{'")
	}

	obj := NewObject()
	for dec.More() {
		kTok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := kTok.(string)
		if !ok {
			return nil, errors.New("expected string key")
		}
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			return nil, err
		}
		obj.Set(key, raw)
	}

	endTok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if endDelim, ok := endTok.(json.Delim); !ok || endDelim != '}' {
		return nil, errors.New("expected closing '}'")
	}

	return obj, nil
}

// Get returns the raw JSON value for key and whether the key exists.
func (o *Object) Get(key string) (json.RawMessage, bool) {
	if o == nil || o.vals == nil {
		return nil, false
	}
	raw, ok := o.vals[key]
	return raw, ok
}

// GetString returns the string value of key. It returns "" for JSON null,
// non-string JSON types, or when the key is absent. The boolean reports
// whether the key was present in the object.
func (o *Object) GetString(key string) (string, bool) {
	if o == nil || o.vals == nil {
		return "", false
	}
	raw, ok := o.vals[key]
	if !ok {
		return "", false
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "", true
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", true
	}
	return s, true
}

// Has reports whether key exists in the object.
func (o *Object) Has(key string) bool {
	if o == nil || o.vals == nil {
		return false
	}
	_, ok := o.vals[key]
	return ok
}

// Set stores raw value for key. It appends the key if new or overwrites
// in place if the key already exists.
func (o *Object) Set(key string, raw json.RawMessage) {
	if o.vals == nil {
		o.vals = make(map[string]json.RawMessage)
	}
	if _, ok := o.vals[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.vals[key] = raw
}

// SetJSON marshals v and stores it at key.
func (o *Object) SetJSON(key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	o.Set(key, json.RawMessage(b))
	return nil
}

// SetString marshals v as a JSON string and stores it at key.
func (o *Object) SetString(key, v string) {
	b, _ := json.Marshal(v)
	o.Set(key, json.RawMessage(b))
}

// Delete removes key from the object and updates key ordering.
func (o *Object) Delete(key string) {
	if o == nil || o.vals == nil {
		return
	}
	if _, ok := o.vals[key]; !ok {
		return
	}
	delete(o.vals, key)
	for i, k := range o.keys {
		if k == key {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
}

// Keys returns a copy of the keys in stored order.
func (o *Object) Keys() []string {
	if o == nil || len(o.keys) == 0 {
		return nil
	}
	res := make([]string, len(o.keys))
	copy(res, o.keys)
	return res
}

// MarshalJSON encodes the object, emitting keys in stored order.
func (o *Object) MarshalJSON() ([]byte, error) {
	if o == nil {
		return []byte("null"), nil
	}
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, err := json.Marshal(k)
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		val, ok := o.vals[k]
		if !ok || len(val) == 0 {
			buf.WriteString("null")
		} else {
			buf.Write(val)
		}
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// UnmarshalJSON implements json.Unmarshaler.
func (o *Object) UnmarshalJSON(b []byte) error {
	parsed, err := ParseObject(b)
	if err != nil {
		return err
	}
	o.keys = parsed.keys
	o.vals = parsed.vals
	return nil
}

// Clone creates a shallow clone of the object with copied key order.
func (o *Object) Clone() *Object {
	c := NewObject()
	if o == nil {
		return c
	}
	for _, k := range o.keys {
		raw := make(json.RawMessage, len(o.vals[k]))
		copy(raw, o.vals[k])
		c.Set(k, raw)
	}
	return c
}

// parsePositiveInt extracts a positive integer from raw JSON number or string.
func parsePositiveInt(raw json.RawMessage) int {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var n int
	if err := json.Unmarshal(raw, &n); err == nil && n > 0 {
		return n
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if parsed, err := strconv.Atoi(strings.TrimSpace(s)); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

// isTruthy evaluates JavaScript-style truthiness of a raw JSON payload.
func isTruthy(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return false
	}
	s := strings.TrimSpace(string(raw))
	if s == "" || s == "null" || s == "false" || s == "0" || s == `""` {
		return false
	}
	return true
}
