package oss

import (
	"context"
	"fmt"
	"time"

	"github.com/aliyun/credentials-go/credentials"
	"github.com/zeromicro/go-zero/core/logx"
)

// STSCredential STS 临时凭证（前端上传要用的）
type STSCredential struct {
	AccessKeyId     string
	AccessKeySecret string
	SecurityToken   string
	Expiration      string // ISO8601,前端用来判断是否快过期
}

// fetchSTSCredential 拿 STS 临时凭证（带本地缓存）
//   - 缓存有效期 defaultSTSCacheTTL(30 分钟 < STS 1 小时)
//   - 高并发场景下把上游 STS AssumeRole QPS 从 N 压到 ~N/1800
//   - 失败时记录详细日志（包含 endpoint / roleArn 便于排查）
func (c *Client) fetchSTSCredential(ctx context.Context) (*STSCredential, error) {
	if c.cfg.AccessKeyID == "" || c.cfg.AccessKeySecret == "" || c.cfg.RoleArn == "" {
		return nil, ErrConfigIncomplete
	}

	// 1. 先查缓存(双重检查锁)
	c.stsCacheMutex.Lock()
	if c.stsCache != nil && time.Since(c.stsCacheAt) < defaultSTSCacheTTL {
		cred := c.stsCache
		c.stsCacheMutex.Unlock()
		return cred, nil
	}
	c.stsCacheMutex.Unlock()

	// 2. 缓存未命中或已过期, 重新调 STS
	sessionExpire := int(defaultSTSSessionDuration.Seconds())
	cred, err := credentials.NewCredential(
		new(credentials.Config).
			SetType("ram_role_arn").
			SetAccessKeyId(c.cfg.AccessKeyID).
			SetAccessKeySecret(c.cfg.AccessKeySecret).
			SetRoleArn(c.cfg.RoleArn).
			SetRoleSessionName("oss-sts-upload").
			SetRoleSessionExpiration(sessionExpire),
	)
	if err != nil {
		logx.WithContext(ctx).Errorf("oss: credentials.NewCredential 失败: %v", err)
		return nil, fmt.Errorf("%w: NewCredential: %v", ErrAssumeRoleFailed, err)
	}

	cm, err := cred.GetCredential()
	if err != nil {
		logx.WithContext(ctx).Errorf("oss: STS AssumeRole 失败 roleArn=%s: %v", c.cfg.RoleArn, err)
		return nil, fmt.Errorf("%w: GetCredential: %v", ErrAssumeRoleFailed, err)
	}

	if cm.AccessKeyId == nil || cm.AccessKeySecret == nil || cm.SecurityToken == nil {
		logx.WithContext(ctx).Errorf("oss: STS 返回凭证字段为空: %+v", cm)
		return nil, fmt.Errorf("%w: 凭证字段为空", ErrAssumeRoleFailed)
	}

	// SDK 字段都是 *string,统一解引用
	expiration := time.Now().Add(defaultSTSSessionDuration).UTC().Format(time.RFC3339)
	newCred := &STSCredential{
		AccessKeyId:     *cm.AccessKeyId,
		AccessKeySecret: *cm.AccessKeySecret,
		SecurityToken:   *cm.SecurityToken,
		Expiration:      expiration,
	}

	// 3. 写回缓存(加锁, 即使并发也只更新一次)
	c.stsCacheMutex.Lock()
	c.stsCache = newCred
	c.stsCacheAt = time.Now()
	c.stsCacheMutex.Unlock()

	logx.WithContext(ctx).Infof("oss: STS 凭证已缓存 expiry=%s", newCred.Expiration)
	return newCred, nil
}

// FetchSTSCredential 导出方法: 让业务 logic 拿到 STS 凭证(给 SignGetObjectURL/SignURLs 用)
//   - 内部委托给 fetchSTSCredential, 同样享受 30 分钟本地缓存
//   - 返回值是 *STSCredential(指针), 业务侧用值类型接收即可
func (c *Client) FetchSTSCredential(ctx context.Context) (STSCredential, error) {
	cred, err := c.fetchSTSCredential(ctx)
	if err != nil {
		return STSCredential{}, err
	}
	return *cred, nil
}
