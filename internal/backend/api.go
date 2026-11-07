package backend

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// APIError 是 quaver-server 信封错误（{code,msg} + HTTP 状态）。
type APIError struct {
	Status int // HTTP 状态码
	Code   int // 信封里的 code（负数）
	Msg    string
}

func (e *APIError) Error() string {
	if e.Msg != "" {
		return e.Msg
	}
	return fmt.Sprintf("HTTP %d (code %d)", e.Status, e.Code)
}

// HTTPStatus 让播放器不用做类型断言也能区分可重试与确定性失败。
func (e *APIError) HTTPStatus() int { return e.Status }

// Unauthorized 报告 401（未登录/凭证过期）。
func Unauthorized(err error) bool {
	e, ok := err.(*APIError)
	return ok && e.Status == http.StatusUnauthorized
}

// Client 是 quaver-server 的 HTTP 客户端（信封 {code,msg,data}）。
type Client struct {
	Base string
	h    *http.Client
}

func NewClient(base string) *Client {
	return &Client{Base: base, h: &http.Client{Timeout: 20 * time.Second}}
}

func (c *Client) do(method, path string, body []byte) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequest(method, c.Base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.h.Do(req)
	if err != nil {
		return nil, &APIError{Msg: err.Error()}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, &APIError{Status: resp.StatusCode, Msg: err.Error()}
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, &APIError{Status: resp.StatusCode, Msg: fmt.Sprintf("HTTP %d: 响应解析失败", resp.StatusCode)}
	}
	if resp.StatusCode >= 400 || env.Code != 0 {
		return nil, &APIError{Status: resp.StatusCode, Code: env.Code, Msg: env.Msg}
	}
	return env.Data, nil
}

func (c *Client) get(path string, q url.Values) (json.RawMessage, error) {
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	return c.do(http.MethodGet, path, nil)
}

func (c *Client) postJSON(path string, body any) (json.RawMessage, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return c.do(http.MethodPost, path, b)
}

func (c *Client) getJSON(path string, qv url.Values, out any) error {
	raw, err := c.get(path, qv)
	if err != nil {
		return err
	}
	return decode(raw, out)
}

func (c *Client) postJSONInto(path string, body any, out any) error {
	raw, err := c.postJSON(path, body)
	if err != nil {
		return err
	}
	return decode(raw, out)
}

func q(params ...any) url.Values {
	v := url.Values{}
	for i := 0; i+1 < len(params); i += 2 {
		k, _ := params[i].(string)
		switch x := params[i+1].(type) {
		case string:
			if x != "" {
				v.Set(k, x)
			}
		case int:
			v.Set(k, strconv.Itoa(x))
		case int64:
			v.Set(k, strconv.FormatInt(x, 10))
		}
	}
	return v
}

// ===================== 模型 =====================

// SingerRef 歌手引用（raw 歌曲扁平结构里的 singer 数组项）。
type SingerRef struct {
	ID   int64  `json:"id"`
	Mid  string `json:"mid"`
	Name string `json:"name"`
	Pmid string `json:"pmid"`
}

// AlbumRef 专辑引用。
type AlbumRef struct {
	Mid  string `json:"mid"`
	Name string `json:"name"`
	Pmid string `json:"pmid"`
}

// Song 对齐上游扁平 Song 结构（mid/name/title/subtitle/singer/album/interval/type）。
type Song struct {
	Mid      string      `json:"mid"`
	ID       json.Number `json:"id"`
	Type     int         `json:"type"`
	Name     string      `json:"name"`
	Title    string      `json:"title"`
	Subtitle string      `json:"subtitle"`
	Singer   []SingerRef `json:"singer"`
	Album    *AlbumRef   `json:"album"`
	Interval int         `json:"interval"`
}

func (s Song) DisplayName() string {
	if s.Title != "" {
		return s.Title
	}
	return s.Name
}

func (s Song) Artists() string {
	out := ""
	for i, a := range s.Singer {
		if i > 0 {
			out += "/"
		}
		out += a.Name
	}
	return out
}

func (s Song) AlbumName() string {
	if s.Album != nil {
		return s.Album.Name
	}
	return ""
}

func (s Song) Duration() float64 { return float64(s.Interval) }

// SonglistSummary 歌单摘要（compat songlistItem 形状）。
type SonglistSummary struct {
	ID        int64  `json:"id"`
	DirID     int64  `json:"dirid"`
	Title     string `json:"title"`
	Picurl    string `json:"picurl"`
	Desc      string `json:"desc"`
	Songnum   int64  `json:"songnum"`
	Listennum int64  `json:"listennum"`
	Nickname  string `json:"nickname"`
}

// Creator 歌单创建者。
type Creator struct {
	Musicid    int64  `json:"musicid"`
	Nick       string `json:"nick"`
	Headurl    string `json:"headurl"`
	EncryptUin string `json:"encrypt_uin"`
}

// SonglistDetail 歌单详情（info+songs+hasmore+total），/user/liked 同形状。
type SonglistDetail struct {
	Info    SonglistSummary `json:"info"`
	Creator Creator         `json:"creator"`
	Songs   []Song          `json:"songs"`
	Hasmore int64           `json:"hasmore"`
	Total   int64           `json:"total"`
}

