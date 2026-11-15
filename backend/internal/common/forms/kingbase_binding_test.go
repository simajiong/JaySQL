package forms

import (
	"testing"

	"github.com/gin-gonic/gin/binding"

	"github.com/simajiong/jaysql/internal/common/models"
)

// 金仓（Kingbase）必须在「新增 / 编辑 / 测试连接」三个表单白名单里都放行。
// 这是最容易漏的一处：三个 oneof 是三份独立声明，漏一个就会出现
// 「新增页能选、测试连接报 400」或「能存不能改」。
//
// 注意必须走 gin 的 binding 校验器：gin 用 TagNameFunc 把 `form` 名字与 `binding`
// tag 映射进 validator，直接用 go-playground 的 validator.New() 只会读 `validate`
// tag —— 那时所有规则都不生效，测试会「恒通过」，等于没测。
func TestKingbaseAcceptedByInstanceForms(t *testing.T) {
	cases := []struct {
		name string
		data any
	}{
		{
			name: "新增实例",
			data: AdminCreateInstancesForm{
				Hostname:    "192.168.1.20",
				Port:        54321,
				User:        "system",
				Password:    "Kingbase@123",
				DbType:      models.EnumType("Kingbase"),
				Environment: 1,
				Remark:      "金仓测试实例",
			},
		},
		{
			name: "编辑实例",
			data: AdminUpdateInstancesForm{
				Hostname:    "192.168.1.20",
				Port:        54321,
				User:        "system",
				DbType:      models.EnumType("Kingbase"),
				Environment: 1,
				Remark:      "金仓测试实例",
			},
		},
		{
			name: "测试连接",
			data: AdminTestInstanceConnectionForm{
				Hostname: "192.168.1.20",
				Port:     54321,
				User:     "system",
				Password: "Kingbase@123",
				DbType:   models.EnumType("Kingbase"),
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := binding.Validator.ValidateStruct(c.data); err != nil {
				t.Fatalf("Kingbase 应通过 %s 表单校验，实际报错：%v", c.name, err)
			}
		})
	}
}

// 反例：白名单仍要拦住错误拼写，避免 oneof 被写坏成「啥都放行」。
func TestKingbaseTypoStillRejected(t *testing.T) {
	err := binding.Validator.ValidateStruct(AdminTestInstanceConnectionForm{
		Hostname: "192.168.1.20",
		Port:     54321,
		User:     "system",
		Password: "Kingbase@123",
		DbType:   models.EnumType("KingBaseES"),
	})
	if err == nil {
		t.Fatal("拼写错误（KingBaseES）应被 oneof 拒绝，说明白名单校验已失效")
	}
}
