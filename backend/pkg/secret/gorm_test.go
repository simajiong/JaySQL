package secret

import (
	"path/filepath"
	"testing"

	"github.com/simajiong/jaysql/internal/global"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// secretTestTarget 透明拦截的测试模型（表名即清单里登记的 secret_test_targets）。
type secretTestTarget struct {
	ID          uint   `gorm:"primaryKey"`
	Name        string `gorm:"size:64"`
	SSHPassword string `gorm:"size:512"`
	ExtraConfig string `gorm:"size:2048"`
}

func (secretTestTarget) TableName() string { return "secret_test_targets" }

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	global.App.Config.App.SECRET_KEY = "0123456789abcdef0123456789abcdef"
	if err := LoadKey(); err != nil {
		t.Fatalf("LoadKey failed: %v", err)
	}
	dsn := filepath.Join(t.TempDir(), "secret.db")
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.Table("secret_test_targets").AutoMigrate(&secretTestTarget{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	return db
}

func TestInstallModeOffKeepsRawValue(t *testing.T) {
	db := newTestDB(t)
	Register(FieldSpec{Table: "secret_test_targets", Column: "ssh_password"})
	if err := Install(db, WithTransparentMode(ModeOff)); err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	row := secretTestTarget{Name: "n1", SSHPassword: "plain"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create failed: %v", err)
	}
	var stored struct{ SSHPassword string }
	if err := db.Table("secret_test_targets").Select("ssh_password").Where("id = ?", row.ID).Scan(&stored).Error; err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if stored.SSHPassword != "plain" {
		t.Fatalf("ModeOff should not encrypt, got %q", stored.SSHPassword)
	}
}

func TestTransparentEncryptDecryptRoundTrip(t *testing.T) {
	db := newTestDB(t)
	Register(FieldSpec{Table: "secret_test_targets", Column: "ssh_password"})
	defer SetMode(ModeOff)

	if err := Install(db, WithTransparentMode(ModeOn)); err != nil {
		t.Fatalf("Install failed: %v", err)
	}

	row := secretTestTarget{Name: "n2", SSHPassword: "TestPwd@NotReal"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create failed: %v", err)
	}

	// 库里必须是带前缀的密文
	var stored struct{ SSHPassword string }
	if err := db.Table("secret_test_targets").Select("ssh_password").Where("id = ?", row.ID).Scan(&stored).Error; err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !IsEncrypted(stored.SSHPassword) {
		t.Fatalf("stored value should be ciphertext, got %q", stored.SSHPassword)
	}

	// 读出来必须是明文（且模型字段被就地改写）
	var loaded secretTestTarget
	if err := db.Where("id = ?", row.ID).First(&loaded).Error; err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if loaded.SSHPassword != "TestPwd@NotReal" {
		t.Fatalf("decrypt on read failed, got %q", loaded.SSHPassword)
	}
	if loaded.Name != "n2" {
		t.Fatalf("non-sensitive field changed: %q", loaded.Name)
	}
}

func TestTransparentUpdateViaMap(t *testing.T) {
	db := newTestDB(t)
	Register(FieldSpec{Table: "secret_test_targets", Column: "ssh_password"})
	defer SetMode(ModeOff)

	if err := Install(db, WithTransparentMode(ModeOn)); err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	row := secretTestTarget{Name: "n3", SSHPassword: "old"}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create failed: %v", err)
	}
	if err := db.Model(&secretTestTarget{}).Where("id = ?", row.ID).
		Updates(map[string]any{"ssh_password": "new-secret"}).Error; err != nil {
		t.Fatalf("update failed: %v", err)
	}

	var stored struct{ SSHPassword string }
	if err := db.Table("secret_test_targets").Select("ssh_password").Where("id = ?", row.ID).Scan(&stored).Error; err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !IsEncrypted(stored.SSHPassword) {
		t.Fatalf("map update should encrypt, got %q", stored.SSHPassword)
	}
	plain, err := Decrypt(stored.SSHPassword)
	if err != nil || plain != "new-secret" {
		t.Fatalf("map update value wrong: %q / %v", plain, err)
	}
}

func TestTransparentJSONColumn(t *testing.T) {
	db := newTestDB(t)
	Register(FieldSpec{
		Table:    "secret_test_targets",
		Column:   "extra_config",
		JSONPath: []string{"$.ssh_password", "$.ssh_private_key"},
	})
	defer SetMode(ModeOff)

	if err := Install(db, WithTransparentMode(ModeOn)); err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	raw := `{"ssh_user":"root","ssh_port":22,"ssh_password":"pw","ssh_private_key":"key"}`
	row := secretTestTarget{Name: "n4", SSHPassword: "x", ExtraConfig: raw}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create failed: %v", err)
	}
	var stored struct{ ExtraConfig string }
	if err := db.Table("secret_test_targets").Select("extra_config").Where("id = ?", row.ID).Scan(&stored).Error; err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !contains(stored.ExtraConfig, `"ssh_password":"`+PrefixV1) {
		t.Fatalf("JSON password not encrypted: %s", stored.ExtraConfig)
	}
	if !contains(stored.ExtraConfig, `"ssh_user":"root"`, `"ssh_port":22`) {
		t.Fatalf("unrelated JSON fields changed: %s", stored.ExtraConfig)
	}

	var loaded secretTestTarget
	if err := db.Where("id = ?", row.ID).First(&loaded).Error; err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if loaded.ExtraConfig != raw {
		t.Fatalf("JSON round trip mismatch:\n got %s\nwant %s", loaded.ExtraConfig, raw)
	}
}

func TestInstallIdempotent(t *testing.T) {
	db := newTestDB(t)
	if err := Install(db); err != nil {
		t.Fatalf("first Install failed: %v", err)
	}
	if err := Install(db); err != nil {
		t.Fatalf("second Install failed: %v", err)
	}
}

func TestFieldsForAndRegister(t *testing.T) {
	Register(FieldSpec{Table: "insight_demo", Column: "secret_col"})
	specs := FieldsFor("insight_demo")
	if len(specs) != 1 || specs[0].Column != "secret_col" {
		t.Fatalf("FieldsFor = %+v", specs)
	}
	if len(FieldsFor("insight_not_exists")) != 0 {
		t.Fatalf("unknown table should return empty")
	}
	// 默认清单必须包含核心凭据列，且绝不包含 bcrypt 的 users.password
	found := func(table, column string) bool {
		for _, s := range All() {
			if s.Table == table && s.Column == column {
				return true
			}
		}
		return false
	}
	for _, key := range [][2]string{
		{"insight_instances", "password"},
		{"insight_instances", "extra_config"},
		{"insight_backup_storage_targets", "s3_secret_key"},
		{"insight_backup_nodes", "ssh_password"},
	} {
		if !found(key[0], key[1]) {
			t.Fatalf("default registry missing %s.%s", key[0], key[1])
		}
	}
	if found("insight_users", "password") {
		t.Fatalf("insight_users.password is bcrypt and must NOT be managed by secret")
	}
}
