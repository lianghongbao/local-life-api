package oss

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// 预定义错误
var (
	// ErrURLInvalid 通用非法 URL
	ErrURLInvalid = errors.New("oss: URL 非法")
	// ErrURLHostNotAllowed host 不在白名单
	ErrURLHostNotAllowed = errors.New("oss: URL host 不在白名单")
	// ErrURLPrefixNotMatch path 不在允许的前缀下
	ErrURLPrefixNotMatch = errors.New("oss: URL 路径前缀不匹配")
	// ErrURLExtNotAllowed 后缀不在白名单
	ErrURLExtNotAllowed = errors.New("oss: URL 后缀不在白名单")
)

// HostConfig URL 校验配置(host 白名单 + 拼 host 用的元数据)
//   - 本结构只关心"图片 URL 的通用属性"
//   - 不带业务概念, 任何 OSS/CDN 资源 URL 都能用
type HostConfig struct {
	// Hosts 允许的 host(小写, 不含 scheme/port)
	//   留空时用 Bucket + Endpoint 兜底
	Hosts []string
	// Bucket + Endpoint 当 Hosts 为空时构造 host 兜底
	Bucket   string
	Endpoint string
}

// ValidateURL 校验 URL 是否合法
//
// 校验规则:
//  1. 必须 http/https
//  2. host 必须在 cfg.Hosts(防前端传外网 URL)
//  3. path 必须以 allowedPrefixes 任一项开头(防越权指向别目录, 谁调谁传)
//  4. path 后缀必须在 allowedExts(防前端传 .html/.svg/.js 等可执行文件, 谁调谁传)
//
// 返回清洗后的 URL(去掉 query 和 fragment, 防签名 URL 污染入库)
func ValidateURL(raw string, cfg HostConfig, allowedPrefixes []string, allowedExts []string) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: 空 URL", ErrURLInvalid)
	}

	// 后缀白名单必须给(由调用方决定), 给空就拒掉
	if len(allowedExts) == 0 {
		return "", fmt.Errorf("%w: 调用方未传 allowedExts", ErrURLExtNotAllowed)
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: parse 失败: %v", ErrURLInvalid, err)
	}

	// 1. scheme 校验
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("%w: scheme=%s", ErrURLInvalid, u.Scheme)
	}

	// 2. host 白名单
	host := strings.ToLower(u.Host)
	if len(cfg.Hosts) > 0 && !hostInList(host, cfg.Hosts) {
		// 容错: Hosts 为空时跳过 host 校验(仅做最低保障)
		fallback := fmt.Sprintf("%s.%s", strings.ToLower(cfg.Bucket), strings.ToLower(cfg.Endpoint))
		if !hostInList(host, []string{fallback}) {
			return "", fmt.Errorf("%w: host=%s", ErrURLHostNotAllowed, host)
		}
	}

	// 3. path 必须存在
	if u.Path == "" || u.Path == "/" {
		return "", fmt.Errorf("%w: path 为空", ErrURLInvalid)
	}

	// 4. path 前缀必须命中 allowedPrefixes 之一
	//    例: 调用方传 ["/post/123/", "/post/123/extra/"] → 必须以其中之一开头
	//    注意: 前缀里要求带前导 "/", url.Path 也带前导 "/", 直接比对
	prefixMatch := false
	for _, p := range allowedPrefixes {
		normalized := p
		if !strings.HasPrefix(normalized, "/") {
			normalized = "/" + normalized
		}
		if !strings.HasSuffix(normalized, "/") {
			normalized += "/"
		}
		if strings.HasPrefix(u.Path, normalized) {
			prefixMatch = true
			break
		}
	}
	if !prefixMatch {
		return "", fmt.Errorf("%w: path=%s, allowed=%v", ErrURLPrefixNotMatch, u.Path, allowedPrefixes)
	}

	// 5. 后缀校验
	lower := strings.ToLower(u.Path)
	extOK := false
	for _, ext := range allowedExts {
		if strings.HasSuffix(lower, "."+strings.ToLower(ext)) {
			extOK = true
			break
		}
	}
	if !extOK {
		return "", fmt.Errorf("%w: ext=%s, allowed=%v", ErrURLExtNotAllowed, extOf(lower), allowedExts)
	}

	// 6. 清洗: 去掉 query 和 fragment(防 XSS payload 入库)
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

// hostInList host 是否在白名单(忽略大小写)
func hostInList(host string, list []string) bool {
	for _, h := range list {
		if strings.EqualFold(host, h) {
			return true
		}
	}
	return false
}

// extOf 提取 path 后缀(不带点, 小写)
func extOf(path string) string {
	idx := strings.LastIndex(path, ".")
	if idx < 0 || idx == len(path)-1 {
		return ""
	}
	return strings.ToLower(path[idx+1:])
}
