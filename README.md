# 随译 SuiYi

轻量、后台常驻、跨平台的本地翻译服务：以腾讯 **Hy-MT2 1.8B** 翻译模型为核心，默认 **llama.cpp 本地离线推理**，也支持切换到任意 **OpenAI 兼容 API**。内置 Web 管理界面（Google 式左右对照翻译）、批量翻译、模型下拉切换、日志查看。

- 目标平台：Windows 10/11（首选）、macOS、Linux
- 技术栈：Go（应用层）+ llama.cpp（推理引擎）+ 原生 JS 单页
- 数据安全：本地后端全程离线，数据不出本机

---

## 功能特性

- **Google 式左右对照翻译**：原文/译文双栏、语言一键交换、输入即译（可关闭）
- **快捷翻译标签**：一键选择「中英 / 英中 / 日中 / 韩中 / 自动→中」，带彩色语言徽章
- **原文拖拽上传**：把 `.txt` 拖入原文框即可载入翻译
- **GPU 加速（Vulkan）**：`ngl=99` 启用 Radeon 780M 等 iGPU，生成约 53 t/s（CPU 2.5 倍）
- **流式输出 + 进度条**：长文本逐段流式渲染，进度条展示百分比 / 预计剩余秒数 / **token/s**
- **翻译可停止**：翻译过程中随时点「停止」，保留已生成部分；刷新页面也不会崩溃
- **长文本自动分段**：超长文本按自然句界切段翻译并拼接，携带「滚动前文」保持术语/语气一致，上下文近似无限
- **剪贴板即译**：复制外文自动翻译，Web 弹 toast 提示
- **开机自启**：设置页一键开启（Windows 注册表）
- **可选 token 鉴权**：设置 token 后 API 需鉴权，防本机程序滥用
- **多后端**：
  - `local`（默认）：本地 llama.cpp（llama-server 子进程），完全离线
  - `hymt`：hy-mt-rs 引擎，支持 AngelSlim 官方 **1.25bit（STQ1_0）** 与 **2bit（SEQ）** GGUF（体积小、省内存）
  - 模型切换时**按文件类型自动路由后端**（2bit/1.25bit → `hymt`，其余 → 本地 llama-server）
  - `openai`：任意 OpenAI 兼容 API（OpenAI / 通义 / DeepSeek / 本地自建等）
- **模型下拉切换**：把 `.gguf` 放入 `models/` 目录，Web 设置页下拉选择并「刷新」
- **批量翻译**：每行一条，串行处理，结果表格展示并可复制
- **运行日志**：应用日志 + 本地引擎日志分栏展示，切页自动加载
- **亮/暗色主题**：默认亮色，一键切换，本地记忆
- **跨平台构建**：GitHub Actions + CNB 双 CI 自动构建 11 平台 Release
- **桌面 GUI 与服务合一（Windows）**：单个 `suiyi.exe` 默认打开 Wails v2.12 + WebView2 原生窗口（内嵌完整 Web 界面），
  同时监听 `127.0.0.1:8848`，任意浏览器可直接访问；默认**不显示控制台黑窗口**（推理子进程也后台运行），
  加 `-debug` 才弹调试控制台；**左键单击托盘图标显示主窗口**，右键菜单为 **显示主窗口 / 打开浏览器 / 退出**，
  关闭窗口自动隐藏到托盘继续后台服务；重复启动会先清理旧实例与推理进程，不会端口冲突
- **长文阅读翻页（窗口内，浏览器里同样生效）**：译文较长、页面出现滚动条时，
  `Home`/`End` 直接跳到内容头/尾，`PageUp`/`PageDown` 上一页/下一页，方便通读长译文；
  没有长内容时这几个键完全不拦截，保留原生行为
- **分享截图**：原文面板左上角「分享截图」把 **原文 + 译文** 画成一张卡片图并**复制到剪贴板**
  （纯本地、不上传、不落盘），直接粘贴即可分享；浏览器不允许写图片剪贴板时改为下载 PNG

---

