# quaver-astra 项目备忘

## 架构

全 Go 原生桌面应用，无 WebView、无 cgo。

```
main.go              启动：内嵌后端 → 配置 → MyGo 窗口
internal/backend/    Typhoeus-go 进程内嵌（子模块）+ HTTP 客户端
internal/player/     播放器状态机（队列/模式/随机/降级链/歌词/收藏），与 UI 解耦
internal/audio/      mpv 子进程 + JSON IPC 引擎
internal/appui/      MyGo 原生 UI：外壳、侧栏、路由页、播放条、正在播放、队列
internal/colorprobe/ 封面主色提取
internal/vault/      登录凭证加密（ChaCha20-Poly1305）
internal/conf/       JSON 配置
```

`player.Engine` 是 audio 与 player 之间的接口，换播放后端只需改这一层。

## 关键决策与理由

### 为什么播放用 mpv 子进程而不是 libmpv

要交叉编译到 linux/{amd64,arm64,loong64}、windows/{amd64,arm64}、darwin/arm64。
cgo 会破坏交叉编译。mpv 各发行版可独立安装，IPC 通信后播放后端与 UI 完全解耦。

### mpv 依赖是硬依赖

README 与设置「关于」页都写明了。`QAA_MPV` 可指定路径，
`QAA_MPV_AO` 可强制音频输出（无声卡环境自检用 `null`）。

### 播放位置以 mpv 为唯一真相源

不要自己用墙钟推算位置——那需要处理 seek/换歌时的重锚定，误差会累积。
直接读 `time-pos`，`Ended()` 用 `eof-reached`。

## 踩过的坑（改这块前先看）

### mpv JSON IPC

1. 顶层必须是 JSON **对象** `{"command":[...]}`，不是数组。
   发数组 → mpv 当 input.conf 文本命令处理，永远无响应。
2. `--idle` 必须无值。`--idle=yes` 会让 mpv 读完就退。
3. **握手完成前不能启动 observe 协程**：socket 只有一个读消费位，
   observe 抢到读锁后阻塞在读上，握手的 command() 拿不到锁 → 死锁。
4. `loadfile` 必须 `async:true`。mpv 命令同步执行期间不服务 socket，
   网络流加载慢必然顶到读超时。失败由 `end-file`+`reason=="error"` 回填。
5. 读超时是必要兜底——`loadfile` 在起播主流程上，不能被 mpv 拖死。

### 本机环境

- `/tmp` 只有 10MB（tmpfs），Go 链接器会报 `no space left on device`。
  **用 `TMPDIR=$PWD/.gotmp` 构建**（该目录已 gitignore）。
- 无声卡（无 `/dev/snd`）：真实 mpv 测试必须 `QAA_MPV_AO=null`，
  否则音频初始化挂起。无显示服务，GUI 截图钩子跑不出图。
- 无头环境下无法目视验证 UI 改动，只能靠代码语义推断 + 交叉编译通过。

## 测试约定

```sh
TMPDIR=$PWD/.gotmp go test ./internal/...                    # 常规
TMPDIR=$PWD/.gotmp QAA_TEST_MPV=1 QAA_MPV_AO=null \
  go test -race ./internal/audio/ ./internal/player/         # 含真实 mpv
```

`TestEnginePlaybackSequence` 按 player 的真实调用顺序走一遍播放链路
（起播→位置→暂停→恢复→seek→eof→Stop），守的是协议层时序正确性——
单条命令各自通过，整条链仍可能因读锁竞争、事件与响应交错而出错。

`TestFallbackOnLoadError` 守降档链：引擎异步载入失败要能换下一档重试。

## 依赖管理

`mygo` 用 `replace => ../mygo` 指向本地仓库，方便对着现行 main 调 API。
发布前需确认是否改回版本号。