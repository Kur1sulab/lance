package msf

// 最小 MessagePack 编解码。
//
// 为什么不用第三方库：Metasploit 新版 RPC 只收 binary/message-pack，
// 而这里需要的类型集合很窄（nil/bool/int/float/string/array/map）。
// 自己实现两百行，省掉一个依赖和它的供应链风险 —— 这层是唯一进出口，
// 格式不对会立刻在上线时报错，不会静默跑偏。
import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Encode 把 []any / map[string]any / string / int / bool 等编成 msgpack。
func Encode(v any) []byte {
	var b []byte
	return appendValue(b, v)
}

func appendValue(b []byte, v any) []byte {
	switch x := v.(type) {
	case nil:
		return append(b, 0xc0)
	case bool:
		if x {
			return append(b, 0xc3)
		}
		return append(b, 0xc2)
	case string:
		return appendString(b, x)
	case []byte:
		return appendString(b, string(x))
	case int:
		return appendInt(b, int64(x))
	case int64:
		return appendInt(b, x)
	case float64:
		b = append(b, 0xcb)
		return binary.BigEndian.AppendUint64(b, math.Float64bits(x))
	case []any:
		n := len(x)
		switch {
		case n <= 15:
			b = append(b, 0x90|byte(n))
		case n <= 65535:
			b = append(b, 0xdc)
			b = binary.BigEndian.AppendUint16(b, uint16(n))
		default:
			b = append(b, 0xdd)
			b = binary.BigEndian.AppendUint32(b, uint32(n))
		}
		for _, e := range x {
			b = appendValue(b, e)
		}
		return b
	case map[string]any:
		n := len(x)
		switch {
		case n <= 15:
			b = append(b, 0x80|byte(n))
		case n <= 65535:
			b = append(b, 0xde)
			b = binary.BigEndian.AppendUint16(b, uint16(n))
		default:
			b = append(b, 0xdf)
			b = binary.BigEndian.AppendUint32(b, uint32(n))
		}
		for k, val := range x {
			b = appendString(b, k)
			b = appendValue(b, val)
		}
		return b
	}
	// 兜底：转字符串，总比 panic 好
	return appendString(b, fmt.Sprint(v))
}

func appendString(b []byte, s string) []byte {
	n := len(s)
	switch {
	case n <= 31:
		b = append(b, 0xa0|byte(n))
	case n <= 255:
		b = append(b, 0xd9, byte(n))
	case n <= 65535:
		b = append(b, 0xda)
		b = binary.BigEndian.AppendUint16(b, uint16(n))
	default:
		b = append(b, 0xdb)
		b = binary.BigEndian.AppendUint32(b, uint32(n))
	}
	return append(b, s...)
}

func appendInt(b []byte, i int64) []byte {
	switch {
	case i >= 0 && i <= 127:
		return append(b, byte(i))
	case i < 0 && i >= -32:
		return append(b, byte(0xe0|(i+32)))
	case i >= 0 && i <= 255:
		return append(b, 0xcc, byte(i))
	case i >= 0 && i <= 65535:
		b = append(b, 0xcd)
		return binary.BigEndian.AppendUint16(b, uint16(i))
	case i >= 0 && i <= math.MaxUint32:
		b = append(b, 0xce)
		return binary.BigEndian.AppendUint32(b, uint32(i))
	case i >= math.MinInt8 && i <= math.MaxInt8:
		return append(b, 0xd0, byte(int8(i)))
	case i >= math.MinInt16 && i <= math.MaxInt16:
		b = append(b, 0xd1)
		return binary.BigEndian.AppendUint16(b, uint16(int16(i)))
	case i >= math.MinInt32 && i <= math.MaxInt32:
		b = append(b, 0xd2)
		return binary.BigEndian.AppendUint32(b, uint32(int32(i)))
	default:
		b = append(b, 0xd3)
		return binary.BigEndian.AppendUint64(b, uint64(i))
	}
}

var errShort = errors.New("msgpack: 数据被截断")

// Decode 解成 map[string]any / []any / string / int64 / float64 / bool / nil。
func Decode(b []byte) (any, error) {
	v, _, err := decodeAt(b, 0)
	return v, err
}