// Lyric 歌词（服务端已解密；trans=1 带翻译，roma=1 带音译）。
type Lyric struct {
	Songid int64  `json:"songid"`
	Lyric  string `json:"lyric"`
	Trans  string `json:"trans"`
	Roma   string `json:"roma"`
}

// RecommendSonglists /recommend/songlist 响应。
type RecommendSonglists struct {
	Songlists []SonglistSummary `json:"songlists"`
}

// AISonglists /recommend/ai-songlist 响应。
type AISonglists struct {
	Songlists []SonglistSummary `json:"songlists"`
	Total     int64             `json:"total"`
	HasMore   bool              `json:"hasmore"`
}

// GuessSongs /recommend/guess 响应（一轮约 5 首）。
type GuessSongs struct {
	Songs  []Song `json:"songs"`
	Rounds int    `json:"rounds"`
}

// ToplistSummary 首页榜单卡片（/top/category 归一化后的形状）。
type ToplistSummary struct {
	ID               int64  `json:"id"`
	Title            string `json:"title"`
	Picurl           string `json:"picurl"`
	Desc             string `json:"desc"`
	Songnum          int64  `json:"songnum"`
	Listennum        int64  `json:"listennum"`
	Period           string `json:"period"`
	UpdateType       int    `json:"update_type"`
	UpdateTime       string `json:"update_time"`
	UpdateTips       string `json:"update_tips"`
	HistorySupported bool   `json:"history_supported"`
}

// ToplistGroup 榜单分类。
type ToplistGroup struct {
	ID       int64            `json:"id"`
	Name     string           `json:"name"`
	Toplists []ToplistSummary `json:"toplists"`
}

// ToplistCategories /top/category 响应。
type ToplistCategories struct {
	Groups []ToplistGroup `json:"groups"`
}

// ToplistDetail /top/{id}/detail 响应；info 与歌单摘要同形。
type ToplistDetail struct {
	Info    SonglistSummary `json:"info"`
	Songs   []Song          `json:"songs"`
	Total   int64           `json:"total"`
	Hasmore bool            `json:"hasmore"`
}

// Tier 音质档位。
type Tier struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	Rank      int    `json:"rank"`
	HiRes     bool   `json:"hi_res"`
	Encrypted bool   `json:"encrypted"`
	Mime      string `json:"mime"`
	Ext       string `json:"ext"`
	Requires  int    `json:"requires"`
	Locked    bool   `json:"locked"`
}

// FavSonglists /user/fav-songlists 响应（我收藏的歌单）。
type FavSonglists struct {
	Total     int64             `json:"total"`
	Number    int64             `json:"number"`
	Hasmore   bool              `json:"hasmore"`
	Playlists []SonglistSummary `json:"playlists"`
}

// Tiers /stream/tiers 响应。
type Tiers struct {
	Membership      int    `json:"membership"`
	MembershipLabel string `json:"membership_label"`
	Tiers           []Tier `json:"tiers"`
	AllTiers        []Tier `json:"all_tiers"`
	Max             string `json:"max"`
}

// Resolved /stream/resolve 响应。
type Resolved struct {
	Token         string `json:"token"`
	Path          string `json:"path"`
	Tier          string `json:"tier"`
	TierLabel     string `json:"tier_label"`
	Degraded      bool   `json:"degraded"`
	Mime          string `json:"mime"`
	Size          int64  `json:"size"`
	Filename      string `json:"filename"`
	Encrypted     bool   `json:"encrypted"`
	RequestedTier string `json:"requested_tier"`
}

// MusicCacheStatus 是 GET /cache/music/status 的响应。
type MusicCacheStatus struct {
	Items      int   `json:"items"`
	Bytes      int64 `json:"bytes"`
	LimitBytes int64 `json:"limit_bytes"`
}

// WeiyunStatus 是 GET /weiyun/status 的响应。configured 表示微云 MCP 令牌
// 已保存（独立于 QQ 音乐登录）；has_qq_credential 是网盘列表来源的前提。
type WeiyunStatus struct {
	Configured      bool   `json:"configured"`
	Endpoint        string `json:"endpoint"`
	HasQQCredential bool   `json:"hasQqCredential"`
}

// WeiyunAuthStart 是 POST /weiyun/auth/start 的响应：URL 指向微云授权页，
// UI 打开它后轮询 /weiyun/auth/poll 换取令牌。
type WeiyunAuthStart struct {
	URL          string `json:"url"`
	ExpiresInSec int    `json:"expiresInSec"`
}

// CloudStats 是网盘库的汇总。
type CloudStats struct {
	Total     int `json:"total"`
	Playable  int `json:"playable"`
	WithCover int `json:"withCover"`
	WithLyric int `json:"withLyric"`
	Scraped   int `json:"scraped"`
}

