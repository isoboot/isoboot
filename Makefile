.PHONY: build test clean

build:
	go build ./...

test:
	go test ./...

clean:
	rm -f bin/*
