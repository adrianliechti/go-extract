package opc

import (
	"strings"
	"unicode/utf8"
)

// resolvePartName follows OPC's source-part base and RFC 3986 resolution
// order: resolve literal dot segments, normalize percent encodings, then
// resolve decoded dot segments. path.Clean cannot be used here because it
// erases empty and trailing segments that OPC does not permit.
func resolvePartName(source, target string) string {
	reference, _, _ := strings.Cut(target, "#")
	first, _, _ := strings.Cut(reference, "/")
	if strings.HasPrefix(reference, "//") || strings.Contains(reference, "?") || strings.Contains(first, ":") || !validReference(reference) {
		return ""
	}
	base := "/" + strings.TrimPrefix(source, "/")
	merged := reference
	if reference == "" {
		merged = base
	} else if !strings.HasPrefix(reference, "/") {
		merged = base[:strings.LastIndexByte(base, '/')+1] + reference
	}
	name, ok := normalizePercent(removeDotSegments(merged))
	if !ok {
		return ""
	}
	name = strings.TrimPrefix(removeDotSegments(name), "/")
	if !validReference(name) {
		return ""
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || strings.HasSuffix(segment, ".") || strings.Contains(segment, "%2F") || strings.Contains(segment, "%5C") {
			return ""
		}
	}
	return name
}

func validReference(s string) bool {
	if !utf8.ValidString(s) {
		return false
	}
	// Validate the whole authored reference before dot removal, including
	// malformed triplets in segments that would otherwise disappear.
	for i := 0; i < len(s); i++ {
		if s[i] == '%' {
			if i+2 >= len(s) || hexValue(s[i+1]) < 0 || hexValue(s[i+2]) < 0 {
				return false
			}
			i += 2
		}
	}
	for _, r := range s {
		if r < 128 {
			if !unreserved(byte(r)) && !strings.ContainsRune("/!$&'()*+,;=:@%", r) {
				return false
			}
		} else if !ucschar(r) {
			return false
		}
	}
	return true
}

// RFC 3987's ucschar excludes private-use and non-character code points.
func ucschar(r rune) bool {
	return r >= 0xA0 && r <= 0xD7FF || r >= 0xF900 && r <= 0xFDCF ||
		r >= 0xFDF0 && r <= 0xFFEF ||
		r >= 0x10000 && r <= 0xEFFFD && r&0xFFFF <= 0xFFFD && !(r >= 0xE0000 && r <= 0xE0FFF)
}

func removeDotSegments(s string) string {
	segments := strings.Split(strings.TrimPrefix(s, "/"), "/")
	out := make([]string, 0, len(segments))
	for i, segment := range segments {
		if segment == "." || segment == ".." {
			if segment == ".." && len(out) > 0 {
				out = out[:len(out)-1]
			}
			if i == len(segments)-1 {
				out = append(out, "")
			}
		} else {
			out = append(out, segment)
		}
	}
	return "/" + strings.Join(out, "/")
}

func unreserved(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || strings.ContainsRune("-._~", rune(b))
}

func hexValue(b byte) int {
	switch {
	case b >= '0' && b <= '9':
		return int(b - '0')
	case b >= 'a' && b <= 'f':
		return int(b-'a') + 10
	case b >= 'A' && b <= 'F':
		return int(b-'A') + 10
	}
	return -1
}

// normalizePercent decodes unreserved ASCII, retains reserved octets, and
// encodes non-ASCII UTF-8 bytes for equivalent ZIP/IRI lookup.
func normalizePercent(s string) (string, bool) {
	const hex = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		b := s[i]
		if b == '%' {
			if i+2 >= len(s) || hexValue(s[i+1]) < 0 || hexValue(s[i+2]) < 0 {
				return "", false
			}
			b = byte(hexValue(s[i+1])*16 + hexValue(s[i+2]))
			i += 2
			if unreserved(b) {
				out.WriteByte(b)
				continue
			}
		} else if b < 128 {
			out.WriteByte(b)
			continue
		}
		out.WriteByte('%')
		out.WriteByte(hex[b>>4])
		out.WriteByte(hex[b&15])
	}
	return out.String(), true
}
