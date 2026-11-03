package tools

import (
	"fmt"
	"strings"
	"time"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
	commonModels "github.com/simajiong/jaysql/internal/common/models"
	"github.com/simajiong/jaysql/internal/global"
	monitorModels "github.com/simajiong/jaysql/internal/monitor/models"
	orderModels "github.com/simajiong/jaysql/internal/orders/models"
	perfModels "github.com/simajiong/jaysql/internal/performance/models"
)

// ============================================================
// 只读工具：直接查询平台已落库的数据，执行后把结果回灌模型
// 第一个闭环只做只读，写操作一律走草稿 + 人工确认（见 write.go）
// ============================================================

func init() {
	register(resolveInstanceTool())
	register(listOrdersTool())
	register(getOrderTool())
	register(backupStatusTool())
	register(listAlertsTool())
	register(slowQueriesTool())
}

const (
	defaultToolLimit = 10
	maxToolLimit     = 50
)

func normalizeLimit(limit int) int {
	if limit <= 0 {
		return defaultToolLimit
	}
	if limit > maxToolLimit {
		return maxToolLimit
	}
	return limit
}

func normalizeHours(hours int, def int) int {
	if hours <= 0 {
		return def
	}
	if hours > 24*30 {
		return 24 * 30
	}
	return hours
}

// timeText 时间字段统一转可读文本。
// 注意：平台的 LocalTime 是 time.Time 的别名类型（`type LocalTime time.Time`），
// 新类型不继承 Format/String 方法，直接 %v 会打印成 {0 0 <nil>}。
// 因此优先走它自己实现的 MarshalJSON（零值输出 null → 转成空串）。
func timeText(value any) string {
	if value == nil {
		return ""
	}
	if marshaler, ok := value.(interface{ MarshalJSON() ([]byte, error) }); ok {
		if body, err := marshaler.MarshalJSON(); err == nil {
			text := strings.Trim(string(body), `"`)
			if text == "null" {
				return ""
			}
			return text
		}
	}
	if formatter, ok := value.(interface{ Format(string) string }); ok {
		return formatter.Format("2006-01-02 15:04:05")
	}
	return fmt.Sprintf("%v", value)
}

func plainString(value any) string {
	return strings.TrimSpace(fmt.Sprintf("%v", value))
}

// envNameMap 环境 ID → 环境名
func envNameMap() map[int]string {
	out := map[int]string{}
	var rows []commonModels.InsightInstanceEnvironments
	if err := global.App.DB.Model(&commonModels.InsightInstanceEnvironments{}).Find(&rows).Error; err != nil {
		return out
	}
	for _, row := range rows {
		if row.Model != nil {
			out[int(row.ID)] = row.Name
		}
	}
	return out
}

// lookupInstance 按业务 ID 或 host(+port) 定位唯一实例
func lookupInstance(instanceID, host string, port int) (*commonModels.InsightInstances, error) {
	query := global.App.DB.Model(&commonModels.InsightInstances{})
	if id := strings.TrimSpace(instanceID); id != "" {
		query = query.Where("instance_id = ?", id)
	} else if h := strings.TrimSpace(host); h != "" {
		query = query.Where("hostname = ?", h)
		if port > 0 {
			query = query.Where("port = ?", port)
		}
	} else {
		return nil, fmt.Errorf("需要提供 instance_id 或 host 参数")
	}

	var rows []commonModels.InsightInstances
	if err := query.Order("id ASC").Limit(2).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("平台未登记该实例，请先在「工单 → 数据源」中登记后重试")
	}
	if len(rows) > 1 {
		return nil, fmt.Errorf("匹配到多个实例，请提供更精确的 host+port 或 instance_id")
	}
	return &rows[0], nil
}

