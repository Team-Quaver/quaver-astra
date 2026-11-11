package player

import (
	"errors"
	"fmt"
	"net/http"
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

	loved      map[string]struct{} // mid → 是否收藏；完整歌曲只留在 likedCache
	likedCache []Song
	likedTotal int64
	likedSeq   uint64

	lyrics      []LyricLine
	qrc         []QrcLine // 逐字时间轴，与 lyrics 同源同序；无逐字歌词时为空
	lyricState  string    // idle|loading|ok|none
	lyricAux    string    // off|translation|romanization
	lyricAsText string    // MPRIS xesam:asText 载荷（LRC 或纯文本）
	mprisLrc    bool      // true = asText 带行级时间戳；false = 纯文本
	lyricSeq    uint64    // 歌词请求代号：后发起的覆盖先发起的
	lyricFormat string    // 逐字来源：qrc|ttml；行级歌词为空

	// 猜你喜欢无限电台。Queue/Index 仍是唯一播放队列；radioEnd 是本轮
	// 最后一首的下标，播到它时续一轮并裁掉当前曲之前的历史。
	radioOn          bool
	radioEnd         int
	radioLoading     bool
	radioWaitingNext bool
	radioRetryAt     time.Time
	radioSeq         uint64
	radioErr         string

	loggedIn bool
	me       UserInfo

	playlists  []PlaylistRef
	playSeq    uint64
	failStreak int
	vol        float64
	muted      bool

	// 会话持久化（session.go）：退出保留播放列表与当前进度，启动恢复。
	// saveMu 护住防抖计时器、force 合并标记与「上次写盘」去重缓存；
	// 锁序恒为 saveMu → p.mu，任何路径不得反向。
	saveMu          sync.Mutex
	saveTimer       *time.Timer
	saveForce       bool
	sessionWroteQ   string
	sessionWrotePos sessionPos

	// listenTick 是音响力物理流水的采样窗口：只有真实出声的时间会累计，
	// 整曲只在切歌/停止/退出时提交一次；goroutine 只负责发送，
	// 状态全部由 p.mu 保护。
	listenActive     bool
	listenLast       time.Time
	listenSeen       float64
	listenQueue      []ListenReport
	listenSending    bool
	listenRetryAt    time.Time
	listenFailures   int
	listenTimer      *time.Timer
	listenRetryTimer *time.Timer
	listenTarget     ListenReport
	// listenKeep 标记下一次起播是同一首歌带进度重新取流（换音质、换后端、
	// 断流重连），不是新的一次收听；累计要保留，不能拆成两条记录。
	listenKeep bool
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

	// 微云音乐网盘条目。CloudFileID 非空表示这首歌从网盘播放：取流走
	// ResolveCloud（cloud_file_id），歌词走网盘刮削结果，封面可以来自
	// CloudKey 对应的自定义封面。CloudKey 是后端条目键（sha1）。
	CloudFileID   string
	CloudKey      string
	CloudName     string // 微云原始文件名（取流票据用它判断是否 QMCv1 加密）
	CloudHasCover bool
}

// Singer 歌手引用（跳歌手页需要 mid，不能只有拼好的名字）。
type Singer struct {
	ID        int64
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
	Token     string
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
	Resolve(mid, mediaMid string, songType int64, tier string, deprioritize []string, durationMs int64) (*StreamInfo, error)
	ReportListenTime(report ListenReport) (ListenReportResult, error)
	FetchLyric(mid string, trans, roma bool) (lrc, translation, romanization string, err error)
	GuessSongs(rounds int) ([]Song, error)
	LikeSong(songID int64, writeType int64, like bool) error
}

// ListenReport 一段归属明确的真实听歌时间。整曲累计、切歌时一次交给后端。
type ListenReport struct {
	StreamToken string
	SongID      int64
	Mid         string
	MediaMid    string
	AudioTimeMS int64
	Seconds     int
	SongType    int64
	SingerID    int64
	CoSinger    string
}

// ListenReportResult ok=false 表示上游明确拒绝这一段，重试不会变成功。
type ListenReportResult struct {
	OK     bool
	Reason string
}

// httpStatusError 播放器对 HTTP 状态码的最小依赖。
type httpStatusError interface {
	error
	HTTPStatus() int
}

// PlaylistRef 用户歌单引用（歌单页与选择菜单共用）。
type PlaylistRef struct {
	ID    int64
	DirID int64
	Title string
	// Cover 是歌单封面地址（后端 picurl）。歌单行首画它；为空或还没取到
	// 时退回♪占位，所以调用方不能拿它判断「这个歌单有没有封面」。
	Cover string
}

// Engine 是 player 需要的音频能力（由 *audio.Engine 基于 Liino 实现）。
// 位置/时长由引擎直接取，player 不再自己推算。
type Engine interface {
	// OpenURL 异步载入并起播；载入结果由 Loaded / LoadError 回报。
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
	// Loaded 报告本次载入是否成形（引擎收到 file-loaded）。这是「这一档能不能
	// 播」的权威信号，比「等它报出时长」可靠：不报时长的流照样能播。
	Loaded() bool
	LoadError() string
	// LoadErrorFatal 报告 LoadError 是否与音质档位无关（引擎拒绝命令、进程没了）。
	// 这类错误换档/跳歌都救不回来，player 据此直接收手。
	LoadErrorFatal() bool
	OnObserve(fn func())
}

// cloudResolver 是后端可选的微云取流能力。单独成接口，避免给所有测试
// 假后端都加一个只有网盘才用到的方法。
type cloudResolver interface {
	ResolveCloud(mid, fileID, name string, durationMs int64) (*StreamInfo, error)
}

// cloudLyricFetcher 是后端可选的网盘歌词能力（刮削/导入结果，不走 QQ
// 歌曲歌词接口）。
type cloudLyricFetcher interface {
	FetchCloudLyric(key string) (lrc, translation, romanization string, err error)
}

// startVolumeSetter is the optional fast path of an asynchronous audio
// engine: the saved volume is set locally before its background start
// finishes, instead of queueing an IPC command that would be lost.
type startVolumeSetter interface {
	SetStartVolume(v float64)
}

// versionReporter 是引擎的可选版本自报；设置页用它展示引擎版本。
type versionReporter interface {
	Version() string
}

// EngineVersion 返回播放引擎的版本串（不可用时为空）。
func (p *Player) EngineVersion() string {
	p.mu.RLock()
	e := p.eng
	p.mu.RUnlock()
	vr, ok := e.(versionReporter)
	if !ok {
		return ""
	}
	return vr.Version()
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
		loved:      map[string]struct{}{},
		lyricAux:   readLyricAux(prefs),
		mprisLrc:   readMprisLrc(prefs),
	}
	return p
}

// readLyricAux 兼容旧配置：先读三档 Style.LyricAux；没有就按布尔
// Style.ShowTranslation 回落（旧版本只有翻译开关）。
func readLyricAux(prefs Preferences) string {
	switch strings.ToLower(strings.TrimSpace(prefs.String("Style.LyricAux", ""))) {
	case "off", "translation", "romanization":
		return strings.ToLower(strings.TrimSpace(prefs.String("Style.LyricAux", "")))
	}
	if prefs.Bool("Style.ShowTranslation", true) {
		return "translation"
	}
	return "off"
}

