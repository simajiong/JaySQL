package services

// SQL 工作台的「写后失效」钩子。
//
// 背景：库/模式清单存在 insight_instance_schemas 这张快照表里。此前它只由
// 定时任务全量刷，所以「在工作台里 CREATE DATABASE」之后，工单提交页、授权申请页、
// 资源树最长要等一个同步周期才看得到新库。
//
// 这里做的是最小闭环：执行成功的语句如果是「改库清单」这一层，
// 立刻让该实例的快照失效并异步重建（见 common/tasks/meta_refresh.go）。
// 只在真正的库级 DDL 上触发，建表/加索引这类不改变库清单的语句一律不触发——
// 否则每次 CREATE TABLE 都要连一遍目标实例的所有库，代价远大于收益。

import (
	"regexp"
	"strings"

	"github.com/simajiong/jaysql/internal/common/tasks"
	"github.com/simajiong/jaysql/internal/global"
)

// schemaInventoryDDL 匹配会改变「有哪些库 / schema」的语句。
//
// 各引擎口径：
//   - MySQL / TiDB / Doris：CREATE|DROP|ALTER DATABASE；CREATE SCHEMA 是它的别名
//   - PostgreSQL / SQLServer：CREATE|DROP|ALTER SCHEMA 与 CREATE|DROP DATABASE
//   - Oracle：schema 即用户，CREATE|DROP USER 才会改变 schema 清单
//   - SQL Server：sp_renamedb 改库名
//
// 刻意不匹配 CREATE TABLE / CREATE INDEX / ALTER TABLE：它们不动库清单。
var schemaInventoryDDL = regexp.MustCompile(
	`(?is)^(?:create|drop|alter)\s+(?:database|schema|user)\b` +
		`|^sp_renamedb\b`,
)

// NotifySchemaInventoryChanged SQL 工作台执行成功后调用。
//
// 非库级 DDL 直接返回，不产生任何开销；是库级 DDL 则异步刷新，
// 不阻塞本次执行结果的返回。
func NotifySchemaInventoryChanged(instanceID string, sqlText string) {
	if !IsSchemaInventoryDDL(sqlText) {
		return
	}
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		return
	}
	global.App.Log.Info("检测到库级 DDL，触发元数据快照刷新：instance=" + instanceID)
	tasks.RefreshInstanceMetaAsync(instanceID)
}

// IsSchemaInventoryDDL 判断语句是否属于「改库清单」的 DDL。
func IsSchemaInventoryDDL(sqlText string) bool {
	return schemaInventoryDDL.MatchString(trimLeadingSQLComments(sqlText))
}

// trimLeadingSQLComments 去掉语句开头空白与注释，让正则从真正的关键字开始匹配。
//
// 工作台里带注释的 DDL 很常见（"-- 新建测试库\nCREATE DATABASE t;"），
// 不去注释就会漏判，表现为「明明执行成功了却还是看不到库」。
func trimLeadingSQLComments(sqlText string) string {
	text := strings.TrimSpace(sqlText)
	for {
		switch {
		case strings.HasPrefix(text, "--"), strings.HasPrefix(text, "#"):
			idx := strings.IndexByte(text, '\n')
			if idx < 0 {
				return ""
			}
			text = strings.TrimSpace(text[idx+1:])
		case strings.HasPrefix(text, "/*"):
			idx := strings.Index(text, "*/")
			if idx < 0 {
				return ""
			}
			text = strings.TrimSpace(text[idx+2:])
		default:
			return text
		}
	}
}
