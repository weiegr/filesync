#!/usr/bin/env bash
# =============================================================================
# 文件互传应用 - 自动引导式部署脚本
# 用法:
#   bash install.sh                     # 交互式引导部署
#   bash install.sh --binary ./filesync # 使用本地二进制部署
#   bash install.sh --release <url>     # 从 URL 下载二进制部署
#   bash install.sh --status            # 查看服务状态
#   bash install.sh --start             # 启动服务
#   bash install.sh --stop              # 停止服务
#   bash install.sh --restart           # 重启服务
#   bash install.sh --logs              # 实时查看日志
#   bash install.sh --uninstall         # 干净卸载
#   bash install.sh --help
#
# 一键安装 (从 GitHub 拉取):
#   curl -fsSL https://raw.githubusercontent.com/<user>/filesync/main/deploy/install.sh | bash
#
# 环境预检 -> 引导提问 -> 安装 -> 配置 -> 启动 -> 自检
# =============================================================================
set -euo pipefail

# ---------- 常量 ----------
INSTALL_MARK="/etc/filesync/.installed"
CONF_DIR="/etc/filesync"
DATA_DIR="/var/lib/filesync"
BIN_DIR="/usr/local/bin"
APP_USER="filesync"
APP_SERVICE="filesync.service"
NGINX_CONF="/etc/nginx/conf.d/filesync.conf"
APP_DOMAIN=""
APP_PORT=8080
MAX_TOTAL="50GB"
MAX_FILE="500MB"
MAX_FILES=10
EXPIRE_HOURS=10
ADMIN_EMAIL=""
MEMORY_MAX="1G"
NGINX_MAX_BODY="500m"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]:-$0}")" && pwd)"
# 二进制来源（本地上传路径 或 URL）
BINARY_SRC=""

# ---------- 工具函数 ----------
log()  { echo -e "\033[1;32m[OK]\033[0m $*"; }
info() { echo -e "\033[1;34m[i]\033[0m $*"; }
warn() { echo -e "\033[1;33m[!]\033[0m $*"; }
fail() { echo -e "\033[1;31m[FAIL]\033[0m $*"; exit 1; }

ask() {
    local prompt="$1" default="$2" var="$3" input
    if [ -n "$default" ]; then
        read -r -p "$prompt (默认: $default): " input
    else
        read -r -p "$prompt: " input
    fi
    eval "$var='${input:-$default}'"
}

# ---------- 帮助 ----------
show_help() {
    cat <<'EOF'
文件互传应用 - 自动引导式部署

部署:
  bash install.sh                       交互式引导部署
  bash install.sh --binary ./filesync   使用本地二进制部署
  bash install.sh --release <url>       从 URL 下载二进制部署

管理 (部署后):
  bash install.sh --status   查看服务状态
  bash install.sh --start    启动服务
  bash install.sh --stop     停止服务
  bash install.sh --restart  重启服务
  bash install.sh --logs     实时查看日志

其他:
  bash install.sh --uninstall  卸载
  bash install.sh --help       帮助

流程: 环境预检 -> 引导提问 -> 安装依赖 -> 渲染配置 -> 启动 -> 自检
EOF
}

# ---------- 管理命令 ----------
do_status() {
    systemctl status "$APP_SERVICE" --no-pager 2>&1
    echo "--- 健康检查 ---"
    curl -fsS "http://127.0.0.1:${APP_PORT:-8080}/health" 2>/dev/null && echo "" || echo "服务未响应"
}
do_start() { systemctl start "$APP_SERVICE" && echo "已启动 $APP_SERVICE"; }
do_stop()  { systemctl stop "$APP_SERVICE" && echo "已停止 $APP_SERVICE"; }
do_restart(){ systemctl restart "$APP_SERVICE" && echo "已重启 $APP_SERVICE"; }
do_logs()  { journalctl -u "$APP_SERVICE" -f "$@"; }

