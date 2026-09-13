<p align="center">
  <b>WorkBuddy2API Web GUI</b><br>
  <sub>给 <a href="https://github.com/287775856/workbuddy2api">workbuddy2api</a> 网关配的可视化控制台</sub>
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/badge/Go-1.22+-00ADD8?logo=go&logoColor=white&style=flat-square">
  <img alt="React" src="https://img.shields.io/badge/React-18-61DAFB?logo=react&logoColor=white&style=flat-square">
  <img alt="TypeScript" src="https://img.shields.io/badge/TypeScript-5-3178C6?logo=typescript&logoColor=white&style=flat-square">
  <img alt="Deploy" src="https://img.shields.io/badge/Deploy-Docker_Compose-2496ED?logo=docker&logoColor=white&style=flat-square">
</p>

---

## 📖 这是什么

[workbuddy2api](https://github.com/287775856/workbuddy2api) 是一个把 CodeBuddy 账号变成 OpenAI 兼容 API 的网关。
它本身**只有命令行工具**：加账号要 `./login.sh`、签到要 `./signin.sh`、看积分要 `./credit.sh`、改配置要手改 `config.json`。

本项目为它补齐一个 **Web 控制台**，把上述操作全部搬进浏览器：

| 原来怎么做 | 现在怎么做 |
|---|---|
| `./login.sh` 看授权链接、终端按 y | **网页点「发起授权」**，弹出链接，自动检测登录完成，自动落盘并重启网关 |
| `git diff config.json` 手改配置 | **表单 / JSON 双模式在线编辑**，改前自动备份，非法 JSON 直接拦下 |
| `curl /status \| jq` 看账号池 | **仪表盘实时卡片** + 账号列表筛选/搜索，冷却倒计时自动走秒 |
| `./signin.sh` 批量签到 | **一键批量签到**，带逐账号进度与结果弹窗 |
| `./credit.sh` 看积分 | **账号行内查积分** + 批量查询，汇总到仪表盘 |
| 手动 `docker restart` 加载新账号 | **系统页一键重启**（可选，需解锁高危操作） |
| 用 curl 试接口 | **内置聊天测试台**，支持流式/非流式、推理内容、token 用量与首字延迟 |

> 面板是**独立服务**，只通过 HTTP API 调用网关，并直读磁盘上的 `auths/` 与 `config.json`。
> 它不修改网关代码，网关升级不受影响；面板挂掉也不影响网关转发。

## ✨ 功能一览

| 页面 | 能力 |
|---|---|
| 📊 **仪表盘** | 账号池可用/冷却/禁用计数、积分总览、在途与粘性会话、Token 过期预警、凭证目录问题文件提示、异常账号清单；每 15 秒自动刷新 |
| 👥 **账号管理** | 全量账号表格（状态/积分/成功失败/Token 有效期/最近活动）、按状态筛选、搜索、多选批量操作、单账号 签到·刷新·猫猫旅行·查积分·删除、详情弹窗（实时积分 + 猫档案 + 旅行状态）、手工导入凭证 |
| ➕ **添加账号** | 网页版 OAuth 设备授权，**支持国内版 / 国际版**：选择区域 → 生成授权链接 → 自动轮询 → 自动落盘 → 按配置自动重启网关加载 |
| 💬 **聊天测试** | 模型下拉（动态拉取网关模型表）、流式/非流式、System Prompt、temperature/max_tokens、推理过程（reasoning_content）折叠展示、token 用量与 TTFB、会话 ID 开关（验证会话粘性） |
| ⚙️ **网关配置** | 基础设置/定时任务/账号池冷却/上游超时/粘性开关/Redis 的分组表单，或直接编辑 JSON 源码；保存前自动备份原始文件，可一键恢复 |
| 🔧 **系统** | 网关身份校验（`service=workbuddy2api`）、容器状态与健康检查、面板运行信息、**网页修改登录口令**、任务历史、客户端接入示例 |

### 国内版 / 国际版

WorkBuddy 有两套独立的账号体系，面板两种都支持，登录时选择区域即可：

| 区域 | 上游域名 | 登录地址 |
|---|---|---|
| 国内版 `cn` | copilot.tencent.com / codebuddy.cn | `https://copilot.tencent.com/login?...` |
| 国际版 `global` | workbuddy.ai | `https://www.workbuddy.ai/login?...` |

登录成功后凭据里会写入对应的 `domain`；之后的面板操作（签到 / 查积分 / 猫猫旅行）
与网关侧的转发都会**按 domain 自动路由**到正确的区域，无需额外配置。

### 安全设计

面板能读到账号的 `accessToken` / `refreshToken`，等同于账号完全控制权，因此：

- **强制登录**：默认口令即启用鉴权（不会出现"无鉴权直通"），会话用 HttpOnly + SameSite=Strict Cookie；
- **登录防爆破**：同一来源连续失败 10 次锁定 10 分钟；
- **网页改密码**：需验证当前口令，新口令至少 6 位；改后**吊销全部会话**，凭据落盘持久化（重启仍生效）；
- **CSRF 纵深防御**：写操作校验 `Origin` 同源；
- **高危操作开关**：`dangerous_ops` 默认 **false**，删除账号 / 重启容器 / 恢复配置备份在 UI 上直接禁用；
- **只读模式**：`read_only=true` 可一键关闭所有写操作，适合仅监控场景；
- **破坏性写操作的安全网**：配置保存前先备份、临时文件 + 原子替换；凭证写入用 `0600` + 原子替换；`uid` 校验阻断路径穿越；删除账号要求在 URL 里显式带 `?confirm=<uid>`。

## 🚀 快速开始

### 方式一：Docker Compose（推荐）

```bash
git clone <本项目地址>
cd workbuddy2api-gui

# 按实际情况改两处：宿主机网关目录、面板口令
vi docker-compose.yml

docker compose up -d --build
```

访问 `http://<服务器IP>:8787`，默认账号 `admin` / `workbuddy`（**请立即修改**）。

> 部署前确认 `docker-compose.yml` 里的挂载路径与你的网关部署位置一致。
> 官方 README 的默认部署路径是 `/root/workbuddy2api`。

### 方式二：本地直接运行

```bash
# 1. 构建前端（产物会落到 internal/webui/dist，被 Go embed 打包）
cd web && npm install && npm run build && cd ..

# 2. 编译并启动
go build -o wbgui ./cmd/server
cp config.example.json config.json
vi config.json            # 改 auth_dir / config_file / ui.password
./wbgui -config config.json
```

浏览器打开 `http://127.0.0.1:8787`。

> 前端未构建也能编译运行：此时后端照常工作，页面会显示"前端未构建"的引导提示。

## ⚙️ 配置说明

完整字段见 [`config.example.json`](config.example.json)。

| 字段 | 默认 | 说明 |
|---|---|---|
| `listen` | `:8787` | 面板监听地址 |
| `gateway_url` | `http://127.0.0.1:7863` | workbuddy2api 网关地址 |
| `gateway_api_key` | 空 | 网关 `api_key`；**留空会自动从下面的 `config.json` 读取**，单机部署无需填写 |
| `auth_dir` | 自动探测 | 网关账号凭证目录（`auths/`），面板直接读写 |
| `auth_owner_uid` / `auth_owner_gid` | `-1` / `-1` | 落盘凭证后把属主改成该 uid/gid（`-1` = 不改）。**容器部署必设**为网关容器的运行用户，见下方 FAQ |
| `config_file` | 自动探测 | 网关配置文件（`config.json`），供配置页读写 |
| `backup_dir` | `./data/backups` | 配置备份目录；单文件挂载下备份会写到这里（见下方 FAQ） |
| `credentials_file` | 空 | 面板登录凭据持久化文件，**网页改密码必需**（`config.json` 常为只读挂载）。留空则系统页的「修改密码」按钮禁用 |
| `docker_container` | `workbuddy2api` | 网关容器名，供系统页查询/重启；留空则关闭重启能力 |
| `dangerous_ops` | `false` | 解锁删除账号 / 重启容器 / 恢复配置备份 |
| `read_only` | `false` | 全局只读：关闭一切写操作 |
| `refresh_config_on_login` | `true` | 网页登录成功后自动重启网关加载新账号（还需 `dangerous_ops`） |
| `timeout_seconds` | `120` | 调用网关与上游的超时 |
| `ui.username` / `ui.password` | `admin` / `workbuddy` | 面板登录凭据的**初始值**；网页改密码后会写入 `credentials_file` 并优先生效 |
| `ui.session_ttl` | `12h` | 会话有效期 |

`auth_dir` / `config_file` 留空或路径不存在时，启动时会自动在 `/root/workbuddy2api`、`./workbuddy2api`、`..` 中探测。

### 环境变量覆盖

所有配置项都有等价的 `WBGUI_*` 环境变量（非空才覆盖），便于容器部署：

`WBGUI_LISTEN` · `WBGUI_GATEWAY_URL` · `WBGUI_GATEWAY_API_KEY` · `WBGUI_AUTH_DIR` ·
`WBGUI_AUTH_OWNER_UID` · `WBGUI_AUTH_OWNER_GID` · `WBGUI_CONFIG_FILE` · `WBGUI_CREDENTIALS_FILE` ·
`WBGUI_CONTAINER` · `WBGUI_USERNAME` · `WBGUI_PASSWORD` · `WBGUI_SESSION_TTL` ·
`WBGUI_DANGEROUS_OPS`（bool） · `WBGUI_READ_ONLY`（bool） · `WBGUI_REFRESH_CONFIG_ON_LOGIN`（bool） ·
`WBGUI_BACKUP_DIR` · `WBGUI_TIMEOUT_SECONDS`

### 关于 docker.sock 挂载

`docker-compose.yml` 默认挂载 `/var/run/docker.sock`，用于「系统」页的一键重启。

**安全提示**：挂载 docker.sock 等价于把宿主机 Docker 控制权交给该容器（可通过它启动特权容器逃逸）。
如果不需要一键重启能力，注释掉该挂载行即可 —— 面板会自动降级（系统页显示"Docker 不可用"），
其余功能完全不受影响，重启网关改用命令行 `docker restart workbuddy2api`。

## 🔌 API 接口

面板自身也是一套 REST API，可用脚本/curl 自动化（支持 `Authorization: Bearer <token>` 代替 Cookie）。

```bash
# 取会话 token
TOKEN=$(curl -s -X POST http://127.0.0.1:8787/api/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"yourpassword"}' | jq -r .token)

# 账号池总览
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8787/api/overview | jq

# 批量签到（异步任务，返回 task id）
curl -s -X POST -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' -d '{"uids":[]}' \
  http://127.0.0.1:8787/api/tasks/checkin | jq

# 查询任务进度
curl -s -H "Authorization: Bearer $TOKEN" http://127.0.0.1:8787/api/tasks/checkin-1 | jq
```

| 方法 | 路径 | 说明 |
|---|---|---|
| `GET` | `/api/session` | 当前会话与运行模式（免登录可调，不含敏感信息） |
| `POST` | `/api/login` · `/api/logout` | 面板登录/注销 |
| `GET` | `/api/overview` | 仪表盘总览 |
| `GET` | `/api/accounts` | 账号列表（含问题文件） |
| `GET` | `/api/accounts/{uid}` | 账号详情（`?silent=true` 跳过上游调用） |
| `POST` | `/api/accounts/{uid}/checkin\|refresh\|travel\|credits` | 单账号操作 |
| `DELETE` | `/api/accounts/{uid}?confirm={uid}` | 删除凭证（高危） |
| `POST` | `/api/accounts/import` | 手工导入凭证 |
| `POST` | `/api/tasks/checkin\|refresh\|travel\|credits` | 批量任务（返回 task id） |
| `GET` | `/api/tasks` · `/api/tasks/{id}` | 任务历史 / 进度明细 |
| `POST` | `/api/login/start` | 发起 OAuth 授权，body `{"region":"cn"\|"global"}` 选择区域，返回 `auth_url` |
| `POST` | `/api/login/{id}/poll` | 轮询登录状态 |
| `POST` | `/api/password` | 修改面板登录口令（需当前口令；改后吊销全部会话） |
| `GET` | `/api/models` | 网关模型列表（透传 `/v1/models`） |
| `POST` | `/api/chat` · `/api/chat/stream` | 聊天（非流式 / SSE） |
| `GET`/`PUT` | `/api/config` | 读写网关 `config.json` |
| `POST` | `/api/config/reset` | 从备份恢复（高危） |
| `GET` | `/api/system` | 面板与容器运行信息 |
| `POST` | `/api/system/restart` | 重启网关容器（高危） |

## 🏗️ 架构

```
┌─────────────────────────────────────────────────────────────┐
│  浏览器（React SPA，embed 进二进制）                          │
│  仪表盘 · 账号管理 · 添加账号 · 聊天测试 · 网关配置 · 系统     │
└───────────────────────────┬─────────────────────────────────┘
                            │ REST + SSE（Cookie / Bearer）
┌───────────────────────────▼─────────────────────────────────┐
│  wbgui（Go）:8787                                            │
│  ├── api/        鉴权中间件 · REST 路由 · SSE 转发            │
│  ├── ops/        账号聚合 · 批量任务 · OAuth 流程 · 配置读写   │
│  ├── gateway/    HTTP 客户端 → workbuddy2api 的 /status 等    │
│  ├── upstream/   直连腾讯 CodeBuddy（登录/签到/积分/旅行）     │
│  └── authstore/  读写 auths/workbuddy-<uid>.json             │
└──────┬──────────────────────┬───────────────────┬───────────┘
       │ HTTP API             │ 直读/写入         │ docker
       ▼                      ▼                   ▼
┌──────────────┐   ┌────────────────────┐   ┌──────────────┐
│ workbuddy2api│   │ auths/*.json       │   │ 容器重启      │
│   :7863      │   │ config.json        │   │ docker restart│
└──────┬───────┘   └────────────────────┘   └──────────────┘
       │
       ▼
  CodeBuddy 上游（copilot.tencent.com）
```

### 为什么面板要直连上游？

网关没有暴露「发起 OAuth 登录」「签到」「查积分」这类接口（它只做转发）。
要让网页版登录与签到成为可能，面板必须自己实现这几个上游调用 —— 因此
`internal/upstream` 复刻了网关 `internal/upstream` 的请求头、路径与信封格式，
保证同一账号在两侧的请求指纹一致。

### 目录结构

```
├── cmd/server/            程序入口（配置装配、启动自检日志）
├── internal/
│   ├── api/                HTTP 接口层（鉴权 / 路由 / SSE 转发）
│   ├── authstore/          凭证文件读写（原子写、uid 校验防穿越）
│   ├── config/             面板自身配置
│   ├── gateway/            workbuddy2api 网关客户端
│   ├── ops/                业务操作层（账号聚合、批量任务、OAuth、配置编辑、Docker）
│   ├── upstream/           腾讯 CodeBuddy 直连客户端
│   └── webui/              前端产物 embed + SPA 回落
└── web/                    React + TypeScript 前端（Vite）
    └── src/pages/          六个页面
```

## 🛠️ 开发

```bash
# 终端 1：后端（默认 8787）
go run ./cmd/server -config config.json

# 终端 2：前端热更新（5173，/api 自动代理到 8787）
cd web && npm run dev
```

改完前端后重新构建：`cd web && npm run build` 然后重新编译 Go 二进制。

### 测试

```bash
# Go 单元测试（含 -race）
go test -race ./internal/...

# 前端类型检查
cd web && npx tsc -b
```

单元测试覆盖的关键路径：

| 包 | 覆盖内容 |
|---|---|
| `fsutil` | 原子写入、bind mount 回退、截断彻底性、权限 0600 |
| `config` | 默认值、环境变量覆盖、**网关配置误加载的拒绝逻辑**、非法 TTL |
| `authstore` | 双格式解析、**uid 路径穿越防护**、空 token 拒写、坏文件容错、权限、**落盘属主 chown** |
| `api` | 口令校验、**登录防爆破锁定**、会话过期、Origin 同源校验、**网页改密码全流程与持久化** |
| `upstream` | 错误分类优先级、文案可读性（HTML 错误页净化为中文建议）、幂等判定、**CN/GLOBAL 区域路由** |
| `ops` | 批量任务聚合、panic 隔离、淘汰上限、并发追加（-race） |

### 已知部署陷阱（开发时踩过，已处理）

1. **面板配置 vs 网关配置混淆** —— compose 早期版本把网关 `config.json` 挂到了面板的配置路径上，
   面板会静默解析它并监听到网关端口 `7863`（端口冲突）。现在服务端会**直接拒绝启动**并给出指引，
   compose 也改为把网关配置挂到独立路径 `/gateway/config.json`。
2. **单文件挂载导致 `rename` 失败** —— `docker-compose` 的 `- /host/config.json:/gateway/config.json`
   是单文件挂载，`rename()` 替换它会报 `device or resource busy`，导致"保存配置"直接失败。
   现在自动回退为原地写入（并在 UI 中说明）。
3. **备份写到容器可写层丢失** —— 承上，单文件挂载时其父目录不是宿主机目录，备份写在那里会随容器
   重建消失。现在会检测设备号并改写到持久化的 `backup_dir`。
4. **挂载路径的权限** —— 凭证与配置通常是宿主机 root 的 0600 文件；compose 默认以 `user: "0:0"`
   运行以便读写，如需非 root 请自行调整属主（README 的 compose 注释里有说明）。
5. **面板写的凭证网关读不到** —— 面板 root 写 `root:0600`，网关以 uid 10001 读，权限拒绝导致
   「账号已添加但池中未加载」，且**重启网关也无效**（每次扫描都跳过同一个文件）。
   现在用 `auth_owner_uid` / `auth_owner_gid` 让面板落盘后自动 chown，并在 UI 直接给出诊断。

## ❓ 常见问题

**Q：面板显示"网关不可达"？**
确认网关容器在运行（`docker ps`），且 `gateway_url` 可达。容器部署时注意
`127.0.0.1` 指的是**面板容器自己**，要用宿主机地址（如 `http://172.17.0.1:7863`）或改用同一 compose 网络。

**Q：账号页/聊天测试报 401，但仪表盘正常？**
`gateway_api_key` 与网关 `config.json` 里的 `api_key` 不一致。留空该字段让面板自动读取即可。

**Q：网页登录报"申请授权失败"？**
面板所在服务器无法访问对应区域的上游（`copilot.tencent.com` 或 `workbuddy.ai`）。
可在能出网的地方用 `./login.sh [cn|global]` 登录，再用「账号管理 → 导入凭证」把
`auths/workbuddy-*.json` 的内容粘贴进来。

**Q：加了账号，但账号管理页显示「未加载」/ 账号池里没有？**

两个原因，按顺序排查：

1. **凭证文件权限让网关读不到**（容器部署最常见，**重启也没用**）。
   面板以 root 写凭证（`root:0600`），网关以低权限用户运行（官方镜像是 uid 10001 的 `app`），
   `open()` 直接 permission denied，网关启动扫描时静默跳过该文件。
   面板现在会在仪表盘直接给出这条告警（含具体 chown 命令）。根治办法是设置：
   ```json
   "auth_owner_uid": 10001,
   "auth_owner_gid": 10001
   ```
   面板落盘后会自动 chown 成网关用户。**注意**：`10001` 是官方网关镜像的运行用户，
   若你改了镜像或 `user:`，请填实际值（`docker inspect <网关容器> --format '{{.Config.User}}'`）。
2. **网关还没重新扫描目录**。网关只在启动时读 `auths/`，到「系统」页点「重启网关」，
   或命令行 `docker restart workbuddy2api`。

**Q：点了「重启网关」，账号还是没加载？**
先看第 1 条。权限不对时，重启只是把同样的文件再跳过一遍 —— 这正是"重启无效"的原因。

**Q：改配置了但没生效？**
`config.json` 只在网关启动时读取，需要重启。

**Q：保存配置时提示「原地写入」？**
说明 `config.json` 是以 **Docker 单文件**方式挂载的（compose 里写 `- /host/config.json:/gateway/config.json`）。
这种情况下 `rename()` 无法替换被挂载的 inode（会报 `device or resource busy`），面板会自动回退为
原地写入。功能不受影响，只是这一步不是原子的。若希望完全原子，可改为挂载整个目录：

```yaml
- /root/workbuddy2api:/gateway   # 挂目录而非单文件
```

**Q：备份文件在哪里？为什么不在 config.json 旁边？**
单文件挂载时，`config.json` 的父目录在容器内属于容器可写层，**不是宿主机目录** ——
备份写在那里会随容器重建一起消失。因此面板会检测到这种情况，把备份写到 `backup_dir`
（compose 已挂载为 `./data:/data`）。配置页会显示备份的**实际路径**。

**Q：「删除」按钮是灰的？**
两种原因：① 高危操作未解锁（见上一条）；② 该账号在网关池中但**没有本地凭证文件**
（说明面板挂载的 `auths/` 与网关挂载的不是同一个目录）。鼠标悬停会显示具体原因。

**Q：为什么"删除账号"按钮是灰的？**
高危操作默认锁定。在配置里设 `dangerous_ops: true`（或 `WBGUI_DANGEROUS_OPS=true`）后解锁。

## ⚠️ 合规提示

本项目是 workbuddy2api 的**管理界面**，不改变其性质：请仅用于**本人授权账号**的
本机/私有环境测试。面板会读写账号凭证，请务必：

- 修改默认口令，不要暴露到公网（如需公网访问，请置于 HTTPS 反代 + 强口令之后）；
- 谨慎开启 `dangerous_ops` 与 `docker.sock` 挂载；
- 定期备份 `auths/` 目录。

## 📄 License

与上游 workbuddy2api 保持一致（见 [LICENSE](LICENSE)）。
