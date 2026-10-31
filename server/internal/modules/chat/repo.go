package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// Repository 聊天模块数据访问层。
type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }

var ErrNoDB = errors.New("chat: 数据库未初始化")

func (r *Repository) conn() (*gorm.DB, error) {
	if r == nil || r.db == nil {
		return nil, ErrNoDB
	}
	return r.db, nil
}

// FindUser 查询正常用户，不存在返回 nil。
func (r *Repository) FindUser(ctx context.Context, uid int64) (*User, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var u User
	err = db.WithContext(ctx).Where("id = ? AND deleted_at IS NULL", uid).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat: 查询用户失败: %w", err)
	}
	return &u, nil
}

// BlockedEitherWay 判断两人任一方向是否存在拉黑。
func (r *Repository) BlockedEitherWay(ctx context.Context, a, b int64) (bool, error) {
	db, err := r.conn()
	if err != nil {
		return false, err
	}
	var n int64
	err = db.WithContext(ctx).Table("user_block").
		Where("((user_id = ? AND blocked_user_id = ?) OR (user_id = ? AND blocked_user_id = ?)) AND deleted_at IS NULL", a, b, b, a).
		Count(&n).Error
	if err != nil {
		return false, fmt.Errorf("chat: 查询拉黑关系失败: %w", err)
	}
	return n > 0, nil
}

func pair(a, b int64) (int64, int64) {
	if a < b {
		return a, b
	}
	return b, a
}

// FindMessageByClient 按发送者+客户端幂等键查询消息。
func (r *Repository) FindMessageByClient(ctx context.Context, fromUID int64, clientMsgID string) (*Message, error) {
	if clientMsgID == "" {
		return nil, nil
	}
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var m Message
	err = db.WithContext(ctx).Where("from_uid = ? AND client_msg_id = ?", fromUID, clientMsgID).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat: 查询幂等消息失败: %w", err)
	}
	return &m, nil
}

