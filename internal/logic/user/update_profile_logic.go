package user

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"

	"local-life-api/common/jwtx"
	"local-life-api/internal/errorx"
	"local-life-api/internal/model"
	"local-life-api/internal/svc"
	"local-life-api/internal/types"

	"github.com/zeromicro/go-zero/core/logx"
)

type UpdateProfileLogic struct {
	logx.Logger
	ctx    context.Context
	svcCtx *svc.ServiceContext
}

func NewUpdateProfileLogic(ctx context.Context, svcCtx *svc.ServiceContext) *UpdateProfileLogic {
	return &UpdateProfileLogic{
		Logger: logx.WithContext(ctx),
		ctx:    ctx,
		svcCtx: svcCtx,
	}
}

// ---------- nickname 校验 ----------

// MaxNicknameRune 昵称最长字符数（按 rune 计，emoji 不算超）
const MaxNicknameRune = 16

// ErrNicknameTooLong 昵称超过 16 字符
//   - 调用方必须翻译为 errorx.ErrInvalidParam 等对外错误
var ErrNicknameTooLong = fmt.Errorf("nickname too long (max %d runes)", MaxNicknameRune)

// validateNicknameLen 校验昵称 rune 长度
//   - 空字符串视为不更新（调用方跳过），不算错误
func validateNicknameLen(s string) error {
	if s == "" {
		return nil
	}
	if utf8.RuneCountInString(s) > MaxNicknameRune {
		return ErrNicknameTooLong
	}
	return nil
}

// ---------- nickname 清洗 ----------

var (
	htmlTagRe     = regexp.MustCompile(`<[^>]*>`)
	controlCharRe = regexp.MustCompile(`\p{Cc}`)
)

// stripHTML 去掉输入里的 HTML 标签（防御 XSS）
func stripHTML(s string) string {
	return htmlTagRe.ReplaceAllString(s, "")
}

// stripControlChars 去掉输入里的 Unicode 经典控制字符（防御日志注入 / 终端渲染问题）
//   - 范围：\p{Cc} = ASCII 0x00-0x1F + 0x7F-0x9F
//   - 不过滤 \p{Cf}（ZWJ 等格式字符），避免破坏 emoji 组合序列
func stripControlChars(s string) string {
	return controlCharRe.ReplaceAllString(s, "")
}

// ---------- handler ----------

// UpdateProfile C 端更新用户资料（昵称 + 头像）
//
//  1. JWT 取 openid(业务标识符)
//  2. 查 user 表
//  3. 部分更新：nickname / avatar 非空才改
//  4. model.Update 写库 + 清缓存
func (l *UpdateProfileLogic) UpdateProfile(req *types.UpdateProfileRequest) (resp *types.UserProfileResponse, err error) {
	// 1. JWT 取 openid(业务标识符)
	openid, err := jwtx.GetOpenid(l.ctx)
	if err != nil {
		l.Errorf("JWT 解析失败: %v", err)
		return nil, errorx.ErrUnauthorized
	}

	// 2. 用 openid 查 user
	user, err := l.svcCtx.UserModel.FindOneByOpenid(l.ctx, openid)
	if err != nil {
		if errors.Is(err, model.ErrNotFound) {
			return nil, errorx.ErrUnauthorized
		}
		l.Errorf("查询用户失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}

	// 3. 部分更新：只覆盖有值的字段
	if req.Nickname != "" {
		if err := validateNicknameLen(req.Nickname); err != nil {
			l.Errorf("昵称超长: openid=%s, len=%d", openid, utf8.RuneCountInString(req.Nickname))
			return nil, errorx.ErrInvalidParam
		}
		// 先剥 HTML 标签，再剥控制字符（顺序无关，但习惯先结构后内容）
		user.Nickname = stripControlChars(stripHTML(req.Nickname))
	}
	if req.Avatar != "" {
		user.Avatar = req.Avatar
	}

	// 4. 写到 DB + 清缓存
	if err = l.svcCtx.UserModel.Update(l.ctx, user); err != nil {
		l.Errorf("更新用户资料失败: openid=%s, err=%v", openid, err)
		return nil, errorx.ErrInternal
	}

	l.Infof("更新用户资料成功: openid=%s", openid)

	// Avatar file_key → 完整 URL(无需签名, 永久有效)
	avatarURL := user.Avatar
	if user.Avatar != "" {
		avatarURL = l.svcCtx.OSS.Client.URL(user.Avatar)
	}

	return &types.UserProfileResponse{
		Id:        int64(user.Id),
		Nickname:  user.Nickname,
		Avatar:    avatarURL,
		Phone:     user.Phone,
		Status:    int(user.Status),
		CreatedAt: user.CreatedAt.Format("2006-01-02 15:04:05"),
	}, nil
}