func decodeAt(b []byte, i int) (any, int, error) {
	if i >= len(b) {
		return nil, i, errShort
	}
	c := b[i]
	i++
	switch {
	case c <= 0x7f: // positive fixint
		return int64(c), i, nil
	case c >= 0xe0: // negative fixint
		return int64(int8(c)), i, nil
	case c&0xf0 == 0x80: // fixmap
		return decodeMap(b, i, int(c&0x0f))
	case c&0xf0 == 0x90: // fixarray
		return decodeArray(b, i, int(c&0x0f))
	case c&0xe0 == 0xa0: // fixstr
		n := int(c & 0x1f)
		if i+n > len(b) {
			return nil, i, errShort
		}
		return string(b[i : i+n]), i + n, nil
	}
	switch c {
	case 0xc0:
		return nil, i, nil
	case 0xc2:
		return false, i, nil
	case 0xc3:
		return true, i, nil
	case 0xc4, 0xc5, 0xc6: // bin8/16/32
		var n int
		if c == 0xc4 {
			n = int(b[i]); i++
		} else if c == 0xc5 {
			n = int(binary.BigEndian.Uint16(b[i:])); i += 2
		} else {
			n = int(binary.BigEndian.Uint32(b[i:])); i += 4
		}
		if i+n > len(b) {
			return nil, i, errShort
		}
		return string(b[i : i+n]), i + n, nil
	case 0xca:
		return float64(math.Float32frombits(binary.BigEndian.Uint32(b[i:]))), i + 4, nil
	case 0xcb:
		return math.Float64frombits(binary.BigEndian.Uint64(b[i:])), i + 8, nil
	case 0xcc:
		return int64(b[i]), i + 1, nil
	case 0xcd:
		return int64(binary.BigEndian.Uint16(b[i:])), i + 2, nil
	case 0xce:
		return int64(binary.BigEndian.Uint32(b[i:])), i + 4, nil
	case 0xcf:
		return int64(binary.BigEndian.Uint64(b[i:])), i + 8, nil
	case 0xd0:
		return int64(int8(b[i])), i + 1, nil
	case 0xd1:
		return int64(int16(binary.BigEndian.Uint16(b[i:]))), i + 2, nil
	case 0xd2:
		return int64(int32(binary.BigEndian.Uint32(b[i:]))), i + 4, nil
	case 0xd3:
		return int64(binary.BigEndian.Uint64(b[i:])), i + 8, nil
	case 0xd9:
		n := int(b[i]); i++
		if i+n > len(b) {
			return nil, i, errShort
		}
		return string(b[i : i+n]), i + n, nil
	case 0xda:
		n := int(binary.BigEndian.Uint16(b[i:])); i += 2
		if i+n > len(b) {
			return nil, i, errShort
		}
		return string(b[i : i+n]), i + n, nil
	case 0xdb:
		n := int(binary.BigEndian.Uint32(b[i:])); i += 4
		if i+n > len(b) {
			return nil, i, errShort
		}
		return string(b[i : i+n]), i + n, nil
	case 0xdc:
		n := int(binary.BigEndian.Uint16(b[i:])); i += 2
		return decodeArray(b, i, n)
	case 0xdd:
		n := int(binary.BigEndian.Uint32(b[i:])); i += 4
		return decodeArray(b, i, n)
	case 0xde:
		n := int(binary.BigEndian.Uint16(b[i:])); i += 2
		return decodeMap(b, i, n)
	case 0xdf:
		n := int(binary.BigEndian.Uint32(b[i:])); i += 4
		return decodeMap(b, i, n)
	}
	return nil, i, fmt.Errorf("msgpack: 未知类型 0x%02x", c)
}

func decodeArray(b []byte, i, n int) (any, int, error) {
	out := make([]any, 0, n)
	for k := 0; k < n; k++ {
		v, ni, err := decodeAt(b, i)
		if err != nil {
			return nil, ni, err
		}
		out = append(out, v)
		i = ni
	}
	return out, i, nil
}

func decodeMap(b []byte, i, n int) (any, int, error) {
	out := make(map[string]any, n)
	for k := 0; k < n; k++ {
		kv, ni, err := decodeAt(b, i)
		if err != nil {
			return nil, ni, err
		}
		i = ni
		key, _ := kv.(string)
		vv, ni2, err := decodeAt(b, i)
		if err != nil {
			return nil, ni2, err
		}
		i = ni2
		out[key] = vv
	}
	return out, i, nil
}
