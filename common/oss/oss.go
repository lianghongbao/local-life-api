// Package oss 封装阿里云 OSS 直传能力(V4 签名 + STS 临时凭证方案)
// 适用场景:前端直传 OSS,不走后端转发
// 安全模型:
//   - 后端用 RAM 长期 AK/SK 调 STS AssumeRole
//   - 拿到 STS 临时凭证后再签 V4 直传头部
//   - 不向前端暴露 AccessKeySecret
// 参考: https://help.aliyun.com/zh/oss/developer-reference/signature-version-4-recommend
//
// 本包只提供 OSS 协议层的通用能力, 不耦合任何业务语义:
//   - file_type / biz / 文件大小上限 / 后缀白名单 / file_key 目录规则 —— 全部由调用方传入
//   - 调用方在业务 logic 中校验、转换
package oss

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	// product 阿里云 V4 签名中的 service 标识
	product = "oss"
	// SignatureVersion 固定值
	SignatureVersion = "OSS4-HMAC-SHA256"

	// 默认 STS 临时凭证有效期
	defaultSTSSessionDuration = 3600 * time.Second
	// 默认 Policy 过期时间(Policy 比 STS 略短)
	defaultPolicyExpiration = 1 * time.Hour
	// stsCacheTTL STS 凭证本地缓存有效期
	// 设为 30 分钟 < STS 1 小时, 保证从缓存取出的凭证到前端用时仍未过期
	// 同时把上游 STS QPS 从 N 压到 1/30N
	defaultSTSCacheTTL = 30 * time.Minute
)

// 预定义错误
var (
	// ErrConfigIncomplete OSS 配置缺失
	ErrConfigIncomplete = errors.New("oss: 配置缺失(bucket/region/roleArn/ak/sk)")
	// ErrAssumeRoleFailed 调 STS 失败
	ErrAssumeRoleFailed = errors.New("oss: STS AssumeRole 失败")
	// ErrInvalidExt 后缀不在白名单内
	ErrInvalidExt = errors.New("oss: 后缀不在白名单内")
)

// Config OSS 客户端配置(从 yaml 注入, 结构化暴露所需字段)
type Config struct {
	// Endpoint bucket 域名, 例 "oss-cn-guangzhou.aliyuncs.com"
	Endpoint string
	// Bucket bucket 名, 例 "lhb-local-life"
	Bucket string
	// Region bucket 所在地域, 例 "cn-guangzhou"(V4 签名派生 + 拼 host 用)
	Region string
	// RoleArn RAM 角色 ARN, 例 "acs:ram::123:role/oss-sts-upload"
	RoleArn string
	// AccessKeyID RAM 长期 AK
	AccessKeyID string
	// AccessKeySecret RAM 长期 SK
	AccessKeySecret string

	// UploadHost 前端直传 OSS 用的目标地址(bucket 域名)
	//   例 https://<bucket>.<endpoint>
	//   留空则用 https://<bucket>.<endpoint> 兜底
	UploadHost string
	// URLPrefix 资源访问 URL 前缀(指向 CDN 加速域名或 OSS bucket host)
	//   例 https://<your-cdn-domain>
	//   URL(fileKey) 直接返回 URLPrefix + "/" + fileKey
	//   必填,否则 URL() 返回空串
	URLPrefix string

	// Hosts 入库图片 URL 允许的 host 列表(不含 scheme)
	//   用于 ValidateURL 时校验前端传入的 URL 不指向外网
	Hosts []string `json:",optional"`
}

// Client OSS 客户端(值类型, 无状态)
// stsCacheMutex + stsCache 用于缓存 STS 凭证, 防止高并发打爆上游 STS 配额
type Client struct {
	cfg Config

	stsCacheMutex sync.Mutex
	stsCache      *STSCredential
	stsCacheAt    time.Time
}

// NewClient 构造 OSS 客户端
func NewClient(cfg Config) *Client {
	return &Client{cfg: cfg}
}

// Config 返回配置(只读场景, logic 层做拼 host 用)
func (c *Client) Config() Config {
	return c.cfg
}

// URL 把 file_key 拼成完整访问 URL
//   - fileKey 可以是纯 key("post/1/abc.jpg")也可以是完整 URL(会自动提取 path)
//   - 纯拼接,不做签名,不调外部服务
//   - 空 key 返空串
//   - URLPrefix 未配置时返空串(让上层走降级或报错)
func (c *Client) URL(fileKey string) string {
	if fileKey == "" {
		return ""
	}
	key := extractFileKey(fileKey)
	prefix := strings.TrimRight(c.cfg.URLPrefix, "/")
	if prefix == "" {
		return ""
	}
	return prefix + "/" + key
}

// URLs 批量转 URL, 等价于 for + URL
//   - 入参 keys 可以是纯 key 或完整 URL, 空字符串原样保留
//   - 出参与入参等长, 顺序一致
//   - 业务侧 list 场景推荐用法: l.svcCtx.OSS.Client.URLs(avatarKeys)
func (c *Client) URLs(keys []string) []string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = c.URL(k)
	}
	return out
}

// UploadHost 返回前端直传 OSS 用的目标地址(OSS bucket 域名)
//   - UploadHost 优先, 留空兜底用 https://bucket.endpoint
func (c *Client) UploadHost() string {
	host := strings.TrimRight(c.cfg.UploadHost, "/")
	if host == "" {
		host = fmt.Sprintf("https://%s.%s", c.cfg.Bucket, c.cfg.Endpoint)
	}
	return host
}

// extractFileKey 从 URL 或纯 key 里抠出 file key
//   - "https://bucket.oss-cn-guangzhou.aliyuncs.com/post/1/abc.jpg" → "post/1/abc.jpg"
//   - "post/1/abc.jpg" → "post/1/abc.jpg"
//   - "" → ""
func extractFileKey(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Path == "" {
		// 解析失败或没有 path, 当成纯 key 返回
		return raw
	}
	return strings.TrimPrefix(u.Path, "/")
}
