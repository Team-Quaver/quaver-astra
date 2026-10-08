package player

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// Player 是播放器状态机：队列、模式、随机、流解析回退、歌词、收藏。
// 所有方法并发安全；状态变化经 OnNotify 注册的回调通知（从任意 goroutine
// 触发，UI 负责切回主线程重绘）。
type Player struct {
	mu     sync.RWMutex
	notify func()

	api  Backend
	eng  Engine
	conf Preferences

	queue     []Song
	index     int
	mode      string // off | all | one
	shuf      bool
	shufOrder []int
	shufDay   string

	playing  bool // 传输层在播（非暂停）
	loading  bool
	err      string
	pos      float64
	dur      float64
	stream   *StreamInfo
	sessionQ string // 会话级音质覆盖
	tiers    *TierTable

	loved      map[string]Song // mid → 歌
	likedCache []Song
	likedTotal int64
	likedSeq   uint64

	lyrics     []LyricLine
	qrc        []QrcLine // 逐字时间轴，与 lyrics 同源同序；无逐字歌词时为空
	lyricState string    // idle|loading|ok|none
	showTrans  bool

	loggedIn bool
	me       UserInfo

	playlists  []PlaylistRef
	playSeq    uint64
	failStreak int
	vol        float64
	muted      bool
}

// Song / StreamInfo / TierTable / UserInfo 是 player 与外界解耦的最小形状，
// 由 appui 组装时从 backend / 引擎适配。
type Song struct {
	Mid, Name, Title, Subtitle string
	Artists                    string
	Singers                    []Singer // 逐个歌手（跳歌手页用，Artists 是它们的拼接）
	Album                      string
	AlbumPmid, AlbumMid        string
	Interval                   float64
	SongID                     int64
	SongType                   int64 // 读侧枚举
}

// Singer 歌手引用（跳歌手页需要 mid，不能只有拼好的名字）。
type Singer struct {
	Mid, Name string
}

func (s Song) DisplayName() string {
	if s.Title != "" {
		return s.Title
	}
	return s.Name
}

type StreamInfo struct {
	URL       string
	Tier      string
	TierLabel string
	Degraded  bool
	Size      int64
}

type Tier struct {
	ID, Label string
	Rank      int
	Locked    bool
}

type TierTable struct {
	Tiers []Tier
	Max   string
}

type UserInfo struct {
	Name, Avatar string
	VipLabel     string
}

// Backend 是 player 需要的后端能力（实际由 *backend.Client 适配）。
type Backend interface {
	LoginStatus() (loggedIn bool, err error)
	UserInfo() (UserInfo, error)
	LikedPage(page, num int) (songs []Song, total int64, hasmore bool, err error)
	Playlists() (pls []PlaylistRef, err error)
	Tiers() (*TierTable, error)
	Resolve(mid, mediaMid string, songType int64, tier string, deprioritize []string) (*StreamInfo, error)
	FetchLyric(mid string, trans bool) (lrc, translation string, err error)
	LikeSong(songID int64, writeType int64, like bool) error
}

// PlaylistRef 侧栏歌单项。
type PlaylistRef struct {
	ID    int64
	Title string
}

// Engine 是 player 需要的音频能力（由 *audio.Engine 基于 mpv 实现）。
// 位置/时长由引擎直接从 mpv 取，player 不再自己推算。
type Engine interface {
	// OpenURL 异步载入并起播；载入结果由 LoadError 回报。
	OpenURL(url string, startFrac, dur float64, autoplay bool) error
	Play()
	Pause()
	Stop()
	Close()
	IsPlaying() bool
	Ended() bool
	Paused() bool
	Position() float64
	Duration(fallback float64) float64
	SeekTo(frac, dur float64)
	SetVolume(v float64)
	LoadError() string
	OnObserve(fn func())
}

// Preferences 是持久化配置读写接口。
type Preferences interface {
	String(key, def string) string
	Float(key string, def float64) float64
	Bool(key string, def bool) bool
	Set(key string, val any)
}

