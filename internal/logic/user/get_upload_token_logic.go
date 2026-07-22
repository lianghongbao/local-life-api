package user

import (
	"context"

	"local-life-api/common/jwtx"
	"local-life-api/common/oss"
	"local-life-api/internal/errorx"
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

// GetUploadToken 生成 OSS V4 直传所需的临时凭证 + 签名头
//
// 业务编排(本层负责):
//  1. 从 JWT 取 openid → 反查 user.id 用于拼 file key 目录
//  2. 校验 biz 合法性 + 拼 file_type 策略
//  3. 调 shared/uploadtoken.Builder 拿 STS + V4 + signed URL
//  4. 映射 View 到 types.GetUploadTokenResponse(过滤 STS AccessKeySecret)
func (l *GetUploadTokenLogic) GetUploadToken(req *types.GetUploadTokenRequest) (resp *types.GetUploadTokenResponse, err error) {
	// 1. 从 JWT 取 openid → 反查 user.id
	openid, err := jwtx.GetOpenid(l.ctx)
	if err != nil {
		l.Errorf("从 ctx 取 openid 失败: %v", err)
		return nil, errorx.ErrUnauthorized
	}
	userRow, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		l.Errorf("查询用户失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrUnauthorized
	}
	uid := int64(userRow.Id)

	// 2. 拿 file_type 对应的业务策略(后缀白名单 + 大小上限)
	policy, err := ResolveFileTypePolicy(req.FileType)
	if err != nil {
		l.Errorf("file_type 不合法: fileType=%s, err=%v", req.FileType, err)
		return nil, err
	}
	if err = ValidateUploadSize(req.FileType, req.Size); err != nil {
		l.Errorf("文件大小超限: fileType=%s, size=%d, err=%v", req.FileType, req.Size, err)
		return nil, err
	}

	// 3. 调 shared builder(单例 client, 保 STS 缓存)
	builder := uploadtoken.NewBuilder(l.svcCtx.OSS.Client)
	view, err := builder.Build(l.ctx, uploadtoken.Input{
		FileType:    req.FileType,
		Biz:         req.Biz,
		Ext:         req.Ext,
		Size:        req.Size,
		UID:         uid,
		AllowedExts: policy.AllowedExts,
	})
	if err != nil {
		return nil, err
	}

	// 4. 映射 View → types.GetUploadTokenResponse
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