func instanceBrief(instance *commonModels.InsightInstances, envs map[int]string) map[string]any {
	return map[string]any{
		"instance_id":       instance.InstanceID,
		"hostname":          instance.Hostname,
		"port":              instance.Port,
		"db_type":           plainString(instance.DbType),
		"environment":       instance.Environment,
		"env_name":          envs[instance.Environment],
		"remark":            instance.Remark,
		"last_check_status": instance.LastCheckStatus,
	}
}

// ------------------------------------------------------------------
// resolve_instance
// ------------------------------------------------------------------

func resolveInstanceTool() *Tool {
	return &Tool{
		Name: "resolve_instance",
		Description: "根据主机地址与端口（如 192.0.2.10:3306）在平台已登记的实例中查找实例，" +
			"返回实例 ID、数据库类型、所属环境。涉及具体实例的问题应先调用本工具，把地址解析成平台实例。",
		Permission: "order.view",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"host": map[string]any{"type": "string", "description": "实例主机地址或 IP"},
				"port": map[string]any{"type": "integer", "description": "实例端口，如 3306"},
			},
			"required": []string{"host"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			instance, err := lookupInstance("", argString(args, "host"), argInt(args, "port", 0))
			if err != nil {
				return nil, err
			}
			// 数据授权收口：解析到未授权的实例时按「没找到」处理，
			// 否则 AI 能把任意地址解析成平台实例并继续追问其数据。
			visible, err := instanceVisible(ctx, instance.InstanceID)
			if err != nil {
				return nil, err
			}
			if !visible {
				return map[string]any{"found": false}, nil
			}
			return map[string]any{
				"found":    true,
				"instance": instanceBrief(instance, envNameMap()),
			}, nil
		},
	}
}

// ------------------------------------------------------------------
// list_orders / get_order
// ------------------------------------------------------------------

func listOrdersTool() *Tool {
	return &Tool{
		Name: "list_orders",
		Description: "查询平台工单列表，可按实例、状态、关键字过滤。" +
			"用于回答「有哪些工单」「待审批的工单有几张」这类统计问题。状态取值示例：PENDING(待审批)、APPROVED(已通过)、EXECUTING(执行中)、COMPLETED(已完成)、REJECTED(已拒绝)。",
		Permission: "order.view",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头），可先用 resolve_instance 获取"},
				"status":      map[string]any{"type": "string", "description": "工单状态，如 PENDING"},
				"keyword":     map[string]any{"type": "string", "description": "标题或工单号关键字"},
				"limit":       map[string]any{"type": "integer", "description": "返回条数，默认 10，最多 50"},
			},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			query := global.App.DB.Model(&orderModels.InsightOrderRecords{})
			if instanceID := argString(args, "instance_id"); instanceID != "" {
				query = query.Where("instance_id = ?", instanceID)
			}
			if status := argString(args, "status"); status != "" {
				query = query.Where("progress = ?", status)
			}
			if keyword := argString(args, "keyword"); keyword != "" {
				like := "%" + keyword + "%"
				query = query.Where("title LIKE ? OR order_id LIKE ?", like, like)
			}

			// 数据授权收口：工单内容含变更 SQL，只看被授权的实例
			query, filterErr := applyVisibleInstances(query, ctx, "instance_id")
			if filterErr != nil {
				return nil, filterErr
			}

			var rows []orderModels.InsightOrderRecords
			if err := query.Order("id DESC").Limit(normalizeLimit(argInt(args, "limit", 0))).Find(&rows).Error; err != nil {
				return nil, err
			}

			items := make([]map[string]any, 0, len(rows))
			for index := range rows {
				row := rows[index]
				items = append(items, map[string]any{
					"order_id":    row.OrderID,
					"title":       row.Title,
					"status":      plainString(row.Progress),
					"stage":       plainString(row.Stage),
					"instance_id": row.InstanceID,
					"database":    row.Database,
					"applicant":   row.Applicant,
					"db_type":     plainString(row.DBType),
					"sql_type":    plainString(row.SQLType),
				})
			}
			return map[string]any{
				"count":  len(items),
				"orders": items,
				"note":   "仅返回最近若干条；如需精确统计总数请在结果基础上说明。",
			}, nil
		},
	}
}