// CloudScrapeState 是刮削任务状态（GET /cloud/scrape）。
type CloudScrapeState struct {
	Running    bool   `json:"running"`
	Total      int    `json:"total"`
	Processed  int    `json:"processed"`
	Matched    int    `json:"matched"`
	Unmatched  int    `json:"unmatched"`
	Failed     int    `json:"failed"`
	StartedAt  int64  `json:"startedAt"`
	FinishedAt int64  `json:"finishedAt"`
	LastError  string `json:"lastError"`
}

// CloudItem 是 GET /cloud/library 里的一条网盘曲目。
//
// Key 是后端按账号与 file_id 算出的稳定条目键（sha1），自定义封面/歌词与
// 元数据都以它命名；FileId 是微云下载用的 file_id。Song 是上游匹配到的
// QQ 音乐曲目（可能为空），曲长/歌手 mid/专辑 mid 都从它取。
type CloudItem struct {
	Key           string `json:"key"`
	FileID        string `json:"fileId"`
	FileName      string `json:"fileName"`
	AddTime       int64  `json:"addTime"`
	Matched       int    `json:"matched"`
	Playable      bool   `json:"playable"`
	Title         string `json:"title"`
	Artist        string `json:"artist"`
	Album         string `json:"album"`
	SingerMid     string `json:"singerMid"`
	AlbumMid      string `json:"albumMid"`
	AlbumPmid     string `json:"albumPmid"`
	CoverQq       bool   `json:"coverQq"`
	LyricQq       bool   `json:"lyricQq"`
	ScrapedAt     int64  `json:"scrapedAt"`
	EditedMeta    bool   `json:"editedMeta"`
	CoverManual   bool   `json:"coverManual"`
	LyricImport   bool   `json:"lyricImport"`
	ImportFormat  string `json:"importFormat"`
	HasCover      bool   `json:"hasCover"`
	HasLyric      bool   `json:"hasLyric"`
	PlaybackState string `json:"playbackState"`
	PlaybackError string `json:"playbackError"`
	Song          *Song  `json:"song"`
}

// CloudLibrary 是 GET /cloud/library 的响应。
type CloudLibrary struct {
	Items  []CloudItem      `json:"items"`
	Count  int              `json:"count"`
	Stats  CloudStats       `json:"stats"`
	Scrape CloudScrapeState `json:"scrape"`
}

// CloudLyric 是 GET /cloud/lyric/{key} 的响应；source=qq 时是后端刮削到的
// QRC/LRC，source=import 时是用户导入的明文歌词。
type CloudLyric struct {
	Lyric  string `json:"lyric"`
	Trans  string `json:"trans"`
	Roma   string `json:"roma"`
	Source string `json:"source"`
	Format string `json:"format"`
}

// SoundPowerReport 一段真实听歌时长（秒）。stream_token 让后端复用当次
// 播放凭证；audio_time_ms 是歌曲总时长，与本次实际听了多久无关。
type SoundPowerReport struct {
	StreamToken        string `json:"stream_token"`
	SongID             int64  `json:"song_id"`
	Mid                string `json:"mid"`
	MediaMid           string `json:"media_mid"`
	AudioTimeMS        int64  `json:"audio_time_ms"`
	PlayDurationSecond int    `json:"play_duration_seconds"`
	SongType           int    `json:"song_type"`
	SingerID           int64  `json:"singer_id"`
	CoSinger           string `json:"co_singer"`
}

// SoundPowerResult true 表示上游已把这段时间计入当日音响力。
type SoundPowerResult struct {
	OK       bool   `json:"ok"`
	Duration int    `json:"duration"`
	Reason   string `json:"reason,omitempty"`
}

// SearchResults 归一化搜索结果。
type SearchResults struct {
	Song      []Song            `json:"song"`
	Songlists []SonglistSummary `json:"songlist"`
	TotalNum  int64             `json:"total_num"`
	Nextpage  int64             `json:"nextpage"`
}

// SingerBase 歌手主页基本信息（/singer/{mid}/info 的 base_info）。
type SingerBase struct {
	Name         string `json:"name"`
	Avatar       string `json:"avatar"`
	EncryptedUin string `json:"encrypted_uin"`
	UserType     int    `json:"user_type"`
	IsSinger     bool   `json:"is_singer"`
}

// SingerProfile 歌手百科详情（/singer/{mid}/desc，服务端已规整 area/identity）。
type SingerProfile struct {
	Name        string `json:"name"`
	Mid         string `json:"mid"`
	Pic         string `json:"pic"`
	BigPic      string `json:"big_pic"`
	Desc        string `json:"desc"`
	ForeignName string `json:"foreign_name"`
	Birthday    string `json:"birthday"`
	Area        string `json:"area"`
	Identity    string `json:"identity"`
}

// SingerSongs 歌手歌曲（/singer/{mid}/songs；order 1=热门 2=最新发布）。
// song_list 与歌单详情的 songs 同形状（上游扁平 Song）。
type SingerSongs struct {
	SingerMid string `json:"singer_mid"`
	TotalNum  int64  `json:"total_num"`
	SongList  []Song `json:"song_list"`
}

