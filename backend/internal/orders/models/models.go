package models

import (
	"github.com/simajiong/jaysql/internal/common/models"

	"gorm.io/datatypes"
)

// 工单记录
/**
# 工单生命周期
"WAIT_PRECHECK": "待预审",
"PENDING":       "审批中",
"APPROVED":      "审批通过",
"EXECUTING":     "执行中",
"COMPLETED":     "执行成功",
"FAILED":        "执行失败",
"TERMINATED":    "已终止",
"REJECTED":      "审批驳回",
"WITHDRAWN":     "已撤回",
*/
type InsightOrderRecords struct {
	*models.Model
	Title                 string           `gorm:"type:varchar(256);not null;default:'';comment:工单标题;index:idx_title" json:"title"`
	OrderID               string           `gorm:"type:char(15);not null;default:'';comment:工单ID;uniqueIndex:uniq_order_id" json:"order_id"`
	HookOrderID           string           `gorm:"type:char(15);not null;default:'';comment:HOOK源工单ID;index:idx_hook_order_id" json:"hook_order_id"`
	Remark                string           `gorm:"type:varchar(2048);not null;default:'';comment:工单备注" json:"remark"`
	IsRestrictAccess      bool             `gorm:"type:tinyint(1);not null;default:0;comment:是否限制访问" json:"is_restrict_access"`
	DBType                models.EnumType  `gorm:"type:varchar(32);not null;default:'MySQL';comment:DB类型" json:"db_type"`
	// SQL类型：DDL / DML / EXPORT / MIXED。
	// MIXED 表示同一工单内 DDL 与 DML 并存（按语句书写顺序串行执行，每条任务按自身类型分派执行器）。
	// 列类型用 varchar 而非 ENUM：ENUM 增枚举值必须 ALTER 才能生效，AutoMigrate 检测不到
	// 枚举值差异，一旦漏改就会在写入 MIXED 时被静默截断成空串。取值合法性由服务层校验。
	SQLType               models.EnumType  `gorm:"type:varchar(16);not null;default:'DML';comment:SQL类型(DDL/DML/EXPORT/MIXED)" json:"sql_type"`
	Environment           string           `gorm:"type:varchar(32);not null;default:'';comment:环境名;index:idx_environment" json:"environment"`
	Applicant             string           `gorm:"type:varchar(32);not null;default:'';comment:申请人;index" json:"applicant"`
	Organization          string           `gorm:"type:varchar(256);not null;default:'';index;comment:组织" json:"organization"`
	Claimer               string           `gorm:"type:varchar(32);not null;default:'';comment:认领人;index" json:"claimer"`
	ClaimUsers            datatypes.JSON   `gorm:"type:json;null;default:null;comment:可认领人员列表" json:"claim_users"`
	Executor              string           `gorm:"type:varchar(32);not null;default:'';comment:工单执行人;index" json:"executor"`
	Approver              datatypes.JSON   `gorm:"type:json;null;default:null;comment:工单审核人" json:"approver"`
	Reviewer              datatypes.JSON   `gorm:"type:json;null;default:null;comment:工单复核人" json:"reviewer"`
	CC                    datatypes.JSON   `gorm:"type:json;null;default:null;comment:工单抄送人" json:"cc"`
	InstanceID            string           `gorm:"type:char(15);not null;default:'';comment:关联insight_instances的instance_id;index" json:"instance_id"`
	Database              string           `gorm:"type:varchar(255);not null;default:'';comment:数据库名称" json:"database"`
	Schema                string           `gorm:"type:varchar(255);not null;default:'';comment:资源路径" json:"schema"`
	Stage                 int              `gorm:"type:tinyint(1);not null;default:1;comment:审批阶段" json:"stage"`
	Progress              models.EnumType  `gorm:"type:varchar(32);not null;default:'WAIT_PRECHECK';comment:工单进度" json:"progress"`
	FixVersion            string           `gorm:"type:varchar(128);not null;default:'';comment:上线版本;index" json:"fix_version"`
	// mediumtext：工单支持 SQL 文件上传，text 的 64KB 上限会被大工单打爆（Error 1406）
	Content               string           `gorm:"type:mediumtext;null;comment:工单内容" json:"content"`
	RollbackSQL           string           `gorm:"type:mediumtext;null;comment:回滚SQL" json:"rollback_sql"`
	EstimatedAffectedRows int64            `gorm:"type:bigint;not null;default:0;comment:预估影响行数" json:"estimated_affected_rows"`
	ExportFileFormat      models.EnumType  `gorm:"type:ENUM('XLSX', 'CSV');default:'XLSX';comment:导出文件格式" json:"export_file_format"`
	ExecutionConfig       datatypes.JSON   `gorm:"type:json;null;default:null;comment:执行配置" json:"execution_config"`
	ExecutedAt            models.LocalTime `gorm:"comment:执行完成时间" json:"executed_at"`
	// 开始执行时间：与 executed_at 配对，用于区分「排队等待」与「真正执行」各花了多久。
	// 背景：定时工单实际触发时间比计划时间晚（调度器轮询粒度），而 execute_cost_time
	// 只统计 SQL 在目标库上的执行耗时，两者口径完全不同，只看后者会让人误判成"执行很快"。
	StartedAt models.LocalTime `gorm:"null;comment:开始执行时间" json:"started_at"`
	// 执行方式：IMMEDIATE 立即执行 / SCHEDULED 定时执行 / MANUAL 手动执行。
	// 单独落列（而非只放 execution_config JSON）是为了让「定时执行」调度器
	// 能直接按索引扫描到期工单，不必在 JSON 里做全表条件匹配。
	ExecutionMode string `gorm:"type:varchar(16);not null;default:'IMMEDIATE';index:idx_execution_mode;comment:执行方式(IMMEDIATE/SCHEDULED/MANUAL)" json:"execution_mode"`
	// 定时执行时间：仅 SCHEDULED 模式有值；调度器扫描 `scheduled_at <= NOW()` 且未触发的工单
	ScheduledAt models.LocalTime `gorm:"null;comment:定时执行时间;index:idx_scheduled_at" json:"scheduled_at"`
}

