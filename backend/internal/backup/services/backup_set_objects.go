package services

import (
	"fmt"
	"strings"

	backupModels "github.com/simajiong/jaysql/internal/backup/models"
)

// 恢复向导第 2 步「恢复对象」的数据来源：
// 从备份产物离线解析可恢复对象清单，以及物理备份的「全量 / 非全量」链路信息。
// 逻辑备份可解析到库粒度；物理备份为整实例产物，返回链路而非对象清单。

type BackupSetObjectItem struct {
	Name   string `json:"name"`
	Entry  string `json:"entry,omitempty"`
	Format string `json:"format,omitempty"`
	IsDir  bool   `json:"is_dir,omitempty"`
}

type BackupSetChainItem struct {
	Order      int    `json:"order"`
	BackupType string `json:"backup_type"`
	RunID      uint64 `json:"run_id,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	SizeBytes  int64  `json:"size_bytes,omitempty"`
	FromLSN    string `json:"from_lsn,omitempty"`
	ToLSN      string `json:"to_lsn,omitempty"`
}

type BackupSetObjectsResult struct {
	BackupSetID          uint64                `json:"backup_set_id"`
	Name                 string                `json:"name"`
	DBType               string                `json:"db_type"`
	DriverType           string                `json:"driver_type"`
	RestoreKind          string                `json:"restore_kind"`
	BackupType           string                `json:"backup_type"`
	IsFull               bool                  `json:"is_full"`
	ArtifactFormat       string                `json:"artifact_format,omitempty"`
	SupportsObjectSelect bool                  `json:"supports_object_select"`
	Objects              []BackupSetObjectItem `json:"objects"`
	Chain                []BackupSetChainItem  `json:"chain,omitempty"`
	ChainComplete        bool                  `json:"chain_complete"`
	Hint                 string                `json:"hint,omitempty"`
}

// GetBackupSetObjectsService 枚举备份集内部可恢复对象（只读，不修改任何数据）。
func GetBackupSetObjectsService(backupSetID uint64) (*BackupSetObjectsResult, error) {
	set, err := LoadBackupSet(backupSetID)
	if err != nil {
		return nil, err
	}
	result := &BackupSetObjectsResult{
		BackupSetID: set.ID,
		Name:        strings.TrimSpace(set.Name),
		DBType:      strings.TrimSpace(string(set.DBType)),
		DriverType:  strings.TrimSpace(string(set.DriverType)),
		BackupType:  strings.ToUpper(strings.TrimSpace(string(set.BackupType))),
		Objects:     make([]BackupSetObjectItem, 0),
	}
	if result.BackupType == "" {
		result.BackupType = "FULL"
	}

	switch {
	case isMySQLPhysicalDriverType(string(set.DriverType)):
		result.RestoreKind = restoreKindPhysical
		chain, chainErr := resolveMySQLPhysicalRestoreChain(set)
		if chainErr != nil {
			result.IsFull = strings.EqualFold(result.BackupType, "FULL")
			result.Hint = "备份链解析失败：" + chainErr.Error()
			return result, nil
		}
		appendMySQLPhysicalChain(result, chain)
		return result, nil
	case isPostgresPhysicalDriverType(string(set.DriverType)):
		result.RestoreKind = restoreKindPhysical
		result.IsFull = strings.EqualFold(result.BackupType, "FULL")
		result.ChainComplete = true
		if result.IsFull {
			result.Hint = "该备份集为全量物理备份，恢复时直接回填基线数据。"
		} else {
			result.Hint = fmt.Sprintf("该备份集为 %s 物理备份，恢复时按仓库内所需基线自动回放。", result.BackupType)
		}
		return result, nil
	}

	result.RestoreKind = restoreKindLogical
	result.IsFull = true
	setFile, fileErr := ValidateBackupSetFile(set)
	if fileErr != nil {
		result.Hint = "备份文件当前不可访问，无法解析恢复对象：" + fileErr.Error()
		return result, nil
	}
	switch {
	case strings.EqualFold(strings.TrimSpace(result.DBType), "MySQL"):
		appendMySQLLogicalObjects(result, set, setFile)
	case isPostgresLogicalFamily(result.DBType):
		appendPostgresLogicalObjects(result, set, setFile)
	default:
		result.Hint = "暂不支持解析该数据库类型的备份对象。"
	}
	return result, nil
}

func appendMySQLPhysicalChain(result *BackupSetObjectsResult, chain []mysqlPhysicalRestoreChainNode) {
	if len(chain) == 0 {
		return
	}
	result.ChainComplete = true
	result.IsFull = len(chain) == 1
	items := make([]BackupSetChainItem, 0, len(chain))
	for _, node := range chain {
		items = append(items, BackupSetChainItem{
			Order:      node.Order,
			BackupType: node.BackupType,
			RunID:      node.RunID,
			TaskID:     node.TaskID,
			SizeBytes:  node.SizeBytes,
			FromLSN:    node.FromLSN,
			ToLSN:      node.ToLSN,
		})
	}
	result.Chain = items
	if len(chain) == 1 {
		result.Hint = "该备份集为全量物理备份，恢复时直接回填基线数据。"
		return
	}
	result.Hint = fmt.Sprintf("该备份集为非全量物理备份，恢复时将自动按 %d 层备份链（全量 + 增量）合并回填。", len(chain))
}

func appendMySQLLogicalObjects(result *BackupSetObjectsResult, set *backupModels.InsightBackupSets, setFile string) {
	lower := strings.ToLower(strings.TrimSpace(setFile))
	if strings.HasSuffix(lower, ".zip") {
		plans, err := inspectMySQLRestoreZipSources(setFile, logicalRestoreRunOptions{})
		if err != nil {
			result.Hint = "无法解析备份集内的恢复对象：" + err.Error()
			return
		}
		result.ArtifactFormat = "zip"
		result.SupportsObjectSelect = true
		for _, plan := range plans {
			result.Objects = append(result.Objects, BackupSetObjectItem{
				Name:   plan.SourceObjectName,
				Entry:  plan.EntryName,
				Format: plan.Format,
			})
		}
		return
	}
	result.ArtifactFormat = ternaryString(strings.HasSuffix(lower, ".gz"), "sql.gz", "sql")
	names := extractMySQLDumpDatabases(setFile)
	if len(names) == 0 {
		if source := strings.TrimSpace(set.SourceDBName); source != "" {
			names = []string{source}
		}
	}
	for _, name := range names {
		result.Objects = append(result.Objects, BackupSetObjectItem{Name: name, Format: result.ArtifactFormat})
	}
	result.Hint = "该备份为单对象产物，恢复时按整体导入，不支持按对象拆分。"
}

func appendPostgresLogicalObjects(result *BackupSetObjectsResult, set *backupModels.InsightBackupSets, setFile string) {
	lower := strings.ToLower(strings.TrimSpace(setFile))
	if strings.HasSuffix(lower, ".zip") {
		sources, err := inspectPostgresLogicalRestoreSources(setFile)
		if err != nil {
			result.Hint = "无法解析备份集内的恢复对象：" + err.Error()
			return
		}
		result.ArtifactFormat = "zip"
		result.SupportsObjectSelect = true
		for _, source := range sources {
			result.Objects = append(result.Objects, BackupSetObjectItem{
				Name:   source.SourceDBName,
				Entry:  source.EntryName,
				Format: ternaryString(source.IsDir, "directory", "custom"),
				IsDir:  source.IsDir,
			})
		}
		return
	}
	format := "custom"
	switch {
	case strings.HasSuffix(lower, ".sql.gz"):
		format = "plain.gz"
	case strings.HasSuffix(lower, ".sql"):
		format = "plain"
	}
	result.ArtifactFormat = format
	name := strings.TrimSpace(set.SourceDBName)
	if name == "" {
		name = strings.TrimSpace(set.Name)
	}
	if name != "" {
		result.Objects = append(result.Objects, BackupSetObjectItem{Name: name, Format: format})
	}
	result.Hint = "该备份为单对象产物，恢复时按整体导入，不支持按对象拆分。"
}
