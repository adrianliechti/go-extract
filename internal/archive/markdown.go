package archive

import (
	"fmt"
	"strconv"
	"strings"
)

func renderMarkdown(doc *Document) string {
	var output strings.Builder
	output.WriteString("# ")
	output.WriteString(formatLabel(doc.Format))
	output.WriteString(" archive\n")
	if len(doc.Entries) == 0 {
		output.WriteString("\n_Archive is empty._")
		return output.String()
	}

	output.WriteString("\n## Contents\n\n")
	for _, entry := range doc.Entries {
		output.WriteString("- `")
		output.WriteString(strings.ReplaceAll(entry.Name, "`", "'"))
		output.WriteString("` (")
		output.WriteString(formatBytes(len(entry.Data)))
		output.WriteString(")\n")
	}
	return strings.TrimSpace(output.String())
}

func formatLabel(format Format) string {
	switch format {
	case FormatZIP:
		return "ZIP"
	case FormatTAR:
		return "TAR"
	case FormatGZIP:
		return "GZIP"
	default:
		return "File"
	}
}

func formatBytes(size int) string {
	if size < 1_024 {
		return strconv.Itoa(size) + " B"
	}
	if size < 1_024*1_024 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1_024)
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1_024*1_024))
}
