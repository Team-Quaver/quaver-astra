<p align="center"><strong>Quaver Astra · Native</strong></p>

# Quaver Astra（Native 分支）

Quaver Astra 的轻量化重写：**全 Go、原生 UI、无 WebView**。

- **UI**：[MyGo](https://github.com/egoist/mygo) 的原生 UI 工具包（`ui` 包）——视图是状态的函数，由 MyGo 在 GPU 上直接绘制（Linux 走 OpenGL），不开 WebKitGTK，窗口秒开、内存占用小。
- **后端**：[Typhoeus-go](https://github.com/Team-Quaver/typhoeus-go)（子模块 `third_party/Typhoeus-go`）**进程内嵌**——不再是 sidecar 独立进程，`quaver-server` 的完整路由表直接挂在本进程 `127.0.0.1` 的随机端口上。
- **凭证持久化**：复用后端 external 模式的 QCRED1 管道交接协议（登录/自动刷新/登出实时回写），凭证以 ChaCha20-Poly1305 加密落盘（`credential.enc` + 随机主密钥 `session.key`，0600），明文绝不落盘；登出或"清除已保存的凭证"即删。
- **音频**：[mpv](https://mpv.io/) 子进程经 JSON IPC（Unix socket / 命名管道）驱动。解码交给 mpv 内置的 FFmpeg，本项目不再自带任何解码器——**全档位可播**（含atmos / DTS / FLAC / AAC），seek 由 mpv 自己做 Range 与重试，播放位置直接取 mpv 的 `time-pos`（不走墙钟推算，零累积误差）。仍是无 cgo、单二进制，可交叉编译。mpv 的查找顺序与 Quaver Astra 本体一致：**显式路径 → 随包目录 → PATH**。

> [!CAUTION]
> 真爱音乐，尊重正版，音乐平台不易，该应用**不提供盗版 QQ 音乐曲目服务！**

## 构建与运行

依赖：

- Go 1.27+（Linux 需 GTK3）
- **mpv**（播放必需）：`apt install mpv` / `dnf install mpv` / `brew install mpv`。设置 → 关于页会显示检测状态与实际路径；也可用 `QAA_MPV` 指定可执行文件。

```sh
git clone --recurse-submodules https://github.com/Team-Quaver/quaver-astra
cd quaver-astra
go build .
./quaver-astra
```

测试：

```sh
go test ./internal/...
QAA_TEST_MPV=1 go test -run TestEngineRealMPV ./internal/audio/   # 需要装mpv
```

环境变量（自检用）：

| 变量 | 作用 |
| --- | --- |
| `QAA_MPV` | 指定 mpv 可执行文件（绝对路径） |
| `QAA_MPV_DIR` | 随包mpv 所在目录（找 `mpv/AppRun`、`mpv/mpv`、`mpv.exe`、`mpv`） |
| `QAA_MPV_AO` | 强制音频输出（如 `null` 用于无声卡环境自检） |
| `QAA_MPV_DEVICE` | 强制音频输出设备 |
| `QAA_TEST_MPV=1` | 开启真实 mpv 往返测试 |

mpv 定位优先级与 Quaver Astra 本体（`ui/electron/audio/dist/bins.js`）一致：
显式路径 → 随包目录 → PATH。随包 mpv 是AppImage 那种「目录形态」时，
其 `lib/` 会**追加**到子进程的 `LD_LIBRARY_PATH` 末尾——放前面会导致
「能解码但没声音」（音频输出库必须用宿主机的）。

## 功能（轻量版范围）

- 首页：今日精选 hero、新歌速递、推荐歌单网格
- 歌单详情：头部 + 歌曲列表，触底翻页、收藏歌单
- 搜索：歌曲 / 歌单两个 tab，热搜词、翻页
- 我喜欢 / 每日 30 首 / 猜你喜欢（换一批）
- **收藏的歌单**：我收藏的他人歌单网格、悬停播放、加载更多（登录态变化时自动重取）
- 登录：QQ / 微信 / 手机三通道扫码（自动生成 + 轮询），凭证加密保存、重启自动恢复
- 正在播放页：模糊封面底、**逐字（QRC）歌词**——逐字卡拉OK 高亮，无逐字时间轴时自动回退行级 + 翻译（点击跳转、跟随高亮）、音质胶囊
- 播放条：整条拖拽 seek、音量弹层、循环模式、随机播放（每日一套确定性顺序）、音质菜单（会话级切换）、红心收藏、队列面板（可拖拽排序）
- 播放队列：插队播放 / 加入队列 / 删除 / 清空；双击歌曲即播
- 封面动态取色：主题强调色随当前封面变化（含深浅色两套映射）
- 明暗主题跟随系统（可在设置里强制；强制值优先于桌面外观，见下）
- **动效**：对齐主项目（桌面版 Quaver）的动效语言——正在播放页上滑开合、路由页错峰入场（route-in）、侧栏宽度过渡 + 收起图标旋转、行/卡片底色过渡、进度条缓动、逐字歌词每帧推进
- 侧栏可收起；收起态底部按钮纵向排列；侧栏几何有布局测试守护（图标列共线、行距均匀）；纯图标按钮均有 tooltip
- 系统托盘：对齐 Quaver Astra 的曲目行、上一曲 / 播放暂停 / 下一曲、循环与随机、显示/隐藏、退出；窗口关闭按钮可收进托盘
- 播放音频时阻止系统睡眠（暂停/停止即恢复，不阻止熄屏）；退出应用时自动终止 mpv 子进程

## 架构

```
main.go                 启动：内嵌后端 → 配置 → MyGo 窗口
internal/backend/       quaver-server 进程内嵌 + HTTP 客户端（信封 {code,msg,data}）
internal/player/        播放器状态机（队列/模式/随机/回退链/歌词/收藏），与 UI 解耦
                        lyric.go = 行级 LRC 解析；qrc.go = 逐字 QRC 解码与解析
internal/audio/         mpv 子进程 + JSON IPC 引擎
                        bins.go = 二进制定位（显式/随包/PATH）+ 子进程环境
                        mpv.go  = 引擎（命令下发 / 属性观察 / 事件处理）
internal/appui/         MyGo 原生 UI：外壳、侧栏、路由页、播放条、正在播放、队列
                        theme.go = 调色板与明暗判定；motion.go = 动效语言（缓动/错峰入场/逐字绘制）
internal/colorprobe/    封面主色提取 + 模糊底图
internal/vault/         登录凭证加密持久化（ChaCha20-Poly1305 + 随机主密钥）
internal/conf/          JSON 配置（quaver-astra.json）
```

播放链路：

```
player.startCurrent
  → backend /stream/resolve      解析音档 → 本地中继 URL
  → audio.OpenURL                mpv loadfile（mpv 自己发 Range / 重试）
  ← mpv observe_property         time-pos / duration / pause / eof-reached
  → player.tick                  同步位置、检测自然播完切下一首
```

自检钩子（开发用）：

- `QUAVER_ROUTE=<path>` 启动后导航到指定路由
- `QUAVER_SCREENSHOT=<png>` + `QUAVER_SCREENSHOT_DELAY=<秒>` 延时截图后退出
- `QUAVER_PLAY_FIRST=1` 自动播一首新歌（验证播放链路）
- `QUAVER_CONFIG_DIR=<dir>` 自定义配置目录

## 与主项目（Quaver Astra）的差异

轻量版刻意裁剪的部分：无歌手/专辑页、无搜索联想、无歌单写侧（只读浏览与收藏）、无 MPRIS / 桌面快捷键。凭证保存为本机文件加密（密钥与密文同目录，防拷贝不防本机 root；后续可升级 OS 密钥环）。

**逐字歌词**：请求歌词时带 `qrc=1`，上游把 QRC 明文塞在同一个 `lyric` 字段里（XML 信封形态，Typhoeus-go 在服务端解密）。解析在 `internal/player/qrc.go`；服务端万一解密失败、原样透传密文，再由 [jixunmoe-go/qrc](https://github.com/jixunmoe-go/qrc) 在本地兜底解一层。

**明暗主题**：应用自己按 `Style.Theme` 定明暗，不依赖 MyGo 的 `Theme.IsDark()`——它的 Linux 后端 `SetSource("light")` 只清 GTK 的 prefer-dark 标志，紧接着 `IsDark()` 又去问 XDG portal 的 color-scheme，深色桌面上「强制浅色」会被 portal 覆盖，浅色模式因此完全无法生效。

换用 mpv 之后，音质支持不再是短板：原先受自研解码器限制只能播 MP3 128/320、FLAC 与 Ogg，atmos/母带需嗅探降档；现在由 FFmpeg 全档位直解。

## 许可

AGPL-3.0-Only（见 [LICENSE](LICENSE)）。第三方依赖见 go.mod；MyGo（MIT）、Typhoeus-go、jixunmoe-go/qrc（MIT，QRC 本地兜底解码）、mpv（LGPL-2.1+，作为外部进程调用）。
