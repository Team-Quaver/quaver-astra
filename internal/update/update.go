// Package update 实现 Quaver Astra 的自动更新：读取 GitHub Releases API
// 区分「稳定版」与「快照版」两个渠道，挑选当前平台对应的发行产物，下载并
// 校验，再交由 install.go 原地升级可执行文件。
//
// 渠道口径与发布 CI（.github/workflows/build.yml）严格对齐：
//
//   - 稳定版：打 v* tag 触发的正式 Release。用 /releases/latest 读取，GitHub
//     保证它排除 draft 与 prerelease。
//   - 快照版：每次 push 触发的滚动预发布，tag 形如 snapshot-<sha>，标题与产物
//     名里的版本形如 <base>-<sha>-snapshot。用 /releases 列表里最新的
//     snapshot-* 预发布。
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	// Repo 是发行产物的来源仓库（owner/name）。
	Repo = "Team-Quaver/quaver-astra"
	// APIBase 是 GitHub REST API 的默认入口，测试用 Client.APIBase 覆盖。
	APIBase = "https://api.github.com"
	// userAgent 让 GitHub 能识别调用方；匿名请求也要求它非空。
	userAgent = "quaver-astra-updater"
	// releasesPerPage 限制列表请求大小：快照只需最近若干条。
	releasesPerPage = 50
)

// Channel 是更新渠道。
type Channel string

const (
	// Stable 跟踪最新的正式 Release（/releases/latest）。
	Stable Channel = "stable"
	// Snapshot 跟踪最新的 snapshot-* 预发布。
	Snapshot Channel = "snapshot"
)

// ParseChannel 把配置里的字符串收敛成合法渠道，未知值一律回落到稳定版。
func ParseChannel(s string) Channel {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "snapshot", "pre", "prerelease", "nightly":
		return Snapshot
	default:
		return Stable
	}
}

// Label 是渠道的中文展示名。
func (ch Channel) Label() string {
	if ch == Snapshot {
		return "快照版"
	}
	return "稳定版"
}

var (
	// ErrNoRelease 表示渠道里暂时没有可用的发行版本。
	ErrNoRelease = errors.New("没有找到可用的发行版本")
	// ErrNoAsset 表示该发行没有当前平台对应的产物。
	ErrNoAsset = errors.New("该版本没有当前平台的安装包")
)

// Asset 是 Release 里的一个产物文件。
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
	// Digest 形如 "sha256:<hex>"；旧产物可能为空。
	Digest string `json:"digest"`
}

// Release 是 GitHub Release 的本地视图，附带归一化后的渠道与版本。
type Release struct {
	Tag         string    `json:"tag_name"`
	Name        string    `json:"name"`
	Prerelease  bool      `json:"prerelease"`
	Draft       bool      `json:"draft"`
	PublishedAt time.Time `json:"published_at"`
	Notes       string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	Assets      []Asset   `json:"assets"`

	// Channel 是归一化后的渠道，Version 是展示用完整版本号，
	// SHA 是快照版的构建标识（稳定版为空）。
	Channel Channel `json:"-"`
	Version string  `json:"-"`
	SHA     string  `json:"-"`
}

// normalize 从 tag/name 推导渠道、展示版本与快照标识。
func (r *Release) normalize() {
	tag := strings.TrimSpace(r.Tag)
	if strings.HasPrefix(tag, "snapshot-") {
		r.Channel = Snapshot
		r.SHA = strings.TrimPrefix(tag, "snapshot-")
		// 发布标题是 "Snapshot <base>-<sha>-snapshot"，比 tag 更能表达版本。
		if v := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(r.Name), "Snapshot")); v != "" {
			r.Version = strings.TrimSpace(v)
		} else {
			r.Version = tag
		}
		return
	}
	r.Channel = Stable
	r.Version = strings.TrimLeft(tag, "vV")
	if r.Version == "" {
		r.Version = tag
	}
}

// LabelVersion 返回展示用版本号，空值时回落到 tag。
func (r *Release) LabelVersion() string {
	if r == nil {
		return ""
	}
	if r.Version != "" {
		return r.Version
	}
	return r.Tag
}

// Client 是 GitHub Releases API 客户端。
type Client struct {
	Repo    string
	APIBase string
	HTTP    *http.Client
	// Token 是可选的 GitHub token（提高匿名 60 次/小时的限流）。为空时读取
	// GITHUB_TOKEN / GH_TOKEN 环境变量。
	Token string
}

// NewClient 返回指向本项目发行仓库的客户端。
func NewClient() *Client {
	return &Client{
		Repo:    Repo,
		APIBase: APIBase,
		HTTP:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) repo() string {
	if c != nil && c.Repo != "" {
		return c.Repo
	}
	return Repo
}

func (c *Client) base() string {
	if c != nil && c.APIBase != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return APIBase
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) token() string {
	if c != nil && c.Token != "" {
		return c.Token
	}
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t
	}
	return os.Getenv("GH_TOKEN")
}

