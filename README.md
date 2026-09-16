# raincut — 暴雨管网最小关管成本

暴雨污染从供水入口（sources）沿管网流向取水口（sinks）。每根管道有一个关闭
成本，本服务计算：**阻断所有 source → sink 有向路径所需的最小关管总成本**，
即有向图的最小 s-t 割。

## 算法

- 自建**超级源**与**超级汇**：超级源以容量 `总边费 + 1` 连向每个 source，
  每个 sink 以同样容量连向超级汇。因为任何只含原始边的割至多为总边费，
  最优割绝不会切断超级边，故最大流的值恰等于最小关管成本
  （最大流最小割定理）。
- 最大流使用**自实现的 Dinic 算法**（BFS 分层图 + 当前弧优化的 DFS 增广），
  容量全程使用 64 位整数。无外部求解器，不枚举割集。
- 平行边各自独立计费；自环永远不会跨越任何割，自然不影响结果；
  原本无通路时最大流为 0，返回 0。

## 快速开始（Docker Compose）

```bash
# 启动 API（默认宿主端口 8080，可用 API_PORT 覆盖）
API_PORT=9000 docker compose up --build api

# 一次性验收：先跑 Go 测试，再对真实 API 跑全部黑盒检查
docker compose up --build --exit-code-from verify
docker compose down
```

`verify` 服务的退出码即验收结果（0 = 全部通过）。

## API

### `POST /mincut`

请求体（仅普通 JSON 基础类型，数值必须为整数）：

| 字段      | 类型    | 约束                                             |
| --------- | ------- | ------------------------------------------------ |
| `n`       | integer | 节点数，编号 `0..n-1`，`2 ≤ n ≤ 20000`           |
| `edges`   | array   | 至多 100000 条；`from`/`to` 为合法节点，`1 ≤ cost ≤ 10^9` |
| `sources` | array   | 非空节点数组，与 `sinks` 互斥                     |
| `sinks`   | array   | 非空节点数组，与 `sources` 互斥                    |

成功响应 `200`：

```json
{"minimum_shutdown_cost": 13}
```

请求示例：

```bash
curl -s -X POST http://localhost:${API_PORT:-8080}/mincut \
  -H 'Content-Type: application/json' \
  -d '{
        "n": 4,
        "edges": [
          {"from": 0, "to": 1, "cost": 5},
          {"from": 0, "to": 2, "cost": 8},
          {"from": 1, "to": 3, "cost": 5},
          {"from": 2, "to": 3, "cost": 8}
        ],
        "sources": [0],
        "sinks": [3]
      }'
# => {"minimum_shutdown_cost":13}
```

### 错误

任何非法输入（越界、非法引用、空端点组、端点重叠、非整数数值、畸形
JSON 等）都不会进入求解，统一返回 `422` 与稳定错误结构：

```bash
curl -s -X POST http://localhost:8080/mincut \
  -d '{"n":1,"edges":[],"sources":[0],"sinks":[1]}'
# HTTP 422
# {"error":{"code":"invalid_input","message":"n must satisfy 2 <= n <= 20000, got 1"}}
```

### `GET /healthz`

健康检查，返回 `{"status":"ok"}`。

## 验收内容（verify 服务）

`cmd/verify` 是黑盒验收客户端，只调用真实 HTTP 接口（无假接口、无固定
结果），检查：

- 平行边各自计费、自环不影响结果、有向性、多源多汇等**精确代价**；
- 原本无通路时返回 0；
- 各类非法图返回 `422` 且错误结构稳定（`error.code` / `error.message`）；
- **20000 节点 / 100000 边**的大图在 **10 秒请求超时**内返回精确值。

verify 容器启动时先执行 `go test ./...`（含对小图与暴力枚举割的对拍、
以及同规模大图的单元测试），全部通过后再发起 HTTP 检查。

## 本地开发

```bash
go test ./...        # 单元测试
go run ./cmd/api     # 本地启动（PORT 环境变量，默认 8080）
API_BASE_URL=http://localhost:8080 go run ./cmd/verify   # 对本地实例验收
```

## 项目结构

```
cmd/api/       HTTP 服务入口
cmd/verify/    一次性验收客户端
internal/api/  路由、请求校验、错误结构
internal/flow/ 自实现 Dinic 最大流 / 最小割求解器
Dockerfile     多阶段构建（api 运行镜像 + verify 验收镜像）
docker-compose.yml  api 与 verify 服务编排（API_PORT 控制宿主端口）
```
