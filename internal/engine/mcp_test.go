package engine

import (
	"reflect"
	"testing"
)

func TestUnionToolLists(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		a, b, want []string
	}{
		{"both empty", nil, nil, nil},
		{"only a", []string{"a", "b"}, nil, []string{"a", "b"}},
		{"only b", nil, []string{"c"}, []string{"c"}},
		{"disjoint", []string{"a"}, []string{"b"}, []string{"a", "b"}},
		{"overlap dedupes", []string{"a", "b"}, []string{"b", "c"}, []string{"a", "b", "c"}},
		{"unsorted inputs produce sorted output", []string{"c", "a"}, []string{"b"}, []string{"a", "b", "c"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := UnionToolLists(tc.a, tc.b)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("UnionToolLists(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}