// SingerAlbum 歌手专辑条目（/singer/{mid}/albums，服务端已平铺驼峰归一）。
type SingerAlbum struct {
	Mid        string `json:"mid"`
	Pmid       string `json:"pmid"`
	Name       string `json:"name"`
	TranName   string `json:"tran_name"`
	AlbumType  string `json:"album_type"`
	TimePublic string `json:"time_public"`
	SingerName string `json:"singer_name"`
	TotalNum   int64  `json:"total_num"`
}

// SingerAlbums 歌手专辑列表。
type SingerAlbums struct {
	SingerMid string        `json:"singer_mid"`
	Total     int64         `json:"total"`
	AlbumList []SingerAlbum `json:"album_list"`
}

// AlbumInfo 专辑详情（/album/{value}/detail 的 album）。
type AlbumInfo struct {
	ID         json.Number `json:"id"`
	Mid        string      `json:"mid"`
	Pmid       string      `json:"pmid"`
	Name       string      `json:"name"`
	Subtitle   string      `json:"subtitle"`
	Desc       string      `json:"desc"`
	AlbumType  string      `json:"album_type"`
	TimePublic string      `json:"time_public"`
	Genre      string      `json:"genre"`
	Language   string      `json:"language"`
	Singer     string      `json:"singer"`
}

// AlbumSinger 专辑详情里的歌手项。上游 singerList 的键名不稳
// （name / singer_name / singerMID 各处不同），做多别名容错。
type AlbumSinger struct {
	Mid  string `json:"mid"`
	Name string `json:"name"`
}

func (s *AlbumSinger) UnmarshalJSON(b []byte) error {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(b, &m); err != nil {
		return err
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k]; ok {
				var out string
				if json.Unmarshal(v, &out) == nil && out != "" {
					return out
				}
			}
		}
		return ""
	}
	s.Mid = str("mid", "singer_mid", "singerMID", "singerMid")
	s.Name = str("name", "singer_name", "singerName")
	return nil
}

// AlbumDetail /album/{value}/detail。
type AlbumDetail struct {
	Album   AlbumInfo     `json:"album"`
	Company string        `json:"company"`
	Singers []AlbumSinger `json:"singers"`
}

// AlbumSongs /album/{value}/songs（song_list 与歌单详情同形状）。
type AlbumSongs struct {
	AlbumMid string `json:"album_mid"`
	TotalNum int64  `json:"total_num"`
	SongList []Song `json:"song_list"`
}

// UserMe /user/me。
type UserMe struct {
	BaseInfo struct {
		Name         string `json:"name"`
		Avatar       string `json:"avatar"`
		EncryptedUin string `json:"encrypted_uin"`
		UserType     int    `json:"user_type"`
		IsSinger     bool   `json:"is_singer"`
	} `json:"base_info"`
}

// LoginStatus /login/status。
type LoginStatus struct {
	LoggedIn       bool   `json:"logged_in"`
	Expired        bool   `json:"expired"`
	CredentialMode string `json:"credential_mode"`
}

// QRCode /login/qrcode/{channel}。
type QRCode struct {
	QRType     string `json:"qr_type"`
	Identifier string `json:"identifier"`
	Mimetype   string `json:"mimetype"`
	Data       string `json:"data"`
	Img        string `json:"img"` // data URL
}

// QRStatus /login/qrcode/{channel}/status：event 1=已扫 2=待确认 3=过期 4=拒绝，done=完成。
type QRStatus struct {
	Event int    `json:"event"`
	Done  bool   `json:"done"`
	Error string `json:"error"`
}

// QQQuickAccount 是本机 QQ 客户端检测到的一个在线账号；uin 用于一键登录。
type QQQuickAccount struct {
	UIN      int64  `json:"uin"`
	Nickname string `json:"nickname"`
	Avatar   string `json:"avatar"`
}

// QQQuickAvailability 是 GET /login/qq-quick 的响应：available=false 表示
// 本机没有检测到已登录的 QQ 客户端（不是错误，UI 直接降级为二维码登录）。
type QQQuickAvailability struct {
	Available bool   `json:"available"`
	UIN       int64  `json:"uin"`
	Nickname  string `json:"nickname"`
	Avatar    string `json:"avatar"`
}

// VIPInfo 是后端稳定的会员权益契约。svip=超级会员、identity.huge_vip=豪华绿钻、
// identity.vip=绿钻；它们是各自独立且已经按到期时间过滤的有效标志，不是档位数值。
type VIPInfo struct {
	Svip      int         `json:"svip"`
	SvipStart string      `json:"svip_start"`
	SvipEnd   string      `json:"svip_end"`
	Identity  VIPIdentity `json:"identity"`
	Userinfo  VIPUserinfo `json:"userinfo"`
}

// VIPIdentity 是绿钻/豪华绿钻权益；Level 只是成长等级，不代表会员档位。
type VIPIdentity struct {
	Vip          int    `json:"vip"`
	VipStart     string `json:"vip_start"`
	VipEnd       string `json:"vip_end"`
	HugeVip      int    `json:"huge_vip"`
	HugeVipStart string `json:"huge_vip_start"`
	HugeVipEnd   string `json:"huge_vip_end"`
	Level        int    `json:"level"`
}

