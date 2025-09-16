FROM --platform=$BUILDPLATFORM node:16 AS builder

WORKDIR /web
COPY ./VERSION .
COPY ./web .

# 1) 允许旧式 peer 解析，避免 ERESOLVE
ENV NPM_CONFIG_LEGACY_PEER_DEPS=true

# 2) 在 default 主题内就地修正 ajv 依赖组合（不会改 package.json）
RUN npm install --no-save --prefix /web/default ajv@^6 ajv-keywords@^3 schema-utils@^3

# 3) 安装依赖 & 构建（同步执行，确保先成功再复制产物）
RUN npm install --prefix /web/default && \
    DISABLE_ESLINT_PLUGIN='true' REACT_APP_VERSION="$(cat /web/VERSION)" npm run build --prefix /web/default && \
    mkdir -p /web/build && \
    cp -r /web/build/default/. /web/build/




FROM golang:alpine AS builder2

RUN apk add --no-cache \
    gcc \
    musl-dev \
    sqlite-dev \
    build-base

ENV GO111MODULE=on \
    CGO_ENABLED=1 \
    GOOS=linux

WORKDIR /build

ADD go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=builder /web/build ./web/build

RUN go build -trimpath -ldflags "-s -w -X 'github.com/songquanpeng/one-api/common.Version=$(cat VERSION)' -linkmode external -extldflags '-static'" -o one-api

FROM alpine:latest

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder2 /build/one-api /

EXPOSE 3000
WORKDIR /data
ENTRYPOINT ["/one-api"]