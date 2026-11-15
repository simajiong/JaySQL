package services

import (
	"github.com/simajiong/jaysql/internal/backup/forms"
	"github.com/simajiong/jaysql/internal/global"
	"github.com/simajiong/jaysql/pkg/pagination"

	"github.com/gin-gonic/gin"
)

type GetInstancesService struct {
	*forms.GetInstancesForm
	C *gin.Context
}

func (s *GetInstancesService) Run() (data interface{}, total int64, err error) {
	// environment_name / env_type 供恢复向导判断「目标是否生产环境」：
	// 生产环境必须强制走四确认。环境未标注时两个字段为空串，由前端回落到手选。
	type row struct {
		ID              uint64 `json:"id"`
		Hostname        string `json:"hostname"`
		Port            int    `json:"port"`
		DbType          string `json:"db_type"`
		Remark          string `json:"remark"`
		EnvironmentID   uint64 `json:"environment_id"`
		EnvironmentName string `json:"environment_name"`
		EnvType         string `json:"env_type"`
	}
	var list []row
	tx := global.App.DB.Table("insight_instances AS i").
		Select(`i.id, i.hostname, i.port, i.db_type, i.remark,
			COALESCE(i.environment, 0) AS environment_id,
			COALESCE(e.name, '') AS environment_name,
			COALESCE(e.env_type, '') AS env_type`).
		Joins("LEFT JOIN insight_instance_environments AS e ON e.id = i.environment").
		// 备份可选的实例范围：逻辑备份支持 MySQL / PostgreSQL / Kingbase；
		// 物理备份（pgBackRest / XtraBackup）只在前端按类型收窄，这里不预先排除，
		// 否则金仓这类"只有逻辑备份可用"的库在创建页根本选不到数据源。
		Where("i.db_type in ('MySQL','PostgreSQL','Kingbase','KingbaseES')")
	if s.Search != "" {
		tx = tx.Where("i.hostname like ? or i.remark like ?", "%"+s.Search+"%", "%"+s.Search+"%")
	}
	if s.DBType != "" {
		tx = tx.Where("i.db_type = ?", s.DBType)
	}
	tx = tx.Order("i.id desc")
	total = pagination.Pager(&s.PaginationQ, tx, &list)
	return &list, total, nil
}
