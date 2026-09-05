package backup

// BackupInfo 是一份已完成备份的文件元数据；CreatedAt 为文件修改时间的
// unix 秒（备份文件的诞生时刻，即 VACUUM INTO 完成时间）。
type BackupInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"sizeBytes"`
	CreatedAt int64  `json:"createdAt"`
}
