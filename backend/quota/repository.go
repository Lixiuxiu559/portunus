package quota

import (
	"errors"
	"gorm.io/gorm"
)

func InvalidateInheritedSourcesTx(tx *gorm.DB, channelID int64) error {
	var sources []Source
	if err := tx.Where("channel_id = ? AND credential_mode = ?", channelID, CredentialInherit).Find(&sources).Error; err != nil {
		return err
	}
	for _, s := range sources {
		if err := tx.Model(&Source{}).Where("id = ?", s.ID).UpdateColumn("config_revision", gorm.Expr("config_revision + 1")).Error; err != nil {
			return err
		}
		if err := tx.Where("source_id = ?", s.ID).Delete(&State{}).Error; err != nil {
			return err
		}
	}
	return nil
}
func DeleteByChannelTx(tx *gorm.DB, channelID int64) error {
	ids := tx.Model(&Source{}).Select("id").Where("channel_id = ?", channelID)
	if err := tx.Where("source_id IN (?)", ids).Delete(&State{}).Error; err != nil {
		return err
	}
	return tx.Where("channel_id = ?", channelID).Delete(&Source{}).Error
}
func publicDBError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return statusError(404, "查询来源或渠道不存在")
	}
	return err
}
func readState(tx *gorm.DB, s Source) (State, error) {
	var st State
	err := tx.First(&st, "source_id = ?", s.ID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return State{SourceID: s.ID, ConfigRevision: s.ConfigRevision}, nil
	}
	if st.ConfigRevision != s.ConfigRevision {
		return State{SourceID: s.ID, ConfigRevision: s.ConfigRevision}, err
	}
	return st, err
}

func readStates(tx *gorm.DB, sources []Source) (map[int64]State, error) {
	out := make(map[int64]State, len(sources))
	ids := make([]int64, 0, len(sources))
	for _, src := range sources {
		ids = append(ids, src.ID)
		out[src.ID] = State{SourceID: src.ID, ConfigRevision: src.ConfigRevision}
	}
	if len(ids) == 0 {
		return out, nil
	}
	var states []State
	if err := tx.Where("source_id IN ?", ids).Find(&states).Error; err != nil {
		return nil, err
	}
	for _, st := range states {
		if current, ok := out[st.SourceID]; ok && current.ConfigRevision == st.ConfigRevision {
			out[st.SourceID] = st
		}
	}
	return out, nil
}
