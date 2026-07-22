package admin

import (
	"context"

	"local-life-api/common/oss"
	"local-life-api/internal/logic/shared/uploadpolicy"
	"local-life-api/internal/logic/shared/uploadtoken"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type GetUploadTokenLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewGetUploadTokenLogic(ctx context.Context, svcCtx *svc.ServiceContext) *GetUploadTokenLogic {
	return &GetUploadTokenLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// GetUploadToken admin 侧 OSS 上传凭证签发
//   - 与 user 侧区别: admin 不绑定个人, biz=banner/notice 时 uid=0(由 BuildFileKeyDir 自动归到 biz/0/)
//   - 复用 shared/uploadtoken builder, 零 STS 签发重复
//   - 不读 ctx 的 openid(AdminAuth JWT 不挂 openid)
func (l *GetUploadTokenLogic) GetUploadToken(req *types.GetUploadTokenRequest) (resp *types.GetUploadTokenResponse, err error) {
	// 1. file_type 策略(白名单 + 大小)
	policy, err := uploadpolicy.ResolveFileTypePolicy(req.FileType)
	if err != nil {
		l.Errorf("file_type 不合法: fileType=%s, err=%v", req.FileType, err)
		return nil, err
	}
	if err = uploadpolicy.ValidateUploadSize(req.FileType, req.Size); err != nil {
		l.Errorf("文件大小超限: fileType=%s, size=%d, err=%v", req.FileType, req.Size, err)
		return nil, err
	}

	// 2. 调 shared builder(uid 固定 0, 平台级 biz 由 BuildFileKeyDir 内部处理)
	builder := uploadtoken.NewBuilder(l.svcCtx.OSS.Client)
	view, err := builder.Build(l.ctx, uploadtoken.Input{
		FileType:    req.FileType,
		Biz:         req.Biz,
		Ext:         req.Ext,
		Size:        req.Size,
		UID:         0,
		AllowedExts: policy.AllowedExts,
	})
	if err != nil {
		return nil, err
	}

	// 3. 映射 View → types.GetUploadTokenResponse
	//    注意: STS AccessKeySecret 不许漏给前端,只挑 3 个安全字段
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
