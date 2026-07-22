// Package uploadtoken 给 user / admin 两侧 HTTP handler 共用的"OSS 上传凭证签发"核心能力。
//   - 接收已校验过的入参(fileType/biz/ext/size + 可选 uid)
//   - 调 oss.Client 拿 STS + V4 签名(给前端直传 OSS 用)
//   - 同时算好访问 URL(URLPrefix + fileKey)
//   - 返回与 types.GetUploadTokenResponse 同构的 view 对象, 由两侧 handler 各自映射 DTO
package uploadtoken

import (
	"context"
	"errors"
	"fmt"

	"local-life-api/common/oss"
	"local-life-api/internal/errorx"
	"local-life-api/internal/logic/shared/uploadpolicy"
)

// Input 一次签发请求的输入(已通过 types 校验 + biz 合法性 + 大小校验)
type Input struct {
	FileType    string   // image/video/avatar, 仅用于日志
	Biz         string   // post/shop/avatar/banner/notice
	Ext         string   // 文件后缀(不带点)
	Size        int64    // 文件大小, <=0 不校验
	UID         int64    // 个人资源必传, 平台资源(banner/notice)传 0
	AllowedExts []string // 后缀白名单, 由 FileTypePolicies 决定
}

// View 一次签发请求的结果(与 types.GetUploadTokenResponse 字段同构)
type View struct {
	Host    string // 前端直传 OSS 用的目标地址
	FileKey string // 文件 key(纯路径, 不带 host)
	Url     string // 拼好的访问 URL(URLPrefix + fileKey, 永久有效)
	// Oss 字段含 STS 短期 AK + SK + Token。handler 映射 DTO 时**必须**只挑
	// AccessKeyId / SecurityToken / Expiration 三字段转发给前端,
	// 严禁 json.Marshal 整个 STSCredential —— 否则 STS SK 会随响应泄漏
	Oss        oss.STSCredential
	Credential string
	DateTime   string
	Signature  string
	Policy     string
	ExpireAt   int64
}

// Builder 调用方注入单例 oss.Client(确保 STS 凭证本地缓存复用)
type Builder struct {
	Client *oss.Client
}

func NewBuilder(client *oss.Client) *Builder {
	return &Builder{Client: client}
}

// Build 签发上传凭证, 不做业务校验(由调用方先 ValidateUploadSize / IsValidBiz)
//   - 步骤: BuildFileKeyDir → oss.GenerateUploadToken → URL
//   - 失败: 配置类 / STS 类 → ErrInternal; 入参类 → ErrInvalidParam
func (b *Builder) Build(ctx context.Context, in Input) (*View, error) {
	// 1. 拼 dir(biz/uid 校验独立于 client, 先做便于错误分类)
	dir, err := uploadpolicy.BuildFileKeyDir(in.Biz, in.UID)
	if err != nil {
		return nil, err
	}

	if b.Client == nil {
		return nil, fmt.Errorf("%w: oss client nil", errorx.ErrInternal)
	}

	// 2. 调 oss 包拿 STS + V4 + file_key(给前端直传 OSS 用)
	result, err := b.Client.GenerateUploadToken(ctx, oss.Options{
		FileType:    in.FileType,
		Dir:         dir,
		Ext:         in.Ext,
		Size:        in.Size,
		AllowedExts: in.AllowedExts,
	})
	if err != nil {
		if errors.Is(err, oss.ErrInvalidExt) {
			return nil, errorx.ErrInvalidParam
		}
		return nil, errorx.ErrInternal
	}

	return &View{
		Host:       result.Host,
		FileKey:    result.FileKey,
		Url:        b.Client.URL(result.FileKey),
		Oss:        result.STSCred,
		Credential: result.Credential,
		DateTime:   result.DateTime,
		Signature:  result.Signature,
		Policy:     result.Policy,
		ExpireAt:   result.ExpireAt,
	}, nil
}
