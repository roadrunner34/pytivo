package pytivoimport

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// section is one [name] block of an INI file, in file order.
type section struct {
	name    string
	options map[string]string
}

// decode turns the file's bytes into text. pyTivo wrote UTF-8 from Python 3
// and the Windows code page from Python 2, so UTF-8 is tried first and
// cp1252 is the fallback, the same order pyTivo itself uses.
func decode(raw []byte) string {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf")) // byte-order mark
	if utf8.Valid(raw) {
		return string(raw)
	}
	out, err := charmap.Windows1252.NewDecoder().Bytes(raw)
	if err != nil {
		// A few byte values are undefined in cp1252. Keep what can be read.
		return strings.ToValidUTF8(string(raw), "�")
	}
	return string(out)
}

// parseINI reads text the way Python's configparser does with pyTivo's
// settings: option names are lower-cased, "=" or ":" separates name from
// value, "#" and ";" start a comment only at the beginning of a line, an
// indented line continues the value above it, and "%" has no special
// meaning.
//
// Python refuses a file with duplicate sections or options. This reader
// keeps the last one and reports a warning, because an importer should
// salvage what it can.
func parseINI(text string) (sections []section, warnings []string) {
	index := map[string]int{}
	var cur *section
	lastKey := ""    // option that a continuation line would extend
	lastIndent := -1 // indentation of that option's first line

	text = strings.ReplaceAll(text, "\r\n", "\n")
	for n, line := range strings.Split(text, "\n") {
		lineNo := n + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") {
			continue
		}
		indent := len(line) - len(strings.TrimLeftFunc(line, unicode.IsSpace))

		if cur != nil && lastKey != "" && indent > lastIndent {
			cur.options[lastKey] += "\n" + trimmed
			continue
		}

		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") && len(trimmed) > 2 {
			name := trimmed[1 : len(trimmed)-1]
			if i, seen := index[name]; seen {
				warnings = append(warnings, fmt.Sprintf("line %d: section [%s] appears more than once; the settings were merged", lineNo, name))
				cur = &sections[i]
			} else {
				index[name] = len(sections)
				sections = append(sections, section{name: name, options: map[string]string{}})
				cur = &sections[len(sections)-1]
			}
			lastKey, lastIndent = "", -1
			continue
		}

		if cur == nil {
			warnings = append(warnings, fmt.Sprintf("line %d: ignored, because it comes before any [section]", lineNo))
			continue
		}

		sep := strings.IndexAny(trimmed, "=:")
		if sep < 0 {
			warnings = append(warnings, fmt.Sprintf("line %d: ignored, because it has no \"=\"", lineNo))
			lastKey = ""
			continue
		}
		key := strings.ToLower(strings.TrimSpace(trimmed[:sep]))
		value := strings.TrimSpace(trimmed[sep+1:])
		if key == "" {
			warnings = append(warnings, fmt.Sprintf("line %d: ignored, because the setting has no name", lineNo))
			lastKey = ""
			continue
		}
		if _, dup := cur.options[key]; dup {
			warnings = append(warnings, fmt.Sprintf("line %d: %q is set more than once in [%s]; the last value was kept", lineNo, key, cur.name))
		}
		cur.options[key] = value
		lastKey, lastIndent = key, indent
	}
	return sections, warnings
}
