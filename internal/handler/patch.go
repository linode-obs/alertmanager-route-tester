package handler

import (
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"unicode"
)

func validPatchTarget(target string) bool {
	if target == "." || !fs.ValidPath(target) || strings.ContainsAny(target, "\\:") {
		return false
	}
	for _, character := range target {
		if !unicode.IsPrint(character) {
			return false
		}
	}
	return true
}

func configurationPatch(diff, target string) string {
	if diff == "" {
		return ""
	}
	previous, proposed := strconv.Quote("a/"+target), strconv.Quote("b/"+target)
	return fmt.Sprintf("diff --git %s %s\n--- %s\n+++ %s\n%s", previous, proposed, previous, proposed, strings.TrimPrefix(diff, "--- fetched\n+++ draft\n"))
}
