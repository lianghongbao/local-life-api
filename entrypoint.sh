#!/bin/sh
# local-life-api 容器启动脚本

set -e

# named volume 挂载点由 docker 以 root 创建,容器内 app 用户需先 chown 才能写日志
mkdir -p /data/logs/mini-api
chown -R app:app /data/logs/mini-api

# 切到 app 用户启动主进程(PID 1)
exec su -s /bin/sh -c '/app/local-life-api "$@"' app -- "$@"
