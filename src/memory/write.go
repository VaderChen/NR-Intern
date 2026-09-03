package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"AgenticService/src/domain"
)

// Remember 是回憶空間唯一的寫入入口。
//
// 工具過去直接呼叫 Repository，等於繞過所有策略：什麼都收、重複的照收、
// 永遠不淘汰。跨對話共用的記憶如果沒有節制，半年後就是一堆彼此矛盾又過時的
// 內容每輪塞進提示。因此准入、去重與淘汰都集中在這裡。
func (m *Manager) Remember(ctx context.Context, session domain.Session, input domain.RememberMemoryInput) (domain.Memory, error) {
	if m == nil || m.Repository == nil {
		return domain.Memory{}, fmt.Errorf("memory repository is unavailable")
	}
	space := m.space()
	if strings.TrimSpace(input.Scope) == "" {
		input.Scope = ScopeForSessionWithSpace(session, space.Enabled)
	}
	if err := space.Admit(input); err != nil {
		return domain.Memory{}, err
	}
	if !space.Enabled {
		return m.Repository.Remember(ctx, input)
	}
	existing, err := m.Repository.ListScope(ctx, input.Scope)
	if err != nil {
		return domain.Memory{}, fmt.Errorf("讀取既有記憶: %w", err)
	}
	// 模型明確指定 supersedes 時不做去重：它已經說了「這筆取代那筆」，
	// 用相似度把它併回舊的那筆，等於把矛盾更新吃掉。
	explicitSupersede := len(input.Supersedes) > 0
	// 同一件事再說一次不該變成兩則：更新原本那筆的信心值即可。
	if duplicate, ok := findDuplicate(existing, input.Content); ok && !explicitSupersede {
		return m.Repository.Remember(ctx, domain.RememberMemoryInput{
			Scope:           input.Scope,
			Kind:            duplicate.Kind,
			Content:         duplicate.Content,
			Tags:            mergeTags(duplicate.Tags, input.Tags),
			Confidence:      strengthenedConfidence(duplicate.Confidence, input.Confidence),
			SourceSessionID: input.SourceSessionID,
			Supersedes:      []string{duplicate.ID},
			Metadata:        map[string]any{"source": "agent_tool", "reinforced": true},
		})
	}
	value, err := m.Repository.Remember(ctx, input)
	if err != nil {
		return domain.Memory{}, err
	}
	// 淘汰在寫入之後做：先確保這次的內容一定留得住，再處理總量。
	if err := m.evictOverCap(ctx, input.Scope, space); err != nil {
		return value, nil
	}
	return value, nil
}

// Search 讓工具與 Recall 共用同一套 scope 解析。
func (m *Manager) Search(ctx context.Context, session domain.Session, query domain.MemoryQuery) ([]domain.Memory, error) {
	if m == nil || m.Repository == nil {
		return nil, fmt.Errorf("memory repository is unavailable")
	}
	if strings.TrimSpace(query.Scope) == "" {
		query.Scope = ScopeForSessionWithSpace(session, m.SpaceEnabled())
	}
	return m.Repository.Search(ctx, query)
}

// Forget 同樣走統一的 scope 解析。
func (m *Manager) Forget(ctx context.Context, session domain.Session, id, reason string) (domain.Memory, error) {
	if m == nil || m.Repository == nil {
		return domain.Memory{}, fmt.Errorf("memory repository is unavailable")
	}
	return m.Repository.Forget(ctx, ScopeForSessionWithSpace(session, m.SpaceEnabled()), id, reason)
}

// SpaceEnabled 讓呼叫端知道目前是否為回憶空間模式。
func (m *Manager) SpaceEnabled() bool {
	return m != nil && m.spaceEnabled.Load()
}

