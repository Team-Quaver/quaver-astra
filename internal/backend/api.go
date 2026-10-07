package backend

import (
	"bytes"
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

// Lyric 歌词（服务端已解密；trans=1 时带翻译）。
type Lyric struct {
	Songid int64  `json:"songid"`
	Lyric  string `json:"lyric"`
	Trans  string `json:"trans"`
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

// SearchResults 归一化搜索结果。
type SearchResults struct {
	Song      []Song            `json:"song"`
	Songlists []SonglistSummary `json:"songlist"`
	TotalNum  int64             `json:"total_num"`
	Nextpage  int64             `json:"nextpage"`
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

// VIPInfo 归一化 VIP 响应里 UI 关心的部分。
type VIPInfo struct {
	Identity struct {
		Vip        int    `json:"vip"`
		HugeVip    int    `json:"huge_vip"`
		HugeVipEnd string `json:"huge_vip_end"`
	} `json:"identity"`
	Userinfo struct {
		Expire string `json:"expire"`
		Score  int    `json:"score"`
	} `json:"userinfo"`
}

// ===================== 端点 =====================

func (c *Client) LoginStatus() (LoginStatus, error) {
	var out LoginStatus
	err := c.getJSON("/login/status", nil, &out)
	return out, err
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

// ResolveStream 解析播放流。tier 为档位 id 或 "auto"；deprioritize 降权档位。
func (c *Client) ResolveStream(mid, mediaMid string, songType int, tier string, auto bool, deprioritize []string) (Resolved, error) {
	if deprioritize == nil {
		deprioritize = []string{}
	}
	var out Resolved
	body := map[string]any{
		"mid": mid, "media_mid": mediaMid, "song_type": songType,
		"tier": tier, "auto": auto, "deprioritize": deprioritize,
	}
	err := c.postJSONInto("/stream/resolve", body, &out)
	return out, err
}

func (c *Client) Lyric(mid string, trans bool) (Lyric, error) {
	t := ""
	if trans {
		t = "1"
	}
	var out Lyric
	err := c.getJSON("/song/"+mid+"/lyric", q("trans", t), &out)
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

func (c *Client) RecommendSonglists(page, num int) (struct {
	Songlists []SonglistSummary `json:"songlists"`
}, error) {
	var out struct {
		Songlists []SonglistSummary `json:"songlists"`
	}
	err := c.getJSON("/recommend/songlist", q("page", page, "num", num), &out)
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

func (c *Client) RecommendGuess(rounds int) (struct {
	Songs  []Song `json:"songs"`
	Rounds int    `json:"rounds"`
}, error) {
	var out struct {
		Songs  []Song `json:"songs"`
		Rounds int    `json:"rounds"`
	}
	err := c.getJSON("/recommend/guess", q("rounds", rounds), &out)
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
