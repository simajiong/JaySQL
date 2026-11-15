package tools

import (
	"fmt"
	"regexp"
	"strings"
)

// ============================================================
// 写工具：不落库，只产出「待确认草稿」
//
// 这是行业共识里的 HITL（Human-in-the-loop）闸门：
//   - 模型负责把意图翻译成结构化参数（实例、SQL、回滚 SQL）
//   - 平台负责校验（实例必须已登记、参数必须完整）
//   - 但绝不代替用户提交 —— 草稿卡片上的按钮由用户点击，
//     后端确认接口（含 order.submit 权限校验）才完成落库
//
// 确认后的落点：工单以「待规范预审」状态创建，并由创建服务自动生成
// 规范预审快照（见 orders/services.CreateOrderService.Run）。因此从用户视角
// 看是「点一次确认 → 工单已在规范预审环节」，中间没有额外的人工提交动作。
// 创建人固定取当前登录用户（确认接口从会话取，模型与页面都无权指定）。
// ============================================================

func init() {
	register(createOrderTool())
	register(createBackupTool())
}

var createDatabasePattern = regexp.MustCompile(`(?i)^\s*create\s+database\s+(?:if\s+not\s+exists\s+)?[` + "`" + `"']?([A-Za-z0-9_$]+)`)

// createOrderSubmitPath AI 对话里创建工单的确认落库接口（与工单创建页共用创建服务）
const createOrderSubmitPath = "/api/v1/ai/orders/confirm"

// createBackupSubmitPath AI 对话里创建备份任务的确认落库接口
const createBackupSubmitPath = "/api/v1/ai/backups/confirm"

func createOrderTool() *Tool {
	return &Tool{
		Name:  "create_order",
		Description: "为某个实例创建一张变更工单（如建库、建表、改表、DML），创建后直接进入规范预审环节。" +
			"本工具只会生成一张待确认的草稿卡片，用户在卡片上点「确认创建」后由平台落库；" +
			"创建人固定为当前登录用户，无需（也不允许）由你指定。" +
			"调用前请先用 resolve_instance 确认实例已登记；若用户只给了地址，可直接传 host/port。",
		Write:      true,
		Permission: "order.submit",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"sql": map[string]any{
					"type":        "string",
					"description": "本次变更的 SQL 语句，例如 CREATE DATABASE test001 DEFAULT CHARACTER SET utf8mb4;",
				},
				"rollback_sql": map[string]any{"type": "string", "description": "回滚 SQL；建库场景可省略，平台会自动推导 DROP DATABASE"},
				"title":        map[string]any{"type": "string", "description": "工单标题；留空则根据 SQL 自动生成"},
				"reason":       map[string]any{"type": "string", "description": "变更原因/备注"},
				"instance_id":  map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"host":         map[string]any{"type": "string", "description": "实例主机地址，与 instance_id 二选一"},
				"port":         map[string]any{"type": "integer", "description": "实例端口"},
				"database":     map[string]any{"type": "string", "description": "目标库名；建库场景请留空"},
				"schema":       map[string]any{"type": "string", "description": "目标 Schema；PostgreSQL 等 Schema 层级库需要，与 database 一起给出"},
				"executor":     map[string]any{"type": "string", "description": "执行人用户名；留空默认为当前登录用户，仅在用户明确指定执行人时才传"},
			},
			"required": []string{"sql"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			sqlText := strings.TrimSpace(argString(args, "sql", "sql_text", "content"))
			if sqlText == "" {
				return nil, fmt.Errorf("缺少参数 sql：请给出本次变更要执行的 SQL 语句")
			}

			instance, err := lookupInstance(
				argString(args, "instance_id"),
				argString(args, "host"),
				argInt(args, "port", 0),
			)
			if err != nil {
				return nil, err
			}

			database := argString(args, "database")
			title := argString(args, "title")
			if title == "" {
				title = inferOrderTitle(sqlText, database)
			}

			rollback := argString(args, "rollback_sql")
			if rollback == "" {
				rollback = inferRollbackSQL(sqlText)
			}

			envName := envNameMap()[instance.Environment]
			envText := envName
			if envText == "" {
				envText = "未标注环境"
			}
			description := fmt.Sprintf(
				"目标实例：%s:%d（%s / %s）。确认后将直接创建工单并进入规范预审环节，创建人为你本人。",
				instance.Hostname, instance.Port, plainString(instance.DbType), envText,
			)
			if rollback == "" {
				description += "未提供回滚 SQL，建议先补充再确认。"
			}

			// Prefill 既是确认接口的请求体：字段名与 AICreateOrderConfirmForm 对齐
			prefill := map[string]any{
				"title":        title,
				"instance_id":  instance.InstanceID,
				"content":      sqlText,
				"rollback_sql": rollback,
			}
			if database != "" {
				prefill["database"] = database
			}
			if schema := argString(args, "schema"); schema != "" {
				prefill["schema"] = schema
			}
			if remark := argString(args, "reason"); remark != "" {
				prefill["remark"] = remark
			}
			if executor := argString(args, "executor"); executor != "" {
				prefill["executor"] = executor
			}

			return &DraftAction{
				Type:        "create_order",
				Title:       "新建工单：" + title,
				Description: description,
				SQL:         sqlText,
				ButtonText:  "确认创建",
				Tone:        "primary",
				SubmitPath:  createOrderSubmitPath,
				Prefill:     prefill,
			}, nil
		},
	}
}

