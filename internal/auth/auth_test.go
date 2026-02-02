package auth

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

// 1. 正常签发验证
func TestMakeAndValidateJWT(t *testing.T) {
	userID := uuid.New()
	secret := "service-secret-key"
	duration := time.Hour

	token, err := MakeJWT(userID, secret, duration)
	if err != nil {
		t.Fatalf("创建 JWT 失败: %v", err)
	}

	gotID, err := ValidateJWT(token, secret)
	if err != nil {
		t.Fatalf("验证合法 JWT 失败: %v", err)
	}

	if gotID != userID {
		t.Errorf("期望的 userID 为 %v, 实际得到 %v", userID, gotID)
	}
}

// 2. 过期逻辑
func TestExpiredJWT(t *testing.T) {
	userID := uuid.New()
	secret := "service-secret-key"
	// 关键：设置一个已经过期的时间
	duration := -time.Minute

	token, _ := MakeJWT(userID, secret, duration)
	_, err := ValidateJWT(token, secret)

	if err == nil {
		t.Error("预期过期 Token 会报错，但 ValidateJWT 返回了 nil")
	}
}

// 3. 密钥错误
func TestWrongSecretJWT(t *testing.T) {
	userID := uuid.New()
	secret := "correct-secret"
	wrongSecret := "hacker-secret"

	token, _ := MakeJWT(userID, secret, time.Hour)
	_, err := ValidateJWT(token, wrongSecret)

	if err == nil {
		t.Error("预期使用错误密钥会报错，但验证通过了")
	}
}
