package group

import "gorm.io/gorm"

// DeletionImpact 描述删除一个或多个模型后对某个分组的只读影响。
type DeletionImpact struct {
	GroupID        int64    `json:"group_id"`
	Name           string   `json:"name"`
	Strategy       Strategy `json:"strategy"`
	RemovedItemIDs []int64  `json:"removed_item_ids"`
	ClearsActive   bool     `json:"clears_active"`
	BecomesEmpty   bool     `json:"becomes_empty"`
}

// PreviewDeletionImpact 计算删除指定模型后受影响的分组，只读、不修改任何数据。
// 结果按分组 ID 升序，removed_item_ids 按分组项 ID 升序；无影响时返回空切片而非 nil。
// 只执行固定三次查询，不随分组数量增加。
func PreviewDeletionImpact(db *gorm.DB, modelIDs []int64) ([]DeletionImpact, error) {
	impacts := make([]DeletionImpact, 0)
	if len(modelIDs) == 0 {
		return impacts, nil
	}

	var removed []GroupItem
	if err := db.Where("model_id IN ?", modelIDs).Order("id ASC").Find(&removed).Error; err != nil {
		return nil, err
	}
	if len(removed) == 0 {
		return impacts, nil
	}

	groupIDs := make([]int64, 0, len(removed))
	seen := make(map[int64]bool, len(removed))
	for _, item := range removed {
		if !seen[item.GroupID] {
			seen[item.GroupID] = true
			groupIDs = append(groupIDs, item.GroupID)
		}
	}
	var groups []Group
	if err := db.Where("id IN ?", groupIDs).Order("id ASC").Find(&groups).Error; err != nil {
		return nil, err
	}
	// 删除后的空组判断必须统计全部成员，包括引用已缺失模型的历史项。
	var allItems []GroupItem
	if err := db.Where("group_id IN ?", groupIDs).Find(&allItems).Error; err != nil {
		return nil, err
	}

	removedByGroup := make(map[int64][]int64, len(groupIDs))
	for _, item := range removed {
		removedByGroup[item.GroupID] = append(removedByGroup[item.GroupID], item.ID)
	}
	totalByGroup := make(map[int64]int64, len(groupIDs))
	for _, item := range allItems {
		totalByGroup[item.GroupID]++
	}
	for _, g := range groups {
		itemIDs := removedByGroup[g.ID]
		impacts = append(impacts, DeletionImpact{
			GroupID:        g.ID,
			Name:           g.Name,
			Strategy:       g.Strategy,
			RemovedItemIDs: append(make([]int64, 0, len(itemIDs)), itemIDs...),
			ClearsActive:   g.ActiveItemID != 0 && containsItemID(itemIDs, g.ActiveItemID),
			BecomesEmpty:   totalByGroup[g.ID] == int64(len(itemIDs)),
		})
	}
	return impacts, nil
}

// containsItemID 判断分组项 ID 是否在切片中。
func containsItemID(ids []int64, target int64) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}
