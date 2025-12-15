# Go-Swap

**零停机热重载工具** - 为 Go HTTP 服务提供无缝的开发体验。

![Go Version](https://img.shields.io/badge/Go-1.21+-blue.svg)
![License](https://img.shields.io/badge/License-MIT-green.svg)

## ✨ 特性

- 🔥 **零停机热重载** - 编译期间老服务继续运行，无请求丢失
- 👀 **文件监听** - 自动检测代码变更并重新编译
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

[proxy]
port = 8080

[watch]
include = ["./"]
exclude = ["vendor", ".git", "tmp"]
extensions = ["go", "html", "tmpl"]

[log]
level = "info"
color = true
```

### 3. 修改你的 Gin 项目

确保你的应用从 `ADDR` 环境变量读取地址（推荐），或从 `PORT` 读取端口：

```go
package main

import (
    "os"
    "github.com/gin-gonic/gin"
)

func main() {
    // 使用 ADDR 环境变量 (推荐，避免 Windows 防火墙弹窗)
    addr := os.Getenv("ADDR")
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

现在访问 `http://localhost:8080`，修改代码后会自动重新编译并无缝切换！

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

1. **文件变更** → 文件监听器检测到变更
2. **后台编译** → 老服务继续运行，编译新版本
3. **启动新服务** → 新服务在新端口启动并健康检查
4. **无缝切换** → 代理切换到新服务
5. **优雅关闭** → 老服务完成剩余请求后关闭

## 📁 命令行参数

| 参数 | 说明 | 默认值 |
|------|------|--------|
| `-c` | 配置文件路径 | `ginswap.toml` |
| `-v` | 显示版本 | - |
| `-init` | 创建默认配置文件 | - |

## 📄 License

MIT License
