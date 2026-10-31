package chat

// SendMessageInput 发送文本消息。
type SendMessageInput struct {
	PeerUID     int64  `json:"peer_uid"`
	Text        string `json:"text"`
	ClientMsgID string `json:"client_msg_id"`
}

// PeerBrief 会话对端摘要。
type PeerBrief struct {
	UID      int64  `json:"uid"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// MessageView 客户端消息模型。
type MessageView struct {
	ID          int64  `json:"id"`
	MsgID       string `json:"msg_id"`
	ClientMsgID string `json:"client_msg_id,omitempty"`
	ConvID      int64  `json:"conv_id"`
	FromUID     int64  `json:"from_uid"`
	ToUID       int64  `json:"to_uid"`
	Type        int    `json:"type"`
	Text        string `json:"text,omitempty"`
	Status      int    `json:"status"`
	SentAt      int64  `json:"sent_at"`
	RecalledAt  int64  `json:"recalled_at,omitempty"`
	Mine        bool   `json:"mine"`
}

// ConversationItem 会话列表项。
type ConversationItem struct {
	ConvID      int64     `json:"conv_id"`
	Peer        PeerBrief `json:"peer"`
	LastMsgID   int64     `json:"last_msg_id"`
	LastType    int       `json:"last_type"`
	LastPreview string    `json:"last_preview"`
	LastAt      int64     `json:"last_at"`
	UnreadCount int       `json:"unread_count"`
	Pinned      bool      `json:"pinned"`
	Muted       bool      `json:"muted"`
}

// ConversationList 分页会话列表。
type ConversationList struct {
	List        []ConversationItem `json:"list"`
	Page        int                `json:"page"`
	Size        int                `json:"size"`
	Total       int64              `json:"total"`
	UnreadTotal int                `json:"unread_total"`
}

// MessageList 消息历史与对端资料。
type MessageList struct {
	Peer       PeerBrief     `json:"peer"`
	List       []MessageView `json:"list"`
	NextCursor int64         `json:"next_cursor"`
	HasMore    bool          `json:"has_more"`
}

// ReadResult 已读同步结果。
type ReadResult struct {
	PeerUID      int64 `json:"peer_uid"`
	LastReadSeq  int64 `json:"last_read_seq"`
	UnreadCount  int   `json:"unread_count"`
	RemoteSynced bool  `json:"remote_synced"`
}

// RecallResult 撤回结果。
type RecallResult struct {
	MsgID        string `json:"msg_id"`
	Status       int    `json:"status"`
	RecalledAt   int64  `json:"recalled_at"`
	RemoteSynced bool   `json:"remote_synced"`
}