// New 构造播放器。
func New(b Backend, e Engine, prefs Preferences) *Player {
	p := &Player{
		api:        b,
		eng:        e,
		conf:       prefs,
		mode:       "all",
		index:      -1,
		lyricState: "idle",
		loved:      map[string]Song{},
		showTrans:  prefs.Bool("Style.ShowTranslation", true),
	}
	return p
}

// OnNotify 注册状态变化回调。
func (p *Player) OnNotify(fn func()) { p.notify = fn }

func (p *Player) notifyChange() {
	if p.notify != nil {
		p.notify()
	}
}

// ===== 登录态 =====

// RefreshUser 刷新登录状态；已登录则顺带拉用户信息与我喜欢。
func (p *Player) RefreshUser() {
	go func() {
		loggedIn, err := p.api.LoginStatus()
		if err != nil {
			return
		}
		p.mu.Lock()
		was := p.loggedIn
		p.loggedIn = loggedIn
		p.mu.Unlock()
		if was != loggedIn {
			p.notifyChange()
		}
		if loggedIn {
			p.loadUserInfo()
			p.LoadLoved()
			p.loadPlaylists()
		}
	}()
}

func (p *Player) loadUserInfo() {
	me, err := p.api.UserInfo()
	p.mu.Lock()
	if err == nil {
		p.me = me
	}
	p.mu.Unlock()
	p.notifyChange()
}

func (p *Player) loadPlaylists() {
	pls, err := p.api.Playlists()
	p.mu.Lock()
	if err == nil {
		p.playlists = pls
	}
	p.mu.Unlock()
	p.notifyChange()
}

func (p *Player) Playlists() []PlaylistRef {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.playlists
}

// LoadLoved 分页拉取我喜欢（最多 2 页 × 500）。
func (p *Player) LoadLoved() {
	p.mu.Lock()
	p.likedSeq++
	seq := p.likedSeq
	p.mu.Unlock()
	go func() {
		all := map[string]Song{}
		var first []Song
		total := int64(0)
		for page := 1; page <= 2; page++ {
			songs, tot, hasmore, err := p.api.LikedPage(page, 500)
			if err != nil {
				return
			}
			total = tot
			if page == 1 {
				first = songs
			}
			for _, s := range songs {
				all[s.Mid] = s
			}
			if !hasmore {
				break
			}
		}
		p.mu.Lock()
		if seq != p.likedSeq {
			p.mu.Unlock()
			return
		}
		p.loved = all
		p.likedCache = first
		p.likedTotal = total
		p.mu.Unlock()
		p.notifyChange()
	}()
}

func (p *Player) LoggedIn() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.loggedIn
}

func (p *Player) Me() UserInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.me
}

// ===== 队列 =====

func (p *Player) Queue() []Song {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return append([]Song(nil), p.queue...)
}

func (p *Player) Index() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.index
}

func (p *Player) Current() (Song, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.currentLocked()
}

func (p *Player) currentLocked() (Song, bool) {
	if p.index < 0 || p.index >= len(p.queue) {
		return Song{}, false
	}
	return p.queue[p.index], true
}

// PlayList 用给定列表替换队列并从 i 开始播放。
func (p *Player) PlayList(songs []Song, i int) {
	p.mu.Lock()
	if len(songs) == 0 {
		p.mu.Unlock()
		return
	}
	p.queue = append([]Song(nil), songs...)
	if i < 0 || i >= len(p.queue) {
		i = 0
	}
	p.index = i
	p.shufOrder = nil
	p.mu.Unlock()
	p.notifyChange()
	p.startCurrent(0, true, "")
}

// Jump 跳到队列第 i 首。
func (p *Player) Jump(i int) {
	p.mu.Lock()
	if i < 0 || i >= len(p.queue) {
		p.mu.Unlock()
		return
	}
	p.index = i
	p.mu.Unlock()
	p.notifyChange()
	p.startCurrent(0, true, "")
}

