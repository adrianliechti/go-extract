package msg

import "net/http"

func sniffContentType(data []byte) string {
	return http.DetectContentType(data)
}
