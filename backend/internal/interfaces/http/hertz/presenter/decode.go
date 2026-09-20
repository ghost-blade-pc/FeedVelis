package presenter

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

var (
	// ErrBodyTooLarge 表示正文超过该端点的上限。
	ErrBodyTooLarge = errors.New("请求正文过大")
	// ErrMalformedBody 表示正文不是合法的 JSON 对象，或含未知字段、重复字段、尾随内容。
	ErrMalformedBody = errors.New("请求正文格式不正确")
)

// DecodeStrictJSON 严格解码请求正文：限制大小，拒绝未知字段、重复字段与尾随内容。
func DecodeStrictJSON(body []byte, maxBytes int, target any) error {
	if len(body) == 0 {
		return ErrMalformedBody
	}
	if len(body) > maxBytes {
		return ErrBodyTooLarge
	}
	duplicated, err := hasDuplicateKeys(body)
	if err != nil || duplicated {
		return ErrMalformedBody
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrMalformedBody
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return ErrMalformedBody
	}
	return nil
}

// hasDuplicateKeys 在顶层对象上检测重复键；非对象正文交由主解码报错。
func hasDuplicateKeys(body []byte) (bool, error) {
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil {
		return false, err
	}
	if delim, ok := token.(json.Delim); !ok || delim != '{' {
		return false, nil
	}
	seen := make(map[string]struct{})
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return false, err
		}
		key, ok := token.(string)
		if !ok {
			return false, nil
		}
		if _, exists := seen[key]; exists {
			return true, nil
		}
		seen[key] = struct{}{}
		if err := skipValue(decoder); err != nil {
			return false, err
		}
	}
	return false, nil
}

// skipValue 消费一个完整的 JSON 值（含嵌套对象与数组）。
func skipValue(decoder *json.Decoder) error {
	depth := 0
	for {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			switch delim {
			case '{', '[':
				depth++
			case '}', ']':
				depth--
			}
		}
		if depth <= 0 {
			return nil
		}
	}
}
