package api

import (
	"bytes"
	"encoding/json"
)

// locateJSONValue is the loc of the first value in body whose JSON text is
// raw: "body" and the object keys down to it. Array positions are left out,
// as every other loc here leaves them. A value it cannot find is loc ["body"].
func locateJSONValue(body, raw []byte) []string {
	want, ok := jsonToken(raw)
	if !ok {
		return []string{"body"}
	}
	type frame struct {
		object    bool
		key       string
		expectKey bool
	}
	var stack []frame
	valueDone := func() {
		if n := len(stack); n > 0 && stack[n-1].object {
			stack[n-1].expectKey = true
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err != nil {
			return []string{"body"}
		}
		if n := len(stack); n > 0 && stack[n-1].object && stack[n-1].expectKey {
			if key, isKey := token.(string); isKey {
				stack[n-1].key, stack[n-1].expectKey = key, false
				continue
			}
		}
		switch value := token.(type) {
		case json.Delim:
			switch value {
			case '{':
				stack = append(stack, frame{object: true, expectKey: true})
			case '[':
				stack = append(stack, frame{})
			default:
				stack = stack[:len(stack)-1]
				valueDone()
			}
			continue
		}
		if token == want {
			loc := []string{"body"}
			for _, one := range stack {
				if one.object {
					loc = append(loc, one.key)
				}
			}
			return loc
		}
		valueDone()
	}
}

// jsonToken is the token json.Decoder.Token yields for one scalar value, with
// numbers kept as their text.
func jsonToken(raw []byte) (json.Token, bool) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return nil, false
	}
	if _, isDelim := token.(json.Delim); isDelim {
		return nil, false
	}
	return token, true
}
