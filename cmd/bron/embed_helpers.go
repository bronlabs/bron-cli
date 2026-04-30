package main

// mapItems unwraps a list-shape response (`{"<key>": [...]}`) or a bare array
// into a slice of object items. Used by --embed orchestrators that walk
// balances/tx/etc. without caring about the outer envelope.
func mapItems(v interface{}, key string) []map[string]interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		if arr, ok := t[key].([]interface{}); ok {
			return castMapSlice(arr)
		}
	case []interface{}:
		return castMapSlice(t)
	}
	return nil
}

func castMapSlice(arr []interface{}) []map[string]interface{} {
	out := make([]map[string]interface{}, 0, len(arr))
	for _, item := range arr {
		if m, ok := item.(map[string]interface{}); ok {
			out = append(out, m)
		}
	}
	return out
}