func (InsightOrderRecords) TableName() string {
	return "insight_order_records"
}

// 审批流
type InsightApprovalFlows struct {
	*models.Model
	ApprovalID string         `gorm:"type:char(15);not null;default:'';comment:审批流ID;uniqueIndex:uniq_approval_id" json:"approval_id"`
	Name       string         `gorm:"type:varchar(64);not null;default:'';comment:审批流名称" json:"name"`
	ClaimUsers datatypes.JSON `gorm:"type:json;null;default:null;comment:可认领人员列表" json:"claim_users"`
	Definition datatypes.JSON `json:"definition"` // [{"stage":1, "approvers":["zhangsan","lisi"], "type":"AND", "stage_name": '部门审批'}]
}

func (InsightApprovalFlows) TableName() string {
	return "insight_approval_flow"
}

// 审批流和用户映射表，每个用户只能在一个审批流里面
type InsightApprovalFlowUsers struct {
	*models.Model
	Username   string `gorm:"type:varchar(32);not null;uniqueIndex:uniq_username;comment:用户名" json:"username"`
	ApprovalID string `gorm:"type:char(15);not null;default:'';comment:审批流ID;index:idx_approval_id" json:"approval_id"`
}

func (InsightApprovalFlowUsers) TableName() string {
	return "insight_approval_flow_users"
}

// 审批记录
type InsightApprovalRecords struct {
	*models.Model
	OrderID        string           `gorm:"type:char(15);not null;default:'';comment:工单ID;index:index_order_id" json:"order_id"`
	Stage          int              `gorm:"type:tinyint(1);not null;default:1;comment:审批阶段" json:"stage"`
	StageName      string           `gorm:"type:varchar(64);null;default:null;comment:审批阶段名称" json:"stage_name"`
	Approver       string           `gorm:"type:varchar(32);not null;comment:审批人" json:"approver"`
	ApprovalType   models.EnumType  `gorm:"type:varchar(16);default:'SINGLE';comment:审批类型" json:"approval_type"`
	ApprovalStatus models.EnumType  `gorm:"type:ENUM('PENDING', 'APPROVED', 'REJECTED');default:'PENDING';comment:审批状态" json:"approval_status"`
	ApprovalAt     models.LocalTime `gorm:"comment:审批时间" json:"approval_at"`
	Msg            string           `gorm:"type:varchar(512);not null;default:'';comment:审批意见" json:"msg"`
}

func (InsightApprovalRecords) TableName() string {
	return "insight_approval_records"
}

// 工单操作日志表
type InsightOrderLogs struct {
	*models.Model
	Username string `gorm:"type:varchar(32);not null;index:idx_username;comment:操作用户" json:"username"`
	OrderID  string `gorm:"type:char(15);not null;default:'';comment:工单ID;index:idx_order_id" json:"order_id"`
	Msg      string `gorm:"type:varchar(1024);null;;comment:操作信息" json:"msg"`
}