// VIPUserinfo 是会员页的辅助信息。
type VIPUserinfo struct {
	Expire string `json:"expire"`
	Score  int    `json:"score"`
}

// ===================== 端点 =====================

func (c *Client) LoginStatus() (LoginStatus, error) {
	var out LoginStatus
	err := c.getJSON("/login/status", nil, &out)
	return out, err
}

// LoginQQQuickDetect 探测本机 QQ 客户端的快速登录可用性。
// available=false 不是错误：只有真正的通信故障才返回 error。
func (c *Client) LoginQQQuickDetect() (QQQuickAvailability, error) {
	var out QQQuickAvailability
	err := c.getJSON("/login/qq-quick", nil, &out)
	return out, err
}

// LoginQQQuick 用本机 QQ 的 clientkey 一键登录。QQ 客户端中途退出/拒绝时
// 后端返回 DataError，这里原样透传给 UI 展示。
func (c *Client) LoginQQQuick(uin int64) error {
	_, err := c.postJSON("/login/qq-quick", map[string]any{"uin": uin})
	return err
}

func (c *Client) Logout() error {
	_, err := c.postJSON("/login/logout", nil)
	return err
}

func (c *Client) UserMe() (UserMe, error) {
	var out UserMe
	err := c.getJSON("/user/me", nil, &out)
	return out, err
}

func (c *Client) UserVIP() (VIPInfo, error) {
	var out VIPInfo
	err := c.getJSON("/user/vip", nil, &out)
	return out, err
}

func (c *Client) Liked(page, num int) (SonglistDetail, error) {
	var out SonglistDetail
	err := c.getJSON("/user/liked", q("page", page, "num", num), &out)
	return out, err
}

func (c *Client) CreatedSonglists() (struct {
	Total     int64             `json:"total"`
	Playlists []SonglistSummary `json:"playlists"`
	Hasmore   bool              `json:"hasmore"`
}, error) {
	var out struct {
		Total     int64             `json:"total"`
		Playlists []SonglistSummary `json:"playlists"`
		Hasmore   bool              `json:"hasmore"`
	}
	err := c.getJSON("/user/created-songlists", nil, &out)
	return out, err
}

// FavSonglists 我收藏的歌单（他人创建的）。需登录。
func (c *Client) FavSonglists(page, num int) (FavSonglists, error) {
	var out FavSonglists
	err := c.getJSON("/user/fav-songlists", q("page", page, "num", num), &out)
	return out, err
}

func (c *Client) StreamTiers() (Tiers, error) {
	var out Tiers
	err := c.getJSON("/stream/tiers", nil, &out)
	return out, err
}

// ResolveStream 解析播放流。tier 为档位 id 或 "auto"；deprioritize 降权档位；
// durationMs 是上游曲长提示，后端据此把中继限速定在该曲 ≈3× 实时码率
// （缺省时按整曲 ≥2 分钟兜底，高码率档会被压得过慢）。cacheSeed 是本地缓存
// 前端半种：非空时后端先查本地缓存命中（cached=true 的 path 指向缓存流），
// 未命中则回源并异步落盘缓存。
func (c *Client) ResolveStream(mid, mediaMid string, songType int, tier string, auto bool, deprioritize []string, durationMs int64, cacheSeed string) (Resolved, error) {
	if deprioritize == nil {
		deprioritize = []string{}
	}
	if durationMs < 0 {
		durationMs = 0
	}
	var out Resolved
	body := map[string]any{
		"mid": mid, "media_mid": mediaMid, "song_type": songType,
		"tier": tier, "auto": auto, "deprioritize": deprioritize,
		"duration_ms": durationMs,
	}
	if cacheSeed != "" {
		body["cache_seed"] = cacheSeed
	}
	err := c.postJSONInto("/stream/resolve", body, &out)
	return out, err
}

// MusicCacheStatus 查询本地音乐缓存占用。
func (c *Client) MusicCacheStatus() (MusicCacheStatus, error) {
	var out MusicCacheStatus
	err := c.getJSON("/cache/music/status", nil, &out)
	return out, err
}

// ClearMusicCache 清空本地音乐缓存（索引、所有分片与正在使用的读取句柄）。
func (c *Client) ClearMusicCache() (MusicCacheStatus, error) {
	var out MusicCacheStatus
	err := c.postJSONInto("/cache/music/clear", map[string]any{}, &out)
	return out, err
}

// SetMusicCacheLimit 设置缓存上限（字节）；0 表示回到后端默认上限。
// 超限时后端按最近最少使用（按写入时间）淘汰到上限以内。
func (c *Client) SetMusicCacheLimit(bytes int64) (MusicCacheStatus, error) {
	var out MusicCacheStatus
	err := c.postJSONInto("/cache/music/limit", map[string]any{"bytes": bytes}, &out)
	return out, err
}

// WeiyunStatus 查询微云令牌与 QQ 登录态。
func (c *Client) WeiyunStatus() (WeiyunStatus, error) {
	var out WeiyunStatus
	err := c.getJSON("/weiyun/status", nil, &out)
	return out, err
}