// ============================================================
// create_backup
//
// 与 create_order 同一套 HITL 闸门：只产出待确认草稿，落库由用户在卡片上点确认完成。
// 参数口径与「备份中心 → 备份任务」创建页保持一致（备份对象=实例、备份名称=任务名、
// 备份范围=整实例/指定库、备份方式、调度与保留天数），确认后复用 backup 模块的
// 创建服务，预检查、执行节点解析与调度注册全部沿用既有实现。
// ============================================================

func createBackupTool() *Tool {
	return &Tool{
		Name: "create_backup",
		Description: "为某个实例创建备份任务（物理备份或逻辑备份，可指定库与调度周期）。" +
			"本工具只会生成一张待确认的草稿卡片，用户在卡片上点「确认创建」后由平台落库并纳入调度；" +
			"创建人固定为当前登录用户，无需（也不允许）由你指定。" +
			"调用前请先用 resolve_instance 确认实例已登记；若用户只给了地址，可直接传 host/port。" +
			"未指定 driver_type 时：给了 databases 走逻辑备份，否则按实例类型走物理备份（MySQL→XtraBackup，PostgreSQL→pgBackRest）。",
		Write:      true,
		Permission: "backup.task.manage",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"instance_id": map[string]any{"type": "string", "description": "实例业务 ID（inst- 开头）"},
				"host":        map[string]any{"type": "string", "description": "实例主机地址，与 instance_id 二选一"},
				"port":        map[string]any{"type": "integer", "description": "实例端口"},
				"name":        map[string]any{"type": "string", "description": "备份任务名称；留空则按「备份方式-实例地址」自动生成"},
				"object_mode": map[string]any{"type": "string", "description": "备份范围：ALL=整实例（物理备份只能 ALL），CUSTOM=指定库"},
				"databases":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "object_mode=CUSTOM 时必填：要备份的库名列表"},
				"driver_type": map[string]any{"type": "string", "description": "备份方式：LOGICAL（逻辑备份）/ XTRABACKUP（MySQL 物理）/ PG_BACKREST（PostgreSQL 物理）；留空由平台推导"},
				"backup_type": map[string]any{"type": "string", "description": "备份类型：FULL（默认）/ SCHEMA / DATA，后两者仅逻辑备份支持"},
				"schedule_days": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "调度星期，取值 MON/TUE/WED/THU/FRI/SAT/SUN（也接受「周一」这类中文）；留空表示每天",
				},
				"schedule_time":  map[string]any{"type": "string", "description": "调度时间 HH:MM，留空默认 01:00"},
				"retention_days": map[string]any{"type": "integer", "description": "备份保留天数，留空默认 7 天"},
				"storage_target": map[string]any{"type": "string", "description": "存储目标名称关键字（如「备份中心」）；留空由平台选择备份中心（LOCAL_SERVER）"},
			},
			"required": []string{"instance_id"},
		},
		Handler: func(ctx *Context, args map[string]any) (any, error) {
			instance, err := lookupInstance(
				argString(args, "instance_id"),
				argString(args, "host"),
				argInt(args, "port", 0),
			)
			if err != nil {
				return nil, err
			}
			dbType := plainString(instance.DbType)
			if dbType != "MySQL" && dbType != "PostgreSQL" && !isKingbaseDBType(dbType) {
				return nil, fmt.Errorf("备份任务仅支持 MySQL/PostgreSQL/KingbaseES 实例，当前实例类型为 %s", dbType)
			}

			databases := argStringList(args, "databases", "database")
			driverType, err := inferBackupDriverType(dbType, argString(args, "driver_type"), databases)
			if err != nil {
				return nil, err
			}

			objectMode := strings.ToUpper(strings.TrimSpace(argString(args, "object_mode")))
			if objectMode == "" {
				objectMode = "ALL"
			}
			if objectMode != "ALL" && objectMode != "CUSTOM" {
				return nil, fmt.Errorf("备份范围 object_mode 只能是 ALL 或 CUSTOM，当前为 %s", objectMode)
			}
			if isPhysicalBackupDriverType(driverType) && objectMode == "CUSTOM" {
				return nil, fmt.Errorf("%s是实例级全量备份，不支持指定库：请把 object_mode 改为 ALL，或改用 LOGICAL 逻辑备份",
					backupDriverText(driverType))
			}
			if objectMode == "CUSTOM" && len(databases) == 0 {
				return nil, fmt.Errorf("object_mode=CUSTOM 时必须给出 databases（要备份的库名）")
			}
			if objectMode == "ALL" {
				databases = nil
			}

			scheduleDays, err := normalizeBackupScheduleDays(argStringList(args, "schedule_days", "schedule_day"))
			if err != nil {
				return nil, err
			}
			scheduleTime := strings.TrimSpace(argString(args, "schedule_time"))
			if scheduleTime == "" {
				scheduleTime = "01:00"
			}
			if !backupScheduleTimePattern.MatchString(scheduleTime) {
				return nil, fmt.Errorf("调度时间 %s 格式非法，应为 HH:MM（如 01:30）", scheduleTime)
			}
			retentionDays := argInt(args, "retention_days", 7)
			if retentionDays <= 0 {
				retentionDays = 7
			}
			backupType := strings.ToUpper(strings.TrimSpace(argString(args, "backup_type")))
			if backupType == "" {
				backupType = "FULL"
			}
			if backupType != "FULL" && backupType != "SCHEMA" && backupType != "DATA" {
				return nil, fmt.Errorf("备份类型 %s 非法，可选 FULL / SCHEMA / DATA", backupType)
			}

			envText := envNameMap()[instance.Environment]
			if envText == "" {
				envText = "未标注环境"
			}
			objectText := "全部实例"
			if objectMode == "CUSTOM" {
				objectText = strings.Join(databases, "、")
			}
			description := fmt.Sprintf(
				"目标实例：%s:%d（%s / %s）；备份方式：%s；范围：%s；调度：%s；保留 %d 天。确认后将直接创建备份任务并纳入调度。",
				instance.Hostname, instance.Port, dbType, envText,
				backupDriverText(driverType), objectText, buildBackupScheduleText(scheduleDays, scheduleTime), retentionDays,
			)
			if storage := argString(args, "storage_target"); storage == "" {
				description += "未指定存储目标，将由平台选择备份中心（LOCAL_SERVER）。"
			}

			name := strings.TrimSpace(argString(args, "name"))
			if name == "" {
				name = inferBackupTaskName(backupDriverText(driverType), instance.Hostname, instance.Port, databases)
			}

			// Prefill 既是确认接口的请求体：字段名与 AICreateBackupConfirmForm 对齐
			prefill := map[string]any{
				"name":           name,
				"instance_id":    instance.InstanceID,
				"object_mode":    objectMode,
				"backup_type":    backupType,
				"driver_type":    driverType,
				"schedule_days":  scheduleDays,
				"schedule_time":  scheduleTime,
				"retention_days": retentionDays,
			}
			if len(databases) > 0 {
				prefill["databases"] = databases
			}
			if storage := argString(args, "storage_target"); storage != "" {
				prefill["storage_target"] = storage
			}

			return &DraftAction{
				Type:        "create_backup",
				Title:       "新建备份任务：" + name,
				Description: description,
				ButtonText:  "确认创建",
				Tone:        "primary",
				SubmitPath:  createBackupSubmitPath,
				Prefill:     prefill,
			}, nil
		},
	}
}

