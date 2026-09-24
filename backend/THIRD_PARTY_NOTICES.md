# 第三方组件与许可证

本文件记录随后端发布的第三方组件、固定版本与许可证。版本以 `backend/go.mod` 为准，升级依赖时同步更新本表并复核许可证与漏洞信息。

## Go 模块

| 组件 | 版本 | 许可证 |
| --- | --- | --- |
| [CloudWeGo Hertz](https://github.com/cloudwego/hertz) | v0.10.6 | Apache-2.0 |
| [CloudWeGo Eino](https://github.com/cloudwego/eino) | v0.7.13 | Apache-2.0 |
| [Eino OpenAI-compatible ChatModel](https://github.com/cloudwego/eino-ext/tree/main/components/model/openai) | v0.1.13 | Apache-2.0 |
| [Eino OpenAI-compatible Embedding](https://github.com/cloudwego/eino-ext/tree/main/components/embedding/openai) | v0.0.0-20260923073216-8dee1c82d215 | Apache-2.0 |
| [golang-jwt/jwt](https://github.com/golang-jwt/jwt) | v5.3.1 | MIT |
| [golang-migrate](https://github.com/golang-migrate/migrate) | v4.19.1 | MIT |
| [pgx](https://github.com/jackc/pgx) | v5.9.2 | MIT |
| [bluemonday](https://github.com/microcosm-cc/bluemonday) | v1.0.27 | BSD-3-Clause |
| [gofeed](https://github.com/mmcdole/gofeed) | v1.4.2 | MIT |
| [golang.org/x/crypto](https://pkg.go.dev/golang.org/x/crypto) | v0.56.0 | BSD-3-Clause |
| [golang.org/x/net](https://pkg.go.dev/golang.org/x/net) | v0.57.0 | BSD-3-Clause |
| [golang.org/x/term](https://pkg.go.dev/golang.org/x/term) | v0.46.0 | BSD-3-Clause |
| [gopkg.in/yaml.v3](https://pkg.go.dev/gopkg.in/yaml.v3) | v3.0.1 | MIT 与 Apache-2.0 |

认证相关组件的用途：`golang-jwt/jwt` 负责访问令牌的 HS256 签发与校验，`golang.org/x/crypto` 提供 Argon2id 密码散列，`golang.org/x/term` 负责 CLI 的隐藏密码输入。三者都在 `go.mod` 中固定版本，升级前需复核模块兼容性与已知漏洞。

AI 内容增强使用 Eino v0.7.13 编译有界 Workflow，并分别通过两个 OpenAI-compatible 扩展组件调用 Chat Completions 与 Embeddings。三个模块均为 Apache-2.0；Embedding 当前固定到未发布 tag 的完整伪版本，升级时必须重新验证 API 兼容性和许可证。

## 随应用发布的数据文件

| 文件 | 来源 | 许可证 |
| --- | --- | --- |
| `internal/infrastructure/security/data/10k-most-common.txt` | SecLists `Passwords/Common-Credentials/10k-most-common.txt`，固定提交 `d9458f277ed978a608ad165e5eb2fdb389e4c7ee` | MIT |

该文件的来源、提交、SHA-256 与许可声明同时记录在 `internal/infrastructure/security/data/NOTICE.md`；运行时不访问外部服务。

## 前端依赖

Web 端依赖与其版本见 `web/package.json`（Vue、Vue Router、Pinia、Vite、Vitest 等，均为 MIT）。
