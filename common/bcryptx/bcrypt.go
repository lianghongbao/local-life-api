package bcryptx

import "golang.org/x/crypto/bcrypt"

// Hash 密码加密（bcrypt cost=10）
func Hash(password string) (string, error) {
    b, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
    if err != nil {
        return "", err
    }
    return string(b), nil
}

// Verify 密码校验（返回 nil=密码正确）
func Verify(hash, password string) error {
    return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
}