// backupScheduleTimePattern 调度时间（HH:MM）
var backupScheduleTimePattern = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// backupWeekdayTokens 调度星期：模型可能给 MON/Monday/周一/星期一，统一归一成 MON..SUN
var backupWeekdayTokens = map[string]string{
	"MON": "MON", "MONDAY": "MON", "周一": "MON", "星期一": "MON", "礼拜一": "MON", "1": "MON",
	"TUE": "TUE", "TUESDAY": "TUE", "周二": "TUE", "星期二": "TUE", "礼拜二": "TUE", "2": "TUE",
	"WED": "WED", "WEDNESDAY": "WED", "周三": "WED", "星期三": "WED", "礼拜三": "WED", "3": "WED",
	"THU": "THU", "THURSDAY": "THU", "周四": "THU", "星期四": "THU", "礼拜四": "THU", "4": "THU",
	"FRI": "FRI", "FRIDAY": "FRI", "周五": "FRI", "星期五": "FRI", "礼拜五": "FRI", "5": "FRI",
	"SAT": "SAT", "SATURDAY": "SAT", "周六": "SAT", "星期六": "SAT", "礼拜六": "SAT", "6": "SAT",
	"SUN": "SUN", "SUNDAY": "SUN", "周日": "SUN", "周天": "SUN", "星期日": "SUN", "星期天": "SUN",
	"礼拜日": "SUN", "礼拜天": "SUN", "0": "SUN", "7": "SUN",
}

