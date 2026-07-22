// Package jwtx 提供 JWT 签发、解析与 ctx 业务侧解析工具。
//
// 业务侧调用约定:
//   - 生成 token  使用 GenerateAccessToken / GenerateRefreshToken / GenerateAdminAccessToken
//   - 从 ctx 取用户身份 使用 FromContext / GetOpenid / GetAdminIdentity
//   - 需要 user.id 时(写外键)调 svcCtx.UserModel.FindOneByOpenid 反查
//   - 业务侧错误码映射 优先 errors.Is(err, ErrUnauthorized / ErrInvalidClaim)
//
// 设计约定:
//   - JWT 主载荷为 openid(业务标识符),不用自增 user.id
//   - DB 主键仍是 user.id(自增),openid 是 UNIQUE 业务标识
//   - C 端 / Admin 端各用一套 token:GenerateAdminAccessToken 不写 openid,
//     业务侧用 GetAdminIdentity 校验 role=admin
package jwtx

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/golang-jwt/jwt/v4"
)

// ===================== 常量 =====================

// ctxKeyOpenid / ctxKeyUserId / ctxKeyUsername / ctxKeyRole 是 go-zero JWT 中间件注入到 ctx 的 claim 名。
//   - openid:   C 端业务标识符
//   - userId:   C 端 user.id 或 Admin 端 admin.id
//   - username: Admin 端业务标识符
//   - role:     角色标识(user / admin / dev)
const (
	ctxKeyOpenid   = "openid"
	ctxKeyUserId   = "userId"
	ctxKeyUsername = "username"
	ctxKeyRole     = "role"
)

// ===================== 错误（sentinel）=====================

var (
	// ErrUnauthorized ctx 中无 openid/userId（用户未登录 / 路由未配 JWT）
	ErrUnauthorized = errors.New("jwtx: 未登录")

	// ErrInvalidClaim openid/userId 声明类型无法解析（框架异常 / JWT 损坏）
	ErrInvalidClaim = errors.New("jwtx: JWT 声明解析失败")
)

// ===================== 类型 =====================

// Claims 业务侧 JWT 载荷
//   - Openid:   微信 openid（C 端业务标识符）
//   - UserId:   自增 id（C 端用户 id 或 Admin 端 admin id）
//   - Username: Admin 端 username（C 端不用）
//   - Role:     角色（user / admin / dev）
type Claims struct {
	Openid   string
	UserId   int64
	Username string
	Role     string
}

// ===================== 公开 API =====================

// GenerateAccessToken 生成 C 端用户访问令牌
//   - secret:    密钥
//   - openid:    微信 openid（业务标识符）
//   - userId:    自增 id（可选，传 0 即可）
//   - role:      角色标识（user / dev）
//   - expireSec: 有效期（秒）
//
// C 端载荷：{openid, userId, role, iat, exp}
func GenerateAccessToken(secret, openid string, userId int64, role string, expireSec int64) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"openid": openid,
		"role":   role,
		"iat":    now.Unix(),
		"exp":    now.Add(time.Duration(expireSec) * time.Second).Unix(),
	}
	if userId > 0 {
		claims["userId"] = userId
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// GenerateAdminAccessToken 生成管理后台访问令牌
//   - secret:    密钥（用 Config.AdminAuth.AccessSecret）
//   - adminId:   admin 表主键
//   - username:  业务标识符（admin 端无 openid 列,用 username 当业务标识符）
//   - role:      "admin"
//   - expireSec: 有效期（秒）
//
// Admin 载荷：{userId=adminId, username, role="admin", iat, exp}
//   - 不写 openid 字段,避免与 C 端 token 混淆
func GenerateAdminAccessToken(secret string, adminId int64, username string, role string, expireSec int64) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"userId":   adminId,
		"username": username,
		"role":     role,
		"iat":      now.Unix(),
		"exp":      now.Add(time.Duration(expireSec) * time.Second).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// GenerateRefreshToken 生成刷新令牌（只含 openid，不含 role）
func GenerateRefreshToken(secret, openid string, expireSec int64) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"openid": openid,
		"iat":    now.Unix(),
		"exp":    now.Add(time.Duration(expireSec) * time.Second).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(secret))
}