## 快速开始（Windows）

### 1. 准备运行时

```
suiyi/
├── suiyi.exe                 # 本程序（或 build\suiyi.exe）
├── third_party/windows/amd64/  # llama-server.exe + DLL（llama.cpp 预编译）
└── models/                   # GGUF 模型文件
    └── Hy-MT2-1.8B-Q4_K_M.gguf
```

- 从 llama.cpp 官方 Release 获取 `llama-server`（Windows CPU x64），放入 `third_party/windows/amd64/`
- 从 HuggingFace / ModelScope 下载 Hy-MT2 GGUF，放入 `models/`

> **关于 STQ 极低比特模型**：`1.25bit`（STQ1_0 / type 40）与 `2bit`（SEQ / type 41）是腾讯 AngelSlim 的
> 私有极低比特量化，**标准 llama.cpp 加载会报 `tensor '...' has offset X, expected Y`**。
> 本项目内置 hy-mt-rs 引擎（`third_party/windows/amd64/hymt/hy-mt.exe`）解码这两种格式：
> 把 GGUF 放进 `models/` 后，Web 设置页选中即用——**推理后端会按模型文件名自动切换**
> （含 `2bit` / `1.25bit` → `hymt`；其余如 `Q4_K_M` → 本地 llama-server），原理见
> [设计文档](设计文档.md) §2.3 与 §6.11。默认仍使用兼容性最好的 `Q4_K_M`。
>
> 实测（2026-09-14）：`Hy-MT2-1.8B-1.25bit-v2.gguf`、`Hy-MT1.5-1.8B-1.25bit.gguf`、
> `Hy-MT2-1.8B-2bit-v2.gguf` 三种均翻译正常；本地用脚本改过张量类型的
> `Hy-MT2-1.8B-1.25bit-v2.fixed.gguf` 会报 `unsupported ggml dtype id 43`，改用官方 `-v2.gguf` 即可。

### 2. 启动

方式一：双击运行（桌面窗口 + 本地服务，无控制台黑窗口）

```bat
build\suiyi.exe              :: 打开窗口，同时提供 http://127.0.0.1:8848
build\suiyi.exe -debug       :: 同上，额外弹出控制台窗口（看日志 / 排查启动失败）
```

方式二：本地构建脚本（杀进程 → 构建 → 启动 GUI）

```bat
build.bat                    :: 构建 build\suiyi.exe 并启动（无控制台）
build.bat debug              :: 构建并带控制台启动（-debug）
```

方式三：纯服务模式（无窗口，适合脚本 / 开机自启 / 服务器）

```bat
build\suiyi.exe serve              :: 托盘 + API/Web，无桌面窗口
build\suiyi.exe serve -headless    :: 无窗口、无托盘（Linux/macOS 服务器常用）
```

> 通用参数：`-port <n>` 覆盖 API 端口、`-ngl <n>` GPU 层数、`-debug` 显示控制台、
> `-headless` 不显示桌面窗口、`-notray` 不显示托盘图标。
> （`serve` 默认就是无窗口 + 托盘；`-headless` 在此之上再去掉托盘。）

> 关于控制台：Windows 版以 GUI 子系统构建（`-H=windowsgui`），默认不出现黑窗口；
> 需要日志时加 `-debug`，程序会动态分配控制台并把 stdout / 日志接进去。
> 启动失败且无控制台时，会弹出错误对话框（避免双击后毫无反馈）。
> 推理子进程（`llama-server` / `hy-mt`）同样以 `CREATE_NO_WINDOW` 后台运行，不会弹黑窗口
> （否则关掉那个黑窗口会连带杀掉推理进程，表现为「引擎启动失败」）。

> 重复启动：每次启动会先检查是否有旧实例在运行——是则先结束旧实例与推理进程（`taskkill /F /T`），
> 释放 8848 / 8849 端口后再启动，因此反复双击图标不会出现端口冲突。

> 窗口关闭行为：托盘可用时，点窗口 ✕ 会**隐藏到托盘**（服务与翻译接口继续运行），
> 需真正退出请用托盘菜单「退出」或窗口菜单「文件 → 退出」。