// evictOverCap 在超過上限時淘汰價值最低的記憶。
//
// 淘汰是轉成 forgotten，不是刪資料——與 transcript 撤回同一個原則：
// 出問題時要能查回「當時到底記了什麼」。
func (m *Manager) evictOverCap(ctx context.Context, scope string, space SpaceConfig) error {
	values, err := m.Repository.ListScope(ctx, scope)
	if err != nil {
		return err
	}
	if len(values) <= space.ScopeCap {
		return nil
	}
	now := time.Now().UTC()
	sort.SliceStable(values, func(first, second int) bool {
		return retentionValue(values[first], now, space) < retentionValue(values[second], now, space)
	})
	for index := 0; index < len(values)-space.ScopeCap; index++ {
		if _, err := m.Repository.Forget(ctx, scope, values[index].ID, "回憶空間已達上限，淘汰最久未被引用的記憶"); err != nil {
			return err
		}
	}
	return nil
}

// retentionValue 越低越先淘汰：久未召回又沒被驗證過的先走。
func retentionValue(value domain.Memory, now time.Time, space SpaceConfig) float64 {
	return memoryFreshness(value, now, space)*0.7 + memoryHitRate(value)*0.3
}

// findDuplicate 找出語意上等同的既有記憶。
//
// 判定用詞集合的重疊比例而不是字串相等：模型很少用一模一樣的句子說同一件事。
func findDuplicate(values []domain.Memory, content string) (domain.Memory, bool) {
	terms := queryTerms(content)
	if len(terms) == 0 {
		return domain.Memory{}, false
	}
	for _, value := range values {
		if value.Status != domain.MemoryStatusActive {
			continue
		}
		existing := queryTerms(value.Content)
		if len(existing) == 0 {
			continue
		}
		if !sameSubject(terms, existing) {
			continue
		}
		shared := 0
		for term := range terms {
			if existing[term] {
				shared++
			}
		}
		larger := len(terms)
		if len(existing) > larger {
			larger = len(existing)
		}
		if float64(shared)/float64(larger) >= duplicateOverlapRatio {
			return value, true
		}
	}
	return domain.Memory{}, false
}

// duplicateOverlapRatio 是「這兩句在講同一件事」的判定門檻。
// 訂得高：把兩件不同的事合併成一則，比多留一則重複的傷害大得多——尤其當那兩件
// 事其實是前後矛盾的決策時，合併等於默默丟掉更新過的那一筆。
const duplicateOverlapRatio = 0.9

// sameSubject 擋掉「只差一個關鍵字」的假重複。
//
// 「部署目標選用 staging 環境」與「⋯⋯production 環境」的詞重疊高達八成以上，
// 但它們是互相矛盾的兩個決策。這種差異幾乎都落在具體識別字上（環境名、旗標、
// 路徑），所以只要有一方帶了對方沒有的識別字，就不當成重複。
func sameSubject(left, right map[string]bool) bool {
	return !hasExclusiveIdentifier(left, right) && !hasExclusiveIdentifier(right, left)
}

func hasExclusiveIdentifier(terms, other map[string]bool) bool {
	for term := range terms {
		if other[term] || len([]rune(term)) < minimumIdentifierRunes {
			continue
		}
		if isASCIIIdentifier(term) {
			return true
		}
	}
	return false
}

// minimumIdentifierRunes 排除 the、and 這類沒有指涉力的短詞。
const minimumIdentifierRunes = 3

func isASCIIIdentifier(term string) bool {
	for _, value := range term {
		if value > 127 {
			return false
		}
	}
	return true
}

func mergeTags(existing, incoming []string) []string {
	seen := map[string]bool{}
	result := make([]string, 0, len(existing)+len(incoming))
	for _, group := range [][]string{existing, incoming} {
		for _, tag := range group {
			tag = strings.TrimSpace(tag)
			if tag == "" || seen[tag] {
				continue
			}
			seen[tag] = true
			result = append(result, tag)
		}
	}
	sort.Strings(result)
	return result
}

// strengthenedConfidence 讓重複確認的記憶更可信，但不會直接跳到 1。
func strengthenedConfidence(existing, incoming float64) float64 {
	value := existing
	if incoming > value {
		value = incoming
	}
	value += (1 - value) * 0.25
	if value > 0.99 {
		value = 0.99
	}
	return value
}