// WeiyunAuthStart 发起一次微云网页授权，返回需要打开的授权 URL。
func (c *Client) WeiyunAuthStart() (WeiyunAuthStart, error) {
	var out WeiyunAuthStart
	err := c.postJSONInto("/weiyun/auth/start", map[string]any{}, &out)
	return out, err
}

// WeiyunAuthPoll 轮询一次授权结果。用户还没在网页上确认时后端返回 409，
// 调用方用 WeiyunAuthPending 判断而不是把它当成失败。
func (c *Client) WeiyunAuthPoll() error {
	_, err := c.postJSON("/weiyun/auth/poll", map[string]any{})
	return err
}

// WeiyunAuthPending 报告轮询错误是否只是「网页上还没确认」。
func WeiyunAuthPending(err error) bool {
	e, ok := err.(*APIError)
	return ok && e.Status == http.StatusConflict
}

// WeiyunAuthSet 直接保存用户手动粘贴的微云令牌。
func (c *Client) WeiyunAuthSet(token, mcpURL string) (WeiyunStatus, error) {
	var out struct {
		Configured bool `json:"configured"`
	}
	err := c.postJSONInto("/weiyun/auth", map[string]any{
		"token": token, "mcpUrl": mcpURL,
	}, &out)
	if err != nil {
		return WeiyunStatus{}, err
	}
	return WeiyunStatus{Configured: out.Configured}, nil
}

// WeiyunAuthClear 清除已保存的微云令牌。
func (c *Client) WeiyunAuthClear() (WeiyunStatus, error) {
	var out struct {
		Configured bool `json:"configured"`
	}
	raw, err := c.do(http.MethodDelete, "/weiyun/auth", nil)
	if err != nil {
		return WeiyunStatus{}, err
	}
	if err := decode(raw, &out); err != nil {
		return WeiyunStatus{}, err
	}
	return WeiyunStatus{Configured: out.Configured}, nil
}

// CloudLibrary 拉取完整网盘库（后端会合并 QQ 云盘匹配结果与本地刮削状态）。
// 后端最多翻 10 页上游列表，默认 20 秒超时在文件多时会不够，这里单独放宽。
func (c *Client) CloudLibrary() (CloudLibrary, error) {
	var out CloudLibrary
	slow := &Client{Base: c.Base, h: &http.Client{Timeout: 90 * time.Second}}
	raw, err := slow.get("/cloud/library", nil)
	if err != nil {
		return out, err
	}
	err = decode(raw, &out)
	return out, err
}

// CloudScrapeStatus 查询刮削任务进度。
func (c *Client) CloudScrapeStatus() (CloudScrapeState, error) {
	var out CloudScrapeState
	err := c.getJSON("/cloud/scrape", nil, &out)
	return out, err
}

// CloudScrapeStart 启动刮削。keys 非空时只刮这些条目，all/force 控制是否
// 重刮已完成的条目。
func (c *Client) CloudScrapeStart(keys []string, all, force bool) (bool, error) {
	if keys == nil {
		keys = []string{}
	}
	var out struct {
		Started bool `json:"started"`
	}
	err := c.postJSONInto("/cloud/scrape", map[string]any{
		"keys": keys, "all": all, "force": force,
	}, &out)
	return out.Started, err
}

// CloudMetaSet 覆盖网盘条目的曲名/歌手/专辑（手动编辑后不再被刮削覆盖）。
func (c *Client) CloudMetaSet(key, title, artist, album string) error {
	_, err := c.postJSON("/cloud/meta/set", map[string]any{
		"key": key, "title": title, "artist": artist, "album": album,
	})
	return err
}

// CloudPlayback 记录条目的播放状态（matched / resolved / blocked）。
func (c *Client) CloudPlayback(key, state, errMsg string) error {
	_, err := c.postJSON("/cloud/playback", map[string]any{
		"key": key, "state": state, "error": errMsg,
	})
	return err
}

// CloudLyricImport 导入用户本地歌词文件（明文 LRC/QRC/TTML，不做解密）。
func (c *Client) CloudLyricImport(key, name string, data []byte) (string, error) {
	var out struct {
		Format string `json:"format"`
	}
	err := c.postJSONInto("/cloud/lyric/import", map[string]any{
		"key": key, "name": name, "data": base64.StdEncoding.EncodeToString(data),
	}, &out)
	return out.Format, err
}

// CloudLyricRevert 撤销导入歌词，恢复自动刮削的歌词。
func (c *Client) CloudLyricRevert(key string) error {
	_, err := c.do(http.MethodDelete, "/cloud/lyric/import/"+url.PathEscape(key), nil)
	return err
}

// CloudCoverImport 导入用户本地封面图片（jpg/png/gif/webp）。
func (c *Client) CloudCoverImport(key, name string, data []byte) error {
	_, err := c.postJSON("/cloud/cover/import", map[string]any{
		"key": key, "name": name, "data": base64.StdEncoding.EncodeToString(data),
	})
	return err
}

// CloudLyric 取网盘条目的歌词（刮削结果优先，其次用户导入）。
func (c *Client) CloudLyric(key string) (CloudLyric, error) {
	var out CloudLyric
	err := c.getJSON("/cloud/lyric/"+url.PathEscape(key), nil, &out)
	return out, err
}

