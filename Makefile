APP := myrient-cli
CMD := ./cmd/myrient-cli

.PHONY: build install test clean

build:
	go build -o $(APP) $(CMD)

install:
	go install $(CMD)

test:
	go test ./...

clean:
	rm -f $(APP)
