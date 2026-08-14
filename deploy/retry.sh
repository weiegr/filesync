#!/usr/bin/env bash
# 带重试的 ssh 执行脚本 (在服务器上运行)
# 用法: ./retry.sh <command...>
set -e
for i in $(seq 1 8); do
    "$@" && exit 0
    echo "[retry] attempt $i failed, retrying in 4s..." >&2
    sleep 4
done
echo "[retry] all attempts failed" >&2
exit 1
