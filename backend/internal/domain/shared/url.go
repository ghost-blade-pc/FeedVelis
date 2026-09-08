// Package shared 提供跨领域且不依赖外层的基础值规则。
package shared

import (
	"errors"
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

var ErrInvalidHTTPURL = errors.New("HTTP URL 无效")

// NormalizeHTTPURL 保留 query 顺序和有语义的 percent-encoding，只移除 fragment、默认端口和 dot-segment。
func NormalizeHTTPURL(raw string, maxBytes int) (string, error) {
	if len(raw) == 0 || len(raw) > maxBytes {
		return "", ErrInvalidHTTPURL
	}
	u, err := url.Parse(raw)
	if err != nil || !u.IsAbs() || u.User != nil {
		return "", ErrInvalidHTTPURL
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", ErrInvalidHTTPURL
	}
	host, err := idna.Lookup.ToASCII(strings.ToLower(u.Hostname()))
	if err != nil || host == "" {
		return "", ErrInvalidHTTPURL
	}
	port := u.Port()
	if port != "" && port != "80" && port != "443" {
		return "", ErrInvalidHTTPURL
	}
	if (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443") {
		port = ""
	}
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	u.Host = host
	if port != "" {
		u.Host = net.JoinHostPort(strings.Trim(host, "[]"), port)
	}
	u.Fragment = ""
	escapedPath := u.EscapedPath()
	if escapedPath == "" {
		escapedPath = "/"
	} else {
		escapedPath = removeDotSegments(escapedPath)
		if !strings.HasPrefix(escapedPath, "/") {
			escapedPath = "/" + escapedPath
		}
	}
	decodedPath, err := url.PathUnescape(escapedPath)
	if err != nil {
		return "", ErrInvalidHTTPURL
	}
	u.Path = decodedPath
	u.RawPath = escapedPath
	result := u.String()
	if len(result) > maxBytes {
		return "", ErrInvalidHTTPURL
	}
	return result, nil
}

// removeDotSegments 实现 RFC 3986 5.2.4，同时保留重复斜杠、尾斜杠和 percent-encoding。
func removeDotSegments(input string) string {
	output := ""
	for input != "" {
		switch {
		case strings.HasPrefix(input, "../"):
			input = input[3:]
		case strings.HasPrefix(input, "./"):
			input = input[2:]
		case strings.HasPrefix(input, "/./"):
			input = "/" + input[3:]
		case input == "/.":
			input = "/"
		case strings.HasPrefix(input, "/../"):
			input = "/" + input[4:]
			output = removeLastPathSegment(output)
		case input == "/..":
			input = "/"
			output = removeLastPathSegment(output)
		case input == "." || input == "..":
			input = ""
		default:
			end := len(input)
			searchFrom := 0
			if strings.HasPrefix(input, "/") {
				searchFrom = 1
			}
			if next := strings.IndexByte(input[searchFrom:], '/'); next >= 0 {
				end = searchFrom + next
			}
			output += input[:end]
			input = input[end:]
		}
	}
	return output
}

func removeLastPathSegment(value string) string {
	if index := strings.LastIndexByte(value, '/'); index >= 0 {
		return value[:index]
	}
	return ""
}