// backupWeekdayOrder 输出顺序：周一在前，符合中文阅读习惯
var backupWeekdayOrder = []string{"MON", "TUE", "WED", "THU", "FRI", "SAT", "SUN"}

// normalizeBackupScheduleDays 归一调度星期；空表示每天（返回全部七天）
func normalizeBackupScheduleDays(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return append([]string(nil), backupWeekdayOrder...), nil
	}
	seen := map[string]struct{}{}
	for _, item := range raw {
		text := strings.TrimSpace(item)
		if text == "" {
			continue
		}
		if _, ok := backupWeekdayTokens[strings.ToUpper(text)]; !ok {
			if _, ok := backupWeekdayTokens[text]; !ok {
				return nil, fmt.Errorf("调度星期 %s 无法识别，请使用 MON/TUE/WED/THU/FRI/SAT/SUN 或「周一」这类中文写法", item)
			}
		}
	}
	out := make([]string, 0, len(raw))
	for _, day := range backupWeekdayOrder {
		for _, item := range raw {
			text := strings.TrimSpace(item)
			if text == "" {
				continue
			}
			if backupWeekdayTokens[strings.ToUpper(text)] == day || backupWeekdayTokens[text] == day {
				if _, dup := seen[day]; !dup {
					seen[day] = struct{}{}
					out = append(out, day)
				}
			}
		}
	}
	if len(out) == 0 {
		return append([]string(nil), backupWeekdayOrder...), nil
	}
	return out, nil
}

// isKingbaseDBType 金仓实例类型归一：库里存的写法历史上有 Kingbase / KingbaseES 等多种，
// 统一按前缀小写匹配，避免新增一种写法就漏一处判断。
func isKingbaseDBType(dbType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(dbType))
	return strings.HasPrefix(normalized, "kingbase")
}