### 3. 使用 Web 界面

打开 <http://127.0.0.1:8848>

- **翻译**页：左侧输入原文，右侧出译文；顶部可换语言/交换
- **设置**页：
  - 推理后端：`本地 llama.cpp` 或 `OpenAI 兼容 API`（填基地址 / Key / 模型名）
  - 模型文件：下拉选择 `models/` 下的 GGUF，放入新模型后点「刷新」
  - 默认目标语言（默认中文）、端口等
  - 修改端口或切换后端后需**重启服务**生效

### 4. 长文翻页与分享截图

**长文翻页**（窗口与浏览器通用）：译文较长把页面撑出滚动条后，用下面这几个键通读译文。

| 按键 | 作用 |
|------|------|
| `Home` / `End` | 跳到内容最上 / 最下 |
| `PageUp` / `PageDown` | 上一页 / 下一页（约一屏） |

- **只在页面确实可滚动（存在长内容）时接管**；没有长内容时这几个键完全放行，保持原生行为
- 焦点在原文框里也一样接管（长文时读译文更常用）；想在文本内跳光标请用 `Ctrl+Home` / `Ctrl+End`
  （带 `Ctrl`/`Alt`/`Shift` 的按键一律放行，不抢系统快捷键）

**分享截图**：原文面板左上角「分享截图」→ 把「原文 + 译文」画成一张卡片图（品牌渐变条 + 语言对 +
时间 + 分栏 + 页脚）→ **复制到剪贴板**并提示。纯前端 canvas 绘制、纯本地操作，不上传任何图床、
不落盘；浏览器禁止写图片剪贴板时会改为下载 PNG 文件并提示。

### 4. 命令行单次翻译（服务需已运行）

```bat
build\suiyi.exe translate "你好世界" -t en
```

---

## OpenAI 兼容后端

在 Web 设置页选择 `OpenAI 兼容 API` 并填写：

| 字段 | 示例 |
|---|---|
| API 基地址 | `https://api.openai.com/v1`、`https://dashscope.aliyuncs.com/compatible-mode/v1`、`http://127.0.0.1:8000/v1` |
| API Key | `sk-...`（本地自建可留空） |
| 模型名 | `gpt-4o-mini`、`qwen-plus`、`deepseek-chat`、`hy-mt2-pro`（腾讯 TokenHub 在线推理的服务 ID）等 |

保存后重启服务生效。此时无需本地模型与 llama-server，`/health` 显示 `backend=openai`。

> **模型名要填服务方认的那个名字**：程序会把「模型名」原样写进 `/chat/completions` 的 `model` 字段
> （本地 llama-server 用的 `suiyi` 只是内部别名，不会发给第三方）。填错会直接收到对方的 400，
> 例如腾讯 TokenHub 会回 `The model or service ID xxx does not exist`；留空则本地直接提示
> 「未配置模型名/服务 ID」。改**模型名 / API 基地址 / Key** 都需要重启服务生效（保存时会有提示）。

---

## 本地 API

| 接口 | 方法 | 说明 |
|---|---|---|
| `/health` | GET | 服务与后端状态（`backend` + `engine`）；推理进程退出时 `engine=false`，自动重启后转回 `true` |
| `/translate` | POST | `{text, source?, target?, glossary?, style?}` |
| `/translate/stream` | POST | SSE 流式翻译（增量 `delta` + 进度 `progress`/`stage`/`eta`/`tps`） |
| `/languages` | GET | 支持语言列表 |
| `/models` | GET | `models/` 目录下的 GGUF 模型列表 |
| `/config` | GET/PUT | 配置读写（含 `backend`/`openai_*`/`n_ctx`） |
| `/logs` | GET | 应用日志 + 引擎日志尾部 |
| `/` | GET | Web 管理界面 |

---

## 构建与发布

### 本地构建

```bat
build.bat          :: 构建 build\suiyi.exe 并启动桌面窗口
build.bat debug    :: 构建并带 -debug 控制台启动
```