// readMprisLrc 读系统媒体控件的歌词载荷格式：默认 LRC（带时间戳）。
func readMprisLrc(prefs Preferences) bool {
	switch strings.ToLower(strings.TrimSpace(prefs.String("Lyrics.MprisLrc", ""))) {
	case "false", "0", "no":
		return false
	case "true", "1", "yes":
		return true
	}
	return prefs.Bool("Lyrics.MprisLrc", true)
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

// likedRetryDelay 是「我喜欢」第 1 页失败后的重试间隔：只为跨过冷启动时后端
// 未就绪、网络抖一下这一小段，不做无限重试。（var 而非 const：测试要压缩等待。）
var likedRetryDelay = 2 * time.Second

// LoadLoved 分页拉取我喜欢（最多 2 页 × 500）。
//
// 这份集合决定每一行画空心还是实心红心，所以原则是「拿到多少落多少、落不下
// 就什么都不动」：
//   - 第 1 页失败：隔 likedRetryDelay 重试一次（冷启动时后端可能还没就绪、
//     网络抖一下），仍失败就整份保留上一次的结果。拿空集去覆盖，满屏红心会
//     一起消失，看起来就像「收藏渲染不出来」。
//   - 第 2 页失败：把手里的第 1 页照样落地。少几首红心远好于所有红心都不显示。
func (p *Player) LoadLoved() {
	p.mu.Lock()
	p.likedSeq++
	seq := p.likedSeq
	p.mu.Unlock()

	go func() {
		all := map[string]struct{}{}
		var first []Song
		total := int64(0)
		for page := 1; page <= 2; page++ {
			songs, tot, hasmore, err := p.api.LikedPage(page, 500)
			if err != nil && page == 1 && p.likedFresh(seq) {
				time.Sleep(likedRetryDelay)
				songs, tot, hasmore, err = p.api.LikedPage(1, 500)
			}
			if err != nil {
				if page == 1 {
					return // 一首都没拿到：什么都不改
				}
				break // 第 2 页没拿到：已有的部分照样落地
			}
			if !p.likedFresh(seq) {
				return // 已经有更新的一轮在跑，这轮的结论作废
			}
			total = tot
			if page == 1 {
				first = songs
			}
			for _, s := range songs {
				all[s.Mid] = struct{}{}
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

// likedFresh 报告这一轮喜欢刷新还是最新的那一轮（没被后发起的一轮取代）。
func (p *Player) likedFresh(seq uint64) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return seq == p.likedSeq
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

// QueueLen 返回队列长度；只需要数量时避免复制整份队列。
func (p *Player) QueueLen() int {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.queue)
}

// QueueRowView 是队列/托盘渲染需要的紧凑歌曲形状。按索引取一行只复制少量
// 字符串头，不复制整个 Song（其中还包含 Singers 切片）。
type QueueRowView struct {
	Mid, Title, Name, Artists, AlbumPmid, AlbumMid string
	CloudFileID, CloudKey                          string
	CloudHasCover                                  bool
}

func (s QueueRowView) DisplayName() string {
	if s.Title != "" {
		return s.Title
	}
	return s.Name
}

// QueueRow 返回第 i 行的紧凑视图。
func (p *Player) QueueRow(i int) (QueueRowView, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if i < 0 || i >= len(p.queue) {
		return QueueRowView{}, false
	}
	s := p.queue[i]
	return QueueRowView{
		Mid:           s.Mid,
		Title:         s.Title,
		Name:          s.Name,
		Artists:       s.Artists,
		AlbumPmid:     s.AlbumPmid,
		AlbumMid:      s.AlbumMid,
		CloudFileID:   s.CloudFileID,
		CloudKey:      s.CloudKey,
		CloudHasCover: s.CloudHasCover,
	}, true
}

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

// listenTargetLocked 返回正在累计的上报目标。恢复会话时没有 token，
// 音响力不上报：后端宁可重新取凭证也不接受可能已换人的旧 token。
func (p *Player) listenTargetLocked(seconds int) (ListenReport, bool) {
	if p.listenTarget.StreamToken == "" || p.listenTarget.SongID <= 0 {
		return ListenReport{}, false
	}
	job := p.listenTarget
	job.Seconds = seconds
	return job, true
}

// listenSwitchLocked 在新流真正载入后切换上报目标。旧目标此时已在
// startCurrent 开头交出整曲累计，这里只建立新窗口。
func (p *Player) listenSwitchLocked(song Song, info *StreamInfo) {
	p.listenTarget = ListenReport{}
	// 网盘曲目不能记到 QQ 音乐音响力：实际播放的是微云文件，不是
	// song_info 指向的曲库音频。
	if song.CloudFileID != "" {
		return
	}
	if info == nil || info.Token == "" || song.Mid == "" || song.SongID <= 0 || p.dur <= 0 {
		return
	}
	var singerID int64
	coSinger := ""
	if len(song.Singers) > 0 {
		singerID = song.Singers[0].ID
		coSinger = song.Singers[0].Name
	}
	p.listenTarget = ListenReport{
		StreamToken: info.Token,
		SongID:      song.SongID,
		Mid:         song.Mid,
		AudioTimeMS: int64(p.dur * 1000),
		SongType:    song.SongType,
		SingerID:    singerID,
		CoSinger:    coSinger,
	}
}

// listenEnabled 是音响力开关的唯一读取入口；nil 配置只在测试直构 App 时出现。
func (p *Player) listenEnabled() bool {
	return p.conf != nil && p.conf.Bool("Playing.ReportListenTime", true)
}

// listenTargetReadyLocked 表示当前有可归属的上报目标。加载间隙与恢复会话
// （没有新取的 token）都不算听歌时间，采样窗口也不该启动。
func (p *Player) listenTargetReadyLocked() bool {
	return p.listenTarget.StreamToken != "" && p.listenTarget.SongID > 0
}

// listenSyncLocked 把采样窗口对齐真实出声状态；停止时顺手交出整曲累计。
func (p *Player) listenSyncLocked(active bool) {
	if active && !p.listenTargetReadyLocked() {
		active = false
	}
	if p.listenActive == active {
		if active {
			if p.listenLast.IsZero() {
				p.listenLast = time.Now()
			}
			p.listenArmLocked()
		}
		return
	}
	if active {
		p.listenActive = true
		p.listenLast = time.Now()
		p.listenArmLocked()
		return
	}
	// 暂停/缓冲不是换曲：本曲累计要留给下一次续播，交出去就会把
	// 同一首听歌拆成两条记录。
	p.listenPauseLocked()
}

// listenArmLocked 排下一次采样心跳。必须幂等：tick 的 200ms 循环每次都会
// 走到这里，重复排定时器会让累计速度成倍。
func (p *Player) listenArmLocked() {
	if p.listenTimer != nil || !p.listenActive {
		return
	}
	p.listenTimer = time.AfterFunc(listenTickInterval, func() { go p.listenTick() })
}

const (
	listenMinReportSeconds = 5
	listenMaxSampleSeconds = 5
	listenTickInterval     = time.Second
	listenRetryBase        = 30 * time.Second
	listenRetryMax         = 5 * time.Minute
	// listenCloseFlushWait 是退出时等待最后一次上报的上限。整曲累计只在
	// 切歌/停止/退出时提交，退出时丢了就真的没了；但也不能让用户等太久。
	listenCloseFlushWait = 3 * time.Second
)

// PlayList 用给定列表替换队列并从 i 开始播放。
func (p *Player) PlayList(songs []Song, i int) {
	p.mu.Lock()
	if len(songs) == 0 {
		p.mu.Unlock()
		return
	}
	p.exitRadioLocked()
	p.queue = append([]Song(nil), songs...)
	if i < 0 || i >= len(p.queue) {
		i = 0
	}
	p.index = i
	p.shufOrder = nil
	p.mu.Unlock()
	p.sessionSaveSoon(true)
	p.notifyChange()
	p.playFrom(0, true, "")
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
	p.sessionSaveSoon(true)
	p.notifyChange()
	p.playFrom(0, true, "")
}

// EnqueueNext 「下一首播放」：排到当前曲之后等着播，不切歌、不打断当前曲。
//
// 队列还空着（没播过任何东西）时没有「下一首」可言，退化成单曲起播
// （与主项目 player.enqueueNext 同一语义分叉）。
func (p *Player) EnqueueNext(s Song) {
	p.mu.Lock()
	if p.radioOn {
		p.mu.Unlock()
		return
	}
	if p.index < 0 || len(p.queue) == 0 {
		p.mu.Unlock()
		p.PlayList([]Song{s}, 0)
		return
	}
	at := p.index + 1
	p.queue = append(p.queue, Song{})
	copy(p.queue[at+1:], p.queue[at:])
	p.queue[at] = s
	p.mu.Unlock()
	p.sessionSaveSoon(false)
	p.notifyChange()
}

// PlayNextNow 插队并立即播放。
func (p *Player) PlayNextNow(s Song) {
	p.mu.Lock()
	if p.radioOn {
		p.mu.Unlock()
		return
	}
	at := p.index + 1
	if p.index < 0 {
		at = len(p.queue)
	}
	p.queue = append(p.queue, Song{})
	copy(p.queue[at+1:], p.queue[at:])
	p.queue[at] = s
	p.index = at
	p.mu.Unlock()
	p.sessionSaveSoon(true)
	p.notifyChange()
	p.playFrom(0, true, "")
}

// EnqueueMany 追加到队列尾部。
func (p *Player) EnqueueMany(songs []Song) {
	p.mu.Lock()
	p.exitRadioLocked()
	p.queue = append(p.queue, songs...)
	p.mu.Unlock()
	p.sessionSaveSoon(false)
	p.notifyChange()
}

// RemoveAt 删除队列第 i 首（删当前曲则原地停/换歌）。
func (p *Player) RemoveAt(i int) {
	p.mu.Lock()
	p.exitRadioLocked()
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
			p.sessionSaveSoon(true)
			p.notifyChange()
			p.playFrom(0, false, "")
			return
		}
		p.stopLocked()
		p.mu.Unlock()
		p.sessionSaveSoon(true)
		p.notifyChange()
		return
	}
	if i < p.index {
		p.index--
	}
	p.mu.Unlock()
	p.sessionSaveSoon(false)
	p.notifyChange()
}

// ClearQueue 清空队列。
func (p *Player) ClearQueue() {
	p.mu.Lock()
	p.exitRadioLocked()
	p.queue = nil
	p.index = -1
	p.stopLocked()
	p.mu.Unlock()
	p.sessionSaveSoon(true)
	p.notifyChange()
}

// ===== 猜你喜欢 · 无限电台 =====

func (p *Player) RadioActive() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.radioOn
}

func (p *Player) RadioLoading() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.radioLoading
}