# ---------- 卸载 ----------
uninstall() {
    info "开始卸载..."
    systemctl stop "$APP_SERVICE" 2>/dev/null || true
    systemctl disable "$APP_SERVICE" 2>/dev/null || true
    rm -f "/etc/systemd/system/$APP_SERVICE"
    systemctl daemon-reload

    rm -f "$NGINX_CONF"
    if command -v nginx >/dev/null 2>&1; then
        nginx -t 2>/dev/null && systemctl reload nginx 2>/dev/null || true
    fi

    rm -f "$BIN_DIR/filesync"
    rm -f "$INSTALL_MARK"

    local ans=n
    if [ -z "${FS_NONINTERACTIVE:-}" ]; then
        read -r -p "是否删除所有数据目录 $DATA_DIR 和配置 $CONF_DIR? [y/N]: " ans
    fi
    if [[ "${ans,,}" == "y" ]]; then
        userdel -r "$APP_USER" 2>/dev/null || true
        rm -rf "$DATA_DIR" "$CONF_DIR"
        info "数据与配置已删除"
    else
        userdel "$APP_USER" 2>/dev/null || true
        info "保留数据与配置"
    fi
    log "卸载完成"
}

# ---------- 环境预检 ----------
precheck() {
    info "=== 环境预检 ==="
    [ "$(id -u)" -eq 0 ] || fail "需要 root 权限 (请用 sudo 运行)"

    if [ -f /etc/os-release ]; then
        . /etc/os-release
        info "操作系统: $PRETTY_NAME"
    else
        fail "无法识别操作系统"
    fi
    case "$ID" in
        debian|ubuntu|centos|rhel|rocky|fedora) : ;;
        *) warn "未验证的操作系统: $ID" ;;
    esac

    command -v systemctl >/dev/null 2>&1 || fail "未检测到 systemd"
    info "systemd 正常"

    if command -v apt-get >/dev/null 2>&1; then PKG_MGR="apt"
    elif command -v yum >/dev/null 2>&1; then PKG_MGR="yum"
    elif command -v dnf >/dev/null 2>&1; then PKG_MGR="dnf"
    else fail "未找到包管理器 (apt/yum/dnf)"; fi
    info "包管理器: $PKG_MGR"

    for p in 80 443; do
        if command -v ss >/dev/null 2>&1 && ss -tlnp 2>/dev/null | grep -q ":$p "; then
            warn "端口 $p 已占用 (可能已有 web 服务)"
        fi
    done
    info "=== 预检通过 ==="
}

# ---------- 引导提问 ----------
prompt_config() {
    APP_DOMAIN="${FS_APP_DOMAIN:-$APP_DOMAIN}"
    APP_PORT="${FS_APP_PORT:-$APP_PORT}"
    MAX_TOTAL="${FS_MAX_TOTAL:-$MAX_TOTAL}"
    MAX_FILE="${FS_MAX_FILE:-$MAX_FILE}"
    MAX_FILES="${FS_MAX_FILES:-$MAX_FILES}"
    EXPIRE_HOURS="${FS_EXPIRE_HOURS:-$EXPIRE_HOURS}"
    ADMIN_EMAIL="${FS_ADMIN_EMAIL:-$ADMIN_EMAIL}"

    if [ -n "${FS_NONINTERACTIVE:-}" ]; then
        info "非交互模式，使用环境变量/默认配置"
        info "域名=$APP_DOMAIN 端口=$APP_PORT 总容量=$MAX_TOTAL 单文件=$MAX_FILE 文件数=$MAX_FILES 有效期=${EXPIRE_HOURS}h"
    else
        info "=== 部署引导 (回车采用默认值) ==="
        echo ""
        ask "域名 (留空则用 IP 访问，不申请 HTTPS)" "$APP_DOMAIN" APP_DOMAIN
        if [ -n "$APP_DOMAIN" ]; then
            echo "$APP_DOMAIN" | grep -qE '^[a-zA-Z0-9.-]+\.[a-zA-Z]{2,}$' || { warn "域名格式可能不正确，继续..."; }
        fi
        ask "应用端口 (nginx 反代到此端口)" "$APP_PORT" APP_PORT
        [[ "$APP_PORT" =~ ^[0-9]+$ ]] && [ "$APP_PORT" -ge 1024 ] && [ "$APP_PORT" -le 65535 ] || fail "端口必须在 1024-65535"
        ask "暂存总容量上限 (保护磁盘)" "$MAX_TOTAL" MAX_TOTAL
        ask "单文件大小上限" "$MAX_FILE" MAX_FILE
        ask "单分享最多文件数" "$MAX_FILES" MAX_FILES
        [[ "$MAX_FILES" =~ ^[0-9]+$ ]] && [ "$MAX_FILES" -ge 1 ] && [ "$MAX_FILES" -le 100 ] || fail "文件数必须在 1-100"
        ask "分享有效期 (小时)" "$EXPIRE_HOURS" EXPIRE_HOURS
        [[ "$EXPIRE_HOURS" =~ ^[0-9]+$ ]] && [ "$EXPIRE_HOURS" -ge 1 ] && [ "$EXPIRE_HOURS" -le 168 ] || fail "有效期必须在 1-168 小时"
        ask "管理员邮箱 (用于 HTTPS 证书申请，可选)" "$ADMIN_EMAIL" ADMIN_EMAIL
    fi

    [[ "$APP_PORT" =~ ^[0-9]+$ ]] && [ "$APP_PORT" -ge 1024 ] && [ "$APP_PORT" -le 65535 ] || fail "端口必须在 1024-65535"
    [[ "$MAX_FILES" =~ ^[0-9]+$ ]] && [ "$MAX_FILES" -ge 1 ] && [ "$MAX_FILES" -le 100 ] || fail "文件数必须在 1-100"
    [[ "$EXPIRE_HOURS" =~ ^[0-9]+$ ]] && [ "$EXPIRE_HOURS" -ge 1 ] && [ "$EXPIRE_HOURS" -le 168 ] || fail "有效期必须在 1-168 小时"

    local file_num="${MAX_FILE//[!0-9]/}"
    if [ "$file_num" -ge 1024 ]; then
        NGINX_MAX_BODY="$((file_num/1024))g"
    else
        NGINX_MAX_BODY="${file_num}m"
    fi
    info "配置完成"
    echo ""
}