// ParseToken 解析并校验原始 token 字符串（返回 MapClaims）
//   - 多用于自定义 token 校验场景
//   - 业务侧推荐用 FromContext / GetOpenid（自动兼容 json.Number）
func ParseToken(tokenStr, secret string) (jwt.MapClaims, error) {
	token, err := jwt.Parse(tokenStr, func(token *jwt.Token) (interface{}, error) {
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	if claims, ok := token.Claims.(jwt.MapClaims); ok && token.Valid {
		return claims, nil
	}
	return nil, jwt.ErrSignatureInvalid
}

// FromContext 从 ctx 解析 JWT 业务载荷
//   - 优先取 openid(业务标识符),所有业务侧逻辑基于 openid
//   - Admin 端 token 用 userId + username + role=admin 表示
//   - 失败返回 ErrUnauthorized(ctx 无 openid/userId) 或 ErrInvalidClaim(类型不对)
//   - role 字段缺失时为空字符串(不报错)
//   - 调用方可用 errors.Is(err, jwtx.ErrUnauthorized) 区分
//
// 注意:jwtx 不做 DevMode fallback,业务侧需要 dev 旁路(如 OSS 联调)自行判断 c.DevMode
func FromContext(ctx context.Context) (*Claims, error) {
	openid := stringFromCtx(ctx, ctxKeyOpenid)
	username := stringFromCtx(ctx, ctxKeyUsername)
	role := stringFromCtx(ctx, ctxKeyRole)

	var userId int64
	if v := ctx.Value(ctxKeyUserId); v != nil {
		if uid, err := toInt64(v); err == nil {
			userId = uid
		}
	}

	if openid == "" {
		// admin 端:必有 userId + username + role=admin
		if userId > 0 && username != "" {
			return &Claims{UserId: userId, Username: username, Role: role}, nil
		}
		return nil, ErrUnauthorized
	}

	return &Claims{Openid: openid, UserId: userId, Role: role}, nil
}

// GetOpenid 从 ctx 提取 C 端 openid（业务标识符）
//   - admin token 没 openid 字段,会返回 ErrUnauthorized
func GetOpenid(ctx context.Context) (string, error) {
	c, err := FromContext(ctx)
	if err != nil {
		return "", err
	}
	if c.Openid == "" {
		return "", ErrUnauthorized
	}
	return c.Openid, nil
}

// GetAdminIdentity 从 ctx 提取 admin 身份
//   - 强制 role=admin 且有 userId+username,否则 ErrUnauthorized
//   - 防止 admin token 跟 user token 互串
func GetAdminIdentity(ctx context.Context) (adminId int64, username string, err error) {
	c, err := FromContext(ctx)
	if err != nil {
		return 0, "", err
	}
	if c.Role != "admin" {
		return 0, "", ErrUnauthorized
	}
	if c.UserId <= 0 || c.Username == "" {
		return 0, "", ErrUnauthorized
	}
	return c.UserId, c.Username, nil
}

// ===================== 内部辅助 =====================

// stringFromCtx 从 ctx 取字符串，类型不匹配返回空
func stringFromCtx(ctx context.Context, key string) string {
	v := ctx.Value(key)
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	// go-zero jwt.WithJSONNumber 偶尔会把字符串塞 json.Number
	if n, ok := v.(json.Number); ok {
		return n.String()
	}
	if s, ok := v.(fmt.Stringer); ok {
		return s.String()
	}
	// 兜底：int/float 等转字符串
	return fmt.Sprintf("%v", v)
}

// toInt64 兼容 json.Number / int64 / int / float64 / string
func toInt64(v any) (int64, error) {
	switch val := v.(type) {
	case json.Number:
		n, err := val.Int64()
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrInvalidClaim, err)
		}
		return n, nil
	case int64:
		return val, nil
	case int:
		return int64(val), nil
	case float64:
		return int64(val), nil
	case string:
		n, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("%w: %v", ErrInvalidClaim, err)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("%w: 类型 %T", ErrInvalidClaim, v)
	}
}