// StoreMessage 幂等写入消息，并在同一事务中更新会话摘要与双方成员游标。
// 返回 inserted=false 表示 msg_id 或 client_msg_id 已存在，不会重复增加未读数。
func (r *Repository) StoreMessage(ctx context.Context, m *Message, preview string) (*Message, bool, error) {
	db, err := r.conn()
	if err != nil {
		return nil, false, err
	}
	if m == nil || m.MsgID == "" {
		return nil, false, fmt.Errorf("chat: msg_id 不能为空")
	}
	a, b := pair(m.FromUID, m.ToUID)
	var stored Message
	inserted := false

	err = db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		const ensureConv = `INSERT INTO conversation
			(user_a_id, user_b_id, created_at, updated_at)
			VALUES (?, ?, NOW(), NOW())
			ON DUPLICATE KEY UPDATE id = id`
		if err := tx.Exec(ensureConv, a, b).Error; err != nil {
			return fmt.Errorf("chat: 创建会话失败: %w", err)
		}
		var conv Conversation
		if err := tx.Where("user_a_id = ? AND user_b_id = ?", a, b).First(&conv).Error; err != nil {
			return fmt.Errorf("chat: 回查会话失败: %w", err)
		}
		m.ConvID = conv.ID
		if len(m.Content) == 0 {
			m.Content = json.RawMessage(`{}`)
		}
		if m.Status == 0 {
			m.Status = MessageStatusNormal
		}
		if m.SentAt.IsZero() {
			m.SentAt = time.Now()
		}

		var client any
		if m.ClientMsgID != nil && *m.ClientMsgID != "" {
			client = *m.ClientMsgID
		}
		const insertMsg = `INSERT IGNORE INTO message
			(msg_id, msg_key, client_msg_id, conv_id, from_uid, to_uid, msg_type, content, seq, status, sent_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NOW(), NOW())`
		res := tx.Exec(insertMsg, m.MsgID, m.MsgKey, client, m.ConvID, m.FromUID, m.ToUID,
			m.MsgType, string(m.Content), m.Seq, m.Status, m.SentAt)
		if res.Error != nil {
			return fmt.Errorf("chat: 写入消息失败: %w", res.Error)
		}
		inserted = res.RowsAffected == 1
		if !inserted {
			q := tx.Where("msg_id = ?", m.MsgID)
			if m.ClientMsgID != nil && *m.ClientMsgID != "" {
				q = tx.Where("msg_id = ? OR (from_uid = ? AND client_msg_id = ?)", m.MsgID, m.FromUID, *m.ClientMsgID)
			}
			if err := q.First(&stored).Error; err != nil {
				return fmt.Errorf("chat: 回查重复消息失败: %w", err)
			}
			// 回调通常比 REST 回包晚；补齐此前可能为空的 MsgKey，但不改正文与未读。
			if stored.MsgKey == "" && m.MsgKey != "" {
				_ = tx.Model(&Message{}).Where("id = ?", stored.ID).Update("msg_key", m.MsgKey).Error
				stored.MsgKey = m.MsgKey
			}
			return nil
		}
		if err := tx.Where("msg_id = ?", m.MsgID).First(&stored).Error; err != nil {
			return fmt.Errorf("chat: 回查新消息失败: %w", err)
		}

		if err := tx.Model(&Conversation{}).Where("id = ?", conv.ID).
			UpdateColumn("msg_count", gorm.Expr("msg_count + 1")).Error; err != nil {
			return fmt.Errorf("chat: 更新会话消息数失败: %w", err)
		}
		const updateLast = `UPDATE conversation
			SET last_msg_id = ?, last_msg_type = ?, last_msg_preview = ?, last_msg_at = ?, updated_at = NOW()
			WHERE id = ? AND (last_msg_at IS NULL OR last_msg_at <= ?)`
		if err := tx.Exec(updateLast, stored.ID, stored.MsgType, preview, stored.SentAt, conv.ID, stored.SentAt).Error; err != nil {
			return fmt.Errorf("chat: 更新会话摘要失败: %w", err)
		}

		const upsertSender = `INSERT INTO conversation_member
			(conv_id, user_id, last_msg_seq, unread_count, is_deleted, created_at, updated_at)
			VALUES (?, ?, ?, 0, 0, NOW(), NOW())
			ON DUPLICATE KEY UPDATE last_msg_seq = GREATEST(last_msg_seq, VALUES(last_msg_seq)), is_deleted = 0, updated_at = NOW()`
		if err := tx.Exec(upsertSender, conv.ID, m.FromUID, m.Seq).Error; err != nil {
			return fmt.Errorf("chat: 更新发送方会话状态失败: %w", err)
		}
		const upsertReceiver = `INSERT INTO conversation_member
			(conv_id, user_id, last_msg_seq, unread_count, is_deleted, created_at, updated_at)
			VALUES (?, ?, ?, 1, 0, NOW(), NOW())
			ON DUPLICATE KEY UPDATE last_msg_seq = GREATEST(last_msg_seq, VALUES(last_msg_seq)), unread_count = unread_count + 1, is_deleted = 0, updated_at = NOW()`
		if err := tx.Exec(upsertReceiver, conv.ID, m.ToUID, m.Seq).Error; err != nil {
			return fmt.Errorf("chat: 更新接收方未读失败: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return &stored, inserted, nil
}

// conversationRow 是会话列表联表结果。
type conversationRow struct {
	ConvID         int64      `gorm:"column:conv_id"`
	PeerUID        int64      `gorm:"column:peer_uid"`
	PeerNickname   string     `gorm:"column:peer_nickname"`
	PeerAvatar     string     `gorm:"column:peer_avatar"`
	LastMsgID      int64      `gorm:"column:last_msg_id"`
	LastMsgType    int        `gorm:"column:last_msg_type"`
	LastMsgPreview string     `gorm:"column:last_msg_preview"`
	LastMsgAt      *time.Time `gorm:"column:last_msg_at"`
	UnreadCount    int        `gorm:"column:unread_count"`
	IsPinned       int        `gorm:"column:is_pinned"`
	IsMuted        int        `gorm:"column:is_muted"`
}

// ListConversations 查询会话列表和总未读。
func (r *Repository) ListConversations(ctx context.Context, uid int64, page, size int) ([]conversationRow, int64, int, error) {
	db, err := r.conn()
	if err != nil {
		return nil, 0, 0, err
	}
	base := db.WithContext(ctx).Table("conversation_member cm").
		Joins("JOIN conversation c ON c.id = cm.conv_id").
		Joins("JOIN user u ON u.id = IF(c.user_a_id = ?, c.user_b_id, c.user_a_id) AND u.deleted_at IS NULL", uid).
		Where("cm.user_id = ? AND cm.is_deleted = 0", uid)
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, 0, fmt.Errorf("chat: 统计会话失败: %w", err)
	}
	var unread int
	if err := db.WithContext(ctx).Table("conversation_member").
		Select("COALESCE(SUM(unread_count), 0)").
		Where("user_id = ? AND is_deleted = 0", uid).Scan(&unread).Error; err != nil {
		return nil, 0, 0, fmt.Errorf("chat: 统计未读失败: %w", err)
	}
	var rows []conversationRow
	err = base.Select(`c.id AS conv_id,
		IF(c.user_a_id = ?, c.user_b_id, c.user_a_id) AS peer_uid,
		u.nickname AS peer_nickname, u.avatar AS peer_avatar,
		c.last_msg_id, c.last_msg_type, c.last_msg_preview, c.last_msg_at,
		cm.unread_count, cm.is_pinned, cm.is_muted`, uid).
		Order("cm.is_pinned DESC, c.last_msg_at DESC, c.id DESC").
		Offset((page - 1) * size).Limit(size).Scan(&rows).Error
	if err != nil {
		return nil, 0, 0, fmt.Errorf("chat: 查询会话列表失败: %w", err)
	}
	return rows, total, unread, nil
}

// FindConversation 查两人的会话，不存在返回 nil。
func (r *Repository) FindConversation(ctx context.Context, a, b int64) (*Conversation, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	a, b = pair(a, b)
	var conv Conversation
	err = db.WithContext(ctx).Where("user_a_id = ? AND user_b_id = ?", a, b).First(&conv).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat: 查询会话失败: %w", err)
	}
	return &conv, nil
}