// setHeaders 补齐 GitHub API 需要的请求头。
func (c *Client) setHeaders(req *http.Request) {
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", userAgent)
	if t := c.token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
}

func (c *Client) getJSON(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+path, nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return &HTTPError{
			Code:   resp.StatusCode,
			Status: resp.Status,
			Body:   strings.TrimSpace(string(body)),
		}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("解析 GitHub 响应失败：%w", err)
	}
	return nil
}

// HTTPError 是 GitHub API 返回的非 200 响应。
type HTTPError struct {
	Code   int
	Status string
	Body   string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("GitHub API %s: %s", e.Status, e.Body)
}

// Latest 按渠道返回最新发行版本。
func (c *Client) Latest(ctx context.Context, ch Channel) (*Release, error) {
	if ch == Snapshot {
		return c.latestSnapshot(ctx)
	}
	return c.latestStable(ctx)
}

// latestStable 读取 /releases/latest，GitHub 保证它排除 draft 与 prerelease。
func (c *Client) latestStable(ctx context.Context) (*Release, error) {
	var r Release
	if err := c.getJSON(ctx, "/repos/"+c.repo()+"/releases/latest", &r); err != nil {
		var he *HTTPError
		if errors.As(err, &he) && he.Code == http.StatusNotFound {
			return nil, ErrNoRelease
		}
		return nil, err
	}
	r.normalize()
	if r.Draft || r.Prerelease {
		return nil, ErrNoRelease
	}
	return &r, nil
}

// latestSnapshot 在 Releases 列表里挑最新一条 snapshot-* 预发布。CI 用滚动
// tag，历史上会积累多个快照，因此按发布时间取最新而不是取列表首条。
func (c *Client) latestSnapshot(ctx context.Context) (*Release, error) {
	var list []Release
	path := fmt.Sprintf("/repos/%s/releases?per_page=%d", c.repo(), releasesPerPage)
	if err := c.getJSON(ctx, path, &list); err != nil {
		return nil, err
	}
	var best *Release
	for i := range list {
		r := &list[i]
		if r.Draft || !r.Prerelease || !strings.HasPrefix(strings.TrimSpace(r.Tag), "snapshot-") {
			continue
		}
		r.normalize()
		if best == nil || r.PublishedAt.After(best.PublishedAt) {
			best = r
		}
	}
	if best == nil {
		return nil, ErrNoRelease
	}
	return best, nil
}

// assetTarget 返回各平台对应产物的文件名关键字与扩展名（均小写）。
func assetTarget(goos, goarch string) (key, ext string, ok bool) {
	switch goos {
	case "linux":
		switch goarch {
		case "amd64":
			return "linux-amd64", ".appimage", true
		case "arm64":
			return "linux-arm64", ".appimage", true
		case "loong64":
			return "linux-loong64", ".tar.gz", true
		}
	case "windows":
		if goarch == "amd64" || goarch == "arm64" {
			return "windows-" + goarch, ".zip", true
		}
	case "darwin":
		if goarch == "arm64" {
			return "macos-arm64", ".dmg", true
		}
	}
	return "", "", false
}

// AssetFor 按 GOOS/GOARCH 选出发行产物。发行包名形如
// Quaver-Astra-<version>-<target>.<ext>，因此按键名子串与扩展名匹配即可。
func (r *Release) AssetFor(goos, goarch string) (Asset, bool) {
	if r == nil {
		return Asset{}, false
	}
	key, ext, ok := assetTarget(goos, goarch)
	if !ok {
		return Asset{}, false
	}
	for _, a := range r.Assets {
		name := strings.ToLower(a.Name)
		if strings.Contains(name, key) && strings.HasSuffix(name, ext) {
			return a, true
		}
	}
	return Asset{}, false
}

// ProgressFunc 报告下载进度：done 为已写字节，total 为总字节（未知为 0）。
type ProgressFunc func(done, total int64)

// Download 把产物下载到 dst，边下边算 sha256 校验 Digest 与字节数。
func (c *Client) Download(ctx context.Context, a Asset, dst string, progress ProgressFunc) error {
	if a.URL == "" {
		return errors.New("产物地址为空")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", userAgent)
	if t := c.token(); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("下载失败：%s", resp.Status)
	}

	total := a.Size
	if resp.ContentLength > 0 {
		total = resp.ContentLength
	}
	if err := os.MkdirAll(dirOf(dst), 0o755); err != nil {
		return err
	}
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	sum := sha256.New()
	buf := make([]byte, 64<<10)
	var done int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				f.Close()
				os.Remove(tmp)
				return werr
			}
			sum.Write(buf[:n])
			done += int64(n)
			if progress != nil {
				progress(done, total)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			os.Remove(tmp)
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if total > 0 && done != total {
		os.Remove(tmp)
		return fmt.Errorf("下载不完整：期望 %d 字节，实得 %d", total, done)
	}
	if err := verifyDigest(a.Digest, sum.Sum(nil)); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}