// EnqueueNext 「下一首播放」：排到当前曲之后等着播，不切歌、不打断当前曲。
//
// 队列还空着（没播过任何东西）时没有「下一首」可言，退化成单曲起播
//（与主项目 player.enqueueNext 同一语义分叉）。
func (p *Player) EnqueueNext(s Song) {
	p.mu.Lock()
	if p.index < 0 || len(p.queue) == 0 {
		p.mu.Unlock()
		p.PlayList([]Song{s}, 0)
		return
	}
	at := p.index + 1
	p.queue = append(p.queue[:at], append([]Song{s}, p.queue[at:]...)...)
	p.mu.Unlock()
	p.notifyChange()
}

// PlayNextNow 插队并立即播放。
func (p *Player) PlayNextNow(s Song) {
	p.mu.Lock()
	at := p.index + 1
	if p.index < 0 {
		at = len(p.queue)
	}
	p.queue = append(p.queue[:at], append([]Song{s}, p.queue[at:]...)...)
	p.index = at
	p.mu.Unlock()
	p.notifyChange()
	p.startCurrent(0, true, "")
}

// EnqueueMany 追加到队列尾部。
func (p *Player) EnqueueMany(songs []Song) {
	p.mu.Lock()
	p.queue = append(p.queue, songs...)
	p.mu.Unlock()
	p.notifyChange()
}

// RemoveAt 删除队列第 i 首（删当前曲则原地停/换歌）。
func (p *Player) RemoveAt(i int) {
	p.mu.Lock()
	if i < 0 || i >= len(p.queue) {
		p.mu.Unlock()
		return
	}
	wasCurrent := i == p.index
	p.queue = append(p.queue[:i], p.queue[i+1:]...)
	if wasCurrent {
		if p.index >= len(p.queue) {
			p.index = len(p.queue) - 1
		}
		if p.index >= 0 {
			p.mu.Unlock()
			p.notifyChange()
			p.startCurrent(0, false, "")
			return
		}
		p.stopLocked()
		p.mu.Unlock()
		p.notifyChange()
		return
	}
	if i < p.index {
		p.index--
	}
	p.mu.Unlock()
	p.notifyChange()
}

// ClearQueue 清空队列。
func (p *Player) ClearQueue() {
	p.mu.Lock()
	p.queue = nil
	p.index = -1
	p.stopLocked()
	p.mu.Unlock()
	p.notifyChange()
}

// MoveInQueue 拖拽排序（rows 的歌移到 to 之前；当前曲跟随）。
func (p *Player) MoveInQueue(rows []int, to int) {
	p.mu.Lock()
	if len(rows) == 0 || to < 0 || to > len(p.queue) {
		p.mu.Unlock()
		return
	}
	cur := ""
	if p.index >= 0 && p.index < len(p.queue) {
		cur = p.queue[p.index].Mid
	}
	moved := make([]Song, 0, len(rows))
	rm := map[int]bool{}
	for _, r := range rows {
		if r >= 0 && r < len(p.queue) {
			moved = append(moved, p.queue[r])
			rm[r] = true
		}
	}
	out := make([]Song, 0, len(p.queue))
	for i, s := range p.queue {
		if !rm[i] {
			out = append(out, s)
		}
	}
	adjusted := to
	for r := range rm {
		if r < to {
			adjusted--
		}
	}
	if adjusted < 0 {
		adjusted = 0
	}
	if adjusted > len(out) {
		adjusted = len(out)
	}
	out = append(out[:adjusted], append(append([]Song(nil), moved...), out[adjusted:]...)...)
	p.queue = out
	if cur != "" {
		for i, s := range p.queue {
			if s.Mid == cur {
				p.index = i
				break
			}
		}
	}
	p.shufOrder = nil
	p.mu.Unlock()
	p.notifyChange()
}

// ===== 模式 =====

func (p *Player) Mode() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mode
}

func (p *Player) CycleMode() {
	p.mu.Lock()
	switch p.mode {
	case "off":
		p.mode = "all"
	case "all":
		p.mode = "one"
	default:
		p.mode = "off"
	}
	p.mu.Unlock()
	p.notifyChange()
}

