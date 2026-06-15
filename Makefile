.PHONY: build run dev clean deps

build:
	go build -o adoboflix ./cmd/server

run: build
	./adoboflix

dev:
	@echo "Starting backend..."
	go run ./cmd/server &
	@echo "Starting frontend dev server..."
	cd client && npm run dev

clean:
	rm -f adoboflix
	rm -rf client/dist
	rm -rf static

deps:
	cd client && npm install
