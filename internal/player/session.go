package player

import (
	"encoding/json"
	"math"
	"time"
)

// ===== 播放会话持久化 =====
//
// 退出时保留播放列表与正在播放的进度：队列（含当前曲目下标、循环模式、随机
// 开关）与当前曲进度写进 quaver-astra.json 的 Session.* 两个键，Boot 时恢复。
//
// 取舍与出处：
//
//   - 落盘节奏：结构性变化和进度跳转合并在一个防抖窗口里（sessionDebounce），
//     连续多选入队 / 拖拽排序 / 快切歌只写一次盘；正常播放中由 tick 节流，
//     每 5 秒（sessionPosStride）最多一条。conf 的 Set 每次重写整个文件，
//     绝不能跟着 200ms 的心跳写。
//   - Close 强制同步落盘，且赶在 mpv 关闭之前——进程随时可能在 Close 返回后
//     结束，异步定时器靠不住；位置也要在引擎还活着时从引擎取。
//   - 恢复只「就位」：Boot 不碰 mpv、不发网络请求（窗口秒开、不自动连网的
//     原则不变）。进度直接摆上界面，用户按播放时由 PlayOrPause/Play 的
//     「未装载起播」分支带着这个位置走完整起播链。若在启动时就自动
//     startCurrent，断网冷启动会撞上 failover 连跳链，几秒内把整条队列跳完。
//   - Session.Pos 里的 mid 与队列快照的当前曲不符时丢弃进度、保留队列：
//     宁可从 0 播，不可播错位置。

const (
	sessionQueueKey = "Session.Queue"
	sessionPosKey   = "Session.Pos"

	// sessionDebounce 是保存触发的合并窗口。
	sessionDebounce = 1200 * time.Millisecond

	// sessionPosStride 是心跳驱动进度落盘的阈值（秒）：崩溃时最多丢 5 秒。
	sessionPosStride = 5.0

	// sessionPosRound 把进度量化到半秒：落盘值稳定可比较，去重判断不会被
	// 浮点尾数反复打穿（0.5 在二进制浮点里是精确值）。
	sessionPosRound = 2.0
)

// sessionSinger / sessionSong 是 Song 的落盘形状：逐字段对齐，空值省略，
// 让「mid + 曲名 + 歌手」这些主干尽量小（一千首歌也就一两百 KB）。
// 读侧容忍未知字段与缺失字段（零值），因此 Song 加字段不需要版本号迁移；
// 删字段才需要考虑兼容，届时再谈。
type sessionSinger struct {
	Mid  string `json:"mid,omitempty"`
	Name string `json:"name,omitempty"`
}

type sessionSong struct {
	Mid       string          `json:"mid,omitempty"`
	Name      string          `json:"name,omitempty"`
	Title     string          `json:"title,omitempty"`
	Subtitle  string          `json:"subtitle,omitempty"`
	Artists   string          `json:"artists,omitempty"`
	Singers   []sessionSinger `json:"singers,omitempty"`
	Album     string          `json:"album,omitempty"`
	AlbumPmid string          `json:"albumPmid,omitempty"`
	AlbumMid  string          `json:"albumMid,omitempty"`
	Interval  float64         `json:"interval,omitempty"`
	SongID    int64           `json:"songId,omitempty"`
	SongType  int64           `json:"songType,omitempty"`
}

type sessionQueue struct {
	Songs   []sessionSong `json:"songs"`
	Index   int           `json:"index"`
	Mode    string        `json:"mode,omitempty"`
	Shuffle bool          `json:"shuffle,omitempty"`
}

type sessionPos struct {
	Mid string  `json:"mid"`
	Pos float64 `json:"pos"`
}

func songToSession(s Song) sessionSong {
	out := sessionSong{
		Mid: s.Mid, Name: s.Name, Title: s.Title, Subtitle: s.Subtitle,
		Artists: s.Artists, Album: s.Album, AlbumPmid: s.AlbumPmid,
		AlbumMid: s.AlbumMid, Interval: s.Interval, SongID: s.SongID, SongType: s.SongType,
	}
	if len(s.Singers) > 0 {
		out.Singers = make([]sessionSinger, 0, len(s.Singers))
		for _, sg := range s.Singers {
			out.Singers = append(out.Singers, sessionSinger{Mid: sg.Mid, Name: sg.Name})
		}
	}
	return out
}

func sessionToSong(ss sessionSong) Song {
	out := Song{
		Mid: ss.Mid, Name: ss.Name, Title: ss.Title, Subtitle: ss.Subtitle,
		Artists: ss.Artists, Album: ss.Album, AlbumPmid: ss.AlbumPmid,
		AlbumMid: ss.AlbumMid, Interval: ss.Interval, SongID: ss.SongID, SongType: ss.SongType,
	}
	if len(ss.Singers) > 0 {
		out.Singers = make([]Singer, 0, len(ss.Singers))
		for _, sg := range ss.Singers {
			out.Singers = append(out.Singers, Singer{Mid: sg.Mid, Name: sg.Name})
		}
	}
	return out
}

// sessionRoundPos 把进度量化到半秒（见 sessionPosRound 注释）。
func sessionRoundPos(pos float64) float64 {
	if pos < 0 {
		pos = 0
	}
	return math.Round(pos*sessionPosRound) / sessionPosRound
}