func (p *Player) Shuffle() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.shuf
}

func (p *Player) SetShuffle(on bool) {
	p.mu.Lock()
	p.shuf = on
	p.shufOrder = nil
	p.mu.Unlock()
	p.notifyChange()
}

// nextIndexLocked 计算 auto 播完后的下一首（-1 = 停）。手动 next 总回绕；
// 顺序模式自然播到队尾即停。
func (p *Player) nextIndexLocked(auto bool) int {
	n := len(p.queue)
	if n == 0 {
		return -1
	}
	if auto && p.mode == "one" {
		return p.index
	}
	if p.shuf {
		return p.shufNextLocked()
	}
	next := p.index + 1
	if next >= n {
		if auto && p.mode == "off" {
			return -1
		}
		next = 0
	}
	return next
}

func (p *Player) prevIndexLocked() int {
	n := len(p.queue)
	if n == 0 {
		return -1
	}
	if p.shuf {
		return p.shufPrevLocked()
	}
	return (p.index - 1 + n) % n
}

// shufNextLocked 每日一套的确定性随机顺序（FNV-1a 种子 + mulberry32 + Fisher-Yates）。
func (p *Player) shufNextLocked() int {
	n := len(p.queue)
	if n == 0 {
		return -1
	}
	p.ensureShufLocked()
	if len(p.shufOrder) == 0 {
		return -1
	}
	at := -1
	for i, v := range p.shufOrder {
		if v == p.index {
			at = i
			break
		}
	}
	if at < 0 {
		return p.shufOrder[0]
	}
	if at+1 >= len(p.shufOrder) {
		if p.mode == "off" {
			return -1
		}
		return p.shufOrder[0]
	}
	return p.shufOrder[at+1]
}

func (p *Player) shufPrevLocked() int {
	if len(p.queue) == 0 {
		return -1
	}
	p.ensureShufLocked()
	if len(p.shufOrder) == 0 {
		return -1
	}
	at := -1
	for i, v := range p.shufOrder {
		if v == p.index {
			at = i
			break
		}
	}
	if at <= 0 {
		return p.shufOrder[len(p.shufOrder)-1]
	}
	return p.shufOrder[at-1]
}

func (p *Player) ensureShufLocked() {
	day := time.Now().Format("2006-01-02")
	if p.shufOrder != nil && p.shufDay == day && len(p.shufOrder) == len(p.queue) {
		return
	}
	n := len(p.queue)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	rnd := mulberry32(fnv1a(day))
	for i := n - 1; i > 0; i-- {
		j := int(rnd() * float64(i+1))
		if j > i {
			j = i
		}
		order[i], order[j] = order[j], order[i]
	}
	p.shufOrder, p.shufDay = order, day
}

func fnv1a(s string) uint32 {
	h := uint32(2166136261)
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func mulberry32(seed uint32) func() float64 {
	a := seed
	return func() float64 {
		a += 0x6D2B79F5
		t := a
		t = (t ^ (t >> 15)) * (t | 1)
		t ^= t + (t^(t>>7))*(t|61)
		return float64((t^(t>>14))&0xFFFFFFFF) / 4294967296.0
	}
}

// ===== 播放控制 =====

func (p *Player) Next(auto bool) {
	p.mu.Lock()
	next := p.nextIndexLocked(auto)
	if next < 0 {
		p.stopLocked()
		p.mu.Unlock()
		p.notifyChange()
		return
	}
	p.index = next
	p.mu.Unlock()
	p.notifyChange()
	p.startCurrent(0, true, "")
}

func (p *Player) Prev() {
	p.mu.RLock()
	replay := p.conf.String("Playing.PrevReplay", "replay") == "replay" && p.pos > 3
	p.mu.RUnlock()
	if replay {
		p.SeekTo(0)
		return
	}
	p.mu.Lock()
	prev := p.prevIndexLocked()
	if prev < 0 {
		p.mu.Unlock()
		return
	}
	p.index = prev
	p.mu.Unlock()
	p.notifyChange()
	p.startCurrent(0, true, "")
}

// PlayOrPause 播放/暂停切换；没有当前曲则播第一首。
func (p *Player) PlayOrPause() {
	p.mu.Lock()
	if p.index < 0 || p.index >= len(p.queue) {
		if len(p.queue) > 0 {
			p.index = 0
			p.mu.Unlock()
			p.notifyChange()
			p.startCurrent(0, true, "")
			return
		}
		p.mu.Unlock()
		return
	}
	if p.eng.Paused() || !p.playing {
		p.eng.Play()
		p.playing = true
	} else {
		p.eng.Pause()
		p.playing = false
	}
	p.mu.Unlock()
	p.notifyChange()
}

func (p *Player) Playing() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.playing && !p.eng.Paused()
}

