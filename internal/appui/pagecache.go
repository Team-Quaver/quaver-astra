package appui

// pageCacheLimit 是往返页面状态缓存的上限。页面状态包含分页后的完整歌曲列表，
// 不能像 map 那样随浏览历史无限增长；超出上限时淘汰最久未访问的页面。
const pageCacheLimit = 8

// getOrCreatePage 取缓存页面，不存在时创建；命中和创建都会把 key 移到队尾。
// 调用方固定在 UI 主线程，因此这里不需要额外加锁。
func getOrCreatePage[K comparable, V any](m map[K]V, order *[]K, key K, create func() V) V {
	if v, ok := m[key]; ok {
		touchPageKey(order, key)
		return v
	}
	v := create()
	m[key] = v
	*order = append(*order, key)
	for len(*order) > pageCacheLimit {
		oldest := (*order)[0]
		last := len(*order) - 1
		copy(*order, (*order)[1:])
		var zero K
		(*order)[last] = zero
		*order = (*order)[:last]
		delete(m, oldest)
	}
	return v
}

// touchPageKey 把 key 移到最近使用队尾；不存在的 key 不做处理。
func touchPageKey[K comparable](order *[]K, key K) {
	for i, k := range *order {
		if k != key {
			continue
		}
		copy((*order)[i:], (*order)[i+1:])
		(*order)[len(*order)-1] = key
		return
	}
}
