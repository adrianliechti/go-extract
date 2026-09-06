package archive

import (
	"fmt"
	"io"
	"mime"
	"path"
	"strconv"
	"strings"
)

type extractionState struct {
	limits  limits
	records uint64
	total   uint64
	entries []Entry
	used    map[string]uint64
}

func (s *extractionState) countRecord() error {
	s.records++
	if s.records > s.limits.entries {
		return fmt.Errorf("%w: archive has more than %d entries", ErrResourceLimit, s.limits.entries)
	}
	return nil
}

func (s *extractionState) addFile(originalName string, declaredSize uint64, reader io.Reader) error {
	if declaredSize > s.limits.entry {
		return fmt.Errorf("%w: archive entry %q exceeds %d inflated bytes", ErrResourceLimit, originalName, s.limits.entry)
	}
	if declaredSize > s.limits.total-s.total {
		return fmt.Errorf("%w: archive exceeds %d total inflated bytes", ErrResourceLimit, s.limits.total)
	}

	allowance := min(s.limits.entry, s.limits.total-s.total)
	data, err := io.ReadAll(io.LimitReader(reader, int64(allowance)+1))
	if err != nil {
		return err
	}
	if uint64(len(data)) > s.limits.entry {
		return fmt.Errorf("%w: archive entry %q exceeds %d inflated bytes", ErrResourceLimit, originalName, s.limits.entry)
	}
	if uint64(len(data)) > s.limits.total-s.total {
		return fmt.Errorf("%w: archive exceeds %d total inflated bytes", ErrResourceLimit, s.limits.total)
	}

	name := cleanEntryName(originalName, s.records)
	name = s.uniqueName(name)
	entry := Entry{
		Name:      name,
		MediaType: mediaTypeForName(name),
		Data:      data,
	}
	if name != originalName {
		entry.OriginalName = originalName
	}
	s.entries = append(s.entries, entry)
	s.total += uint64(len(data))
	return nil
}

func (s *extractionState) uniqueName(name string) string {
	s.used[name]++
	if s.used[name] == 1 {
		return name
	}

	dir, file := path.Split(name)
	extension := path.Ext(file)
	base := strings.TrimSuffix(file, extension)
	for index := s.used[name]; ; index++ {
		candidate := path.Join(dir, base+" ("+strconv.FormatUint(index, 10)+")"+extension)
		if s.used[candidate] == 0 {
			s.used[candidate] = 1
			return candidate
		}
	}
}

func cleanEntryName(name string, index uint64) string {
	name = strings.ToValidUTF8(name, "�")
	name = strings.Map(func(value rune) rune {
		if value < 0x20 || value == 0x7f {
			return '_'
		}
		return value
	}, name)
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(path.Clean("/"+name), "/")
	if name == "" || name == "." {
		return "entry-" + strconv.FormatUint(index, 10)
	}
	if slash := strings.IndexByte(name, '/'); slash >= 0 {
		name = strings.ReplaceAll(name[:slash], ":", "_") + name[slash:]
	} else {
		name = strings.ReplaceAll(name, ":", "_")
	}
	return name
}

func mediaTypeForName(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".zip":
		return "application/zip"
	case ".tar":
		return "application/x-tar"
	case ".gz", ".gzip", ".tgz":
		return "application/gzip"
	case ".pdf":
		return "application/pdf"
	case ".html", ".htm":
		return "text/html"
	case ".xhtml":
		return "application/xhtml+xml"
	case ".rtf":
		return "application/rtf"
	case ".eml":
		return "message/rfc822"
	case ".msg":
		return "application/vnd.ms-outlook"
	case ".txt", ".text", ".log":
		return "text/plain; charset=utf-8"
	case ".md", ".markdown", ".mdown", ".mkd", ".mkdn":
		return "text/markdown; charset=utf-8"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".docm":
		return "application/vnd.ms-word.document.macroEnabled.12"
	case ".dotx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.template"
	case ".dotm":
		return "application/vnd.ms-word.template.macroEnabled.12"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".xlsm":
		return "application/vnd.ms-excel.sheet.macroEnabled.12"
	case ".xltx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.template"
	case ".xltm":
		return "application/vnd.ms-excel.template.macroEnabled.12"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case ".pptm":
		return "application/vnd.ms-powerpoint.presentation.macroEnabled.12"
	case ".ppsx":
		return "application/vnd.openxmlformats-officedocument.presentationml.slideshow"
	case ".ppsm":
		return "application/vnd.ms-powerpoint.slideshow.macroEnabled.12"
	case ".potx":
		return "application/vnd.openxmlformats-officedocument.presentationml.template"
	case ".potm":
		return "application/vnd.ms-powerpoint.template.macroEnabled.12"
	}
	mediaType, _, _ := mime.ParseMediaType(mime.TypeByExtension(path.Ext(name)))
	return mediaType
}