func (p *Player) Loading() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.loading
}

func (p *Player) Error() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.err
}

func (p *Player) Position() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pos
}

func (p *Player) Duration() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.dur
}

func (p *Player) Stream() *StreamInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.stream
}

// SeekTo 按 0..1 比例跳转。位置由引擎回报，这里不再自行重锚定。
func (p *Player) SeekTo(frac float64) {
	p.mu.RLock()
	dur := p.dur
	p.mu.RUnlock()
	p.eng.SeekTo(frac, dur)
	p.notifyChange()
}

// ===== 音质 =====

// Quality 当前生效音质：会话覆盖优先于默认配置。
func (p *Player) Quality() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.sessionQ != "" {
		return p.sessionQ
	}
	return p.conf.String("Quality.DefaultQuality", "auto")
}

func (p *Player) SwitchQuality(tier string) {
	p.mu.Lock()
	p.sessionQ = tier
	p.mu.Unlock()
	p.notifyChange()
	p.RestartAtPosition()
}

// RestartAtPosition 以当前进度重启当前曲（换音质用）。
func (p *Player) RestartAtPosition() {
	p.mu.RLock()
	pos := p.pos
	p.mu.RUnlock()
	p.startCurrent(pos, true, "")
}

func (p *Player) TierTable() *TierTable {
	p.mu.RLock()
	t := p.tiers
	p.mu.RUnlock()
	if t == nil {
		go p.loadTiers()
	}
	return t
}

func (p *Player) loadTiers() {
	t, err := p.api.Tiers()
	if err != nil {
		return
	}
	p.mu.Lock()
	p.tiers = t
	p.mu.Unlock()
	p.notifyChange()
}

// pickTier 把 "auto" 换算成本机可播放的最高档位。
//
// 换了 mpv 之后支持集是全档位（含 atmos / DTS / FLAC / AAC），
// 因此这里直接取未锁定档位里 rank 最高的一档。
func pickTier(table *TierTable, want string) string {
	if want != "auto" {
		return want
	}
	if table == nil {
		return "320"
	}
	best, bestRank := "128", -1
	for _, t := range table.Tiers {
		if t.Locked {
			continue
		}
		if t.Rank > bestRank {
			best, bestRank = t.ID, t.Rank
		}
	}
	return best
}

// ===== 内部：起播 =====

func (p *Player) stopLocked() {
	p.eng.Stop()
	p.playing = false
	p.loading = false
	p.stream = nil
	p.pos, p.dur = 0, 0
}