func (InsightOrderLogs) TableName() string {
	return "insight_order_logs"
}

// 工单规范预审结果快照
// 工单创建（进入「待预审」阶段）时生成一份预审结果快照；此后所有阶段——包括审批中、
// 执行成功、已终止——都只读这份快照，不再实时重跑预审。
// 原因：预审规则（如 ALTER 加列重复定义）会实时读取目标库表结构，DDL 一旦落库，
// 重跑必然把「已经加上去的列」判成重复列，产生事后误报。
type InsightOrderPreInspections struct {
	*models.Model
	OrderID     string         `gorm:"type:char(15);not null;default:'';comment:工单ID;uniqueIndex:uniq_order_id" json:"order_id"`
	ContentHash string         `gorm:"type:varchar(64);not null;default:'';comment:预审时的SQL内容哈希" json:"content_hash"`
	OrderStage  string         `gorm:"type:varchar(32);not null;default:'';comment:生成快照时的工单进度" json:"order_stage"`
	Status      string         `gorm:"type:varchar(16);not null;default:'success';comment:快照状态 success/failed" json:"status"`
	Message     string         `gorm:"type:varchar(512);not null;default:'';comment:快照失败原因" json:"message"`
	Result      datatypes.JSON `gorm:"type:json;null;default:null;comment:预审结果快照" json:"result"`
	// RuleVersion 本次预审所用规范规则的版本指纹（空串表示未绑定规范，或快照生成时尚未记录版本）。
	// 撤回工单后的重新提交是全新校验：旧快照一律作废并按当前版本重跑，
	// 该字段用于判断「已有快照是否仍对应当前生效的规则」。
	RuleVersion string `gorm:"type:varchar(32);not null;default:'';comment:预审所用规则版本指纹" json:"rule_version"`
	// Backfilled 标记该快照不是工单提交时生成的，而是快照机制上线后首次查看时回填的
	// （存量工单没有提交时刻的快照，回填只能基于当前库表结构重跑，可能存在事后误报）。
	Backfilled bool `gorm:"not null;default:false;comment:是否为上线后回填的快照" json:"backfilled"`
}

func (InsightOrderPreInspections) TableName() string {
	return "insight_order_pre_inspections"
}

// 工单记录生成的执行任务
type InsightOrderTasks struct {
	*models.Model
	OrderID string `gorm:"type:char(15);not null;default:'';comment:关联insight_order_records的order_id;index:idx_order_id" json:"order_id"`
	TaskID  string `gorm:"type:char(15);not null;default:'';comment:任务ID;uniqueIndex:uniq_order_task_id" json:"task_id"`
	// Seq 任务在工单内的执行序号（从 1 开始，对应语句书写顺序）。
	// 混合工单的执行顺序完全由它决定：必须显式落列并按其排序，
	// 不能依赖「批量插入后按主键返回」这种隐式顺序。
	Seq      int             `gorm:"type:int;not null;default:0;comment:工单内执行序号" json:"seq"`
	DBType   models.EnumType `gorm:"type:varchar(32);not null;default:'MySQL';comment:DB类型" json:"db_type"`
	SQLType  models.EnumType `gorm:"type:varchar(16);not null;default:'DML';comment:SQL类型(DDL/DML/EXPORT)" json:"sql_type"`
	Executor string          `gorm:"type:varchar(128);null;default:null;comment:任务执行人" json:"executor"`
	SQL      string          `gorm:"type:mediumtext;null;comment:SQL语句" json:"sql"`
	Progress models.EnumType `gorm:"type:ENUM('PENDING', 'EXECUTING', 'COMPLETED', 'FAILED');default:'PENDING';comment:进度" json:"progress"`
	Result   datatypes.JSON  `gorm:"type:json;null;default:null;comment:执行结果" json:"result"`
	// 任务级真实执行时间：created_at 是任务生成时刻（审批通过拆分任务时），
	// updated_at 是行最后更新时刻，拿它们当「开始/结束时间」会让待执行任务
	// 显示成 开始=结束 的假象。落列后在执行循环里随进度流转写入。
	StartedAt  models.LocalTime `gorm:"null;comment:任务开始执行时间" json:"started_at"`
	FinishedAt models.LocalTime `gorm:"null;comment:任务结束执行时间" json:"finished_at"`
}

