package msg

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/abemedia/go-cfb"
	"github.com/adrianliechti/go-extract/internal/textutil"
)

const (
	ptI2      = 0x0002
	ptLong    = 0x0003
	ptBoolean = 0x000b
	ptI8      = 0x0014
	ptString8 = 0x001e
	ptUnicode = 0x001f
	ptSysTime = 0x0040
	ptBinary  = 0x0102
)

type mapiObject struct {
	storage    *cfb.Storage
	streams    map[uint32][]byte
	fixed      map[uint32]uint64
	codePage   int
	streamSize int64
}

func readMAPIObject(storage *cfb.Storage, messageObject bool, maxStream int64) (*mapiObject, error) {
	o := &mapiObject{
		storage:    storage,
		streams:    make(map[uint32][]byte),
		fixed:      make(map[uint32]uint64),
		codePage:   1252,
		streamSize: maxStream,
	}
	var properties []byte
	for _, entry := range storage.Entries {
		stream, ok := entry.(*cfb.Stream)
		if !ok {
			continue
		}
		if stream.Size < 0 || stream.Size > maxStream {
			return nil, fmt.Errorf("%w: MSG stream %q is too large", ErrResourceLimit, stream.Name)
		}
		data, err := io.ReadAll(io.LimitReader(stream.Open(), maxStream+1))
		if err != nil {
			return nil, fmt.Errorf("msg: read MSG stream %q: %w", stream.Name, err)
		}
		if int64(len(data)) > maxStream {
			return nil, fmt.Errorf("%w: MSG stream %q is too large", ErrResourceLimit, stream.Name)
		}
		if strings.EqualFold(stream.Name, "__properties_version1.0") {
			properties = data
			continue
		}
		if tag, ok := streamPropertyTag(stream.Name); ok {
			o.streams[tag] = data
		}
	}

	headerSize := 8
	if messageObject {
		headerSize = 32
	}
	if len(properties) >= headerSize {
		for offset := headerSize; offset+16 <= len(properties); offset += 16 {
			tag := binary.LittleEndian.Uint32(properties[offset : offset+4])
			typeID := uint16(tag)
			switch typeID {
			case ptI2:
				o.fixed[tag] = uint64(binary.LittleEndian.Uint16(properties[offset+8 : offset+10]))
			case ptLong:
				o.fixed[tag] = uint64(binary.LittleEndian.Uint32(properties[offset+8 : offset+12]))
			case ptBoolean:
				o.fixed[tag] = uint64(binary.LittleEndian.Uint16(properties[offset+8 : offset+10]))
			case ptI8, ptSysTime:
				o.fixed[tag] = binary.LittleEndian.Uint64(properties[offset+8 : offset+16])
			}
		}
	}
	for _, id := range []uint16{0x3fde, 0x3ffd} {
		if value, ok := o.integer(id); ok && value > 0 && value <= 65535 {
			o.codePage = int(value)
			break
		}
	}
	return o, nil
}

func streamPropertyTag(name string) (uint32, bool) {
	const prefix = "__substg1.0_"
	if len(name) < len(prefix)+8 || !strings.EqualFold(name[:len(prefix)], prefix) {
		return 0, false
	}
	value, err := strconv.ParseUint(name[len(prefix):len(prefix)+8], 16, 32)
	if err != nil {
		return 0, false
	}
	return uint32(value), true
}

func propertyTag(id, typeID uint16) uint32 {
	return uint32(id)<<16 | uint32(typeID)
}

func (o *mapiObject) string(id uint16) string {
	if data, ok := o.streams[propertyTag(id, ptUnicode)]; ok {
		return decodeUTF16LE(data)
	}
	if data, ok := o.streams[propertyTag(id, ptString8)]; ok {
		return decodeCodePage(data, o.codePage)
	}
	return ""
}

func (o *mapiObject) binary(id uint16) []byte {
	return o.streams[propertyTag(id, ptBinary)]
}

func (o *mapiObject) integer(id uint16) (uint64, bool) {
	for _, typeID := range []uint16{ptLong, ptI2, ptBoolean, ptI8} {
		if value, ok := o.fixed[propertyTag(id, typeID)]; ok {
			return value, true
		}
	}
	return 0, false
}

func (o *mapiObject) boolean(id uint16) bool {
	value, ok := o.fixed[propertyTag(id, ptBoolean)]
	return ok && value != 0
}

func (o *mapiObject) time(id uint16) time.Time {
	value, ok := o.fixed[propertyTag(id, ptSysTime)]
	if !ok || value == 0 {
		return time.Time{}
	}
	return filetime(value)
}

func (o *mapiObject) childStorages(prefix string) []*cfb.Storage {
	var result []*cfb.Storage
	for _, entry := range o.storage.Entries {
		if storage, ok := entry.(*cfb.Storage); ok && strings.HasPrefix(strings.ToLower(storage.Name), strings.ToLower(prefix)) {
			result = append(result, storage)
		}
	}
	return result
}

func (o *mapiObject) childStorage(name string) *cfb.Storage {
	for _, entry := range o.storage.Entries {
		if storage, ok := entry.(*cfb.Storage); ok && strings.EqualFold(storage.Name, name) {
			return storage
		}
	}
	return nil
}

func decodeUTF16LE(data []byte) string {
	data = bytes.TrimSuffix(data, []byte{0, 0})
	if len(data)%2 != 0 {
		data = data[:len(data)-1]
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = binary.LittleEndian.Uint16(data[i*2 : i*2+2])
	}
	return strings.TrimRight(string(utf16.Decode(units)), "\x00")
}

func decodeCodePage(data []byte, codePage int) string {
	return textutil.DecodeCodePage(data, codePage)
}

func filetime(ticks uint64) time.Time {
	const unixEpoch = uint64(116444736000000000)
	if ticks < unixEpoch {
		return time.Time{}
	}
	ticks -= unixEpoch
	seconds := int64(ticks / 10_000_000)
	nanos := int64(ticks%10_000_000) * 100
	return time.Unix(seconds, nanos).UTC()
}
