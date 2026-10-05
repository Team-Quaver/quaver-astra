<p align="center">
  <img src="img/quaver-astra.svg" width="96">
</p>

# Quaver Music Astra - 最美观/实用的 Q 音第三方客户端，现已为轻量化而生

Quaver Music Astra，是一款 QQ 音乐的第三方客户端，其目的是为了让 Linux DE / Wayland WM 用户能够爽用，基于 **MyGO** 实现。

该项目定位为轻量化版本的 Quaver Music，因此部分功能并不会完整实现（见文末「与主项目的差异」）。

名字取自于八分音符，对应了音乐，"QQ" 的 Q 字母。

#### 主界面

<p align="center">
  <img src="img/preview-home.png" width="720">
</p>

> [!CAUTION]
> 真爱音乐，尊重正版，音乐平台不易，该应用**不提供盗版 QQ 音乐曲目服务！**
>
> 本软件系 Vibe Coding 的产物，虽然我会尽力尝试个人维护，但不提供可用性保证

# 架构

```
┌────────────────────────────────────────────┐
│  QML UI（Qt Quick / Controls / Multimedia） │
│   侧栏 · 路由视图 · 播放条 · 正在播放页      │
└──────────────┬─────────────────────────────┘
               │ http://127.0.0.1:<port>  （JSON 信封 + Range 流中继）
┌──────────────▼─────────────────────────────┐
│  Typhoeus Go sidecar（复用主项目后端）       │
│                                           │
└────────────────────────────────────────────┘
```

- **C++ 壳层**（`src/`）：拉起 sidecar（`BackendProcess`，含 HTTP 就绪探测与崩溃重启）、封面取色（`ColorProbe`，对齐主项目 `lib/color.ts` 算法）、配置/文件存储（`AppConfig`）。
- **QML 层**（`src/qml/`）：全部界面与业务状态机。`Player.qml` 对齐主项目 `player.ts`（队列/回退链/歌词/收藏/会话存档），`Qrc.js` 实现行级 LRC 与逐字 QRC 解析。
- **音频**：Qt Multimedia（ffmpeg 后端）直接播放 sidecar 中继流；解码失败沿档位链自动降级。

# 构建与运行

依赖：Qt ≥ 6.5（Quick / QuickControls2 / Multimedia / Network / Svg）、CMake ≥ 3.21、Go ≥ 1.21（构建 sidecar）。

```bash
# 子模块只需初始化一次（vendor/Typhoeus-go）
git submodule update --init

# 一次性构建主程序 + sidecar（CMake 检测到 go 工具链会自动把 sidecar 编进 <build>/bin/）
cmake -B build -DCMAKE_BUILD_TYPE=Release
cmake --build build

# 运行（sidecar 自动随包发现并拉起，无需手工启动）
./build/quaver-astra
```

QtCreator：直接打开仓库根的 `CMakeLists.txt` 即可——构建时会自动产出 sidecar 到构建目录 `bin/`，
运行时主进程按「应用目录/bin → 应用目录 → 源码树 vendor/Typhoeus-go（已编译产物或 `go run` 现编）」
顺序找到它并拉起，不需要手工配置运行环境。

开发态环境变量：

| 变量 | 说明 |
|---|---|
| `QUAVER_API` | 外接已运行的 sidecar（如 `http://127.0.0.1:3200`），本进程不自拉（该模式下凭证不交接、不落盘） |
| `QUAVER_SIDECAR` | 指定 sidecar 二进制路径（默认找 应用目录/bin、应用目录、源码树 vendor） |
| `QUAVER_SCREENSHOT` | 自检钩子：`[np:]<png路径>`，启动 8s 后抓窗存图再退出（`np:` 先展开正在播放页） |
| `QUAVER_VAULT_SELFTEST` | 自检钩子：`1` 跑 SessionVault 回归（RFC 8439 向量 + 落盘回读），退出码 0/1 |

配置与状态存于系统标准配置目录（Linux `~/.config/quaver/quaver-astra/`，Windows `%AppData%\quaver\quaver-astra`，macOS `~/Library/Application Support/quaver/quaver-astra/`）：`quaver-astra.conf` 是 INI 可手改；`session.json` 保存队列与播放进度；`loved.json` 保存红心。

# 逐字歌词

正在播放页支持**卡拉OK逐字扫色**：

- 拉取歌词时带 `qrc=1`，后端返回 QRC（XML 信封或纯文本，词级毫秒时间轴）；
- `Qrc.js` 解析为词级行数据，并与翻译 LRC 按时间就近对齐；
- `KaraokeView` / `KaraokeLineItem` 以 16ms 外推时钟逐帧驱动，当前句逐词扫色（染色取自封面主色），非当前句淡显，点击任意句 seek，滚轮翻阅 3s 后自动回跟；
- 无逐字数据的歌曲自动回退行级 `LyricView`（字号可调、可关翻译）。

# CI

`.github/workflows/build.yml` 六目标矩阵，各自随包构建对应 GOOS/GOARCH 的 sidecar：

| 目标 | Runner | Qt | 产物 |
|---|---|---|---|
| Linux AMD64 | ubuntu-24.04 | aqt linux_gcc_64 | AppImage |
| Linux ARM64 | ubuntu-24.04-arm | aqt linux_gcc_arm64 | AppImage |
| Linux loong64 | ubuntu-24.04 + qemu + loong64 容器 | 容器内系统 Qt6 | tar.gz（依赖系统 Qt6） |
| Windows AMD64 | windows-latest | aqt win64_msvc2022_64 | zip |
| Windows ARM64 | windows-latest | aqt win64_msvc2022_arm64_cross | zip |
| macOS Apple Silicon | macos-14 | aqt clang_64 | zip（.app） |

版本三态：打 `v*` tag → 汇总为 Release **草稿**（人工核对后手动 Publish）；push / PR → 仅 artifact。

# 与主项目（Electron 版）的差异

- 无 Sparkle 插件系统；
- 无 MPRIS / 画廊模式 / 歌单写侧管理 / 歌手·专辑页 / 热评；
- 音频后端为 Qt Multimedia（无 mpv 引擎与音频设备选择、淡入淡出）。

# 协议

该项目使用 AGPLv3 及其未来版本协议，其使用的 API 上游使用 GPLv3 及其未来版本协议。

与此同时，该项目依旧无法避免属于 QQ 音乐第三方客户端，请尊重 QQ 音乐的最终用户协议，禁止破解 QQ 音乐的曲库，本应用仅提供流媒体服务，不提供任何下载服务。