脚本流程：`终止 suiyi/llama-server 进程 → 生成图标资源 → go build → 启动应用`。
构建使用 `-tags production`（Wails 必须，否则走 dev 模式找不到前端）与
`-ldflags "-s -w -H=windowsgui"`（隐藏控制台，日志改由 `-debug` 按需分配）。
产物输出到 `build\suiyi.exe`，运行数据（配置/日志）在 `build\data\`。

### CI 自动构建

- **GitHub Actions**（`.github/workflows/build.yml`）：push `main`/`master` → 构建 11 平台 → 生成 `v{YYYY_MMDD}` Release
- **CNB**（`.cnb.yml`）：push `main` → 构建 + Release；push tag → 正式发版；PR → 质量门禁
- 版本号通过 `-ldflags "-X main.version=..."` 注入

### 仓库

- GitHub：<https://github.com/JackCh3n/SuiYi>
- CNB：<https://cnb.cool/jackch3n/SuiYi>

---

## 目录结构

```
suiyi/
├── main.go                    # 统一入口：GUI + 服务（serve / translate 子命令）
├── console_windows.go         # -debug 分配控制台；无可见输出时错误弹窗（console_other.go 为 noop）
├── browser_windows.go         # 打开默认浏览器（browser_other.go：open / xdg-open）
├── ui_windows.go + ui_other.go # 平台分支：Windows 走桌面窗口，其他平台走服务模式
├── instance_windows.go        # 启动前清理旧实例与残留推理进程（instance_other.go 为 noop）
├── build.bat                  # Windows 本地构建脚本（构建 + 启动，支持 debug）
├── internal/
│   ├── appcore/               # 服务核心聚合（引擎+队列+API，各入口共用）
│   ├── gui/                   # 桌面窗口（Wails v2.12 + WebView2，仅 Windows）
│   │   ├── gui_windows.go     #   窗口配置、原生菜单、前端 embed
│   │   ├── app_windows.go     #   Wails 绑定（版本/API 地址/显示隐藏/打开目录）
│   │   └── frontend/index.html #  窗口首页（重定向到本地 Web 界面）
│   ├── tray/                  # 系统托盘（纯 Win32：左键显示主窗口 / 右键菜单）
│   ├── engine/                # llama-server 子进程管理 + OpenAI 兼容客户端
│   │   └── proc_windows.go    #   子进程后台拉起（CREATE_NO_WINDOW，不弹黑窗口）
│   ├── api/                   # REST API + Web 静态资源(embed)
│   ├── queue/                 # 翻译队列（串行）
│   ├── config/                # 配置读写（多后端字段）
│   └── ...
├── assets/appicon.ico         # 应用图标（构建时用 rsrc 嵌入 exe / 托盘）
├── web/index.html             # Web 管理界面（Google 式左右对照翻译）
├── third_party/               # 各平台 llama-server（gitignore）
├── models/                    # GGUF 模型（gitignore）
├── .github/workflows/build.yml # GitHub Actions
├── .cnb.yml + .cnb/           # CNB 云原生构建
└── 设计文档.md                # 详细设计文档
```

---

## 常见问题

- **端口被占用 / 启动超时**：先 `taskkill /F /IM suiyi.exe`、`taskkill /F /IM llama-server.exe`，再运行 `build.bat`
- **模型加载报 `tensor ... has offset X, expected Y`**：模型为 STQ 极低比特量化，需 STQ 专用 llama.cpp 构建，或改用 `Q4_K_M`
- **修改端口不生效**：端口在启动时绑定，需重启服务
- **OpenAI 后端翻译失败**：检查基地址是否含 `/v1`、Key 是否有权限、模型名是否正确

## 许可与致谢

- 模型：腾讯 Hy-MT2（HuggingFace: `AngelSlim/Hy-MT2-*`，GitHub: `Tencent-Hunyuan/Hy-MT2`）
- 推理引擎：<https://github.com/ggml-org/llama.cpp>