// startCurrent 解析并载入当前曲。调用方不得持有 p.mu。
func (p *Player) startCurrent(resumeTo float64, autoplay bool, overrideTier string) {
	p.mu.Lock()
	p.playSeq++
	seq := p.playSeq
	song, ok := p.currentLocked()
	if !ok {
		p.loading = false
		p.mu.Unlock()
		p.notifyChange()
		return
	}
	p.loading = true
	p.err = ""
	p.pos = resumeTo
	p.dur = song.Interval
	if p.dur <= 0 {
		p.dur = 1
	}
	want := overrideTier
	if want == "" {
		want = p.sessionQ
	}
	if want == "" {
		want = p.conf.String("Quality.DefaultQuality", "auto")
	}
	if want == "auto" {
		want = pickTier(p.tiers, "auto")
	}
	dep := []string{}
	if p.conf.Bool("Quality.FallbackToQMAtmos", false) {
		dep = []string{"atmos51", "atmos71"}
	}
	p.mu.Unlock()
	p.notifyChange()

	// 会话局部回退集合：只被本次起播的协程链访问，避免跨会话共享竞争
	fallback := map[string]bool{}
	go func() {
		// 解析 → 交给 mpv 载入 → 等载入结果。任何一环失败（档位不被支持、
		// 网络错误）都继续走回退链换下一档。
		var info *StreamInfo
		for {
			i, tier, err := p.resolveWithFallback(song, want, dep, seq, fallback)
			if err != nil {
				p.failWithErr(seq, err)
				return
			}
			p.mu.Lock()
			stale := seq != p.playSeq
			dur, resume := p.dur, resumeTo
			p.mu.Unlock()
			if stale {
				return
			}
			if lerr := p.eng.OpenURL(i.URL, resume/dur, dur, autoplay); lerr != nil {
				continue // 引擎拒绝（mpv 未运行等），下一轮换档
			}
			// mpv 载入是异步的：网络流可能慢，要等它给出结果才能判断这档
			// 能不能播。等不到（超时）或报错都视为该档失败，继续降档。
			if lerr := p.awaitLoaded(seq); lerr != nil {
				_ = lerr
				continue
			}
			info = i
			_ = tier
			break
		}
		p.mu.Lock()
		if seq != p.playSeq {
			p.mu.Unlock()
			return
		}
		if !autoplay {
			p.eng.Pause()
			p.playing = false
		} else {
			p.eng.Play()
			p.playing = true
		}
		p.loading = false
		p.stream = info
		p.failStreak = 0
		p.mu.Unlock()
		p.notifyChange()
		p.fetchLyric(song, seq)
	}()
}

// awaitLoaded 等 mpv 报出本次载入的结果。
//
// mpv 的 loadfile 是异步的：命令立刻返回，真正的结果（打开成功/失败、
// 时长就位）随后以属性事件的形式到来。这里轮询到「拿到时长」或「报错」
// 为止；超时就按失败处理，让调用方降档——总比卡住整条起播链好。
func (p *Player) awaitLoaded(seq uint64) error {
	deadline := time.Now().Add(loadWaitTimeout)
	for time.Now().Before(deadline) {
		p.mu.RLock()
		stale := seq != p.playSeq
		p.mu.RUnlock()
		if stale {
			return errStale
		}
		if e := p.eng.LoadError(); e != "" {
			return errors.New(e)
		}
		// 时长就位即视为打开成功（>0 且不是我们塞进去的兜底值）
		if p.eng.Duration(0) > 0 {
			return nil
		}
		time.Sleep(40 * time.Millisecond)
	}
	return errLoadTimeout
}

// loadWaitTimeout 是单档位的载入等待上限。首帧通常几百毫秒，网络差时
// 给到几秒；再久就不如换下一档重试。
const loadWaitTimeout = 8 * time.Second

var errLoadTimeout = errors.New("载入超时")

