package browser

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// playwright-go v0.6000.0 walks an Evaluate argument itself and accepts only
// JSON's shapes: map[string]any, []any, string, the numbers, bool and nil. Its
// map case has no comma-ok, so any other map panics, and a struct reaches the
// page as `undefined`.

// jsonArg round-trips an Evaluate argument through JSON. A Playwright handle
// would not survive it, and nothing passes one.
func jsonArg(arg any) (any, error) {
	if arg == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(arg)
	if err != nil {
		return nil, fmt.Errorf("browser: this cannot be handed to a page: %w", err)
	}
	var shaped any
	if err := json.Unmarshal(encoded, &shaped); err != nil {
		return nil, fmt.Errorf("browser: this cannot be handed to a page: %w", err)
	}
	return shaped, nil
}

// Serializable says whether a value is already in those shapes, naming the
// first place it is not, so tests catch a bad argument without Chromium.
func Serializable(value any) error { return serializable(value, "the argument") }

func serializable(value any, at string) error {
	if value == nil {
		return nil
	}
	switch typed := value.(type) {
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return nil
	case map[string]any:
		for key, one := range typed {
			if err := serializable(one, at+"."+key); err != nil {
				return err
			}
		}
		return nil
	case []any:
		for index, one := range typed {
			if err := serializable(one, fmt.Sprintf("%s[%d]", at, index)); err != nil {
				return err
			}
		}
		return nil
	}
	shape := reflect.TypeOf(value)
	switch shape.Kind() {
	case reflect.Map:
		return fmt.Errorf(
			"%s is a %s; a page takes map[string]any, and the serializer panics on any other map",
			at, shape)
	case reflect.Slice, reflect.Array:
		return fmt.Errorf("%s is a %s; a page takes []any", at, shape)
	case reflect.Struct, reflect.Pointer:
		return fmt.Errorf(
			"%s is a %s; a page takes map[string]any, and a struct reaches the page as `undefined`",
			at, shape)
	}
	return fmt.Errorf("%s is a %s, which cannot be handed to a page", at, shape)
}