# ---------- 获取二进制 ----------
acquire_binary() {
    info "=== 获取二进制 ==="
    local dest="$BIN_DIR/filesync"

    # 若目标已存在且服务正在运行，覆盖会报 Text file busy，先删除旧文件
    rm -f "$dest"

    # 1. 用户指定本地二进制
    if [ -n "$BINARY_SRC" ] && [ -f "$BINARY_SRC" ]; then
        info "使用本地二进制: $BINARY_SRC"
        cp "$BINARY_SRC" "$dest"
    # 2. 用户指定 URL (如 GitHub release)
    elif [ -n "$BINARY_SRC" ]; then
        info "从 URL 下载: $BINARY_SRC"
        curl -fsSL -o "$dest" "$BINARY_SRC"
    # 3. 从环境变量 FS_BINARY_URL 下载
    elif [ -n "${FS_BINARY_URL:-}" ]; then
        info "从环境变量 FS_BINARY_URL 下载"
        curl -fsSL -o "$dest" "$FS_BINARY_URL"
    # 4. 当前目录已有一键安装拉取的二进制
    elif [ -f "$SCRIPT_DIR/filesync" ]; then
        info "使用脚本同目录二进制: $SCRIPT_DIR/filesync"
        cp "$SCRIPT_DIR/filesync" "$dest"
    # 5. 默认从 GitHub release 下载 (需配置 FS_GITHUB_REPO)
    elif [ -n "${FS_GITHUB_REPO:-}" ]; then
        local url="https://github.com/${FS_GITHUB_REPO}/releases/latest/download/filesync-linux-amd64"
        info "从 GitHub release 下载: $url"
        curl -fsSL -o "$dest" "$url"
    else
        fail "未找到二进制。请用 --binary ./filesync 指定本地文件，或用 --release <url> 指定下载地址"
    fi

    chmod 755 "$dest"
    log "二进制已安装: $dest"
    ls -la "$dest"
}