// CloudCoverURL 拼网盘条目自定义封面地址。
func (c *Client) CloudCoverURL(key string) string {
	if key == "" {
		return ""
	}
	return c.Base + "/cloud/cover/" + url.PathEscape(key)
}

// ResolveCloudStream 解析微云网盘曲目的播放流。云文件不走 QQ 音质协商，
// 直接在 /stream/resolve 里带 cloud_file_id，复用后端的令牌与缓存链路。
func (c *Client) ResolveCloudStream(mid, fileID, name string, durationMs int64, cacheSeed string) (Resolved, error) {
	if durationMs < 0 {
		durationMs = 0
	}
	body := map[string]any{
		"mid": mid, "tier": "weiyun", "auto": true,
		"cloud_file_id": fileID, "duration_ms": durationMs,
	}
	if cacheSeed != "" {
		body["cache_seed"] = cacheSeed
	}
	path := "/stream/resolve"
	if name != "" {
		path += "?name=" + url.QueryEscape(name)
	}
	var out Resolved
	err := c.postJSONInto(path, body, &out)
	return out, err
}

// ReportListenTime 提交 QQ 音乐音响力物理流水。确定性失败（未登录、
// 参数无效、上游明确拒绝）返回 ok=false，调用方不应重试。
func (c *Client) ReportListenTime(report SoundPowerReport) (SoundPowerResult, error) {
	var out SoundPowerResult
	err := c.postJSONInto("/sound-power/report", report, &out)
	return out, err
}

// Lyric 取歌词。qrc=true 时向上游要逐字（QRC）歌词——上游会把它塞在
// 同一个 Lyric 字段里（XML 信封形态，服务端已解密），歌词没有逐字
// 时间轴时上游自动回落到普通 LRC，所以这个开关总是安全可开的。
// trans/roma 分别要翻译与音译；两者可同时请求，由播放器按档位挑选。
func (c *Client) Lyric(mid string, trans, roma, qrc bool) (Lyric, error) {
	t, r, qc := "", "", ""
	if trans {
		t = "1"
	}
	if roma {
		r = "1"
	}
	if qrc {
		qc = "1"
	}
	var out Lyric
	err := c.getJSON("/song/"+mid+"/lyric", q("trans", t, "roma", r, "qrc", qc), &out)
	return out, err
}

func (c *Client) SonglistDetail(id int64, page, num int) (SonglistDetail, error) {
	var out SonglistDetail
	err := c.getJSON("/songlist/"+strconv.FormatInt(id, 10)+"/detail", q("page", page, "num", num), &out)
	return out, err
}

func (c *Client) SonglistFav(id int64, fav bool) error {
	if fav {
		_, err := c.postJSON("/songlist/"+strconv.FormatInt(id, 10)+"/like", nil)
		return err
	}
	_, err := c.do(http.MethodDelete, "/songlist/"+strconv.FormatInt(id, 10)+"/like", nil)
	return err
}

// SonglistAddSong 加歌到自建歌单。songType 是写侧枚举（读侧 type - 1）。
func (c *Client) SonglistAddSong(dirid, tid, songID, songType int64) error {
	body := map[string]any{"song_id": songID, "song_type": songType, "tid": tid}
	_, err := c.postJSON("/songlist/"+strconv.FormatInt(dirid, 10)+"/songs", body)
	return err
}

// SonglistDelSong 从自建歌单移除歌曲。songType 是写侧枚举（读侧 type - 1）。
func (c *Client) SonglistDelSong(dirid, tid, songID, songType int64) error {
	b, err := json.Marshal(map[string]any{"song_id": songID, "song_type": songType, "tid": tid})
	if err != nil {
		return err
	}
	_, err = c.do(http.MethodDelete, "/songlist/"+strconv.FormatInt(dirid, 10)+"/songs", b)
	return err
}

// SonglistFavCheck 返回已收藏歌单 ID 集合。
func (c *Client) SonglistFavCheck() ([]int64, error) {
	var out struct {
		IDs []int64 `json:"ids"`
	}
	err := c.getJSON("/songlist/fav/check", nil, &out)
	return out.IDs, err
}

// SongLike 收藏/取消收藏。songType 是读侧枚举，写侧要减一（这里由调用方换算）。
func (c *Client) SongLike(songID int64, writeType int64, like bool) error {
	body := map[string]any{"song_id": songID, "song_type": writeType}
	path := "/song/unlike"
	if like {
		path = "/song/like"
	}
	_, err := c.postJSON(path, body)
	return err
}

// SingerInfo 歌手主页基本信息。
func (c *Client) SingerInfo(mid string) (SingerBase, error) {
	var out struct {
		BaseInfo SingerBase `json:"base_info"`
	}
	err := c.getJSON("/singer/"+mid+"/info", nil, &out)
	return out.BaseInfo, err
}

// SingerDesc 歌手百科详情（简介/外文名/生日/地区/立绘）。
func (c *Client) SingerDesc(mid string) (SingerProfile, error) {
	var out SingerProfile
	err := c.getJSON("/singer/"+mid+"/desc", nil, &out)
	return out, err
}

