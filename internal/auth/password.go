package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/scrypt"
)

// 密码哈希：与 Python 原版（passlib 风格）完全兼容的 scrypt 存储格式。
//
// 存储字符串形如：
//
//	scrypt$16384$8$1$<salt_hex>$<hash_hex>
//
// 字段含义（用 '$' 分隔，共 6 段）：
//   - "scrypt"    算法标识
//   - 16384       N，CPU/内存开销参数（2^14）
//   - 8           r，块大小
//   - 1           p，并行度
//   - salt_hex    盐，十六进制编码；解码后是原始字节直接喂给 scrypt
//   - hash_hex    派生密钥，十六进制编码，长度 64 字节（128 个 hex 字符）
//
// 关键点（决定能否兼容旧库）：salt 存的是 hex，验证时必须先 hex.DecodeString
// 还原成字节再参与 scrypt 计算，而不是把 32 个 hex 字符当作 ASCII 盐。
// 这一点已用真实旧库账号（alice@test.com / password123）实测确认。
const (
	scryptN      = 16384 // 2^14
	scryptR      = 8
	scryptP      = 1
	scryptDKLen  = 64 // 派生密钥字节数 -> 128 hex 字符
	scryptSaltNB = 16 // 新密码生成时的盐字节数 -> 32 hex 字符
)

// ErrMalformedHash 表示存储的哈希字符串不是可识别的 scrypt 格式。
var ErrMalformedHash = errors.New("auth: malformed password hash")

// HashPassword 用 scrypt 生成新密码的存储字符串（随机 16 字节盐）。
// 输出格式与旧库一致，因此新旧用户的哈希可以混存于同一张 users 表。
func HashPassword(password string) (string, error) {
	salt := make([]byte, scryptSaltNB)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("auth: read random salt: %w", err)
	}
	dk, err := scrypt.Key([]byte(password), salt, scryptN, scryptR, scryptP, scryptDKLen)
	if err != nil {
		return "", fmt.Errorf("auth: scrypt derive: %w", err)
	}
	return fmt.Sprintf("scrypt$%d$%d$%d$%s$%s",
		scryptN, scryptR, scryptP,
		hex.EncodeToString(salt),
		hex.EncodeToString(dk),
	), nil
}

// VerifyPassword 校验明文密码是否匹配存储哈希。
// 解析出 N/r/p 与盐，重算派生密钥，用常数时间比较避免时序侧信道。
// 参数从哈希串里读取（而非写死常量），这样即便未来调整 N 也能验证历史数据。
func VerifyPassword(password, stored string) (bool, error) {
	parts := strings.Split(stored, "$")
	if len(parts) != 6 || parts[0] != "scrypt" {
		return false, ErrMalformedHash
	}

	n, err1 := strconv.Atoi(parts[1])
	r, err2 := strconv.Atoi(parts[2])
	p, err3 := strconv.Atoi(parts[3])
	if err1 != nil || err2 != nil || err3 != nil {
		return false, ErrMalformedHash
	}

	salt, err := hex.DecodeString(parts[4])
	if err != nil {
		return false, ErrMalformedHash
	}
	want, err := hex.DecodeString(parts[5])
	if err != nil {
		return false, ErrMalformedHash
	}

	got, err := scrypt.Key([]byte(password), salt, n, r, p, len(want))
	if err != nil {
		return false, fmt.Errorf("auth: scrypt derive: %w", err)
	}

	// 常数时间比较：命中 1 表示相等。
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
