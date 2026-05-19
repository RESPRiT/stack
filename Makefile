.PHONY: build install clean

build:
	go build -o bin/stack .

install:
	go install .

clean:
	rm -rf bin
