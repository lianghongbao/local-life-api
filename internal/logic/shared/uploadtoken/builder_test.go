package uploadtoken

import (
	"context"
	"errors"
	"testing"

	"local-life-api/internal/errorx"
)

// TestBuilder_NilClient 验证 nil client 时直接报错(防止下游 panic)
func TestBuilder_NilClient(t *testing.T) {
	b := NewBuilder(nil)
	_, err := b.Build(context.Background(), Input{Biz: "banner", UID: 0})
	if err == nil {
		t.Fatal("expected error for nil client, got nil")
	}
	if !errors.Is(err, errorx.ErrInternal) {
		t.Fatalf("expected ErrInternal, got %v", err)
	}
}

// TestBuilder_BadBiz 验证 biz 不合法时返 ErrInvalidParam
func TestBuilder_BadBiz(t *testing.T) {
	b := NewBuilder(nil) // nil 也行,因为 biz 校验在 BuildFileKeyDir 里先做
	_, err := b.Build(context.Background(), Input{Biz: "invalid_biz", UID: 0})
	if err == nil {
		t.Fatal("expected error for bad biz, got nil")
	}
	if !errors.Is(err, errorx.ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
}

// TestBuilder_PersonalBiz_MissingUID 验证个人资源(biz=post)未传 uid 时返 ErrInvalidParam
func TestBuilder_PersonalBiz_MissingUID(t *testing.T) {
	b := NewBuilder(nil)
	_, err := b.Build(context.Background(), Input{Biz: "post", UID: 0})
	if err == nil {
		t.Fatal("expected error for missing uid, got nil")
	}
	if !errors.Is(err, errorx.ErrInvalidParam) {
		t.Fatalf("expected ErrInvalidParam, got %v", err)
	}
}

// TestBuilder_PlatformBiz_ZeroUIDOK 验证平台资源(biz=banner/notice)uid=0 时 BuildFileKeyDir 不报错
// 这里只走 BuildFileKeyDir 路径(会被 nil client 拦截), 用于锁定"biz 合法 + uid=0 不在 BuildFileKeyDir 报"的语义。
// 真正的 STS 联调留给集成测试。
func TestBuilder_PlatformBiz_ZeroUIDOK(t *testing.T) {
	b := NewBuilder(nil)
	_, err := b.Build(context.Background(), Input{Biz: "notice", UID: 0})
	if err == nil {
		t.Fatal("expected error from nil client, got nil")
	}
	if !errors.Is(err, errorx.ErrInternal) {
		t.Fatalf("expected ErrInternal (from nil client), got %v", err)
	}
}