// ListMessages 倒序取消息后翻转为时间正序。
func (r *Repository) ListMessages(ctx context.Context, convID, beforeID int64, limit int) ([]Message, bool, error) {
	db, err := r.conn()
	if err != nil {
		return nil, false, err
	}
	q := db.WithContext(ctx).Where("conv_id = ?", convID)
	if beforeID > 0 {
		q = q.Where("id < ?", beforeID)
	}
	var list []Message
	if err := q.Order("id DESC").Limit(limit + 1).Find(&list).Error; err != nil {
		return nil, false, fmt.Errorf("chat: 查询消息历史失败: %w", err)
	}
	hasMore := len(list) > limit
	if hasMore {
		list = list[:limit]
	}
	for i, j := 0, len(list)-1; i < j; i, j = i+1, j-1 {
		list[i], list[j] = list[j], list[i]
	}
	return list, hasMore, nil
}

// MarkRead 把 uid 对 peer 的入站消息标记已读，返回已读游标。
func (r *Repository) MarkRead(ctx context.Context, uid, peerUID int64) (int64, error) {
	db, err := r.conn()
	if err != nil {
		return 0, err
	}
	conv, err := r.FindConversation(ctx, uid, peerUID)
	if err != nil || conv == nil {
		return 0, err
	}
	var seq int64
	if err := db.WithContext(ctx).Table("message").Select("COALESCE(MAX(seq), 0)").
		Where("conv_id = ? AND from_uid = ? AND to_uid = ?", conv.ID, peerUID, uid).
		Scan(&seq).Error; err != nil {
		return 0, fmt.Errorf("chat: 查询已读游标失败: %w", err)
	}
	const q = `UPDATE conversation_member
		SET last_read_seq = GREATEST(last_read_seq, ?), unread_count = 0, updated_at = NOW()
		WHERE conv_id = ? AND user_id = ?`
	if err := db.WithContext(ctx).Exec(q, seq, conv.ID, uid).Error; err != nil {
		return 0, fmt.Errorf("chat: 更新已读状态失败: %w", err)
	}
	return seq, nil
}

// FindMessageForRecall 查询发送者自己的消息。
func (r *Repository) FindMessageForRecall(ctx context.Context, uid int64, msgID string) (*Message, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var m Message
	err = db.WithContext(ctx).Where("msg_id = ? AND from_uid = ?", msgID, uid).First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat: 查询待撤回消息失败: %w", err)
	}
	return &m, nil
}

