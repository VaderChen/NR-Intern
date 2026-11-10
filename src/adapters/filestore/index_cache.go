package filestore

import "container/list"

// 索引可由原檔重建，只保留最近使用的檔案；原始 transcript／事件不受淘汰影響。
// 上限針對檔案數，不截斷單一檔案的索引，避免大型 Run 每次追加都被迫全檔重掃。
const maxCachedFileIndexes = 128

// indexCache 由 repository.mu 保護；呼叫端持有單一檔案鎖保護索引本身。
// 淘汰只移除快取參照，已取得的索引仍可完成當次讀取。
type indexCache[T any] struct {
	items map[string]*list.Element
	order list.List
}

type cachedFileIndex[T any] struct {
	key   string
	value T
}

func (c *indexCache[T]) get(key string) (T, bool) {
	if element := c.items[key]; element != nil {
		c.order.MoveToFront(element)
		return element.Value.(cachedFileIndex[T]).value, true
	}
	var zero T
	return zero, false
}

func (c *indexCache[T]) set(key string, value T) {
	if c.items == nil {
		c.items = make(map[string]*list.Element)
	}
	if element := c.items[key]; element != nil {
		element.Value = cachedFileIndex[T]{key, value}
		c.order.MoveToFront(element)
		return
	}
	c.items[key] = c.order.PushFront(cachedFileIndex[T]{key, value})
	if len(c.items) > maxCachedFileIndexes {
		c.delete(c.order.Back().Value.(cachedFileIndex[T]).key)
	}
}

func (c *indexCache[T]) delete(key string) {
	if element := c.items[key]; element != nil {
		c.order.Remove(element)
		delete(c.items, key)
	}
}
