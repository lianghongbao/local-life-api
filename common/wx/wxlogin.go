package wx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/rest/httpc"
)

// 文档: https://developers.weixin.qq.com/miniprogram/dev/api-backend/open-api/login/auth.code2Session.html

// 微信接口常量
const (
	code2SessionURL = "https://api.weixin.qq.com/sns/jscode2session"
	// code2Session 超时 3s
	defaultTimeout = 3 * time.Second
)

// 预定义错误
var (
	ErrCodeEmpty      = errors.New("wx: 登录凭证 code 不能为空")
	ErrAppIDEmpty     = errors.New("wx: 微信小程序 AppID 未配置")
	ErrAppSecretEmpty = errors.New("wx: 微信小程序 AppSecret 未配置")
	ErrEmptyOpenID    = errors.New("wx: 微信接口未返回 openid")
)

// WxSession 微信 code2Session 响应结构体
type WxSession struct {
	OpenID     string `json:"openid"`
	UnionID    string `json:"unionid,omitempty"`
	SessionKey string `json:"session_key"` // 敏感字段，禁止对外输出
	ErrCode    int    `json:"errcode"`
	ErrMsg     string `json:"errmsg"`
}

// code2SessionReq 微信 code2Session 请求参数
//   - 必须用 struct（不能 map），httpc.Do 内部 reflection 需要 struct tag
type code2SessionReq struct {
	AppID     string `form:"appid"`
	Secret    string `form:"secret"`
	JSCode    string `form:"js_code"`
	GrantType string `form:"grant_type"`
}

// Code2Session 小程序登录：js_code 换取 openid + session_key
func Code2Session(ctx context.Context, appID, appSecret, code string) (*WxSession, error) {
	// 1. 入参校验
	if appID == "" {
		return nil, ErrAppIDEmpty
	}
	if appSecret == "" {
		return nil, ErrAppSecretEmpty
	}
	if code == "" {
		return nil, ErrCodeEmpty
	}

	// 2. 超时控制
	ctx, cancel := context.WithTimeout(ctx, defaultTimeout)
	defer cancel()

	// 3. 调微信 code2Session
	resp, err := httpc.Do(ctx, "GET", code2SessionURL, code2SessionReq{
		AppID:     appID,
		Secret:    appSecret,
		JSCode:    code,
		GrantType: "authorization_code",
	})
	if err != nil {
		logx.WithContext(ctx).Errorf("微信 code2Session 请求失败: %v", err)
		return nil, fmt.Errorf("微信接口请求失败: %w", err)
	}
	defer resp.Body.Close()

	// 4. 读响应
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取微信响应失败: %w", err)
	}

	// 5. 解析 JSON
	var session WxSession
	if err = json.Unmarshal(body, &session); err != nil {
		logx.WithContext(ctx).Errorf("微信 code2Session 解析 JSON 失败, body: %s, err: %v", string(body), err)
		return nil, fmt.Errorf("解析微信响应失败: %w", err)
	}

	// 6. 业务错误码
	if session.ErrCode != 0 {
		logx.WithContext(ctx).Errorf("微信 code2Session 业务错误, errcode: %d, errmsg: %s", session.ErrCode, session.ErrMsg)
		return nil, fmt.Errorf("微信业务错误 errcode=%d, errmsg=%s", session.ErrCode, session.ErrMsg)
	}

	// 7. 兜底校验 openid
	if session.OpenID == "" {
		return nil, ErrEmptyOpenID
	}

	return &session, nil
}