// resolveWithFallback 按回退链尝试解析（档位受限支持集）。fallback 由调用方持有（会话局部）。
func (p *Player) resolveWithFallback(song Song, want string, dep []string, seq uint64, fallback map[string]bool) (*StreamInfo, string, error) {
	var lastErr error
	for _, tier := range fallbackChain(want) {
		if fallback[tier] {
			continue
		}
		fallback[tier] = true
		p.mu.RLock()
		stale := seq != p.playSeq
		p.mu.RUnlock()
		if stale {
			return nil, "", errStale
		}
		info, err := p.api.Resolve(song.Mid, "", song.SongType, tier, dep)
		if err == nil && info != nil {
			return info, tier, nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errStale
	}
	return nil, "", lastErr
}

// fallbackChain 从高档到低档的回退链（mpv 可解全部档位，这里按上游
// 的 rank 顺序逐级让位；换档由起播协程的 fallback 集合去重）。
func fallbackChain(want string) []string {
	order := []string{
		"master", "atmos71", "atmos51", "atmos2", "flac",
		"640ogg", "320ogg", "320", "128",
	}
	at := -1
	for i, id := range order {
		if id == want {
			at = i
			break
		}
	}
	if at < 0 {
		return []string{want}
	}
	return order[at:]
}

var errStale = errors.New("已切换歌曲")

func (p *Player) failWithErr(seq uint64, err error) {
	p.mu.Lock()
	if seq != p.playSeq {
		p.mu.Unlock()
		return
	}
	p.loading = false
	p.err = err.Error()
	p.failStreak++
	next := -1
	if p.failStreak <= len(p.queue) && len(p.queue) > 1 {
		next = p.nextIndexLocked(true)
	}
	p.mu.Unlock()
	p.notifyChange()
	if next >= 0 {
		p.mu.Lock()
		p.index = next
		p.mu.Unlock()
		p.startCurrent(0, true, "")
	}
}

// ===== 歌词 =====

func (p *Player) fetchLyric(song Song, seq uint64) {
	p.mu.Lock()
	p.lyricState = "loading"
	p.mu.Unlock()
	p.notifyChange()
	src, trans, err := p.api.FetchLyric(song.Mid, p.ShowTranslation())
	p.mu.Lock()
	if seq != p.playSeq {
		p.mu.Unlock()
		return
	}
	if err != nil || strings.TrimSpace(src) == "" {
		p.lyrics, p.qrc = nil, nil
		p.lyricState = "none"
		p.mu.Unlock()
		p.notifyChange()
		return
	}
	// 先按逐字（QRC）解析：后端请求时带了 qrc=1，有逐字时间轴时 src 就是
	// QRC 明文（XML 信封或纯文本）。解析不出词级时间轴就按行级 LRC 走。
	//
	// 逐字歌词仍以「行级视图」喂给列表（滚动/索引/点击跳转那一套不用改），
	// 词级时间轴平行存在 p.qrc 里，按行号回查。
	if ql := ParseQRC(src); HasWordTiming(ql) {
		alignQrcTrans(ql, lrcLines(trans))
		p.qrc = ql
		p.lyrics = qrcToLines(ql)
	} else {
		p.qrc = nil
		p.lyrics = ParseLrc(src, trans)
	}
	if len(p.lyrics) == 0 {
		p.lyrics, p.qrc = nil, nil
		p.lyricState = "none"
	} else {
		p.lyricState = "ok"
	}
	p.mu.Unlock()
	p.notifyChange()
}

// QrcLines 返回逐字时间轴（与 LyricLines 同源同序）。长度为 0 表示这首
// 歌没有词级时间轴，调用方走行级渲染。
func (p *Player) QrcLines() []QrcLine {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.qrc
}

// QrcLineAt 返回第 i 行的逐字时间轴。
func (p *Player) QrcLineAt(i int) (QrcLine, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if i < 0 || i >= len(p.qrc) {
		return QrcLine{}, false
	}
	return p.qrc[i], true
}

func (p *Player) LyricLines() ([]LyricLine, string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lyrics, p.lyricState
}

func (p *Player) LyricIndex(t float64) int {
	p.mu.RLock()
	lines := p.lyrics
	p.mu.RUnlock()
	return LyricIndexAt(lines, t)
}

// SetShowTranslation 切换翻译显示（重新拉当前歌词）。
func (p *Player) SetShowTranslation(on bool) {
	p.mu.Lock()
	p.showTrans = on
	p.mu.Unlock()
	p.conf.Set("Style.ShowTranslation", on)
	if cur, ok := p.Current(); ok {
		p.mu.RLock()
		seq := p.playSeq
		p.mu.RUnlock()
		go p.fetchLyric(cur, seq)
	}
	p.notifyChange()
}

func (p *Player) ShowTranslation() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.showTrans
}

