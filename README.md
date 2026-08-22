# 文件互传 (Filesync)

> 服务器中转暂存的文件互传工具：上传文件生成 **6 位邀请码**，凭码下载，**大文件分片上传 + 断点续传**，**自定义有效期**（1-168 小时），到期自动清理。
>
> 单二进制、纯 Go 无 CGO、Go embed 内嵌前端，跨平台编译。

**开源协议：[MIT](./LICENSE)** ✅

## 功能特性

- 📤 **分片上传**：大文件按 8MB 切片上传，服务端合并，避免整包上传超时与内存压力
- ⏯ **断点续传**：上传中断后自动续传（含刷新页面后重新选择同一文件续传）
- ⏰ **自定义有效期**：创建分享时可选 1/6/10/24 小时（后端支持 1-168 小时）
- 🔑 **邀请码即凭证**：6 位数字邀请码 + 失败锁定 + IP 限流防爆破
- 🛡 **安全**：HTTPS、安全响应头、路径穿越防护、磁盘配额、非 root 运行、systemd 沙箱
- 🔒 下载支持 **HTTP Range 断点续传**（`Accept-Ranges: bytes`）
- 🧹 过期分享由后台协程自动清理，无磁盘泄漏

---

## 快速入门

### 前置依赖

本地运行（开发/编译）需要：

| 依赖 | 版本 | 说明 |
| --- | --- | --- |
| Go | 1.26+ | 编译（`go.mod` 声明 `go 1.26.4`） |
| 网络 | - | 前端字体/图标走 CDN（亦可本地化，见"前端资源"） |

部署到生产服务器不需要 Go（用预编译二进制），仅需 systemd + nginx（可选）+ 域名与 HTTPS 证书。

### 安装

本地直接运行：

```bash
go build -o bin/filesync ./cmd/server
./bin/filesync -config config.yaml
# 访问 http://localhost:8080
```

一键部署到服务器：

```bash
curl -fsSL https://raw.githubusercontent.com/weiegr/filesync/main/deploy/install.sh \
  | FS_GITHUB_REPO=weiegr/filesync bash
```

或使用本地二进制部署：

```bash
bash deploy/install.sh --binary ./filesync-linux-amd64
```

脚本会自动完成：环境预检 → 引导提问 → 安装 nginx/certbot → 渲染配置 → 创建专用用户 → 启动 systemd → 申请 HTTPS 证书 → 健康自检。

---

## 使用说明

### 使用流程

1. 打开首页，点击「分享文件」
2. 选择文件（单文件 ≤ 500MB，最多 10 个），选择有效期
3. 大文件自动分片上传，支持断点续传；完成后生成 6 位邀请码，可一键复制
4. 对方在首页「填入分享码」输入邀请码 → 下载文件
5. 文件到期自动删除

### 管理命令

部署后可用 `deploy/install.sh` 管理服务（等价于 `systemctl` / `journalctl`）：

```bash
bash deploy/install.sh --status     # 查看服务状态 + 健康检查
bash deploy/install.sh --start      # 启动
bash deploy/install.sh --stop       # 暂停
bash deploy/install.sh --restart    # 重启
bash deploy/install.sh --logs       # 实时查看日志
bash deploy/install.sh --uninstall  # 卸载
```

安装完成后会生成全局管理命令 `filesync-ctl`，**任意目录**直接可用，无需进入仓库：

```bash
sudo filesync-ctl --status      # 状态
sudo filesync-ctl --restart     # 重启
sudo filesync-ctl --logs        # 日志
sudo filesync-ctl --uninstall   # 卸载
```

等价命令（systemctl 直管）：

```bash
systemctl status filesync        # 查看状态 + 最近日志
systemctl start filesync         # 启动
systemctl stop filesync          # 停止
systemctl restart filesync       # 重启
systemctl enable filesync        # 设置开机自启
systemctl disable filesync       # 取消开机自启
systemctl is-active filesync     # 仅返回 active/inactive
journalctl -u filesync -f        # 实时日志
journalctl -u filesync -n 100    # 最近 100 条日志
```

