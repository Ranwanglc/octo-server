# DMWork

<p align="center">
<img align="center" width="150px" src="./docs/logo.svg">
</p>

<p align="center">
企业级即时通讯平台，基于 <a href="https://github.com/WuKongIM/WuKongIM">WuKongIM</a> 通讯引擎构建
</p>

<div align=center>

![Go](https://img.shields.io/badge/Go-1.20+-00ADD8?logo=go&logoColor=white)
![License: Apache 2.0](https://img.shields.io/github/license/WuKongIM/WuKongIM)

</div>

## 简介

DMWork 是一款运营级别的即时通讯平台，支持多端（iOS、Android、Web、PC），提供完整的 IM 能力和 AI Bot 生态。

## 架构

系统分为两层：

- **通讯层**（WuKongIM）：长连接维护、消息投递、高效存储
- **业务层**（DMWork）：好友关系、群组管理、Bot 系统、文件服务等

```
客户端 ←→ WuKongIM（WebSocket/TCP）←→ DMWork（HTTP/gRPC）
                                         ↓
                              MySQL / Redis / MinIO
```

## 快速开始

### 环境要求

- Go >= 1.20
- Docker & Docker Compose

### Docker Compose 一键部署

```bash
cd docker/tsdd
cp .env.example .env  # 修改配置
docker compose up -d
```

服务端口：
- **Web UI**: 82
- **API**: 8090
- **WuKongIM TCP**: 5100
- **WuKongIM WS**: 5200

### 本地开发

```bash
go mod download
go run main.go --config configs/tsdd.yaml
```

## 核心功能

- **消息**：文本/图片/语音/视频/文件，消息撤回、转发、收藏、搜索
- **群组**：无限人数群聊，群公告、群管理、@消息
- **好友**：添加/删除好友，备注、拉黑
- **多端同步**：App、Web、PC 消息实时同步
- **Bot 系统**：BotFather 创建管理 Bot，AI Agent 接入
- **文件存储**：MinIO / 阿里云 OSS / 七牛云 / SeaweedFS
- **推送**：APNs / Firebase / 华为 / 小米 / Vivo / Oppo

## 项目结构

```
dmworkim/
├── main.go              # 入口
├── modules/             # 业务模块
│   ├── user/            # 用户管理
│   ├── message/         # 消息
│   ├── group/           # 群组
│   ├── botfather/       # Bot 管理
│   ├── robot/           # Bot 运行时
│   ├── webhook/         # 推送服务
│   ├── file/            # 文件服务
│   └── ...
├── pkg/                 # 工具包
├── adapters/            # AI Agent 适配器
├── configs/             # 配置文件
└── docker/              # Docker 部署
```

## 相关项目

| 项目 | 说明 |
|------|------|
| [dmwork-web](https://github.com/Mininglamp-OSS/octo-web) | Web/PC 客户端 |
| [dmwork-adapters](https://github.com/Mininglamp-OSS/octo-adapters) | AI Agent 适配器（OpenClaw / Claude Code） |
| [WuKongIM](https://github.com/WuKongIM/WuKongIM) | 通讯引擎 |

## License

Apache 2.0