// ===== 收藏 =====

func (p *Player) IsLoved(mid string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	_, ok := p.loved[mid]
	return ok
}

func (p *Player) LikedCache() []Song {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.likedCache
}

func (p *Player) LikedTotal() int64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.likedTotal
}

// ToggleLove 乐观切换收藏；失败回滚。
func (p *Player) ToggleLove(song Song) {
	p.mu.Lock()
	_, was := p.loved[song.Mid]
	if was {
		delete(p.loved, song.Mid)
	} else {
		p.loved[song.Mid] = song
	}
	p.mu.Unlock()
	p.notifyChange()
	writeType := song.SongType - 1
	if writeType < 0 {
		writeType = 0
	}
	go func() {
		if err := p.api.LikeSong(song.SongID, writeType, !was); err != nil {
			p.mu.Lock()
			if was {
				p.loved[song.Mid] = song
			} else {
				delete(p.loved, song.Mid)
			}
			p.mu.Unlock()
			p.notifyChange()
			return
		}
		p.LoadLoved()
	}()
}

// ===== 心跳 =====

// StartTicker 启动播放状态监听（200ms）。
//
// 位置直接读 mpv 的 time-pos，不再用墙钟推算——旧实现必须自己累加
// 位置并处理 seek/换歌时的重锚定，误差会累积；mpv 是唯一真相源。
// 本循环只负责：同步位置/时长、检测自然播完切下一首、驱动 UI 重绘。
func (p *Player) StartTicker() {
	go func() {
		t := time.NewTicker(200 * time.Millisecond)
		defer t.Stop()
		for range t.C {
			p.tick()
		}
	}()
}

// tick 同步播放位置并处理自然播完。
func (p *Player) tick() {
	p.mu.Lock()
	if p.index < 0 || p.index >= len(p.queue) || !p.playing {
		p.mu.Unlock()
		return
	}
	paused := p.eng.Paused()
	fallback := p.dur
	p.mu.Unlock()

	if paused {
		p.notifyChange()
		return
	}

	// 从引擎同步真实位置与时长（mpv 的 time-pos / duration）
	pos := p.eng.Position()
	dur := p.eng.Duration(fallback)

	p.mu.Lock()
	p.pos = pos
	if dur > 0 {
		p.dur = dur
	}
	ended := p.eng.Ended()
	atEnd := p.dur > 0 && p.pos >= p.dur-0.05
	p.mu.Unlock()

	if ended || atEnd {
		p.Next(true)
		return
	}
	p.notifyChange()
}

// ===== 音量 =====

func (p *Player) Volume() float64 {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.vol
}

func (p *Player) SetVolume(v float64) {
	p.mu.Lock()
	if v < 0 {
		v = 0
	}
	if v > 1 {
		v = 1
	}
	p.vol = v
	if !p.muted {
		p.eng.SetVolume(v)
	}
	p.mu.Unlock()
	p.conf.Set("Playing.Volume", v)
	p.notifyChange()
}

func (p *Player) ToggleMute() {
	p.mu.Lock()
	p.muted = !p.muted
	if p.muted {
		p.eng.SetVolume(0)
	} else {
		p.eng.SetVolume(p.vol)
	}
	p.mu.Unlock()
	p.conf.Set("Playing.Muted", p.muted)
	p.notifyChange()
}

func (p *Player) Muted() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.muted
}

// Boot 载入持久化音量、挂上引擎观察回调并启动心跳。
func (p *Player) Boot() {
	p.mu.Lock()
	p.vol = p.conf.Float("Playing.Volume", 0.8)
	p.muted = p.conf.Bool("Playing.Muted", false)
	p.eng.SetVolume(0)
	if !p.muted {
		p.eng.SetVolume(p.vol)
	}
	p.mu.Unlock()
	// mpv 属性变化即刻通知，让进度条/歌词不等到下一个心跳才动
	p.eng.OnObserve(p.notifyChange)
	p.StartTicker()
}
