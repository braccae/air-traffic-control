package nodeapi

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestObject_PreservesKeyOrder(t *testing.T) {
	input := `{"z":1,"a":"hello","m":[1,2,3],"b":true}`
	obj, err := ParseObject([]byte(input))
	if err != nil {
		t.Fatalf("ParseObject failed: %v", err)
	}

	expectedKeys := []string{"z", "a", "m", "b"}
	if !reflect.DeepEqual(obj.Keys(), expectedKeys) {
		t.Fatalf("expected keys %v, got %v", expectedKeys, obj.Keys())
	}

	out, err := obj.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}

	if string(out) != input {
		t.Fatalf("expected marshaled output %q, got %q", input, string(out))
	}
}

func TestObject_OverwriteKeepsOriginalPosition(t *testing.T) {
	obj := NewObject()
	obj.SetString("first", "1")
	obj.SetString("second", "2")
	obj.SetString("third", "3")

	// Overwrite 'second'
	obj.SetString("second", "updated")

	expectedKeys := []string{"first", "second", "third"}
	if !reflect.DeepEqual(obj.Keys(), expectedKeys) {
		t.Fatalf("expected keys %v, got %v", expectedKeys, obj.Keys())
	}

	val, ok := obj.GetString("second")
	if !ok || val != "updated" {
		t.Fatalf("expected 'updated', got %q", val)
	}
}

func TestObject_Delete(t *testing.T) {
	obj := NewObject()
	obj.SetString("a", "1")
	obj.SetString("b", "2")
	obj.SetString("c", "3")

	obj.Delete("b")
	expectedKeys := []string{"a", "c"}
	if !reflect.DeepEqual(obj.Keys(), expectedKeys) {
		t.Fatalf("expected keys %v after delete, got %v", expectedKeys, obj.Keys())
	}

	if obj.Has("b") {
		t.Fatalf("expected obj.Has('b') to be false")
	}

	// Deleting non-existent key is a no-op
	obj.Delete("non-existent")
	if !reflect.DeepEqual(obj.Keys(), expectedKeys) {
		t.Fatalf("expected keys unchanged, got %v", obj.Keys())
	}
}

func TestObject_EmptyObjectHasNoKeys(t *testing.T) {
	obj, err := ParseObject([]byte("{}"))
	if err != nil {
		t.Fatalf("ParseObject({}) failed: %v", err)
	}

	if len(obj.Keys()) != 0 {
		t.Fatalf("expected 0 keys, got %v", obj.Keys())
	}

	out, err := obj.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON failed: %v", err)
	}
	if string(out) != "{}" {
		t.Fatalf("expected '{}', got %q", string(out))
	}
}

func TestObject_GetString(t *testing.T) {
	input := `{"str":"val","nullVal":null,"num":42,"bool":false}`
	obj, err := ParseObject([]byte(input))
	if err != nil {
		t.Fatalf("ParseObject failed: %v", err)
	}

	s, ok := obj.GetString("str")
	if !ok || s != "val" {
		t.Errorf("expected ('val', true), got (%q, %v)", s, ok)
	}

	s, ok = obj.GetString("nullVal")
	if !ok || s != "" {
		t.Errorf("expected ('', true) for null, got (%q, %v)", s, ok)
	}

	s, ok = obj.GetString("num")
	if !ok || s != "" {
		t.Errorf("expected ('', true) for number, got (%q, %v)", s, ok)
	}

	s, ok = obj.GetString("absent")
	if ok || s != "" {
		t.Errorf("expected ('', false) for absent, got (%q, %v)", s, ok)
	}
}

func TestObject_ParsePositiveIntAndTruthy(t *testing.T) {
	if parsePositiveInt(json.RawMessage("22")) != 22 {
		t.Errorf("expected 22 for number 22")
	}
	if parsePositiveInt(json.RawMessage(`"2222"`)) != 2222 {
		t.Errorf("expected 2222 for string \"2222\"")
	}
	if parsePositiveInt(json.RawMessage(`"invalid"`)) != 0 {
		t.Errorf("expected 0 for non-numeric string")
	}
	if parsePositiveInt(json.RawMessage("0")) != 0 {
		t.Errorf("expected 0 for 0")
	}

	if !isTruthy(json.RawMessage("true")) {
		t.Errorf("expected true to be truthy")
	}
	if !isTruthy(json.RawMessage(`"yes"`)) {
		t.Errorf("expected \"yes\" to be truthy")
	}
	if !isTruthy(json.RawMessage("1")) {
		t.Errorf("expected 1 to be truthy")
	}
	if isTruthy(json.RawMessage("false")) {
		t.Errorf("expected false to not be truthy")
	}
	if isTruthy(json.RawMessage("null")) {
		t.Errorf("expected null to not be truthy")
	}
	if isTruthy(json.RawMessage("0")) {
		t.Errorf("expected 0 to not be truthy")
	}
	if isTruthy(json.RawMessage(`""`)) {
		t.Errorf("expected empty string to not be truthy")
	}
	if isTruthy(nil) {
		t.Errorf("expected nil to not be truthy")
	}
}
