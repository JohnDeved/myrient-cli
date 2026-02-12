APP := myrient
CMD := ./cmd/myrient

.PHONY: build install test clean

build:
	go build -o $(APP) $(CMD)

install:
	go install $(CMD)

test:
	go test ./...

clean:
	rm -f $(APP)
