package filesystems

import (
	"regexp"
	"strings"
)

var unsafeMountTargetChars = regexp.MustCompile(`[^0-9A-Za-z._-]`)

func SanitizeMountTarget(target string) string {
	safe := unsafeMountTargetChars.ReplaceAllString(target, "-")
	safe = strings.Trim(safe, "-")
	if safe == "" {
		return "share"
	}
	return safe
}
