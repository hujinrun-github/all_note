PORT ?= 8080
FRONTEND_PORT ?= 5199

.PHONY: kill dev start-backend start-frontend

# === 一键启动（所有服务 nohup 后台运行） ===
dev: kill
	@echo "=== 编译后端 ==="
	cd backend && go build -o server ./cmd/server
	@echo "=== 后台启动后端 (端口 $(PORT)) ==="
	@(cd backend && PORT=$(PORT) nohup ./server </dev/null >/tmp/flowspace-backend.log 2>&1) &
	@sleep 1
	@echo "  后端 PID: $$(lsof -ti:$(PORT))"
	@echo "=== 后台启动前端 (端口 $(FRONTEND_PORT)) ==="
	@(cd frontend && VITE_BACKEND_PORT=$(PORT) nohup npx vite --port $(FRONTEND_PORT) --host 127.0.0.1 </dev/null >/tmp/flowspace-frontend.log 2>&1) &
	@sleep 3
	@echo "  前端 PID: $$(lsof -ti:$(FRONTEND_PORT))"
	@echo ""
	@echo "=== 服务已后台运行 ==="
	@echo "后端: http://localhost:$(PORT)    日志: /tmp/flowspace-backend.log"
	@echo "前端: http://localhost:$(FRONTEND_PORT)   日志: /tmp/flowspace-frontend.log"

# === 杀掉旧进程 ===
kill:
	@echo "=== 清理旧进程 ==="
	@lsof -ti:$(PORT) | xargs kill -9 2>/dev/null || true
	@lsof -ti:$(FRONTEND_PORT) | xargs kill -9 2>/dev/null || true
	@sleep 1
	@echo "端口 $(PORT) / $(FRONTEND_PORT) 已释放"

# === 单独启动后端 ===
start-backend: kill
	cd backend && go build -o server ./cmd/server
	@(cd backend && PORT=$(PORT) nohup ./server </dev/null >/tmp/flowspace-backend.log 2>&1) &
	@sleep 1
	@echo "后端已后台启动 (PID $$(lsof -ti:$(PORT)))"

# === 单独启动前端 ===
start-frontend:
	@(cd frontend && VITE_BACKEND_PORT=$(PORT) nohup npx vite --port $(FRONTEND_PORT) --host 127.0.0.1 </dev/null >/tmp/flowspace-frontend.log 2>&1) &
	@sleep 3
	@echo "前端已后台启动 (PID $$(lsof -ti:$(FRONTEND_PORT)))"
