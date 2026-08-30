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
  - `hymt`：hy-mt-rs 引擎，支持 AngelSlim 官方 **1.25bit（STQ1_0）** GGUF（440MB，省内存）
  - `openai`：任意 OpenAI 兼容 API（OpenAI / 通义 / DeepSeek / 本地自建等）
- **模型下拉切换**：把 `.gguf` 放入 `models/` 目录，Web 设置页下拉选择并「刷新」
- **批量翻译**：每行一条，串行处理，结果表格展示并可复制
- **运行日志**：应用日志 + 本地引擎日志分栏展示，切页自动加载
- **亮/暗色主题**：默认亮色，一键切换，本地记忆
- **跨平台构建**：GitHub Actions + CNB 双 CI 自动构建 11 平台 Release
- **桌面 GUI（Windows）**：Wails v2.12 + WebView2 原生窗口（`build-gui.bat`），内嵌完整 Web 界面

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

> **关于 STQ 模型**：`1.25bit-v2` 等使用 AngelSlim STQ 极低比特量化，**标准 llama.cpp 无法正确解码**
> （PR #22836 的 x86 内核问题）。使用 `1.25bit-v2` 时请切换 **`hymt` 后端**（hy-mt-rs 引擎，
> 见 [设计文档](设计文档.md) §2.3）；`2bit-v2`（SEQ）当前无可用引擎（llama.cpp / hy-mt 均不支持）。
> 默认仍使用 `Q4_K_M`。

### 2. 启动

方式一：直接运行

```bat
build\suiyi.exe serve        :: 默认 http://127.0.0.1:8848
```

方式二：本地构建脚本（杀进程 → 构建 → 启动并打开浏览器）

```bat
build.bat
```

方式三：桌面 GUI（Wails v2.12 + WebView2 原生窗口，Windows）

```bat
build-gui.bat          :: 构建 build\suiyi-gui.exe
build\suiyi-gui.exe    :: 运行（窗口内加载本地 Web 界面）
```

### 3. 使用 Web 界面

打开 <http://127.0.0.1:8848>

- **翻译**页：左侧输入原文，右侧出译文；顶部可换语言/交换
- **设置**页：
  - 推理后端：`本地 llama.cpp` 或 `OpenAI 兼容 API`（填基地址 / Key / 模型名）
  - 模型文件：下拉选择 `models/` 下的 GGUF，放入新模型后点「刷新」
  - 默认目标语言（默认中文）、端口等
  - 修改端口或切换后端后需**重启服务**生效

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
| 模型名 | `gpt-4o-mini`、`qwen-plus`、`deepseek-chat` 等 |

保存后重启服务生效。此时无需本地模型与 llama-server，`/health` 显示 `backend=openai`。

---

## 本地 API

| 接口 | 方法 | 说明 |
|---|---|---|
| `/health` | GET | 服务与后端状态（含 `backend`、`engine`） |
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
build.bat
```

脚本流程：`终止 suiyi/llama-server 进程 → go build → 启动服务 → 打开浏览器`。
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
├── main.go                    # 入口：服务 + API
├── build.bat                  # Windows 本地构建脚本（服务版）
├── build-gui.bat              # Windows 本地构建脚本（桌面 GUI 版）
├── internal/
│   ├── appcore/               # 服务核心聚合（引擎+队列+API，CLI/GUI 共用）
│   ├── engine/                # llama-server 子进程管理 + OpenAI 兼容客户端
│   ├── api/                   # REST API + Web 静态资源(embed)
│   ├── queue/                 # 翻译队列（串行）
│   ├── config/                # 配置读写（多后端字段）
│   └── ...
├── gui/                       # 桌面 GUI（Wails v2.12 + WebView2）
│   ├── main.go                #   GUI 入口（启动服务核心 + 窗口）
│   ├── app.go                 #   Wails 绑定（版本/API 地址/打开目录）
│   └── frontend/index.html    #   窗口首页（重定向到本地 Web 界面）
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
