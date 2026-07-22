package oss

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"time"
)

// v4Material V4 签名的中间材料（policy + signing key + 拼好的字符串）
type v4Material struct {
	policy       string
	stringToSign string
	signingKey   []byte
	date         string    // 20060102
	dateTime     string    // 20060102T150405Z
	expiration   time.Time
	credential   string    // "<ak>/<date>/<region>/oss/aliyun_v4_request"
}

// buildV4 拼 V4 签名材料 + 算 signature
//   - Policy conditions 限制上传到指定 bucket + 限定签名版本/凭证/时间/安全令牌
//   - signing key: 4 次 HMAC-SHA256("aliyun_v4" + secret → date → region → product → "aliyun_v4_request")
//   - signature: hex(HMAC-SHA256(signingKey, base64(policy)))
//
// dir 用于在 policy 中限制 key 必须以指定前缀开头(防越权上传到别人目录),
// 传空字符串时 policy 仍声明 key 字段(任意值)
func (c *Client) buildV4(cred *STSCredential, dir string, now time.Time) (material v4Material, signature string) {
	nowUTC := now.UTC()
	date := nowUTC.Format("20060102")
	dateTime := nowUTC.Format("20060102T150405Z")
	expiration := nowUTC.Add(defaultPolicyExpiration)
	credential := fmt.Sprintf("%s/%s/%s/%s/aliyun_v4_request", cred.AccessKeyId, date, c.cfg.Region, product)

	policyMap := map[string]any{
		"expiration": expiration.Format("2006-01-02T15:04:05.000Z"),
		"conditions": []any{
			// 顺序: bucket → x-oss-* 头部 → key → success_action_status
			// 这是 OSS V4 PostObject 的官方规范顺序, form 字段顺序也要与之对应
			map[string]string{"bucket": c.cfg.Bucket},
			map[string]string{"x-oss-signature-version": SignatureVersion},
			map[string]string{"x-oss-credential": credential},
			map[string]string{"x-oss-date": dateTime},
			// STS 临时凭证的 security token 必须用对象形式声明, value 为实际 token
			map[string]string{"x-oss-security-token": cred.SecurityToken},
			// key 限定在前端上传时使用的 dir 前缀下, 防越权上传
			[]any{"starts-with", "$key", dir},
			// success_action_status 必须声明,否则 OSS 视为未授权多余字段
			[]any{"eq", "$success_action_status", "200"},
		},
	}
	policyJSON, _ := json.Marshal(policyMap)
	stringToSign := base64.StdEncoding.EncodeToString(policyJSON)

	signingKey := c.deriveSigningKey(cred.AccessKeySecret, date)
	signature = hmacHex(signingKey, stringToSign)

	return v4Material{
		policy:       stringToSign,
		stringToSign: stringToSign,
		signingKey:   signingKey,
		date:         date,
		dateTime:     dateTime,
		expiration:   expiration,
		credential:   credential,
	}, signature
}

// deriveSigningKey 阿里云 V4 签名:4 次 HMAC-SHA256 派生 signing key
//   "aliyun_v4" + secret → date → region → product → "aliyun_v4_request"
func (c *Client) deriveSigningKey(secret, date string) []byte {
	hmacHash := func() hash.Hash { return sha256.New() }

	h1 := hmac.New(hmacHash, []byte("aliyun_v4"+secret))
	h1.Write([]byte(date))
	h1Key := h1.Sum(nil)

	h2 := hmac.New(hmacHash, h1Key)
	h2.Write([]byte(c.cfg.Region))
	h2Key := h2.Sum(nil)

	h3 := hmac.New(hmacHash, h2Key)
	h3.Write([]byte(product))
	h3Key := h3.Sum(nil)

	h4 := hmac.New(hmacHash, h3Key)
	h4.Write([]byte("aliyun_v4_request"))
	return h4.Sum(nil)
}

// hmacHex HMAC-SHA256 后转 hex
func hmacHex(key []byte, data string) string {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return hex.EncodeToString(h.Sum(nil))
}