# ---------- 准备模板 (自包含/管道模式) ----------
# 当脚本从 GitHub 拉取执行(无本地模板)时, 从 GitHub raw 下载模板到临时目录
prepare_templates() {
    if [ -d "$SCRIPT_DIR/templates" ]; then
        return  # 已有本地模板
    fi
    if [ -n "${FS_GITHUB_REPO:-}" ]; then
        info "从 GitHub 拉取部署模板..."
        local base="https://raw.githubusercontent.com/${FS_GITHUB_REPO}/main/deploy/templates"
        local tmp="/tmp/filesync-deploy-$$"
        mkdir -p "$tmp/templates"
        for f in config.yaml.tmpl filesync.service.tmpl nginx.conf.tmpl; do
            curl -fsSL -o "$tmp/templates/$f" "$base/$f" || fail "拉取模板 $f 失败"
        done
        SCRIPT_DIR="$tmp"
        trap "rm -rf '$tmp'" EXIT
        log "模板已就绪"
    else
        fail "缺少 templates 目录。请使用仓库完整部署，或设置 FS_GITHUB_REPO 从 GitHub 拉取"
    fi
}

# ---------- 渲染配置 ----------
render_config() {
    info "=== 渲染配置 ==="
    mkdir -p "$CONF_DIR" "$DATA_DIR/files" "$DATA_DIR/logs"

    # config.yaml
    sed -e "s/{{PORT}}/$APP_PORT/g" \
        -e "s|{{DATA_DIR}}|$DATA_DIR|g" \
        -e "s|{{MAX_TOTAL}}|$MAX_TOTAL|g" \
        -e "s|{{MAX_FILE}}|$MAX_FILE|g" \
        -e "s|{{MAX_FILES}}|$MAX_FILES|g" \
        -e "s|{{EXPIRE_HOURS}}|$EXPIRE_HOURS|g" \
        "$SCRIPT_DIR/templates/config.yaml.tmpl" > "$CONF_DIR/config.yaml"
    # 目录 755 让 filesync 用户可进入，文件 0640 root:filesync 可读
    # (属主在 setup_user 创建用户后设置)
    chmod 755 "$CONF_DIR"
    chmod 0640 "$CONF_DIR/config.yaml"
    log "config.yaml 已写入"

    # systemd unit
    sed -e "s|{{DATA_DIR}}|$DATA_DIR|g" \
        -e "s|{{BIN_DIR}}|$BIN_DIR|g" \
        -e "s|{{CONF_DIR}}|$CONF_DIR|g" \
        -e "s|{{MEMORY_MAX}}|$MEMORY_MAX|g" \
        "$SCRIPT_DIR/templates/filesync.service.tmpl" > "/etc/systemd/system/$APP_SERVICE"

    # nginx 配置 (有域名时)
    if [ -n "$APP_DOMAIN" ]; then
        sed -e "s|{{APP_DOMAIN}}|$APP_DOMAIN|g" \
            -e "s|{{PORT}}|$APP_PORT|g" \
            -e "s|{{NGINX_MAX_BODY}}|$NGINX_MAX_BODY|g" \
            "$SCRIPT_DIR/templates/nginx.conf.tmpl" > "$NGINX_CONF"
    fi
    log "配置已生成"
}

# ---------- 安装依赖 ----------
install_deps() {
    info "=== 安装依赖 ==="
    if ! command -v nginx >/dev/null 2>&1; then
        info "安装 nginx..."
        case "$PKG_MGR" in
            apt) apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nginx ;;
            yum) yum install -y -q nginx ;;
            dnf) dnf install -y -q nginx ;;
        esac
    fi
    log "nginx 就绪"

    if [ -n "$APP_DOMAIN" ] && ! command -v certbot >/dev/null 2>&1; then
        info "安装 certbot..."
        case "$PKG_MGR" in
            apt) DEBIAN_FRONTEND=noninteractive apt-get install -y -qq certbot python3-certbot-nginx ;;
            yum) yum install -y -q certbot python3-certbot-nginx ;;
            dnf) dnf install -y -q certbot python3-certbot-nginx ;;
        esac
    fi
    log "certbot 就绪 (如适用)"
}

# ---------- 创建用户与目录 ----------
setup_user() {
    info "=== 创建用户与目录 ==="
    if ! id "$APP_USER" >/dev/null 2>&1; then
        useradd --system --no-create-home --shell /usr/sbin/nologin "$APP_USER"
        log "创建系统用户 $APP_USER"
    fi
    chown -R "$APP_USER:$APP_USER" "$DATA_DIR"
    chmod 0700 "$DATA_DIR"
    # config.yaml 属主设为 root:filesync，filesync 用户可读
    chown root:"$APP_USER" "$CONF_DIR/config.yaml" 2>/dev/null || true
    log "目录权限设置完成"
}