// sessionSaveSoon 安排一次防抖落盘。force 标记「进度只要有变化就写」——
// 暂停、seek、退出这类事件值得精确；心跳节流路径则只在切歌或跨 5 秒时写。
//
// 锁纪律：saveMu → p.mu。调用方不得持有 p.mu。
func (p *Player) sessionSaveSoon(force bool) {
	if p.conf == nil {
		return
	}
	p.saveMu.Lock()
	defer p.saveMu.Unlock()
	if force {
		p.saveForce = true
	}
	if p.saveTimer == nil {
		p.saveTimer = time.AfterFunc(sessionDebounce, p.sessionDue)
		return
	}
	p.saveTimer.Reset(sessionDebounce)
}

func (p *Player) sessionDue() {
	p.saveMu.Lock()
	force := p.saveForce
	p.saveForce = false
	p.saveMu.Unlock()
	p.sessionSave(force)
}

// sessionSave 同步落盘：队列快照按整串比较（没变不写盘），进度按 force 的
// 节流语义。持锁顺序 saveMu → p.mu；引擎位置在 p.mu 内读，与其他在锁内调
// 引擎的既有代码（PlayOrPause 等）同一惯例。
func (p *Player) sessionSave(force bool) {
	if p.conf == nil {
		return
	}
	p.saveMu.Lock()
	defer p.saveMu.Unlock()

	p.mu.RLock()
	snap := sessionQueue{
		Songs:   make([]sessionSong, 0, len(p.queue)),
		Index:   p.index,
		Mode:    p.mode,
		Shuffle: p.shuf,
	}
	for _, s := range p.queue {
		snap.Songs = append(snap.Songs, songToSession(s))
	}
	cur, hasCur := p.currentLocked()
	pos := p.pos
	if p.stream != nil && p.eng != nil {
		// 装载过就信引擎的实时位置（暂停中 tick 不跑，p.pos 会停在旧值）；
		// 未装载（停止态/恢复后待命）时 p.pos 本身就是待续点。
		if ep := p.eng.Position(); ep > 0 {
			pos = ep
		}
	}
	p.mu.RUnlock()

	if b, err := json.Marshal(snap); err == nil {
		if s := string(b); s != p.sessionWroteQ {
			p.conf.Set(sessionQueueKey, s)
			p.sessionWroteQ = s
		}
	}

	want := sessionPos{}
	if hasCur {
		want = sessionPos{Mid: cur.Mid, Pos: sessionRoundPos(pos)}
	}
	if want == p.sessionWrotePos {
		return
	}
	throttled := want.Mid != p.sessionWrotePos.Mid ||
		math.Abs(want.Pos-p.sessionWrotePos.Pos) >= sessionPosStride
	if !force && !throttled {
		return
	}
	if b, err := json.Marshal(want); err == nil {
		p.conf.Set(sessionPosKey, string(b))
		p.sessionWrotePos = want
	}
}

// sessionTickSave 是 tick 的节流门：只有「切歌或进度跨 5 秒」才放行真正
// 带序列化的 sessionSave，免得每 200ms 都 marshal 一遍整个队列。
func (p *Player) sessionTickSave() {
	if p.conf == nil {
		return
	}
	p.mu.RLock()
	cur, ok := p.currentLocked()
	pos := p.pos
	p.mu.RUnlock()
	if !ok {
		return
	}
	p.saveMu.Lock()
	need := cur.Mid != p.sessionWrotePos.Mid ||
		math.Abs(pos-p.sessionWrotePos.Pos) >= sessionPosStride
	p.saveMu.Unlock()
	if need {
		p.sessionSave(false)
	}
}

// restoreSession 由 Boot 调用：从配置恢复队列、当前下标、循环模式、随机开关
// 与当前曲进度。只「就位」，不起播（见文件头的设计说明）。
func (p *Player) restoreSession() {
	if p.conf == nil {
		return
	}
	raw := p.conf.String(sessionQueueKey, "")
	if raw == "" {
		return
	}
	var snap sessionQueue
	if err := json.Unmarshal([]byte(raw), &snap); err != nil || len(snap.Songs) == 0 {
		return
	}
	queue := make([]Song, 0, len(snap.Songs))
	for _, ss := range snap.Songs {
		queue = append(queue, sessionToSong(ss))
	}
	index := snap.Index
	if index >= len(queue) {
		index = len(queue) - 1
	}
	if index < -1 {
		index = -1
	}

	resume := 0.0
	var wrotePos sessionPos
	if praw := p.conf.String(sessionPosKey, ""); praw != "" && index >= 0 {
		var sp sessionPos
		if err := json.Unmarshal([]byte(praw), &sp); err == nil &&
			sp.Mid == queue[index].Mid && sp.Pos > 0 {
			resume = sp.Pos
			if iv := queue[index].Interval; iv > 0 && resume >= iv {
				// 元数据时长和真实流可能差一两秒；留点余量，别让「续播到
				// 结尾」一播放就判播完连跳。
				resume = iv - 0.5
			}
			if resume < 0 {
				resume = 0
			}
			sp.Pos = resume
			wrotePos = sp
		}
	}

	p.mu.Lock()
	p.queue = queue
	p.index = index
	switch snap.Mode {
	case "off", "all", "one":
		p.mode = snap.Mode
	}
	p.shuf = snap.Shuffle
	p.shufOrder = nil
	if index >= 0 {
		p.pos = resume
		if iv := queue[index].Interval; iv > 0 {
			p.dur = iv
		}
	}
	p.mu.Unlock()

	// 种下「已写盘」缓存：启动后的第一次保存不该把同样的内容又写一遍。
	p.saveMu.Lock()
	p.sessionWroteQ = raw
	if wrotePos.Mid != "" {
		p.sessionWrotePos = wrotePos
	}
	p.saveMu.Unlock()

	p.notifyChange()
}
