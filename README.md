# Go-Swap

**零停机热重载工具** - 为 Go HTTP 服务提供无缝的开发体验。

![Go Version](https://img.shields.io/badge/Go-1.21+-blue.svg)
![License](https://img.shields.io/badge/License-MIT-green.svg)

## ✨ 特性

- 🔥 **零停机热重载** - 编译期间老服务继续运行，无请求丢失
- ⌨️ **手动控制** - 通过命令行指令控制重新编译，精确掌控重载时机
- 🔄 **无缝切换** - 新服务就绪后自动替换老服务
- 🎨 **彩色日志** - 清晰的状态输出
- ⚙️ **灵活配置** - TOML 格式配置文件
- 🌐 **框架无关** - 支持 Gin、Echo、Fiber、Chi 等任何 Go HTTP 服务

## 📦 安装

```bash
go install github.com/u-nine/goswap/cmd/goswap@latest
```

或者克隆并编译：

```bash
git clone https://github.com/u-nine/goswap.git
cd goswap
go build -o goswap.exe ./cmd/goswap
```

## 🚀 快速开始

### 1. 初始化配置文件

```bash
goswap -init
```

这会在当前目录创建 `goswap.toml` 配置文件。

### 2. 配置你的项目

编辑 `goswap.toml`：

```toml
root = "."

[build]
cmd = "go build -o ./tmp/main.exe ."
bin = "./tmp/main.exe"
delay = 500

[process]
start_port = 56700

[proxy]
port = 8080

[watch]
# 注意：当前版本不再使用文件监听，这些配置保留用于未来扩展
include = ["./"]
exclude = ["vendor", ".git", "tmp"]
extensions = ["go", "html", "tmpl"]

[log]
level = "info"
color = true
```

### 3. 修改你的 Gin 项目

确保你的应用从 `GOSWAP_ADDR` 环境变量读取地址（推荐），或从 `GOSWAP_PORT` 读取端口：

```go
package main

import (
    "os"
    "github.com/gin-gonic/gin"
)

func main() {
    // 使用 GOSWAP_ADDR 环境变量 (推荐，避免 Windows 防火墙弹窗)
    addr := os.Getenv("GOSWAP_ADDR")
    if addr == "" {
        addr = "127.0.0.1:8080"
    }
    
    r := gin.Default()
    r.GET("/", func(c *gin.Context) {
        c.JSON(200, gin.H{"message": "Hello!"})
    })
    r.Run(addr)
}
```

### 4. 启动 goswap

```bash
goswap
```

现在访问 `http://localhost:8080`，程序启动后会显示可用命令。

### 5. 手动触发重新编译

修改代码后，在 goswap 运行的终端中输入以下命令来触发重新编译：

- `rebuild` 或 `r` - 重新编译并热重载应用
- `quit` 或 `q` - 退出 goswap

示例：

```bash
$ goswap
[15:04:05] [SWAP] Starting go-swap...
[15:04:05] [SWAP] Performing initial build...
[15:04:06] [SWAP] go-swap is running!
[15:04:06] [SWAP] Listening on http://localhost:8080
[15:04:06] [SWAP] Commands:
[15:04:06] [SWAP]   rebuild, r - Rebuild and reload the application
[15:04:06] [SWAP]   quit, q   - Stop go-swap
[15:04:06] [SWAP] Press Ctrl+C to stop

# 修改代码后，输入以下命令触发重新编译：
rebuild
[15:05:10] [SWAP] Manual rebuild triggered
[15:05:11] [SWAP] Hot reload completed!
```

## 🔧 工作原理

```
+------------------+
|   前端客户端      |
+--------+---------+
         |
         v
+--------+---------+
|  反向代理 :8080   |  <-- 固定端口，对外提供服务
+--------+---------+
         |
         v
+--------+---------+
|   Go 应用        |  <-- 动态端口，由 goswap 管理
+------------------+
```

1. **用户输入命令** → 在终端输入 `rebuild` 或 `r` 触发重新编译
2. **后台编译** → 老服务继续运行，编译新版本
3. **启动新服务** → 新服务在新端口启动并健康检查
4. **无缝切换** → 代理切换到新服务
5. **优雅关闭** → 老服务完成剩余请求后关闭

## 🔌 接入现有项目

要将 goswap 接入现有项目，**必须让应用支持动态端口**。goswap 会通过环境变量告诉应用应该监听哪个端口。

### ⚠️ 核心原则

| 组件 | 端口 | 说明 |
|------|------|------|
| goswap 代理 | 固定端口（如 8080） | 对外提供服务，前端访问这个端口 |
| 你的应用 | 动态端口（56700+） | 由 goswap 分配，应用必须读取环境变量 |

**重要**：goswap 代理的端口不能与应用原本配置的端口相同，否则会端口冲突！

### 环境变量说明

goswap 启动应用时会设置以下环境变量：

| 环境变量 | 示例值 | 说明 |
|----------|--------|------|
| `GOSWAP_ADDR` | `127.0.0.1:56701` | 完整监听地址（推荐使用） |
| `GOSWAP_PORT` | `56701` | 端口号 |
| `GOSWAP_HOST` | `127.0.0.1` | 主机地址 |

### 各框架适配示例

#### Gin 框架

```go
package main

import (
    "os"
    "github.com/gin-gonic/gin"
)

func main() {
    // 从环境变量读取地址
    addr := os.Getenv("GOSWAP_ADDR")
    if addr == "" {
        // 没有 goswap 时使用默认端口
        port := os.Getenv("GOSWAP_PORT")
        if port == "" {
            port = "8888"  // 你的项目默认端口
        }
        addr = "127.0.0.1:" + port
    }
    
    r := gin.Default()
    r.GET("/", func(c *gin.Context) {
        c.JSON(200, gin.H{"message": "Hello!"})
    })
    r.Run(addr)  // 使用动态地址
}
```

#### Echo 框架

```go
package main

import (
    "os"
    "github.com/labstack/echo/v4"
)

func main() {
    addr := os.Getenv("GOSWAP_ADDR")
    if addr == "" {
        port := os.Getenv("GOSWAP_PORT")
        if port == "" {
            port = "8888"
        }
        addr = ":" + port
    }

    e := echo.New()
    e.GET("/", func(c echo.Context) error {
        return c.JSON(200, map[string]string{"message": "Hello!"})
    })
    e.Logger.Fatal(e.Start(addr))
}
```

#### Fiber 框架

```go
package main

import (
    "os"
    "github.com/gofiber/fiber/v2"
)

func main() {
    addr := os.Getenv("GOSWAP_ADDR")
    if addr == "" {
        port := os.Getenv("GOSWAP_PORT")
        if port == "" {
            port = "8888"
        }
        addr = ":" + port
    }

    app := fiber.New()
    app.Get("/", func(c *fiber.Ctx) error {
        return c.JSON(fiber.Map{"message": "Hello!"})
    })
    app.Listen(addr)
}
```

#### 标准库 net/http

```go
package main

import (
    "net/http"
    "os"
)

func main() {
    addr := os.Getenv("GOSWAP_ADDR")
    if addr == "" {
        port := os.Getenv("GOSWAP_PORT")
        if port == "" {
            port = "8888"
        }
        addr = ":" + port
    }

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Write([]byte("Hello!"))
    })
    http.ListenAndServe(addr, nil)
}
```

### gin-vue-admin 适配示例

对于 gin-vue-admin 这类项目，需要修改 `server/core/server.go` 或类似的启动文件：

```go
// 在 RunWindowsServer() 或 Run() 函数中

// 原代码可能是这样：
// address := fmt.Sprintf(":%d", global.GVA_CONFIG.System.Addr)

// 修改为：
address := os.Getenv("GOSWAP_ADDR")
if address == "" {
    address = fmt.Sprintf(":%d", global.GVA_CONFIG.System.Addr)
}

s := initServer(address, Router)
```

### 配置文件示例

```toml
# goswap.toml
root = "."

[build]
# 根据你的项目调整
cmd = "go build -o ./tmp/server.exe ./server"
bin = "./tmp/server.exe"
delay = 500

[process]
start_port = 56700

[proxy]
# 代理端口（前端访问这个端口）
# 注意：不要与应用原配置端口相同！
port = 8080

[watch]
# 注意：当前版本不再使用文件监听，这些配置保留用于未来扩展
include = ["./"]
exclude = ["vendor", ".git", "tmp", "node_modules", "web"]
extensions = ["go", "html", "tmpl", "yaml", "yml"]

[log]
level = "info"
color = true
```

### 接入检查清单

- [ ] 应用代码已修改，支持读取 `GOSWAP_ADDR` 或 `GOSWAP_PORT` 环境变量
- [ ] goswap 代理端口与应用原端口不冲突
- [ ] 前端 API 请求地址改为 goswap 代理端口
- [ ] `goswap.toml` 中的 `build.cmd` 正确配置了编译命令

## 📁 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-c` | 配置文件路径 | `goswap.toml` |
| `-v` | 显示版本 | - |
| `-init` | 创建默认配置文件 | - |

## ⌨️ 交互式命令

goswap 启动后，支持以下交互式命令：

| 命令 | 简写 | 说明 |
|------|------|------|
| `rebuild` | `r` | 触发重新编译并热重载应用 |
| `quit` | `q` | 退出 goswap 程序 |

**使用示例**：

```bash
# 启动 goswap
$ goswap

# 修改代码后，在终端输入：
rebuild
# 或简写：
r

# 退出程序：
quit
# 或简写：
q
```

**提示**：也可以使用 `Ctrl+C` 来退出程序。

## ❓ 常见问题

### 端口冲突错误

```
listen tcp :8888: bind: Only one usage of each socket address...
```

**原因**：goswap 代理端口与应用配置的端口相同。

**解决**：修改应用代码读取 `GOSWAP_ADDR` 环境变量，或更改 goswap 代理端口。

### 健康检查超时

goswap 默认会检查应用是否正常启动。如果应用没有 `/` 路径或启动较慢，会显示警告但不会阻止运行。这是正常的，热重载功能仍然可用。

### Windows 防火墙弹窗

使用 `127.0.0.1` 作为监听地址（而不是 `0.0.0.0` 或 `:`）可以避免 Windows 防火墙弹窗。goswap 会自动设置 `GOSWAP_HOST=127.0.0.1`。

### 如何触发重新编译？

goswap 不再自动检测文件变更。修改代码后，需要在 goswap 运行的终端中输入 `rebuild` 或 `r` 命令来触发重新编译。这样可以让你精确控制重载时机，避免频繁的自动编译。

## 📄 License

MIT License
