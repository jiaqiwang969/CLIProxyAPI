.PHONY: help build run stop clean install-deps init-config generate-key setup deploy logs

# 变量定义
BINARY_NAME=cli-proxy-api
MAIN_PATH=./cmd/server/main.go
CONFIG_FILE=config.yaml
PORT=8317
LOG_DIR=logs
DATA_DIR=data

# 颜色定义
RED=\033[0;31m
GREEN=\033[0;32m
YELLOW=\033[0;33m
BLUE=\033[0;34m
NC=\033[0m # No Color

help:
	@echo "$(BLUE)╔════════════════════════════════════════════════════════════╗$(NC)"
	@echo "$(BLUE)║         CLIProxyAPI - Token 看板部署工具                  ║$(NC)"
	@echo "$(BLUE)╚════════════════════════════════════════════════════════════╝$(NC)"
	@echo ""
	@echo "$(GREEN)可用命令:$(NC)"
	@echo "  $(YELLOW)make setup$(NC)              - 一键部署（推荐）"
	@echo "  $(YELLOW)make build$(NC)              - 编译代码"
	@echo "  $(YELLOW)make run$(NC)                - 启动服务"
	@echo "  $(YELLOW)make stop$(NC)               - 停止服务"
	@echo "  $(YELLOW)make clean$(NC)              - 清理编译文件"
	@echo "  $(YELLOW)make install-deps$(NC)       - 安装依赖"
	@echo "  $(YELLOW)make init-config$(NC)        - 初始化配置文件"
	@echo "  $(YELLOW)make generate-key$(NC)       - 生成 API 密钥"
	@echo "  $(YELLOW)make logs$(NC)               - 查看日志"
	@echo "  $(YELLOW)make test$(NC)               - 运行测试"
	@echo ""
	@echo "$(GREEN)快速开始:$(NC)"
	@echo "  1. 运行: $(YELLOW)make setup$(NC)"
	@echo "  2. 访问: $(YELLOW)http://localhost:$(PORT)/console$(NC)"
	@echo "  3. 申请密钥: $(YELLOW)make generate-key$(NC)"
	@echo ""

# 一键部署
setup: install-deps init-config build generate-key
	@echo "$(GREEN)✓ 部署完成！$(NC)"
	@echo ""
	@echo "$(BLUE)下一步:$(NC)"
	@echo "  1. 启动服务: $(YELLOW)make run$(NC)"
	@echo "  2. 访问看板: $(YELLOW)http://localhost:$(PORT)/console$(NC)"
	@echo ""

# 安装依赖
install-deps:
	@echo "$(BLUE)正在安装依赖...$(NC)"
	@if ! command -v go &> /dev/null; then \
		echo "$(RED)✗ Go 未安装，请先安装 Go 1.21+$(NC)"; \
		exit 1; \
	fi
	@go mod download
	@go mod tidy
	@echo "$(GREEN)✓ 依赖安装完成$(NC)"

# 初始化配置文件
init-config:
	@echo "$(BLUE)正在初始化配置文件...$(NC)"
	@mkdir -p $(LOG_DIR) $(DATA_DIR)
	@if [ ! -f $(CONFIG_FILE) ]; then \
		echo "$(YELLOW)创建配置文件: $(CONFIG_FILE)$(NC)"; \
		cp config.yaml.example $(CONFIG_FILE); \
		echo "$(GREEN)✓ 配置文件创建完成$(NC)"; \
	else \
		echo "$(YELLOW)配置文件已存在，跳过创建$(NC)"; \
	fi

# 编译代码
build:
	@echo "$(BLUE)正在编译代码...$(NC)"
	@go build -o $(BINARY_NAME) $(MAIN_PATH)
	@echo "$(GREEN)✓ 编译完成: $(BINARY_NAME)$(NC)"

# 启动服务
run: build
	@echo "$(BLUE)正在启动服务...$(NC)"
	@echo "$(YELLOW)服务地址: http://localhost:$(PORT)$(NC)"
	@echo "$(YELLOW)Token 看板: http://localhost:$(PORT)/console$(NC)"
	@echo ""
	@echo "$(YELLOW)按 Ctrl+C 停止服务$(NC)"
	@echo ""
	@./$(BINARY_NAME) -config $(CONFIG_FILE)

# 后台启动服务
start: build
	@echo "$(BLUE)正在后台启动服务...$(NC)"
	@nohup ./$(BINARY_NAME) -config $(CONFIG_FILE) > $(LOG_DIR)/server.log 2>&1 &
	@echo "$(GREEN)✓ 服务已启动$(NC)"
	@echo "$(YELLOW)日志文件: $(LOG_DIR)/server.log$(NC)"
	@echo "$(YELLOW)访问地址: http://localhost:$(PORT)/console$(NC)"

# 停止服务
stop:
	@echo "$(BLUE)正在停止服务...$(NC)"
	@pkill -f "$(BINARY_NAME)" || echo "$(YELLOW)服务未运行$(NC)"
	@echo "$(GREEN)✓ 服务已停止$(NC)"

# 生成 API 密钥
generate-key:
	@echo "$(BLUE)╔════════════════════════════════════════════════════════════╗$(NC)"
	@echo "$(BLUE)║              API 密钥生成工具                              ║$(NC)"
	@echo "$(BLUE)╚════════════════════════════════════════════════════════════╝$(NC)"
	@echo ""
	@echo "$(YELLOW)请输入密钥名称 (默认: default):$(NC)"
	@read -p "密钥名称: " KEY_NAME; \
	if [ -z "$$KEY_NAME" ]; then KEY_NAME="default"; fi; \
	KEY_VALUE="sk-$$(date +%s)-$$(openssl rand -hex 16)"; \
	echo ""; \
	echo "$(GREEN)✓ API 密钥已生成$(NC)"; \
	echo ""; \
	echo "$(BLUE)密钥信息:$(NC)"; \
	echo "  名称: $(YELLOW)$$KEY_NAME$(NC)"; \
	echo "  密钥: $(YELLOW)$$KEY_VALUE$(NC)"; \
	echo ""; \
	echo "$(YELLOW)请妥善保管此密钥，不要泄露给他人！$(NC)"; \
	echo ""; \
	echo "$(BLUE)使用方式:$(NC)"; \
	echo "  curl -H \"Authorization: Bearer $$KEY_VALUE\" http://localhost:$(PORT)/v1/chat/completions"; \
	echo ""

# 查看日志
logs:
	@if [ -f "$(LOG_DIR)/error.log" ]; then \
		tail -f $(LOG_DIR)/error.log; \
	else \
		echo "$(YELLOW)日志文件不存在$(NC)"; \
	fi

# 运行测试
test:
	@echo "$(BLUE)正在运行测试...$(NC)"
	@go test ./... -v -cover
	@echo "$(GREEN)✓ 测试完成$(NC)"

# 清理编译文件
clean:
	@echo "$(BLUE)正在清理编译文件...$(NC)"
	@rm -f $(BINARY_NAME)
	@go clean
	@echo "$(GREEN)✓ 清理完成$(NC)"

# 完全清理（包括日志和数据）
clean-all: clean
	@echo "$(BLUE)正在完全清理...$(NC)"
	@rm -rf $(LOG_DIR) $(DATA_DIR)
	@echo "$(GREEN)✓ 完全清理完成$(NC)"

# 显示状态
status:
	@echo "$(BLUE)╔════════════════════════════════════════════════════════════╗$(NC)"
	@echo "$(BLUE)║              服务状态                                      ║$(NC)"
	@echo "$(BLUE)╚════════════════════════════════════════════════════════════╝$(NC)"
	@echo ""
	@if pgrep -f "$(BINARY_NAME)" > /dev/null; then \
		echo "$(GREEN)✓ 服务运行中$(NC)"; \
		echo "  PID: $$(pgrep -f "$(BINARY_NAME)")"; \
		echo "  地址: http://localhost:$(PORT)"; \
		echo "  看板: http://localhost:$(PORT)/console"; \
	else \
		echo "$(RED)✗ 服务未运行$(NC)"; \
	fi
	@echo ""

# 显示配置
show-config:
	@echo "$(BLUE)╔════════════════════════════════════════════════════════════╗$(NC)"
	@echo "$(BLUE)║              配置信息                                      ║$(NC)"
	@echo "$(BLUE)╚════════════════════════════════════════════════════════════╝$(NC)"
	@echo ""
	@echo "$(YELLOW)服务配置:$(NC)"
	@echo "  端口: $(PORT)"
	@echo "  主机: 0.0.0.0"
	@echo "  日志目录: $(LOG_DIR)"
	@echo "  数据目录: $(DATA_DIR)"
	@echo ""
	@echo "$(YELLOW)访问地址:$(NC)"
	@echo "  API: http://localhost:$(PORT)/v1"
	@echo "  看板: http://localhost:$(PORT)/console"
	@echo "  健康检查: http://localhost:$(PORT)/health"
	@echo ""

# 显示模型列表
show-models:
	@echo "$(BLUE)╔════════════════════════════════════════════════════════════╗$(NC)"
	@echo "$(BLUE)║              支持的模型列表                                ║$(NC)"
	@echo "$(BLUE)╚════════════════════════════════════════════════════════════╝$(NC)"
	@echo ""
	@echo "$(GREEN)Claude 模型:$(NC)"
	@echo "  • claude-opus-4-6"
	@echo "    最强大的 Claude 模型，适合复杂任务"
	@echo "    上下文: 200K tokens | 成本: $0.015/$0.075 per 1K"
	@echo ""
	@echo "  • claude-sonnet-4-6"
	@echo "    平衡性能和成本的 Claude 模型"
	@echo "    上下文: 200K tokens | 成本: $0.003/$0.015 per 1K"
	@echo ""
	@echo "  • claude-haiku-4-5-20251001"
	@echo "    快速且经济的 Claude 模型"
	@echo "    上下文: 200K tokens | 成本: $0.0008/$0.004 per 1K"
	@echo ""
	@echo "$(GREEN)Gemini 模型:$(NC)"
	@echo "  • gemini-3.1-pro-high"
	@echo "    高精度的 Gemini 模型"
	@echo "    上下文: 1M tokens | 成本: $0.0075/$0.03 per 1K"
	@echo ""
	@echo "  • gemini-3.1-pro"
	@echo "    标准的 Gemini 模型"
	@echo "    上下文: 1M tokens | 成本: $0.0075/$0.03 per 1K"
	@echo ""
	@echo "  • gemini-3.1-flash"
	@echo "    快速的 Gemini 模型"
	@echo "    上下文: 1M tokens | 成本: $0.0075/$0.03 per 1K"
	@echo ""

# 测试 API
test-api:
	@echo "$(BLUE)正在测试 API...$(NC)"
	@echo ""
	@echo "$(YELLOW)1. 测试健康检查:$(NC)"
	@curl -s http://localhost:$(PORT)/health | jq '.' || echo "$(RED)✗ 连接失败$(NC)"
	@echo ""
	@echo "$(YELLOW)2. 测试 Token 统计:$(NC)"
	@curl -s http://localhost:$(PORT)/api/console/stats | jq '.' || echo "$(RED)✗ 连接失败$(NC)"
	@echo ""
	@echo "$(YELLOW)3. 测试模型列表:$(NC)"
	@curl -s http://localhost:$(PORT)/v1/models | jq '.' || echo "$(RED)✗ 连接失败$(NC)"
	@echo ""

# 默认目标
.DEFAULT_GOAL := help
