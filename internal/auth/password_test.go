package auth

import "testing"

// legacyAliceStored 是从原版 portfolio.db 直接导出的真实密码哈希（alice@test.com）。
// 明文口令 "password123" 已通过暴力枚举实测确认。这条向量用于锁死向后兼容：
// 若它变红，说明 scrypt 编码/盐处理与旧库不再一致，旧用户将无法登录。
const legacyAliceStored = "scrypt$16384$8$1$6dd10bc46cae3f120055f8e4210b6779$4b21a26df46da1949f2635ecd8fd198abbcf59503bee5ac23f5e1dda90d31dc36aa6fd138e31e4eb293545d9f560f635d7cc9fe7ac860ec21053d3faaf83f203"

func TestVerifyPassword_LegacyVector(t *testing.T) {
	ok, err := VerifyPassword("password123", legacyAliceStored)
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if !ok {
		t.Fatal("legacy alice password should verify, got false — 向后兼容被破坏")
	}

	// 错误口令必须拒绝。
	ok, err = VerifyPassword("wrongpassword", legacyAliceStored)
	if err != nil {
		t.Fatalf("VerifyPassword returned error: %v", err)
	}
	if ok {
		t.Fatal("wrong password verified as true")
	}
}

func TestHashPassword_RoundTrip(t *testing.T) {
	const pw = "S0me-Str0ng-Pass!"
	h, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	ok, err := VerifyPassword(pw, h)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("freshly hashed password failed to verify")
	}
	// 每次盐随机，两次哈希结果必须不同。
	h2, _ := HashPassword(pw)
	if h == h2 {
		t.Fatal("two hashes of same password are identical — salt not random")
	}
}

func TestVerifyPassword_Malformed(t *testing.T) {
	cases := []string{
		"",
		"plaintext",
		"scrypt$16384$8$1$onlyfivefields",
		"bcrypt$16384$8$1$aa$bb",
		"scrypt$notnum$8$1$aa$bb",
		"scrypt$16384$8$1$zz$bb", // 非法 hex 盐
	}
	for _, c := range cases {
		if _, err := VerifyPassword("x", c); err == nil {
			t.Errorf("expected error for malformed hash %q, got nil", c)
		}
	}
}
