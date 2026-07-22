package admin

import (
	"context"

	"local-life-api/common/jwtx"
	"local-life-api/common/oss"
	"local-life-api/internal/errorx"
	"local-life-api/internal/logic/shared/uploadpolicy"
	"local-life-api/internal/logic/shared/uploadtoken"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type AdminGetUploadTokenLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewAdminGetUploadTokenLogic(ctx context.Context, svcCtx *svc.ServiceContext) *AdminGetUploadTokenLogic {
	return &AdminGetUploadTokenLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// AdminGetUploadToken 后台 OSS 直传凭证
//   - 与 C 端共用 builder,但用 admin JWT 里的 userId(=admin.Id) 拼 file key
//   - file key 前缀加 "admin/" 区分 C 端上传
func (l *AdminGetUploadTokenLogic) AdminGetUploadToken(req *types.GetUploadTokenRequest) (resp *types.GetUploadTokenResponse, err error) {
	// 1. 从 ctx 取 admin 身份(强制 role=admin + 有 userId+username)
	adminId, _, err := jwtx.GetAdminIdentity(l.ctx)
	if err != nil {
		l.Errorf("从 ctx 取 admin 身份失败: err=%v", err)
		return nil, errorx.ErrUnauthorized
	}
	// 用负数 UID 让 shared builder 加 "admin/" 前缀(避免与 C 端 user.id 撞)
	adminUID := -adminId

	// 2. 拿 file_type 业务策略
	policy, err := uploadpolicy.ResolveFileTypePolicy(req.FileType)
	if err != nil {
		l.Errorf("file_type 不合法: fileType=%s, err=%v", req.FileType, err)
		return nil, err
	}
	if err = uploadpolicy.ValidateUploadSize(req.FileType, req.Size); err != nil {
		l.Errorf("文件大小超限: fileType=%s, size=%d, err=%v", req.FileType, req.Size, err)
		return nil, err
	}

	// 3. 调 shared builder
	builder := uploadtoken.NewBuilder(l.svcCtx.OSS.Client)
	view, err := builder.Build(l.ctx, uploadtoken.Input{
		FileType:    req.FileType,
		Biz:         req.Biz,
		Ext:         req.Ext,
		Size:        req.Size,
		UID:         adminUID,
		AllowedExts: policy.AllowedExts,
	})
	if err != nil {
		return nil, err
	}

	// 4. 映射 View → types.GetUploadTokenResponse
	return &types.GetUploadTokenResponse{
		Host:    view.Host,
		FileKey: view.FileKey,
		Url:     view.Url,
		Oss: types.STSCredentials{
			AccessKeyId:   view.Oss.AccessKeyId,
			SecurityToken: view.Oss.SecurityToken,
			Expiration:    view.Oss.Expiration,
		},
		V4: types.V4SignatureHeaders{
			XOssSignatureVersion: oss.SignatureVersion,
			XOssCredential:       view.Credential,
			XOssDate:             view.DateTime,
			XOssSignature:        view.Signature,
		},
		Policy:   view.Policy,
		ExpireAt: view.ExpireAt,
	}, nil
}
