package notidrawer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

// unsyncedConfigAccess traverses into the current config and performs
// the operation at path according to method, using body and out as
// needed. This is a low-level, unsynchronized function; most callers
// will want to use changeConfig or readConfig instead. This requires a
// read or write lock on currentCtxMu, depending on method (GET needs
// only a read lock; all others need a write lock).
func unsyncedConfigAccess(method, path string, body []byte, out io.Writer) error {
	var err error
	var val any

	// if there is a request body, decode it into the
	// variable that will be set in the config according
	// to method and path
	if len(body) > 0 {
		err = json.Unmarshal(body, &val)
		if err != nil {
			if jsonErr, ok := err.(*json.SyntaxError); ok {
				return fmt.Errorf("decoding request body: %w, at offset %d", jsonErr, jsonErr.Offset)
			}
			return fmt.Errorf("decoding request body: %w", err)
		}
	}

	enc := json.NewEncoder(out)

	cleanPath := strings.Trim(path, "/")
	if cleanPath == "" {
		return fmt.Errorf("no traversable path")
	}

	parts := strings.Split(cleanPath, "/")
	if len(parts) == 0 {
		return fmt.Errorf("path missing")
	}

	// A path that ends with "..." implies:
	// 1) the part before it is an array
	// 2) the payload is an array
	// and means that the user wants to expand the elements
	// in the payload array and append each one into the
	// destination array, like so:
	//     array = append(array, elems...)
	// This special case is handled below.
	ellipses := parts[len(parts)-1] == "..."
	if ellipses {
		parts = parts[:len(parts)-1]
	}

	var ptr any = rawCfg

traverseLoop:
	for i, part := range parts {
		switch v := ptr.(type) {
		case map[string]any:
			// if the next part enters a slice, and the slice is our destination,
			// handle it specially (because appending to the slice copies the slice
			// header, which does not replace the original one like we want)
			if arr, ok := v[part].([]any); ok && i == len(parts)-2 {
				var idx int
				if method != http.MethodPost {
					idxStr := parts[len(parts)-1]
					idx, err = strconv.Atoi(idxStr)
					if err != nil {
						return fmt.Errorf("[%s] invalid array index '%s': %v",
							path, idxStr, err)
					}
					if idx < 0 || (method != http.MethodPut && idx >= len(arr)) || idx > len(arr) {
						return fmt.Errorf("[%s] array index out of bounds: %s", path, idxStr)
					}
				}

				switch method {
				case http.MethodGet:
					err = enc.Encode(arr[idx])
					if err != nil {
						return fmt.Errorf("encoding config: %v", err)
					}
				case http.MethodPost:
					if ellipses {
						valArray, ok := val.([]any)
						if !ok {
							return fmt.Errorf("final element is not an array")
						}
						v[part] = append(arr, valArray...)
					} else {
						v[part] = append(arr, val)
					}
				case http.MethodPut:
					// avoid creation of new slice and a second copy (see
					// https://github.com/golang/go/wiki/SliceTricks#insert)
					arr = append(arr, nil)
					copy(arr[idx+1:], arr[idx:])
					arr[idx] = val
					v[part] = arr
				case http.MethodPatch:
					arr[idx] = val
				case http.MethodDelete:
					v[part] = append(arr[:idx], arr[idx+1:]...)
				default:
					return fmt.Errorf("unrecognized method %s", method)
				}
				break traverseLoop
			}

			if i == len(parts)-1 {
				switch method {
				case http.MethodGet:
					err = enc.Encode(v[part])
					if err != nil {
						return fmt.Errorf("encoding config: %v", err)
					}
				case http.MethodPost:
					// if the part is an existing list, POST appends to
					// it, otherwise it just sets or creates the value
					if arr, ok := v[part].([]any); ok {
						if ellipses {
							valArray, ok := val.([]any)
							if !ok {
								return fmt.Errorf("final element is not an array")
							}
							v[part] = append(arr, valArray...)
						} else {
							v[part] = append(arr, val)
						}
					} else {
						v[part] = val
					}
				case http.MethodPut:
					if _, ok := v[part]; ok {
						return DaemonError{
							Phase: PhaseStarting,
							Err:   fmt.Errorf("[%s] key already exists: %s", path, part),
						}
					}
					v[part] = val
				case http.MethodPatch:
					if _, ok := v[part]; !ok {
						return DaemonError{
							Phase: PhaseStarting,
							Err:   fmt.Errorf("[%s] key does not exist: %s", path, part),
						}
					}
					v[part] = val
				case http.MethodDelete:
					if _, ok := v[part]; !ok {
						return DaemonError{
							Phase: PhaseStarting,
							Err:   fmt.Errorf("[%s] key does not exist: %s", path, part),
						}
					}
					delete(v, part)
				default:
					return fmt.Errorf("unrecognized method %s", method)
				}
			} else {
				// if we are "PUTting" a new resource, the key(s) in its path
				// might not exist yet; that's OK but we need to make them as
				// we go, while we still have a pointer from the level above
				if v[part] == nil && method == http.MethodPut {
					v[part] = make(map[string]any)
				}
				ptr = v[part]
			}

		case []any:
			partInt, err := strconv.Atoi(part)
			if err != nil {
				return fmt.Errorf("[/%s] invalid array index '%s': %v",
					strings.Join(parts[:i+1], "/"), part, err)
			}
			if partInt < 0 || partInt >= len(v) {
				return fmt.Errorf("[/%s] array index out of bounds: %s",
					strings.Join(parts[:i+1], "/"), part)
			}
			ptr = v[partInt]

		default:
			return fmt.Errorf("invalid traversal path at: %s", strings.Join(parts[:i+1], "/"))
		}
	}

	return nil
}

var idRegexp = regexp.MustCompile(`(?m)^\s*"@id"\s*:\s*".*?"\s*,?`)

// RemoveMetaFields removes meta fields like "@id" from a JSON message
// by using a simple regular expression. (An alternate way to do this
// would be to delete them from the raw, map[string]any
// representation as they are indexed, then iterate the index we made
// and add them back after encoding as JSON, but this is simpler.)
func RemoveMetaFields(rawJSON []byte) []byte {
	return idRegexp.ReplaceAllFunc(rawJSON, func(in []byte) []byte {
		// matches with a comma on both sides (when "@id" property is
		// not the first or last in the object) need to keep exactly
		// one comma for correct JSON syntax
		comma := []byte{','}
		if bytes.HasPrefix(in, comma) && bytes.HasSuffix(in, comma) {
			return comma
		}
		return []byte{}
	})
}
