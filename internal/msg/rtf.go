package msg

import (
	"encoding/binary"
	"errors"

	"github.com/adrianliechti/go-extract/internal/rtf"
)

const (
	rtfMagicCompressed   = 0x75465a4c // LZFu
	rtfMagicUncompressed = 0x414c454d // MELA
)

var rtfDictionary = []byte("{\\rtf1\\ansi\\mac\\deff0\\deftab720{\\fonttbl;}{\\f0\\fnil \\froman \\fswiss \\fmodern \\fscript \\fdecor MS Sans SerifSymbolArialTimes New RomanCourier{\\colortbl\\red0\\green0\\blue0\r\n\\par \\pard\\plain\\f0\\fs20\\b\\i\\u\\tab\\tx")

func decompressRTF(data []byte) ([]byte, error) {
	if len(data) < 16 {
		return nil, errors.New("msg: compressed RTF header is truncated")
	}
	compressedSize := binary.LittleEndian.Uint32(data[0:4])
	rawSize := binary.LittleEndian.Uint32(data[4:8])
	magic := binary.LittleEndian.Uint32(data[8:12])
	if rawSize > 512<<20 || uint64(rawSize) > uint64(len(data))*16+1<<20 {
		return nil, errors.New("msg: compressed RTF expands beyond safety limit")
	}
	payload := data[16:]
	if compressedSize >= 12 && int(compressedSize-12) < len(payload) {
		payload = payload[:compressedSize-12]
	}
	if magic == rtfMagicUncompressed {
		if int(rawSize) < len(payload) {
			payload = payload[:rawSize]
		}
		return append([]byte(nil), payload...), nil
	}
	if magic != rtfMagicCompressed {
		return nil, errors.New("msg: unknown compressed RTF magic")
	}

	dictionary := make([]byte, 4096)
	copy(dictionary, rtfDictionary)
	writePos := len(rtfDictionary) & 0x0fff
	result := make([]byte, 0, rawSize)
	for pos := 0; pos < len(payload) && len(result) < int(rawSize); {
		flags := payload[pos]
		pos++
		for bit := 0; bit < 8 && pos < len(payload) && len(result) < int(rawSize); bit++ {
			if flags&(1<<bit) == 0 {
				value := payload[pos]
				pos++
				result = append(result, value)
				dictionary[writePos] = value
				writePos = (writePos + 1) & 0x0fff
				continue
			}
			if pos+1 >= len(payload) {
				break
			}
			token := uint16(payload[pos])<<8 | uint16(payload[pos+1])
			pos += 2
			readPos := int(token >> 4)
			length := int(token&0x0f) + 2
			for i := 0; i < length && len(result) < int(rawSize); i++ {
				value := dictionary[readPos]
				readPos = (readPos + 1) & 0x0fff
				result = append(result, value)
				dictionary[writePos] = value
				writePos = (writePos + 1) & 0x0fff
			}
		}
	}
	if len(result) == 0 && rawSize != 0 {
		return nil, errors.New("msg: compressed RTF contains no data")
	}
	return result, nil
}

func rtfToText(data []byte, codePage int) string {
	return rtf.ToText(data, rtf.Options{CodePage: codePage})
}