// SingerSongs 歌手歌曲。order 必须显式传：1=按热度（热门），2=按发行时间倒序（最新发布）。
func (c *Client) SingerSongs(mid string, page, num, order int) (SingerSongs, error) {
	var out SingerSongs
	err := c.getJSON("/singer/"+mid+"/songs", q("page", page, "num", num, "order", order), &out)
	return out, err
}

// SingerAlbums 歌手专辑。
func (c *Client) SingerAlbums(mid string, page, num int) (SingerAlbums, error) {
	var out SingerAlbums
	err := c.getJSON("/singer/"+mid+"/albums", q("page", page, "num", num), &out)
	return out, err
}

// AlbumDetail 专辑详情。value 是专辑 mid。
func (c *Client) AlbumDetail(value string) (AlbumDetail, error) {
	var out AlbumDetail
	err := c.getJSON("/album/"+value+"/detail", nil, &out)
	return out, err
}

// AlbumSongs 专辑歌曲。value 是专辑 mid。
func (c *Client) AlbumSongs(value string, page, num int) (AlbumSongs, error) {
	var out AlbumSongs
	err := c.getJSON("/album/"+value+"/songs", q("page", page, "num", num), &out)
	return out, err
}

func (c *Client) Search(keyword string, typ, page, num int) (SearchResults, error) {
	var out SearchResults
	err := c.getJSON("/search", q("keyword", keyword, "type", typ, "page", page, "num", num), &out)
	return out, err
}

func (c *Client) SearchHotkey() ([]string, error) {
	raw, err := c.get("/search/hotkey", nil)
	if err != nil {
		return nil, err
	}
	// 上游透传形状：{vec_hotkey:[{query,...}]}；容错 {hotkey:[...]}
	var flexible struct {
		VecHotkey []struct {
			Query string `json:"query"`
			K     string `json:"k"`
			N     string `json:"n"`
		} `json:"vec_hotkey"`
		Hotkey []struct {
			K string `json:"k"`
			N string `json:"n"`
		} `json:"hotkey"`
	}
	if err := json.Unmarshal(raw, &flexible); err != nil {
		return nil, nil
	}
	out := make([]string, 0, len(flexible.VecHotkey)+len(flexible.Hotkey))
	for _, h := range flexible.VecHotkey {
		if h.Query != "" {
			out = append(out, h.Query)
		}
	}
	for _, h := range flexible.Hotkey {
		if h.K != "" {
			out = append(out, h.K)
		} else if h.N != "" {
			out = append(out, h.N)
		}
	}
	return out, nil
}

func (c *Client) RecommendSonglists(page, num int) (RecommendSonglists, error) {
	var out RecommendSonglists
	err := c.getJSON("/recommend/songlist", q("page", page, "num", num), &out)
	return out, err
}

// RecommendAISonglists 取 AI 歌单；失败时首页块单独降级，不影响其他推荐。
func (c *Client) RecommendAISonglists(page, num int) (AISonglists, error) {
	var out AISonglists
	err := c.getJSON("/recommend/ai-songlist", q("page", page, "num", num), &out)
	return out, err
}

func (c *Client) RecommendNewsong() (struct {
	Songs []Song `json:"songs"`
}, error) {
	var out struct {
		Songs []Song `json:"songs"`
	}
	err := c.getJSON("/recommend/newsong", q("type", 5), &out)
	return out, err
}

func (c *Client) RecommendDaily() (SonglistDetail, error) {
	var out SonglistDetail
	err := c.getJSON("/recommend/daily", q("page", 1, "num", 100), &out)
	return out, err
}

func (c *Client) RecommendGuess(rounds int) (GuessSongs, error) {
	var out GuessSongs
	err := c.getJSON("/recommend/guess", q("rounds", rounds), &out)
	return out, err
}

// ToplistCategories 首页排行榜分组。
func (c *Client) ToplistCategories() (ToplistCategories, error) {
	var out ToplistCategories
	err := c.getJSON("/top/category", nil, &out)
	return out, err
}

// ToplistDetail 榜单详情，形状与歌单详情近似，可复用歌曲行。
func (c *Client) ToplistDetail(id int64, page, num int) (ToplistDetail, error) {
	var out ToplistDetail
	err := c.getJSON("/top/"+strconv.FormatInt(id, 10)+"/detail", q("page", page, "num", num), &out)
	return out, err
}

func (c *Client) LoginQR(channel string) (QRCode, error) {
	var out QRCode
	err := c.getJSON("/login/qrcode/"+channel, nil, &out)
	return out, err
}

func (c *Client) LoginQRStatus(channel, identifier string) (QRStatus, error) {
	var out QRStatus
	err := c.getJSON("/login/qrcode/"+channel+"/status", q("identifier", identifier), &out)
	return out, err
}

func decode(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// StreamClient 是长连接音频流客户端：不设整体超时（流要一直读），
// 只对连接/响应头限时。
var StreamClient = &http.Client{
	Timeout: 0,
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       90 * time.Second,
	},
}
