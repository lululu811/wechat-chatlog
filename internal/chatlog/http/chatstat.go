package http

import (
	"math"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"

	"github.com/sjzar/chatlog/internal/errors"
)

const (
	chatStatDefaultDays    = 7
	chatStatMaxDays        = 90
	chatStatDefaultTop     = 20
	chatStatMaxTop         = 100
	chatStatDefaultLimit   = 200
	chatStatMaxLimit       = 1000
	chatStatMaxSamples     = 8
	chatStatMaxSampleLen   = 200
	chatStatMaxKWPerMsg    = 6
	chatStatContactFetchTO = 5 * time.Second
)

var chatStatStopWords = map[string]bool{
	"的": true, "了": true, "是": true, "在": true, "和": true, "与": true, "等": true,
	"为": true, "于": true, "以": true, "从": true, "到": true, "上": true, "下": true,
	"一个": true, "一些": true, "我们": true, "你们": true, "他们": true, "这个": true,
	"那个": true, "可以": true, "应该": true, "怎么": true, "什么": true, "不是": true,
	"都是": true, "一种": true, "不": true, "也": true, "还": true, "只": true, "就": true,
	"才": true, "并": true, "及": true, "或": true, "吗": true, "啊": true, "嗯": true,
	"哦": true, "哈": true, "哈哈": true, "你": true, "我": true, "他": true, "她": true,
	"它": true, "吧": true, "呢": true, "没": true, "有": true, "好": true, "对": true,
	"行": true, "去": true, "来": true, "看": true, "说": true, "想": true, "知道": true,
}

