FROM golang:1.26-alpine AS builder

WORKDIR /src

RUN apk add --no-cache ca-certificates tzdata

ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN GOMAXPROCS=1 CGO_ENABLED=0 GOOS=linux go build -p 1 -o /out/flowspace-server ./cmd/server
RUN GOMAXPROCS=1 CGO_ENABLED=0 GOOS=linux go build -p 1 -o /out/flowspace-admin ./cmd/flowspace-admin

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app/backend

ENV FLOWSPACE_ENV=prod
ENV PORT=4201
ENV TZ=Asia/Shanghai

COPY --from=builder /out/flowspace-server /usr/local/bin/flowspace-server
COPY --from=builder /out/flowspace-admin /usr/local/bin/flowspace-admin
COPY db ./db

EXPOSE 4201

CMD ["flowspace-server"]
