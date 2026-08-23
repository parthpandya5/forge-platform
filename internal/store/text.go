package store

import (
	"strings"
	"unicode/utf8"
)

func boundedText(value string, maximumBytes int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) <= maximumBytes {
		return value
	}
	value = value[:maximumBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
