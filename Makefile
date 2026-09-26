# AdoboFlix — IPTV Player with VOD & EPG
# Before running: cp .env.example .env and fill in your database URL.

.PHONY: build run dev clean deps verify-playback

build:
	go build -o adoboflix ./cmd/server

run: build
	./adoboflix

dev:
	@echo "Starting backend (requires ADOBOFLIX_PG_URL in .env)..."
	go run ./cmd/server &
	@echo "Starting frontend dev server..."
	cd client && npm run dev

# Samples all four content shapes, resolves them through the running server's
# real /api/v1/resolve -> /api/v1/proxy path, and verifies manifests + segments.
# Start the server first (make run). Extra flags: make verify-playback VERIFY_FLAGS="-n 10"
verify-playback:
	go build -o verifyplayback ./cmd/verifyplayback
	./verifyplayback $(VERIFY_FLAGS)

clean:
	rm -f adoboflix
	rm -f verifyplayback
	rm -rf client/dist
	rm -rf static

deps:
	cd client && npm install