# ---------- 启动 ----------
start_service() {
    info "=== 启动服务 ==="
    systemctl daemon-reload
    systemctl enable "$APP_SERVICE" >/dev/null 2>&1
    systemctl restart "$APP_SERVICE"
    log "应用服务已启动"

    if [ -f "$NGINX_CONF" ]; then
        systemctl enable nginx >/dev/null 2>&1 || true
        systemctl restart nginx 2>/dev/null || systemctl reload nginx 2>/dev/null || true
        log "nginx 已启动"
    fi

    if [ -n "$APP_DOMAIN" ]; then
        local certbot_args=(--nginx -d "$APP_DOMAIN" --redirect --non-interactive --agree-tos)
        if [ -n "$ADMIN_EMAIL" ]; then
            certbot_args+=(-m "$ADMIN_EMAIL")
        else
            certbot_args+=(--register-unsafely-without-email)
        fi
        info "申请 HTTPS 证书 (Let's Encrypt)..."
        if certbot "${certbot_args[@]}" >/dev/null 2>&1; then
            log "HTTPS 证书已配置并启用自动续期"
        else
            warn "证书申请失败，请检查域名 DNS 解析"
        fi
    fi
}

# ---------- 自检 ----------
verify() {
    info "=== 自检 ==="
    command -v curl >/dev/null 2>&1 || { warn "无 curl，跳过自检"; return; }
    local tries=0
    until curl -fsS "http://127.0.0.1:$APP_PORT/health" >/dev/null 2>&1; do
        tries=$((tries+1))
        [ "$tries" -gt 15 ] && { warn "健康检查超时，请执行: bash install.sh --logs"; return; }
        sleep 1
    done
    log "健康检查通过"

    echo "installed_at=$(date -Is)" > "$INSTALL_MARK"
    echo "app_domain=$APP_DOMAIN" >> "$INSTALL_MARK"
    echo "app_port=$APP_PORT" >> "$INSTALL_MARK"

    echo ""
    info "================ 部署完成 ================"
    if [ -n "$APP_DOMAIN" ]; then
        log "访问地址: https://$APP_DOMAIN"
    else
        local ip
        ip=$(hostname -I 2>/dev/null | awk '{print $1}')
        log "访问地址: http://$ip:$APP_PORT (未配置域名/HTTPS)"
    fi
    log "管理命令:  bash install.sh --status|--start|--stop|--restart|--logs"
    echo "==========================================="
}

# ---------- 主流程 ----------
main() {
    if [ -f "$INSTALL_MARK" ]; then
        warn "检测到已安装记录"
        if [ -n "${FS_NONINTERACTIVE:-}" ]; then
            info "非交互模式，自动重新部署（覆盖配置并重启，数据保留）"
        else
            read -r -p "已安装过，是否重新部署(会覆盖配置并重启，数据保留)? [y/N]: " ans
            [[ "${ans,,}" == "y" ]] || exit 0
        fi
    fi

    precheck
    prompt_config
    install_deps
    prepare_templates
    acquire_binary
    render_config
    setup_user
    start_service
    verify
}

# ---------- 参数解析 ----------
ACTION="install"
while [ $# -gt 0 ]; do
    case "$1" in
        --binary)  BINARY_SRC="$2"; shift 2 ;;
        --release) BINARY_SRC="$2"; shift 2 ;;
        --status)  ACTION="status"; shift ;;
        --start)   ACTION="start"; shift ;;
        --stop)    ACTION="stop"; shift ;;
        --restart) ACTION="restart"; shift ;;
        --logs)    ACTION="logs"; shift ;;
        --uninstall) ACTION="uninstall"; shift ;;
        --help|-h) ACTION="help"; shift ;;
        *) shift ;;
    esac
done

case "$ACTION" in
    status)    do_status ;;
    start)     do_start ;;
    stop)      do_stop ;;
    restart)   do_restart ;;
    logs)      do_logs ;;
    uninstall) uninstall ;;
    help)      show_help ;;
    install)   main ;;
esac
