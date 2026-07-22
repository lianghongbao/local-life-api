package oss

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/zeromicro/go-zero/core/logx"
)

// UploadTokenResult 前端直传 OSS 所需的全部信息
type UploadTokenResult struct {
	// Host       上传地址(不含 path), 例 "https://my-bucket.oss-cn-hangzhou.aliyuncs.com"
	Host string
	// Dir        建议的 key 前缀, 由调用方传入(例如 "post/123/")
	Dir string
	// FileKey    推荐的文件 key(已含 ext, 不含 host);前端可覆盖
	//            命名规则: <dir><uuid>.<ext>
	FileKey string
	// Policy     Base64 编码后的 Policy 字符串
	Policy string
	// ExpireAt   整套凭证过期时间(Unix 秒)
	ExpireAt int64
	// STSCred    STS 临时凭证(AccessKeyId / SecurityToken / Expiration)
	STSCred STSCredential
	// Credential x-oss-credential Header 值
	Credential string
	// DateTime   x-oss-date Header 值
	DateTime string
	// Signature  x-oss-signature Header 值
	Signature string
}

// Options 一次上传凭证的请求参数
//   - 通用能力, 不带任何业务含义
//   - Dir 由调用方决定(例如 "post/123/")
//   - FileType 透传给调用方做日志(不影响签名)
//   - Size <= 0 时跳过业务大小校验(oss 包本身不再校验大小)
type Options struct {
	// FileType 业务文件类型(image/video/avatar), 仅用于日志
	FileType string
	// Dir 推荐的文件 key 前缀, 必须以 "/" 结尾
	Dir string
	// Ext 文件后缀(不带点), 由 oss 包 normalize
	Ext string
	// Size 文件大小(字节), 透传给调用方做大小校验
	Size int64
	// AllowedExts 允许的文件后缀白名单, nil/空表示不校验
	AllowedExts []string
}

// GenerateUploadToken 生成 OSS V4 直传所需的临时凭证 + 签名头
//
//  1. 校验 OSS 配置
//  2. 校验后缀白名单
//  3. 调 STS AssumeRole 拿临时凭证
//  4. 拼 V4 Policy + signing key + signature
//  5. 生成 FileKey(以 Dir + uuid + ext 拼装)
//  6. 返回
func (c *Client) GenerateUploadToken(ctx context.Context, opts Options) (*UploadTokenResult, error) {
	// 1. 配置校验
	if c.cfg.Bucket == "" || c.cfg.Region == "" {
		logx.WithContext(ctx).Errorf("oss: 配置缺失 bucket=%s, region=%s", c.cfg.Bucket, c.cfg.Region)
		return nil, ErrConfigIncomplete
	}

	// 2. 后缀白名单
	ext := NormalizeExt(opts.Ext)
	if err := ValidateExt(ext, opts.AllowedExts); err != nil {
		logx.WithContext(ctx).Errorf("oss: 后缀不在白名单内: ext=%s", ext)
		return nil, err
	}

	// 3. STS 临时凭证
	cred, err := c.fetchSTSCredential(ctx)
	if err != nil {
		return nil, err
	}

	// 4. file key 前缀(由调用方提供 Dir, 需先归一化才能写入 policy)
	dir := opts.Dir
	if dir != "" && dir[len(dir)-1] != '/' {
		dir += "/"
	}

	// 5. V4 签名材料(policy 用 dir 限定上传前缀, 防越权上传到别人目录)
	now := time.Now().UTC()
	material, signature := c.buildV4(cred, dir, now)

	// 6. file key(Dir + uuid 防冲突)
	fileKey := fmt.Sprintf("%s%s.%s", dir, uuid.NewString(), ext)
	// 前端直传 OSS 用的目标地址
	host := c.UploadHost()
	// 拼好的访问 URL(URLPrefix + fileKey, 永久有效)
	url := c.URL(fileKey)

	logx.WithContext(ctx).Infof("oss: 生成上传凭证 fileType=%s, fileKey=%s, url=%s, expireAt=%d",
		opts.FileType, fileKey, url, material.expiration.Unix())

	return &UploadTokenResult{
		Host:       host,
		Dir:        dir,
		FileKey:    fileKey,
		Policy:     material.policy,
		ExpireAt:   material.expiration.Unix(),
		STSCred:    *cred,
		Credential: material.credential,
		DateTime:   material.dateTime,
		Signature:  signature,
	}, nil
}
