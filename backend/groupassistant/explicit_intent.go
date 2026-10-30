package groupassistant

import (
	"regexp"
	"strings"
)

// Only the authenticated current message is supplied here. Quotes, history,
// model-produced plans and tool arguments are never sources of authority.
// Ambiguous prose retains the existing actor/version-bound confirmation.
func explicitFileIntent(text, name string, del bool) bool {
	if name == "" || strings.ContainsAny(text, "\n\r`>？?") {
		return false
	}
	for _, word := range []string{"不要", "别", "不许", "不能", "不得", "如果", "假如", "是否", "解释", "引用", "例如", "比如", "有人", "don't", "do not", "if ", "whether", "explain", "quote"} {
		if strings.Contains(strings.ToLower(text), word) {
			return false
		}
	}
	prefix := `^\s*(?:请|帮我|麻烦|please\s+)?\s*`
	verb := `(?:修改|更新|覆盖|重写|编辑|改|update|overwrite|edit|modify)`
	if del {
		verb = `(?:删除|删掉|移除|delete|remove)`
	}
	filename := regexp.QuoteMeta(name)
	if del {
		// Whole-file deletion is authorized only by a complete, single request.
		// A filename prefix must never authorize deleting a line, backup or field.
		return regexp.MustCompile(`(?i)` + prefix + verb + `\s*(?:文件\s*)?` + filename + `\s*[。.!！]?\s*$`).MatchString(text)
	}
	// The filename must be a whole object, not a suffix or backup filename.
	end := `(?:$|[\s，。；,;的让为成并])`
	pattern := prefix + verb + `\s*(?:文件\s*)?` + filename + end
	pattern += `|` + prefix + `把\s*` + filename + `(?:\s|的).*(?:修改|更新|覆盖|改成|改为|编辑)`
	return regexp.MustCompile(`(?i)(?:` + pattern + `)`).MatchString(text)
}

func explicitlyRequestedChange(snap *Snapshot, text string, change Change) bool {
	for logical, entry := range snap.Entries {
		if entry.Ref != change.Ref {
			continue
		}
		name := logical
		if strings.HasPrefix(name, "mine/") {
			name = strings.TrimPrefix(name, "mine/")
		}
		return explicitFileIntent(text, name, change.Delete)
	}
	return false
}