// inferBackupDriverType 备份方式推导：
// 明确指定时用指定值（并校验与实例类型匹配）；未指定时，给了库名走逻辑备份，否则走物理备份。
// 金仓无 pgBackRest 工具链，未指定时一律推逻辑备份，避免模型把物理备份挂到金仓上。
func inferBackupDriverType(dbType, raw string, databases []string) (string, error) {
	driver := strings.ToUpper(strings.TrimSpace(raw))
	if driver == "" {
		if len(databases) > 0 {
			return "LOGICAL", nil
		}
		if isKingbaseDBType(dbType) {
			return "LOGICAL", nil
		}
		if dbType == "PostgreSQL" {
			return "PG_BACKREST", nil
		}
		return "XTRABACKUP", nil
	}
	switch driver {
	case "XTRABACKUP":
		if dbType != "MySQL" {
			return "", fmt.Errorf("XtraBackup 物理备份仅支持 MySQL 实例，当前实例类型为 %s", dbType)
		}
	case "PG_BACKREST":
		if dbType != "PostgreSQL" {
			return "", fmt.Errorf("pgBackRest 物理备份仅支持 PostgreSQL 实例，当前实例类型为 %s", dbType)
		}
	case "LOGICAL":
	default:
		return "", fmt.Errorf("不支持的备份方式 %s，可选 LOGICAL / XTRABACKUP / PG_BACKREST", driver)
	}
	return driver, nil
}

func isPhysicalBackupDriverType(driverType string) bool {
	return driverType == "XTRABACKUP" || driverType == "PG_BACKREST"
}

func backupDriverText(driverType string) string {
	switch driverType {
	case "XTRABACKUP":
		return "物理备份（XtraBackup）"
	case "PG_BACKREST":
		return "物理备份（pgBackRest）"
	default:
		return "逻辑备份（mysqldump/pg_dump/sys_dump）"
	}
}

func buildBackupScheduleText(days []string, scheduleTime string) string {
	if len(days) == 0 || len(days) == len(backupWeekdayOrder) {
		return fmt.Sprintf("每天 %s", scheduleTime)
	}
	names := make([]string, 0, len(days))
	for _, day := range days {
		names = append(names, map[string]string{
			"MON": "周一", "TUE": "周二", "WED": "周三", "THU": "周四", "FRI": "周五", "SAT": "周六", "SUN": "周日",
		}[day])
	}
	return fmt.Sprintf("每周 %s %s", strings.Join(names, "、"), scheduleTime)
}

// inferBackupTaskName 任务名：物理/逻辑备份 + 实例地址（指定库时补上库名），超长截断
func inferBackupTaskName(driverText, host string, port int, databases []string) string {
	name := fmt.Sprintf("%s-%s:%d", strings.SplitN(driverText, "（", 2)[0], strings.TrimSpace(host), port)
	if len(databases) > 0 {
		name += "(" + strings.Join(databases, ",") + ")"
	}
	if len([]rune(name)) > 128 {
		name = string([]rune(name)[:128])
	}
	return name
}

// inferOrderTitle 工单标题：优先用「动作 + 对象」，拿不到就用 SQL 首行
func inferOrderTitle(sqlText, database string) string {
	upper := strings.ToUpper(strings.TrimSpace(sqlText))

	switch {
	case strings.HasPrefix(upper, "CREATE DATABASE"):
		if match := createDatabasePattern.FindStringSubmatch(sqlText); len(match) > 1 {
			return fmt.Sprintf("创建数据库 %s", match[1])
		}
		return "创建数据库"
	case strings.HasPrefix(upper, "DROP DATABASE"):
		return "删除数据库"
	case strings.HasPrefix(upper, "CREATE TABLE"):
		return "创建表"
	case strings.HasPrefix(upper, "DROP TABLE"):
		return "删除表"
	case strings.HasPrefix(upper, "ALTER TABLE"):
		return "修改表结构"
	case strings.HasPrefix(upper, "CREATE INDEX"):
		return "创建索引"
	}

	firstLine := sqlText
	if index := strings.IndexAny(firstLine, "\r\n"); index >= 0 {
		firstLine = firstLine[:index]
	}
	if len(firstLine) > 40 {
		firstLine = firstLine[:40] + "..."
	}
	if database != "" {
		return fmt.Sprintf("变更 %s：%s", database, firstLine)
	}
	return "数据库变更：" + firstLine
}

// inferRollbackSQL 常见 DDL 的自动回滚推导（只覆盖语义确定的场景，其余留给用户填写）
func inferRollbackSQL(sqlText string) string {
	if match := createDatabasePattern.FindStringSubmatch(strings.TrimSpace(sqlText)); len(match) > 1 {
		return fmt.Sprintf("DROP DATABASE IF EXISTS `%s`;", match[1])
	}
	return ""
}