func getOrderTool() *Tool {
	return &Tool{
		Name:        "get_order",
		Description: "按工单号查询单张工单的详情，包含变更 SQL、回滚 SQL、当前状态与申请人。",
		Permission:  "order.view",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"order_id": map[string]any{"type": "string", "description": "工单号，order_id"},
			},
			"required": []string{"order_id"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			orderID := argString(args, "order_id")
			if orderID == "" {
				return nil, fmt.Errorf("缺少参数 order_id")
			}
			var row orderModels.InsightOrderRecords
			err := global.App.DB.Model(&orderModels.InsightOrderRecords{}).
				Where("order_id = ?", orderID).First(&row).Error
			if err != nil {
				return nil, fmt.Errorf("未找到工单 %s", orderID)
			}
			visible, err := instanceVisible(ctx, row.InstanceID)
			if err != nil {
				return nil, err
			}
			if !visible {
				return nil, fmt.Errorf("未找到工单 %s", orderID)
			}
			return map[string]any{
				"order_id":     row.OrderID,
				"title":        row.Title,
				"status":       plainString(row.Progress),
				"stage":        plainString(row.Stage),
				"instance_id":  row.InstanceID,
				"database":     row.Database,
				"applicant":    row.Applicant,
				"executor":     row.Executor,
				"content":      row.Content,
				"rollback_sql": row.RollbackSQL,
			}, nil
		},
	}
}

// ------------------------------------------------------------------
// get_backup_status
// ------------------------------------------------------------------

func backupStatusTool() *Tool {
	return &Tool{
		Name: "get_backup_status",
		Description: "查询备份任务及其最近一次执行结果（成功/失败、开始与结束时间、备份类型）。" +
			"可按实例过滤，也可用关键字按任务名模糊匹配。用于回答「昨晚备份有没有失败」「某实例的备份情况」等问题。",
		Permission: "backup.task.view",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"keyword":     map[string]any{"type": "string", "description": "备份任务名关键字"},
				"limit":       map[string]any{"type": "integer", "description": "返回条数，默认 10，最多 50"},
			},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			query := global.App.DB.Model(&backupModels.InsightBackupTasks{})

			if instanceID := argString(args, "instance_id"); instanceID != "" {
				instance, err := lookupInstance(instanceID, "", 0)
				if err != nil {
					return nil, err
				}
				if instance.Model == nil {
					return nil, fmt.Errorf("实例 %s 记录异常，请刷新后重试", instanceID)
				}
				query = query.Where("source_instance_id = ?", instance.ID)
			}
			if keyword := argString(args, "keyword"); keyword != "" {
				query = query.Where("name LIKE ?", "%"+keyword+"%")
			}

			// 数据授权收口：备份任务清单同样按可见实例收敛（source_instance_id 是数字 ID）
			query, filterErr := applyVisibleInstancesByNumericID(query, ctx, "source_instance_id")
			if filterErr != nil {
				return nil, filterErr
			}

			var tasks []backupModels.InsightBackupTasks
			if err := query.Order("id DESC").Limit(normalizeLimit(argInt(args, "limit", 0))).Find(&tasks).Error; err != nil {
				return nil, err
			}

			items := make([]map[string]any, 0, len(tasks))
			for _, task := range tasks {
				item := map[string]any{
					"task_id":   task.TaskID,
					"name":      task.Name,
					"enabled":   task.Enabled,
					"db_type":   plainString(task.DBType),
					"backup_type": plainString(task.BackupType),
					"schedule":  task.ScheduleTime,
				}
				var runs []backupModels.InsightBackupRuns
				if err := global.App.DB.Model(&backupModels.InsightBackupRuns{}).
					Where("task_id = ?", task.TaskID).
					Order("id DESC").Limit(1).Find(&runs).Error; err == nil && len(runs) > 0 {
					item["last_run"] = map[string]any{
						"status":      plainString(runs[0].Status),
						"backup_type": plainString(runs[0].BackupType),
						"start_at":    timeText(runs[0].StartAt),
						"end_at":      timeText(runs[0].EndAt),
					}
				} else {
					item["last_run"] = nil
				}
				items = append(items, item)
			}
			return map[string]any{"count": len(items), "tasks": items}, nil
		},
	}
}