func (p *Player) RadioError() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.radioErr
}

// CanPrev 报告上一首是否可用。无限电台只向前走，必须把按钮、快捷键和
// 媒体键的共同底层入口一起关掉，不能只在 UI 里藏按钮。
func (p *Player) CanPrev() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return len(p.queue) > 0 && !p.radioOn
}

func (p *Player) exitRadioLocked() {
	p.radioSeq++
	p.radioOn = false
	p.radioEnd = -1
	p.radioLoading = false
	p.radioWaitingNext = false
	p.radioRetryAt = time.Time{}
	p.radioErr = ""
}

// StartRadio 开始 / 换一批猜你喜欢无限电台。先取一轮 5 首，播到本轮末
// 自动续下一轮并裁掉当前曲之前的历史；再次调用会换掉整轮队列。
func (p *Player) StartRadio(autoplay bool) {
	p.mu.Lock()
	seq := p.radioSeq + 1
	p.radioSeq = seq
	wasRadio := p.radioOn
	prevEnd := p.radioEnd
	waiting := p.radioWaitingNext
	p.radioOn = true
	p.radioEnd = -1
	p.radioLoading = true
	p.radioWaitingNext = false
	p.radioRetryAt = time.Time{}
	p.radioErr = ""
	p.mu.Unlock()
	p.notifyChange()

	go func() {
		songs, err := p.api.GuessSongs(1)
		p.mu.Lock()
		if seq != p.radioSeq {
			p.mu.Unlock()
			return
		}
		if err != nil || len(songs) == 0 {
			if err == nil {
				err = errRadioEmpty
			}
			p.radioLoading = false
			p.radioErr = err.Error()
			if !wasRadio && len(p.queue) == 0 {
				p.radioOn = false
			} else {
				p.radioEnd = prevEnd
				p.radioWaitingNext = waiting
			}
			p.mu.Unlock()
			p.notifyChange()
			return
		}
		p.queue = append([]Song(nil), songs...)
		p.index = 0
		p.radioEnd = len(p.queue) - 1
		p.radioLoading = false
		p.radioErr = ""
		p.shufOrder = nil
		p.mu.Unlock()
		p.sessionSaveSoon(true)
		p.notifyChange()
		p.startCurrent(0, autoplay, "")
	}()
}

// StopRadio 退出电台会话但不动当前播放队列。
func (p *Player) StopRadio() {
	p.mu.Lock()
	if !p.radioOn {
		p.mu.Unlock()
		return
	}
	p.exitRadioLocked()
	p.mu.Unlock()
	p.notifyChange()
}

var errRadioEmpty = errors.New("猜你喜欢暂无推荐，登录后可得")

// requestRadioExtendLocked 在轮末请求下一轮。返回 true 表示已经开始请求。
func (p *Player) requestRadioExtendLocked() bool {
	if !p.radioOn || p.radioLoading || p.radioEnd < 0 {
		return false
	}
	p.radioLoading = true
	p.radioWaitingNext = true
	p.radioRetryAt = time.Time{}
	p.radioErr = ""
	seq := p.radioSeq
	p.mu.Unlock()
	p.notifyChange()
	go p.extendRadio(seq)
	return true
}

func (p *Player) extendRadio(seq uint64) {
	songs, err := p.api.GuessSongs(1)
	p.mu.Lock()
	if seq != p.radioSeq || !p.radioOn {
		p.mu.Unlock()
		return
	}
	if err != nil || len(songs) == 0 {
		if err == nil {
			err = errors.New("猜你喜欢暂无更多推荐")
		}
		p.radioLoading = false
		p.radioErr = err.Error()
		// 自然播完会在每个 tick 再调 Next；失败后必须退避，不能 200ms
		// 一次锤上游。用户手点下一首（auto=false）可立即重试。
		p.radioRetryAt = time.Now().Add(5 * time.Second)
		p.mu.Unlock()
		p.notifyChange()
		return
	}
	anchor := p.index
	p.queue = append(p.queue, songs...)
	drop := max(0, anchor)
	if drop > 0 {
		p.queue = append([]Song(nil), p.queue[drop:]...)
		p.index -= drop
	}
	if p.index < 0 {
		p.index = 0
	}
	p.radioEnd = len(p.queue) - 1
	p.radioLoading = false
	p.radioRetryAt = time.Time{}
	waiting := p.radioWaitingNext
	p.radioWaitingNext = false
	next := p.index + 1
	p.mu.Unlock()
	p.sessionSaveSoon(false)
	p.notifyChange()
	if waiting && next < p.QueueLen() {
		p.Jump(next)
	}
}

