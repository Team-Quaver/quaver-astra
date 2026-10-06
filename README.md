<p align="center"><strong>Quaver Music Astra · Native</strong></p>

# Quaver Music Astra（Native 分支）

Quaver Music 的轻量化重写：**全 Go、原生 UI、无 WebView**。

- **UI**：[MyGo](https://github.com/egoist/mygo) 的原生 UI 工具包（`ui` 包）——视图是状态的函数，由 MyGo 在 GPU 上直接绘制（Linux 走 OpenGL），不开 WebKitGTK，窗口秒开、内存占用小。
- **后端**：[Typhoeus-go](https://github.com/Team-Quaver/typhoeus-go)（子模块 `third_party/Typhoeus-go`）**进程内嵌**——不再是 sidecar 独立进程，`quaver-server` 的完整路由表直接挂在本进程 `127.0.0.1` 的随机端口上。
- **音频**：纯 Go 播放引擎——[oto](https://github.com/ebitengine/oto) 输出（Linux 走 PulseAudio/PipeWire）+ [go-mp3](https://github.com/hajimehoshi/go-mp3) / [mewkiz/flac](https://github.com/mewkiz/flac) 解码，无 cgo、单二进制。

> [!CAUTION]
> 真爱音乐，尊重正版，音乐平台不易，该应用**不提供盗版 QQ 音乐曲目服务！**

## 构建与运行

依赖：Go 1.27+（Linux 需 GTK3；音频走 PulseAudio/PipeWire）。

```sh
git clone --recurse-submodules https://github.com/Team-Quaver/quaver-astra
cd quaver-astra
go build .
./quaver-astra
```

测试：

```sh
go test ./internal/...
```

## 功能（轻量版范围）

- 首页：今日精选 hero、新歌速递、推荐歌单网格
- 歌单详情：头部 + 歌曲列表，触底翻页、收藏歌单
- 搜索：歌曲 / 歌单两个 tab，热搜词、翻页
- 我喜欢 / 每日 30 首 / 猜你喜欢（换一批）
- 登录：QQ / 微信 / 手机 三通道扫码（自动生成 + 轮询）
- 正在播放页：模糊封面底、行级歌词 + 翻译（点击跳转、跟随高亮）、音质胶囊
- 播放条：整条拖拽 seek、音量弹层、循环模式、随机播放（每日一套确定性顺序）、音质菜单（会话级切换）、红心收藏、队列面板（可拖拽排序）
- 播放队列：插队播放 / 加入队列 / 删除 / 清空；双击歌曲即播
- 封面动态取色：主题强调色随当前封面变化（含深浅色两套映射）
- 明暗主题跟随系统（可在设置里强制）

## 架构

```
main.go                 启动：内嵌后端 → 配置 → MyGo 窗口
internal/backend/       quaver-server 进程内嵌 + HTTP 客户端（信封 {code,msg,data}）
internal/player/        播放器状态机（队列/模式/随机/回退链/歌词/收藏），与 UI 解耦
internal/audio/         oto + mp3/flac 解码（整曲缓冲模型，48k 立体声输出，重采样）
internal/appui/         MyGo 原生 UI：外壳、侧栏、路由页、播放条、正在播放、队列
internal/colorprobe/    封面主色提取 + 模糊底图
internal/conf/          JSON 配置（quaver-astra.json）
```

自检钩子（开发用）：

- `QUAVER_ROUTE=<path>` 启动后导航到指定路由
- `QUAVER_SCREENSHOT=<png>` + `QUAVER_SCREENSHOT_DELAY=<秒>` 延时截图后退出
- `QUAVER_PLAY_FIRST=1` 自动播一首新歌（验证播放链路）
- `QUAVER_CONFIG_DIR=<dir>` 自定义配置目录

## 与主项目（Quaver Music）的差异

轻量版刻意裁剪的部分：无逐字（QRC）歌词（仅行级 + 翻译）、无歌手/专辑页、无搜索联想、音质仅 MP3 128/320 与 FLAC（ogg/atmos 档位不做，`auto` 自动降级到支持集内最高档）、无 MPRIS / 桌面快捷键、无收藏歌单侧栏与歌单写侧、**登录凭证只驻后端内存（重启需重新扫码）**。

## 许可

AGPL-3.0（见 [LICENSE](LICENSE)）。第三方依赖见 go.mod；MyGo（MIT）、Typhoeus-go、oto、go-mp3、mewkiz/flac。