### 卸载

一键卸载（会停止服务、删除 systemd/nginx 配置、删除二进制与 filesync-ctl；数据与配置默认保留）：

```bash
sudo filesync-ctl --uninstall     # 任意目录直接卸
# 或
bash deploy/install.sh --uninstall
```

> 卸载脚本最后会询问是否删除数据目录 `/var/lib/filesync` 和配置目录 `/etc/filesync`，回答 `y` 才会清空数据。

手动卸载（等价，适合不用脚本的情况）：

```bash
systemctl stop filesync
systemctl disable filesync
rm -f /etc/systemd/system/filesync.service
systemctl daemon-reload
rm -f /etc/nginx/conf.d/filesync.conf
nginx -t && systemctl reload nginx   # 若有 nginx 配置
rm -f /usr/local/bin/filesync
rm -f /etc/filesync/.installed

# 确认不再需要数据后（谨慎，不可恢复）：
rm -rf /var/lib/filesync /etc/filesync
userdel filesync 2>/dev/null || true
```

---

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| POST | `/api/share` | multipart 上传，`files` 字段多文件，返回邀请码（兼容旧接口） |
| GET | `/api/share/:code` | 查询分享文件列表（邀请码即凭证） |
| GET | `/api/share/:code/files/:fileID` | 下载文件（支持 Range 断点续传） |
| POST | `/api/upload/init` | 初始化分片上传会话 `{name,size}` |
| POST | `/api/upload/:id/chunk?index=N` | 上传第 N 个分片（multipart 字段 `chunk`） |
| GET | `/api/upload/:id/status` | 查询已接收进度，用于断点续传 |
| POST | `/api/upload/complete` | 合并分片生成分享 `{uploadIds,expireHours?}` |
| GET | `/health` | 健康检查 |

前端统一走分片上传：每个文件先 `init` 拿 `uploadId`，按序传分片，最后 `complete` 合并为一个分享。自定义有效期（1-168 小时，0 用默认）在 `complete` 时通过 `expireHours` 传入。

---

## 配置 (config.yaml)

```yaml
server:
  port: 8080
  trustProxy: true       # 部署在 nginx 后需为 true

storage:
  dir: /var/lib/filesync/files
  maxTotalSize: 50GB     # 磁盘配额保护，超出拒绝新上传

limits:
  maxFileSize: 500MB
  maxFilesPerShare: 10
  expireHours: 10
  chunkSize: 8MB          # 分片上传每片大小
  uploadTTLHours: 24      # 未完成上传会话保留时长

security:
  rateLimitPerMin: 120       # 每 IP 每分钟请求上限
  shareCodeMaxAttempts: 5    # 邀请码连续失败次数
  shareCodeLockMinutes: 10   # 失败后锁定分钟

log:
  dir: /var/lib/filesync/logs
```

环境变量可覆盖：`FILESYNC_PORT`、`FILESYNC_DATA_DIR`、`FILESYNC_MAX_TOTAL`、`FILESYNC_MAX_FILE`、`FILESYNC_MAX_FILES`、`FILESYNC_EXPIRE_HOURS`、`FILESYNC_CHUNK_SIZE`、`FILESYNC_UPLOAD_TTL_HOURS`、`FILESYNC_LOG_DIR`。

---

## 架构说明

**传输模型：服务器中转暂存**（非 P2P）。文件上传到服务器磁盘，按邀请码生成分享，接收方凭邀请码从服务器下载。实现简单稳定，无需信令/打洞。

**核心流程：**

```
上传方                      服务器                       接收方
   │   分片上传 (init/chunk) │                            │
   ├───────────────────────►│  暂存 data/uploads/         │
   │                        │  complete 合并 → data/files │
   │◄───────────────────────┤  返回 6 位邀请码             │
   │                        │                            │ 输入邀请码
   │                        │◄───────────────────────────┤
   │                        │  下载 (支持 Range)          │
   │                        ├───────────────────────────►│
```