// MoveInQueue 拖拽排序（rows 的歌移到 to 之前；当前曲跟随）。
func (p *Player) MoveInQueue(rows []int, to int) {
	p.mu.Lock()
	p.exitRadioLocked()
	if len(rows) == 0 || to < 0 || to > len(p.queue) {
		p.mu.Unlock()
		return
	}
	cur := ""
	if p.index >= 0 && p.index < len(p.queue) {
		cur = p.queue[p.index].Mid
	}
	rm := make([]bool, len(p.queue))
	for _, r := range rows {
		if r >= 0 && r < len(p.queue) {
			rm[r] = true
		}
	}
	adjusted := to
	for i := 0; i < to && i < len(rm); i++ {
		if rm[i] {
			adjusted--
		}
	}
	out := make([]Song, 0, len(p.queue))
	sourcePos := 0
	for sourcePos < len(p.queue) && len(out) < adjusted {
		if !rm[sourcePos] {
			out = append(out, p.queue[sourcePos])
		}
		sourcePos++
	}
	for _, r := range rows {
		if r >= 0 && r < len(p.queue) {
			out = append(out, p.queue[r])
		}
	}
	for ; sourcePos < len(p.queue); sourcePos++ {
		if !rm[sourcePos] {
			out = append(out, p.queue[sourcePos])
		}
	}
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
	p.sessionSaveSoon(false)
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
	if p.radioOn {
		p.mu.Unlock()
		return
	}
	switch p.mode {
	case "off":
		p.mode = "all"
	case "all":
		p.mode = "one"
	default:
		p.mode = "off"
	}
	p.mu.Unlock()
	p.sessionSaveSoon(false)
	p.notifyChange()
}

// SetMode 直接设置播放模式（托盘菜单的单选项用；非法值忽略）。
func (p *Player) SetMode(mode string) {
	if mode != "off" && mode != "all" && mode != "one" {
		return
	}
	p.mu.Lock()
	if p.radioOn {
		p.mu.Unlock()
		return
	}
	changed := p.mode != mode
	p.mode = mode
	p.mu.Unlock()
	if changed {
		p.sessionSaveSoon(false)
		p.notifyChange()
	}
}

func (p *Player) Shuffle() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.shuf
}

func (p *Player) SetShuffle(on bool) {
	p.mu.Lock()
	if p.radioOn {
		p.mu.Unlock()
		return
	}
	p.shuf = on
	p.shufOrder = nil
	p.mu.Unlock()
	p.sessionSaveSoon(false)
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
	if p.radioOn {
		if p.radioEnd < 0 {
			p.mu.Unlock()
			return
		}
		if p.index >= p.radioEnd || p.index+1 >= len(p.queue) {
			if auto && time.Now().Before(p.radioRetryAt) {
				p.mu.Unlock()
				return
			}
			if p.requestRadioExtendLocked() {
				return
			}
			p.mu.Unlock()
			return
		}
		next := p.index + 1
		p.index = next
		p.mu.Unlock()
		p.sessionSaveSoon(true)
		p.notifyChange()
		p.playFrom(0, true, "")
		return
	}
	next := p.nextIndexLocked(auto)
	if next < 0 {
		p.stopLocked()
		p.mu.Unlock()
		p.sessionSaveSoon(true)
		p.notifyChange()
		return
	}
	p.index = next
	p.mu.Unlock()
	p.sessionSaveSoon(true)
	p.notifyChange()
	p.playFrom(0, true, "")
}

func (p *Player) Prev() {
	p.mu.RLock()
	if p.radioOn {
		p.mu.RUnlock()
		return
	}
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
	p.sessionSaveSoon(true)
	p.notifyChange()
	p.playFrom(0, true, "")
}

// SetEngine 在运行时切换音频后端（设置页「使用 MPV 作为播放后端」）。
//
// 旧引擎先停后关；播到一半时用新引擎从同一进度重新取流，暂停态则只
// 保留进度，等用户按播放再续。播放代数递增，旧起播协程的结果不会落到
// 新引擎上。
func (p *Player) SetEngine(e Engine) {
	if e == nil {
		return
	}
	p.mu.Lock()
	old := p.eng
	wasPlaying := p.playing
	pos := p.pos
	vol := p.vol
	muted := p.muted
	has := p.index >= 0 && p.index < len(p.queue)
	p.playSeq++
	p.eng = e
	p.playing = false
	p.loading = false
	p.stream = nil
	p.err = ""
	// 换后端不是换曲：之后的重新取流/续播要接着累计。这里只打标记，
	// 真正是否同一首由 listenRestartLocked 按 SongID+Mid 再确认一次。
	p.listenKeep = p.listenTargetReadyLocked()
	p.listenPauseLocked()
	p.mu.Unlock()

	if old != nil && old != e {
		old.Stop()
		// Close 可能等待解码线程/子进程收尾（最长 2s）；它已经不再服务
		// 播放器，放到后台收，切换开关时不冻结 UI。
		go old.Close()
	}
	if sv, ok := e.(startVolumeSetter); ok {
		sv.SetStartVolume(vol)
	} else {
		e.SetVolume(vol)
	}
	if muted {
		e.SetVolume(0)
	}
	e.OnObserve(p.notifyChange)
	p.notifyChange()
	if has && wasPlaying {
		p.playFrom(pos, true, "")
	}
}

// PlayOrPause 播放/暂停切换；没有当前曲则播第一首。
func (p *Player) PlayOrPause() {
	p.mu.Lock()
	if p.index < 0 || p.index >= len(p.queue) {
		if len(p.queue) > 0 {
			p.index = 0
			p.mu.Unlock()
			p.sessionSaveSoon(true)
			p.notifyChange()
			p.playFrom(0, true, "")
			return
		}
		p.mu.Unlock()
		return
	}
	if p.eng.Paused() || !p.playing {
		if p.stream == nil && !p.loading {
			// 待命/停止态引擎里没有装载对象，eng.Play 会空转——走完整
			// 起播链，从界面进度续播（会话恢复的上次位置，stop 后是 0）。
			resume := p.pos
			p.mu.Unlock()
			p.playFrom(resume, true, "")
			return
		}
		p.eng.Play()
		p.playing = true
	} else {
		p.eng.Pause()
		p.playing = false
	}
	p.mu.Unlock()
	p.listenSetPlaying()
	p.sessionSaveSoon(true)
	p.notifyChange()
}

// Play 播放：暂停中直接续播，没有当前曲（停止态/首次）则从队列第一首起播。
// 与 PlayOrPause 的区别是它只朝「播」的方向走——已在播时什么都不做，
// 不会被媒体键或「播放」快捷键误暂停。
func (p *Player) Play() {
	p.mu.Lock()
	if p.index < 0 || p.index >= len(p.queue) {
		if len(p.queue) > 0 {
			p.index = 0
			p.mu.Unlock()
			p.sessionSaveSoon(true)
			p.notifyChange()
			p.playFrom(0, true, "")
			return
		}
		p.mu.Unlock()
		return
	}
	if p.eng.Paused() || !p.playing {
		if p.stream == nil && !p.loading {
			// 同 PlayOrPause：待命态（会话恢复后）走完整起播链续播，
			// 别让 eng.Play 在空引擎上空转。
			resume := p.pos
			p.mu.Unlock()
			p.playFrom(resume, true, "")
			return
		}
		p.eng.Play()
		p.playing = true
		p.mu.Unlock()
		p.listenSetPlaying()
		p.sessionSaveSoon(true)
		p.notifyChange()
		return
	}
	p.mu.Unlock()
}

// Pause 暂停：只有真在播（含载入中）才动手。已暂停或未播放时不通知，
// 免得「暂停」键按下去白白刷一次托盘/媒体中心。
func (p *Player) Pause() {
	p.mu.Lock()
	paused := p.index >= 0 && p.index < len(p.queue) && p.playing && !p.eng.Paused()
	if paused {
		p.eng.Pause()
		p.playing = false
	}
	p.mu.Unlock()
	p.listenSetPlaying()
	if paused {
		p.sessionSaveSoon(true)
		p.notifyChange()
	}
}

func (p *Player) Playing() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.playing && !p.eng.Paused()
}

// PlayingAudio 报告引擎是否正在实际出声。Loading/缓冲开始前不算播放，
// 「播放时禁止睡眠」按这个真实口径持有系统请求。
func (p *Player) PlayingAudio() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.playing && !p.eng.Paused() && p.eng.IsPlaying()
}

