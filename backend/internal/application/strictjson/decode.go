// Package strictjson 校验会话与内部工具的扁平 JSON 输入，拒绝编码修复和宽松字段匹配。
package strictjson

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"unicode/utf8"
)

var ErrInvalid = errors.New("JSON 输入无效")

// ValidDocument 保留 UTF-8 和代理项的严格语义，适配器不能先反序列化并修复编码。
func ValidDocument(body []byte) bool {
	return utf8.Valid(body) && json.Valid(body) && validEscapes(body)
}

// Decode 只接受声明字段组成的对象，字段不得为null或嵌套值。
func Decode(body []byte, maxBytes int, target any, allowed ...string) error {
	fields, err := Object(body, maxBytes, allowed...)
	if err != nil {
		return err
	}
	for _, raw := range fields {
		trimmed := bytes.TrimSpace(raw)
		if trimmed[0] == '{' || trimmed[0] == '[' {
			return ErrInvalid
		}
	}
	if json.Unmarshal(body, target) != nil {
		return ErrInvalid
	}
	return nil
}

// Object 返回严格对象字段供按 schema 递归解码；子对象必须再次校验，不能直接宽松反序列化。
func Object(body []byte, maxBytes int, allowed ...string) (map[string]json.RawMessage, error) {
	if len(body) == 0 || len(body) > maxBytes || !ValidDocument(body) {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	start, err := decoder.Token()
	if err != nil || start != json.Delim('{') {
		return nil, ErrInvalid
	}
	known := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		known[key] = true
	}
	seen := map[string]bool{}
	fields := make(map[string]json.RawMessage)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return nil, ErrInvalid
		}
		name, ok := key.(string)
		if !ok || !known[name] || seen[name] {
			return nil, ErrInvalid
		}
		seen[name] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return nil, ErrInvalid
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
			return nil, ErrInvalid
		}
		fields[name] = raw
	}
	return fields, nil
}

// validEscapes 在JSON语法已经合法的前提下检查未配对代理项和NUL。
func validEscapes(body []byte) bool {
	for i := 0; i < len(body); i++ {
		if body[i] != '"' {
			continue
		}
		i++
		for ; i < len(body) && body[i] != '"'; i++ {
			if body[i] != '\\' {
				continue
			}
			i++
			if body[i] != 'u' {
				continue
			}
			code, err := strconv.ParseUint(string(body[i+1:i+5]), 16, 16)
			if err != nil || code == 0 {
				return false
			}
			i += 4
			if code >= 0xdc00 && code <= 0xdfff {
				return false
			}
			if code >= 0xd800 && code <= 0xdbff {
				if i+6 >= len(body) || body[i+1] != '\\' || body[i+2] != 'u' {
					return false
				}
				low, err := strconv.ParseUint(string(body[i+3:i+7]), 16, 16)
				if err != nil || low < 0xdc00 || low > 0xdfff {
					return false
				}
				i += 6
			}
		}
	}
	return true
}