// ------------------------------------------------------------------
// list_alerts
// ------------------------------------------------------------------

func listAlertsTool() *Tool {
	return &Tool{
		Name: "list_alerts",
		Description: "查询监控告警列表，支持按实例、严重级别、状态、时间窗过滤，用于回答「当前有哪些告警」「某实例最近有没有告警」这类统计问题。" +
			"status=active 是「未恢复告警」，口径与监控大屏一致（status≠recovered 且未被忽略，含待认领 pending / 已认领 acknowledged），" +
			"此时不限触发时间——未恢复告警可能停留在几天前，只有显式传 hours 才收窄时间窗。" +
			"status=recovered 查已恢复的历史告警，默认最近 24 小时（可用 hours 扩大，最多 720）。" +
			"severity 取值：P0(最严重)/P1/P2/P3。",
		Permission: "monitor.view",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"severity":    map[string]any{"type": "string", "description": "严重级别 P0/P1/P2/P3"},
				"status":      map[string]any{"type": "string", "description": "告警状态：active(未恢复，含已认领)/recovered(已恢复)；也可传精确状态 pending/acknowledged"},
				"hours":       map[string]any{"type": "integer", "description": "最近多少小时，默认 24；status=active 时仅在显式传入时生效"},
				"limit":       map[string]any{"type": "integer", "description": "返回条数，默认 10，最多 50"},
			},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			status := strings.ToLower(strings.TrimSpace(argString(args, "status")))
			rawHours := argInt(args, "hours", 0)
			hours := normalizeHours(rawHours, 24)
			query := global.App.DB.Model(&monitorModels.InsightMonitorAlertEvents{})
			if instanceID := argString(args, "instance_id"); instanceID != "" {
				query = query.Where("instance_id = ?", instanceID)
			}
			if severity := argString(args, "severity"); severity != "" {
				query = query.Where("severity = ?", strings.ToUpper(severity))
			}

			// 平台告警状态机是 pending/active/acknowledged/recovered，「当前告警」
			// 的唯一口径是 status <> 'recovered' AND ignored = false（大屏、告警
			// 中心同源）。历史实现把 status=active 直接翻成 WHERE status='active'：
			// ① 会漏掉已认领(acknowledged)的告警；② 无条件叠默认 24h 时间窗，
			// 把停留在几天前的未恢复老告警切掉 —— 大屏明明有数，AI 却答「没有告警」。
			// 现在的口径：status=active 落在唯一口径上且不加时间窗（显式传 hours
			// 才收窄）；时间窗只对历史查询（recovered / 精确状态 / 不筛状态）生效。
			scope := "recent"
			switch status {
			case "active", "unresolved":
				query = query.Where("status <> ? AND ignored = ?", "recovered", false)
				scope = "unresolved"
				if rawHours > 0 {
					query = query.Where("triggered_at >= ?", time.Now().Add(-time.Duration(hours)*time.Hour))
				}
			case "recovered":
				query = query.Where("status = ?", status)
				query = query.Where("triggered_at >= ?", time.Now().Add(-time.Duration(hours)*time.Hour))
			case "":
				query = query.Where("triggered_at >= ?", time.Now().Add(-time.Duration(hours)*time.Hour))
			default:
				query = query.Where("status = ?", status)
				query = query.Where("triggered_at >= ?", time.Now().Add(-time.Duration(hours)*time.Hour))
			}

			// 数据授权收口：告警只暴露被授权实例
			query, filterErr := applyVisibleInstances(query, ctx, "instance_id")
			if filterErr != nil {
				return nil, filterErr
			}

			var rows []monitorModels.InsightMonitorAlertEvents
			if err := query.Order("triggered_at DESC").
				Limit(normalizeLimit(argInt(args, "limit", 0))).Find(&rows).Error; err != nil {
				return nil, err
			}

			bySeverity := map[string]int{}
			items := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				severity := plainString(row.Severity)
				bySeverity[severity]++
				items = append(items, map[string]any{
					"alert_id":     row.AlertID,
					"instance_id":  row.InstanceID,
					"name":         row.Name,
					"metric_key":   row.MetricKey,
					"severity":     severity,
					"status":       row.Status,
					"message":      row.Message,
					"observed":     row.ObservedValue,
					"threshold":    row.Threshold,
					"occurrences":  row.Occurrences,
					"triggered_at": timeText(row.TriggeredAt),
					"ignored":      row.Ignored,
				})
			}
			result := map[string]any{
				"window_hours": hours,
				"count":        len(items),
				"by_severity":  bySeverity,
				"alerts":       items,
			}
			// 口径说明回灌给模型：让「没有告警」「有 N 条」这类回答与
			// 监控大屏的数字对得上，避免模型把口径猜错转述给用户。
			if scope == "unresolved" {
				result["scope"] = "unresolved"
				result["note"] = "口径与监控大屏一致：status≠recovered 且未被忽略的告警，不限触发时间；count 即大屏「当前告警」数。"
			} else {
				result["scope"] = "recent"
				result["note"] = fmt.Sprintf("仅统计最近 %d 小时内触发的告警（含已恢复）", hours)
			}
			return result, nil
		},
	}
}

