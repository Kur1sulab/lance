# Lance 运行镜像：把预先编好的二进制拷进来，Node 只做 esbuild 打包。
FROM node:22-slim AS ui
WORKDIR /ui
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
# 先把对应架构的二进制放到 dist/<arch>/lance，再构建镜像
COPY dist/${TARGETARCH}/lance /app/lance
COPY start.sh /app/start.sh
RUN chmod +x /app/lance /app/start.sh
ENV LANCE_ADDR=0.0.0.0:8787
EXPOSE 8787 8788
# start.sh 负责把 SIGTERM 转给子进程，docker stop 才能优雅退出
ENTRYPOINT ["/app/start.sh"]
