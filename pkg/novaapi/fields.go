package novaapi

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Field is one -f/-F argument of `nova api`, parsed like `gh api`.
type Field struct {
	Key   string
	Value any
}

// ParseRawField parses -f key=value: the value stays a string.
func ParseRawField(s string) (Field, error) {
	k, v, ok := strings.Cut(s, "=")
	if !ok || k == "" {
		return Field{}, fmt.Errorf("field %q: expected key=value", s)
	}
	return Field{Key: k, Value: v}, nil
}

// ParseTypedField parses -F key=value: true, false, null and numbers become
// JSON literals, @file (or @- for stdin) reads the value from a file, anything
// else stays a string. (gh converts only integers; numbers with a fraction are
// common in the NOVA API, poses and joints, so they are converted too.) A
// number is passed on exactly as written (no rounding of large integers, no
// reformatting in the query); forms JSON doesn't allow (0x10, +1, 01, NaN) stay
// strings.
func ParseTypedField(s string, readFile func(string) ([]byte, error)) (Field, error) {
	f, err := ParseRawField(s)
	if err != nil {
		return f, err
	}
	v := f.Value.(string)
	switch {
	case v == "true":
		f.Value = true
	case v == "false":
		f.Value = false
	case v == "null":
		f.Value = nil
	case strings.HasPrefix(v, "@"):
		b, err := readFile(v[1:])
		if err != nil {
			return f, fmt.Errorf("field %q: %w", f.Key, err)
		}
		f.Value = string(b)
	case jsonNumber.MatchString(v):
		f.Value = json.Number(v)
	}
	return f, nil
}

// jsonNumber is the number grammar of JSON (RFC 8259).
var jsonNumber = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?([eE][+-]?[0-9]+)?$`)

// BuildBody turns fields into a JSON object. Keys use gh's syntax for nesting:
// a[b]=1 sets {"a": {"b": 1}}, a[]=1 appends to the array a, a[][b]=1 starts a
// new object in the array a (or sets b on the last one if it has no b yet).
func BuildBody(fields []Field) (map[string]any, error) {
	root := map[string]any{}
	for _, f := range fields {
		path, err := keyPath(f.Key)
		if err != nil {
			return nil, err
		}
		if err := setPath(root, path, f.Value, f.Key); err != nil {
			return nil, err
		}
	}
	return root, nil
}

// keyPath splits "a[b][]" into ["a", "b", ""].
func keyPath(key string) ([]string, error) {
	i := strings.IndexByte(key, '[')
	if i < 0 {
		return []string{key}, nil
	}
	if i == 0 {
		return nil, fmt.Errorf("field %q: key must not start with [", key)
	}
	path := []string{key[:i]}
	rest := key[i:]
	for rest != "" {
		if rest[0] != '[' {
			return nil, fmt.Errorf("field %q: expected [ at %q", key, rest)
		}
		j := strings.IndexByte(rest, ']')
		if j < 0 {
			return nil, fmt.Errorf("field %q: missing ]", key)
		}
		path = append(path, rest[1:j])
		rest = rest[j+1:]
	}
	return path, nil
}

func setPath(obj map[string]any, path []string, value any, key string) error {
	name := path[0]
	if len(path) == 1 {
		obj[name] = value
		return nil
	}
	next := path[1:]
	if next[0] == "" { // array
		arr, _ := obj[name].([]any)
		if _, isArr := obj[name].([]any); obj[name] != nil && !isArr {
			return fmt.Errorf("field %q: %s is not an array", key, name)
		}
		if len(next) == 1 {
			obj[name] = append(arr, value)
			return nil
		}
		// a[][b]=…: set b on the last element unless it has b already
		var elem map[string]any
		if n := len(arr); n > 0 {
			if last, ok := arr[n-1].(map[string]any); ok {
				if _, taken := last[next[1]]; !taken {
					elem = last
				}
			}
		}
		if elem == nil {
			elem = map[string]any{}
			arr = append(arr, elem)
		}
		obj[name] = arr
		return setPath(elem, next[1:], value, key)
	}
	child, ok := obj[name].(map[string]any)
	if !ok {
		if obj[name] != nil {
			return fmt.Errorf("field %q: %s is not an object", key, name)
		}
		child = map[string]any{}
		obj[name] = child
	}
	return setPath(child, next, value, key)
}

// AddQuery appends fields to the query of path (for GET requests, like gh).
// Nested keys are passed as written (a[b]=1); a[]=1 becomes a=1, so repeated
// keys form an array.
func AddQuery(path string, fields []Field) string {
	if len(fields) == 0 {
		return path
	}
	q := url.Values{}
	for _, f := range fields {
		k := strings.TrimSuffix(f.Key, "[]")
		v := ""
		if f.Value != nil {
			v = fmt.Sprint(f.Value)
		}
		q.Add(k, v)
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + q.Encode()
}
