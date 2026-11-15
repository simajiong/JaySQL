package services

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	datasecModels "github.com/simajiong/jaysql/internal/datasec/models"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/middleware"
)

// currentUsername 从 JWT claims 取当前登录用户名。
// 注意：gin context 里没有 "username" 这个 key，用户名走 middleware.GetUserNameFromJWT。
func currentUsername(c *gin.Context) string {
	username, _ := middleware.GetUserNameFromJWT(c)
	return username
}

// 明文访问工单：申请 → 审批 → 限时生效 → 自动收回。
// 「自动收回」采用惰性失效：查询/列表时以 expires_at 实时判定，
// 不依赖定时任务把状态刷成 expired（刷状态的 cron 只影响展示，不影响安全边界）。

const (
	GrantStatusPending  = "pending"
	GrantStatusApproved = "approved"
	GrantStatusRejected = "rejected"
	GrantStatusExpired  = "expired"
	GrantStatusRevoked  = "revoked"
)

const maxGrantHours = 24

// loadActivePlainGrants 拉取用户在实例上已批准且未过期的工单。
func loadActivePlainGrants(instanceID, username string) []datasecModels.InsightDataSecPlainGrants {
	var grants []datasecModels.InsightDataSecPlainGrants
	err := global.App.DB.
		Where("instance_id=? and username=? and status=? and expires_at>?",
			strings.TrimSpace(instanceID), strings.TrimSpace(username), GrantStatusApproved, time.Now()).
		Find(&grants).Error
	if err != nil {
		global.App.Log.Error(fmt.Sprintf("datasec: 查询明文工单失败: %v", err))
		return nil
	}
	return grants
}

// grantCoversColumn 工单范围是否覆盖结果列。
// 范围逐级通配：库空=整实例、表空=整库、列空=整表；列名匹配大小写不敏感。
// 与列配置同样按「列名」匹配（不感知来源表），口径保持一致。
func grantCoversColumn(g datasecModels.InsightDataSecPlainGrants, columnName string) bool {
	if strings.EqualFold(strings.TrimSpace(g.ColumnName), strings.TrimSpace(columnName)) {
		return true
	}
	// 工单到表/库级：覆盖该范围内所有敏感列（含列名为空的情况）
	if strings.TrimSpace(g.ColumnName) == "" {
		return true
	}
	return false
}

// NormalizeGrantScope 归一工单范围：列必须带表，表必须带库；库级申请允许全空。
func NormalizeGrantScope(schema, table, column string) error {
	schema, table, column = strings.TrimSpace(schema), strings.TrimSpace(table), strings.TrimSpace(column)
	if schema == "" && (table != "" || column != "") {
		return fmt.Errorf("申请范围必须逐级指定：填了表或列时必须同时填写库名")
	}
	if table == "" && column != "" {
		return fmt.Errorf("申请范围必须逐级指定：填了列时必须同时填写表名")
	}
	return nil
}