**分层：**

- **存储**：SQLite（纯 Go 无 CGO）+ 磁盘文件按邀请码分目录；上传临时区 `data/uploads/` 与正式分享区 `data/files/` 隔离
- **分片上传**：`/api/upload/{init,chunk,status,complete}` 四段式，按偏移顺序写入、`received` 记录连续字节；断点续传靠 `status` 查询 + 前端 localStorage 记忆会话；超时会话由清理协程回收
- **生命周期**：后台协程每分钟扫描，删除过期分享与超时上传会话（磁盘优先，再删 DB）
- **安全**：6 位邀请码 + 失败锁定(5次/10分钟) + IP 限流；路径穿越防护（`filepath.Rel` 校验）；SQL 全参数化；非 root 运行 + systemd 沙箱
- **发布**：GitHub Actions 按 tag 自动交叉编译（linux/darwin/windows × amd64/arm64），发布到 Releases 供 `install.sh` 一键拉取

**技术栈**：Go + Gin + SQLite(modernc.org/sqlite) + 原生 JS/HTML + Tailwind（CDN）+ Phosphor 图标（已本地化）。

---

## 安全说明

- **邀请码即访问凭证**：6 位数字 + 失败锁定(5次/10分钟) + IP 限流防爆破
- **路径穿越防护**：所有磁盘路径经 `filepath.Rel` 校验，必须落在存储目录内
- **SQL 注入防护**：全部参数化查询
- **文件名清洗**：拒绝路径分隔符 / `..` / 空字节，防头注入
- **非 root 运行**：专用系统用户 + systemd `ProtectSystem`/`NoNewPrivileges`/`PrivateTmp` 沙箱
- **磁盘配额**：后台实时统计占用，超限拒绝新上传 (HTTP 507)
- **HTTPS**：Let's Encrypt 证书自动续期 + HSTS + 安全响应头 (CSP/X-Frame-Options 等)

---

## 项目结构

```
├── cmd/server/                 # 入口 + embed 前端
│   └── web/                    # 前端页面 (index/upload/share-list)
│       └── vendor/phosphor/    # 本地化图标字体
├── internal/
│   ├── config/                 # 配置 (默认值 + YAML + 环境变量)
│   ├── model/                  # Share / File / Upload 结构体
│   ├── store/                  # SQLite + 磁盘文件 + 上传临时区 + 路径穿越防护
│   ├── service/                # 业务逻辑 + 分片上传 + 邀请码 + 过期清理
│   ├── middleware/             # 限流 / 安全头 / 失败锁定
│   ├── handler/                # share / upload / static / health
│   └── server/                 # gin 路由组装 + 优雅关闭
├── deploy/                     # 部署脚本 install.sh + 模板
├── .github/workflows/          # GitHub Actions 自动构建 Release
├── config.yaml                 # 默认配置
└── *_test.go                   # 单元/集成/安全测试
```

---

## 开发 / 测试

```bash
go build ./...          # 编译
go test ./... -cover    # 全部测试
go vet ./...            # 静态检查
```

测试覆盖：配置解析、文件存储安全（路径穿越）、完整 API 流程、边界（超限/超量）、失败锁定、限流、过期清理、磁盘配额、分片上传、断点续传、自定义有效期。

## 跨平台编译

```bash
# Linux amd64 (生产，纯静态)
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "-s -w" -o filesync-linux-amd64 ./cmd/server
# macOS / Windows 同理调整 GOOS/GOARCH
```

## 前端资源

前端已本地化图标字体（Phosphor），不依赖外部 CDN 即可显示；Inter 正文字体与 Tailwind 暂走 CDN。

## License

[MIT](./LICENSE) © 2026 文件互传 (Filesync)