// verifyDigest 在 Digest 非空时校验 sha256。
func verifyDigest(digest string, got []byte) error {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return nil
	}
	algo, want, ok := strings.Cut(digest, ":")
	if !ok || !strings.EqualFold(algo, "sha256") {
		// 未知算法：不冒进，交给上层按需处理。
		return nil
	}
	have := hex.EncodeToString(got)
	if !strings.EqualFold(have, strings.TrimSpace(want)) {
		return fmt.Errorf("校验失败：sha256 不匹配（期望 %s，实得 %s）", strings.TrimSpace(want), have)
	}
	return nil
}

func dirOf(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[:i]
	}
	return "."
}

// ===== 版本比较 =====

// Core 是版本号的数值部分 major.minor.patch（缺省补 0）。
type Core struct {
	Major, Minor, Patch int
}

// Compare 返回 -1/0/1。
func (a Core) Compare(b Core) int {
	for _, d := range [][2]int{{a.Major, b.Major}, {a.Minor, b.Minor}, {a.Patch, b.Patch}} {
		switch {
		case d[0] < d[1]:
			return -1
		case d[0] > d[1]:
			return 1
		}
	}
	return 0
}

// ParseCore 解析形如 "v1.2.3"、"1.2.3-<sha>-snapshot" 的数值核心；
// 预发布后缀（- 之后）不参与比较。
func ParseCore(s string) (Core, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "vV")
	if i := strings.IndexByte(s, '-'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return Core{}, false
	}
	var out Core
	nums := []*int{&out.Major, &out.Minor, &out.Patch}
	parts := strings.Split(s, ".")
	for i := 0; i < len(parts) && i < len(nums); i++ {
		n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
		if err != nil {
			return Core{}, false
		}
		*nums[i] = n
	}
	return out, true
}

// SnapshotSHA 从形如 "1.2.3-<sha>-snapshot" 的版本串里取出快照标识。
func SnapshotSHA(version string) string {
	v := strings.TrimSpace(version)
	if !strings.HasSuffix(v, "-snapshot") {
		return ""
	}
	v = strings.TrimSuffix(v, "-snapshot")
	if i := strings.LastIndexByte(v, '-'); i >= 0 {
		return v[i+1:]
	}
	return ""
}

// IsSnapshot 报告版本串是否是快照构建。
func IsSnapshot(version string) bool { return SnapshotSHA(version) != "" }

// Relation 描述某个发行版本相对当前构建的关系。
type Relation int

const (
	// RelationUnknown 无法比较（版本串不可解析，或目标为空）。
	RelationUnknown Relation = iota
	// RelationSame 已是同一个构建。
	RelationSame
	// RelationNewer 目标比当前新。
	RelationNewer
	// RelationOlder 目标比当前旧。
	RelationOlder
	// RelationSwitch 同主版本、不同渠道或不同构建（稳定版↔快照版切换）。
	RelationSwitch
)

// Relate 判定发行版本 r 相对当前版本 current 的关系。dev 构建不做版本比较，
// 一律视为可升级。
func Relate(current string, r *Release) Relation {
	if r == nil {
		return RelationUnknown
	}
	cur := strings.TrimSpace(current)
	if cur == "" || cur == "dev" {
		return RelationNewer
	}
	cc, ok := ParseCore(cur)
	tc, ok2 := ParseCore(r.LabelVersion())
	if !ok || !ok2 {
		return RelationUnknown
	}
	switch cc.Compare(tc) {
	case -1:
		return RelationNewer
	case 1:
		return RelationOlder
	}
	curSnap := IsSnapshot(cur)
	tgtSnap := r.Channel == Snapshot
	switch {
	case curSnap && tgtSnap:
		if sha := SnapshotSHA(cur); sha != "" && sha == r.SHA {
			return RelationSame
		}
		return RelationSwitch
	case !curSnap && !tgtSnap:
		return RelationSame
	default:
		return RelationSwitch
	}
}

// Available 报告该关系下是否值得向用户提供/安装该版本。
func (r Relation) Available() bool {
	switch r {
	case RelationNewer, RelationSwitch, RelationOlder:
		return true
	default:
		return false
	}
}

// SortAssets 便于测试与调试时稳定遍历产物。
func SortAssets(as []Asset) {
	sort.Slice(as, func(i, j int) bool { return as[i].Name < as[j].Name })
}
