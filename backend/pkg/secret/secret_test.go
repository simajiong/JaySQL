package secret

import (
	"strings"
	"testing"

	"github.com/simajiong/jaysql/internal/global"
)

func setupKey(t *testing.T) {
	t.Helper()
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	if err := LoadKey(); err != nil {
		t.Fatalf("LoadKey failed: %v", err)
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	setupKey(t)
	const plain = "TestPwd@NotReal!中文密码"
	cipher, err := Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	if !strings.HasPrefix(cipher, PrefixV1) {
		t.Fatalf("cipher missing prefix: %s", cipher)
	}
	if !IsEncrypted(cipher) {
		t.Fatalf("IsEncrypted should be true: %s", cipher)
	}
	got, err := Decrypt(cipher)
	if err != nil {
		t.Fatalf("Decrypt failed: %v", err)
	}
	if got != plain {
		t.Fatalf("round trip mismatch: got %q want %q", got, plain)
	}
}

func TestEncryptIdempotent(t *testing.T) {
	setupKey(t)
	first, err := Encrypt("secret-value")
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}
	second, err := Encrypt(first)
	if err != nil {
		t.Fatalf("Encrypt(cipher) failed: %v", err)
	}
	if first != second {
		t.Fatalf("Encrypt must be idempotent, got %q vs %q", first, second)
	}
	// 双重加密后一次解密就能拿回明文
	plain, err := Decrypt(second)
	if err != nil || plain != "secret-value" {
		t.Fatalf("double encrypted value decrypt failed: %v / %q", err, plain)
	}
}

func TestDecryptPassThroughNonCipher(t *testing.T) {
	setupKey(t)
	// 历史明文 / 无前缀旧密文：原样返回且不报错，这是迁移期兼容的基石
	for _, input := range []string{"", "plain-password", "c2hvdWxkIGJlIHBhc3N0aHJvdWdo"} {
		got, err := Decrypt(input)
		if err != nil {
			t.Fatalf("Decrypt(%q) should not error: %v", input, err)
		}
		if got != input {
			t.Fatalf("Decrypt(%q) = %q, want pass-through", input, got)
		}
	}
}

func TestDecryptLegacyCiphertextWithoutPrefix(t *testing.T) {
	setupKey(t)
	// utils.Encrypt 产出的无前缀密文（迁移脚本补前缀之前的状态）。
	// 加这段回退的意义：运维若漏跑 A 类迁移脚本，发版后存量密文仍能解开，
	// 不会出现"全站连不上数据库"的硬故障。
	legacy, err := SealRaw("legacy-secret")
	if err != nil {
		t.Fatalf("SealRaw failed: %v", err)
	}
	if IsEncrypted(legacy) {
		t.Fatalf("无前缀密文不应被 IsEncrypted 识别")
	}
	got, err := Decrypt(legacy)
	if err != nil {
		t.Fatalf("Decrypt(legacy) failed: %v", err)
	}
	if got != "legacy-secret" {
		t.Fatalf("Decrypt(legacy) = %q want %q", got, "legacy-secret")
	}
}

func TestDecryptPlainThatLooksLikeBase64(t *testing.T) {
	setupKey(t)
	// 长度 40+ 且是合法 base64 的明文：GCM tag 校验通不过，必须原样返回
	cases := []string{
		"c2hvdWxkIGJlIGEgcGxhaW4gdGV4dCBwYXNzd29yZA==",
		"0123456789012345678901234567890123456789",
		"short",
		"不是base64的中文密码也不该被破坏",
		"abc!@#$%^&*()_+",
	}
	for _, input := range cases {
		got, err := Decrypt(input)
		if err != nil {
			t.Fatalf("Decrypt(%q) should not error: %v", input, err)
		}
		if got != input {
			t.Fatalf("Decrypt(%q) = %q, want pass-through", input, got)
		}
	}
}

func TestEncryptEmpty(t *testing.T) {
	setupKey(t)
	got, err := Encrypt("")
	if err != nil || got != "" {
		t.Fatalf("Encrypt(\"\") = (%q, %v), want empty", got, err)
	}
}

func TestLegacyPrefixCompatibility(t *testing.T) {
	setupKey(t)
	// 通知渠道历史格式：前缀不同但算法一致
	raw, err := SealRaw("dingtalk-secret")
	if err != nil {
		t.Fatalf("SealRaw failed: %v", err)
	}
	legacy := PrefixLegacy + raw
	if !IsEncrypted(legacy) {
		t.Fatalf("legacy ciphertext should be recognized")
	}
	if Version(legacy) != "legacy" {
		t.Fatalf("Version(legacy) = %q", Version(legacy))
	}
	got, err := Decrypt(legacy)
	if err != nil || got != "dingtalk-secret" {
		t.Fatalf("Decrypt(legacy) = (%q, %v)", got, err)
	}
}

func TestVersionAndIsEncrypted(t *testing.T) {
	cases := map[string]string{
		"enc:v1:aesgcm:AAAA": "v1",
		"enc:aes:AAAA":       "legacy",
		"plain":              "",
		"":                   "",
	}
	for input, want := range cases {
		if got := Version(input); got != want {
			t.Fatalf("Version(%q) = %q want %q", input, got, want)
		}
	}
	if IsEncrypted("plain") || IsEncrypted("") {
		t.Fatalf("plain text must not be treated as ciphertext")
	}
}

func TestDecryptWrongKeyFallsBackToPrevious(t *testing.T) {
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	t.Setenv(EnvKeyPrevious, "ffffffffffffffffffffffffffffffff")
	if err := LoadKey(); err != nil {
		t.Fatalf("LoadKey failed: %v", err)
	}
	const plain = "old-key-data"
	cipher, err := Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt failed: %v", err)
	}

	// 切成新密钥（旧密钥挪到 PREVIOUS），存量数据仍应能解密
	global.App.Config.App.SECRET_KEY = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	t.Setenv(EnvKey, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	t.Setenv(EnvKeyPrevious, "0123456789abcdef0123456789abcdef")
	if err := LoadKey(); err != nil {
		t.Fatalf("LoadKey failed: %v", err)
	}
	got, err := Decrypt(cipher)
	if err != nil {
		t.Fatalf("Decrypt with rotated key failed: %v", err)
	}
	if got != plain {
		t.Fatalf("decrypted %q want %q", got, plain)
	}
}

func TestLoadKeyRejectsWrongLength(t *testing.T) {
	global.App.Config.App.SECRET_KEY = "too-short"
	t.Setenv(EnvKey, "")
	t.Setenv(EnvKeyFile, "")
	if err := LoadKey(); err == nil {
		t.Fatalf("LoadKey should reject key with wrong length")
	}
}

func TestSelfCheck(t *testing.T) {
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	t.Setenv(EnvKey, "")
	t.Setenv(EnvKeyFile, "")
	t.Setenv(EnvAllowDefault, "true")
	if err := SelfCheck(); err != nil {
		t.Fatalf("SelfCheck failed: %v", err)
	}
	if KeyFingerprint() == "" {
		t.Fatalf("KeyFingerprint should not be empty")
	}
}
