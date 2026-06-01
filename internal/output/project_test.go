package output

import (
	"encoding/json"
	"testing"
)

func jsonStr(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestProject(t *testing.T) {
	tests := []struct {
		name string
		in   interface{}
		cols []string
		want string
	}{
		{
			name: "pick flat keys in column order",
			in:   map[string]interface{}{"a": "1", "b": "2", "c": "3"},
			cols: []string{"c", "a"},
			want: `{"c":"3","a":"1"}`,
		},
		{
			name: "nested dot-path keeps shape",
			in:   map[string]interface{}{"id": "t1", "params": map[string]interface{}{"amount": "10", "x": "y"}},
			cols: []string{"id", "params.amount"},
			want: `{"id":"t1","params":{"amount":"10"}}`,
		},
		{
			name: "list-shape projects each item",
			in: map[string]interface{}{"transactions": []interface{}{
				map[string]interface{}{"id": "1", "status": "a", "junk": "x"},
				map[string]interface{}{"id": "2", "status": "b", "junk": "y"},
			}},
			cols: []string{"id", "status"},
			want: `{"transactions":[{"id":"1","status":"a"},{"id":"2","status":"b"}]}`,
		},
		{
			name: "missing path is skipped",
			in:   map[string]interface{}{"a": "1"},
			cols: []string{"a", "nope"},
			want: `{"a":"1"}`,
		},
		{
			name: "several leaves of one nested object merge under it",
			in: map[string]interface{}{
				"balanceId": "b1",
				"_embedded": map[string]interface{}{"price": "10", "baseAssetId": "s1", "junk": "x"},
			},
			cols: []string{"balanceId", "_embedded.price", "_embedded.baseAssetId"},
			want: `{"balanceId":"b1","_embedded":{"price":"10","baseAssetId":"s1"}}`,
		},
		{
			name: "multiple fields from objects inside a nested array",
			in: map[string]interface{}{"transactions": []interface{}{
				map[string]interface{}{
					"transactionId": "t1",
					"events": []interface{}{
						map[string]interface{}{"type": "created", "ts": "1", "extra": "x"},
						map[string]interface{}{"type": "signed", "ts": "2", "extra": "y"},
					},
				},
			}},
			cols: []string{"transactionId", "events.type", "events.ts"},
			want: `{"transactions":[{"transactionId":"t1","events":[{"type":"created","ts":"1"},{"type":"signed","ts":"2"}]}]}`,
		},
		{
			name: "bare head takes the whole subtree",
			in: map[string]interface{}{
				"id":     "t1",
				"params": map[string]interface{}{"amount": "10", "to": "0xabc"},
			},
			cols: []string{"id", "params"},
			want: `{"id":"t1","params":{"amount":"10","to":"0xabc"}}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := jsonStr(t, Project(tt.in, tt.cols)); got != tt.want {
				t.Fatalf("Project = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestPlainUnwrapsOrderedMap(t *testing.T) {
	// Project emits orderedMap nodes; Plain must turn them back into plain
	// maps so downstream consumers (gojq) don't choke on the wrapper.
	projected := Project(
		map[string]interface{}{"id": "t1", "params": map[string]interface{}{"amount": "10", "x": "y"}},
		[]string{"id", "params.amount"},
	)
	if _, isOrdered := projected.(orderedMap); !isOrdered {
		t.Fatalf("expected Project to yield orderedMap, got %T", projected)
	}
	plain := Plain(projected)
	if _, isOrdered := plain.(orderedMap); isOrdered {
		t.Fatal("Plain left an orderedMap in the tree")
	}
	m, ok := plain.(map[string]interface{})
	if !ok {
		t.Fatalf("Plain root is %T, want map", plain)
	}
	if _, ok := m["params"].(map[string]interface{}); !ok {
		t.Fatalf("nested node is %T, want map", m["params"])
	}
	if jsonStr(t, plain) != `{"id":"t1","params":{"amount":"10"}}` {
		t.Fatalf("unexpected: %s", jsonStr(t, plain))
	}
}

func TestProjectEmptyColsReturnsInputUnchanged(t *testing.T) {
	in := map[string]interface{}{"a": "1", "b": map[string]interface{}{"c": "2"}}
	for _, cols := range [][]string{nil, {}} {
		if got := jsonStr(t, Project(in, cols)); got != jsonStr(t, in) {
			t.Fatalf("empty cols changed the value: %s", got)
		}
	}
}
