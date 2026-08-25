package logic

// ItemChanged 判断同步到的工作项是否需要更新本地缓存与向量。
// 触发条件：远端更新时间变化、标题/描述变化、或归档项重新出现。
func ItemChanged(oldTitle, oldDesc, oldRemoteUpdated string, oldArchived bool, newTitle, newDesc, newRemoteUpdated string) bool {
	if oldArchived {
		return true // 平台侧已删除过的项重新出现，恢复并重新向量化
	}
	if newRemoteUpdated != oldRemoteUpdated {
		return true
	}
	if newTitle != oldTitle || newDesc != oldDesc {
		return true
	}
	return false
}

// ProjectChanged 项目维度的同步比对，规则同 ItemChanged。
func ProjectChanged(oldName, oldDesc, oldRemoteUpdated string, oldArchived bool, newName, newDesc, newRemoteUpdated string) bool {
	return ItemChanged(oldName, oldDesc, oldRemoteUpdated, oldArchived, newName, newDesc, newRemoteUpdated)
}
