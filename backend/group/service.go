package group

import (
	"time"

	"gorm.io/gorm"

	"github.com/Lixiuxiu559/portunus/backend/model"
	"github.com/Lixiuxiu559/portunus/backend/shared"
)

// ErrInvalid 表示分组字段校验不通过。
var ErrInvalid = &shared.StatusError{Status: 400, Message: "分组字段不合法"}

// ErrModelNotFound 表示分组项引用的模型不存在。
var ErrModelNotFound = &shared.StatusError{Status: 400, Message: "模型不存在"}

// CreateRequest 创建分组请求。
type CreateRequest struct {
	Name     string           `json:"name" binding:"required"`
	Strategy Strategy         `json:"strategy" binding:"required"`
	Items    []ItemAddRequest `json:"items"` // 可选，初始模型
}

// UpdateRequest 更新分组请求，仅包含需要变更的字段。
type UpdateRequest struct {
	Name         *string   `json:"name"`
	Strategy     *Strategy `json:"strategy"`
	ActiveItemID *int64    `json:"active_item_id"`
}

// ItemAddRequest 添加分组项请求。
type ItemAddRequest struct {
	ModelID  int64 `json:"model_id" binding:"required"`
	Priority int   `json:"priority"`
}

// ItemUpdateRequest 更新分组项请求。
type ItemUpdateRequest struct {
	Priority *int `json:"priority"`
}

// ItemResponse 是分组项的对外响应。
type ItemResponse struct {
	ID       int64 `json:"id"`
	GroupID  int64 `json:"group_id"`
	ModelID  int64 `json:"model_id"`
	Priority int   `json:"priority"`
}

// Response 是分组的对外响应。
type Response struct {
	ID           int64          `json:"id"`
	Name         string         `json:"name"`
	Strategy     Strategy       `json:"strategy"`
	ActiveItemID int64          `json:"active_item_id"`
	Items        []ItemResponse `json:"items"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

// ToResponse 将实体转为响应。
func (g *Group) ToResponse() Response {
	items := make([]ItemResponse, 0, len(g.Items))
	for _, it := range g.Items {
		items = append(items, ItemResponse{
			ID:       it.ID,
			GroupID:  it.GroupID,
			ModelID:  it.ModelID,
			Priority: it.Priority,
		})
	}
	return Response{
		ID:           g.ID,
		Name:         g.Name,
		Strategy:     g.Strategy,
		ActiveItemID: g.ActiveItemID,
		Items:        items,
		CreatedAt:    g.CreatedAt,
		UpdatedAt:    g.UpdatedAt,
	}
}

// List 返回所有分组（含分组项，按 priority 排序）。
func List() ([]Group, error) {
	var gs []Group
	err := shared.DB.Preload("Items", orderByPriority).Order("id").Find(&gs).Error
	return gs, err
}

// Get 返回单个分组（含分组项）。
func Get(id int64) (*Group, error) {
	var g Group
	if err := shared.DB.Preload("Items", orderByPriority).First(&g, id).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

// GetByName 按分组名返回分组（含分组项，按 priority 排序）。
func GetByName(name string) (*Group, error) {
	var g Group
	if err := shared.DB.Preload("Items", orderByPriority).Where("name = ?", name).First(&g).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

// Create 创建分组，可同时传入初始分组项。
func Create(req CreateRequest) (*Group, error) {
	if req.Name == "" || !req.Strategy.Valid() {
		return nil, ErrInvalid
	}
	// 预校验所有模型存在（只读，无需进事务）
	for _, it := range req.Items {
		ok, err := model.Exists(it.ModelID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, ErrModelNotFound
		}
	}
	g := Group{Name: req.Name, Strategy: req.Strategy}
	err := shared.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&g).Error; err != nil {
			return err
		}
		for _, it := range req.Items {
			item := GroupItem{GroupID: g.ID, ModelID: it.ModelID, Priority: it.Priority}
			if err := tx.Create(&item).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return Get(g.ID)
}

// Update 更新分组基础字段，仅应用请求中非 nil 的字段。
func Update(id int64, req UpdateRequest) (*Group, error) {
	var g Group
	if err := shared.DB.First(&g, id).Error; err != nil {
		return nil, err
	}
	if req.Name != nil {
		if *req.Name == "" {
			return nil, ErrInvalid
		}
		g.Name = *req.Name
	}
	if req.Strategy != nil {
		if !req.Strategy.Valid() {
			return nil, ErrInvalid
		}
		g.Strategy = *req.Strategy
	}
	if req.ActiveItemID != nil {
		if *req.ActiveItemID != 0 && !itemInGroup(*req.ActiveItemID, id) {
			return nil, ErrInvalid
		}
		g.ActiveItemID = *req.ActiveItemID
	}
	if err := shared.DB.Save(&g).Error; err != nil {
		return nil, err
	}
	return Get(id)
}

// Delete 删除分组及其全部分组项。
func Delete(id int64) error {
	return shared.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", id).Delete(&GroupItem{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Group{}, id).Error
	})
}

// AddItem 向分组添加一个模型。
func AddItem(groupID int64, req ItemAddRequest) (*GroupItem, error) {
	var g Group
	if err := shared.DB.First(&g, groupID).Error; err != nil {
		return nil, err
	}
	ok, err := model.Exists(req.ModelID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrModelNotFound
	}
	item := GroupItem{GroupID: groupID, ModelID: req.ModelID, Priority: req.Priority}
	if err := shared.DB.Create(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// UpdateItem 更新分组项的 priority。
func UpdateItem(groupID, itemID int64, req ItemUpdateRequest) (*GroupItem, error) {
	var item GroupItem
	if err := shared.DB.Where("id = ? AND group_id = ?", itemID, groupID).First(&item).Error; err != nil {
		return nil, err
	}
	if req.Priority != nil {
		item.Priority = *req.Priority
	}
	if err := shared.DB.Save(&item).Error; err != nil {
		return nil, err
	}
	return &item, nil
}

// DeleteItem 从分组中删除一个分组项。
func DeleteItem(groupID, itemID int64) error {
	return shared.DB.Where("id = ? AND group_id = ?", itemID, groupID).Delete(&GroupItem{}).Error
}

// DeleteItemsByModelIDsTx 在事务中删除引用指定模型的全部分组项，
// 并把指向这些分组项的 ActiveItemID 清零。
func DeleteItemsByModelIDsTx(tx *gorm.DB, modelIDs []int64) error {
	if len(modelIDs) == 0 {
		return nil
	}
	var itemIDs []int64
	if err := tx.Model(&GroupItem{}).Where("model_id IN ?", modelIDs).Pluck("id", &itemIDs).Error; err != nil {
		return err
	}
	if len(itemIDs) == 0 {
		return nil
	}
	if err := tx.Model(&Group{}).Where("active_item_id IN ?", itemIDs).Update("active_item_id", 0).Error; err != nil {
		return err
	}
	return tx.Where("id IN ?", itemIDs).Delete(&GroupItem{}).Error
}

// orderByPriority 让分组项按 priority 升序加载。
func orderByPriority(db *gorm.DB) *gorm.DB {
	return db.Order("priority")
}

// itemInGroup 判断分组项是否属于指定分组。
func itemInGroup(itemID, groupID int64) bool {
	var count int64
	if err := shared.DB.Model(&GroupItem{}).Where("id = ? AND group_id = ?", itemID, groupID).Count(&count).Error; err != nil {
		return false
	}
	return count > 0
}