func (InsightOrderTasks) TableName() string {
	return "insight_order_tasks"
}

// 工单消息投递记录
//
// 记录每一次「工单事件 → 通知渠道」的投递结果，用于回答「审批人说没收到通知」
// 这类问题：是压根没投、投了但失败、还是投成功而人没看。
// 这张表原先只有 order_id/receiver/response 三列（注释写着"暂时没有用到"），
// 现按实际需要补齐渠道、事件类型、投递状态。
type InsightOrderMessages struct {
	*models.Model
	OrderID  string         `gorm:"type:char(15);not null;default:'';comment:关联insight_order_records的order_id;index" json:"order_id"`
	Channel  string         `gorm:"type:varchar(16);not null;default:'';comment:投递渠道 dingtalk/wechat/email" json:"channel"`
	MsgType  string         `gorm:"type:varchar(32);not null;default:'';comment:消息类型 ORDER_PENDING_APPROVAL 等" json:"msg_type"`
	Receiver datatypes.JSON `gorm:"type:json;null;default:null;comment:接收消息的用户" json:"receiver"`
	Response string         `gorm:"type:text;null;comment:第三方返回的响应" json:"response"`
	Status   string         `gorm:"type:varchar(16);not null;default:'';comment:投递状态 SENT/FAILED" json:"status"`
	// DedupKey 幂等键（工单+事件+渠道+接收人），供服务层做"短时间内不重复投递"判断
	DedupKey string `gorm:"type:varchar(160);not null;default:'';index:idx_order_msg_dedup;comment:幂等键" json:"dedup_key"`
}

func (InsightOrderMessages) TableName() string {
	return "insight_order_messages"
}

// 钉钉审批一次性令牌
//
// 为什么需要它：钉钉互动卡片的按钮回调只带来 (outTrackId, actionId, userId)，
// 这三样都不足以证明「本次点击确实属于这张工单的这一位审批人」，也无法防重放
// —— 钉钉在未收到 ACK 时会重推同一事件。令牌把「工单 + 审批人 + 渠道 + 卡片实例」
// 绑成一个不可猜测的随机串，回调时用行锁原子消费，从而保证：
//  1. 只能操作自己那张卡片对应的工单（杜绝横向越权）；
//  2. 同一令牌只生效一次，重复点击/重放返回幂等结果（不会重复审批）；
//  3. 转交后旧令牌立即作废（卡片同时被更新为终态），前一位审批人不会误操作。
type InsightOrderApprovalTokens struct {
	*models.Model
	Token    string `gorm:"type:char(64);not null;default:'';comment:一次性令牌;uniqueIndex:uniq_approval_token" json:"token"`
	OrderID  string `gorm:"type:char(15);not null;default:'';comment:工单ID;index:idx_token_order_id" json:"order_id"`
	Stage    int    `gorm:"type:tinyint(1);not null;default:1;comment:审批阶段" json:"stage"`
	Approver string `gorm:"type:varchar(32);not null;default:'';comment:绑定的审批人;index:idx_token_approver" json:"approver"`
	// ApproverDingTalkID 审批人的钉钉 userid。回调时用「卡片实例ID + 点击人 userid」
	// 双重校验，确保只有这张卡片的主人能操作它（卡片 ID 泄露也无法被他人利用）。
	ApproverDingTalkID string           `gorm:"type:varchar(64);not null;default:'';comment:审批人钉钉userid" json:"approver_dingtalk_id"`
	Channel            string           `gorm:"type:varchar(16);not null;default:'dingtalk';comment:投递渠道" json:"channel"`
	OutTrackID         string           `gorm:"type:varchar(64);not null;default:'';comment:卡片实例ID;index:idx_token_out_track" json:"out_track_id"`
	Status             string           `gorm:"type:varchar(16);not null;default:'ISSUED';index:idx_token_status;comment:ISSUED/CONSUMED/REVOKED" json:"status"`
	ConsumedAt         models.LocalTime `gorm:"comment:消费时间" json:"consumed_at"`
	ExpiresAt          models.LocalTime `gorm:"comment:过期时间" json:"expires_at"`
	// Remark 消费结果摘要（如"APPROVED 由 admin 通过"），便于事后核对卡片与库内是否一致
	Remark string `gorm:"type:varchar(255);not null;default:'';comment:消费结果摘要" json:"remark"`
}

func (InsightOrderApprovalTokens) TableName() string {
	return "insight_order_approval_tokens"
}