type chatStatTalker struct {
	Rank        int      `json:"rank"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	IsChatRoom  bool     `json:"isChatRoom"`
	Count       int      `json:"count"`
	Sent        int      `json:"sent"`
	Received    int      `json:"received"`
	FirstAt     int64    `json:"firstAt"`
	LastAt      int64    `json:"lastAt"`
	TopKeywords []string `json:"topKeywords"`
	SampleTexts []string `json:"sampleTexts"`
}

type chatStatResponse struct {
	Days          int              `json:"days"`
	StartTime     int64            `json:"startTime"`
	EndTime       int64            `json:"endTime"`
	TotalMessages int              `json:"totalMessages"`
	TalkerCount   int              `json:"talkerCount"`
	PrivateCount  int              `json:"privateCount"`
	GroupCount    int              `json:"groupCount"`
	Talkers       []chatStatTalker `json:"talkers"`
}

func (s *Service) handleChatStat(c *gin.Context) {
	q := struct {
		Days  int `form:"days"`
		Top   int `form:"top"`
		Limit int `form:"limit"`
	}{Days: chatStatDefaultDays, Top: chatStatDefaultTop, Limit: chatStatDefaultLimit}
	_ = c.BindQuery(&q)
	if q.Days <= 0 {
		q.Days = chatStatDefaultDays
	}
	if q.Days > chatStatMaxDays {
		q.Days = chatStatMaxDays
	}
	if q.Top <= 0 {
		q.Top = chatStatDefaultTop
	}
	if q.Top > chatStatMaxTop {
		q.Top = chatStatMaxTop
	}
	if q.Limit <= 0 {
		q.Limit = chatStatDefaultLimit
	}
	if q.Limit > chatStatMaxLimit {
		q.Limit = chatStatMaxLimit
	}

	end := time.Now()
	start := end.AddDate(0, 0, -q.Days)

	talkerNames, isGroups, err := s.chatStatCollectTalkers()
	if err != nil {
		errors.Err(c, err)
		return
	}

	type bucket struct {
		name     string
		isGroup  bool
		count    int
		sent     int
		received int
		firstAt  int64
		lastAt   int64
		texts    []string
		kwFreq   map[string]int
	}
	buckets := map[string]*bucket{}

	for id, name := range talkerNames {
		msgs, err := s.db.GetMessages(start, end, id, "", "", q.Limit, 0)
		if err != nil {
			if err == errors.ErrTalkerEmpty {
				continue
			}
			errors.Err(c, err)
			return
		}
		if len(msgs) == 0 {
			continue
		}
		b := &bucket{
			name:    name,
			isGroup: isGroups[id],
			firstAt: math.MaxInt64,
			kwFreq:  map[string]int{},
		}
		for _, m := range msgs {
			b.count++
			if m.IsSelf {
				b.sent++
			} else {
				b.received++
			}
			ts := m.Time.Unix()
			if ts < b.firstAt {
				b.firstAt = ts
			}
			if ts > b.lastAt {
				b.lastAt = ts
			}
			if t := strings.TrimSpace(m.Content); t != "" && len(t) <= chatStatMaxSampleLen {
				if len(b.texts) < chatStatMaxSamples {
					b.texts = append(b.texts, t)
				}
				for _, w := range chatStatExtractKeywords(t, chatStatMaxKWPerMsg) {
					b.kwFreq[w]++
				}
			}
		}
		buckets[id] = b
	}

	priv, group := 0, 0
	talkers := make([]chatStatTalker, 0, len(buckets))
	for id, b := range buckets {
		if b.isGroup {
			group++
		} else {
			priv++
		}
		talkers = append(talkers, chatStatTalker{
			ID:          id,
			Name:        b.name,
			IsChatRoom:  b.isGroup,
			Count:       b.count,
			Sent:        b.sent,
			Received:    b.received,
			FirstAt:     b.firstAt,
			LastAt:      b.lastAt,
			TopKeywords: chatStatTopK(b.kwFreq, 8),
			SampleTexts: b.texts,
		})
	}
	sort.Slice(talkers, func(i, j int) bool {
		if talkers[i].Count != talkers[j].Count {
			return talkers[i].Count > talkers[j].Count
		}
		return talkers[i].LastAt > talkers[j].LastAt
	})
	for i := range talkers {
		talkers[i].Rank = i + 1
	}
	if len(talkers) > q.Top {
		talkers = talkers[:q.Top]
	}

	c.JSON(http.StatusOK, chatStatResponse{
		Days:          q.Days,
		StartTime:     start.Unix(),
		EndTime:       end.Unix(),
		TotalMessages: sumCounts(talkers),
		TalkerCount:   len(buckets),
		PrivateCount:  priv,
		GroupCount:    group,
		Talkers:       talkers,
	})
}

func sumCounts(talkers []chatStatTalker) int {
	n := 0
	for _, t := range talkers {
		n += t.Count
	}
	return n
}

func (s *Service) chatStatCollectTalkers() (map[string]string, map[string]bool, error) {
	names := map[string]string{}
	isGroup := map[string]bool{}

	contactsResp, err := s.db.GetContacts("", 0, 0)
	if err == nil && contactsResp != nil {
		for _, c := range contactsResp.Items {
			if c == nil || c.UserName == "" {
				continue
			}
			display := chatStatDisplayName(c.Alias, c.Remark, c.NickName, c.UserName)
			names[c.UserName] = display
			isGroup[c.UserName] = false
		}
	}

	roomsResp, err := s.db.GetChatRooms("", 0, 0)
	if err == nil && roomsResp != nil {
		for _, r := range roomsResp.Items {
			if r == nil || r.Name == "" {
				continue
			}
			display := r.NickName
			if display == "" {
				display = r.Name
			}
			names[r.Name] = display
			isGroup[r.Name] = true
		}
	}

	if len(names) == 0 {
		return nil, nil, errors.ErrDBNotReady
	}
	return names, isGroup, nil
}

func chatStatDisplayName(alias, remark, nick, fallback string) string {
	for _, s := range []string{alias, remark, nick} {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return fallback
}

func chatStatTopK(freq map[string]int, k int) []string {
	if len(freq) == 0 || k <= 0 {
		return nil
	}
	type kv struct {
		k string
		v int
	}
	all := make([]kv, 0, len(freq))
	for k, v := range freq {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].v != all[j].v {
			return all[i].v > all[j].v
		}
		return all[i].k < all[j].k
	})
	if len(all) > k {
		all = all[:k]
	}
	out := make([]string, len(all))
	for i, kv := range all {
		out[i] = kv.k
	}
	return out
}

func chatStatExtractKeywords(text string, maxPerText int) []string {
	runes := make([]rune, 0, len(text))
	for _, r := range text {
		if unicode.Is(unicode.Han, r) || unicode.IsLetter(r) || unicode.IsDigit(r) {
			runes = append(runes, r)
		}
	}
	if len(runes) == 0 {
		return nil
	}
	out := make([]string, 0, maxPerText*2)
	count := 0
	for n := 2; n <= 3 && count < maxPerText; n++ {
		for i := 0; i+n <= len(runes) && count < maxPerText; i++ {
			gram := string(runes[i : i+n])
			if chatStatStopWords[gram] {
				continue
			}
			out = append(out, gram)
			count++
		}
	}
	return out
}