// FindMessageByReference 按 MsgId 或 MsgKey 查询消息，不存在返回 nil。
func (r *Repository) FindMessageByReference(ctx context.Context, msgID, msgKey string) (*Message, error) {
	db, err := r.conn()
	if err != nil {
		return nil, err
	}
	var m Message
	q := db.WithContext(ctx)
	switch {
	case msgID != "" && msgKey != "":
		q = q.Where("msg_id = ? OR msg_key = ?", msgID, msgKey)
	case msgID != "":
		q = q.Where("msg_id = ?", msgID)
	case msgKey != "":
		q = q.Where("msg_key = ?", msgKey)
	default:
		return nil, nil
	}
	err = q.First(&m).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("chat: 查询消息引用失败: %w", err)
	}
	return &m, nil
}

// RecallMessage 幂等更新撤回状态，并在它是最后消息时刷新会话摘要。
func (r *Repository) RecallMessage(ctx context.Context, msgID string, operatorID int64, at time.Time) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m Message
		if err := tx.Where("msg_id = ?", msgID).First(&m).Error; err != nil {
			return fmt.Errorf("chat: 查询撤回消息失败: %w", err)
		}
		if m.Status == MessageStatusRecalled {
			return nil
		}
		if err := tx.Model(&Message{}).Where("id = ?", m.ID).Updates(map[string]any{
			"status": MessageStatusRecalled, "recalled_at": at, "recalled_by": operatorID,
		}).Error; err != nil {
			return fmt.Errorf("chat: 更新撤回状态失败: %w", err)
		}
		if err := tx.Model(&Conversation{}).Where("id = ? AND last_msg_id = ?", m.ConvID, m.ID).
			Updates(map[string]any{"last_msg_preview": "一条消息已撤回", "last_msg_type": MessageTypeSystem}).Error; err != nil {
			return fmt.Errorf("chat: 更新撤回摘要失败: %w", err)
		}
		return nil
	})
}

// BeginCallback 登记回调。duplicateProcessed=true 表示同一报文已成功处理，可直接应答。
func (r *Repository) BeginCallback(ctx context.Context, sdkAppID int64, command, kind, hash, raw string) (id int64, duplicateProcessed bool, err error) {
	db, err := r.conn()
	if err != nil {
		return 0, false, err
	}
	const q = `INSERT IGNORE INTO im_callback_log
		(sdk_app_id, callback_type, command, payload_hash, raw, processed, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, 0, NOW(), NOW())`
	res := db.WithContext(ctx).Exec(q, sdkAppID, kind, command, hash, raw)
	if res.Error != nil {
		return 0, false, fmt.Errorf("chat: 登记 IM 回调失败: %w", res.Error)
	}
	var rec CallbackLog
	if err := db.WithContext(ctx).Where("payload_hash = ?", hash).First(&rec).Error; err != nil {
		return 0, false, fmt.Errorf("chat: 回查 IM 回调失败: %w", err)
	}
	if res.RowsAffected == 0 && rec.Processed != 1 {
		_ = db.WithContext(ctx).Model(&CallbackLog{}).Where("id = ?", rec.ID).
			UpdateColumn("retry_count", gorm.Expr("retry_count + 1")).Error
	}
	return rec.ID, res.RowsAffected == 0 && rec.Processed == 1, nil
}

// FinishCallback 写回回调处理结果。
func (r *Repository) FinishCallback(ctx context.Context, id int64, processErr error) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	state, msg := 1, ""
	if processErr != nil {
		state, msg = 2, processErr.Error()
		if len(msg) > 255 {
			msg = msg[:255]
		}
	}
	now := time.Now()
	if err := db.WithContext(ctx).Model(&CallbackLog{}).Where("id = ?", id).Updates(map[string]any{
		"processed": state, "error_msg": msg, "processed_at": now,
	}).Error; err != nil {
		return fmt.Errorf("chat: 更新 IM 回调状态失败: %w", err)
	}
	return nil
}

// InsertModeration 留存本地敏感词审核结果。
func (r *Repository) InsertModeration(ctx context.Context, bizID string, uid int64, result int, label string, raw string) error {
	db, err := r.conn()
	if err != nil {
		return err
	}
	var rawPtr *string
	if raw != "" {
		rawPtr = &raw
	}
	rec := &ModerationRecord{
		BizType: 1, BizID: bizID, UserID: uid, Provider: 1,
		Scene: "im_text", Label: label, Result: result, Raw: rawPtr,
	}
	if err := db.WithContext(ctx).Create(rec).Error; err != nil {
		return fmt.Errorf("chat: 写消息审核记录失败: %w", err)
	}
	return nil
}
