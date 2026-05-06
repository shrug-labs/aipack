package common

import (
	"strconv"
	"strings"
)

// ParseLayerIndex parses a click layer id with a numeric suffix.
func ParseLayerIndex(id, prefix string) (int, bool) {
	raw, ok := strings.CutPrefix(id, prefix)
	if !ok {
		return 0, false
	}
	idx, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return idx, true
}