// ------------------------------------------------------------------
// get_slow_queries
// ------------------------------------------------------------------

func slowQueriesTool() *Tool {
	return &Tool{
		Name: "get_slow_queries",
		Description: "查询慢 SQL 记录（按耗时倒序），支持按实例和时间窗过滤，用于回答「哪条 SQL 慢」「最近的慢查询」等问题。",
		Permission: "performance.view",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"hours":       map[string]any{"type": "integer", "description": "最近多少小时，默认 24"},
				"limit":       map[string]any{"type": "integer", "description": "返回条数，默认 10，最多 50"},
			},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			hours := normalizeHours(argInt(args, "hours", 0), 24)
			query := global.App.DB.Model(&perfModels.InsightPerformanceSlowLogEntries{})
			if instanceID := argString(args, "instance_id"); instanceID != "" {
				query = query.Where("instance_id = ?", instanceID)
			}
			query = query.Where("event_time >= ?", time.Now().Add(-time.Duration(hours)*time.Hour))

			// 数据授权收口：慢 SQL 原文含业务数据字面值，只暴露被授权实例
			query, filterErr := applyVisibleInstances(query, ctx, "instance_id")
			if filterErr != nil {
				return nil, filterErr
			}

			var rows []perfModels.InsightPerformanceSlowLogEntries
			if err := query.Order("query_time_ms DESC").
				Limit(normalizeLimit(argInt(args, "limit", 0))).Find(&rows).Error; err != nil {
				return nil, err
			}

			items := make([]map[string]any, 0, len(rows))
			for _, row := range rows {
				sqlText := row.SQLText
				if len(sqlText) > 300 {
					sqlText = sqlText[:300] + "..."
				}
				items = append(items, map[string]any{
					"instance_id":  row.InstanceID,
					"event_time":   timeText(row.EventTime),
					"schema":       row.SchemaName,
					"query_time_ms": row.QueryTimeMS,
					"lock_time_ms": row.LockTimeMS,
					"rows_examined": row.RowsExamined,
					"username":     row.Username,
					"sql":          sqlText,
				})
			}
			return map[string]any{
				"window_hours": hours,
				"count":        len(items),
				"slow_queries": items,
			}, nil
		},
	}
}
