package model

import (
	"fmt"
	"strings"
	"time"
)

// BizMessage 公众号消息 (来自 biz_message_0.db, 4.x macOS 微信)
// 每条公众号的每条推送对应 biz_message_0.db 里的 Msg_<md5(gh_id)> 表里的一行
type BizMessage struct {
	GHID        string    `json:"ghID"`        // 公众号 ID, 如 gh_a4a87df43a4c
	GHName      string    `json:"ghName"`      // 公众号昵称 (来源 contact.db)
	Time        time.Time `json:"time"`        // 发布时间
	Title       string    `json:"title"`       // 标题
	Desc        string    `json:"desc"`        // 摘要 / CDATA 内容
	URL         string    `json:"url"`         // 原文链接
	AppID       string    `json:"appId"`       // appmsg appid
	LocalType   int64     `json:"localType"`   // 消息类型
	LocalID     int64     `json:"localId"`     // 本地 ID
	SortSeq     int64     `json:"sortSeq"`     // 排序序号
}

// PlainText 输出适合 MCP / 普通文本展示的紧凑格式
func (m *BizMessage) PlainText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%s] %s\n", m.Time.Format("2006-01-02 15:04"), m.Title)
	if m.Desc != "" {
		desc := m.Desc
		if len(desc) > 240 {
			desc = desc[:240] + "…"
		}
		fmt.Fprintf(&b, "  %s\n", desc)
	}
	if m.URL != "" {
		fmt.Fprintf(&b, "  url: %s\n", m.URL)
	}
	return strings.TrimRight(b.String(), "\n")
}

// CSV 返回 CSV 行
func (m *BizMessage) CSV() []string {
	return []string{
		m.Time.Format("2006-01-02 15:04:05"),
		m.GHID,
		m.GHName,
		m.Title,
		m.URL,
	}
}