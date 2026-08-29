# 随译 SuiYi 流水线专用构建环境：在官方 Go 镜像基础上预装 zip，
# 避免每次流水线执行时重复 apt-get install（CNB 最佳实践）。
FROM golang:1.25
RUN apt-get update && apt-get install -y --no-install-recommends zip \
    && rm -rf /var/lib/apt/lists/*