func (p *Player) listenAudioLocked() bool {
	// loading 期间引擎里可能还是上一段流（换音质/换后端正在重新取流），
	// 这段等待不是听歌时间，别算进累计。
	return p.listenEnabled() && p.playing && !p.loading && !p.eng.Paused() && p.eng.IsPlaying()
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

// listenTick 是音响力的采样心跳。上报与 HTTP 重试全部走后台 goroutine，
// 采样本身只做时间差累计，不阻塞播放心跳。
//
// 这里刻意不做分片上报：上游会把每次提交都记成一条独立听歌记录，
// 同一首歌切成几片就会在「最近播放」里重复出现。整曲只累计，
// 到切歌/停止时由 listenFlushLocked 一次交出。
func (p *Player) listenTick() {
	p.mu.Lock()
	if !p.listenEnabled() {
		p.listenActive = false
		p.listenLast = time.Time{}
		p.listenSeen = 0
		p.listenQueue = nil
		p.listenStopTimersLocked()
		p.mu.Unlock()
		return
	}
	if !p.listenActive {
		p.mu.Unlock()
		return
	}
	// 本次心跳已经触发，AfterFunc 不会自己续期。先停掉（对已触发的定时器
	// 是空操作）再清句柄，避免与 listenSyncLocked 并发时各排一个。
	if p.listenTimer != nil {
		p.listenTimer.Stop()
		p.listenTimer = nil
	}
	p.listenArmLocked()
	now := time.Now()
	elapsed := now.Sub(p.listenLast).Seconds()
	p.listenLast = now
	if elapsed > listenMaxSampleSeconds {
		elapsed = listenMaxSampleSeconds
	}
	if elapsed < 0 {
		elapsed = 0
	}
	p.listenSeen += elapsed
	p.mu.Unlock()
	p.listenDrain()
}

// listenSetPlaying 在真实出声与暂停/缓冲之间切换采样窗口。
// 状态必须在这里按锁内现场推导，不能采信调用方在解锁前捕获的旧值：
// 暂停与续播可能紧挨着发生，旧快照若晚到会把刚恢复的采样窗口又关掉。
func (p *Player) listenSetPlaying() {
	p.mu.Lock()
	p.listenSyncLocked(p.listenAudioLocked())
	p.mu.Unlock()
	p.listenDrain()
}

// listenFlushLocked 把当前曲的累计时长入队。调用方必须持有 p.mu 且已先采样。
func (p *Player) listenFlushLocked() {
	seconds := int(p.listenSeen)
	if job, ok := p.listenTargetLocked(seconds); ok && seconds >= listenMinReportSeconds {
		p.listenQueue = append(p.listenQueue, job)
	}
	p.listenSeen = 0
}

// listenSampleLocked 把上次采样点到现在的真实出声时间记进本曲累计。
// 事件循环被挂起时最多只认 listenMaxSampleSeconds，避免把整段空白算成听歌。
func (p *Player) listenSampleLocked() {
	if !p.listenActive || p.listenLast.IsZero() {
		return
	}
	elapsed := time.Now().Sub(p.listenLast).Seconds()
	p.listenLast = time.Time{}
	if elapsed <= 0 {
		return
	}
	if elapsed > listenMaxSampleSeconds {
		elapsed = listenMaxSampleSeconds
	}
	p.listenSeen += elapsed
}

// listenPauseLocked 暂停/缓冲：结算已听时长但不上报，续播时接着累计。
func (p *Player) listenPauseLocked() {
	p.listenSampleLocked()
	p.listenActive = false
	p.listenStopSampleLocked()
}

// listenRestartLocked 在起播新流前结算当前目标。同一首歌带进度重新取流
// （换音质、换播放后端、断流重连）不是新的一次收听：累计保留，等新 token
// 到位后继续记在同一段里。真正换曲或从头重播才交出整曲累计。
func (p *Player) listenRestartLocked(song Song, ok bool) {
	p.listenSampleLocked()
	sameSong := ok && p.listenKeep &&
		p.listenTarget.SongID > 0 && p.listenTarget.SongID == song.SongID &&
		p.listenTarget.Mid == song.Mid
	p.listenKeep = false
	if sameSong {
		// 旧目标先留着：万一重新取流失败，listenResetLocked 还能把这段
		// 时长归到这首歌上（后端可按 mid 重新取 vkey）。新流载入后
		// listenSwitchLocked 会换成新 token。
		p.listenActive = false
		p.listenStopSampleLocked()
		return
	}
	p.listenFlushLocked()
	// 旧 token 只服务旧的那一次收听；新曲要等自己的流载入后重新建立目标。
	p.listenTarget = ListenReport{}
	p.listenActive = false
	p.listenStopSampleLocked()
}

// listenResetLocked 结束当前目标：交出整曲累计并停住采样。新歌要等引擎
// 真正重新出声（listenSetPlaying）才开始累计，加载间隙不算听歌时间。
func (p *Player) listenResetLocked() {
	p.listenSampleLocked()
	p.listenFlushLocked()
	p.listenKeep = false
	p.listenActive = false
	p.listenStopSampleLocked()
}

// listenDrain 串行发送队列。网络失败按 30 秒起步的指数退避重试；
// 401/403/422 与上游明确拒绝是确定性失败，直接丢弃这一段。
func (p *Player) listenDrain() {
	p.mu.Lock()
	if p.listenSending || len(p.listenQueue) == 0 || !p.listenEnabled() {
		p.mu.Unlock()
		return
	}
	if time.Now().Before(p.listenRetryAt) {
		p.mu.Unlock()
		return
	}
	job := p.listenQueue[0]
	p.listenQueue = p.listenQueue[1:]
	p.listenSending = true
	p.mu.Unlock()

	go func() {
		res, err := p.api.ReportListenTime(job)
		p.mu.Lock()
		p.listenSending = false
		if err != nil {
			var statusErr httpStatusError
			if errors.As(err, &statusErr) {
				status := statusErr.HTTPStatus()
				if status >= 400 && status < 500 && status != http.StatusRequestTimeout && status != http.StatusTooManyRequests {
					p.listenFailures = 0
					p.listenRetryAt = time.Time{}
					p.mu.Unlock()
					p.listenKickRetryLocked()
					return
				}
			}
			p.listenFailures++
			delay := listenRetryBase
			for i := 1; i < p.listenFailures && i < 5; i++ {
				delay *= 2
			}
			if delay > listenRetryMax {
				delay = listenRetryMax
			}
			p.listenRetryAt = time.Now().Add(delay)
			p.listenQueue = append([]ListenReport{job}, p.listenQueue...)
			p.listenKickRetryLocked()
		} else {
			p.listenFailures = 0
			p.listenRetryAt = time.Time{}
			_ = res
		}
		hasQueue := len(p.listenQueue) > 0
		p.mu.Unlock()
		if hasQueue {
			p.listenDrain()
		}
	}()
}

// listenKickRetryLocked 在退避窗口内保留一个发送队列的唤醒点。
func (p *Player) listenKickRetryLocked() {
	if p.listenRetryTimer != nil || p.listenRetryAt.IsZero() {
		return
	}
	delay := time.Until(p.listenRetryAt)
	if delay <= 0 {
		delay = time.Millisecond
	}
	p.listenRetryTimer = time.AfterFunc(delay, func() { go p.listenDrain() })
}

func (p *Player) listenStopTimersLocked() {
	p.listenStopSampleLocked()
	if p.listenRetryTimer != nil {
		p.listenRetryTimer.Stop()
		p.listenRetryTimer = nil
	}
}

// listenStopSampleLocked 只停采样心跳。上报的重试定时器与采样无关：
// 暂停/切歌时顺手停掉它，退避中的那一条就再也没人来唤醒。
func (p *Player) listenStopSampleLocked() {
	if p.listenTimer != nil {
		p.listenTimer.Stop()
		p.listenTimer = nil
	}
}

// SeekTo 按 0..1 比例跳转。引擎装载了当前曲时交给引擎（位置由引擎回报，
// 这里不再自行重锚定）；待命态（会话刚恢复、停止/播完）引擎里没有媒体，
// seek 只挪界面进度，用户按播放时以该位置续播。
func (p *Player) SeekTo(frac float64) {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	p.mu.Lock()
	dur := p.dur
	if p.stream == nil && !p.loading {
		p.pos = frac * dur
		p.mu.Unlock()
		p.sessionSaveSoon(true)
		p.notifyChange()
		return
	}
	// 引擎装载了当前曲：真实位置由引擎回报。但暂停时心跳不会同步位置
	//（tick 在暂停分支直接返回），所以这里先把界面位置落定——否则拖动
	// 松手后进度条会弹回原处，直到用户按播放才「跳过去」。恢复播放后的
	// 第一次心跳会用引擎时钟校正，不影响精度。
	p.pos = frac * dur
	p.mu.Unlock()
	p.eng.SeekTo(frac, dur)
	p.sessionSaveSoon(true)
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
	p.mu.Lock()
	p.listenKeep = true
	p.mu.Unlock()
	p.playFrom(pos, true, "")
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
// 引擎支持集是全档位（含 atmos / DTS / FLAC / AAC），
// 因此这里直接取未锁定档位里 rank 最高的一档；allowAtmos=false 时
// 跳过全景声档位，「自动」不该悄悄把用户送进 7.1/5.1。
func pickTier(table *TierTable, want string, allowAtmos bool) string {
	if want != "auto" {
		return want
	}
	if table == nil {
		return "320"
	}
	best, bestRank := "128", -1
	for _, t := range table.Tiers {
		if t.Locked || (!allowAtmos && qmAtmosTier(t.ID)) {
			continue
		}
		if t.Rank > bestRank {
			best, bestRank = t.ID, t.Rank
		}
	}
	return best
}

// ===== 内部：起播 =====

// qmAtmosFallbackTiers 是「回退到全景声」开关降权的全部档位。
var qmAtmosFallbackTiers = []string{"atmos71", "atmos51"}

func (p *Player) stopLocked() {
	p.eng.Stop()
	p.playing = false
	p.loading = false
	p.stream = nil
	p.pos, p.dur = 0, 0
	p.listenResetLocked()
}

// playFrom 用户/媒体键发起的起播：先清掉连跳计数再走完整起播链——
// 上一次故障留下的余数不该把这次新的意图直接判成「已经连跳好几首」。
func (p *Player) playFrom(resumeTo float64, autoplay bool, overrideTier string) {
	p.mu.Lock()
	p.failStreak = 0
	p.mu.Unlock()
	p.startCurrent(resumeTo, autoplay, overrideTier)
}

// startCurrent 解析并载入当前曲。调用方不得持有 p.mu。
func (p *Player) startCurrent(resumeTo float64, autoplay bool, overrideTier string) {
	p.mu.Lock()
	p.playSeq++
	seq := p.playSeq
	song, ok := p.currentLocked()
	// 先结束旧目标：正在播的时长归属旧流，不能在解析成功后记到新歌头上。
	// 同一首重新取流（换音质、换后端、单曲循环）只是换 token，累计要带过去，
	// 否则一次连续收听会被拆成两条听歌记录。
	p.listenRestartLocked(song, ok)
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
	// 开关关闭（默认）时全景声不参与自动选档与回退；只有用户显式
	// 点了 7.1 / 5.1 才允许。开关打开时把两档压到回退链尾部——
	// 只在常规档位都不可播时才回退到全景声，而不是优先选它。
	allowAtmos := p.conf.Bool("Quality.FallbackToQMAtmos", false) || qmAtmosTier(want)
	if want == "auto" {
		want = pickTier(p.tiers, "auto", allowAtmos)
	}
	dep := []string{}
	if allowAtmos {
		// 显式列出两档全景声：7.1 与 5.1 都属于「Fallback Atmos」，
		// 不能只降权 5.1 而让 7.1 仍按普通高档位优先。
		dep = append([]string(nil), qmAtmosFallbackTiers...)
	}
	p.mu.Unlock()
	p.notifyChange()

	// 会话局部回退集合：只被本次起播的协程链访问，避免跨会话共享竞争
	fallback := map[string]bool{}
	go func() {
		// 解析 → 交给引擎载入 → 等载入结果。任何一环失败（档位不被支持、
		// 网络错误）都继续走回退链换下一档。
		var info *StreamInfo
		resume := resumeTo     // 起播进度；带进度失败时下面退回从 0 再试
		resumeRetried := false // 「从 0 再试一次」只有一次机会
		for {
			i, tier, err := p.resolveWithFallback(song, want, dep, seq, fallback, allowAtmos)
			if err != nil {
				p.failWithErr(seq, err)
				return
			}
			p.mu.Lock()
			stale := seq != p.playSeq
			dur := p.dur
			p.mu.Unlock()
			if stale {
				return
			}
			if lerr := p.eng.OpenURL(i.URL, resume/dur, dur, autoplay); lerr != nil {
				// 引擎压根没收下这次载入（引擎不可用等）：与档位无关，
				// 换档只是把整条队列白白跳完。
				p.failWithErr(seq, fmt.Errorf("%w：%w", errEngineFatal, lerr))
				return
			}
			// 引擎载入是异步的：网络流可能慢，要等它给出结果才能判断这档
			// 能不能播。等不到（超时）或报错都视为该档失败，继续降档。
			lerr := p.awaitLoaded(seq)
			if lerr == nil {
				info = i
				_ = tier
				break
			}
			if errors.Is(lerr, errStale) {
				return // 用户已经换歌/换档到别处，这轮的结论不必再报给任何人
			}
			if errors.Is(lerr, errEngineFatal) {
				p.failWithErr(seq, lerr)
				return
			}
			if resume > 0 && !resumeRetried {
				// 带进度起播失败：同一档位先退回从 0 再试一次。上次会话的进度、
				// 这一档不支持 Range、起点落在坏点，都只该让「续播」这一下失败，
				// 而从 0 播是用户完全能接受的兜底——比换档、比跳下一首都好。
				resumeRetried = true
				resume = 0
				delete(fallback, tier) // 这一档还没被判死，值得不带进度再试
			}
			continue
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
		p.listenSwitchLocked(song, info)
		p.mu.Unlock()
		p.listenSetPlaying()
		p.notifyChange()
		p.fetchLyric(song, seq)
	}()
}

// awaitLoaded 等引擎报出本次载入的结果。
//
// 引擎的载入是异步的：命令立刻返回，真正的结果（打开成功/失败）随后
// 以事件的形式到来。这里轮询到「file-loaded 成形」或「报错」为止；超时就按
// 失败处理，让调用方降档——总比卡住整条起播链好。
//
// 成功信号用 file-loaded 而不是「时长就位」：时长要等 demuxer 读出元数据，
// 有些不报时长的流永远读不出来，拿时长当成功就会把能播的流误判成坏档，
// 一路降档到超时，最后表现成「卡在加载然后自己跳下一曲」。
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
			if p.eng.LoadErrorFatal() {
				return fmt.Errorf("%w：%s", errEngineFatal, e)
			}
			return errors.New(e)
		}
		if p.eng.Loaded() {
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
func (p *Player) resolveWithFallback(song Song, want string, dep []string, seq uint64, fallback map[string]bool, allowAtmos bool) (*StreamInfo, string, error) {
	durationMs := int64(song.Interval * 1000)
	if durationMs < 0 {
		durationMs = 0
	}
	// 微云网盘曲目没有 QQ 音质档位可协商：直接按 file_id 取一条流，
	// 失败也不降档（换档对网盘文件没有意义）。
	if song.CloudFileID != "" {
		cr, ok := p.api.(cloudResolver)
		if !ok {
			return nil, "", errors.New("当前后端不支持微云播放")
		}
		info, err := cr.ResolveCloud(song.Mid, song.CloudFileID, song.CloudName, durationMs)
		if err != nil {
			return nil, "", err
		}
		if info == nil {
			return nil, "", errors.New("微云取流失败")
		}
		return info, "weiyun", nil
	}
	var lastErr error
	for _, tier := range fallbackChain(want, allowAtmos) {
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
		info, err := p.api.Resolve(song.Mid, "", song.SongType, tier, dep, durationMs)
		if err == nil && info != nil {
			if !allowAtmos && qmAtmosTier(info.Tier) {
				// 后端会自己沿 rank 往下协商：请求 master 也可能拿回
				// atmos51。开关关闭时按「这一档没成」处理，继续让位给
				// 下一档，绝不让全景声在用户不知情时上桌。
				lastErr = fmt.Errorf("服务端把回复降到了全景声档位 %s，但全景声回退已关闭", info.Tier)
				continue
			}
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

// fallbackChain 从高档到低档的回退链（引擎可解全部档位，这里按上游
// 的 rank 顺序逐级让位；换档由起播协程的 fallback 集合去重）。
//
// allowAtmos=false 时整条链跳过 7.1 / 5.1：它们既不该被自动选中，也
// 不该在 master/flac 失败后兜底。
func fallbackChain(want string, allowAtmos bool) []string {
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
	chain := order[at:]
	if allowAtmos {
		return append([]string(nil), chain...)
	}
	out := make([]string, 0, len(chain))
	for _, id := range chain {
		if !qmAtmosTier(id) {
			out = append(out, id)
		}
	}
	return out
}

// qmAtmosTier 判定档位是否属于「回退到全景声」开关管辖的 7.1 / 5.1。
func qmAtmosTier(id string) bool {
	for _, tier := range qmAtmosFallbackTiers {
		if tier == id {
			return true
		}
	}
	return false
}

var errStale = errors.New("已切换歌曲")

// errEngineFatal 标记「与音质档位无关」的引擎故障（引擎拒绝了载入命令、进程已关闭
// 已经没了）。继续降档、继续跳歌只会把整条队列白白吃光，player 据此收手报错。
var errEngineFatal = errors.New("播放引擎故障")

// maxAutoSkips 是失败后最多自动跳几首。
//
// 灰曲、下架、只有会员能听这类「这一首就是播不了」确实该自动往后走；但网络
// 抖动、后端还没就绪时不加限制，用户看到的就是「卡在加载，然后一首接一首
// 自己跳」。设上限停在当前曲，把原因显示出来，交给用户重试。
const maxAutoSkips = 3

func (p *Player) failWithErr(seq uint64, err error) {
	msg := err.Error()
	fatal := errors.Is(err, errEngineFatal)

	p.mu.Lock()
	if seq != p.playSeq {
		p.mu.Unlock()
		return
	}
	p.err = msg
	p.failStreak++
	next := -1
	if !fatal && p.failStreak <= maxAutoSkips && len(p.queue) > 1 {
		next = p.nextIndexLocked(true)
	}
	// 即将重试时保持 loading=true：startCurrent 会同步重置它，
	// 中间不留窗口给 tick 误触发又一次 Next(true)。
	if next < 0 {
		p.loading = false
		// 收手：引擎归零、stream 清空。上一首可能还在出声，界面与实际出声
		// 不能各说各话；stream=nil 也让「按播放重试」走完整起播链，而不是
		// 在空引擎上假装有在播。位置留着，界面还能看出卡在哪。
		p.playing = false
		p.stream = nil
		p.eng.Stop()
		p.listenResetLocked()
	}
	switch {
	case fatal:
		p.err = msg + "（与音质档位无关，已停止自动切歌；可按播放重试）"
	case p.failStreak > maxAutoSkips && len(p.queue) > 1:
		p.err = msg + fmt.Sprintf("（已连续 %d 首播不了，停止自动切歌；可按播放重试）", p.failStreak)
	}
	p.mu.Unlock()
	p.notifyChange()
	if next >= 0 {
		p.mu.Lock()
		p.index = next
		p.mu.Unlock()
		// 注意：这里不能用 playFrom——连跳计数是这条链自己的刹车，
		// 每次自动重试都清零的话，上限就永远不会生效了。
		p.startCurrent(0, true, "")
	}
}

// ===== 歌词 =====

func (p *Player) fetchLyric(song Song, seq uint64) {
	p.fetchLyricMode(song, seq, false)
}

// fetchLyricMode 拉取歌词。keepCurrent 为 true（网盘导入后刷新）时不把
// 已有歌词切成 loading，避免正在看的那一页闪一下转圈。
func (p *Player) fetchLyricMode(song Song, seq uint64, keepCurrent bool) {
	p.mu.Lock()
	p.lyricSeq++
	lyricSeq := p.lyricSeq
	if !keepCurrent || len(p.lyrics) == 0 {
		p.lyricState = "loading"
	}
	if !keepCurrent {
		// 换曲后歌词到达前，别把上一首的 asText 挂在新曲目的媒体元数据上。
		p.lyricAsText = ""
	}
	p.mu.Unlock()
	p.notifyChange()
	var src, trans, roma string
	var err error
	if song.CloudFileID != "" {
		// 网盘曲目没有 QQ 歌曲歌词可查，只能读后端刮削/导入的结果。
		if cf, ok := p.api.(cloudLyricFetcher); ok {
			src, trans, roma, err = cf.FetchCloudLyric(song.CloudKey)
		} else {
			err = errors.New("当前后端不支持网盘歌词")
		}
	} else {
		src, trans, roma, err = p.api.FetchLyric(song.Mid, true, true)
	}
	p.mu.Lock()
	if seq != p.playSeq || lyricSeq != p.lyricSeq {
		p.mu.Unlock()
		return
	}
	if err != nil || strings.TrimSpace(src) == "" {
		p.lyrics, p.qrc = nil, nil
		p.lyricFormat = ""
		p.lyricAsText = ""
		p.lyricState = "none"
		p.mu.Unlock()
		p.notifyChange()
		return
	}
	// 先按逐字解析：TTML 是网盘导入的自制逐字歌词，QRC 是 QQ 音乐
	// 的逐字格式。解析不出词级时间轴就按行级 LRC 走。
	//
	// 逐字歌词仍以「行级视图」喂给列表（滚动/索引/点击跳转那一套不用改），
	// 词级时间轴平行存在 p.qrc 里，按行号回查。
	if ql, format := parseWordLyric(src, trans, roma); len(ql) > 0 {
		p.lyricFormat = format
		p.lyrics = qrcToLines(ql)
		if HasWordTiming(ql) {
			p.qrc = ql
		} else {
			// 只有行时间、词时长全 0 的伪 QRC 仍按行级渲染，但保留
			// QRC 行本身，避免退回 ParseLrc 时把 [kana:] 行级数据丢掉。
			p.qrc = nil
			p.lyricFormat = ""
		}
	} else {
		p.qrc = nil
		p.lyricFormat = ""
		p.lyrics = ParseLrc(src, trans, roma)
	}
	if len(p.lyrics) == 0 {
		p.lyrics, p.qrc = nil, nil
		p.lyricFormat = ""
		p.lyricAsText = ""
		p.lyricState = "none"
	} else {
		p.lyricAsText = mprisLyricText(p.lyrics, p.mprisLrc)
		p.lyricState = "ok"
	}
	p.mu.Unlock()
	p.notifyChange()
}

// parseWordLyric 解析带词级时间轴的歌词：TTML 优先，其次 QRC。
// TTML 自己可能带翻译/音译，只有后端另外给了辅助歌词时才覆盖。
func parseWordLyric(src, trans, roma string) ([]QrcLine, string) {
	if ql := ParseTTML(src); len(ql) > 0 {
		if strings.TrimSpace(trans) != "" || strings.TrimSpace(roma) != "" {
			alignQrcAux(ql, trans, roma)
		}
		return ql, "ttml"
	}
	if ql := ParseQRC(src); len(ql) > 0 {
		alignQrcAux(ql, trans, roma)
		return ql, "qrc"
	}
	return nil, ""
}

// ReloadLyric 重新拉取当前曲目的歌词，供网盘导入/恢复歌词后立即刷新。
func (p *Player) ReloadLyric() {
	p.mu.Lock()
	song, ok := p.currentLocked()
	seq := p.playSeq
	p.mu.Unlock()
	if !ok {
		return
	}
	go p.fetchLyricMode(song, seq, true)
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

// LyricFormat 返回当前逐字歌词来源（qrc / ttml）；行级歌词返回空串。
func (p *Player) LyricFormat() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lyricFormat
}

// LyricAsText 返回系统媒体控件 xesam:asText 的载荷。歌词在拉取时已按当前
// 模式缓存为 LRC 或纯文本，这里只做快照读取。
func (p *Player) LyricAsText() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lyricAsText
}

// MprisLrc 报告 xesam:asText 是否使用带时间戳的 LRC（默认 true）。
func (p *Player) MprisLrc() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.mprisLrc
}

// SetMprisLrc 切换 xesam:asText 的格式：true = LRC，false = 纯文本。
// 只重排内存里已解析的行，不重新拉歌词；notify 让媒体中心补推一帧元数据。
func (p *Player) SetMprisLrc(on bool) {
	p.mu.Lock()
	if p.mprisLrc == on {
		p.mu.Unlock()
		return
	}
	p.mprisLrc = on
	p.lyricAsText = mprisLyricText(p.lyrics, p.mprisLrc)
	p.mu.Unlock()
	p.conf.Set("Lyrics.MprisLrc", on)
	p.notifyChange()
}

// RefreshMprisLyrics 按当前模式重算 asText。保留给「偏好已由外部写入」
// 的调用方；正常切换请用 SetMprisLrc。
func (p *Player) RefreshMprisLyrics() {
	p.mu.Lock()
	p.lyricAsText = mprisLyricText(p.lyrics, p.mprisLrc)
	p.mu.Unlock()
	p.notifyChange()
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

// SetLyricAuxMode 切换歌词辅助行：关闭 / 翻译优先 / 音译优先。
// 翻译与音译在首次拉取时都已拿到，切换不必重走网络。
func (p *Player) SetLyricAuxMode(mode string) {
	switch mode {
	case "off", "translation", "romanization":
	default:
		return
	}
	p.mu.Lock()
	if p.lyricAux == mode {
		p.mu.Unlock()
		return
	}
	p.lyricAux = mode
	p.mu.Unlock()
	p.conf.Set("Style.LyricAux", mode)
	// 兼容仍会读旧布尔键的代码/插件。
	p.conf.Set("Style.ShowTranslation", mode == "translation")
	p.notifyChange()
}

func (p *Player) LyricAuxMode() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lyricAux
}

// SetShowTranslation 是旧入口，保留为翻译档 / 关闭的二元兼容。
func (p *Player) SetShowTranslation(on bool) {
	if on {
		p.SetLyricAuxMode("translation")
		return
	}
	p.SetLyricAuxMode("off")
}

func (p *Player) ShowTranslation() bool {
	return p.LyricAuxMode() != "off"
}

func mprisLyricText(lines []LyricLine, lrc bool) string {
	if lrc {
		return LyricLinesToLRC(lines)
	}
	return LyricLinesToText(lines)
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
	// 网盘条目不是 QQ 曲库里的歌，收藏接口没有对应对象。
	if song.CloudFileID != "" {
		return
	}
	p.mu.Lock()
	_, was := p.loved[song.Mid]
	if was {
		delete(p.loved, song.Mid)
	} else {
		p.loved[song.Mid] = struct{}{}
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
				p.loved[song.Mid] = struct{}{}
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
// 位置直接读引擎时钟，不再用墙钟推算——旧实现必须自己累加
// 位置并处理 seek/换歌时的重锚定，误差会累积；引擎是唯一真相源。
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

	// 从引擎同步真实位置与时长（引擎的时钟 / 时长）
	pos := p.eng.Position()
	dur := p.eng.Duration(fallback)

	p.mu.Lock()
	p.pos = pos
	if dur > 0 {
		p.dur = dur
	}
	ended := p.eng.Ended()
	atEnd := p.dur > 0 && p.pos >= p.dur-0.05
	loading := p.loading
	p.mu.Unlock()

	// 进度节流落盘：切歌或跨 5 秒才真写，退出时另有 Close 兜底。
	p.sessionTickSave()
	p.listenSetPlaying()

	// 加载中不重复触发自动切歌：eof/atEnd 是上一首的残留状态——引擎的
	// end-file 已置 eof=true，但 startCurrent 的 goroutine 还在做 API
	// 解析、尚未调 OpenURL 清除它。每 200ms 的 tick 会反复误判为播完，
	// 导致连跳多首。loading 由 startCurrent 在同 goroutine 内同步置 true，
	// 不存在与 tick 的竞态。
	if (ended || atEnd) && !loading {
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

// Close 终止播放引擎。应用退出时调用，确保音频设备与解码线程被释放。
//
// 退出顺带把播放会话落盘：先停掉未到期的防抖计时器，再同步写一次——必须赶
// 在引擎关闭之前 flush，实时进度要从还活着的引擎里取。
func (p *Player) Close() {
	p.saveMu.Lock()
	if p.saveTimer != nil {
		p.saveTimer.Stop()
		p.saveTimer = nil
	}
	p.saveForce = false
	p.saveMu.Unlock()
	p.sessionSave(true)

	p.mu.Lock()
	p.playing = false
	p.loading = false
	p.listenResetLocked()
	// 退出时不受退避窗口限制：这是这一段的最后一次机会。
	p.listenRetryAt = time.Time{}
	eng := p.eng
	p.mu.Unlock()
	// 退出也要把本曲累计交出去：分片时代这条尾段本来就丢，现在整曲
	// 都押在这一次提交上。给发送一个有界的等待窗口，别让进程先走。
	p.listenDrain()
	deadline := time.Now().Add(listenCloseFlushWait)
	for time.Now().Before(deadline) {
		p.mu.Lock()
		// 失败会进入退避并把任务放回队列：那说明这次退出已经尽力，
		// 不该让用户为一次注定失败的请求等满整个窗口。
		pending := p.listenSending || (len(p.listenQueue) > 0 && p.listenRetryAt.IsZero())
		p.mu.Unlock()
		if !pending {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if eng != nil {
		eng.Close()
	}
	p.notifyChange()
}

// Boot 载入持久化音量、恢复上次会话（队列与进度）、挂上引擎观察回调并启动心跳。
func (p *Player) Boot() {
	p.mu.Lock()
	p.vol = p.conf.Float("Playing.Volume", 0.8)
	p.muted = p.conf.Bool("Playing.Muted", false)
	vol := p.vol
	if p.muted {
		vol = 0
	}
	if sv, ok := p.eng.(startVolumeSetter); ok {
		sv.SetStartVolume(vol)
	} else {
		p.eng.SetVolume(vol)
	}
	p.mu.Unlock()
	// 恢复退出前的播放列表与进度：只「就位」，不自动起播（见 session.go）。
	p.restoreSession()
	// 引擎的状态变化回调（audio 侧目前只做登记，见 Engine.OnObserve）：
	// 引擎一旦接上合并通知，进度条与歌词就能不等心跳立即跟上。
	p.eng.OnObserve(p.notifyChange)
	p.StartTicker()
}
